package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"

	"aiterm/internal/config"
	"aiterm/internal/controller"
	"aiterm/internal/provider"
	"aiterm/internal/securefs"
)

type PIAgent struct {
	cfg       config.Config
	profile   provider.Profile
	selection Selection
	state     WorkspaceState
	counter   atomic.Uint64
	activity  *activityBuffer
}

type piRPCProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	stderr bytes.Buffer
	cancel context.CancelFunc
}

func NewPIAgent(cfg config.Config, profile provider.Profile, selection Selection, state WorkspaceState, activity *activityBuffer) (*PIAgent, error) {
	if selection.ID != RuntimePi {
		return nil, fmt.Errorf("selection %q is not a pi runtime", selection.ID)
	}
	if strings.TrimSpace(selection.Command) == "" {
		selection.Command = runtimeCommand("")
	}
	if _, err := lookPath(selection.Command); err != nil {
		return nil, fmt.Errorf("find pi runtime: %w", err)
	}
	if ok, detail := piProfileSupport(profile); !ok {
		return nil, errors.New(detail)
	}
	if selection.RequiresGrant && !selection.Granted {
		return nil, errors.New("pi runtime requires a workspace trust grant before activation")
	}
	return &PIAgent{
		cfg:       cfg,
		profile:   profile,
		selection: selection,
		state:     state,
		activity:  activity,
	}, nil
}

func (a *PIAgent) Respond(ctx context.Context, input controller.AgentInput) (controller.AgentResponse, error) {
	prompt, err := provider.BuildStructuredPrompt(input)
	if err != nil {
		return controller.AgentResponse{}, err
	}

	configDir, envVars, providerName, err := ensurePIConfig(a.cfg, a.profile)
	if err != nil {
		return controller.AgentResponse{}, err
	}
	sessionDir := filepath.Join(a.cfg.StateDir, "pi", "sessions", a.cfg.WorkspaceID)
	if err := securefs.EnsurePrivateDir(sessionDir); err != nil {
		return controller.AgentResponse{}, fmt.Errorf("prepare pi session dir: %w", err)
	}

	process, err := startPIProcess(ctx, a.selection.Command, sessionDir, configDir, providerName, effectivePIModel(a.profile), envVars)
	if err != nil {
		return controller.AgentResponse{}, err
	}
	defer process.close()

	switchedSession := false
	if strings.TrimSpace(a.state.PISessionFile) != "" {
		if err := process.switchSession(ctx, a.state.PISessionFile); err != nil {
			// Ignore stale resume targets and fall back to a fresh/default session.
		} else {
			switchedSession = true
		}
	}
	if shouldStartPINewSession(switchedSession, input.PreserveExternalSession, a.state.PITaskID, input.Task.TaskID) {
		if err := process.newSession(ctx); err != nil {
			return controller.AgentResponse{}, err
		}
	}

	runtimeEvents, err := process.prompt(ctx, prompt, a.publishActivity)
	if err != nil {
		return controller.AgentResponse{}, err
	}
	lastText, err := process.lastAssistantText(ctx)
	if err != nil {
		return controller.AgentResponse{}, err
	}
	response, err := provider.ParseStructuredResponseTextWithIDFactory(lastText, func(prefix string) string {
		return fmt.Sprintf("%s-%d", prefix, a.counter.Add(1))
	})
	if err != nil {
		return controller.AgentResponse{}, err
	}

	state, err := process.state(ctx)
	if err == nil {
		response.ModelInfo = &controller.AgentModelInfo{
			ProviderPreset: string(a.profile.Preset),
			RequestedModel: strings.TrimSpace(a.profile.Model),
			ResponseModel:  strings.TrimSpace(state.ModelID),
		}
		a.state.PISessionFile = strings.TrimSpace(state.SessionFile)
		a.state.PISessionID = strings.TrimSpace(state.SessionID)
		a.state.PITaskID = strings.TrimSpace(input.Task.TaskID)
		a.state.PIConfigDir = configDir
		a.state.RuntimeID = RuntimePi
		a.state.RuntimeCommand = a.selection.Command
		a.state.ProviderPreset = string(a.profile.Preset)
		a.state.ProviderModel = strings.TrimSpace(a.profile.Model)
		_ = SaveWorkspaceState(a.cfg.StateDir, a.cfg.WorkspaceID, a.state)
	}

	response.RuntimeEvents = runtimeEvents
	return response, nil
}

func (a *PIAgent) CheckHealth(ctx context.Context) error {
	configDir, envVars, providerName, err := ensurePIConfig(a.cfg, a.profile)
	if err != nil {
		return err
	}
	process, err := startPIProcess(ctx, a.selection.Command, filepath.Join(a.cfg.StateDir, "pi", "health"), configDir, providerName, effectivePIModel(a.profile), envVars)
	if err != nil {
		return err
	}
	defer process.close()
	_, err = process.state(ctx)
	return err
}

func (a *PIAgent) publishActivity(item controller.RuntimeActivityItem) {
	if a.activity == nil {
		return
	}
	if strings.TrimSpace(item.Runtime) == "" {
		item.Runtime = string(RuntimePi)
	}
	a.activity.Publish(item)
}

type piState struct {
	ModelID     string
	SessionFile string
	SessionID   string
}

func startPIProcess(ctx context.Context, command string, sessionDir string, configDir string, providerName string, model string, envVars map[string]string) (*piRPCProcess, error) {
	procCtx, cancel := context.WithCancel(ctx)
	args := []string{"--mode", "rpc", "--session-dir", sessionDir}
	if strings.TrimSpace(providerName) != "" {
		args = append(args, "--provider", providerName)
	}
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(procCtx, command, args...)
	env := append([]string{}, os.Environ()...)
	if strings.TrimSpace(configDir) != "" {
		env = append(env, "PI_CODING_AGENT_DIR="+configDir)
	}
	for key, value := range envVars {
		env = append(env, key+"="+value)
	}
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open pi stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open pi stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open pi stderr: %w", err)
	}
	process := &piRPCProcess{
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
		cancel: cancel,
	}
	go func() {
		_, _ = io.Copy(&process.stderr, stderr)
	}()
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start pi runtime: %w", err)
	}
	return process, nil
}

func (p *piRPCProcess) close() {
	if p.cancel != nil {
		p.cancel()
	}
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Wait()
	}
}

func (p *piRPCProcess) prompt(ctx context.Context, message string, publish func(controller.RuntimeActivityItem)) ([]controller.RuntimeEvent, error) {
	id := "prompt-1"
	if err := p.write(map[string]any{"id": id, "type": "prompt", "message": message}); err != nil {
		return nil, err
	}
	if _, err := p.awaitResponse(ctx, id, nil); err != nil {
		return nil, err
	}
	events := make([]controller.RuntimeEvent, 0, 8)
	for {
		record, err := p.read(ctx)
		if err != nil {
			return events, err
		}
		kind, _ := record["type"].(string)
		switch kind {
		case "agent_end":
			if publish != nil {
				publish(controller.RuntimeActivityItem{
					Key:     "agent-status",
					Runtime: string(RuntimePi),
					Kind:    "status",
					Title:   "pi complete",
					Body:    "PI finished the current turn.",
					Done:    true,
					Replace: true,
				})
			}
			return events, nil
		case "message_update":
			if publish != nil {
				if item := piMessageUpdateActivity(record); item != nil {
					publish(*item)
				}
			}
		case "tool_execution_start":
			toolName := stringValue(record["toolName"])
			toolCallID := stringValue(record["toolCallId"])
			if publish != nil {
				publish(controller.RuntimeActivityItem{
					Key:     piActivityKey("tool", toolCallID, toolName),
					Runtime: string(RuntimePi),
					Kind:    "tool",
					Title:   "pi " + toolName,
					Body:    summarizePIArgs(record["args"]),
					Detail:  summarizePIArgs(record["args"]),
					Replace: true,
				})
			}
			events = append(events, controller.RuntimeEvent{
				Runtime: "pi",
				Kind:    "tool_start",
				Title:   "pi " + toolName,
				Body:    summarizePIArgs(record["args"]),
			})
		case "tool_execution_update":
			toolName := stringValue(record["toolName"])
			toolCallID := stringValue(record["toolCallId"])
			if publish != nil {
				body := summarizePIResult(record["partialResult"])
				if strings.TrimSpace(body) == "" {
					body = summarizePIResult(record["result"])
				}
				publish(controller.RuntimeActivityItem{
					Key:     piActivityKey("tool", toolCallID, toolName),
					Runtime: string(RuntimePi),
					Kind:    "tool",
					Title:   "pi " + toolName,
					Body:    body,
					Detail:  body,
					Replace: true,
				})
			}
		case "tool_execution_end":
			toolName := stringValue(record["toolName"])
			toolCallID := stringValue(record["toolCallId"])
			resultSummary := summarizePIResult(record["result"])
			if publish != nil {
				publish(controller.RuntimeActivityItem{
					Key:     piActivityKey("tool", toolCallID, toolName),
					Runtime: string(RuntimePi),
					Kind:    "tool",
					Title:   "pi " + toolName,
					Body:    resultSummary,
					Detail:  resultSummary,
					Done:    true,
					Replace: true,
				})
			}
			events = append(events, controller.RuntimeEvent{
				Runtime: "pi",
				Kind:    "tool_end",
				Title:   "pi " + toolName,
				Body:    resultSummary,
				Detail:  resultSummary,
			})
		case "auto_compaction_start":
			if publish != nil {
				publish(controller.RuntimeActivityItem{
					Key:     "agent-status",
					Runtime: string(RuntimePi),
					Kind:    "status",
					Title:   "pi compaction",
					Body:    "PI compacted its own session context.",
					Replace: true,
				})
			}
			events = append(events, controller.RuntimeEvent{Runtime: "pi", Kind: "status", Title: "pi compaction", Body: "PI compacted its own session context."})
		case "auto_retry_start":
			if publish != nil {
				publish(controller.RuntimeActivityItem{
					Key:     "agent-status",
					Runtime: string(RuntimePi),
					Kind:    "status",
					Title:   "pi retry",
					Body:    "PI is retrying after a transient provider/runtime error.",
					Replace: true,
				})
			}
			events = append(events, controller.RuntimeEvent{Runtime: "pi", Kind: "status", Title: "pi retry", Body: "PI is retrying after a transient provider/runtime error."})
		case "extension_error":
			if publish != nil {
				publish(controller.RuntimeActivityItem{
					Key:     "agent-status",
					Runtime: string(RuntimePi),
					Kind:    "error",
					Title:   "pi extension error",
					Body:    stringValue(record["error"]),
					Detail:  stringValue(record["error"]),
					Done:    true,
					Replace: true,
				})
			}
			events = append(events, controller.RuntimeEvent{Runtime: "pi", Kind: "error", Title: "pi extension error", Body: stringValue(record["error"])})
		}
	}
}

func (p *piRPCProcess) state(ctx context.Context) (piState, error) {
	id := "state-1"
	response, err := p.request(ctx, map[string]any{"id": id, "type": "get_state"})
	if err != nil {
		return piState{}, err
	}
	data, _ := response["data"].(map[string]any)
	model, _ := data["model"].(map[string]any)
	return piState{
		ModelID:     stringValue(model["id"]),
		SessionFile: stringValue(data["sessionFile"]),
		SessionID:   stringValue(data["sessionId"]),
	}, nil
}

func (p *piRPCProcess) lastAssistantText(ctx context.Context) (string, error) {
	response, err := p.request(ctx, map[string]any{"id": "assistant-1", "type": "get_last_assistant_text"})
	if err != nil {
		return "", err
	}
	data, _ := response["data"].(map[string]any)
	text := stringValue(data["text"])
	if strings.TrimSpace(text) == "" {
		return "", errors.New("pi returned no assistant text")
	}
	return text, nil
}

func (p *piRPCProcess) switchSession(ctx context.Context, path string) error {
	_, err := p.request(ctx, map[string]any{"id": "switch-1", "type": "switch_session", "sessionPath": path})
	return err
}

func (p *piRPCProcess) newSession(ctx context.Context) error {
	_, err := p.request(ctx, map[string]any{"id": "new-1", "type": "new_session"})
	return err
}

func (p *piRPCProcess) request(ctx context.Context, payload map[string]any) (map[string]any, error) {
	id := stringValue(payload["id"])
	if id == "" {
		return nil, errors.New("rpc request id must not be empty")
	}
	if err := p.write(payload); err != nil {
		return nil, err
	}
	return p.awaitResponse(ctx, id, nil)
}

func (p *piRPCProcess) write(payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal pi rpc request: %w", err)
	}
	if _, err := p.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write pi rpc request: %w", err)
	}
	return nil
}

func (p *piRPCProcess) awaitResponse(ctx context.Context, id string, handler func(map[string]any)) (map[string]any, error) {
	for {
		record, err := p.read(ctx)
		if err != nil {
			return nil, err
		}
		if stringValue(record["type"]) == "response" && stringValue(record["id"]) == id {
			if ok, _ := record["success"].(bool); !ok {
				return nil, fmt.Errorf("pi rpc %s failed: %s", stringValue(record["command"]), p.errorMessage(record))
			}
			return record, nil
		}
		if handler != nil {
			handler(record)
		}
	}
}

func (p *piRPCProcess) read(ctx context.Context) (map[string]any, error) {
	type result struct {
		line []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := p.reader.ReadBytes('\n')
		ch <- result{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			message := strings.TrimSpace(p.stderr.String())
			if message == "" {
				message = res.err.Error()
			}
			return nil, fmt.Errorf("read pi rpc output: %s", message)
		}
		var record map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(res.line), &record); err != nil {
			return nil, fmt.Errorf("decode pi rpc output: %w", err)
		}
		return record, nil
	}
}

func (p *piRPCProcess) errorMessage(response map[string]any) string {
	if message := stringValue(response["error"]); message != "" {
		return message
	}
	if data, ok := response["data"].(map[string]any); ok {
		if message := stringValue(data["error"]); message != "" {
			return message
		}
	}
	if message := strings.TrimSpace(p.stderr.String()); message != "" {
		return message
	}
	return "unknown pi runtime error"
}

func piProfileSupport(profile provider.Profile) (bool, string) {
	switch profile.Preset {
	case provider.PresetOpenAI, provider.PresetAnthropic, provider.PresetOpenRouter, provider.PresetCustom, provider.PresetOpenWebUI, provider.PresetCodexCLI:
		return true, ""
	case provider.PresetOllama:
		return false, "PI runtime does not support the Ollama preset yet."
	case provider.PresetMock:
		return false, "Mock is only available on the builtin runtime."
	default:
		return false, fmt.Sprintf("PI runtime does not support provider preset %q.", profile.Preset)
	}
}

func ensurePIConfig(cfg config.Config, profile provider.Profile) (string, map[string]string, string, error) {
	configDir := filepath.Join(cfg.StateDir, "pi", "agent", cfg.WorkspaceID)
	if err := securefs.EnsurePrivateDir(configDir); err != nil {
		return "", nil, "", fmt.Errorf("prepare pi config dir: %w", err)
	}
	envVars := map[string]string{}
	providerName := piProviderName(profile)

	modelsPayload := map[string]any{"providers": map[string]any{}}
	providersMap := modelsPayload["providers"].(map[string]any)

	switch profile.Preset {
	case provider.PresetCustom, provider.PresetOpenWebUI:
		customProvider, err := customPIProviderConfig(profile, envVars)
		if err != nil {
			return "", nil, "", err
		}
		providersMap[providerName] = customProvider
	case provider.PresetOpenAI, provider.PresetAnthropic, provider.PresetOpenRouter:
		builtInOverride, err := builtInPIProviderOverride(profile, envVars)
		if err != nil {
			return "", nil, "", err
		}
		if len(builtInOverride) > 0 {
			providersMap[providerName] = builtInOverride
		}
	case provider.PresetCodexCLI:
		// No generated models config; PI uses its own OpenAI/Codex auth flow.
	}

	data, err := json.MarshalIndent(modelsPayload, "", "  ")
	if err != nil {
		return "", nil, "", fmt.Errorf("marshal pi models config: %w", err)
	}
	if err := securefs.WriteAtomicPrivate(filepath.Join(configDir, "models.json"), data, 0o600); err != nil {
		return "", nil, "", fmt.Errorf("write pi models config: %w", err)
	}
	return configDir, envVars, providerName, nil
}

func builtInPIProviderOverride(profile provider.Profile, envVars map[string]string) (map[string]any, error) {
	override := map[string]any{}
	if strings.TrimSpace(profile.BaseURL) != "" {
		override["baseUrl"] = strings.TrimSpace(profile.BaseURL)
	}
	if profile.AuthMethod == provider.AuthAPIKey && strings.TrimSpace(profile.APIKey) != "" {
		envName := builtinPIAPIKeyEnv(profile)
		override["apiKey"] = envName
		envVars[envName] = profile.APIKey
	}
	return override, nil
}

func customPIProviderConfig(profile provider.Profile, envVars map[string]string) (map[string]any, error) {
	if strings.TrimSpace(profile.BaseURL) == "" {
		return nil, errors.New("custom PI provider requires a base URL")
	}
	modelID := strings.TrimSpace(profile.Model)
	if modelID == "" {
		return nil, errors.New("custom PI provider requires a model")
	}
	providerConfig := map[string]any{
		"baseUrl": strings.TrimSpace(profile.BaseURL),
		"api":     "openai-responses",
		"models": []map[string]any{
			{
				"id":     modelID,
				"name":   modelID,
				"input":  []string{"text"},
				"cost":   map[string]float64{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
				"compat": map[string]any{"supportsDeveloperRole": false, "supportsReasoningEffort": false},
			},
		},
	}
	if profile.AuthMethod == provider.AuthAPIKey && strings.TrimSpace(profile.APIKey) != "" {
		envName := customPIAPIKeyEnv(profile)
		providerConfig["apiKey"] = envName
		envVars[envName] = profile.APIKey
	}
	return providerConfig, nil
}

func piProviderName(profile provider.Profile) string {
	switch profile.Preset {
	case provider.PresetOpenAI:
		return "openai"
	case provider.PresetAnthropic:
		return "anthropic"
	case provider.PresetOpenRouter:
		return "openrouter"
	case provider.PresetCodexCLI:
		return "openai"
	case provider.PresetOpenWebUI:
		return "openwebui"
	case provider.PresetCustom:
		return "shuttle-custom"
	default:
		return strings.TrimSpace(string(profile.Preset))
	}
}

func shouldStartPINewSession(switchedSession bool, preserveExternalSession bool, storedTaskID string, inputTaskID string) bool {
	if !switchedSession {
		return true
	}
	if preserveExternalSession {
		return false
	}
	return strings.TrimSpace(storedTaskID) != strings.TrimSpace(inputTaskID)
}

func effectivePIModel(profile provider.Profile) string {
	return strings.TrimSpace(profile.Model)
}

func builtinPIAPIKeyEnv(profile provider.Profile) string {
	switch profile.Preset {
	case provider.PresetAnthropic:
		return "ANTHROPIC_API_KEY"
	case provider.PresetOpenRouter:
		return "OPENROUTER_API_KEY"
	default:
		return "OPENAI_API_KEY"
	}
}

func customPIAPIKeyEnv(profile provider.Profile) string {
	switch profile.Preset {
	case provider.PresetOpenWebUI:
		return "SHUTTLE_PI_OPENWEBUI_API_KEY"
	default:
		return "SHUTTLE_PI_CUSTOM_API_KEY"
	}
}

func summarizePIArgs(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func summarizePIResult(value any) string {
	parsed, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	text := string(parsed)
	if len(text) > 600 {
		return text[:600] + "..."
	}
	return text
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func piActivityKey(prefix string, primary string, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return prefix + ":" + strings.TrimSpace(primary)
	}
	if strings.TrimSpace(fallback) != "" {
		return prefix + ":" + strings.TrimSpace(fallback)
	}
	return prefix
}

func piMessageUpdateActivity(record map[string]any) *controller.RuntimeActivityItem {
	message, _ := record["message"].(map[string]any)
	assistantEvent, _ := record["assistantMessageEvent"].(map[string]any)
	body := extractPIMessageBody(message, assistantEvent)
	if strings.TrimSpace(body) == "" {
		return nil
	}
	return &controller.RuntimeActivityItem{
		Key:     piActivityKey("message", stringValue(message["id"]), "assistant"),
		Runtime: string(RuntimePi),
		Kind:    "assistant",
		Title:   "pi assistant",
		Body:    body,
		Detail:  body,
		Replace: true,
	}
}

func extractPIMessageBody(message map[string]any, assistantEvent map[string]any) string {
	if partial, ok := assistantEvent["partial"].(map[string]any); ok {
		if text := extractPITextFromMessage(partial); text != "" {
			return text
		}
	}
	if text := extractPITextFromMessage(message); text != "" {
		return text
	}
	if delta := stringValue(assistantEvent["delta"]); delta != "" {
		return delta
	}
	return ""
}

func extractPITextFromMessage(message map[string]any) string {
	content, _ := message["content"].([]any)
	parts := make([]string, 0, len(content))
	for _, item := range content {
		contentItem, _ := item.(map[string]any)
		if stringValue(contentItem["type"]) != "text" {
			continue
		}
		text := stringValue(contentItem["text"])
		if text == "" {
			text = stringValue(contentItem["content"])
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

package agentruntime

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
	"strings"
	"sync"
)

var codexAppServerClientFactory = func(command string, meta RuntimeMetadata) codexAppServerClientFactoryFunc {
	return func(ctx context.Context) (codexAppServerClient, error) {
		return newStdioCodexAppServerClient(ctx, command, meta)
	}
}

type CodexAppServerTurnHandler interface {
	Respond(context.Context, Request) (Outcome, error)
}

type codexAppServerRuntime struct {
	meta    RuntimeMetadata
	handler CodexAppServerTurnHandler
}

type codexAppServerClientFactoryFunc func(context.Context) (codexAppServerClient, error)

type codexAppServerDefaultHandler struct {
	meta          RuntimeMetadata
	clientFactory codexAppServerClientFactoryFunc
	mu            sync.Mutex
	client        codexAppServerClient
	threads       map[string]string
}

type codexAppServerClient interface {
	Initialize(context.Context) error
	StartThread(context.Context, codexAppServerThreadStartParams) (codexAppServerThreadStartResult, error)
	StartTurn(context.Context, codexAppServerTurnStartParams) (codexAppServerTurnStartResult, error)
	WaitForTurnCompletion(context.Context, string, string) (codexAppServerTurn, error)
	Close() error
}

type codexAppServerJSONRPC struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type codexAppServerThreadStartParams struct {
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
	Ephemeral      bool   `json:"ephemeral,omitempty"`
	Model          string `json:"model,omitempty"`
	ModelProvider  string `json:"modelProvider,omitempty"`
	Personality    string `json:"personality,omitempty"`
	Sandbox        string `json:"sandbox,omitempty"`
}

type codexAppServerThreadStartResult struct {
	Thread codexAppServerThread `json:"thread"`
}

type codexAppServerTurnStartParams struct {
	ThreadID       string                    `json:"threadId"`
	Input          []codexAppServerUserInput `json:"input"`
	Cwd            string                    `json:"cwd,omitempty"`
	Model          string                    `json:"model,omitempty"`
	OutputSchema   map[string]any            `json:"outputSchema,omitempty"`
	ApprovalPolicy string                    `json:"approvalPolicy,omitempty"`
	Personality    string                    `json:"personality,omitempty"`
}

type codexAppServerUserInput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexAppServerTurnStartResult struct {
	Turn codexAppServerTurn `json:"turn"`
}

type codexAppServerTurnCompletedNotification struct {
	ThreadID string             `json:"threadId"`
	Turn     codexAppServerTurn `json:"turn"`
}

type codexAppServerItemCompletedNotification struct {
	ThreadID string                   `json:"threadId"`
	TurnID   string                   `json:"turnId"`
	Item     codexAppServerThreadItem `json:"item"`
}

type codexAppServerThread struct {
	ID     string          `json:"id"`
	Status json.RawMessage `json:"status,omitempty"`
}

type codexAppServerTurn struct {
	ID     string                     `json:"id"`
	Status string                     `json:"status,omitempty"`
	Items  []codexAppServerThreadItem `json:"items,omitempty"`
}

type codexAppServerThreadItem struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Phase string `json:"phase,omitempty"`
}

type stdioCodexAppServerClient struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	reader   *bufio.Reader
	stderr   bytes.Buffer
	nextID   int64
	readMu   sync.Mutex
	writeMu  sync.Mutex
	waitOnce sync.Once
	waitErr  error
}

type shuttleStructuredResponse struct {
	Message                string   `json:"message"`
	PlanSummary            string   `json:"plan_summary"`
	PlanSteps              []string `json:"plan_steps"`
	PlanStepStatuses       []string `json:"plan_step_statuses"`
	ProposalKind           string   `json:"proposal_kind"`
	ProposalCommand        string   `json:"proposal_command"`
	ProposalKeys           string   `json:"proposal_keys"`
	ProposalPatch          string   `json:"proposal_patch"`
	ProposalPatchTarget    string   `json:"proposal_patch_target"`
	ProposalEditPath       string   `json:"proposal_edit_path"`
	ProposalEditOperation  string   `json:"proposal_edit_operation"`
	ProposalEditAnchorText string   `json:"proposal_edit_anchor_text"`
	ProposalEditOldText    string   `json:"proposal_edit_old_text"`
	ProposalEditNewText    string   `json:"proposal_edit_new_text"`
	ProposalEditStartLine  int      `json:"proposal_edit_start_line"`
	ProposalEditEndLine    int      `json:"proposal_edit_end_line"`
	ProposalDescription    string   `json:"proposal_description"`
	ApprovalKind           string   `json:"approval_kind"`
	ApprovalTitle          string   `json:"approval_title"`
	ApprovalSummary        string   `json:"approval_summary"`
	ApprovalCommand        string   `json:"approval_command"`
	ApprovalPatch          string   `json:"approval_patch"`
	ApprovalPatchTarget    string   `json:"approval_patch_target"`
	ApprovalRisk           string   `json:"approval_risk"`
}

func NewCodexAppServerRuntime(meta RuntimeMetadata, handler CodexAppServerTurnHandler) Runtime {
	meta.Type = normalizeRuntimeSelection(meta.Type)
	if meta.Type == "" {
		meta.Type = RuntimeCodexAppServer
	}
	runtime := codexAppServerRuntime{meta: meta, handler: handler}
	if handler == nil {
		runtime.handler = &codexAppServerDefaultHandler{
			meta:          meta,
			clientFactory: codexAppServerClientFactory(strings.TrimSpace(meta.Command), meta),
			threads:       map[string]string{},
		}
	}
	return runtime
}

func (r codexAppServerRuntime) Handle(ctx context.Context, host Host, req Request) (Outcome, error) {
	handler := r.handler
	outcome, err := handleRuntimeTurn(ctx, host, req, func(ctx context.Context, req Request) (Outcome, error) {
		return handler.Respond(ctx, req)
	})
	if err != nil {
		return Outcome{}, err
	}
	return annotateRuntimeMetadata(outcome, r.meta), nil
}

func (h *codexAppServerDefaultHandler) respond(ctx context.Context, req Request) (Outcome, error) {
	if h.clientFactory == nil {
		return Outcome{}, errors.New("codex app server client factory is required")
	}

	outcome, reusedStoredThread, err := h.respondOnce(ctx, req)
	if err == nil {
		return outcome, nil
	}
	if errors.Is(err, context.Canceled) || !shouldRetryCodexAppServerTurn(err) {
		return Outcome{}, err
	}
	recoveryNote := "Recovered from a stale Codex app-server thread by starting a fresh native thread."
	if !reusedStoredThread {
		recoveryNote = "Recovered from a transient Codex app-server process failure by retrying the turn with a fresh native thread."
	}
	h.resetClientAndThread(req)
	outcome, retryErr := h.respondOnceFresh(ctx, req)
	if retryErr != nil {
		return Outcome{}, retryErr
	}
	if outcome.ModelInfo == nil {
		outcome.ModelInfo = &ModelInfo{}
	}
	if strings.TrimSpace(outcome.ModelInfo.RuntimeFailureReason) == "" {
		outcome.ModelInfo.RuntimeFailureReason = recoveryNote
	}
	return outcome, nil
}

func (h *codexAppServerDefaultHandler) Respond(ctx context.Context, req Request) (Outcome, error) {
	return h.respond(ctx, req)
}

func (h *codexAppServerDefaultHandler) respondOnce(ctx context.Context, req Request) (Outcome, bool, error) {
	threadID, found := h.threadFor(req)
	outcome, err := h.respondWithThreadPreference(ctx, req, strings.TrimSpace(threadID), found)
	return outcome, found, err
}

func (h *codexAppServerDefaultHandler) respondOnceFresh(ctx context.Context, req Request) (Outcome, error) {
	return h.respondWithThreadPreference(ctx, req, "", false)
}

func (h *codexAppServerDefaultHandler) respondWithThreadPreference(ctx context.Context, req Request, storedThreadID string, useStoredThread bool) (Outcome, error) {
	client, err := h.clientFor(ctx)
	if err != nil {
		return Outcome{}, err
	}
	cwd, _ := os.Getwd()
	threadID := strings.TrimSpace(storedThreadID)
	if !useStoredThread || threadID == "" {
		thread, startErr := client.StartThread(ctx, codexAppServerThreadStartParams{
			ApprovalPolicy: "never",
			Cwd:            cwd,
			Ephemeral:      false,
			Model:          strings.TrimSpace(h.meta.Model),
			ModelProvider:  modelProviderForPreset(h.meta.ProviderPreset),
			Personality:    "pragmatic",
			Sandbox:        "read-only",
		})
		if startErr != nil {
			return Outcome{}, startErr
		}
		threadID = strings.TrimSpace(thread.Thread.ID)
		h.storeThread(req, threadID)
	}
	turn, err := client.StartTurn(ctx, codexAppServerTurnStartParams{
		ThreadID: threadID,
		Input: []codexAppServerUserInput{{
			Type: "text",
			Text: buildCodexSDKPrompt(req),
		}},
		Cwd:            cwd,
		Model:          strings.TrimSpace(h.meta.Model),
		OutputSchema:   shuttleAgentResponseSchema(),
		ApprovalPolicy: "never",
		Personality:    "pragmatic",
	})
	if err != nil {
		return Outcome{}, err
	}
	completedTurn, err := client.WaitForTurnCompletion(ctx, threadID, turn.Turn.ID)
	if err != nil {
		return Outcome{}, err
	}
	text := latestCodexAppServerAgentMessage(completedTurn.Items)
	if strings.TrimSpace(text) == "" {
		return Outcome{}, errors.New("codex app server completed turn without a final agent message")
	}
	var structured shuttleStructuredResponse
	if err := json.Unmarshal([]byte(text), &structured); err != nil {
		return Outcome{}, fmt.Errorf("decode codex app server structured output: %w", err)
	}
	return structuredToOutcome(structured)
}

func (h *codexAppServerDefaultHandler) clientFor(ctx context.Context) (codexAppServerClient, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.client != nil {
		return h.client, nil
	}
	client, err := h.clientFactory(ctx)
	if err != nil {
		return nil, err
	}
	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	h.client = client
	if h.threads == nil {
		h.threads = map[string]string{}
	}
	return h.client, nil
}

func (h *codexAppServerDefaultHandler) threadFor(req Request) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	threadID, ok := h.threads[codexAppServerMemoryThreadKey(req.SessionName, req.TaskID)]
	return strings.TrimSpace(threadID), ok && strings.TrimSpace(threadID) != ""
}

func (h *codexAppServerDefaultHandler) storeThread(req Request, threadID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.threads == nil {
		h.threads = map[string]string{}
	}
	h.threads[codexAppServerMemoryThreadKey(req.SessionName, req.TaskID)] = strings.TrimSpace(threadID)
}

func (h *codexAppServerDefaultHandler) resetClientAndThread(req Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.client != nil {
		_ = h.client.Close()
		h.client = nil
	}
	delete(h.threads, codexAppServerMemoryThreadKey(req.SessionName, req.TaskID))
}

func codexAppServerMemoryThreadKey(sessionName string, taskID string) string {
	return strings.TrimSpace(sessionName) + "\x00" + strings.TrimSpace(taskID)
}

func newStdioCodexAppServerClient(ctx context.Context, command string, meta RuntimeMetadata) (codexAppServerClient, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		command = "codex"
	}
	cmd := exec.CommandContext(ctx, command, "app-server", "--listen", "stdio://")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("start codex app server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("start codex app server stdout: %w", err)
	}
	client := &stdioCodexAppServerClient{cmd: cmd, stdin: stdin, reader: bufio.NewReader(stdout)}
	cmd.Stderr = &client.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex app server %q: %w", command, err)
	}
	return client, nil
}

func (c *stdioCodexAppServerClient) Initialize(ctx context.Context) error {
	params := map[string]any{"clientInfo": map[string]any{"name": "shuttle", "version": "0.1"}}
	var result map[string]any
	return c.call(ctx, "initialize", params, &result)
}

func (c *stdioCodexAppServerClient) StartThread(ctx context.Context, params codexAppServerThreadStartParams) (codexAppServerThreadStartResult, error) {
	var result codexAppServerThreadStartResult
	return result, c.call(ctx, "thread/start", params, &result)
}

func (c *stdioCodexAppServerClient) StartTurn(ctx context.Context, params codexAppServerTurnStartParams) (codexAppServerTurnStartResult, error) {
	var result codexAppServerTurnStartResult
	return result, c.call(ctx, "turn/start", params, &result)
}

func (c *stdioCodexAppServerClient) WaitForTurnCompletion(ctx context.Context, threadID string, turnID string) (codexAppServerTurn, error) {
	var latestAgentMessage string
	for {
		message, err := c.readMessage(ctx)
		if err != nil {
			return codexAppServerTurn{}, err
		}
		if message.Method == "error" {
			return codexAppServerTurn{}, fmt.Errorf("codex app server error notification: %s", strings.TrimSpace(string(message.Params)))
		}
		switch message.Method {
		case "item/completed":
			var notification codexAppServerItemCompletedNotification
			if err := json.Unmarshal(message.Params, &notification); err != nil {
				return codexAppServerTurn{}, fmt.Errorf("decode codex app server item completion: %w", err)
			}
			if strings.TrimSpace(notification.ThreadID) != strings.TrimSpace(threadID) || strings.TrimSpace(notification.TurnID) != strings.TrimSpace(turnID) {
				continue
			}
			if notification.Item.Type == "agentMessage" && strings.TrimSpace(notification.Item.Text) != "" {
				latestAgentMessage = strings.TrimSpace(notification.Item.Text)
			}
		case "turn/completed":
			var notification codexAppServerTurnCompletedNotification
			if err := json.Unmarshal(message.Params, &notification); err != nil {
				return codexAppServerTurn{}, fmt.Errorf("decode codex app server turn completion: %w", err)
			}
			if strings.TrimSpace(notification.ThreadID) != strings.TrimSpace(threadID) {
				continue
			}
			if strings.TrimSpace(notification.Turn.ID) != strings.TrimSpace(turnID) {
				continue
			}
			if len(notification.Turn.Items) == 0 && latestAgentMessage != "" {
				notification.Turn.Items = []codexAppServerThreadItem{{Type: "agentMessage", Text: latestAgentMessage, Phase: "final_answer"}}
			}
			return notification.Turn, nil
		}
	}
}

func (c *stdioCodexAppServerClient) Close() error {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	_ = c.cmd.Process.Kill()
	return c.wait()
}

func (c *stdioCodexAppServerClient) wait() error {
	c.waitOnce.Do(func() {
		c.waitErr = c.cmd.Wait()
	})
	return c.waitErr
}

func (c *stdioCodexAppServerClient) call(ctx context.Context, method string, params any, result any) error {
	requestID := c.nextRequestID()
	payload := codexAppServerJSONRPC{JSONRPC: "2.0", ID: requestID, Method: method}
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal codex app server params for %s: %w", method, err)
		}
		payload.Params = encoded
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal codex app server request %s: %w", method, err)
	}
	c.writeMu.Lock()
	_, err = c.stdin.Write(append(encoded, '\n'))
	c.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("write codex app server request %s: %w", method, err)
	}
	for {
		message, err := c.readMessage(ctx)
		if err != nil {
			return err
		}
		if message.Method != "" {
			continue
		}
		if message.ID != requestID {
			continue
		}
		if message.Error != nil {
			return fmt.Errorf("codex app server %s error: %s", method, strings.TrimSpace(message.Error.Message))
		}
		if result != nil {
			if err := json.Unmarshal(message.Result, result); err != nil {
				return fmt.Errorf("decode codex app server %s result: %w", method, err)
			}
		}
		return nil
	}
}

func (c *stdioCodexAppServerClient) nextRequestID() int64 {
	c.nextID++
	return c.nextID
}

func (c *stdioCodexAppServerClient) readMessage(ctx context.Context) (codexAppServerJSONRPC, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return codexAppServerJSONRPC{}, err
		}
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				waitErr := c.wait()
				stderr := strings.TrimSpace(c.stderr.String())
				if stderr == "" && waitErr != nil {
					stderr = waitErr.Error()
				}
				if stderr == "" {
					stderr = "codex app server exited before returning a response"
				}
				return codexAppServerJSONRPC{}, errors.New(stderr)
			}
			return codexAppServerJSONRPC{}, fmt.Errorf("read codex app server response: %w", err)
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var message codexAppServerJSONRPC
		if err := json.Unmarshal(line, &message); err != nil {
			return codexAppServerJSONRPC{}, fmt.Errorf("decode codex app server message: %w", err)
		}
		return message, nil
	}
}

func latestCodexAppServerAgentMessage(items []codexAppServerThreadItem) string {
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if item.Type != "agentMessage" {
			continue
		}
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		return strings.TrimSpace(item.Text)
	}
	return ""
}

func modelProviderForPreset(preset string) string {
	preset = strings.ToLower(strings.TrimSpace(preset))
	switch {
	case strings.Contains(preset, "openrouter"):
		return "openrouter"
	case strings.Contains(preset, "anthropic"):
		return "anthropic"
	case strings.Contains(preset, "ollama"):
		return "ollama"
	default:
		return "openai"
	}
}

func shouldRetryCodexAppServerTurn(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(message, "unknown thread"):
		return true
	case strings.Contains(message, "exited before returning a response"):
		return true
	case strings.Contains(message, "broken pipe"):
		return true
	case strings.Contains(message, "connection reset"):
		return true
	case strings.Contains(message, "eof"):
		return true
	case strings.Contains(message, "without a final agent message"):
		return true
	default:
		return false
	}
}

func structuredToOutcome(input shuttleStructuredResponse) (Outcome, error) {
	outcome := Outcome{Message: strings.TrimSpace(input.Message)}
	if len(input.PlanStepStatuses) > 0 {
		statuses := make([]PlanStepStatus, 0, len(input.PlanStepStatuses))
		for _, status := range input.PlanStepStatuses {
			switch PlanStepStatus(strings.TrimSpace(status)) {
			case PlanStepPending, PlanStepInProgress, PlanStepDone:
				statuses = append(statuses, PlanStepStatus(strings.TrimSpace(status)))
			case "":
				return Outcome{}, fmt.Errorf("unsupported empty plan status")
			default:
				return Outcome{}, fmt.Errorf("unsupported plan status %q", status)
			}
		}
		outcome.PlanStatuses = statuses
	}
	planSummary := strings.TrimSpace(input.PlanSummary)
	if planSummary != "" || len(input.PlanSteps) > 0 {
		steps := make([]string, 0, len(input.PlanSteps))
		for _, step := range input.PlanSteps {
			step = strings.TrimSpace(step)
			if step != "" {
				steps = append(steps, step)
			}
		}
		outcome.Plan = &Plan{Summary: planSummary, Steps: steps}
	}
	proposal, err := parseStructuredProposal(input)
	if err != nil {
		return Outcome{}, err
	}
	outcome.Proposal = proposal
	approval, err := parseStructuredApproval(input)
	if err != nil {
		return Outcome{}, err
	}
	outcome.Approval = approval
	return outcome, nil
}

func parseStructuredProposal(input shuttleStructuredResponse) (*Proposal, error) {
	if strings.TrimSpace(input.ProposalKind) == "" &&
		strings.TrimSpace(input.ProposalCommand) == "" &&
		strings.TrimSpace(input.ProposalKeys) == "" &&
		strings.TrimSpace(input.ProposalPatch) == "" &&
		strings.TrimSpace(input.ProposalEditPath) == "" &&
		strings.TrimSpace(input.ProposalEditOperation) == "" &&
		strings.TrimSpace(input.ProposalEditAnchorText) == "" &&
		strings.TrimSpace(input.ProposalEditOldText) == "" &&
		strings.TrimSpace(input.ProposalEditNewText) == "" &&
		input.ProposalEditStartLine == 0 &&
		input.ProposalEditEndLine == 0 &&
		strings.TrimSpace(input.ProposalDescription) == "" {
		return nil, nil
	}
	kind := ProposalKind(strings.TrimSpace(input.ProposalKind))
	switch kind {
	case "":
		switch {
		case strings.TrimSpace(input.ProposalCommand) != "":
			kind = ProposalCommand
		case strings.TrimSpace(input.ProposalKeys) != "":
			kind = ProposalKeys
		case strings.TrimSpace(input.ProposalPatch) != "":
			kind = ProposalPatch
		case strings.TrimSpace(input.ProposalEditOperation) != "" || strings.TrimSpace(input.ProposalEditPath) != "":
			kind = ProposalEdit
		case strings.EqualFold(strings.TrimSpace(input.ProposalKind), string(ProposalInspectContext)):
			kind = ProposalInspectContext
		default:
			kind = ProposalAnswer
		}
	case ProposalAnswer, ProposalCommand, ProposalKeys, ProposalPatch, ProposalEdit, ProposalInspectContext:
	default:
		return nil, fmt.Errorf("unsupported proposal kind %q", input.ProposalKind)
	}
	var edit *EditIntent
	if kind == ProposalEdit {
		operation := EditOperation(strings.TrimSpace(input.ProposalEditOperation))
		switch operation {
		case EditInsertBefore, EditInsertAfter, EditReplaceExact, EditReplaceRange:
		default:
			return nil, fmt.Errorf("unsupported edit operation %q", input.ProposalEditOperation)
		}
		edit = &EditIntent{
			Target:     normalizePatchTarget(input.ProposalPatchTarget),
			Path:       strings.TrimSpace(input.ProposalEditPath),
			Operation:  operation,
			AnchorText: input.ProposalEditAnchorText,
			OldText:    input.ProposalEditOldText,
			NewText:    input.ProposalEditNewText,
			StartLine:  input.ProposalEditStartLine,
			EndLine:    input.ProposalEditEndLine,
		}
	}
	return &Proposal{
		Kind:        kind,
		Command:     strings.TrimSpace(input.ProposalCommand),
		Keys:        input.ProposalKeys,
		Patch:       strings.TrimSpace(input.ProposalPatch),
		PatchTarget: normalizePatchTarget(input.ProposalPatchTarget),
		Edit:        edit,
		Description: strings.TrimSpace(input.ProposalDescription),
	}, nil
}

func parseStructuredApproval(input shuttleStructuredResponse) (*ApprovalRequest, error) {
	if strings.TrimSpace(input.ApprovalKind) == "" &&
		strings.TrimSpace(input.ApprovalTitle) == "" &&
		strings.TrimSpace(input.ApprovalSummary) == "" &&
		strings.TrimSpace(input.ApprovalCommand) == "" &&
		strings.TrimSpace(input.ApprovalPatch) == "" {
		return nil, nil
	}
	kind := ApprovalKind(strings.TrimSpace(input.ApprovalKind))
	switch kind {
	case "":
		switch {
		case strings.TrimSpace(input.ApprovalCommand) != "":
			kind = ApprovalCommand
		case strings.TrimSpace(input.ApprovalPatch) != "":
			kind = ApprovalPatch
		default:
			kind = ApprovalPlan
		}
	case ApprovalCommand, ApprovalPatch, ApprovalPlan:
	default:
		return nil, fmt.Errorf("unsupported approval kind %q", input.ApprovalKind)
	}
	risk := RiskLevel(strings.TrimSpace(input.ApprovalRisk))
	switch risk {
	case "":
		risk = RiskMedium
	case RiskLow, RiskMedium, RiskHigh:
	default:
		return nil, fmt.Errorf("unsupported approval risk %q", input.ApprovalRisk)
	}
	return &ApprovalRequest{
		ID:          "approval-runtime",
		Kind:        kind,
		Title:       strings.TrimSpace(input.ApprovalTitle),
		Summary:     strings.TrimSpace(input.ApprovalSummary),
		Command:     strings.TrimSpace(input.ApprovalCommand),
		Patch:       strings.TrimSpace(input.ApprovalPatch),
		PatchTarget: normalizePatchTarget(input.ApprovalPatchTarget),
		Risk:        risk,
	}, nil
}

func normalizePatchTarget(value string) PatchTarget {
	switch PatchTarget(strings.TrimSpace(value)) {
	case PatchTargetRemoteShell:
		return PatchTargetRemoteShell
	default:
		return PatchTargetLocalWorkspace
	}
}

func shuttleAgentResponseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message":                   map[string]any{"type": "string"},
			"plan_summary":              map[string]any{"type": "string"},
			"plan_steps":                map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"plan_step_statuses":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"proposal_kind":             map[string]any{"type": "string"},
			"proposal_command":          map[string]any{"type": "string"},
			"proposal_keys":             map[string]any{"type": "string"},
			"proposal_patch":            map[string]any{"type": "string"},
			"proposal_patch_target":     map[string]any{"type": "string"},
			"proposal_edit_path":        map[string]any{"type": "string"},
			"proposal_edit_operation":   map[string]any{"type": "string"},
			"proposal_edit_anchor_text": map[string]any{"type": "string"},
			"proposal_edit_old_text":    map[string]any{"type": "string"},
			"proposal_edit_new_text":    map[string]any{"type": "string"},
			"proposal_edit_start_line":  map[string]any{"type": "integer"},
			"proposal_edit_end_line":    map[string]any{"type": "integer"},
			"proposal_description":      map[string]any{"type": "string"},
			"approval_kind":             map[string]any{"type": "string"},
			"approval_title":            map[string]any{"type": "string"},
			"approval_summary":          map[string]any{"type": "string"},
			"approval_command":          map[string]any{"type": "string"},
			"approval_patch":            map[string]any{"type": "string"},
			"approval_patch_target":     map[string]any{"type": "string"},
			"approval_risk":             map[string]any{"type": "string"},
		},
		"required": []string{
			"message",
			"plan_summary",
			"plan_steps",
			"plan_step_statuses",
			"proposal_kind",
			"proposal_command",
			"proposal_keys",
			"proposal_patch",
			"proposal_patch_target",
			"proposal_edit_path",
			"proposal_edit_operation",
			"proposal_edit_anchor_text",
			"proposal_edit_old_text",
			"proposal_edit_new_text",
			"proposal_edit_start_line",
			"proposal_edit_end_line",
			"proposal_description",
			"approval_kind",
			"approval_title",
			"approval_summary",
			"approval_command",
			"approval_patch",
			"approval_patch_target",
			"approval_risk",
		},
		"additionalProperties": false,
	}
}

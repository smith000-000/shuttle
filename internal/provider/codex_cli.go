package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"aiterm/internal/controller"
)

var defaultCodexCLICommand = "codex"

type CodexCLIAgent struct {
	ResponsesAgent
	command string
}

func NewCodexCLIAgent(profile Profile) (*CodexCLIAgent, error) {
	if profile.BackendFamily != BackendCLIAgent {
		return nil, fmt.Errorf("profile %q is not a CLI agent backend", profile.Preset)
	}

	command := strings.TrimSpace(profile.CLICommand)
	if command == "" {
		command = defaultCodexCLICommand
	}
	if _, err := exec.LookPath(command); err != nil {
		return nil, fmt.Errorf("find codex CLI: %w", err)
	}
	if profile.AuthMethod == AuthCodexLogin {
		status, err := codexLoginStatus(command)
		if err != nil {
			return nil, err
		}
		if !codexStatusIsLoggedIn(status) {
			return nil, errors.New("codex CLI is not logged in; run `codex login` first")
		}
	}

	return &CodexCLIAgent{
		ResponsesAgent: ResponsesAgent{profile: profile},
		command:        command,
	}, nil
}

func (a *CodexCLIAgent) Respond(ctx context.Context, input controller.AgentInput) (controller.AgentResponse, error) {
	tempDir, err := os.MkdirTemp("", "shuttle-codex-*")
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("create codex temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	schemaPath := filepath.Join(tempDir, "schema.json")
	outputPath := filepath.Join(tempDir, "last-message.json")
	schema, err := json.MarshalIndent(shuttleAgentResponseSchema(), "", "  ")
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("marshal shuttle schema: %w", err)
	}
	if err := os.WriteFile(schemaPath, schema, 0o600); err != nil {
		return controller.AgentResponse{}, fmt.Errorf("write codex schema: %w", err)
	}

	args := []string{
		"exec",
		"--skip-git-repo-check",
		"--ephemeral",
		"--sandbox", "read-only",
		"--output-schema", schemaPath,
		"--output-last-message", outputPath,
		"--color", "never",
	}
	if workdir := codexWorkingDir(input); workdir != "" {
		args = append(args, "--cd", workdir)
	}
	if model := strings.TrimSpace(a.profile.Model); model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, buildCodexPrompt(input))

	command := exec.CommandContext(ctx, a.command, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("run codex CLI: %s", summarizeCodexCLIError(output, err))
	}

	lastMessage, err := os.ReadFile(outputPath)
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("read codex output: %s", summarizeCodexCLIError(output, err))
	}

	var structured shuttleStructuredResponse
	if err := json.Unmarshal(lastMessage, &structured); err != nil {
		return controller.AgentResponse{}, fmt.Errorf("decode codex structured output: %w", err)
	}

	response, err := a.toAgentResponse(structured)
	if err != nil {
		return controller.AgentResponse{}, err
	}
	response.ModelInfo = &controller.AgentModelInfo{
		ProviderPreset: string(a.profile.Preset),
		RequestedModel: strings.TrimSpace(a.profile.Model),
		ResponseModel:  strings.TrimSpace(a.profile.Model),
	}

	return response, nil
}

func (a *CodexCLIAgent) CheckHealth(ctx context.Context) error {
	if a.profile.AuthMethod == AuthCodexLogin {
		status, err := codexLoginStatus(a.command)
		if err != nil {
			return err
		}
		if !codexStatusIsLoggedIn(status) {
			return errors.New("codex CLI is not logged in")
		}
		return nil
	}

	_, err := a.Respond(ctx, controller.AgentInput{
		Prompt: "Respond with a short confirmation that the provider path works.",
	})
	return err
}

func codexLoginStatus(command string) (string, error) {
	output, err := exec.Command(command, "login", "status").CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("check codex login status: %s", message)
	}

	return strings.TrimSpace(string(output)), nil
}

func codexStatusIsLoggedIn(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return strings.HasPrefix(status, "logged in")
}

func buildCodexPrompt(input controller.AgentInput) string {
	return shuttleSystemPrompt + "\n\nShuttle turn context:\n" + buildTurnContext(input)
}

func codexWorkingDir(input controller.AgentInput) string {
	return strings.TrimSpace(input.Session.WorkingDirectory)
}

func summarizeCodexCLIError(output []byte, err error) string {
	message := strings.TrimSpace(string(output))
	if message == "" {
		if err == nil {
			return "codex CLI failed before producing a structured response"
		}
		return err.Error()
	}
	if strings.Contains(message, shuttleSystemPrompt) || strings.Contains(message, strings.SplitN(shuttleSystemPrompt, "\n", 2)[0]) {
		return "codex CLI failed before producing a structured response"
	}

	lines := strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n")
	candidates := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case line == "", line == "--------":
			continue
		case strings.EqualFold(line, "user"), strings.EqualFold(line, "assistant"), strings.EqualFold(line, "system"):
			continue
		case strings.HasPrefix(line, "OpenAI Codex "):
			continue
		case strings.HasPrefix(line, "workdir:"), strings.HasPrefix(line, "model:"), strings.HasPrefix(line, "provider:"), strings.HasPrefix(line, "approval:"), strings.HasPrefix(line, "sandbox:"), strings.HasPrefix(line, "reasoning effort:"), strings.HasPrefix(line, "reasoning summaries:"), strings.HasPrefix(line, "session id:"):
			continue
		}
		candidates = append(candidates, line)
	}
	if len(candidates) == 0 {
		return "codex CLI failed before producing a structured response"
	}
	return candidates[len(candidates)-1]
}

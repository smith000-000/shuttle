package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aiterm/internal/controller"
)

func TestCodexCLIAgentRespondUsesStructuredOutputFile(t *testing.T) {
	script := writeFakeCodexCLI(t)
	argsFile := filepath.Join(t.TempDir(), "codex-args.txt")
	t.Setenv("FAKE_CODEX_LOGIN_STATUS", "Logged in using ChatGPT")
	t.Setenv("FAKE_CODEX_LAST_MESSAGE", `{"message":"Codex subscription path works.","plan_summary":"","plan_steps":[],"proposal_kind":"command","proposal_command":"git status --short","proposal_patch":"","proposal_description":"Inspect repository changes.","approval_kind":"","approval_title":"","approval_summary":"","approval_command":"","approval_patch":"","approval_risk":""}`)
	t.Setenv("FAKE_CODEX_ARGS_FILE", argsFile)

	agent, err := NewCodexCLIAgent(Profile{
		BackendFamily: BackendCLIAgent,
		Preset:        PresetCodexCLI,
		AuthMethod:    AuthCodexLogin,
		Model:         "gpt-5.2-codex",
		CLICommand:    script,
	})
	if err != nil {
		t.Fatalf("NewCodexCLIAgent() error = %v", err)
	}

	response, err := agent.Respond(context.Background(), controller.AgentInput{
		Prompt: "show repo status",
		Session: controller.SessionContext{
			WorkingDirectory: "/workspace",
		},
	})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if response.Message != "Codex subscription path works." {
		t.Fatalf("expected response message, got %q", response.Message)
	}
	if response.Proposal == nil || response.Proposal.Command != "git status --short" {
		t.Fatalf("expected command proposal, got %#v", response.Proposal)
	}
	if response.ModelInfo == nil || response.ModelInfo.RequestedModel != "gpt-5.2-codex" {
		t.Fatalf("expected model info, got %#v", response.ModelInfo)
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	argText := string(args)
	for _, fragment := range []string{"exec", "--output-schema", "--output-last-message", "--sandbox", "read-only", "--cd", "/workspace", "--model", "gpt-5.2-codex"} {
		if !strings.Contains(argText, fragment) {
			t.Fatalf("expected args to contain %q, got %q", fragment, argText)
		}
	}
}

func TestCodexCLIAgentRequiresLoginForCodexAuth(t *testing.T) {
	script := writeFakeCodexCLI(t)
	t.Setenv("FAKE_CODEX_LOGIN_STATUS", "Not logged in")

	_, err := NewCodexCLIAgent(Profile{
		BackendFamily: BackendCLIAgent,
		Preset:        PresetCodexCLI,
		AuthMethod:    AuthCodexLogin,
		CLICommand:    script,
	})
	if err == nil {
		t.Fatal("expected login error")
	}
}

func TestCodexCLIAgentRedactsPromptFromCLIError(t *testing.T) {
	script := writeFakeCodexCLI(t)
	t.Setenv("FAKE_CODEX_LOGIN_STATUS", "Logged in using ChatGPT")
	t.Setenv("FAKE_CODEX_ERROR_OUTPUT", "OpenAI Codex v0.116.0 (research preview)\n--------\nworkdir: /home/jsmith\nuser\nYou are the Shuttle agent runtime.")

	agent, err := NewCodexCLIAgent(Profile{
		BackendFamily: BackendCLIAgent,
		Preset:        PresetCodexCLI,
		AuthMethod:    AuthCodexLogin,
		Model:         "gpt-5.4",
		CLICommand:    script,
	})
	if err != nil {
		t.Fatalf("NewCodexCLIAgent() error = %v", err)
	}

	_, err = agent.Respond(context.Background(), controller.AgentInput{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected codex CLI error")
	}
	if !strings.Contains(err.Error(), "codex CLI failed before producing a structured response") {
		t.Fatalf("expected sanitized CLI error, got %q", err)
	}
	if strings.Contains(err.Error(), "You are the Shuttle agent runtime") {
		t.Fatalf("expected system prompt to be redacted, got %q", err)
	}
}

func writeFakeCodexCLI(t *testing.T) string {
	t.Helper()

	script := filepath.Join(t.TempDir(), "codex")
	content := `#!/bin/sh
set -eu

if [ "${1-}" = "login" ] && [ "${2-}" = "status" ]; then
  printf '%s\n' "${FAKE_CODEX_LOGIN_STATUS:-Logged in using ChatGPT}"
  exit 0
fi

if [ "${1-}" = "exec" ]; then
  if [ -n "${FAKE_CODEX_ERROR_OUTPUT:-}" ]; then
    printf '%s\n' "${FAKE_CODEX_ERROR_OUTPUT}"
    exit 1
  fi
  if [ -n "${FAKE_CODEX_ARGS_FILE:-}" ]; then
    printf '%s\n' "$@" > "${FAKE_CODEX_ARGS_FILE}"
  fi
  shift
  output=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --output-last-message)
        output="$2"
        shift 2
        ;;
      *)
        shift
        ;;
    esac
  done
  if [ -z "$output" ]; then
    echo "missing output file" >&2
    exit 1
  fi
  printf '%s' "${FAKE_CODEX_LAST_MESSAGE}" > "$output"
  exit 0
fi

echo "unexpected command" >&2
exit 1
`
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	return script
}

package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aiterm/internal/tmux"
)

type semanticShellEvent string

const (
	semanticEventUnknown semanticShellEvent = ""
	semanticEventPrompt  semanticShellEvent = "prompt"
	semanticEventCommand semanticShellEvent = "command"
)

type semanticShellState struct {
	Event     semanticShellEvent
	ExitCode  *int
	Directory string
	Shell     string
	UpdatedAt time.Time
}

func semanticStatePath(stateDir string, paneTTY string) string {
	safeTTY := strings.TrimSpace(paneTTY)
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	safeTTY = replacer.Replace(safeTTY)
	safeTTY = strings.Trim(safeTTY, "_")
	if safeTTY == "" {
		safeTTY = "unknown"
	}
	return filepath.Join(stateDir, "shell-state", safeTTY+".state")
}

func readSemanticShellState(stateDir string, paneTTY string) (semanticShellState, bool) {
	if strings.TrimSpace(stateDir) == "" || strings.TrimSpace(paneTTY) == "" {
		return semanticShellState{}, false
	}

	statePath := semanticStatePath(stateDir, paneTTY)
	info, err := os.Stat(statePath)
	if err != nil {
		return semanticShellState{}, false
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		return semanticShellState{}, false
	}

	state, ok := parseSemanticShellState(string(data))
	if !ok {
		return semanticShellState{}, false
	}
	state.UpdatedAt = info.ModTime()
	return state, true
}

func parseSemanticShellState(raw string) (semanticShellState, bool) {
	line := strings.TrimSpace(raw)
	if line == "" {
		return semanticShellState{}, false
	}

	var state semanticShellState
	for _, field := range strings.Split(line, "\t") {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "event":
			state.Event = semanticShellEvent(strings.TrimSpace(value))
		case "exit":
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return semanticShellState{}, false
			}
			state.ExitCode = &parsed
		case "cwd":
			state.Directory = value
		case "shell":
			state.Shell = strings.TrimSpace(value)
		}
	}

	if state.Event == semanticEventUnknown {
		return semanticShellState{}, false
	}
	return state, true
}

func synthesizePromptContext(base PromptContext, state semanticShellState) PromptContext {
	context := base
	if context.User == "" || context.Host == "" {
		context = GuessLocalContext(state.Directory)
	}
	if strings.TrimSpace(state.Directory) != "" {
		context.Directory = shortenHome(state.Directory)
	}
	if context.PromptSymbol == "" {
		context.PromptSymbol = "$"
	}
	if state.ExitCode != nil {
		exitCode := *state.ExitCode
		context.LastExitCode = &exitCode
	}
	if strings.TrimSpace(context.RawLine) == "" {
		context.RawLine = context.PromptLine()
	}
	return context
}

func (o *Observer) EnsureLocalShellIntegration(ctx context.Context, paneID string) error {
	if strings.TrimSpace(o.stateDir) == "" {
		return nil
	}

	pane, err := o.client.PaneInfo(ctx, paneID)
	if err != nil {
		return err
	}

	shellName := strings.TrimSpace(strings.ToLower(pane.CurrentCommand))
	switch shellName {
	case "bash", "zsh":
	default:
		return nil
	}

	promptContext, err := o.CaptureShellContext(ctx, paneID)
	if err != nil {
		promptContext = o.promptHint
	}
	if promptContext.PromptLine() == "" || promptContext.Remote {
		return nil
	}

	scriptPath, err := writeSemanticShellIntegrationScript(o.stateDir, pane, shellName)
	if err != nil {
		return err
	}

	command := " [ -n \"$SHUTTLE_SEMANTIC_SHELL_V1\" ] || . " + shellQuote(scriptPath) + " >/dev/null 2>&1"
	if err := o.client.SendKeys(ctx, paneID, command, true); err != nil {
		return fmt.Errorf("send semantic shell integration command: %w", err)
	}
	return nil
}

func writeSemanticShellIntegrationScript(stateDir string, pane tmux.Pane, shellName string) (string, error) {
	integrationDir := filepath.Join(stateDir, "shell-integration")
	if err := os.MkdirAll(integrationDir, 0o755); err != nil {
		return "", fmt.Errorf("create shell integration directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "shell-state"), 0o755); err != nil {
		return "", fmt.Errorf("create shell state directory: %w", err)
	}

	scriptPath := filepath.Join(integrationDir, sanitizeIntegrationName(shellName+"-"+pane.ID)+".sh")
	statePath := semanticStatePath(stateDir, pane.TTY)
	var contents string
	switch shellName {
	case "bash":
		contents = bashSemanticShellIntegrationScript(statePath)
	case "zsh":
		contents = zshSemanticShellIntegrationScript(statePath)
	default:
		return "", fmt.Errorf("unsupported shell for semantic integration: %s", shellName)
	}

	if err := os.WriteFile(scriptPath, []byte(contents), 0o600); err != nil {
		return "", fmt.Errorf("write semantic shell integration script: %w", err)
	}
	return scriptPath, nil
}

func sanitizeIntegrationName(value string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "%", "pane", " ", "_")
	return replacer.Replace(value)
}

func bashSemanticShellIntegrationScript(statePath string) string {
	return strings.TrimSpace(`
export SHUTTLE_SEMANTIC_SHELL_V1=1
export SHUTTLE_SEMANTIC_SHELL_STATE_FILE=`+shellQuote(statePath)+`
__shuttle_semantic_emit_osc133() { printf '\033]133;%s\007' "$1"; }
__shuttle_semantic_emit_osc7() { printf '\033]7;file://%s%s\007' "${HOSTNAME:-localhost}" "$PWD"; }
__shuttle_semantic_write_state() {
  local event="$1"
  local exit_code="$2"
  printf 'event=%s\texit=%s\tcwd=%s\tshell=%s\n' "$event" "$exit_code" "$PWD" "bash" >| "$SHUTTLE_SEMANTIC_SHELL_STATE_FILE"
}
__shuttle_semantic_preexec() {
  [ "${__shuttle_semantic_in_precmd:-0}" = "1" ] && return
  [ "${__shuttle_semantic_started:-0}" = "1" ] && return
  case "$BASH_COMMAND" in
    __shuttle_semantic_* ) return ;;
  esac
  __shuttle_semantic_started=1
  __shuttle_semantic_emit_osc133 "B"
  __shuttle_semantic_emit_osc133 "C"
  __shuttle_semantic_write_state "command" ""
}
__shuttle_semantic_precmd() {
  local exit_code="$?"
  __shuttle_semantic_in_precmd=1
  __shuttle_semantic_started=0
  __shuttle_semantic_emit_osc133 "D;$exit_code"
  __shuttle_semantic_emit_osc7
  __shuttle_semantic_emit_osc133 "A"
  __shuttle_semantic_write_state "prompt" "$exit_code"
  __shuttle_semantic_in_precmd=0
}
trap '__shuttle_semantic_preexec' DEBUG
if [ -n "${PROMPT_COMMAND:-}" ]; then
  PROMPT_COMMAND="__shuttle_semantic_precmd;${PROMPT_COMMAND}"
else
  PROMPT_COMMAND="__shuttle_semantic_precmd"
fi
__shuttle_semantic_precmd
`) + "\n"
}

func zshSemanticShellIntegrationScript(statePath string) string {
	return strings.TrimSpace(`
export SHUTTLE_SEMANTIC_SHELL_V1=1
export SHUTTLE_SEMANTIC_SHELL_STATE_FILE=`+shellQuote(statePath)+`
autoload -Uz add-zsh-hook
__shuttle_semantic_emit_osc133() { printf '\033]133;%s\007' "$1"; }
__shuttle_semantic_emit_osc7() { printf '\033]7;file://%s%s\007' "${HOST:-localhost}" "$PWD"; }
__shuttle_semantic_write_state() {
  local event="$1"
  local exit_code="$2"
  printf 'event=%s\texit=%s\tcwd=%s\tshell=%s\n' "$event" "$exit_code" "$PWD" "zsh" >| "$SHUTTLE_SEMANTIC_SHELL_STATE_FILE"
}
__shuttle_semantic_preexec() {
  __shuttle_semantic_emit_osc133 "B"
  __shuttle_semantic_emit_osc133 "C"
  __shuttle_semantic_write_state "command" ""
}
__shuttle_semantic_precmd() {
  local exit_code="$?"
  __shuttle_semantic_emit_osc133 "D;$exit_code"
  __shuttle_semantic_emit_osc7
  __shuttle_semantic_emit_osc133 "A"
  __shuttle_semantic_write_state "prompt" "$exit_code"
}
add-zsh-hook preexec __shuttle_semantic_preexec
add-zsh-hook precmd __shuttle_semantic_precmd
__shuttle_semantic_precmd
`) + "\n"
}

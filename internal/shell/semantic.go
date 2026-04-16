package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aiterm/internal/protocol"
	"aiterm/internal/securefs"
	"aiterm/internal/tmux"
)

type semanticShellEvent string

const (
	semanticEventUnknown      semanticShellEvent = ""
	semanticEventPrompt       semanticShellEvent = "prompt"
	semanticEventCommand      semanticShellEvent = "command"
	semanticEventCommandDone  semanticShellEvent = "command_done"
	semanticShellStateVersion                    = 1
)

var semanticStateNow = time.Now

type semanticShellState struct {
	Event     semanticShellEvent
	ExitCode  *int
	Directory string
	Shell     string
	UpdatedAt time.Time
}

func semanticStatePath(stateDir string, paneTTY string) string {
	return filepath.Join(stateDir, "shell-state", semanticStateFileName(paneTTY))
}

func semanticStateFileName(paneTTY string) string {
	safeTTY := strings.TrimSpace(paneTTY)
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	safeTTY = replacer.Replace(safeTTY)
	safeTTY = strings.Trim(safeTTY, "_")
	if safeTTY == "" {
		safeTTY = "unknown"
	}
	return safeTTY + ".state"
}

func readSemanticShellState(stateDir string, paneTTY string) (semanticShellState, bool) {
	if strings.TrimSpace(stateDir) == "" || strings.TrimSpace(paneTTY) == "" {
		return semanticShellState{}, false
	}

	statePath := semanticStatePath(stateDir, paneTTY)
	data, info, err := securefs.ReadFileNoFollow(statePath)
	if err != nil {
		return semanticShellState{}, false
	}

	state, ok := parseSemanticShellStateFile(data)
	if !ok {
		return semanticShellState{}, false
	}
	state.UpdatedAt = info.ModTime()
	return state, true
}

func parseSemanticShellStateFile(data []byte) (semanticShellState, bool) {
	raw := string(data)
	if strings.TrimSpace(raw) == "" || !strings.HasSuffix(raw, "\n") {
		return semanticShellState{}, false
	}
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		return parseSemanticShellState(line)
	}
	return semanticShellState{}, false
}

func parseSemanticShellState(raw string) (semanticShellState, bool) {
	line := strings.TrimSpace(raw)
	if line == "" {
		return semanticShellState{}, false
	}

	var payload struct {
		Version int    `json:"version"`
		Event   string `json:"event"`
		Exit    *int   `json:"exit"`
		Cwd     string `json:"cwd"`
		Shell   string `json:"shell"`
	}
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return semanticShellState{}, false
	}
	if payload.Version != 0 && payload.Version != semanticShellStateVersion {
		return semanticShellState{}, false
	}

	state := semanticShellState{
		Event:     semanticShellEvent(strings.TrimSpace(payload.Event)),
		ExitCode:  payload.Exit,
		Directory: strings.TrimSpace(payload.Cwd),
		Shell:     strings.TrimSpace(strings.ToLower(payload.Shell)),
	}
	if state.Directory == "" {
		return semanticShellState{}, false
	}
	switch state.Shell {
	case "bash", "zsh":
	default:
		return semanticShellState{}, false
	}
	switch state.Event {
	case semanticEventPrompt:
	case semanticEventCommand:
		if state.ExitCode != nil {
			return semanticShellState{}, false
		}
	case semanticEventCommandDone:
	default:
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
	_, err := o.ensureLocalShellIntegration(ctx, paneID)
	return err
}

func (o *Observer) ensureLocalShellIntegration(ctx context.Context, paneID string) (bool, error) {
	if strings.TrimSpace(o.stateDir) == "" {
		return false, nil
	}

	pane, err := o.paneInfo(ctx, paneID)
	if err != nil {
		return false, err
	}

	shellName := strings.TrimSpace(strings.ToLower(pane.CurrentCommand))
	switch shellName {
	case "bash", "zsh":
	default:
		return false, nil
	}

	observed, err := o.CaptureObservedShellState(ctx, paneID)
	if err != nil {
		observed = ObservedShellState{PromptContext: o.promptHint, HasPromptContext: o.promptHint.PromptLine() != ""}
		observed.Location = inferShellLocation(observed.PromptContext, pane.CurrentCommand, o.rememberedTransition(paneID))
	}
	promptContext := observed.PromptContext
	if !observed.HasPromptContext || promptContext.PromptLine() == "" || observed.Location.Kind == ShellLocationRemote {
		return false, nil
	}
	if observed.HasSemanticState && observed.SemanticState.Shell == shellName {
		return false, nil
	}

	scriptPath, err := writeSemanticShellIntegrationScript(o.stateDir, pane, shellName)
	if err != nil {
		return false, err
	}

	command := " [ \"${SHUTTLE_SEMANTIC_SHELL_V1_PID:-}\" = \"$$\" ] || . " + shellQuote(scriptPath) + " >/dev/null 2>&1"
	if err := o.runManagedSemanticInstall(ctx, paneID, command); err != nil {
		return false, fmt.Errorf("send semantic shell integration command: %w", err)
	}
	return true, nil
}

func (o *Observer) runManagedSemanticInstall(ctx context.Context, paneID string, command string) error {
	markers := protocol.NewMarkers()
	wrapped := protocol.WrapCommand(command, markers)
	if err := o.sendKeys(ctx, paneID, wrapped, true); err != nil {
		return err
	}

	deadline := time.Now().Add(3 * time.Second)
	lastCapture := ""
	for {
		captured, err := o.capturePane(ctx, paneID, -trackedCaptureLines)
		if err != nil {
			return err
		}
		lastCapture = captured

		result, complete, err := protocol.ParseCommandResult(captured, markers)
		if err != nil {
			return err
		}
		if complete {
			if result.ExitCode != 0 {
				return fmt.Errorf("semantic shell integration exited with %d", result.ExitCode)
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for semantic shell integration to settle")
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
			_ = lastCapture
		}
	}
}

func writeSemanticShellIntegrationScript(stateDir string, pane tmux.Pane, shellName string) (string, error) {
	integrationDir := filepath.Join(stateDir, "shell-integration")
	if err := securefs.EnsurePrivateDir(integrationDir); err != nil {
		return "", fmt.Errorf("create shell integration directory: %w", err)
	}
	if err := securefs.EnsurePrivateDir(filepath.Join(stateDir, "shell-state")); err != nil {
		return "", fmt.Errorf("create shell state directory: %w", err)
	}

	scriptPath := filepath.Join(integrationDir, sanitizeIntegrationName(shellName+"-"+pane.ID+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))+".sh")
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

	if err := securefs.WriteExclusivePrivate(scriptPath, []byte(contents), 0o600); err != nil {
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
export SHUTTLE_SEMANTIC_SHELL_V1_PID=$$
export SHUTTLE_SEMANTIC_SHELL_STATE_FILE=`+shellQuote(statePath)+`
__shuttle_semantic_emit_osc133() { printf '\033]133;%s\033\\' "$1"; }
__shuttle_semantic_emit_osc7() { printf '\033]7;file://%s%s\033\\' "${HOSTNAME:-localhost}" "$PWD"; }
__shuttle_semantic_write_state() {
  local event="$1"
  local exit_code="$2"
  local cwd_json="$PWD"
  local exit_json="null"
  cwd_json=${cwd_json//\\/\\\\}
  cwd_json=${cwd_json//\"/\\\"}
  cwd_json=${cwd_json//$'\n'/\\n}
  cwd_json=${cwd_json//$'\r'/\\r}
  cwd_json=${cwd_json//$'\t'/\\t}
  if [ -n "$exit_code" ]; then
    exit_json="$exit_code"
  fi
  printf '{"version":1,"event":"%s","exit":%s,"cwd":"%s","shell":"bash"}\n' "$event" "$exit_json" "$cwd_json" >| "$SHUTTLE_SEMANTIC_SHELL_STATE_FILE"
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
export SHUTTLE_SEMANTIC_SHELL_V1_PID=$$
export SHUTTLE_SEMANTIC_SHELL_STATE_FILE=`+shellQuote(statePath)+`
autoload -Uz add-zsh-hook
__shuttle_semantic_emit_osc133() { printf '\033]133;%s\033\\' "$1"; }
__shuttle_semantic_emit_osc7() { printf '\033]7;file://%s%s\033\\' "${HOST:-localhost}" "$PWD"; }
__shuttle_semantic_write_state() {
  local event="$1"
  local exit_code="$2"
  local cwd_json="$PWD"
  local exit_json="null"
  cwd_json=${cwd_json//\\/\\\\}
  cwd_json=${cwd_json//\"/\\\"}
  cwd_json=${cwd_json//$'\n'/\\n}
  cwd_json=${cwd_json//$'\r'/\\r}
  cwd_json=${cwd_json//$'\t'/\\t}
  if [ -n "$exit_code" ]; then
    exit_json="$exit_code"
  fi
  printf '{"version":1,"event":"%s","exit":%s,"cwd":"%s","shell":"zsh"}\n' "$event" "$exit_json" "$cwd_json" >| "$SHUTTLE_SEMANTIC_SHELL_STATE_FILE"
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

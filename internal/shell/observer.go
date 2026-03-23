package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"aiterm/internal/logging"
	"aiterm/internal/protocol"
	"aiterm/internal/tmux"
)

type TrackedExecution struct {
	CommandID      string
	Command        string
	State          MonitorState
	Cause          CompletionCause
	Confidence     SignalConfidence
	SemanticShell  bool
	SemanticSource string
	ExitCode       int
	Captured       string
	ShellContext   PromptContext
}

type paneClient interface {
	CapturePane(ctx context.Context, target string, startLine int) (string, error)
	SendKeys(ctx context.Context, target string, command string, pressEnter bool) error
	PaneInfo(ctx context.Context, target string) (tmux.Pane, error)
}

type escapedPaneClient interface {
	CapturePaneEscaped(ctx context.Context, target string, startLine int) (string, error)
}

type Observer struct {
	client          paneClient
	tmuxClient      *tmux.Client
	stateDir        string
	sessionName     string
	startDir        string
	promptHint      PromptContext
	semanticMu      sync.Mutex
	streamCollector *streamSemanticCollector
	transitionMu    sync.Mutex
	transitions     map[string]shellTransitionKind
	paneAliasMu     sync.Mutex
	paneAliases     map[string]string
	sessionEnsurer  func(context.Context) error
}

var wrappedSentinelSuffixPattern = regexp.MustCompile(`^[a-z0-9]{1,16}:\$\?$`)

const shellContextProbeCommand = `printf '__SHUTTLE_CTX_EXIT__=%s\n' "$?"
printf '__SHUTTLE_CTX_USER__=%s\n' "$(whoami 2>/dev/null || id -un 2>/dev/null)"
printf '__SHUTTLE_CTX_HOST__=%s\n' "$(hostname 2>/dev/null || uname -n 2>/dev/null)"
printf '__SHUTTLE_CTX_UNAME__=%s\n' "$(uname -sr 2>/dev/null)"
printf '__SHUTTLE_CTX_PWD__=%s\n' "$PWD"`

const (
	DefaultCommandTimeout           = 10 * time.Second
	ContextTransitionCommandTimeout = 60 * time.Second
	trackedCaptureLines             = 2000
)

func NewObserver(client *tmux.Client) *Observer {
	return &Observer{client: client, tmuxClient: client}
}

func (o *Observer) WithStateDir(stateDir string) *Observer {
	o.stateDir = strings.TrimSpace(stateDir)
	return o
}

func (o *Observer) WithSessionName(sessionName string) *Observer {
	o.sessionName = strings.TrimSpace(sessionName)
	return o
}

func (o *Observer) WithStartDir(startDir string) *Observer {
	o.startDir = strings.TrimSpace(startDir)
	return o
}

func (o *Observer) WithSessionEnsurer(sessionEnsurer func(context.Context) error) *Observer {
	o.sessionEnsurer = sessionEnsurer
	return o
}

func (o *Observer) WithPromptHint(context PromptContext) *Observer {
	if context.PromptLine() != "" {
		o.promptHint = context
	}
	return o
}

func (o *Observer) StartTrackedCommand(ctx context.Context, paneID string, command string, timeout time.Duration) (CommandMonitor, error) {
	if isContextTransitionCommand(command) {
		monitor := newTrackedCommandMonitor("", command)
		go func() {
			result, err := o.runContextTransitionCommand(ctx, paneID, command, timeout)
			state := MonitorStateCompleted
			if err != nil {
				state = monitorStateFromError(err)
			}
			monitor.finish(result, err, state)
		}()
		return monitor, nil
	}

	if err := o.EnsureLocalShellIntegration(ctx, paneID); err != nil {
		logging.TraceError("shell.semantic_bootstrap.prelaunch_error", err, "pane", paneID, "command", command)
	}

	beforeCapture, err := o.capturePane(ctx, paneID, -trackedCaptureLines)
	if err != nil {
		return nil, fmt.Errorf("capture pane before tracked command: %w", err)
	}

	markers := protocol.NewMarkers()
	transportCommand, cleanup, err := o.buildTrackedTransport(ctx, paneID, command, markers)
	if err != nil {
		return nil, err
	}

	if err := o.sendKeys(ctx, paneID, transportCommand, true); err != nil {
		cleanup()
		return nil, fmt.Errorf("send tracked command: %w", err)
	}

	monitor := newTrackedCommandMonitor(markers.CommandID, command)
	go o.runTrackedMonitor(ctx, monitor, paneID, command, transportCommand, timeout, beforeCapture, markers, cleanup)
	return monitor, nil
}

func (o *Observer) CaptureRecentOutput(ctx context.Context, paneID string, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}

	captured, err := o.capturePane(ctx, paneID, -lines)
	if err != nil {
		return "", fmt.Errorf("capture recent output: %w", err)
	}

	return sanitizeCapturedBody(captured), nil
}

func (o *Observer) CaptureShellContext(ctx context.Context, paneID string) (PromptContext, error) {
	captured, err := o.capturePane(ctx, paneID, -80)
	if err != nil {
		return PromptContext{}, fmt.Errorf("capture shell context: %w", err)
	}

	context, ok := ParsePromptContextFromCapture(captured)
	if ok {
		if paneInfo, paneErr := o.paneInfo(ctx, paneID); paneErr == nil {
			if semanticState, _, semanticOK := o.captureSemanticShellState(ctx, paneID, paneInfo.TTY, "", strings.TrimSpace(paneInfo.CurrentCommand), context); semanticOK {
				context = synthesizePromptContext(context, semanticState)
			}
		}
		o.promptHint = context
		return context, nil
	}

	if paneInfo, paneErr := o.paneInfo(ctx, paneID); paneErr == nil {
		if semanticState, _, semanticOK := o.captureSemanticShellState(ctx, paneID, paneInfo.TTY, "", strings.TrimSpace(paneInfo.CurrentCommand), o.promptHint); semanticOK {
			context = synthesizePromptContext(o.promptHint, semanticState)
			o.promptHint = context
			return context, nil
		}
	}

	return PromptContext{}, nil
}

func (o *Observer) RunTrackedCommand(ctx context.Context, paneID string, command string, timeout time.Duration) (TrackedExecution, error) {
	monitor, err := o.StartTrackedCommand(ctx, paneID, command, timeout)
	if err != nil {
		return TrackedExecution{}, err
	}
	return monitor.Wait()
}

func (o *Observer) ResolveTrackedPane(ctx context.Context, paneID string) (string, error) {
	info, err := o.paneInfo(ctx, paneID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(info.ID), nil
}

func (o *Observer) runTrackedMonitor(ctx context.Context, monitor *trackedCommandMonitor, paneID string, command string, transportCommand string, timeout time.Duration, beforeCapture string, markers protocol.Markers, cleanup func()) {
	defer cleanup()
	logging.Trace(
		"shell.tracked.start",
		"pane", paneID,
		"command", command,
		"timeout_ms", timeout.Milliseconds(),
	)

	started := false
	startDeadline := time.Now().Add(timeout)
	lastCapture := ""
	lastPaneInfoCheck := time.Time{}
	alternateOn := false
	currentPaneCommand := ""
	paneTTY := ""
	commandSentAt := time.Now()
	for {
		captured, err := o.capturePane(ctx, paneID, -trackedCaptureLines)
		if err != nil {
			logging.TraceError(
				"shell.tracked.capture_error",
				err,
				"pane", paneID,
				"command", command,
				"last_capture_preview", logging.Preview(lastCapture, 1000),
			)
			monitor.finish(TrackedExecution{
				CommandID:  markers.CommandID,
				Command:    command,
				Cause:      CompletionCauseUnknown,
				Confidence: ConfidenceLow,
				Captured:   monitorTail(lastCapture, transportCommand),
			}, fmt.Errorf("capture pane: %w", err), MonitorStateLost)
			return
		}
		lastCapture = captured
		tail := monitorTail(captured, transportCommand)
		monitor.updateTail(tail)
		var parsedShellContext PromptContext
		hasParsedShellContext := false
		if shellContext, ok := ParsePromptContextFromCapture(captured); ok {
			parsedShellContext = shellContext
			hasParsedShellContext = true
			monitor.updateShellContext(shellContext)
		}
		if lastPaneInfoCheck.IsZero() || time.Since(lastPaneInfoCheck) >= 200*time.Millisecond {
			lastPaneInfoCheck = time.Now()
			if paneInfo, paneErr := o.paneInfo(ctx, paneID); paneErr == nil {
				alternateOn = paneInfo.AlternateOn
				currentPaneCommand = strings.TrimSpace(paneInfo.CurrentCommand)
				paneTTY = strings.TrimSpace(paneInfo.TTY)
				monitor.updateForegroundCommand(currentPaneCommand)
			}
		}
		semanticBaseContext := monitor.Snapshot().ShellContext
		if semanticBaseContext.PromptLine() == "" {
			semanticBaseContext = o.promptHint
		}
		if hasParsedShellContext {
			semanticBaseContext = parsedShellContext
		}
		semanticState, semanticSource, hasSemanticState := o.captureSemanticShellState(ctx, paneID, paneTTY, command, currentPaneCommand, semanticBaseContext)
		if hasSemanticState {
			monitor.updateSemanticMetadata(true, semanticSource)
			monitor.updateShellContext(synthesizePromptContext(semanticBaseContext, semanticState))
		} else {
			monitor.updateSemanticMetadata(false, "")
		}

		if !started && sawTrackedCommandStart(captured, markers) {
			started = true
			monitor.setState(MonitorStateRunning)
			logging.Trace(
				"shell.tracked.started",
				"pane", paneID,
				"command", command,
				"command_id", markers.CommandID,
				"capture_preview", logging.Preview(captured, 1000),
			)
		}
		if !started && trackedCommandLikelyStarted(beforeCapture, captured) {
			started = true
			logging.Trace(
				"shell.tracked.started_inferred",
				"pane", paneID,
				"command", command,
				"command_id", markers.CommandID,
				"delta_preview", logging.Preview(capturePaneDelta(beforeCapture, captured), 1000),
			)
		}
		if !started && hasSemanticState && semanticState.Event == semanticEventCommand && !semanticState.UpdatedAt.Before(commandSentAt) {
			started = true
			monitor.setState(classifyActiveMonitorState(command, tail, alternateOn, currentPaneCommand))
			logging.Trace(
				"shell.tracked.started_inferred_by_semantic_state",
				"pane", paneID,
				"command", command,
				"command_id", markers.CommandID,
				"pane_command", currentPaneCommand,
				"semantic_event", semanticState.Event,
			)
		}
		if started {
			monitor.setState(classifyActiveMonitorState(command, tail, alternateOn, currentPaneCommand))
		}

		result, complete, err := protocol.ParseCommandResult(captured, markers)
		cause := CompletionCauseEndMarker
		confidence := ConfidenceStrong
		if err != nil {
			logging.TraceError(
				"shell.tracked.parse_error",
				err,
				"pane", paneID,
				"command", command,
				"command_id", markers.CommandID,
				"capture_preview", logging.Preview(captured, 1000),
			)
			monitor.finish(TrackedExecution{
				CommandID:  markers.CommandID,
				Command:    command,
				Cause:      CompletionCauseUnknown,
				Confidence: ConfidenceLow,
				Captured:   tail,
			}, fmt.Errorf("parse tracked command result: %w", err), MonitorStateLost)
			return
		}
		if !complete {
			result, complete, err = inferTrackedCommandResultFromEndMarker(captured, beforeCapture, transportCommand, markers)
			if complete {
				cause = CompletionCauseEndMarkerInferred
				confidence = ConfidenceMedium
			}
			if err != nil {
				logging.TraceError(
					"shell.tracked.inferred_parse_error",
					err,
					"pane", paneID,
					"command", command,
					"command_id", markers.CommandID,
					"capture_preview", logging.Preview(captured, 1000),
				)
				monitor.finish(TrackedExecution{
					CommandID:  markers.CommandID,
					Command:    command,
					Cause:      CompletionCauseUnknown,
					Confidence: ConfidenceLow,
					Captured:   tail,
				}, fmt.Errorf("parse tracked command result from end marker: %w", err), MonitorStateLost)
				return
			}
		}

		if complete {
			cleanBody := sanitizeCapturedBody(result.Body)
			cleanBody = stripEchoedCommand(cleanBody, transportCommand)
			shellContext, _ := ParsePromptContextFromCapture(captured)
			logging.Trace(
				"shell.tracked.complete",
				"pane", paneID,
				"command", command,
				"command_id", result.CommandID,
				"exit_code", result.ExitCode,
				"captured_preview", logging.Preview(cleanBody, 2000),
				"captured_len", len(cleanBody),
				"prompt", shellContext.PromptLine(),
			)
			monitor.finish(TrackedExecution{
				CommandID:    result.CommandID,
				Command:      command,
				Cause:        cause,
				Confidence:   confidence,
				ExitCode:     result.ExitCode,
				Captured:     cleanBody,
				ShellContext: shellContext,
			}, nil, MonitorStateCompleted)
			return
		}

		if started && hasSemanticState && semanticState.Event == semanticEventPrompt && !semanticState.UpdatedAt.Before(commandSentAt) {
			promptContext := monitor.Snapshot().ShellContext
			if promptContext.PromptLine() == "" {
				promptContext = synthesizePromptContext(o.promptHint, semanticState)
			}
			cleanBody := sanitizeCapturedBody(capturePaneDelta(beforeCapture, captured))
			cleanBody = stripEchoedCommand(cleanBody, transportCommand)
			if promptContext.PromptLine() != "" {
				cleanBody = stripTrailingPromptLine(cleanBody, promptContext)
			}
			exitCode := 0
			if semanticState.ExitCode != nil {
				exitCode = *semanticState.ExitCode
			}
			state := MonitorStateCompleted
			switch exitCode {
			case InterruptedExitCode:
				state = MonitorStateCanceled
			case 0:
				state = MonitorStateCompleted
			default:
				state = MonitorStateFailed
			}
			logging.Trace(
				"shell.tracked.semantic_prompt_returned",
				"pane", paneID,
				"command", command,
				"command_id", markers.CommandID,
				"exit_code", exitCode,
				"state", state,
				"prompt", promptContext.PromptLine(),
				"capture_preview", logging.Preview(cleanBody, 1200),
			)
			monitor.finish(TrackedExecution{
				CommandID:      markers.CommandID,
				Command:        command,
				Cause:          CompletionCausePromptReturn,
				Confidence:     ConfidenceStrong,
				SemanticShell:  true,
				SemanticSource: semanticSource,
				ExitCode:       exitCode,
				Captured:       cleanBody,
				ShellContext:   promptContext,
			}, nil, state)
			return
		}

		if started && !hasSemanticState && allowPromptReturnInference(command, alternateOn, currentPaneCommand) {
			promptContext := monitor.Snapshot().ShellContext
			if TailSuggestsPromptReturn(captured, promptContext) {
				cleanBody := sanitizeCapturedBody(capturePaneDelta(beforeCapture, captured))
				cleanBody = stripEchoedCommand(cleanBody, transportCommand)
				if promptContext.PromptLine() != "" {
					cleanBody = stripTrailingPromptLine(cleanBody, promptContext)
				}
				confidence := ConfidenceMedium
				if paneCommandIsShell(currentPaneCommand) {
					confidence = ConfidenceStrong
				}
				logging.Trace(
					"shell.tracked.prompt_returned",
					"pane", paneID,
					"command", command,
					"command_id", markers.CommandID,
					"captured_preview", logging.Preview(cleanBody, 1200),
					"prompt", promptContext.PromptLine(),
					"pane_command", currentPaneCommand,
					"confidence", confidence,
				)
				monitor.finish(TrackedExecution{
					CommandID:    markers.CommandID,
					Command:      command,
					Cause:        CompletionCausePromptReturn,
					Confidence:   confidence,
					ExitCode:     InterruptedExitCode,
					Captured:     cleanBody,
					ShellContext: promptContext,
				}, nil, MonitorStateCanceled)
				return
			}
		}

		if !started && time.Now().After(startDeadline) {
			inferredState := classifyActiveMonitorState(command, tail, alternateOn, currentPaneCommand)
			if inferredState == MonitorStateAwaitingInput || inferredState == MonitorStateInteractiveFullscreen {
				started = true
				monitor.setState(inferredState)
				logging.Trace(
					"shell.tracked.started_inferred_by_state",
					"pane", paneID,
					"command", command,
					"command_id", markers.CommandID,
					"state", inferredState,
					"pane_command", currentPaneCommand,
					"tail_preview", logging.Preview(tail, 1000),
				)
				continue
			}
			err := fmt.Errorf("timed out waiting for tracked command %s to start", markers.CommandID)
			logging.Trace(
				"shell.tracked.start_timeout",
				"pane", paneID,
				"command", command,
				"command_id", markers.CommandID,
				"capture_preview", logging.Preview(captured, 2000),
			)
			monitor.finish(TrackedExecution{
				CommandID:  markers.CommandID,
				Command:    command,
				Cause:      CompletionCauseUnknown,
				Confidence: ConfidenceLow,
				Captured:   tail,
			}, err, MonitorStateLost)
			return
		}

		select {
		case <-ctx.Done():
			logging.TraceError(
				"shell.tracked.canceled",
				ctx.Err(),
				"pane", paneID,
				"command", command,
				"command_id", markers.CommandID,
				"started", started,
				"last_capture_preview", logging.Preview(lastCapture, 1000),
			)
			monitor.finish(TrackedExecution{CommandID: markers.CommandID, Command: command}, ctx.Err(), monitorStateFromError(ctx.Err()))
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func classifyActiveMonitorState(command string, tail string, alternateOn bool, currentPaneCommand string) MonitorState {
	if alternateOn {
		return MonitorStateInteractiveFullscreen
	}
	if foregroundCommandSuggestsFullscreen(currentPaneCommand) {
		return MonitorStateInteractiveFullscreen
	}
	if TailSuggestsAwaitingInput(tail) {
		return MonitorStateAwaitingInput
	}
	if foregroundCommandSuggestsAwaitingInput(currentPaneCommand) {
		return MonitorStateAwaitingInput
	}
	if IsInteractiveCommand(command) {
		return MonitorStateAwaitingInput
	}
	return MonitorStateRunning
}

func allowPromptReturnInference(command string, alternateOn bool, currentPaneCommand string) bool {
	transition := detectShellTransition(command, currentPaneCommand, PromptContext{}, shellTransitionNone)
	return !alternateOn && !IsInteractiveCommand(command) && (transition.Kind == shellTransitionNone || transition.Kind == shellTransitionRemote) && paneCommandAllowsPromptInference(currentPaneCommand)
}

func monitorStateFromError(err error) MonitorState {
	if err == nil {
		return MonitorStateCompleted
	}
	if errors.Is(err, context.Canceled) {
		return MonitorStateCanceled
	}
	return MonitorStateLost
}

func trackedCommandLikelyStarted(beforeCapture string, captured string) bool {
	return strings.TrimSpace(capturePaneDelta(beforeCapture, captured)) != ""
}

func paneCommandIsShell(command string) bool {
	switch strings.TrimSpace(strings.ToLower(command)) {
	case "bash", "zsh", "sh", "fish", "dash", "ash", "ksh", "csh", "tcsh":
		return true
	default:
		return false
	}
}

func paneCommandAllowsPromptInference(command string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(command))
	if trimmed == "" {
		return true
	}
	if paneCommandIsShell(trimmed) {
		return true
	}
	switch trimmed {
	case "ssh", "mosh-client", "mosh":
		return true
	default:
		return false
	}
}

func foregroundCommandSuggestsFullscreen(command string) bool {
	switch strings.TrimSpace(strings.ToLower(command)) {
	case "vi", "vim", "nvim", "nano", "emacs", "less", "more", "man", "top", "htop", "btop", "watch", "tmux", "screen":
		return true
	default:
		return false
	}
}

func foregroundCommandSuggestsAwaitingInput(command string) bool {
	switch strings.TrimSpace(strings.ToLower(command)) {
	case "sudo", "doas", "passwd", "su":
		return true
	default:
		return false
	}
}

func inferTrackedCommandResultFromEndMarker(captured string, beforeCapture string, command string, markers protocol.Markers) (protocol.CommandResult, bool, error) {
	lines := strings.Split(strings.ReplaceAll(captured, "\r\n", "\n"), "\n")
	endIndex := -1
	exitCode := 0
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(line, markers.EndPrefix) {
			continue
		}

		exitValue := strings.TrimPrefix(line, markers.EndPrefix)
		if exitValue == "" {
			return protocol.CommandResult{}, false, nil
		}

		parsedExitCode, err := strconv.Atoi(exitValue)
		if err != nil {
			return protocol.CommandResult{}, false, fmt.Errorf("parse end marker exit code from %q: %w", line, err)
		}

		endIndex = index
		exitCode = parsedExitCode
		break
	}

	if endIndex == -1 {
		return protocol.CommandResult{}, false, nil
	}

	delta := capturePaneDelta(beforeCapture, captured)
	return protocol.CommandResult{
		CommandID: markers.CommandID,
		ExitCode:  exitCode,
		Body:      stripEchoedCommand(sanitizeCapturedBody(delta), command),
	}, true, nil
}

func sawTrackedCommandStart(captured string, markers protocol.Markers) bool {
	for _, line := range strings.Split(captured, "\n") {
		if strings.TrimSpace(line) == markers.BeginLine {
			return true
		}
	}

	return false
}

func CommandTimeout(command string) time.Duration {
	if isContextTransitionCommand(command) {
		return ContextTransitionCommandTimeout
	}

	return DefaultCommandTimeout
}

func IsInteractiveCommand(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return false
	}
	if isContextTransitionCommand(command) {
		return true
	}

	index := 0
	for index < len(fields) && strings.Contains(fields[index], "=") && !strings.HasPrefix(fields[index], "-") {
		index++
	}
	if index >= len(fields) {
		return false
	}

	commandName := fields[index]
	switch commandName {
	case "sudo", "doas", "passwd", "su":
		return true
	case "vi", "vim", "nvim", "nano", "emacs", "less", "more", "man", "top", "htop", "btop", "watch", "tmux", "screen":
		return true
	default:
		return false
	}
}

func (o *Observer) runContextTransitionCommand(ctx context.Context, paneID string, command string, timeout time.Duration) (TrackedExecution, error) {
	logging.Trace(
		"shell.context_transition.start",
		"pane", paneID,
		"command", command,
		"timeout_ms", timeout.Milliseconds(),
	)
	effectiveTimeout := timeout
	if effectiveTimeout < 45*time.Second {
		effectiveTimeout = 45 * time.Second
	}

	beforeCapture, err := o.capturePane(ctx, paneID, -200)
	if err != nil {
		logging.TraceError("shell.context_transition.capture_before_error", err, "pane", paneID, "command", command)
		return TrackedExecution{}, fmt.Errorf("capture pane before context transition: %w", err)
	}

	baselineContext, _ := ParsePromptContextFromCapture(beforeCapture)
	if err := o.sendKeys(ctx, paneID, command, true); err != nil {
		logging.TraceError("shell.context_transition.send_error", err, "pane", paneID, "command", command)
		return TrackedExecution{}, fmt.Errorf("send context transition command: %w", err)
	}

	deadline := time.Now().Add(effectiveTimeout)
	promptCapture := beforeCapture
	promptContext := baselineContext
	for {
		captured, err := o.capturePane(ctx, paneID, -200)
		if err != nil {
			logging.TraceError("shell.context_transition.capture_after_error", err, "pane", paneID, "command", command)
			return TrackedExecution{}, fmt.Errorf("capture pane after context transition: %w", err)
		}

		candidate, ok := ParsePromptContextFromCapture(captured)
		if ok && promptReturnedAfterTransition(beforeCapture, baselineContext, candidate, captured) {
			promptCapture = captured
			promptContext = candidate
			logging.Trace(
				"shell.context_transition.prompt_returned",
				"pane", paneID,
				"command", command,
				"prompt", promptContext.PromptLine(),
				"capture_preview", logging.Preview(captured, 1200),
			)
			break
		}

		if time.Now().After(deadline) {
			logging.Trace(
				"shell.context_transition.timeout",
				"pane", paneID,
				"command", command,
				"capture_preview", logging.Preview(captured, 1200),
			)
			return TrackedExecution{}, fmt.Errorf("timed out waiting for context transition command to settle")
		}

		select {
		case <-ctx.Done():
			logging.TraceError("shell.context_transition.canceled", ctx.Err(), "pane", paneID, "command", command)
			return TrackedExecution{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	probeResult, err := o.runProbeCommand(ctx, paneID, 10*time.Second)
	delta := sanitizeCapturedBody(capturePaneDelta(beforeCapture, promptCapture))
	delta = stripEchoedCommand(delta, command)
	delta = stripTrailingPromptLine(delta, promptContext)
	delta = strings.TrimSpace(delta)
	if delta == "" {
		line := strings.TrimSpace(promptContext.PromptLine())
		if line != "" {
			delta = "shell context updated: " + line
		} else {
			delta = "shell context updated"
		}
	}

	exitCode := 0
	commandID := ""
	if err == nil {
		commandID = probeResult.CommandID
		probeOutput, parsedContext, parsedExitCode := parseShellContextProbeOutput(probeResult.Captured, promptContext)
		if parsedContext.PromptLine() != "" {
			promptContext = parsedContext
		}
		exitCode = parsedExitCode
		if probeOutput != "" {
			delta = strings.TrimSpace(delta + "\n" + probeOutput)
		}
	} else {
		logging.TraceError("shell.context_transition.probe_error", err, "pane", paneID, "command", command)
	}

	currentPaneCommand := ""
	if paneInfo, paneErr := o.paneInfo(ctx, paneID); paneErr == nil {
		currentPaneCommand = strings.TrimSpace(paneInfo.CurrentCommand)
	}
	o.finalizeTransitionState(ctx, paneID, command, currentPaneCommand, promptContext)

	logging.Trace(
		"shell.context_transition.complete",
		"pane", paneID,
		"command", command,
		"command_id", commandID,
		"exit_code", exitCode,
		"delta_preview", logging.Preview(delta, 1200),
		"prompt", promptContext.PromptLine(),
	)

	return TrackedExecution{
		CommandID:    commandID,
		Command:      command,
		Cause:        CompletionCauseContextTransition,
		Confidence:   ConfidenceMedium,
		ExitCode:     exitCode,
		Captured:     delta,
		ShellContext: promptContext,
	}, nil
}

func (o *Observer) finalizeTransitionState(ctx context.Context, paneID string, submittedCommand string, currentPaneCommand string, promptContext PromptContext) {
	kind := settledShellTransition(submittedCommand, currentPaneCommand, promptContext, o.rememberedTransition(paneID))
	if kind == shellTransitionLocal {
		installed, err := o.ensureLocalShellIntegration(ctx, paneID)
		if err != nil {
			logging.TraceError("shell.semantic_bootstrap.error", err, "pane", paneID, "command", submittedCommand, "pane_command", currentPaneCommand)
		}
		if installed {
			kind = shellTransitionNone
		}
	}
	o.rememberTransition(paneID, kind)
}

func (o *Observer) runProbeCommand(ctx context.Context, paneID string, timeout time.Duration) (TrackedExecution, error) {
	logging.Trace(
		"shell.probe.start",
		"pane", paneID,
		"timeout_ms", timeout.Milliseconds(),
	)
	markers := protocol.NewMarkers()
	wrapped := protocol.WrapCommand(shellContextProbeCommand, markers)

	if err := o.sendKeys(ctx, paneID, wrapped, true); err != nil {
		logging.TraceError("shell.probe.send_error", err, "pane", paneID, "command_id", markers.CommandID)
		return TrackedExecution{}, fmt.Errorf("send shell context probe: %w", err)
	}

	deadline := time.Now().Add(timeout)
	lastCapture := ""
	for {
		captured, err := o.capturePane(ctx, paneID, -trackedCaptureLines)
		if err != nil {
			logging.TraceError("shell.probe.capture_error", err, "pane", paneID, "command_id", markers.CommandID)
			return TrackedExecution{}, fmt.Errorf("capture pane for shell context probe: %w", err)
		}
		lastCapture = captured

		result, complete, err := protocol.ParseCommandResult(captured, markers)
		if err != nil {
			logging.TraceError(
				"shell.probe.parse_error",
				err,
				"pane", paneID,
				"command_id", markers.CommandID,
				"capture_preview", logging.Preview(captured, 1200),
			)
			return TrackedExecution{}, fmt.Errorf("parse shell context probe result: %w", err)
		}

		if complete {
			cleanBody := sanitizeCapturedBody(result.Body)
			cleanBody = stripEchoedCommand(cleanBody, shellContextProbeCommand)
			shellContext, _ := ParsePromptContextFromCapture(captured)
			logging.Trace(
				"shell.probe.complete",
				"pane", paneID,
				"command_id", result.CommandID,
				"exit_code", result.ExitCode,
				"captured_preview", logging.Preview(cleanBody, 1200),
				"prompt", shellContext.PromptLine(),
			)
			return TrackedExecution{
				CommandID:    result.CommandID,
				Command:      shellContextProbeCommand,
				ExitCode:     result.ExitCode,
				Captured:     cleanBody,
				ShellContext: shellContext,
			}, nil
		}

		if time.Now().After(deadline) {
			logging.Trace(
				"shell.probe.timeout",
				"pane", paneID,
				"command_id", markers.CommandID,
				"capture_preview", logging.Preview(captured, 1200),
			)
			return TrackedExecution{}, fmt.Errorf("timed out waiting for shell context probe")
		}

		select {
		case <-ctx.Done():
			logging.TraceError(
				"shell.probe.canceled",
				ctx.Err(),
				"pane", paneID,
				"command_id", markers.CommandID,
				"last_capture_preview", logging.Preview(lastCapture, 1200),
			)
			return TrackedExecution{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func sanitizeCapturedBody(body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}

	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	filtered := make([]string, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.Contains(trimmed, "__SHUTTLE_E__:") {
			continue
		}

		if strings.Contains(trimmed, "__SHUTTLE_B__:") {
			continue
		}

		if wrappedSentinelSuffixPattern.MatchString(trimmed) {
			continue
		}

		filtered = append(filtered, line)
	}

	return strings.TrimSpace(strings.Join(stripShuttlePlumbingLines(filtered), "\n"))
}

func (o *Observer) captureSemanticShellState(ctx context.Context, paneID string, paneTTY string, submittedCommand string, currentPaneCommand string, promptContext PromptContext) (semanticShellState, string, bool) {
	if shouldIgnoreLocalSemanticState(submittedCommand, currentPaneCommand, promptContext, o.rememberedTransition(paneID)) {
		return semanticShellState{}, semanticSourceNone, false
	}
	for _, collector := range o.semanticCollectors() {
		observation, ok := collector.Collect(ctx, paneID, paneTTY, currentPaneCommand, promptContext)
		if ok {
			return observation.State, observation.Source, true
		}
	}
	return semanticShellState{}, semanticSourceNone, false
}

func shouldIgnoreLocalSemanticState(submittedCommand string, currentPaneCommand string, promptContext PromptContext, remembered shellTransitionKind) bool {
	switch detectShellTransition(submittedCommand, currentPaneCommand, promptContext, remembered).Kind {
	case shellTransitionRemote, shellTransitionExec, shellTransitionLocal, shellTransitionUnknown:
		return true
	default:
		return false
	}
}

func (o *Observer) rememberedTransition(paneID string) shellTransitionKind {
	o.transitionMu.Lock()
	defer o.transitionMu.Unlock()
	if o.transitions == nil {
		return shellTransitionNone
	}
	return o.transitions[paneID]
}

func (o *Observer) rememberTransition(paneID string, kind shellTransitionKind) {
	o.transitionMu.Lock()
	defer o.transitionMu.Unlock()
	if o.transitions == nil {
		o.transitions = make(map[string]shellTransitionKind)
	}
	if kind == shellTransitionNone {
		delete(o.transitions, paneID)
		return
	}
	o.transitions[paneID] = kind
}

type paneListClient interface {
	ListPanes(ctx context.Context, target string) ([]tmux.Pane, error)
}

type pipePaneClient interface {
	PipePaneOutput(ctx context.Context, target string, shellCommand string) error
}

func (o *Observer) capturePane(ctx context.Context, paneID string, startLine int) (string, error) {
	target, err := o.resolvePaneID(ctx, paneID)
	if err != nil {
		return "", err
	}
	captured, err := o.client.CapturePane(ctx, target, startLine)
	if err == nil || (!isPaneNotFoundError(err) && !shouldRecoverObserverSession(err)) {
		return captured, err
	}
	target, err = o.recoverActionTarget(ctx, paneID, err)
	if err != nil {
		return "", err
	}
	return o.client.CapturePane(ctx, target, startLine)
}

func (o *Observer) sendKeys(ctx context.Context, paneID string, command string, pressEnter bool) error {
	target, err := o.resolvePaneID(ctx, paneID)
	if err != nil {
		return err
	}
	err = o.client.SendKeys(ctx, target, command, pressEnter)
	if err == nil || (!isPaneNotFoundError(err) && !shouldRecoverObserverSession(err)) {
		return err
	}
	target, err = o.recoverActionTarget(ctx, paneID, err)
	if err != nil {
		return err
	}
	return o.client.SendKeys(ctx, target, command, pressEnter)
}

func (o *Observer) paneInfo(ctx context.Context, paneID string) (tmux.Pane, error) {
	target, err := o.resolvePaneID(ctx, paneID)
	if err != nil {
		return tmux.Pane{}, err
	}
	info, err := o.client.PaneInfo(ctx, target)
	if err == nil || (!isPaneNotFoundError(err) && !shouldRecoverObserverSession(err)) {
		return info, err
	}
	target, err = o.recoverActionTarget(ctx, paneID, err)
	if err != nil {
		return tmux.Pane{}, err
	}
	return o.client.PaneInfo(ctx, target)
}

func (o *Observer) PipePaneOutput(ctx context.Context, paneID string, shellCommand string) error {
	client, ok := o.client.(pipePaneClient)
	if !ok {
		return fmt.Errorf("pipe-pane output is not supported by the tmux client")
	}

	target, err := o.resolvePaneID(ctx, paneID)
	if err != nil {
		return err
	}
	err = client.PipePaneOutput(ctx, target, shellCommand)
	if err == nil || (!isPaneNotFoundError(err) && !shouldRecoverObserverSession(err)) {
		return err
	}
	target, err = o.recoverActionTarget(ctx, paneID, err)
	if err != nil {
		return err
	}
	return client.PipePaneOutput(ctx, target, shellCommand)
}

func (o *Observer) resolvePaneID(ctx context.Context, paneID string) (string, error) {
	if alias := o.paneAlias(paneID); alias != "" {
		if _, err := o.client.PaneInfo(ctx, alias); err == nil {
			return alias, nil
		}
	}
	return strings.TrimSpace(paneID), nil
}

func (o *Observer) recoverPaneID(ctx context.Context, paneID string) (string, error) {
	replacement, ok := o.findReplacementPaneID(ctx)
	if !ok {
		return "", fmt.Errorf("pane %s no longer exists and no replacement pane was found", paneID)
	}
	o.setPaneAlias(paneID, replacement)
	return replacement, nil
}

func (o *Observer) recoverActionTarget(ctx context.Context, paneID string, cause error) (string, error) {
	if shouldRecoverObserverSession(cause) {
		if err := o.ensureSessionAvailable(ctx); err != nil {
			return "", err
		}
		if target := strings.TrimSpace(paneID); target != "" {
			if _, err := o.client.PaneInfo(ctx, target); err == nil {
				return target, nil
			}
		}
	}
	return o.recoverPaneID(ctx, paneID)
}

func (o *Observer) findReplacementPaneID(ctx context.Context) (string, bool) {
	if strings.TrimSpace(o.sessionName) == "" {
		return "", false
	}
	listClient, ok := o.client.(paneListClient)
	if !ok {
		return "", false
	}
	panes, err := listClient.ListPanes(ctx, o.sessionName)
	if err != nil && shouldRecoverObserverSession(err) {
		if ensureErr := o.ensureSessionAvailable(ctx); ensureErr == nil {
			panes, err = listClient.ListPanes(ctx, o.sessionName)
		}
	}
	if err != nil || len(panes) == 0 {
		return "", false
	}

	best := panes[0]
	for _, pane := range panes[1:] {
		if pane.Top < best.Top || (pane.Top == best.Top && pane.Left < best.Left) {
			best = pane
		}
	}
	return strings.TrimSpace(best.ID), strings.TrimSpace(best.ID) != ""
}

func (o *Observer) ensureSessionAvailable(ctx context.Context) error {
	if o.sessionEnsurer != nil {
		return o.sessionEnsurer(ctx)
	}
	if o.tmuxClient == nil {
		return fmt.Errorf("tmux client is not available for workspace recovery")
	}
	if strings.TrimSpace(o.sessionName) == "" {
		return fmt.Errorf("session name is required for workspace recovery")
	}
	startDir := strings.TrimSpace(o.startDir)
	if startDir == "" {
		startDir = strings.TrimSpace(o.promptHint.Directory)
	}
	if startDir == "" {
		startDir = "."
	}
	_, _, err := tmux.BootstrapWorkspace(ctx, o.tmuxClient, tmux.BootstrapOptions{
		SessionName:       o.sessionName,
		StartDir:          startDir,
		BottomPanePercent: 30,
	})
	return err
}

func shouldRecoverObserverSession(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no server running") || strings.Contains(text, "can't find session")
}

func (o *Observer) paneAlias(paneID string) string {
	o.paneAliasMu.Lock()
	defer o.paneAliasMu.Unlock()
	if o.paneAliases == nil {
		return ""
	}
	return o.paneAliases[paneID]
}

func (o *Observer) setPaneAlias(paneID string, replacement string) {
	o.paneAliasMu.Lock()
	defer o.paneAliasMu.Unlock()
	if o.paneAliases == nil {
		o.paneAliases = make(map[string]string)
	}
	o.paneAliases[paneID] = replacement
}

func isPaneNotFoundError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "can't find pane")
}

func stripShuttlePlumbingLines(lines []string) []string {
	filtered := make([]string, 0, len(lines))
	droppingContinuation := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if isShuttlePlumbingLine(trimmed) {
			if len(filtered) > 0 {
				last := strings.TrimSpace(filtered[len(filtered)-1])
				if last == "." || lineLooksLikeSourcedDotPrompt(last) {
					filtered = filtered[:len(filtered)-1]
				}
			}
			droppingContinuation = true
			continue
		}

		if droppingContinuation {
			if isShuttleContinuationLine(trimmed) {
				continue
			}
			droppingContinuation = false
		}

		filtered = append(filtered, line)
	}

	return filtered
}

func isShuttlePlumbingLine(line string) bool {
	return strings.Contains(line, "__SHUTTLE_") ||
		strings.Contains(line, "__shuttle_status") ||
		strings.Contains(line, "eval \"$(printf") ||
		strings.Contains(line, "SHUTTLE_SEMANTIC_SHELL_V1") ||
		strings.Contains(line, "/shell-integration/") ||
		strings.Contains(line, "/commands/")
}

func isShuttleContinuationLine(line string) bool {
	return strings.Contains(line, "__SHUTTLE_") ||
		strings.Contains(line, "__shuttle_status") ||
		strings.Contains(line, "eval \"$(printf") ||
		strings.HasPrefix(line, "|| ") ||
		strings.HasPrefix(line, "| . ") ||
		strings.Contains(line, "$(whoami") ||
		strings.Contains(line, "$(hostname") ||
		strings.Contains(line, "$(uname") ||
		strings.Contains(line, "\"$PWD\"") ||
		strings.HasPrefix(line, ">/dev/null") ||
		strings.HasSuffix(line, "2>&1") ||
		strings.HasSuffix(line, "2>&1;")
}

func lineLooksLikeSourcedDotPrompt(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasSuffix(line, "% .") ||
		strings.HasSuffix(line, "$ .") ||
		strings.HasSuffix(line, "# .") ||
		strings.HasSuffix(line, "> .")
}

func stripEchoedCommand(body string, command string) string {
	if strings.TrimSpace(body) == "" || strings.TrimSpace(command) == "" {
		return strings.TrimSpace(body)
	}

	bodyLines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	commandLines := strings.Split(strings.ReplaceAll(command, "\r\n", "\n"), "\n")

	for len(commandLines) > 0 && commandLines[len(commandLines)-1] == "" {
		commandLines = commandLines[:len(commandLines)-1]
	}
	if len(commandLines) == 0 || len(bodyLines) < len(commandLines) {
		return strings.TrimSpace(body)
	}

	if stripped, ok := stripEchoedCommandLines(bodyLines, commandLines); ok {
		return stripped
	}
	if stripped, ok := stripWrappedEchoedSingleLine(bodyLines, command); ok {
		return stripped
	}

	if len(bodyLines) > len(commandLines) && lineLooksLikePrompt(bodyLines[0]) {
		if stripped, ok := stripEchoedCommandLines(bodyLines[1:], commandLines); ok {
			return stripped
		}
		if stripped, ok := stripWrappedEchoedSingleLine(bodyLines[1:], command); ok {
			return stripped
		}
	}

	return strings.TrimSpace(body)
}

func stripEchoedCommandLines(bodyLines []string, commandLines []string) (string, bool) {
	if len(bodyLines) < len(commandLines) {
		return "", false
	}

	for index, commandLine := range commandLines {
		if !strings.HasSuffix(strings.TrimRight(bodyLines[index], " \t"), commandLine) {
			return "", false
		}
	}

	return strings.TrimSpace(strings.Join(bodyLines[len(commandLines):], "\n")), true
}

func stripWrappedEchoedSingleLine(bodyLines []string, command string) (string, bool) {
	if strings.Contains(command, "\n") || len(bodyLines) == 0 {
		return "", false
	}

	command = strings.TrimSpace(command)
	if command == "" {
		return "", false
	}

	var joined strings.Builder
	for index, line := range bodyLines {
		joined.WriteString(strings.TrimRight(line, " \t"))
		current := joined.String()
		if current == command {
			return strings.TrimSpace(strings.Join(bodyLines[index+1:], "\n")), true
		}
		if !strings.HasPrefix(command, current) {
			return "", false
		}
	}

	return "", false
}

func lineLooksLikePrompt(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if context, ok := ParsePromptContextFromCapture(trimmed); ok {
		return strings.TrimSpace(context.RawLine) == trimmed
	}
	return false
}

func isContextTransitionCommand(command string) bool {
	return detectCommandTransition(command) != shellTransitionNone
}

func hasAnyArg(args []string, candidates ...string) bool {
	for _, arg := range args {
		for _, candidate := range candidates {
			if arg == candidate {
				return true
			}
		}
	}
	return false
}

func promptReturnedAfterTransition(beforeCapture string, baseline PromptContext, candidate PromptContext, captured string) bool {
	if candidate.PromptLine() == "" {
		return false
	}

	if baseline.RawLine != "" && candidate.RawLine != baseline.RawLine {
		return true
	}

	return strings.TrimSpace(captured) != strings.TrimSpace(beforeCapture)
}

func capturePaneDelta(before string, after string) string {
	beforeLines := strings.Split(strings.ReplaceAll(before, "\r\n", "\n"), "\n")
	afterLines := strings.Split(strings.ReplaceAll(after, "\r\n", "\n"), "\n")

	index := 0
	for index < len(beforeLines) && index < len(afterLines) && beforeLines[index] == afterLines[index] {
		index++
	}

	return strings.Join(afterLines[index:], "\n")
}

func stripTrailingPromptLine(body string, promptContext PromptContext) string {
	body = strings.TrimSpace(body)
	promptLine := strings.TrimSpace(promptContext.RawLine)
	if body == "" || promptLine == "" {
		return body
	}

	lines := strings.Split(body, "\n")
	if strings.TrimSpace(lines[len(lines)-1]) == promptLine {
		lines = lines[:len(lines)-1]
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func TrimTrailingPromptLine(body string, promptContext PromptContext) string {
	return stripTrailingPromptLine(body, promptContext)
}

func parseShellContextProbeOutput(body string, baseline PromptContext) (string, PromptContext, int) {
	context := baseline
	exitCode := 0

	for _, rawLine := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		switch {
		case strings.HasPrefix(line, "__SHUTTLE_CTX_EXIT__="):
			parsed, err := strconv.Atoi(strings.TrimPrefix(line, "__SHUTTLE_CTX_EXIT__="))
			if err == nil {
				exitCode = parsed
			}
		case strings.HasPrefix(line, "__SHUTTLE_CTX_USER__="):
			context.User = strings.TrimSpace(strings.TrimPrefix(line, "__SHUTTLE_CTX_USER__="))
			context.Root = context.User == "root" || context.PromptSymbol == "#"
		case strings.HasPrefix(line, "__SHUTTLE_CTX_HOST__="):
			context.Host = strings.TrimSpace(strings.TrimPrefix(line, "__SHUTTLE_CTX_HOST__="))
		case strings.HasPrefix(line, "__SHUTTLE_CTX_UNAME__="):
			context.System = strings.TrimSpace(strings.TrimPrefix(line, "__SHUTTLE_CTX_UNAME__="))
		case strings.HasPrefix(line, "__SHUTTLE_CTX_PWD__="):
			context.Directory = strings.TrimSpace(strings.TrimPrefix(line, "__SHUTTLE_CTX_PWD__="))
		}
	}

	localHost, _ := os.Hostname()
	context.Remote = isRemoteHost(context.Host, localHost)
	if context.Root && context.PromptSymbol == "" {
		context.PromptSymbol = "#"
	}
	if context.PromptSymbol == "" {
		context.PromptSymbol = "$"
	}

	return "", context, exitCode
}

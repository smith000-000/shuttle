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
	xansi "github.com/charmbracelet/x/ansi"
)

type TrackedExecution struct {
	CommandID       string
	Command         string
	State           MonitorState
	Cause           CompletionCause
	Confidence      SignalConfidence
	SemanticShell   bool
	SemanticSource  string
	ExitCode        int
	Captured        string
	DisplayCaptured string
	ShellContext    PromptContext
	Location        ShellLocation
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
	contextTransitionProbeTimeout   = 2 * time.Second
)

type contextTransitionState string

const (
	contextTransitionSubmitted           contextTransitionState = "submitted"
	contextTransitionAwaitingInteractive contextTransitionState = "awaiting_interactive_input"
	contextTransitionCandidatePromptSeen contextTransitionState = "candidate_prompt_seen"
	contextTransitionProbeVerifying      contextTransitionState = "probe_verifying"
	contextTransitionSettled             contextTransitionState = "settled"
	contextTransitionTimedOut            contextTransitionState = "timed_out"
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
			result, err := o.runContextTransitionCommand(ctx, monitor, paneID, command, timeout)
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
	beforeDisplayCapture, err := o.capturePaneDisplay(ctx, paneID, -trackedCaptureLines)
	if err != nil {
		beforeDisplayCapture = beforeCapture
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
	go o.runTrackedMonitor(ctx, monitor, paneID, command, transportCommand, timeout, beforeCapture, beforeDisplayCapture, markers, cleanup)
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

func (o *Observer) CaptureRecentOutputDisplay(ctx context.Context, paneID string, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}

	captured, err := o.capturePaneDisplay(ctx, paneID, -lines)
	if err != nil {
		return "", fmt.Errorf("capture recent output for display: %w", err)
	}

	return sanitizeDisplayBody(captured), nil
}

func (o *Observer) CaptureShellContext(ctx context.Context, paneID string) (PromptContext, error) {
	observed, err := o.CaptureObservedShellState(ctx, paneID)
	if err != nil {
		return PromptContext{}, err
	}
	if observed.HasPromptContext {
		return observed.PromptContext, nil
	}
	return PromptContext{}, nil
}

func (o *Observer) CaptureObservedShellState(ctx context.Context, paneID string) (ObservedShellState, error) {
	captured, err := o.capturePane(ctx, paneID, -80)
	if err != nil {
		return ObservedShellState{}, fmt.Errorf("capture observed shell state: %w", err)
	}

	paneInfo, paneErr := o.paneInfo(ctx, paneID)
	hasPaneInfo := paneErr == nil
	var info *tmux.Pane
	if hasPaneInfo {
		info = &paneInfo
	}

	observed := o.observeShellState(ctx, paneID, "", captured, info, o.promptHint)
	if observed.HasPromptContext && captureHasCurrentPromptContext(captured, observed.PromptContext) {
		o.promptHint = observed.PromptContext
		return observed, nil
	}

	if observed.HasSemanticState {
		context := synthesizePromptContext(o.promptHint, observed.SemanticState)
		if captureHasCurrentPromptContext(captured, context) {
			observed.PromptContext = context
			observed.HasPromptContext = true
			observed.Location = inferShellLocation(observed.PromptContext, observed.CurrentPaneCommand, observed.RememberedTransition)
			o.promptHint = context
			return observed, nil
		}
	}

	observed.PromptContext = PromptContext{}
	observed.HasPromptContext = false
	observed.Location = inferShellLocation(PromptContext{}, observed.CurrentPaneCommand, observed.RememberedTransition)
	return observed, nil
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
	resolved := strings.TrimSpace(info.ID)
	if trimmed := strings.TrimSpace(paneID); trimmed != "" && resolved != "" && trimmed != resolved {
		logging.Trace(
			"shell.tracked_pane.resolved",
			"requested_pane", trimmed,
			"resolved_pane", resolved,
			"session", o.sessionName,
		)
	}
	return resolved, nil
}

func (o *Observer) runTrackedMonitor(ctx context.Context, monitor *trackedCommandMonitor, paneID string, command string, transportCommand string, timeout time.Duration, beforeCapture string, beforeDisplayCapture string, markers protocol.Markers, cleanup func()) {
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
				CommandID:       markers.CommandID,
				Command:         command,
				Cause:           CompletionCauseUnknown,
				Confidence:      ConfidenceLow,
				Captured:        monitorTail(lastCapture, transportCommand),
				DisplayCaptured: monitorTail(lastCapture, transportCommand),
			}, fmt.Errorf("capture pane: %w", err), MonitorStateLost)
			return
		}
		rawCaptured := captured
		captured = sanitizeCapturedBody(captured)
		lastCapture = rawCaptured
		if lastPaneInfoCheck.IsZero() || time.Since(lastPaneInfoCheck) >= 200*time.Millisecond {
			lastPaneInfoCheck = time.Now()
			if paneInfo, paneErr := o.paneInfo(ctx, paneID); paneErr == nil {
				alternateOn = paneInfo.AlternateOn
				currentPaneCommand = strings.TrimSpace(paneInfo.CurrentCommand)
				paneTTY = strings.TrimSpace(paneInfo.TTY)
				monitor.updateForegroundCommand(currentPaneCommand)
			}
		}
		paneInfo := tmux.Pane{CurrentCommand: currentPaneCommand, TTY: paneTTY, AlternateOn: alternateOn}
		observed := o.observeShellState(ctx, paneID, command, rawCaptured, &paneInfo, monitor.Snapshot().ShellContext)
		tail := observed.Tail
		monitor.updateTail(tail, tail)
		if observed.HasPromptContext {
			monitor.updateShellContext(observed.PromptContext)
		}
		if observed.HasSemanticState {
			monitor.updateSemanticMetadata(true, observed.SemanticSource)
		} else {
			monitor.updateSemanticMetadata(false, "")
		}

		if !started && sawTrackedCommandStart(rawCaptured, markers) {
			started = true
			monitor.setState(MonitorStateRunning)
			logging.Trace(
				"shell.tracked.started",
				"pane", paneID,
				"command", command,
				"command_id", markers.CommandID,
				"capture_preview", logging.Preview(rawCaptured, 1000),
			)
		}
		if !started && trackedCommandLikelyStarted(beforeCapture, rawCaptured) {
			started = true
			logging.Trace(
				"shell.tracked.started_inferred",
				"pane", paneID,
				"command", command,
				"command_id", markers.CommandID,
				"delta_preview", logging.Preview(capturePaneDelta(beforeCapture, rawCaptured), 1000),
			)
		}
		if !started && observed.HasSemanticState && observed.SemanticState.Event == semanticEventCommand && !observed.SemanticState.UpdatedAt.Before(commandSentAt) {
			started = true
			monitor.setState(classifyActiveMonitorState(command, observed))
			logging.Trace(
				"shell.tracked.started_inferred_by_semantic_state",
				"pane", paneID,
				"command", command,
				"command_id", markers.CommandID,
				"pane_command", currentPaneCommand,
				"semantic_event", observed.SemanticState.Event,
			)
		}
		if started {
			monitor.setState(classifyActiveMonitorState(command, observed))
		}

		result, complete, err := protocol.ParseCommandResult(rawCaptured, markers)
		cause := CompletionCauseEndMarker
		confidence := ConfidenceStrong
		if err != nil {
			logging.TraceError(
				"shell.tracked.parse_error",
				err,
				"pane", paneID,
				"command", command,
				"command_id", markers.CommandID,
				"capture_preview", logging.Preview(rawCaptured, 1000),
			)
			monitor.finish(TrackedExecution{
				CommandID:       markers.CommandID,
				Command:         command,
				Cause:           CompletionCauseUnknown,
				Confidence:      ConfidenceLow,
				Captured:        tail,
				DisplayCaptured: tail,
			}, fmt.Errorf("parse tracked command result: %w", err), MonitorStateLost)
			return
		}
		if !complete {
			result, complete, err = inferTrackedCommandResultFromEndMarker(rawCaptured, beforeCapture, transportCommand, markers)
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
					"capture_preview", logging.Preview(rawCaptured, 1000),
				)
				monitor.finish(TrackedExecution{
					CommandID:       markers.CommandID,
					Command:         command,
					Cause:           CompletionCauseUnknown,
					Confidence:      ConfidenceLow,
					Captured:        tail,
					DisplayCaptured: tail,
				}, fmt.Errorf("parse tracked command result from end marker: %w", err), MonitorStateLost)
				return
			}
		}

		if complete {
			cleanBody := sanitizeCapturedBody(result.Body)
			cleanBody = stripEchoedCommand(cleanBody, transportCommand)
			shellContext, _ := ParsePromptContextFromCapture(rawCaptured)
			displayBody := o.captureCommandDisplayDelta(ctx, paneID, beforeDisplayCapture, transportCommand, shellContext)
			if displayBody == "" {
				displayBody = cleanBody
			}
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
				CommandID:       result.CommandID,
				Command:         command,
				Cause:           cause,
				Confidence:      confidence,
				ExitCode:        result.ExitCode,
				Captured:        cleanBody,
				DisplayCaptured: displayBody,
				ShellContext:    shellContext,
			}, nil, MonitorStateCompleted)
			return
		}

		if started && observed.HasSemanticState && observed.SemanticState.Event == semanticEventPrompt && !observed.SemanticState.UpdatedAt.Before(commandSentAt) {
			evaluation, complete := evaluateSemanticPromptReturn(promptReturnInputs{
				CommandID:  markers.CommandID,
				Command:    command,
				Observed:   observed,
				Snapshot:   monitor.Snapshot(),
				PromptHint: o.promptHint,
				RawBody:    capturePaneDelta(beforeCapture, rawCaptured),
				BodyCleaner: func(body string, promptContext PromptContext) string {
					return stripTrailingPromptLine(stripEchoedCommand(sanitizeCapturedBody(body), transportCommand), promptContext)
				},
				SemanticSource: observed.SemanticSource,
			})
			if !complete {
				continue
			}
			logging.Trace(
				"shell.tracked.semantic_prompt_returned",
				"pane", paneID,
				"command", command,
				"command_id", markers.CommandID,
				"exit_code", evaluation.Result.ExitCode,
				"state", evaluation.State,
				"prompt", evaluation.Result.ShellContext.PromptLine(),
				"capture_preview", logging.Preview(evaluation.Result.Captured, 1200),
			)
			monitor.finish(evaluation.Result, nil, evaluation.State)
			return
		}

		if started && allowPromptReturnInference(command, observed) {
			promptContext := promptReturnContext(promptReturnInputs{
				Observed:   observed,
				Snapshot:   monitor.Snapshot(),
				PromptHint: o.promptHint,
			})
			if TailSuggestsPromptReturn(rawCaptured, promptContext) {
				evaluation, complete := evaluatePromptReturnInference(promptReturnInputs{
					CommandID:  markers.CommandID,
					Command:    command,
					Observed:   observed,
					Snapshot:   monitor.Snapshot(),
					PromptHint: o.promptHint,
					RawBody:    capturePaneDelta(beforeCapture, rawCaptured),
					BodyCleaner: func(body string, promptContext PromptContext) string {
						return stripTrailingPromptLine(stripEchoedCommand(sanitizeCapturedBody(body), transportCommand), promptContext)
					},
					SemanticSource: observed.SemanticSource,
				})
				if !complete {
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
					continue
				}
				logging.Trace(
					"shell.tracked.prompt_returned",
					"pane", paneID,
					"command", command,
					"command_id", markers.CommandID,
					"captured_preview", logging.Preview(evaluation.Result.Captured, 1200),
					"prompt", evaluation.Result.ShellContext.PromptLine(),
					"pane_command", currentPaneCommand,
					"confidence", evaluation.Result.Confidence,
					"exit_code", evaluation.Result.ExitCode,
					"state", evaluation.State,
					"inferred_exit", !evaluation.Result.SemanticShell && evaluation.Result.Confidence != ConfidenceStrong,
				)
				monitor.finish(evaluation.Result, nil, evaluation.State)
				return
			}
		}

		if !started && time.Now().After(startDeadline) {
			inferredState := classifyActiveMonitorState(command, observed)
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

func classifyActiveMonitorState(command string, observed ObservedShellState) MonitorState {
	if observed.AlternateOn {
		return MonitorStateInteractiveFullscreen
	}
	if foregroundCommandSuggestsFullscreen(observed.CurrentPaneCommand) {
		return MonitorStateInteractiveFullscreen
	}
	if TailSuggestsAwaitingInput(observed.Tail) {
		return MonitorStateAwaitingInput
	}
	if foregroundCommandSuggestsAwaitingInput(observed.CurrentPaneCommand) {
		return MonitorStateAwaitingInput
	}
	if IsInteractiveCommand(command) {
		return MonitorStateAwaitingInput
	}
	return MonitorStateRunning
}

func allowPromptReturnInference(command string, observed ObservedShellState) bool {
	if observed.AlternateOn || IsInteractiveCommand(command) {
		return false
	}
	switch observed.Location.Kind {
	case ShellLocationUnknown, ShellLocationLocal, ShellLocationRemote:
	default:
		return false
	}
	return paneCommandAllowsPromptInference(observed.CurrentPaneCommand)
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

		parsedExitCode, ok := parseTrackedEndMarkerExitCode(line, markers.EndPrefix)
		if !ok {
			continue
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

func parseTrackedEndMarkerExitCode(line string, prefix string) (int, bool) {
	exitValue := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if exitValue == "" {
		return 0, false
	}
	parsedExitCode, err := strconv.Atoi(exitValue)
	if err != nil {
		return 0, false
	}
	return parsedExitCode, true
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

func publishContextTransitionObservation(monitor *trackedCommandMonitor, observation transitionObservation, decision transitionTrackerDecision) {
	if monitor == nil {
		return
	}
	if strings.TrimSpace(observation.Delta) != "" {
		monitor.updateTail(observation.Delta, observation.Delta)
	}
	if observation.HasPrompt && observation.Candidate.PromptLine() != "" {
		monitor.updateShellContext(observation.Candidate)
	}
	if decision.AwaitingInput {
		monitor.setState(MonitorStateAwaitingInput)
		return
	}
	monitor.setState(MonitorStateRunning)
}

func (o *Observer) runContextTransitionCommand(ctx context.Context, monitor *trackedCommandMonitor, paneID string, command string, timeout time.Duration) (TrackedExecution, error) {
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
	if monitor != nil {
		monitor.setState(MonitorStateRunning)
	}

	deadline := time.Now().Add(effectiveTimeout)
	tracker := newContextTransitionTracker(command, beforeCapture, baselineContext)
	promptCapture := beforeCapture
	promptContext := baselineContext
	for {
		captured, err := o.capturePane(ctx, paneID, -200)
		if err != nil {
			logging.TraceError("shell.context_transition.capture_after_error", err, "pane", paneID, "command", command)
			return TrackedExecution{}, fmt.Errorf("capture pane after context transition: %w", err)
		}

		observation := newTransitionObservation(beforeCapture, captured, command)
		decision := tracker.Observe(observation)
		publishContextTransitionObservation(monitor, observation, decision)
		if decision.NeedsVerify {
			verifyCapture, verifyErr := o.capturePane(ctx, paneID, -200)
			if verifyErr != nil {
				logging.TraceError("shell.context_transition.capture_verify_error", verifyErr, "pane", paneID, "command", command)
				return TrackedExecution{}, fmt.Errorf("capture pane while verifying context transition: %w", verifyErr)
			}
			verifyObservation := newTransitionObservation(beforeCapture, verifyCapture, command)
			verifyDecision := tracker.ObserveVerification(verifyObservation)
			publishContextTransitionObservation(monitor, verifyObservation, verifyDecision)
			if verifyDecision.Settled {
				promptCapture = verifyDecision.PromptCapture
				promptContext = verifyDecision.PromptContext
				logging.Trace(
					"shell.context_transition.prompt_returned",
					"pane", paneID,
					"command", command,
					"prompt", promptContext.PromptLine(),
					"capture_preview", logging.Preview(promptCapture, 1200),
				)
				goto probe
			}
		}

		if time.Now().After(deadline) {
			tracker.state = contextTransitionTimedOut
			logging.Trace(
				"shell.context_transition.timeout",
				"pane", paneID,
				"command", command,
				"state", tracker.state,
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

probe:
	probeResult, err := o.runProbeCommand(ctx, paneID, contextTransitionProbeTimeout)
	delta := sanitizeCapturedBody(capturePaneDelta(beforeCapture, promptCapture))
	delta = stripEchoedCommand(delta, command)
	delta = stripTrailingPromptLine(delta, promptContext)
	delta = strings.TrimSpace(delta)
	usedDefaultDelta := false
	if delta == "" {
		line := strings.TrimSpace(promptContext.PromptLine())
		if line != "" {
			delta = "shell context updated: " + line
		} else {
			delta = "shell context updated"
		}
		usedDefaultDelta = true
	}

	exitCode := 0
	commandID := ""
	location := inferShellLocation(promptContext, "", o.rememberedTransition(paneID))
	if err == nil {
		commandID = probeResult.CommandID
		probeOutput, parsedContext, parsedExitCode := parseShellContextProbeOutput(probeResult.Captured, promptContext)
		if parsedContext.PromptLine() != "" {
			promptContext = parsedContext
		}
		exitCode = parsedExitCode
		if usedDefaultDelta {
			line := strings.TrimSpace(promptContext.PromptLine())
			if line != "" {
				delta = "shell context updated: " + line
			} else {
				delta = "shell context updated"
			}
		}
		if probeOutput != "" {
			delta = strings.TrimSpace(delta + "\n" + probeOutput)
		}
		location = MarkShellLocationDirectoryAuthoritative(location, promptContext.Directory)
	} else {
		logging.TraceError("shell.context_transition.probe_error", err, "pane", paneID, "command", command)
		location = MarkShellLocationDirectoryApproximate(location)
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
		"state", contextTransitionSettled,
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
		Location:     location,
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
				Captured:     result.Body,
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

func sanitizeDisplayBody(body string) string {
	if strings.TrimSpace(xansi.Strip(body)) == "" {
		return ""
	}

	rawLines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	filtered := make([]string, 0, len(rawLines))
	droppingContinuation := false

	for _, rawLine := range rawLines {
		plainLine := xansi.Strip(rawLine)
		trimmed := strings.TrimSpace(plainLine)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "__SHUTTLE_E__:") || strings.Contains(trimmed, "__SHUTTLE_B__:") || wrappedSentinelSuffixPattern.MatchString(trimmed) {
			continue
		}
		if isShuttlePlumbingLine(trimmed) {
			if len(filtered) > 0 {
				lastPlain := strings.TrimSpace(xansi.Strip(filtered[len(filtered)-1]))
				if lastPlain == "." || lineLooksLikeSourcedDotPrompt(lastPlain) {
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
		filtered = append(filtered, rawLine)
	}

	return strings.TrimSpace(strings.Join(filtered, "\n"))
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

type paneCapture struct {
	display string
	raw     string
	clean   string
}

func (o *Observer) capturePane(ctx context.Context, paneID string, startLine int) (string, error) {
	return o.capturePanePlain(ctx, paneID, startLine)
}

func (o *Observer) capturePaneOutput(ctx context.Context, paneID string, startLine int) (paneCapture, error) {
	plainCaptured, err := o.capturePanePlain(ctx, paneID, startLine)
	if err != nil {
		return paneCapture{}, err
	}
	displayCaptured, err := o.capturePaneDisplay(ctx, paneID, startLine)
	if err != nil {
		displayCaptured = plainCaptured
	}
	return paneCapture{
		display: sanitizeDisplayBody(displayCaptured),
		raw:     plainCaptured,
		clean:   sanitizeCapturedBody(plainCaptured),
	}, nil
}

func (o *Observer) capturePanePlain(ctx context.Context, paneID string, startLine int) (string, error) {
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

func (o *Observer) capturePaneDisplay(ctx context.Context, paneID string, startLine int) (string, error) {
	target, err := o.resolvePaneID(ctx, paneID)
	if err != nil {
		return "", err
	}
	captured, err := o.capturePaneDisplayFromClient(ctx, target, startLine)
	if err == nil || (!isPaneNotFoundError(err) && !shouldRecoverObserverSession(err)) {
		return captured, err
	}
	target, err = o.recoverActionTarget(ctx, paneID, err)
	if err != nil {
		return "", err
	}
	return o.capturePaneDisplayFromClient(ctx, target, startLine)
}

func (o *Observer) capturePaneDisplayFromClient(ctx context.Context, target string, startLine int) (string, error) {
	if escapedClient, ok := o.client.(escapedPaneClient); ok {
		return escapedClient.CapturePaneEscaped(ctx, target, startLine)
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
			logging.Trace(
				"shell.tracked_pane.alias_hit",
				"requested_pane", strings.TrimSpace(paneID),
				"alias_pane", alias,
				"session", o.sessionName,
			)
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
	logging.Trace(
		"shell.tracked_pane.recovered",
		"previous_pane", strings.TrimSpace(paneID),
		"replacement_pane", replacement,
		"session", o.sessionName,
	)
	return replacement, nil
}

func (o *Observer) recoverActionTarget(ctx context.Context, paneID string, cause error) (string, error) {
	logging.Trace(
		"shell.tracked_pane.recovery_begin",
		"pane", strings.TrimSpace(paneID),
		"session", o.sessionName,
		"cause", errString(cause),
	)
	if shouldRecoverObserverSession(cause) {
		if err := o.ensureSessionAvailable(ctx); err != nil {
			return "", err
		}
		if target := strings.TrimSpace(paneID); target != "" {
			if _, err := o.client.PaneInfo(ctx, target); err == nil {
				logging.Trace(
					"shell.tracked_pane.recovery_reused_original",
					"pane", target,
					"session", o.sessionName,
				)
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
	replacement := strings.TrimSpace(best.ID)
	if replacement != "" {
		logging.Trace(
			"shell.tracked_pane.replacement_candidate",
			"session", o.sessionName,
			"replacement_pane", replacement,
			"pane_count", len(panes),
		)
	}
	return replacement, replacement != ""
}

func (o *Observer) ensureSessionAvailable(ctx context.Context) error {
	if o.sessionEnsurer != nil {
		logging.Trace(
			"shell.session.ensure_begin",
			"session", o.sessionName,
			"start_dir", strings.TrimSpace(o.startDir),
			"mode", "callback",
		)
		err := o.sessionEnsurer(ctx)
		if err != nil {
			logging.TraceError("shell.session.ensure_error", err, "session", o.sessionName, "mode", "callback")
			return err
		}
		logging.Trace("shell.session.ensure_complete", "session", o.sessionName, "mode", "callback")
		return nil
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
	logging.Trace(
		"shell.session.ensure_begin",
		"session", o.sessionName,
		"start_dir", startDir,
		"mode", "bootstrap_workspace",
	)
	_, _, err := tmux.BootstrapWorkspace(ctx, o.tmuxClient, tmux.BootstrapOptions{
		SessionName:       o.sessionName,
		StartDir:          startDir,
		BottomPanePercent: 30,
	})
	if err != nil {
		logging.TraceError("shell.session.ensure_error", err, "session", o.sessionName, "mode", "bootstrap_workspace")
		return err
	}
	logging.Trace("shell.session.ensure_complete", "session", o.sessionName, "mode", "bootstrap_workspace")
	return err
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
	if isRemotePayloadMarker(line) {
		return false
	}
	if looksLikeShuttleCommandError(line) {
		return false
	}
	return strings.Contains(line, "__SHUTTLE_") ||
		strings.Contains(line, "__shuttle_status") ||
		strings.Contains(line, "eval \"$(printf") ||
		strings.Contains(line, "SHUTTLE_SEMANTIC_SHELL_V1") ||
		strings.Contains(line, "/shell-integration/") ||
		strings.Contains(line, "/commands/")
}

func looksLikeShuttleCommandError(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if !strings.Contains(lower, "/commands/") {
		return false
	}
	return strings.Contains(lower, ": command not found:") ||
		strings.Contains(lower, ": no such file or directory") ||
		strings.Contains(lower, ": permission denied") ||
		strings.Contains(lower, ".sh:")
}

func isShuttleContinuationLine(line string) bool {
	if isRemotePayloadMarker(line) {
		return false
	}
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

func isRemotePayloadMarker(line string) bool {
	trimmed := strings.TrimSpace(line)
	switch trimmed {
	case "__SHUTTLE_REMOTE_DATA_BEGIN__", "__SHUTTLE_REMOTE_DATA_END__":
		return true
	}
	return strings.HasPrefix(trimmed, "__SHUTTLE_REMOTE_READ__")
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

func promptReturnedAfterTransition(beforeCapture string, baseline PromptContext, candidate PromptContext, captured string, delta string) bool {
	if candidate.PromptLine() == "" {
		return false
	}

	if promptContextsMateriallyDiffer(baseline, candidate) {
		return true
	}

	if strings.TrimSpace(delta) != "" {
		if TailSuggestsAwaitingInput(delta) {
			return false
		}
		return true
	}

	if baseline.RawLine != "" && candidate.RawLine == baseline.RawLine {
		return false
	}

	return strings.TrimSpace(captured) != strings.TrimSpace(beforeCapture)
}

func promptContextsMateriallyDiffer(left PromptContext, right PromptContext) bool {
	return strings.TrimSpace(left.User) != strings.TrimSpace(right.User) ||
		strings.TrimSpace(left.Host) != strings.TrimSpace(right.Host) ||
		strings.TrimSpace(left.Directory) != strings.TrimSpace(right.Directory) ||
		strings.TrimSpace(left.PromptSymbol) != strings.TrimSpace(right.PromptSymbol) ||
		left.Remote != right.Remote
}

func promptContextsEquivalent(left PromptContext, right PromptContext) bool {
	return !promptContextsMateriallyDiffer(left, right)
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
	lastLine := strings.TrimSpace(lines[len(lines)-1])
	if lastLine == promptLine || strings.TrimSpace(xansi.Strip(lastLine)) == promptLine {
		lines = lines[:len(lines)-1]
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func TrimTrailingPromptLine(body string, promptContext PromptContext) string {
	return stripTrailingPromptLine(body, promptContext)
}

func (o *Observer) captureCommandDisplayDelta(ctx context.Context, paneID string, beforeDisplay string, command string, promptContext PromptContext) string {
	captured, err := o.capturePaneDisplay(ctx, paneID, -trackedCaptureLines)
	if err != nil {
		return ""
	}

	body := sanitizeDisplayBody(capturePaneDelta(beforeDisplay, captured))
	body = stripEchoedCommand(body, command)
	body = stripTrailingPromptLine(body, promptContext)
	return strings.TrimSpace(body)
}

func parseShellContextProbeOutput(body string, baseline PromptContext) (string, PromptContext, int) {
	context := baseline
	exitCode := 0
	context.GitBranch = ""
	context.RawLine = ""

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
	context.RawLine = context.PromptLine()

	return "", context, exitCode
}

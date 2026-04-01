package shell

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aiterm/internal/logging"
	"aiterm/internal/tmux"
)

func (o *Observer) AttachForegroundCommand(ctx context.Context, paneID string) (CommandMonitor, error) {
	captured, err := o.capturePane(ctx, paneID, -trackedCaptureLines)
	if err != nil {
		return nil, fmt.Errorf("capture pane for foreground attach: %w", err)
	}

	paneInfo, err := o.paneInfo(ctx, paneID)
	if err != nil {
		return nil, fmt.Errorf("inspect pane for foreground attach: %w", err)
	}

	currentPaneCommand := strings.TrimSpace(paneInfo.CurrentCommand)
	command := attachedForegroundCommandLabel(currentPaneCommand)
	focusedCapture := focusAttachedForegroundCapture(captured, currentPaneCommand)
	tail := monitorTail(focusedCapture, "")
	observed := o.observeShellState(ctx, paneID, "", captured, &paneInfo, o.promptHint)
	observed.Tail = tail
	if observed.HasPromptContext && !captureHasCurrentPromptContext(captured, observed.PromptContext) {
		observed.HasPromptContext = false
		observed.PromptContext = PromptContext{}
	}
	state := classifyActiveMonitorState(command, observed)
	if !shouldAttachForegroundMonitor(observed.HasPromptContext, currentPaneCommand, state) {
		return nil, nil
	}

	monitor := newTrackedCommandMonitor("", command)
	monitor.updateForegroundCommand(currentPaneCommand)
	monitor.updateTail(tail)
	if observed.HasPromptContext {
		monitor.updateShellContext(observed.PromptContext)
	}
	monitor.setState(state)

	go o.runAttachedForegroundMonitor(ctx, monitor, paneID, command, currentPaneCommand, focusedCapture)
	return monitor, nil
}

func attachedForegroundCommandLabel(currentPaneCommand string) string {
	command := strings.TrimSpace(currentPaneCommand)
	switch {
	case command == "":
		return "manual shell command"
	case paneCommandIsShell(command):
		return "manual shell command"
	default:
		return command
	}
}

func shouldAttachForegroundMonitor(hasPromptContext bool, currentPaneCommand string, state MonitorState) bool {
	if hasPromptContext && paneCommandAllowsPromptInference(currentPaneCommand) {
		return false
	}

	switch state {
	case MonitorStateAwaitingInput, MonitorStateInteractiveFullscreen:
		return true
	}

	trimmed := strings.TrimSpace(currentPaneCommand)
	if trimmed == "" {
		return false
	}

	if !paneCommandIsShell(trimmed) && !paneCommandAllowsPromptInference(trimmed) {
		return true
	}

	return false
}

func (o *Observer) runAttachedForegroundMonitor(ctx context.Context, monitor *trackedCommandMonitor, paneID string, command string, initialPaneCommand string, initialCapture string) {
	lastCapture := initialCapture
	lastPaneInfoCheck := time.Time{}
	alternateOn := false
	currentPaneCommand := strings.TrimSpace(initialPaneCommand)
	paneTTY := ""

	for {
		captured, err := o.capturePane(ctx, paneID, -trackedCaptureLines)
		if err != nil {
			monitor.finish(TrackedExecution{
				Command:    command,
				Cause:      CompletionCauseUnknown,
				Confidence: ConfidenceLow,
				Captured:   monitorTail(lastCapture, ""),
			}, fmt.Errorf("capture pane for attached foreground command: %w", err), MonitorStateLost)
			return
		}
		focusedCapture := focusAttachedForegroundCapture(captured, currentPaneCommand)
		lastCapture = focusedCapture
		tail := monitorTail(focusedCapture, "")

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
		observed := o.observeShellState(ctx, paneID, "", captured, &paneInfo, monitor.Snapshot().ShellContext)
		observed.Tail = tail
		if observed.HasPromptContext && !captureHasCurrentPromptContext(captured, observed.PromptContext) {
			observed.HasPromptContext = false
			observed.PromptContext = PromptContext{}
		}
		if observed.HasPromptContext {
			trimmedTail := stripEchoedForegroundCommand(strings.TrimSpace(observed.Tail), currentPaneCommand)
			trimmedTail = stripTrailingPromptLine(trimmedTail, observed.PromptContext)
			observed.Tail = trimmedTail
			monitor.updateShellContext(observed.PromptContext)
		}
		if observed.Tail != "" || !observed.HasPromptContext {
			monitor.updateTail(observed.Tail)
		}
		if observed.HasSemanticState {
			monitor.updateSemanticMetadata(true, observed.SemanticSource)
		} else {
			monitor.updateSemanticMetadata(false, "")
		}

		monitor.setState(classifyActiveMonitorState(command, observed))

		if observed.HasSemanticState && observed.SemanticState.Event == semanticEventPrompt {
			promptContext := monitor.Snapshot().ShellContext
			if promptContext.PromptLine() == "" {
				promptContext = synthesizePromptContext(o.promptHint, observed.SemanticState)
			}
			if !captureHasCurrentPromptContext(captured, promptContext) {
				select {
				case <-ctx.Done():
					monitor.finish(TrackedExecution{
						Command:    command,
						Cause:      CompletionCauseUnknown,
						Confidence: ConfidenceLow,
						Captured:   monitorTail(lastCapture, ""),
					}, ctx.Err(), monitorStateFromError(ctx.Err()))
					return
				case <-time.After(50 * time.Millisecond):
				}
				continue
			}
			cleanBody := stripEchoedForegroundCommand(strings.TrimSpace(observed.Tail), currentPaneCommand)
			if promptContext.PromptLine() != "" {
				cleanBody = stripTrailingPromptLine(cleanBody, promptContext)
			}
			if cleanBody == "" {
				snapshot := monitor.Snapshot()
				cleanBody = stripEchoedForegroundCommand(strings.TrimSpace(snapshot.LatestOutputTail), command)
			}
			exitCode := 0
			if observed.SemanticState.ExitCode != nil {
				exitCode = *observed.SemanticState.ExitCode
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
				"shell.attach_foreground.complete_semantic",
				"pane", paneID,
				"command", command,
				"state", state,
				"exit_code", exitCode,
				"pane_command", currentPaneCommand,
			)
			monitor.finish(TrackedExecution{
				Command:        command,
				Cause:          CompletionCausePromptReturn,
				Confidence:     ConfidenceStrong,
				SemanticShell:  true,
				SemanticSource: observed.SemanticSource,
				ExitCode:       exitCode,
				Captured:       cleanBody,
				ShellContext:   promptContext,
			}, nil, state)
			return
		}

		if observed.HasPromptContext && allowPromptReturnInference("", observed) {
			cleanBody := stripEchoedForegroundCommand(strings.TrimSpace(observed.Tail), currentPaneCommand)
			if observed.PromptContext.PromptLine() != "" {
				cleanBody = stripTrailingPromptLine(cleanBody, observed.PromptContext)
			}
			if cleanBody == "" {
				snapshot := monitor.Snapshot()
				cleanBody = stripEchoedForegroundCommand(strings.TrimSpace(snapshot.LatestOutputTail), command)
			}
			confidence := ConfidenceMedium
			if paneCommandAllowsPromptInference(currentPaneCommand) {
				confidence = ConfidenceStrong
			}
			logging.Trace(
				"shell.attach_foreground.complete_prompt",
				"pane", paneID,
				"command", command,
				"pane_command", currentPaneCommand,
				"confidence", confidence,
			)
			monitor.finish(TrackedExecution{
				Command:      command,
				Cause:        CompletionCausePromptReturn,
				Confidence:   confidence,
				ExitCode:     0,
				Captured:     cleanBody,
				ShellContext: observed.PromptContext,
			}, nil, MonitorStateCompleted)
			return
		}

		select {
		case <-ctx.Done():
			monitor.finish(TrackedExecution{Command: command}, ctx.Err(), monitorStateFromError(ctx.Err()))
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func focusAttachedForegroundCapture(captured string, currentPaneCommand string) string {
	captured = sanitizeCapturedBody(captured)
	currentPaneCommand = strings.TrimSpace(currentPaneCommand)
	if captured == "" || currentPaneCommand == "" || paneCommandIsShell(currentPaneCommand) {
		if paneCommandAllowsPromptInference(currentPaneCommand) {
			return focusPromptInferenceCapture(captured)
		}
		return captured
	}
	if paneCommandAllowsPromptInference(currentPaneCommand) {
		return focusPromptInferenceCapture(captured)
	}

	lines := strings.Split(strings.ReplaceAll(captured, "\r\n", "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if !lineStartsForegroundCommand(lines[index], currentPaneCommand) {
			continue
		}
		return strings.TrimSpace(strings.Join(lines[index:], "\n"))
	}

	return captured
}

func stripEchoedForegroundCommand(body string, currentPaneCommand string) string {
	body = strings.TrimSpace(body)
	currentPaneCommand = strings.TrimSpace(currentPaneCommand)
	if body == "" || currentPaneCommand == "" {
		return body
	}
	if paneCommandAllowsPromptInference(currentPaneCommand) {
		return stripPromptPrefixedForegroundCommand(body)
	}

	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return body
	}

	startIndex := 0
	if lineLooksLikePrompt(lines[0]) && len(lines) > 1 {
		startIndex = 1
	}
	if !lineStartsForegroundCommand(lines[startIndex], currentPaneCommand) {
		return body
	}

	return strings.TrimSpace(strings.Join(lines[startIndex+1:], "\n"))
}

func focusPromptInferenceCapture(captured string) string {
	lines := strings.Split(strings.ReplaceAll(captured, "\r\n", "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if !lineStartsWithPromptPrefix(lines[index]) {
			continue
		}
		if index == len(lines)-1 {
			return strings.TrimSpace(lines[index])
		}
		return strings.TrimSpace(strings.Join(lines[index:], "\n"))
	}
	return captured
}

func stripPromptPrefixedForegroundCommand(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return body
	}

	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	if !lineStartsWithPromptPrefix(lines[0]) {
		return body
	}
	if len(lines) < 2 {
		return ""
	}
	if lineLooksLikePrompt(lines[1]) {
		return strings.TrimSpace(strings.Join(lines[1:], "\n"))
	}
	return strings.TrimSpace(strings.Join(lines[1:], "\n"))
}

func lineStartsWithPromptPrefix(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if lineLooksLikePrompt(trimmed) {
		return true
	}
	for _, suffix := range []string{"# ", "$ ", "% ", "> "} {
		index := strings.Index(trimmed, suffix)
		if index < 0 {
			continue
		}
		candidate := strings.TrimSpace(trimmed[:index+1])
		if candidate == "" {
			continue
		}
		if context, ok := ParsePromptContextFromCapture(candidate); ok && strings.TrimSpace(context.RawLine) == candidate {
			return true
		}
	}
	return false
}

func lineStartsForegroundCommand(line string, currentPaneCommand string) bool {
	currentPaneCommand = strings.TrimSpace(currentPaneCommand)
	if currentPaneCommand == "" {
		return false
	}

	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if lineLooksLikePrompt(trimmed) {
		return false
	}

	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return false
	}

	return strings.TrimSpace(fields[0]) == currentPaneCommand
}

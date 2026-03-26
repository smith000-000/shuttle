package shell

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aiterm/internal/logging"
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
	promptContext, hasPromptContext := ParsePromptContextFromCapture(captured)
	if hasPromptContext && !captureHasCurrentPromptContext(captured, promptContext) {
		hasPromptContext = false
		promptContext = PromptContext{}
	}
	state := classifyActiveMonitorState(command, tail, paneInfo.AlternateOn, currentPaneCommand)
	if !shouldAttachForegroundMonitor(hasPromptContext, currentPaneCommand, state) {
		return nil, nil
	}

	monitor := newTrackedCommandMonitor("", command)
	monitor.updateForegroundCommand(currentPaneCommand)
	monitor.updateTail(tail)
	if hasPromptContext {
		monitor.updateShellContext(promptContext)
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

		var parsedShellContext PromptContext
		hasParsedShellContext := false
		if shellContext, ok := ParsePromptContextFromCapture(captured); ok {
			if captureHasCurrentPromptContext(captured, shellContext) {
				parsedShellContext = shellContext
				hasParsedShellContext = true
				monitor.updateShellContext(shellContext)
			}
		}
		if hasParsedShellContext {
			trimmedTail := stripEchoedForegroundCommand(strings.TrimSpace(tail), currentPaneCommand)
			trimmedTail = stripTrailingPromptLine(trimmedTail, parsedShellContext)
			if trimmedTail != "" {
				monitor.updateTail(trimmedTail)
			}
		} else {
			monitor.updateTail(tail)
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

		semanticState, semanticSource, hasSemanticState := o.captureSemanticShellState(ctx, paneID, paneTTY, "", currentPaneCommand, semanticBaseContext)
		if hasSemanticState {
			monitor.updateSemanticMetadata(true, semanticSource)
			monitor.updateShellContext(synthesizePromptContext(semanticBaseContext, semanticState))
		} else {
			monitor.updateSemanticMetadata(false, "")
		}

		monitor.setState(classifyActiveMonitorState(command, tail, alternateOn, currentPaneCommand))

		if hasSemanticState && semanticState.Event == semanticEventPrompt {
			promptContext := monitor.Snapshot().ShellContext
			if promptContext.PromptLine() == "" {
				promptContext = synthesizePromptContext(o.promptHint, semanticState)
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
			cleanBody := stripEchoedForegroundCommand(strings.TrimSpace(tail), currentPaneCommand)
			if promptContext.PromptLine() != "" {
				cleanBody = stripTrailingPromptLine(cleanBody, promptContext)
			}
			if cleanBody == "" {
				snapshot := monitor.Snapshot()
				cleanBody = stripEchoedForegroundCommand(strings.TrimSpace(snapshot.LatestOutputTail), command)
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
				SemanticSource: semanticSource,
				ExitCode:       exitCode,
				Captured:       cleanBody,
				ShellContext:   promptContext,
			}, nil, state)
			return
		}

		if hasParsedShellContext && allowPromptReturnInference("", alternateOn, currentPaneCommand) {
			cleanBody := stripEchoedForegroundCommand(strings.TrimSpace(tail), currentPaneCommand)
			if parsedShellContext.PromptLine() != "" {
				cleanBody = stripTrailingPromptLine(cleanBody, parsedShellContext)
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
				ShellContext: parsedShellContext,
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
		return captured
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

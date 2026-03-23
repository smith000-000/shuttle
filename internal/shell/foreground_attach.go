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
	tail := monitorTail(captured, "")
	promptContext, hasPromptContext := ParsePromptContextFromCapture(captured)
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

	go o.runAttachedForegroundMonitor(ctx, monitor, paneID, command, captured)
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

	if strings.TrimSpace(currentPaneCommand) == "" {
		return !hasPromptContext
	}

	if !paneCommandIsShell(currentPaneCommand) && !paneCommandAllowsPromptInference(currentPaneCommand) {
		return true
	}

	return !hasPromptContext
}

func (o *Observer) runAttachedForegroundMonitor(ctx context.Context, monitor *trackedCommandMonitor, paneID string, command string, initialCapture string) {
	lastCapture := initialCapture
	lastPaneInfoCheck := time.Time{}
	alternateOn := false
	currentPaneCommand := ""
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
		lastCapture = captured
		tail := monitorTail(captured, "")
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
			cleanBody := strings.TrimSpace(tail)
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
			cleanBody := strings.TrimSpace(tail)
			if parsedShellContext.PromptLine() != "" {
				cleanBody = stripTrailingPromptLine(cleanBody, parsedShellContext)
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

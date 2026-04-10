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
	focusedCapture := focusAttachedForegroundCapture(captured, currentPaneCommand)
	promptPrefixedContext, promptPrefixedCommand, _ := promptPrefixedForegroundCommand(focusedCapture)
	command := attachedForegroundCommandLabel(currentPaneCommand)
	if promptInferenceForegroundCommand(currentPaneCommand, promptPrefixedCommand) != "" {
		command = promptPrefixedCommand
	}
	tail := monitorTail(focusedCapture, "")
	observed := o.observeShellState(ctx, paneID, "", captured, &paneInfo, o.promptHint)
	observed.Tail = tail
	if observed.HasPromptContext && !captureHasCurrentPromptContext(captured, observed.PromptContext) {
		observed.HasPromptContext = false
		observed.PromptContext = PromptContext{}
	}
	state := classifyActiveMonitorState(command, observed)
	if !shouldAttachForegroundMonitor(observed.HasPromptContext, currentPaneCommand, state, promptPrefixedCommand) {
		return nil, nil
	}

	monitor := newTrackedCommandMonitor("", command)
	monitor.updateForegroundCommand(currentPaneCommand)
	monitor.updateTail(tail, tail)
	if observed.HasPromptContext {
		monitor.updateShellContext(observed.PromptContext)
	} else if promptPrefixedContext.PromptLine() != "" {
		monitor.updateShellContext(promptPrefixedContext)
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

func shouldAttachForegroundMonitor(hasPromptContext bool, currentPaneCommand string, state MonitorState, promptPrefixedCommand string) bool {
	if hasPromptContext && paneCommandAllowsPromptInference(currentPaneCommand) {
		return false
	}
	if promptInferenceForegroundCommand(currentPaneCommand, promptPrefixedCommand) != "" {
		return true
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
				Command:         command,
				Cause:           CompletionCauseUnknown,
				Confidence:      ConfidenceLow,
				Captured:        monitorTail(lastCapture, ""),
				DisplayCaptured: monitorTail(lastCapture, ""),
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
		promptPrefixedContext, promptPrefixedCommand, _ := promptPrefixedForegroundCommand(focusedCapture)
		if !observed.HasPromptContext && promptPrefixedContext.PromptLine() != "" {
			monitor.updateShellContext(promptPrefixedContext)
		}
		if observed.HasPromptContext {
			trimmedTail := stripEchoedForegroundCommand(strings.TrimSpace(observed.Tail), currentPaneCommand)
			trimmedTail = stripTrailingPromptLine(trimmedTail, observed.PromptContext)
			observed.Tail = trimmedTail
			monitor.updateShellContext(observed.PromptContext)
		}
		if observed.Tail != "" || !observed.HasPromptContext {
			monitor.updateTail(observed.Tail, observed.Tail)
		}
		if observed.HasSemanticState {
			monitor.updateSemanticMetadata(true, observed.SemanticSource)
		} else {
			monitor.updateSemanticMetadata(false, "")
		}

		monitor.setState(classifyActiveMonitorState(command, observed))

		if shouldSettlePromptInferenceTransportCommand(command, currentPaneCommand, promptPrefixedCommand) {
			promptContext := promptPrefixedContext
			if promptContext.PromptLine() == "" {
				promptContext = monitor.Snapshot().ShellContext
			}
			location := inferShellLocation(promptContext, currentPaneCommand, observed.RememberedTransition)
			logging.Trace(
				"shell.attach_foreground.complete_transport",
				"pane", paneID,
				"command", command,
				"next_command", promptPrefixedCommand,
				"pane_command", currentPaneCommand,
				"prompt", promptContext.PromptLine(),
			)
			monitor.finish(TrackedExecution{
				Command:      command,
				Cause:        CompletionCauseContextTransition,
				Confidence:   ConfidenceStrong,
				ExitCode:     0,
				ShellContext: promptContext,
				Location:     location,
			}, nil, MonitorStateCompleted)
			return
		}

		if observed.HasSemanticState && observed.SemanticState.Event == semanticEventPrompt {
			promptContext := promptReturnContext(promptReturnInputs{
				Observed:   observed,
				Snapshot:   monitor.Snapshot(),
				PromptHint: o.promptHint,
			})
			if !captureHasCurrentPromptContext(captured, promptContext) {
				select {
				case <-ctx.Done():
					monitor.finish(TrackedExecution{
						Command:         command,
						Cause:           CompletionCauseUnknown,
						Confidence:      ConfidenceLow,
						Captured:        monitorTail(lastCapture, ""),
						DisplayCaptured: monitorTail(lastCapture, ""),
					}, ctx.Err(), monitorStateFromError(ctx.Err()))
					return
				case <-time.After(50 * time.Millisecond):
				}
				continue
			}
			evaluation, complete := evaluateSemanticPromptReturn(promptReturnInputs{
				Command:    command,
				Observed:   observed,
				Snapshot:   monitor.Snapshot(),
				PromptHint: o.promptHint,
				RawBody:    observed.Tail,
				BodyCleaner: func(body string, promptContext PromptContext) string {
					return stripTrailingPromptLine(stripEchoedForegroundCommand(strings.TrimSpace(body), currentPaneCommand), promptContext)
				},
				FallbackBody: func(snapshot MonitorSnapshot) string {
					return stripEchoedForegroundCommand(strings.TrimSpace(snapshot.LatestOutputTail), promptReturnFallbackStripCommand(currentPaneCommand, command))
				},
				SemanticSource: observed.SemanticSource,
			})
			if !complete {
				continue
			}
			logging.Trace(
				"shell.attach_foreground.complete_semantic",
				"pane", paneID,
				"command", command,
				"state", evaluation.State,
				"exit_code", evaluation.Result.ExitCode,
				"pane_command", currentPaneCommand,
			)
			if strings.TrimSpace(evaluation.Result.DisplayCaptured) == "" {
				evaluation.Result.DisplayCaptured = evaluation.Result.Captured
			}
			monitor.finish(evaluation.Result, nil, evaluation.State)
			return
		}

		if observed.HasPromptContext && allowPromptReturnInference("", observed) {
			evaluation, complete := evaluatePromptReturnInference(promptReturnInputs{
				Command:    command,
				Observed:   observed,
				Snapshot:   monitor.Snapshot(),
				PromptHint: o.promptHint,
				RawBody:    observed.Tail,
				BodyCleaner: func(body string, promptContext PromptContext) string {
					return stripTrailingPromptLine(stripEchoedForegroundCommand(strings.TrimSpace(body), currentPaneCommand), promptContext)
				},
				FallbackBody: func(snapshot MonitorSnapshot) string {
					return stripEchoedForegroundCommand(strings.TrimSpace(snapshot.LatestOutputTail), promptReturnFallbackStripCommand(currentPaneCommand, command))
				},
				AllowEmptyBody: true,
				SemanticSource: observed.SemanticSource,
			})
			if !complete {
				continue
			}
			if paneCommandAllowsPromptInference(currentPaneCommand) && evaluation.Result.Confidence == ConfidenceMedium {
				evaluation.Result.Confidence = ConfidenceStrong
			}
			logging.Trace(
				"shell.attach_foreground.complete_prompt",
				"pane", paneID,
				"command", command,
				"pane_command", currentPaneCommand,
				"confidence", evaluation.Result.Confidence,
			)
			if strings.TrimSpace(evaluation.Result.DisplayCaptured) == "" {
				evaluation.Result.DisplayCaptured = evaluation.Result.Captured
			}
			monitor.finish(evaluation.Result, nil, evaluation.State)
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
	_, _, ok := parsePromptPrefixedForegroundLine(line)
	return ok
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

func promptPrefixedForegroundCommand(body string) (PromptContext, string, bool) {
	body = strings.TrimSpace(body)
	if body == "" {
		return PromptContext{}, "", false
	}

	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return parsePromptPrefixedForegroundLine(line)
	}

	return PromptContext{}, "", false
}

func parsePromptPrefixedForegroundLine(line string) (PromptContext, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return PromptContext{}, "", false
	}
	if context, ok := ParsePromptContextFromCapture(trimmed); ok && strings.TrimSpace(context.RawLine) == trimmed {
		return context, "", true
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
		context, ok := ParsePromptContextFromCapture(candidate)
		if !ok || strings.TrimSpace(context.RawLine) != candidate {
			continue
		}
		return context, strings.TrimSpace(trimmed[index+len(suffix):]), true
	}
	return PromptContext{}, "", false
}

func promptInferenceForegroundCommand(currentPaneCommand string, promptPrefixedCommand string) string {
	if paneCommandIsShell(currentPaneCommand) || !paneCommandAllowsPromptInference(currentPaneCommand) {
		return ""
	}
	return strings.TrimSpace(promptPrefixedCommand)
}

func shouldSettlePromptInferenceTransportCommand(command string, currentPaneCommand string, promptPrefixedCommand string) bool {
	nextCommand := promptInferenceForegroundCommand(currentPaneCommand, promptPrefixedCommand)
	if nextCommand == "" {
		return false
	}
	if !isContextTransitionCommand(command) {
		return false
	}
	return strings.TrimSpace(nextCommand) != strings.TrimSpace(command)
}

func promptReturnFallbackStripCommand(currentPaneCommand string, command string) string {
	if paneCommandAllowsPromptInference(currentPaneCommand) && !paneCommandIsShell(currentPaneCommand) {
		return currentPaneCommand
	}
	return command
}

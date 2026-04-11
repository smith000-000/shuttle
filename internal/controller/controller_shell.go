package controller

import (
	"context"
	"fmt"
	"strings"

	"aiterm/internal/logging"
	"aiterm/internal/shell"
)

func (c *LocalController) ResumeAfterTakeControl(ctx context.Context) ([]TranscriptEvent, error) {
	logging.Trace("controller.resume_after_take_control")
	reconciledEvents, reconciledAgentOwned, reconciled, err := c.reconcileExecutionAfterTakeControl(ctx)
	if err != nil {
		return nil, err
	}
	pendingEvents := append([]TranscriptEvent(nil), reconciledEvents...)
	if reconciled {
		if reconciledAgentOwned {
			notice := c.appendSystemNotice("Returned from shell handoff and reconciled command state.")
			events, err := c.submitAgentTurn(ctx, "", resumeAfterTakeControlPrompt, nil, false)
			if err != nil {
				return append(append([]TranscriptEvent{notice}, pendingEvents...), events...), err
			}
			return append(append([]TranscriptEvent{notice}, pendingEvents...), events...), nil
		}
		return pendingEvents, nil
	}

	attachedEvents, attached, attachErr := c.attachForegroundExecution(ctx)
	if attachErr != nil {
		return nil, attachErr
	}
	pendingEvents = append(pendingEvents, attachedEvents...)
	if attached {
		return pendingEvents, nil
	}

	c.mu.Lock()
	primary := c.primaryExecutionLocked()
	c.mu.Unlock()
	if primary != nil {
		if len(pendingEvents) == 0 {
			return nil, nil
		}
		return pendingEvents, nil
	}
	if len(pendingEvents) == 0 {
		return nil, nil
	}
	return pendingEvents, nil
}

func (c *LocalController) reconcileExecutionAfterTakeControl(ctx context.Context) ([]TranscriptEvent, bool, bool, error) {
	trackedShell, trackedShellEvent := c.syncTrackedShellTargetWithNotice(ctx)
	if c.reader == nil {
		return prependTranscriptEvent(nil, trackedShellEvent), false, false, nil
	}

	c.mu.Lock()
	executionPtr := c.primaryExecutionLocked()
	if executionPtr == nil {
		c.mu.Unlock()
		return prependTranscriptEvent(nil, trackedShellEvent), false, false, nil
	}
	execution := *executionPtr
	var fallbackPrompt *shell.PromptContext
	if c.session.CurrentShell != nil {
		contextCopy := *c.session.CurrentShell
		fallbackPrompt = &contextCopy
	}
	c.mu.Unlock()
	handoffTarget := takeControlTargetForExecution(&execution, trackedShell)
	if strings.TrimSpace(handoffTarget.PaneID) == "" {
		return prependTranscriptEvent(nil, trackedShellEvent), false, false, nil
	}
	if sameTrackedShellTarget(handoffTarget, trackedShell) && !sameTrackedShellTarget(executionTarget(&execution, trackedShell), trackedShell) {
		return prependTranscriptEvent(nil, trackedShellEvent), false, false, nil
	}

	observed, err := c.reader.CaptureObservedShellState(ctx, handoffTarget.PaneID)
	if err != nil {
		return nil, false, false, err
	}

	recentOutput := ""
	recentDisplayOutput := ""
	if execution.OwnershipMode == CommandOwnershipSharedObserver {
		recentOutput = strings.TrimSpace(execution.LatestOutputTail)
		recentDisplayOutput = strings.TrimSpace(execution.LatestDisplayTail)
	}
	if recentOutput == "" {
		if captured, captureErr := c.reader.CaptureRecentOutput(ctx, handoffTarget.PaneID, 120); captureErr == nil {
			recentOutput = strings.TrimSpace(captured)
		}
	}
	if recentDisplayOutput == "" {
		if captured, captureErr := c.reader.CaptureRecentOutputDisplay(ctx, handoffTarget.PaneID, 120); captureErr == nil {
			recentDisplayOutput = strings.TrimSpace(captured)
		}
	}
	if recentOutput == "" {
		recentOutput = strings.TrimSpace(execution.LatestOutputTail)
	}
	if recentDisplayOutput == "" {
		recentDisplayOutput = strings.TrimSpace(execution.LatestDisplayTail)
	}
	reconcileReason := shell.HandoffPromptReturnReason(observed, recentOutput, fallbackPrompt)
	if reconcileReason == "" {
		logging.Trace(
			"controller.resume_after_take_control.unreconciled",
			"execution_id", execution.ID,
			"command", execution.Command,
			"pane_command", strings.TrimSpace(observed.CurrentPaneCommand),
			"has_prompt_context", observed.HasPromptContext,
			"semantic_exit_known", observed.SemanticState.ExitCode != nil,
			"tail_preview", logging.Preview(recentOutput, 800),
		)
		return prependTranscriptEvent(nil, trackedShellEvent), false, false, nil
	}

	promptContext := shell.PromptContext{}
	hasPromptContext := observed.HasPromptContext && observed.PromptContext.PromptLine() != ""
	if hasPromptContext {
		promptContext = observed.PromptContext
		recentOutput = shell.TrimTrailingPromptLine(recentOutput, promptContext)
		recentDisplayOutput = shell.TrimTrailingPromptLine(recentDisplayOutput, promptContext)
	}

	exitCode, state, confidence, semanticShell, semanticSource, inferred := inferHandoffPromptReturnResult(promptContext, observed.SemanticState.ExitCode, observed.SemanticSource, execution, recentOutput)

	summary := CommandResultSummary{
		ExecutionID:    execution.ID,
		CommandID:      execution.ID,
		Command:        execution.Command,
		Origin:         execution.Origin,
		State:          state,
		Cause:          shell.CompletionCausePromptReturn,
		Confidence:     confidence,
		SemanticShell:  semanticShell,
		SemanticSource: semanticSource,
		ExitCode:       exitCode,
		Summary:        recentOutput,
		DisplaySummary: recentDisplayOutput,
	}
	if hasPromptContext {
		contextCopy := promptContext
		summary.ShellContext = &contextCopy
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.executionLocked(execution.ID)
	if current == nil {
		return prependTranscriptEvent(nil, trackedShellEvent), false, false, nil
	}
	if hasPromptContext {
		contextCopy := promptContext
		c.applyPromptContextLocked(&contextCopy)
	} else if observed.Location.Kind != "" {
		c.applyShellLocationLocked(observed.Location)
	}
	c.session.RecentShellOutput = recentOutput
	current.LatestOutputTail = recentOutput
	current.LatestDisplayTail = recentDisplayOutput
	c.task.LastCommandResult = &summary
	cleanup := c.removeExecutionLocked(execution.ID)
	resultEvent := c.newEvent(EventCommandResult, summary)
	c.appendEvents(resultEvent)
	go runExecutionCleanup(cleanup)
	logging.Trace(
		"controller.resume_after_take_control.reconciled",
		"execution_id", execution.ID,
		"command", execution.Command,
		"state", state,
		"exit_code", exitCode,
		"inferred_exit", inferred,
		"confidence", confidence,
		"semantic_shell", semanticShell,
		"semantic_source", semanticSource,
		"reconcile_reason", reconcileReason,
		"tail_preview", logging.Preview(recentOutput, 1000),
	)
	return prependTranscriptEvent([]TranscriptEvent{resultEvent}, trackedShellEvent), isAgentOwnedExecution(execution.Origin), true, nil
}

func inferHandoffPromptReturnResult(promptContext shell.PromptContext, semanticExitCode *int, observedSemanticSource string, execution CommandExecution, recentOutput string) (int, CommandExecutionState, shell.SignalConfidence, bool, string, bool) {
	inferred := false
	confidence := shell.ConfidenceStrong
	semanticShell := false
	semanticSource := ""
	exitCode := 0

	switch {
	case promptContext.LastExitCode != nil:
		exitCode = *promptContext.LastExitCode
		confidence = shell.ConfidenceStrong
		semanticShell = true
		semanticSource = "state_file"
	case semanticExitCode != nil:
		exitCode = *semanticExitCode
		confidence = shell.ConfidenceStrong
		semanticShell = true
		semanticSource = strings.TrimSpace(observedSemanticSource)
	case execution.ExitCode != nil:
		exitCode = *execution.ExitCode
		confidence = shell.ConfidenceMedium
		semanticShell = execution.SemanticShell
		semanticSource = execution.SemanticSource
		inferred = true
	case strings.Contains(recentOutput, "^C"):
		exitCode = shell.InterruptedExitCode
		confidence = shell.ConfidenceMedium
		inferred = true
	default:
		exitCode = 0
		confidence = shell.ConfidenceLow
		inferred = true
	}

	state := CommandExecutionCompleted
	switch exitCode {
	case shell.InterruptedExitCode:
		state = CommandExecutionCanceled
	case 0:
		state = CommandExecutionCompleted
	default:
		state = CommandExecutionFailed
	}

	return exitCode, state, confidence, semanticShell, strings.TrimSpace(semanticSource), inferred
}

func (c *LocalController) RefreshShellContext(ctx context.Context) (*shell.PromptContext, error) {
	trackedShell := c.syncTrackedShellTarget(ctx)
	if observed := c.captureObservedShellStateForTarget(ctx, trackedShell); observed != nil && observed.HasPromptContext {
		c.mu.Lock()
		c.session.TrackedShell = trackedShell
		if trackedShell.SessionName != "" {
			c.session.SessionName = trackedShell.SessionName
		}
		c.applyObservedShellStateLocked(observed)
		contextCopy := observed.PromptContext
		c.mu.Unlock()
		return &contextCopy, nil
	}

	c.refreshUserShellContextForTarget(ctx, trackedShell, false)
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.session.CurrentShell
	if current == nil || current.PromptLine() == "" {
		return nil, nil
	}
	contextCopy := *current
	return &contextCopy, nil
}

func (c *LocalController) PeekShellTail(ctx context.Context, lines int) (string, error) {
	c.mu.Lock()
	target := executionTarget(c.primaryExecutionLocked(), c.session.TrackedShell)
	c.mu.Unlock()
	if strings.TrimSpace(target.PaneID) == "" {
		target = c.syncTrackedShellTarget(ctx)
	}
	if c.reader == nil || target.PaneID == "" {
		return "", nil
	}

	return c.reader.CaptureRecentOutputDisplay(ctx, target.PaneID, lines)
}

func (c *LocalController) captureRecoverySnapshot(ctx context.Context, paneID string, execution *CommandExecution) string {
	if c.reader == nil || paneID == "" || !shouldCaptureRecoverySnapshot(execution) {
		return ""
	}

	captured, err := c.reader.CaptureRecentOutput(ctx, paneID, recoverySnapshotLines)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(captured)
}

func (c *LocalController) activeExecutionUsesTrackedShell() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	execution := c.primaryExecutionLocked()
	if execution == nil {
		return false
	}
	return shouldSyncExecutionIntoUserShellSession(execution, c.session)
}

func (c *LocalController) TrackedShellTarget() TrackedShellTarget {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = normalizeSessionContext(c.session)
	return c.session.TrackedShell
}

func (c *LocalController) syncTrackedShellTarget(ctx context.Context) TrackedShellTarget {
	trackedShell, _ := c.syncTrackedShellTargetWithNotice(ctx)
	return trackedShell
}

func (c *LocalController) syncTrackedShellTargetWithNotice(ctx context.Context) (TrackedShellTarget, *TranscriptEvent) {
	c.mu.Lock()
	c.session = normalizeSessionContext(c.session)
	current := c.session.TrackedShell
	reader := c.reader
	runner := c.runner
	c.mu.Unlock()

	if current.PaneID == "" {
		return current, nil
	}

	var resolver TrackedPaneResolver
	if reader != nil {
		resolver, _ = reader.(TrackedPaneResolver)
	}
	if resolver == nil && runner != nil {
		resolver, _ = runner.(TrackedPaneResolver)
	}
	if resolver == nil {
		return current, nil
	}

	resolved, err := resolver.ResolveTrackedPane(ctx, current.PaneID)
	if err != nil || strings.TrimSpace(resolved) == "" {
		return current, nil
	}
	resolved = strings.TrimSpace(resolved)

	c.mu.Lock()
	previous := c.session.TrackedShell
	c.session.TrackedShell.SessionName = current.SessionName
	c.session.TrackedShell.PaneID = resolved
	c.session = normalizeSessionContext(c.session)
	updated := c.session.TrackedShell
	c.syncTrackedShellExecutionsLocked(previous, updated)
	var notice *TranscriptEvent
	if previous.PaneID != updated.PaneID || previous.SessionName != updated.SessionName {
		event := c.newEvent(EventSystemNotice, TextPayload{Text: trackedShellChangeNotice(previous, updated)})
		c.appendEvents(event)
		notice = &event
	}
	c.mu.Unlock()

	if previous.PaneID != updated.PaneID || previous.SessionName != updated.SessionName {
		logging.Trace(
			"controller.tracked_shell.updated",
			"session", updated.SessionName,
			"previous_pane", previous.PaneID,
			"current_pane", updated.PaneID,
		)
	}

	return updated, notice
}

func (c *LocalController) syncTrackedShellExecutionsLocked(previous TrackedShellTarget, updated TrackedShellTarget) {
	if sameTrackedShellTarget(previous, updated) || len(c.executions) == 0 {
		return
	}

	changed := false
	for _, execution := range c.executions {
		if execution == nil {
			continue
		}
		if !sameTrackedShellTarget(executionTarget(execution, previous), previous) {
			continue
		}
		execution.TrackedShell = updated
		changed = true
	}
	if changed {
		c.syncTaskExecutionViewsLocked()
	}
}

func trackedShellChangeNotice(previous TrackedShellTarget, current TrackedShellTarget) string {
	switch {
	case previous.SessionName != "" && previous.SessionName != current.SessionName:
		return fmt.Sprintf("Tracked shell session moved from %s to %s.", previous.SessionName, current.SessionName)
	case previous.PaneID != "" && previous.PaneID != current.PaneID:
		return fmt.Sprintf("Tracked shell pane changed from %s to %s.", previous.PaneID, current.PaneID)
	default:
		return fmt.Sprintf("Tracked shell target updated to %s %s.", current.SessionName, current.PaneID)
	}
}

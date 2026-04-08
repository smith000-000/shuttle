package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aiterm/internal/agentruntime"
	"aiterm/internal/logging"
	"aiterm/internal/shell"
)

func (c *LocalController) attachForegroundExecution(ctx context.Context) ([]TranscriptEvent, bool, error) {
	trackedShell, trackedShellEvent := c.syncTrackedShellTargetWithNotice(ctx)
	if trackedShell.PaneID == "" {
		return prependTranscriptEvent(nil, trackedShellEvent), false, nil
	}

	monitorRunner, ok := c.runner.(ForegroundMonitoringShellRunner)
	if !ok {
		return prependTranscriptEvent(nil, trackedShellEvent), false, nil
	}

	c.mu.Lock()
	if c.primaryExecutionLocked() != nil || c.foregroundAttachInFlight {
		c.mu.Unlock()
		return prependTranscriptEvent(nil, trackedShellEvent), false, nil
	}
	c.foregroundAttachInFlight = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.foregroundAttachInFlight = false
		c.mu.Unlock()
	}()

	monitorCtx, monitorCancel := context.WithCancel(context.Background())
	monitor, err := monitorRunner.AttachForegroundCommand(monitorCtx, trackedShell.PaneID)
	if err != nil || monitor == nil {
		monitorCancel()
		return nil, false, err
	}

	snapshot := monitor.Snapshot()
	command := strings.TrimSpace(snapshot.Command)
	if command == "" {
		command = "manual shell command"
	}

	c.mu.Lock()
	if c.primaryExecutionLocked() != nil {
		c.mu.Unlock()
		monitorCancel()
		return prependTranscriptEvent(nil, trackedShellEvent), false, nil
	}
	execution := c.newExecutionLocked(command, CommandOriginUserShell)
	execution.OwnershipMode = CommandOwnershipSharedObserver
	if !snapshot.StartedAt.IsZero() {
		execution.StartedAt = snapshot.StartedAt
	}
	registered := c.registerExecutionLocked(execution)
	c.registerAttachedMonitorCancelLocked(registered.ID, monitorCancel)
	c.applyMonitorSnapshotLocked(registered.ID, snapshot)
	current := *c.executionLocked(registered.ID)
	notice := c.newEvent(EventSystemNotice, TextPayload{Text: fmt.Sprintf("Attached to existing foreground command in the tracked shell: %s", current.Command)})
	startEvent := c.newEvent(EventCommandStart, CommandStartPayload{
		Command:   current.Command,
		Execution: current,
	})
	c.appendEvents(notice, startEvent)
	c.mu.Unlock()

	go c.consumeMonitorSnapshots(registered.ID, monitor)
	go c.awaitAttachedMonitor(registered.ID, monitor)

	logging.Trace(
		"controller.attach_foreground_execution",
		"execution_id", registered.ID,
		"command", current.Command,
		"state", current.State,
		"foreground_command", snapshot.ForegroundCommand,
	)
	return prependTranscriptEvent([]TranscriptEvent{notice, startEvent}, trackedShellEvent), true, nil
}

func (c *LocalController) CheckActiveExecution(ctx context.Context) ([]TranscriptEvent, error) {
	if c.runtimeHost == nil {
		return nil, nil
	}

	c.refreshUserShellContext(ctx, false)

	c.mu.Lock()
	execution := c.primaryExecutionLocked()
	if execution == nil || !isAgentOwnedExecution(execution.Origin) {
		c.mu.Unlock()
		return nil, nil
	}

	if execution.State != CommandExecutionAwaitingInput &&
		execution.State != CommandExecutionInteractiveFullscreen &&
		execution.State != CommandExecutionLost {
		c.transitionExecutionStateLocked(execution, CommandExecutionBackgroundMonitor, "check_active_execution")
	}
	session := c.session
	executionTarget := executionTarget(execution, session.TrackedShell)
	task := c.task
	recentOutput := strings.TrimSpace(execution.LatestOutputTail)
	c.mu.Unlock()
	_, trackedShellEvent := c.syncTrackedShellTargetWithNotice(ctx)

	if recentOutput == "" && c.reader != nil && executionTarget.PaneID != "" {
		captured, err := c.reader.CaptureRecentOutput(ctx, executionTarget.PaneID, 120)
		if err == nil {
			recentOutput = captured
		}
	}
	recoverySnapshot := c.captureRecoverySnapshot(ctx, executionTarget.PaneID, task.CurrentExecution)

	c.mu.Lock()
	execution = c.primaryExecutionLocked()
	if execution == nil || !isAgentOwnedExecution(execution.Origin) {
		c.mu.Unlock()
		return nil, nil
	}
	if execution.State != CommandExecutionAwaitingInput &&
		execution.State != CommandExecutionInteractiveFullscreen &&
		execution.State != CommandExecutionLost {
		c.transitionExecutionStateLocked(execution, CommandExecutionBackgroundMonitor, "check_active_execution")
	}
	if strings.TrimSpace(recentOutput) != "" {
		execution.LatestOutputTail = recentOutput
	}
	c.task.RecoverySnapshot = recoverySnapshot
	c.syncTaskExecutionViewsLocked()
	task = c.task
	c.mu.Unlock()

	outcome, err := c.runtime.Handle(ctx, c.runtimeHost, agentruntime.Request{
		Kind:   agentruntime.RequestExecutionCheckIn,
		Prompt: buildActiveExecutionCheckInPrompt(task.CurrentExecution),
	})
	if err != nil {
		logging.TraceError("controller.check_active_execution.error", err)
		return nil, err
	}

	if strings.TrimSpace(outcome.Message) == "" {
		return prependTranscriptEvent(nil, trackedShellEvent), nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.primaryExecutionLocked() == nil {
		return prependTranscriptEvent(nil, trackedShellEvent), nil
	}

	event := c.newEvent(EventAgentMessage, TextPayload{Text: outcome.Message})
	c.appendEvents(event)
	logging.Trace(
		"controller.check_active_execution.complete",
		"event_kinds", eventKinds([]TranscriptEvent{event}),
		"message_preview", logging.Preview(outcome.Message, 600),
	)
	return prependTranscriptEvent([]TranscriptEvent{event}, trackedShellEvent), nil
}

func buildActiveExecutionCheckInPrompt(execution *CommandExecution) string {
	basePrompt := activeExecutionCheckInPrompt
	if execution == nil {
		return appendPromptSuffix(basePrompt, stateAuthorityPromptSuffix)
	}

	switch execution.State {
	case CommandExecutionAwaitingInput:
		basePrompt = awaitingInputCheckInPrompt
	case CommandExecutionInteractiveFullscreen, CommandExecutionHandoffActive:
		basePrompt = fullscreenCheckInPrompt
	case CommandExecutionLost:
		basePrompt = lostTrackingCheckInPrompt
	}
	return appendPromptSuffix(basePrompt, stateAuthorityPromptSuffix)
}

func (c *LocalController) runTrackedCommand(ctx context.Context, executionID string, command string) (shell.TrackedExecution, error) {
	timeout := shell.CommandTimeout(command)
	c.mu.Lock()
	target := executionTarget(c.executionLocked(executionID), c.session.TrackedShell)
	c.mu.Unlock()
	if strings.TrimSpace(target.PaneID) == "" {
		target = c.syncTrackedShellTarget(ctx)
	}
	if monitorRunner, ok := c.runner.(MonitoringShellRunner); ok {
		monitor, err := monitorRunner.StartTrackedCommand(ctx, target.PaneID, command, timeout)
		if err != nil {
			return shell.TrackedExecution{}, err
		}
		go c.consumeMonitorSnapshots(executionID, monitor)
		return monitor.Wait()
	}

	return c.runner.RunTrackedCommand(ctx, target.PaneID, command, timeout)
}

func (c *LocalController) consumeMonitorSnapshots(executionID string, monitor shell.CommandMonitor) {
	for snapshot := range monitor.Updates() {
		c.applyMonitorSnapshot(executionID, snapshot)
	}
}

func (c *LocalController) awaitAttachedMonitor(executionID string, monitor shell.CommandMonitor) {
	result, err := monitor.Wait()

	c.mu.Lock()
	defer c.mu.Unlock()

	execution := c.executionLocked(executionID)
	if execution == nil {
		return
	}
	syncIntoUserShell := shouldSyncExecutionIntoUserShellSession(execution, c.session)

	completedAt := time.Now()
	execution.CompletedAt = &completedAt
	if result.ShellContext.PromptLine() != "" {
		contextCopy := result.ShellContext
		execution.ShellContextAfter = &contextCopy
		if syncIntoUserShell {
			c.applyPromptContextLocked(&contextCopy)
		}
	}
	if strings.TrimSpace(result.Captured) != "" {
		execution.LatestOutputTail = result.Captured
	}
	finalOutput := finalExecutionSummaryOutput(result, execution)
	if strings.TrimSpace(finalOutput) != "" {
		execution.LatestOutputTail = finalOutput
	}
	if result.ExitCode != 0 || result.State == shell.MonitorStateCompleted || result.State == shell.MonitorStateCanceled {
		exitCode := result.ExitCode
		execution.ExitCode = &exitCode
	}
	if err != nil {
		execution.Error = err.Error()
	}
	c.transitionExecutionStateLocked(execution, commandExecutionStateFromMonitorState(result.State), "attached_monitor_complete")

	summary := CommandResultSummary{
		ExecutionID:    execution.ID,
		CommandID:      result.CommandID,
		Command:        execution.Command,
		Origin:         execution.Origin,
		State:          execution.State,
		Cause:          result.Cause,
		Confidence:     result.Confidence,
		SemanticShell:  result.SemanticShell,
		SemanticSource: result.SemanticSource,
		ExitCode:       result.ExitCode,
		Summary:        finalOutput,
	}
	if result.ShellContext.PromptLine() != "" {
		contextCopy := result.ShellContext
		summary.ShellContext = &contextCopy
	}
	c.task.LastCommandResult = &summary
	cleanup := c.removeExecutionLocked(executionID)
	go runExecutionCleanup(cleanup)
}

func commandExecutionStateFromMonitorState(state shell.MonitorState) CommandExecutionState {
	switch state {
	case shell.MonitorStateQueued:
		return CommandExecutionQueued
	case shell.MonitorStateAwaitingInput:
		return CommandExecutionAwaitingInput
	case shell.MonitorStateInteractiveFullscreen:
		return CommandExecutionInteractiveFullscreen
	case shell.MonitorStateCompleted:
		return CommandExecutionCompleted
	case shell.MonitorStateFailed:
		return CommandExecutionFailed
	case shell.MonitorStateCanceled:
		return CommandExecutionCanceled
	case shell.MonitorStateLost:
		return CommandExecutionLost
	default:
		return CommandExecutionRunning
	}
}

func (c *LocalController) applyMonitorSnapshot(executionID string, snapshot shell.MonitorSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applyMonitorSnapshotLocked(executionID, snapshot)
}

func (c *LocalController) applyMonitorSnapshotLocked(executionID string, snapshot shell.MonitorSnapshot) {
	execution := c.executionLocked(executionID)
	if execution == nil {
		return
	}
	syncIntoUserShell := shouldSyncExecutionIntoUserShellSession(execution, c.session)

	switch snapshot.State {
	case shell.MonitorStateQueued:
		if execution.State == CommandExecutionQueued {
			c.transitionExecutionStateLocked(execution, CommandExecutionQueued, "monitor_snapshot")
		}
	case shell.MonitorStateRunning:
		if execution.State != CommandExecutionHandoffActive && execution.State != CommandExecutionBackgroundMonitor {
			c.transitionExecutionStateLocked(execution, CommandExecutionRunning, "monitor_snapshot")
		}
	case shell.MonitorStateAwaitingInput:
		if execution.State != CommandExecutionHandoffActive {
			c.transitionExecutionStateLocked(execution, CommandExecutionAwaitingInput, "monitor_snapshot")
		}
	case shell.MonitorStateInteractiveFullscreen:
		if execution.State != CommandExecutionHandoffActive {
			c.transitionExecutionStateLocked(execution, CommandExecutionInteractiveFullscreen, "monitor_snapshot")
		}
	case shell.MonitorStateCanceled:
		c.transitionExecutionStateLocked(execution, CommandExecutionCanceled, "monitor_snapshot")
	case shell.MonitorStateFailed:
		c.transitionExecutionStateLocked(execution, CommandExecutionFailed, "monitor_snapshot")
	case shell.MonitorStateLost:
		c.transitionExecutionStateLocked(execution, CommandExecutionLost, "monitor_snapshot")
	case shell.MonitorStateCompleted:
		c.transitionExecutionStateLocked(execution, CommandExecutionCompleted, "monitor_snapshot")
	}

	if strings.TrimSpace(snapshot.LatestOutputTail) != "" {
		execution.LatestOutputTail = snapshot.LatestOutputTail
	}
	if strings.TrimSpace(snapshot.ForegroundCommand) != "" {
		execution.ForegroundCommand = snapshot.ForegroundCommand
	}
	execution.SemanticShell = snapshot.SemanticShell
	execution.SemanticSource = snapshot.SemanticSource
	if snapshot.ShellContext.PromptLine() != "" {
		contextCopy := snapshot.ShellContext
		execution.ShellContextAfter = &contextCopy
		if syncIntoUserShell {
			c.applyPromptContextLocked(&contextCopy)
		}
	}
	if snapshot.ExitCode != nil {
		exitCode := *snapshot.ExitCode
		execution.ExitCode = &exitCode
	}
	if snapshot.Error != "" {
		execution.Error = snapshot.Error
	}
	c.syncTaskExecutionViewsLocked()
}

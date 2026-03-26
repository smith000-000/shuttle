package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"aiterm/internal/logging"
	"aiterm/internal/shell"
)

func (c *LocalController) SubmitProposedShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error) {
	logging.Trace("controller.submit_proposed_shell_command", "command", command)
	return c.submitShellCommand(ctx, command, CommandOriginAgentProposal)
}

func (c *LocalController) SubmitShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error) {
	logging.Trace("controller.submit_shell_command", "command", command)
	return c.submitShellCommand(ctx, command, CommandOriginUserShell)
}

func (c *LocalController) prepareCommandExecutionTarget(ctx context.Context, trackedShell TrackedShellTarget, origin CommandOrigin) (TrackedShellTarget, func(context.Context) error, error) {
	if !isAgentOwnedExecution(origin) {
		return trackedShell, nil, nil
	}

	c.mu.Lock()
	currentShell := c.session.CurrentShell
	c.mu.Unlock()
	if currentShell != nil && currentShell.Remote {
		return trackedShell, nil, nil
	}

	runner, ok := c.runner.(OwnedExecutionShellRunner)
	if !ok {
		return trackedShell, nil, nil
	}

	c.mu.Lock()
	startDir := c.ownedExecutionStartDirLocked()
	c.mu.Unlock()

	ownedPane, cleanup, err := runner.CreateOwnedExecutionPane(ctx, startDir)
	if err != nil {
		return TrackedShellTarget{}, nil, err
	}

	return TrackedShellTarget{
		SessionName: strings.TrimSpace(ownedPane.SessionName),
		PaneID:      strings.TrimSpace(ownedPane.PaneID),
	}, cleanup, nil
}

func (c *LocalController) submitShellCommand(ctx context.Context, command string, origin CommandOrigin) ([]TranscriptEvent, error) {
	trackedShell, trackedShellEvent := c.syncTrackedShellTargetWithNotice(ctx)
	c.refreshUserShellContextForTarget(ctx, trackedShell, false)
	executionTarget, executionCleanup, targetErr := c.prepareCommandExecutionTarget(ctx, trackedShell, origin)
	refreshTrackedShellAfterCommand := sameTrackedShellTarget(executionTarget, trackedShell)
	if targetErr != nil {
		errText := fmt.Sprintf("prepare command execution target: %v", targetErr)
		c.mu.Lock()
		errEvent := c.newEvent(EventError, TextPayload{Text: errText})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return prependTranscriptEvent([]TranscriptEvent{errEvent}, trackedShellEvent), nil
	}

	c.mu.Lock()
	if active := c.primaryExecutionLocked(); blocksSerialShellSubmission(active) {
		message := "another shell execution is already active; wait for it to finish or press F2 to take control"
		if active != nil && strings.TrimSpace(active.Command) != "" {
			message = fmt.Sprintf("shell command %q is still active; wait for it to finish or press F2 to take control", active.Command)
		}
		errEvent := c.newEvent(EventError, TextPayload{Text: message})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		logging.Trace(
			"controller.shell_command.rejected_active_execution",
			"command", command,
			"origin", origin,
			"active_execution_id", active.ID,
			"active_command", active.Command,
			"active_state", active.State,
		)
		return prependTranscriptEvent([]TranscriptEvent{errEvent}, trackedShellEvent), nil
	}
	execution := c.newExecutionLocked(command, origin)
	execution.TrackedShell = executionTarget
	registered := c.registerExecutionLocked(execution)
	if executionCleanup != nil {
		c.registerExecutionCleanupLocked(registered.ID, executionCleanup)
	}
	events := make([]TranscriptEvent, 0, 2)
	if executionCleanup != nil {
		events = append(events, c.newEvent(EventSystemNotice, TextPayload{Text: fmt.Sprintf("Running command in owned execution pane %s.", registered.TrackedShell.PaneID)}))
	}
	startEvent := c.newEvent(EventCommandStart, CommandStartPayload{
		Command:   command,
		Execution: *registered,
	})
	events = append(events, startEvent)
	c.appendEvents(events...)
	c.mu.Unlock()

	logging.Trace(
		"controller.shell_command.start",
		"execution_id", execution.ID,
		"command", command,
		"origin", origin,
		"timeout_ms", shell.CommandTimeout(command).Milliseconds(),
	)

	if c.runner == nil {
		c.mu.Lock()
		failedExecution := execution
		failedExecution.State = CommandExecutionFailed
		failedExecution.Error = "shell command runner is not configured"
		cleanup := c.removeExecutionLocked(execution.ID)
		errEvent := c.newEvent(EventError, TextPayload{Text: "shell command runner is not configured"})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		runExecutionCleanup(cleanup)
		logging.Trace("controller.shell_command.runner_missing", "execution_id", execution.ID, "command", command)
		return prependTranscriptEvent(append(events, errEvent), trackedShellEvent), nil
	}

	result, err := c.runTrackedCommand(ctx, execution.ID, command)

	c.mu.Lock()

	current := c.executionLocked(execution.ID)
	if current == nil {
		c.mu.Unlock()
		logging.Trace(
			"controller.shell_command.stale_completion",
			"execution_id", execution.ID,
			"command", command,
			"origin", origin,
			"error", errString(err),
		)
		return nil, context.Canceled
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			cleanup := c.removeExecutionLocked(execution.ID)
			c.mu.Unlock()
			runExecutionCleanup(cleanup)
			logging.Trace("controller.shell_command.canceled", "execution_id", execution.ID, "command", command, "origin", origin)
			return prependTranscriptEvent(events, trackedShellEvent), err
		}
		if result.State == shell.MonitorStateLost {
			lostExecution := *current
			lostExecution.State = CommandExecutionLost
			lostExecution.Error = err.Error()
			if strings.TrimSpace(result.Captured) != "" {
				lostExecution.LatestOutputTail = result.Captured
			} else {
				lostExecution.LatestOutputTail = c.bestEffortRecentOutputLocked()
			}
			completedAt := time.Now()
			lostExecution.CompletedAt = &completedAt
			if result.ShellContext.PromptLine() != "" {
				shellContext := result.ShellContext
				lostExecution.ShellContextAfter = &shellContext
				c.applyPromptContextLocked(&shellContext)
			}

			summary := CommandResultSummary{
				ExecutionID:    execution.ID,
				CommandID:      result.CommandID,
				Command:        result.Command,
				Origin:         origin,
				State:          CommandExecutionLost,
				Cause:          result.Cause,
				Confidence:     result.Confidence,
				SemanticShell:  result.SemanticShell,
				SemanticSource: result.SemanticSource,
				ExitCode:       result.ExitCode,
				Summary:        lostExecution.LatestOutputTail,
			}
			if result.ShellContext.PromptLine() != "" {
				shellContext := result.ShellContext
				summary.ShellContext = &shellContext
			}
			c.task.LastCommandResult = &summary
			cleanup := c.removeExecutionLocked(execution.ID)
			resultEvent := c.newEvent(EventCommandResult, summary)
			c.appendEvents(resultEvent)
			c.mu.Unlock()
			runExecutionCleanup(cleanup)
			if refreshTrackedShellAfterCommand {
				c.refreshUserShellContext(ctx, true)
			}
			logging.Trace(
				"controller.shell_command.lost",
				"execution_id", execution.ID,
				"command", command,
				"origin", origin,
				"error", err.Error(),
				"tail_preview", logging.Preview(lostExecution.LatestOutputTail, 1000),
			)
			commandEvents := prependTranscriptEvent(append(events, resultEvent), trackedShellEvent)
			if isAgentOwnedExecution(origin) {
				recoveryEvents, recoveryErr := c.submitLostExecutionRecovery(ctx, lostExecution)
				if recoveryErr != nil {
					if errors.Is(recoveryErr, context.Canceled) {
						return commandEvents, recoveryErr
					}
					errEvent := c.appendRecoveryError(recoveryErr)
					if errEvent != nil {
						commandEvents = append(commandEvents, *errEvent)
					}
					return commandEvents, nil
				}
				commandEvents = append(commandEvents, recoveryEvents...)
			}
			return commandEvents, nil
		}
		failedExecution := *current
		failedExecution.State = CommandExecutionFailed
		failedExecution.Error = err.Error()
		failedExecution.LatestOutputTail = c.bestEffortRecentOutputForExecutionLocked(current)
		completedAt := time.Now()
		failedExecution.CompletedAt = &completedAt
		cleanup := c.removeExecutionLocked(execution.ID)
		errEvent := c.newEvent(EventError, TextPayload{Text: err.Error()})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		runExecutionCleanup(cleanup)
		logging.Trace(
			"controller.shell_command.error",
			"execution_id", execution.ID,
			"command", command,
			"origin", origin,
			"error", err.Error(),
			"tail_preview", logging.Preview(failedExecution.LatestOutputTail, 1000),
		)
		return prependTranscriptEvent(append(events, errEvent), trackedShellEvent), nil
	}

	if result.State == shell.MonitorStateCanceled {
		canceledExecution := *current
		canceledExecution.State = CommandExecutionCanceled
		canceledExecution.LatestOutputTail = result.Captured
		completedAt := time.Now()
		canceledExecution.CompletedAt = &completedAt
		exitCode := result.ExitCode
		canceledExecution.ExitCode = &exitCode

		summary := CommandResultSummary{
			ExecutionID:    execution.ID,
			CommandID:      result.CommandID,
			Command:        result.Command,
			Origin:         origin,
			State:          CommandExecutionCanceled,
			Cause:          result.Cause,
			Confidence:     result.Confidence,
			SemanticShell:  result.SemanticShell,
			SemanticSource: result.SemanticSource,
			ExitCode:       result.ExitCode,
			Summary:        result.Captured,
		}
		if result.ShellContext.PromptLine() != "" {
			shellContext := result.ShellContext
			summary.ShellContext = &shellContext
			canceledExecution.ShellContextAfter = &shellContext
			if shouldSyncExecutionIntoUserShellSession(current, c.session) {
				c.applyPromptContextLocked(&shellContext)
			}
		}
		c.task.LastCommandResult = &summary
		cleanup := c.removeExecutionLocked(execution.ID)
		resultEvent := c.newEvent(EventCommandResult, summary)
		c.appendEvents(resultEvent)
		c.mu.Unlock()
		runExecutionCleanup(cleanup)
		if refreshTrackedShellAfterCommand {
			c.refreshUserShellContext(ctx, true)
		}
		logging.Trace(
			"controller.shell_command.canceled_result",
			"execution_id", execution.ID,
			"command", command,
			"origin", origin,
			"command_id", result.CommandID,
			"summary_preview", logging.Preview(result.Captured, 1000),
			"prompt", result.ShellContext.PromptLine(),
		)
		return prependTranscriptEvent(append(events, resultEvent), trackedShellEvent), nil
	}

	completedExecution := *current
	completedExecution.State = CommandExecutionCompleted
	completedExecution.LatestOutputTail = result.Captured
	completedAt := time.Now()
	completedExecution.CompletedAt = &completedAt
	exitCode := result.ExitCode
	completedExecution.ExitCode = &exitCode

	summary := CommandResultSummary{
		ExecutionID:    execution.ID,
		CommandID:      result.CommandID,
		Command:        result.Command,
		Origin:         origin,
		State:          CommandExecutionCompleted,
		Cause:          result.Cause,
		Confidence:     result.Confidence,
		SemanticShell:  result.SemanticShell,
		SemanticSource: result.SemanticSource,
		ExitCode:       result.ExitCode,
		Summary:        result.Captured,
	}
	if result.ShellContext.PromptLine() != "" {
		shellContext := result.ShellContext
		summary.ShellContext = &shellContext
		completedExecution.ShellContextAfter = &shellContext
		if shouldSyncExecutionIntoUserShellSession(current, c.session) {
			c.applyPromptContextLocked(&shellContext)
		}
	}
	c.task.LastCommandResult = &summary
	cleanup := c.removeExecutionLocked(execution.ID)
	resultEvent := c.newEvent(EventCommandResult, summary)
	c.appendEvents(resultEvent)
	c.mu.Unlock()
	runExecutionCleanup(cleanup)
	if refreshTrackedShellAfterCommand {
		c.refreshUserShellContext(ctx, true)
	}
	logging.Trace(
		"controller.shell_command.complete",
		"execution_id", execution.ID,
		"command", command,
		"origin", origin,
		"command_id", result.CommandID,
		"exit_code", result.ExitCode,
		"summary_preview", logging.Preview(result.Captured, 1000),
		"prompt", result.ShellContext.PromptLine(),
	)
	return prependTranscriptEvent(append(events, resultEvent), trackedShellEvent), nil
}

func (c *LocalController) submitLostExecutionRecovery(ctx context.Context, execution CommandExecution) ([]TranscriptEvent, error) {
	if c.agent == nil {
		return nil, nil
	}

	c.refreshUserShellContext(ctx, false)

	c.mu.Lock()
	session := c.session
	task := c.task
	c.mu.Unlock()
	if strings.TrimSpace(execution.LatestOutputTail) != "" {
		session.RecentShellOutput = execution.LatestOutputTail
	}

	task.CurrentExecution = &execution
	task.PrimaryExecutionID = execution.ID
	task.ExecutionRegistry = []CommandExecution{execution}
	task.RecoverySnapshot = c.captureRecoverySnapshot(ctx, executionTarget(&execution, session.TrackedShell).PaneID, &execution)

	input := AgentInput{
		Session: session,
		Task:    task,
		Prompt:  lostTrackingCheckInPrompt,
	}

	response, err := c.agent.Respond(ctx, input)
	if err != nil {
		logging.TraceError(
			"controller.lost_recovery.error",
			err,
			"execution_id", execution.ID,
			"command", execution.Command,
		)
		return nil, err
	}
	response = normalizeAgentResponse(response)

	c.mu.Lock()
	defer c.mu.Unlock()

	events := make([]TranscriptEvent, 0, 4)
	if response.Message != "" {
		events = append(events, c.newEvent(EventAgentMessage, TextPayload{Text: response.Message}))
	}
	if response.Plan != nil {
		activePlan := buildActivePlan(*response.Plan)
		c.task.ActivePlan = &activePlan
		events = append(events, c.newEvent(EventPlan, activePlan))
	}
	if response.Proposal != nil {
		events = append(events, c.newEvent(EventProposal, ProposalPayload{
			Kind:        response.Proposal.Kind,
			Command:     response.Proposal.Command,
			Keys:        response.Proposal.Keys,
			Patch:       response.Proposal.Patch,
			Description: response.Proposal.Description,
		}))
	}
	if response.Approval != nil {
		approvalCopy := *response.Approval
		c.task.PendingApproval = &approvalCopy
		events = append(events, c.newEvent(EventApproval, approvalCopy))
	}
	c.appendEvents(events...)
	logging.Trace(
		"controller.lost_recovery.complete",
		"execution_id", execution.ID,
		"command", execution.Command,
		"event_kinds", eventKinds(events),
	)
	return events, nil
}

func (c *LocalController) appendRecoveryError(err error) *TranscriptEvent {
	if err == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	event := c.newEvent(EventError, TextPayload{Text: err.Error()})
	c.appendEvents(event)
	return &event
}

func (c *LocalController) DecideApproval(ctx context.Context, approvalID string, decision ApprovalDecision, refineText string) ([]TranscriptEvent, error) {
	logging.Trace("controller.decide_approval", "approval_id", approvalID, "decision", decision, "refine_text", refineText)
	c.mu.Lock()
	pending := c.task.PendingApproval
	if pending == nil || pending.ID != approvalID {
		errEvent := c.newEvent(EventError, TextPayload{Text: "approval request not found"})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return []TranscriptEvent{errEvent}, nil
	}

	switch decision {
	case DecisionReject:
		c.task.PendingApproval = nil
		event := c.newEvent(EventSystemNotice, TextPayload{Text: "Approval rejected."})
		c.appendEvents(event)
		c.mu.Unlock()
		return []TranscriptEvent{event}, nil
	case DecisionRefine:
		c.task.PendingApproval = nil
		message := "Approval sent back for refinement."
		if refineText != "" {
			message = "Refine request: " + refineText
		}
		event := c.newEvent(EventSystemNotice, TextPayload{Text: message})
		c.appendEvents(event)
		c.mu.Unlock()
		return []TranscriptEvent{event}, nil
	case DecisionApprove:
		patch := pending.Patch
		command := pending.Command
		c.task.PendingApproval = nil
		if pending.Kind == ApprovalPatch {
			if strings.TrimSpace(patch) == "" {
				event := c.newEvent(EventError, TextPayload{Text: "approval does not contain an applicable patch"})
				c.appendEvents(event)
				c.mu.Unlock()
				return []TranscriptEvent{event}, nil
			}
			c.mu.Unlock()
			return c.applyPatch(ctx, patch)
		}
		if strings.TrimSpace(command) == "" {
			event := c.newEvent(EventError, TextPayload{Text: "approval does not contain an executable command"})
			c.appendEvents(event)
			c.mu.Unlock()
			return []TranscriptEvent{event}, nil
		}
		c.mu.Unlock()
		return c.submitShellCommand(ctx, command, CommandOriginAgentApproval)
	default:
		c.mu.Unlock()
		return nil, fmt.Errorf("unsupported approval decision %q", decision)
	}
}

func (c *LocalController) ActiveExecution() *CommandExecution {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneCommandExecution(c.primaryExecutionLocked())
}

func (c *LocalController) RefreshActiveExecution(ctx context.Context) ([]TranscriptEvent, *CommandExecution, error) {
	trackedShell, trackedShellEvent := c.syncTrackedShellTargetWithNotice(ctx)

	c.mu.Lock()
	active := cloneCommandExecution(c.primaryExecutionLocked())
	attachInFlight := c.foregroundAttachInFlight
	c.mu.Unlock()
	if active != nil {
		logging.Trace("controller.refresh_active_execution", "outcome", "active", "execution_id", active.ID, "state", active.State)
		return prependTranscriptEvent(nil, trackedShellEvent), active, nil
	}
	if trackedShell.PaneID == "" {
		logging.Trace("controller.refresh_active_execution", "outcome", "no_tracked_pane")
		return prependTranscriptEvent(nil, trackedShellEvent), nil, nil
	}
	if attachInFlight {
		logging.Trace("controller.refresh_active_execution", "outcome", "attach_in_flight")
		return prependTranscriptEvent(nil, trackedShellEvent), nil, nil
	}

	events, attached, err := c.attachForegroundExecution(ctx)
	if err != nil {
		logging.TraceError("controller.refresh_active_execution.error", err)
		return nil, nil, err
	}
	if !attached {
		logging.Trace("controller.refresh_active_execution", "outcome", "no_attach")
		return events, nil, nil
	}

	c.mu.Lock()
	active = cloneCommandExecution(c.primaryExecutionLocked())
	c.mu.Unlock()
	if active != nil {
		logging.Trace("controller.refresh_active_execution", "outcome", "attached", "execution_id", active.ID, "state", active.State)
	}
	return events, active, nil
}

func (c *LocalController) AbandonActiveExecution(reason string) *CommandExecution {
	c.mu.Lock()
	defer c.mu.Unlock()

	execution := c.primaryExecutionLocked()
	if execution == nil {
		return nil
	}

	executionCopy := *execution
	executionCopy.State = CommandExecutionCanceled
	executionCopy.Error = strings.TrimSpace(reason)
	executionCopy.LatestOutputTail = c.bestEffortRecentOutputForExecutionLocked(execution)
	completedAt := time.Now()
	executionCopy.CompletedAt = &completedAt
	cleanup := c.removeExecutionLocked(execution.ID)
	c.appendEvents(c.newEvent(EventSystemNotice, TextPayload{Text: fmt.Sprintf("Abandoned active execution: %s", executionCopy.Command)}))
	logging.Trace(
		"controller.active_execution.abandoned",
		"execution_id", executionCopy.ID,
		"command", executionCopy.Command,
		"origin", executionCopy.Origin,
		"reason", reason,
		"tail_preview", logging.Preview(executionCopy.LatestOutputTail, 1000),
	)
	go runExecutionCleanup(cleanup)
	return &executionCopy
}

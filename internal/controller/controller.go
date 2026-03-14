package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aiterm/internal/logging"
	"aiterm/internal/shell"
)

const autoContinuePrompt = "The previously approved or proposed command has completed. First summarize the result briefly. If there is an active plan, continue it from the current step. If there is no active plan, do not propose another command unless the user's request clearly still requires more shell work. For one-off commands, demos, or tests, prefer stopping after reporting the outcome. If another action is truly needed, propose it. If risky, request approval."
const continuePlanPrompt = "Continue the active plan from the current step. Propose the next safe action if one is needed. If the next action is risky, request approval. If the plan is complete, answer briefly."
const resumeAfterTakeControlPrompt = "The user temporarily took control of the shell to handle an interactive step such as a password prompt, remote login, or fullscreen terminal app. Reassess the latest shell state and continue the task. If another action is needed, propose it. If risky, request approval. If the task is complete, answer briefly."
const activeExecutionCheckInPrompt = "An agent-started shell command is still active. Use the execution state and latest shell output to decide whether it is running normally or merely quiet. If there is no new output, say that no new shell output has appeared yet. Do not claim the command has completed or that the shell returned to a prompt unless the context shows that. Do not propose a new command, plan, or approval unless the shell is clearly blocked and needs user intervention; if so, say that the user should press F2 to take control."
const awaitingInputCheckInPrompt = "An agent-started shell command is waiting for shell input. Use the latest shell output and recovery snapshot to explain what input is likely needed. Do not claim the command has completed. Prefer a concise recovery message that tells the user to press F2 to take control. If the task only needs a small raw key sequence, mention KEYS> as an option."
const fullscreenCheckInPrompt = "An agent-started shell command has occupied a fullscreen terminal app. Use the latest shell output and recovery snapshot to identify the app or state as best you can. Do not claim the command has completed. Prefer a concise recovery message telling the user to press F2 to take control, or use KEYS> if they only need to send a few raw keys."
const lostTrackingCheckInPrompt = "Tracking confidence for an agent-started shell command is low. Use the latest shell output and recovery snapshot to explain what likely happened, without claiming completion unless the context clearly proves it. Prefer a recovery-oriented message that suggests inspecting the shell with F2 if the state is ambiguous."

const recoverySnapshotLines = 200

type ShellRunner interface {
	RunTrackedCommand(ctx context.Context, paneID string, command string, timeout time.Duration) (shell.TrackedExecution, error)
}

type MonitoringShellRunner interface {
	StartTrackedCommand(ctx context.Context, paneID string, command string, timeout time.Duration) (shell.CommandMonitor, error)
}

type ShellContextReader interface {
	CaptureRecentOutput(ctx context.Context, paneID string, lines int) (string, error)
	CaptureShellContext(ctx context.Context, paneID string) (shell.PromptContext, error)
}

type TextPayload struct {
	Text string
}

type PlanPayload = ActivePlan

type ProposalPayload struct {
	Kind        ProposalKind
	Command     string
	Keys        string
	Patch       string
	Description string
}

type CommandStartPayload struct {
	Command   string
	Execution CommandExecution
}

type LocalController struct {
	agent   Agent
	runner  ShellRunner
	reader  ShellContextReader
	session SessionContext

	mu      sync.Mutex
	counter atomic.Uint64
	task    TaskContext
}

func New(agent Agent, runner ShellRunner, reader ShellContextReader, session SessionContext) *LocalController {
	return &LocalController{
		agent:   agent,
		runner:  runner,
		reader:  reader,
		session: session,
		task: TaskContext{
			TaskID: "task-1",
		},
	}
}

func (c *LocalController) SubmitAgentPrompt(ctx context.Context, prompt string) ([]TranscriptEvent, error) {
	logging.Trace("controller.submit_agent_prompt", "prompt", prompt)
	return c.submitAgentTurn(ctx, prompt, prompt, nil, true)
}

func (c *LocalController) SubmitRefinement(ctx context.Context, approval ApprovalRequest, note string) ([]TranscriptEvent, error) {
	logging.Trace("controller.submit_refinement", "approval_id", approval.ID, "note", note)
	return c.submitAgentTurn(ctx, note, note, &approval, true)
}

func (c *LocalController) SubmitProposalRefinement(ctx context.Context, proposal ProposalPayload, note string) ([]TranscriptEvent, error) {
	logging.Trace(
		"controller.submit_proposal_refinement",
		"proposal_kind", proposal.Kind,
		"proposal_command", proposal.Command,
		"note", note,
	)
	return c.submitAgentTurn(ctx, note, buildProposalRefinementPrompt(proposal, note), nil, true)
}

func (c *LocalController) ContinueActivePlan(ctx context.Context) ([]TranscriptEvent, error) {
	c.mu.Lock()
	activePlan := c.task.ActivePlan
	if activePlan == nil {
		errEvent := c.newEvent(EventError, TextPayload{Text: "no active plan available"})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return []TranscriptEvent{errEvent}, nil
	}
	c.mu.Unlock()

	logging.Trace("controller.continue_active_plan")
	return c.submitAgentTurn(ctx, "", continuePlanPrompt, nil, false)
}

func (c *LocalController) ContinueAfterCommand(ctx context.Context) ([]TranscriptEvent, error) {
	c.mu.Lock()
	lastResult := c.task.LastCommandResult
	if lastResult == nil {
		errEvent := c.newEvent(EventError, TextPayload{Text: "no command result available for agent continuation"})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return []TranscriptEvent{errEvent}, nil
	}
	planEvent := c.advanceActivePlanLocked()
	if planEvent != nil {
		c.appendEvents(*planEvent)
	}
	c.mu.Unlock()

	logging.Trace("controller.continue_after_command")
	events, err := c.submitAgentTurn(ctx, "", autoContinuePrompt, nil, false)
	if planEvent != nil {
		events = append([]TranscriptEvent{*planEvent}, events...)
	}
	return events, err
}

func (c *LocalController) ResumeAfterTakeControl(ctx context.Context) ([]TranscriptEvent, error) {
	logging.Trace("controller.resume_after_take_control")
	return c.submitAgentTurn(ctx, "", resumeAfterTakeControlPrompt, nil, false)
}

func (c *LocalController) CheckActiveExecution(ctx context.Context) ([]TranscriptEvent, error) {
	if c.agent == nil {
		return nil, nil
	}

	c.mu.Lock()
	if c.task.CurrentExecution == nil || !isAgentOwnedExecution(c.task.CurrentExecution.Origin) {
		c.mu.Unlock()
		return nil, nil
	}

	if c.task.CurrentExecution.State != CommandExecutionAwaitingInput &&
		c.task.CurrentExecution.State != CommandExecutionInteractiveFullscreen &&
		c.task.CurrentExecution.State != CommandExecutionLost {
		c.task.CurrentExecution.State = CommandExecutionBackgroundMonitor
	}
	session := c.session
	task := c.task
	recentOutput := strings.TrimSpace(c.task.CurrentExecution.LatestOutputTail)
	topPaneID := c.session.TopPaneID
	c.mu.Unlock()

	if recentOutput == "" && c.reader != nil && topPaneID != "" {
		captured, err := c.reader.CaptureRecentOutput(ctx, topPaneID, 120)
		if err == nil {
			recentOutput = captured
		}
	}
	recoverySnapshot := c.captureRecoverySnapshot(ctx, topPaneID, task.CurrentExecution)

	c.mu.Lock()
	if c.task.CurrentExecution == nil || !isAgentOwnedExecution(c.task.CurrentExecution.Origin) {
		c.mu.Unlock()
		return nil, nil
	}
	if c.task.CurrentExecution.State != CommandExecutionAwaitingInput &&
		c.task.CurrentExecution.State != CommandExecutionInteractiveFullscreen &&
		c.task.CurrentExecution.State != CommandExecutionLost {
		c.task.CurrentExecution.State = CommandExecutionBackgroundMonitor
	}
	if strings.TrimSpace(recentOutput) != "" {
		c.task.CurrentExecution.LatestOutputTail = recentOutput
		c.session.RecentShellOutput = recentOutput
	}
	c.task.RecoverySnapshot = recoverySnapshot
	session = c.session
	task = c.task
	c.mu.Unlock()

	response, err := c.agent.Respond(ctx, AgentInput{
		Session: session,
		Task:    task,
		Prompt:  buildActiveExecutionCheckInPrompt(task.CurrentExecution),
	})
	if err != nil {
		logging.TraceError("controller.check_active_execution.error", err)
		return nil, err
	}

	if strings.TrimSpace(response.Message) == "" {
		return nil, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.task.CurrentExecution == nil {
		return nil, nil
	}

	event := c.newEvent(EventAgentMessage, TextPayload{Text: response.Message})
	c.appendEvents(event)
	logging.Trace(
		"controller.check_active_execution.complete",
		"event_kinds", eventKinds([]TranscriptEvent{event}),
		"message_preview", logging.Preview(response.Message, 600),
	)
	return []TranscriptEvent{event}, nil
}

func buildActiveExecutionCheckInPrompt(execution *CommandExecution) string {
	if execution == nil {
		return activeExecutionCheckInPrompt
	}

	switch execution.State {
	case CommandExecutionAwaitingInput:
		return awaitingInputCheckInPrompt
	case CommandExecutionInteractiveFullscreen, CommandExecutionHandoffActive:
		return fullscreenCheckInPrompt
	case CommandExecutionLost:
		return lostTrackingCheckInPrompt
	default:
		return activeExecutionCheckInPrompt
	}
}

func (c *LocalController) SubmitProposedShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error) {
	logging.Trace("controller.submit_proposed_shell_command", "command", command)
	return c.submitShellCommand(ctx, command, CommandOriginAgentProposal)
}

func (c *LocalController) submitAgentTurn(ctx context.Context, userPrompt string, agentPrompt string, refinement *ApprovalRequest, emitUserMessage bool) ([]TranscriptEvent, error) {
	logging.Trace(
		"controller.agent_turn.start",
		"user_prompt", userPrompt,
		"agent_prompt_preview", logging.Preview(agentPrompt, 800),
		"emit_user_message", emitUserMessage,
		"has_refinement", refinement != nil,
	)
	recentOutput := ""
	if c.reader != nil && c.session.TopPaneID != "" {
		captured, err := c.reader.CaptureRecentOutput(ctx, c.session.TopPaneID, 120)
		if err == nil {
			recentOutput = captured
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	events := make([]TranscriptEvent, 0, 4)
	if emitUserMessage {
		events = append(events, c.newEvent(EventUserMessage, TextPayload{Text: userPrompt}))
	}

	if c.agent == nil {
		errEvent := c.newEvent(EventError, TextPayload{Text: "agent runtime is not configured"})
		c.appendEvents(events...)
		c.appendEvents(errEvent)
		return append(append([]TranscriptEvent(nil), events...), errEvent), nil
	}

	session := c.session
	if recentOutput != "" {
		session.RecentShellOutput = recentOutput
		c.session.RecentShellOutput = recentOutput
	}
	task := c.task
	task.RecoverySnapshot = c.captureRecoverySnapshot(ctx, c.session.TopPaneID, task.CurrentExecution)

	input := AgentInput{
		Session: session,
		Task:    task,
		Prompt:  agentPrompt,
	}
	if refinement != nil {
		refinementCopy := *refinement
		input.Task.PendingApproval = &refinementCopy
	}

	response, err := c.agent.Respond(ctx, input)
	if err != nil {
		logging.TraceError(
			"controller.agent_turn.error",
			err,
			"user_prompt", userPrompt,
			"agent_prompt_preview", logging.Preview(agentPrompt, 800),
		)
		if errors.Is(err, context.Canceled) {
			c.appendEvents(events...)
			return append([]TranscriptEvent(nil), events...), err
		}
		errEvent := c.newEvent(EventError, TextPayload{Text: err.Error()})
		c.appendEvents(events...)
		c.appendEvents(errEvent)
		return append(append([]TranscriptEvent(nil), events...), errEvent), nil
	}
	response = normalizeAgentResponse(response)

	newEvents := append([]TranscriptEvent(nil), events...)

	if response.Message != "" {
		newEvents = append(newEvents, c.newEvent(EventAgentMessage, TextPayload{Text: response.Message}))
	}

	if response.Plan != nil {
		activePlan := buildActivePlan(*response.Plan)
		c.task.ActivePlan = &activePlan
		newEvents = append(newEvents, c.newEvent(EventPlan, activePlan))
	}

	if response.Proposal != nil {
		newEvents = append(newEvents, c.newEvent(EventProposal, ProposalPayload{
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
		newEvents = append(newEvents, c.newEvent(EventApproval, approvalCopy))
	}

	c.appendEvents(newEvents...)
	logging.Trace(
		"controller.agent_turn.complete",
		"event_kinds", eventKinds(newEvents),
		"message_preview", logging.Preview(response.Message, 600),
		"has_plan", response.Plan != nil,
		"has_proposal", response.Proposal != nil,
		"has_approval", response.Approval != nil,
	)
	return newEvents, nil
}

func buildActivePlan(plan Plan) ActivePlan {
	steps := make([]PlanStep, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		step = strings.TrimSpace(step)
		if step == "" {
			continue
		}

		status := PlanStepPending
		if len(steps) == 0 {
			status = PlanStepInProgress
		}
		steps = append(steps, PlanStep{
			Text:   step,
			Status: status,
		})
	}

	return ActivePlan{
		Summary: strings.TrimSpace(plan.Summary),
		Steps:   steps,
	}
}

func normalizeAgentResponse(response AgentResponse) AgentResponse {
	if response.Approval == nil || response.Proposal == nil {
		return response
	}

	approval := *response.Approval
	proposal := *response.Proposal

	if approval.Kind == ApprovalCommand && strings.TrimSpace(approval.Command) == "" && proposal.Kind == ProposalCommand && strings.TrimSpace(proposal.Command) != "" {
		approval.Command = strings.TrimSpace(proposal.Command)
	}
	if approval.Kind == ApprovalPatch && strings.TrimSpace(approval.Patch) == "" && proposal.Kind == ProposalPatch && strings.TrimSpace(proposal.Patch) != "" {
		approval.Patch = strings.TrimSpace(proposal.Patch)
	}

	response.Approval = &approval
	return response
}

func (c *LocalController) advanceActivePlanLocked() *TranscriptEvent {
	if c.task.ActivePlan == nil {
		return nil
	}

	plan := ActivePlan{
		Summary: c.task.ActivePlan.Summary,
		Steps:   append([]PlanStep(nil), c.task.ActivePlan.Steps...),
	}

	if len(plan.Steps) == 0 {
		return nil
	}

	current := -1
	for index, step := range plan.Steps {
		if step.Status == PlanStepInProgress {
			current = index
			break
		}
	}
	if current == -1 {
		for index, step := range plan.Steps {
			if step.Status == PlanStepPending {
				current = index
				break
			}
		}
	}
	if current == -1 {
		return nil
	}

	plan.Steps[current].Status = PlanStepDone
	for index := current + 1; index < len(plan.Steps); index++ {
		if plan.Steps[index].Status == PlanStepPending {
			plan.Steps[index].Status = PlanStepInProgress
			break
		}
	}

	c.task.ActivePlan = &plan
	event := c.newEvent(EventPlan, plan)
	return &event
}

func (c *LocalController) SubmitShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error) {
	logging.Trace("controller.submit_shell_command", "command", command)
	return c.submitShellCommand(ctx, command, CommandOriginUserShell)
}

func (c *LocalController) submitShellCommand(ctx context.Context, command string, origin CommandOrigin) ([]TranscriptEvent, error) {
	c.mu.Lock()
	execution := c.newExecutionLocked(command, origin)
	c.task.CurrentExecution = &execution
	startEvent := c.newEvent(EventCommandStart, CommandStartPayload{
		Command:   command,
		Execution: execution,
	})
	c.appendEvents(startEvent)
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
		defer c.mu.Unlock()
		failedExecution := execution
		failedExecution.State = CommandExecutionFailed
		failedExecution.Error = "shell command runner is not configured"
		c.task.CurrentExecution = nil
		errEvent := c.newEvent(EventError, TextPayload{Text: "shell command runner is not configured"})
		c.appendEvents(errEvent)
		logging.Trace("controller.shell_command.runner_missing", "execution_id", execution.ID, "command", command)
		return []TranscriptEvent{startEvent, errEvent}, nil
	}

	result, err := c.runTrackedCommand(ctx, execution.ID, command)

	c.mu.Lock()

	if c.task.CurrentExecution == nil || c.task.CurrentExecution.ID != execution.ID {
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
			c.task.CurrentExecution = nil
			c.mu.Unlock()
			logging.Trace("controller.shell_command.canceled", "execution_id", execution.ID, "command", command, "origin", origin)
			return []TranscriptEvent{startEvent}, err
		}
		if result.State == shell.MonitorStateLost {
			lostExecution := execution
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
				c.session.CurrentShell = &shellContext
				if shellContext.Directory != "" {
					c.session.WorkingDirectory = shellContext.Directory
				}
			}

			summary := CommandResultSummary{
				ExecutionID: execution.ID,
				CommandID:   result.CommandID,
				Command:     result.Command,
				Origin:      origin,
				State:       CommandExecutionLost,
				Cause:       result.Cause,
				Confidence:  result.Confidence,
				ExitCode:    result.ExitCode,
				Summary:     lostExecution.LatestOutputTail,
			}
			if result.ShellContext.PromptLine() != "" {
				shellContext := result.ShellContext
				summary.ShellContext = &shellContext
			}
			c.session.RecentShellOutput = lostExecution.LatestOutputTail
			c.task.LastCommandResult = &summary
			c.task.CurrentExecution = nil
			resultEvent := c.newEvent(EventCommandResult, summary)
			c.appendEvents(resultEvent)
			c.mu.Unlock()
			logging.Trace(
				"controller.shell_command.lost",
				"execution_id", execution.ID,
				"command", command,
				"origin", origin,
				"error", err.Error(),
				"tail_preview", logging.Preview(lostExecution.LatestOutputTail, 1000),
			)
			events := []TranscriptEvent{startEvent, resultEvent}
			if isAgentOwnedExecution(origin) {
				recoveryEvents, recoveryErr := c.submitLostExecutionRecovery(ctx, lostExecution)
				if recoveryErr != nil {
					if errors.Is(recoveryErr, context.Canceled) {
						return events, recoveryErr
					}
					errEvent := c.appendRecoveryError(recoveryErr)
					if errEvent != nil {
						events = append(events, *errEvent)
					}
					return events, nil
				}
				events = append(events, recoveryEvents...)
			}
			return events, nil
		}
		failedExecution := execution
		failedExecution.State = CommandExecutionFailed
		failedExecution.Error = err.Error()
		failedExecution.LatestOutputTail = c.bestEffortRecentOutputLocked()
		completedAt := time.Now()
		failedExecution.CompletedAt = &completedAt
		c.task.CurrentExecution = nil
		errEvent := c.newEvent(EventError, TextPayload{Text: err.Error()})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		logging.Trace(
			"controller.shell_command.error",
			"execution_id", execution.ID,
			"command", command,
			"origin", origin,
			"error", err.Error(),
			"tail_preview", logging.Preview(failedExecution.LatestOutputTail, 1000),
		)
		return []TranscriptEvent{startEvent, errEvent}, nil
	}

	if result.State == shell.MonitorStateCanceled {
		canceledExecution := execution
		canceledExecution.State = CommandExecutionCanceled
		canceledExecution.LatestOutputTail = result.Captured
		completedAt := time.Now()
		canceledExecution.CompletedAt = &completedAt
		exitCode := result.ExitCode
		canceledExecution.ExitCode = &exitCode

		summary := CommandResultSummary{
			ExecutionID: execution.ID,
			CommandID:   result.CommandID,
			Command:     result.Command,
			Origin:      origin,
			State:       CommandExecutionCanceled,
			Cause:       result.Cause,
			Confidence:  result.Confidence,
			ExitCode:    result.ExitCode,
			Summary:     result.Captured,
		}
		if result.ShellContext.PromptLine() != "" {
			shellContext := result.ShellContext
			summary.ShellContext = &shellContext
			canceledExecution.ShellContextAfter = &shellContext
			c.session.CurrentShell = &shellContext
			if shellContext.Directory != "" {
				c.session.WorkingDirectory = shellContext.Directory
			}
		}
		c.session.RecentShellOutput = result.Captured
		c.task.LastCommandResult = &summary
		c.task.CurrentExecution = nil
		resultEvent := c.newEvent(EventCommandResult, summary)
		c.appendEvents(resultEvent)
		c.mu.Unlock()
		logging.Trace(
			"controller.shell_command.canceled_result",
			"execution_id", execution.ID,
			"command", command,
			"origin", origin,
			"command_id", result.CommandID,
			"summary_preview", logging.Preview(result.Captured, 1000),
			"prompt", result.ShellContext.PromptLine(),
		)
		return []TranscriptEvent{startEvent, resultEvent}, nil
	}

	completedExecution := execution
	completedExecution.State = CommandExecutionCompleted
	completedExecution.LatestOutputTail = result.Captured
	completedAt := time.Now()
	completedExecution.CompletedAt = &completedAt
	exitCode := result.ExitCode
	completedExecution.ExitCode = &exitCode

	summary := CommandResultSummary{
		ExecutionID: execution.ID,
		CommandID:   result.CommandID,
		Command:     result.Command,
		Origin:      origin,
		State:       CommandExecutionCompleted,
		Cause:       result.Cause,
		Confidence:  result.Confidence,
		ExitCode:    result.ExitCode,
		Summary:     result.Captured,
	}
	if result.ShellContext.PromptLine() != "" {
		shellContext := result.ShellContext
		summary.ShellContext = &shellContext
		completedExecution.ShellContextAfter = &shellContext
		c.session.CurrentShell = &shellContext
		if shellContext.Directory != "" {
			c.session.WorkingDirectory = shellContext.Directory
		}
	}
	c.session.RecentShellOutput = result.Captured
	c.task.LastCommandResult = &summary
	c.task.CurrentExecution = nil
	resultEvent := c.newEvent(EventCommandResult, summary)
	c.appendEvents(resultEvent)
	c.mu.Unlock()
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
	return []TranscriptEvent{startEvent, resultEvent}, nil
}

func (c *LocalController) submitLostExecutionRecovery(ctx context.Context, execution CommandExecution) ([]TranscriptEvent, error) {
	if c.agent == nil {
		return nil, nil
	}

	c.mu.Lock()
	session := c.session
	task := c.task
	c.mu.Unlock()

	if strings.TrimSpace(execution.LatestOutputTail) != "" {
		session.RecentShellOutput = execution.LatestOutputTail
	}
	task.CurrentExecution = &execution
	task.RecoverySnapshot = c.captureRecoverySnapshot(ctx, c.session.TopPaneID, &execution)

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

func (c *LocalController) runTrackedCommand(ctx context.Context, executionID string, command string) (shell.TrackedExecution, error) {
	timeout := shell.CommandTimeout(command)
	if monitorRunner, ok := c.runner.(MonitoringShellRunner); ok {
		monitor, err := monitorRunner.StartTrackedCommand(ctx, c.session.TopPaneID, command, timeout)
		if err != nil {
			return shell.TrackedExecution{}, err
		}
		go c.consumeMonitorSnapshots(executionID, monitor)
		return monitor.Wait()
	}

	return c.runner.RunTrackedCommand(ctx, c.session.TopPaneID, command, timeout)
}

func (c *LocalController) consumeMonitorSnapshots(executionID string, monitor shell.CommandMonitor) {
	for snapshot := range monitor.Updates() {
		c.applyMonitorSnapshot(executionID, snapshot)
	}
}

func (c *LocalController) applyMonitorSnapshot(executionID string, snapshot shell.MonitorSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.task.CurrentExecution == nil || c.task.CurrentExecution.ID != executionID {
		return
	}

	execution := c.task.CurrentExecution
	switch snapshot.State {
	case shell.MonitorStateQueued:
		if execution.State == CommandExecutionQueued {
			execution.State = CommandExecutionQueued
		}
	case shell.MonitorStateRunning:
		if execution.State != CommandExecutionHandoffActive && execution.State != CommandExecutionBackgroundMonitor {
			execution.State = CommandExecutionRunning
		}
	case shell.MonitorStateAwaitingInput:
		if execution.State != CommandExecutionHandoffActive {
			execution.State = CommandExecutionAwaitingInput
		}
	case shell.MonitorStateInteractiveFullscreen:
		if execution.State != CommandExecutionHandoffActive {
			execution.State = CommandExecutionInteractiveFullscreen
		}
	case shell.MonitorStateCanceled:
		execution.State = CommandExecutionCanceled
	case shell.MonitorStateFailed:
		execution.State = CommandExecutionFailed
	case shell.MonitorStateLost:
		execution.State = CommandExecutionLost
	case shell.MonitorStateCompleted:
		execution.State = CommandExecutionCompleted
	}

	if strings.TrimSpace(snapshot.LatestOutputTail) != "" {
		execution.LatestOutputTail = snapshot.LatestOutputTail
		c.session.RecentShellOutput = snapshot.LatestOutputTail
	}
	if strings.TrimSpace(snapshot.ForegroundCommand) != "" {
		execution.ForegroundCommand = snapshot.ForegroundCommand
	}
	if snapshot.ShellContext.PromptLine() != "" {
		contextCopy := snapshot.ShellContext
		execution.ShellContextAfter = &contextCopy
		c.session.CurrentShell = &contextCopy
		if contextCopy.Directory != "" {
			c.session.WorkingDirectory = contextCopy.Directory
		}
	}
	if snapshot.ExitCode != nil {
		exitCode := *snapshot.ExitCode
		execution.ExitCode = &exitCode
	}
	if snapshot.Error != "" {
		execution.Error = snapshot.Error
	}
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
		command := pending.Command
		c.task.PendingApproval = nil
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

	if c.task.CurrentExecution == nil {
		return nil
	}

	execution := *c.task.CurrentExecution
	return &execution
}

func (c *LocalController) AbandonActiveExecution(reason string) *CommandExecution {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.task.CurrentExecution == nil {
		return nil
	}

	execution := *c.task.CurrentExecution
	execution.State = CommandExecutionCanceled
	execution.Error = strings.TrimSpace(reason)
	execution.LatestOutputTail = c.bestEffortRecentOutputLocked()
	completedAt := time.Now()
	execution.CompletedAt = &completedAt
	c.task.CurrentExecution = nil
	logging.Trace(
		"controller.active_execution.abandoned",
		"execution_id", execution.ID,
		"command", execution.Command,
		"origin", execution.Origin,
		"reason", reason,
		"tail_preview", logging.Preview(execution.LatestOutputTail, 1000),
	)
	return &execution
}

func (c *LocalController) RefreshShellContext(ctx context.Context) (*shell.PromptContext, error) {
	if c.reader == nil || c.session.TopPaneID == "" {
		return nil, nil
	}

	promptContext, err := c.reader.CaptureShellContext(ctx, c.session.TopPaneID)
	if err != nil {
		return nil, err
	}
	if promptContext.PromptLine() == "" {
		return nil, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	contextCopy := promptContext
	c.session.CurrentShell = &contextCopy
	if contextCopy.Directory != "" {
		c.session.WorkingDirectory = contextCopy.Directory
	}

	return &contextCopy, nil
}

func (c *LocalController) PeekShellTail(ctx context.Context, lines int) (string, error) {
	if c.reader == nil || c.session.TopPaneID == "" {
		return "", nil
	}

	return c.reader.CaptureRecentOutput(ctx, c.session.TopPaneID, lines)
}

func (c *LocalController) captureRecoverySnapshot(ctx context.Context, topPaneID string, execution *CommandExecution) string {
	if c.reader == nil || topPaneID == "" || !shouldCaptureRecoverySnapshot(execution) {
		return ""
	}

	captured, err := c.reader.CaptureRecentOutput(ctx, topPaneID, recoverySnapshotLines)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(captured)
}

func (c *LocalController) newEvent(kind TranscriptEventKind, payload any) TranscriptEvent {
	return TranscriptEvent{
		ID:        fmt.Sprintf("evt-%d", c.counter.Add(1)),
		Kind:      kind,
		Timestamp: time.Now(),
		Payload:   payload,
	}
}

func (c *LocalController) appendEvents(events ...TranscriptEvent) {
	c.task.PriorTranscript = append(c.task.PriorTranscript, events...)
}

func (c *LocalController) newExecutionLocked(command string, origin CommandOrigin) CommandExecution {
	execution := CommandExecution{
		ID:        fmt.Sprintf("cmd-%d", c.counter.Add(1)),
		Command:   command,
		Origin:    origin,
		State:     CommandExecutionRunning,
		StartedAt: time.Now(),
	}
	if c.session.CurrentShell != nil {
		contextCopy := *c.session.CurrentShell
		execution.ShellContextBefore = &contextCopy
	}
	return execution
}

func (c *LocalController) bestEffortRecentOutputLocked() string {
	if c.reader == nil || c.session.TopPaneID == "" {
		return ""
	}

	output, err := c.reader.CaptureRecentOutput(context.Background(), c.session.TopPaneID, 20)
	if err != nil {
		return ""
	}

	return output
}

func shouldCaptureRecoverySnapshot(execution *CommandExecution) bool {
	if execution == nil {
		return false
	}

	switch execution.State {
	case CommandExecutionAwaitingInput, CommandExecutionInteractiveFullscreen, CommandExecutionHandoffActive, CommandExecutionLost:
		return true
	default:
		return false
	}
}

func isAgentOwnedExecution(origin CommandOrigin) bool {
	switch origin {
	case CommandOriginAgentProposal, CommandOriginAgentApproval, CommandOriginAgentPlan:
		return true
	default:
		return false
	}
}

func buildProposalRefinementPrompt(proposal ProposalPayload, note string) string {
	parts := []string{
		"Revise the previous proposal using the user's note.",
	}
	if proposal.Description != "" {
		parts = append(parts, "Original proposal: "+proposal.Description)
	}
	if proposal.Command != "" {
		parts = append(parts, "Original command: "+proposal.Command)
	}
	if proposal.Keys != "" {
		parts = append(parts, "Original keys: "+proposal.Keys)
	}
	if proposal.Patch != "" {
		parts = append(parts, "Original patch:\n"+proposal.Patch)
	}
	if strings.TrimSpace(note) != "" {
		parts = append(parts, "User note: "+strings.TrimSpace(note))
	}
	return strings.Join(parts, "\n")
}

func errString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

func eventKinds(events []TranscriptEvent) []string {
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, string(event.Kind))
	}
	return kinds
}

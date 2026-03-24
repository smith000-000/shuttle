package controller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aiterm/internal/logging"
	"aiterm/internal/shell"
)

const autoContinuePrompt = "The previously approved or proposed command has completed. First summarize the result briefly. If there is an active plan, continue it from the current step. If there is no active plan, only stop after reporting the outcome when the user's request is already satisfied. If the user's request clearly still requires more shell work, propose the next action. If risky, request approval."
const autoContinuePromptSerialSuffix = "The recent transcript indicates the user asked for serial or ordered shell work. If the completed command only unlocked the next step, propose exactly one next command now instead of waiting for another nudge. Do not lump multiple shell actions together, and do not wait for the user to say 'go' unless they explicitly asked to approve each step separately."
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

type ForegroundMonitoringShellRunner interface {
	AttachForegroundCommand(ctx context.Context, paneID string) (shell.CommandMonitor, error)
}

type TrackedPaneResolver interface {
	ResolveTrackedPane(ctx context.Context, paneID string) (string, error)
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

	mu               sync.Mutex
	counter          atomic.Uint64
	task             TaskContext
	executions       map[string]*CommandExecution
	primaryExecution string
}

func New(agent Agent, runner ShellRunner, reader ShellContextReader, session SessionContext) *LocalController {
	controller := &LocalController{
		agent:   agent,
		runner:  runner,
		reader:  reader,
		session: normalizeSessionContext(session),
		task: TaskContext{
			TaskID: "task-1",
		},
	}
	controller.syncTaskExecutionViewsLocked()
	return controller
}

func normalizeSessionContext(session SessionContext) SessionContext {
	session.SessionName = strings.TrimSpace(session.SessionName)
	session.TopPaneID = strings.TrimSpace(session.TopPaneID)
	session.BottomPaneID = strings.TrimSpace(session.BottomPaneID)
	session.WorkingDirectory = strings.TrimSpace(session.WorkingDirectory)
	session.RecentShellOutput = strings.TrimSpace(session.RecentShellOutput)
	session.TrackedShell.SessionName = strings.TrimSpace(session.TrackedShell.SessionName)
	session.TrackedShell.PaneID = strings.TrimSpace(session.TrackedShell.PaneID)

	if session.TrackedShell.SessionName == "" {
		session.TrackedShell.SessionName = session.SessionName
	}
	if session.TrackedShell.PaneID == "" {
		session.TrackedShell.PaneID = session.TopPaneID
	}
	if session.SessionName == "" {
		session.SessionName = session.TrackedShell.SessionName
	}
	if session.TopPaneID == "" {
		session.TopPaneID = session.TrackedShell.PaneID
	}

	return session
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

	prompt := buildAutoContinuePrompt(c.task)
	logging.Trace("controller.continue_after_command")
	events, err := c.submitAgentTurn(ctx, "", prompt, nil, false)
	if planEvent != nil {
		events = append([]TranscriptEvent{*planEvent}, events...)
	}
	return events, err
}

func (c *LocalController) ResumeAfterTakeControl(ctx context.Context) ([]TranscriptEvent, error) {
	logging.Trace("controller.resume_after_take_control")
	reconciledEvents, reconciledAgentOwned, reconciled, err := c.reconcileExecutionAfterTakeControl(ctx)
	if err != nil {
		return nil, err
	}
	if reconciled {
		if reconciledAgentOwned {
			events, err := c.submitAgentTurn(ctx, "", resumeAfterTakeControlPrompt, nil, false)
			if err != nil {
				return reconciledEvents, err
			}
			return append(reconciledEvents, events...), nil
		}
		return reconciledEvents, nil
	}

	attachedEvents, attached, attachErr := c.attachForegroundExecution(ctx)
	if attachErr != nil {
		return nil, attachErr
	}
	if attached {
		return attachedEvents, nil
	}

	c.mu.Lock()
	primary := c.primaryExecutionLocked()
	agentOwned := primary != nil && isAgentOwnedExecution(primary.Origin)
	c.mu.Unlock()
	if !agentOwned {
		return nil, nil
	}
	return c.submitAgentTurn(ctx, "", resumeAfterTakeControlPrompt, nil, false)
}

func (c *LocalController) reconcileExecutionAfterTakeControl(ctx context.Context) ([]TranscriptEvent, bool, bool, error) {
	trackedShell := c.syncTrackedShellTarget(ctx)
	if c.reader == nil || trackedShell.PaneID == "" {
		return nil, false, false, nil
	}

	c.mu.Lock()
	executionPtr := c.primaryExecutionLocked()
	if executionPtr == nil {
		c.mu.Unlock()
		return nil, false, false, nil
	}
	execution := *executionPtr
	c.mu.Unlock()

	promptContext, err := c.reader.CaptureShellContext(ctx, trackedShell.PaneID)
	if err != nil {
		return nil, false, false, err
	}
	if promptContext.PromptLine() == "" || promptContext.LastExitCode == nil {
		if promptContext.PromptLine() == "" {
			return nil, false, false, nil
		}
	}

	recentOutput := ""
	if captured, captureErr := c.reader.CaptureRecentOutput(ctx, trackedShell.PaneID, 120); captureErr == nil {
		recentOutput = strings.TrimSpace(captured)
	}
	if recentOutput == "" {
		recentOutput = strings.TrimSpace(execution.LatestOutputTail)
	}
	if promptContext.PromptLine() != "" {
		recentOutput = shell.TrimTrailingPromptLine(recentOutput, promptContext)
	}

	exitCode, state, confidence, semanticShell, semanticSource, inferred := inferHandoffPromptReturnResult(promptContext, execution, recentOutput)

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
	}
	contextCopy := promptContext
	summary.ShellContext = &contextCopy

	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.executionLocked(execution.ID)
	if current == nil {
		return nil, false, false, nil
	}
	c.session.CurrentShell = &contextCopy
	if contextCopy.Directory != "" {
		c.session.WorkingDirectory = contextCopy.Directory
	}
	c.session.RecentShellOutput = recentOutput
	c.task.LastCommandResult = &summary
	c.removeExecutionLocked(execution.ID)
	resultEvent := c.newEvent(EventCommandResult, summary)
	c.appendEvents(resultEvent)
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
		"tail_preview", logging.Preview(recentOutput, 1000),
	)
	return []TranscriptEvent{resultEvent}, isAgentOwnedExecution(execution.Origin), true, nil
}

func inferHandoffPromptReturnResult(promptContext shell.PromptContext, execution CommandExecution, recentOutput string) (int, CommandExecutionState, shell.SignalConfidence, bool, string, bool) {
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

func (c *LocalController) attachForegroundExecution(ctx context.Context) ([]TranscriptEvent, bool, error) {
	trackedShell := c.syncTrackedShellTarget(ctx)
	if trackedShell.PaneID == "" {
		return nil, false, nil
	}

	monitorRunner, ok := c.runner.(ForegroundMonitoringShellRunner)
	if !ok {
		return nil, false, nil
	}

	c.mu.Lock()
	if c.primaryExecutionLocked() != nil {
		c.mu.Unlock()
		return nil, false, nil
	}
	c.mu.Unlock()

	monitor, err := monitorRunner.AttachForegroundCommand(ctx, trackedShell.PaneID)
	if err != nil || monitor == nil {
		return nil, false, err
	}

	snapshot := monitor.Snapshot()
	command := strings.TrimSpace(snapshot.Command)
	if command == "" {
		command = "manual shell command"
	}

	c.mu.Lock()
	execution := c.newExecutionLocked(command, CommandOriginUserShell)
	execution.OwnershipMode = CommandOwnershipSharedObserver
	if !snapshot.StartedAt.IsZero() {
		execution.StartedAt = snapshot.StartedAt
	}
	registered := c.registerExecutionLocked(execution)
	c.applyMonitorSnapshot(registered.ID, snapshot)
	current := *c.executionLocked(registered.ID)
	startEvent := c.newEvent(EventCommandStart, CommandStartPayload{
		Command:   current.Command,
		Execution: current,
	})
	c.appendEvents(startEvent)
	c.mu.Unlock()

	go c.consumeMonitorSnapshots(execution.ID, monitor)
	go c.awaitAttachedMonitor(execution.ID, monitor)

	logging.Trace(
		"controller.attach_foreground_execution",
		"execution_id", execution.ID,
		"command", current.Command,
		"state", current.State,
		"foreground_command", snapshot.ForegroundCommand,
	)
	return []TranscriptEvent{startEvent}, true, nil
}

func (c *LocalController) CheckActiveExecution(ctx context.Context) ([]TranscriptEvent, error) {
	if c.agent == nil {
		return nil, nil
	}

	c.mu.Lock()
	execution := c.primaryExecutionLocked()
	if execution == nil || !isAgentOwnedExecution(execution.Origin) {
		c.mu.Unlock()
		return nil, nil
	}

	if execution.State != CommandExecutionAwaitingInput &&
		execution.State != CommandExecutionInteractiveFullscreen &&
		execution.State != CommandExecutionLost {
		execution.State = CommandExecutionBackgroundMonitor
	}
	session := c.session
	task := c.task
	recentOutput := strings.TrimSpace(execution.LatestOutputTail)
	c.mu.Unlock()
	trackedShell := c.syncTrackedShellTarget(ctx)
	session.TrackedShell = trackedShell
	if trackedShell.SessionName != "" {
		session.SessionName = trackedShell.SessionName
	}
	if trackedShell.PaneID != "" {
		session.TopPaneID = trackedShell.PaneID
	}

	if recentOutput == "" && c.reader != nil && trackedShell.PaneID != "" {
		captured, err := c.reader.CaptureRecentOutput(ctx, trackedShell.PaneID, 120)
		if err == nil {
			recentOutput = captured
		}
	}
	recoverySnapshot := c.captureRecoverySnapshot(ctx, trackedShell.PaneID, task.CurrentExecution)

	c.mu.Lock()
	execution = c.primaryExecutionLocked()
	if execution == nil || !isAgentOwnedExecution(execution.Origin) {
		c.mu.Unlock()
		return nil, nil
	}
	if execution.State != CommandExecutionAwaitingInput &&
		execution.State != CommandExecutionInteractiveFullscreen &&
		execution.State != CommandExecutionLost {
		execution.State = CommandExecutionBackgroundMonitor
	}
	if strings.TrimSpace(recentOutput) != "" {
		execution.LatestOutputTail = recentOutput
		c.session.RecentShellOutput = recentOutput
	}
	c.task.RecoverySnapshot = recoverySnapshot
	c.syncTaskExecutionViewsLocked()
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
	if c.primaryExecutionLocked() == nil {
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
	trackedShell := c.syncTrackedShellTarget(ctx)

	recentOutput := ""
	if c.reader != nil && trackedShell.PaneID != "" {
		captured, err := c.reader.CaptureRecentOutput(ctx, trackedShell.PaneID, 120)
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
	session.TrackedShell = trackedShell
	if trackedShell.SessionName != "" {
		session.SessionName = trackedShell.SessionName
	}
	if trackedShell.PaneID != "" {
		session.TopPaneID = trackedShell.PaneID
	}
	if recentOutput != "" {
		session.RecentShellOutput = recentOutput
		c.session.RecentShellOutput = recentOutput
	}
	task := c.task
	task.RecoverySnapshot = c.captureRecoverySnapshot(ctx, trackedShell.PaneID, task.CurrentExecution)

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
	if shouldSuppressReturnedPlan(response.Plan, emitUserMessage, userPrompt, c.task.ActivePlan) {
		response.Plan = nil
	}
	completedPlan := completionPlanFromContinuation(response, emitUserMessage, c.task.ActivePlan)

	newEvents := append([]TranscriptEvent(nil), events...)

	if response.Message != "" {
		newEvents = append(newEvents, c.newEvent(EventAgentMessage, TextPayload{Text: response.Message}))
	}

	if response.ModelInfo != nil {
		modelInfo := *response.ModelInfo
		newEvents = append(newEvents, c.newEvent(EventModelInfo, modelInfo))
	}

	if completedPlan != nil {
		c.task.ActivePlan = nil
		newEvents = append(newEvents, c.newEvent(EventPlan, *completedPlan))
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
		step = normalizePlanStepText(step)
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
	if response.Proposal != nil && !isActionableProposal(*response.Proposal) {
		if strings.TrimSpace(response.Message) == "" && strings.TrimSpace(response.Proposal.Description) != "" {
			response.Message = strings.TrimSpace(response.Proposal.Description)
		}
		response.Proposal = nil
	}

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

func isActionableProposal(proposal Proposal) bool {
	return strings.TrimSpace(proposal.Command) != "" ||
		proposal.Keys != "" ||
		strings.TrimSpace(proposal.Patch) != ""
}

func completionPlanFromContinuation(response AgentResponse, emitUserMessage bool, activePlan *ActivePlan) *ActivePlan {
	if emitUserMessage || activePlan == nil {
		return nil
	}
	if response.Plan != nil || response.Proposal != nil || response.Approval != nil {
		return nil
	}
	if !messageIndicatesPlanCompletion(response.Message) {
		return nil
	}

	completed := completePlan(*activePlan)
	return &completed
}

func shouldSuppressReturnedPlan(plan *Plan, emitUserMessage bool, userPrompt string, existing *ActivePlan) bool {
	if plan == nil {
		return false
	}
	if !emitUserMessage {
		return true
	}
	if existing != nil && !isExplicitPlanRequest(userPrompt) {
		return true
	}
	return false
}

func messageIndicatesPlanCompletion(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return false
	}
	return containsAnySubstring(
		message,
		"plan is complete",
		"active plan is complete",
		"workflow is complete",
		"workflow completed",
		"workflow fully completed",
		"no further action is needed",
		"no further shell work is needed",
		"task is complete",
	)
}

func completePlan(plan ActivePlan) ActivePlan {
	completed := ActivePlan{
		Summary: plan.Summary,
		Steps:   append([]PlanStep(nil), plan.Steps...),
	}
	for index := range completed.Steps {
		completed.Steps[index].Status = PlanStepDone
	}
	return completed
}

func isExplicitPlanRequest(prompt string) bool {
	prompt = strings.ToLower(strings.TrimSpace(prompt))
	if prompt == "" {
		return false
	}
	return containsAnySubstring(
		prompt,
		"plan",
		"next step",
		"next steps",
		"strategy",
		"approach",
		"checklist",
		"troubleshoot",
	)
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

	if isPlanComplete(plan) {
		c.task.ActivePlan = nil
	} else {
		c.task.ActivePlan = &plan
	}
	event := c.newEvent(EventPlan, plan)
	return &event
}

func (c *LocalController) SubmitShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error) {
	logging.Trace("controller.submit_shell_command", "command", command)
	return c.submitShellCommand(ctx, command, CommandOriginUserShell)
}

func (c *LocalController) submitShellCommand(ctx context.Context, command string, origin CommandOrigin) ([]TranscriptEvent, error) {
	trackedShell := c.syncTrackedShellTarget(ctx)

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
		return []TranscriptEvent{errEvent}, nil
	}
	execution := c.newExecutionLocked(command, origin)
	execution.TrackedShell = trackedShell
	registered := c.registerExecutionLocked(execution)
	startEvent := c.newEvent(EventCommandStart, CommandStartPayload{
		Command:   command,
		Execution: *registered,
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
		c.removeExecutionLocked(execution.ID)
		errEvent := c.newEvent(EventError, TextPayload{Text: "shell command runner is not configured"})
		c.appendEvents(errEvent)
		logging.Trace("controller.shell_command.runner_missing", "execution_id", execution.ID, "command", command)
		return []TranscriptEvent{startEvent, errEvent}, nil
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
			c.removeExecutionLocked(execution.ID)
			c.mu.Unlock()
			logging.Trace("controller.shell_command.canceled", "execution_id", execution.ID, "command", command, "origin", origin)
			return []TranscriptEvent{startEvent}, err
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
				c.session.CurrentShell = &shellContext
				if shellContext.Directory != "" {
					c.session.WorkingDirectory = shellContext.Directory
				}
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
			c.session.RecentShellOutput = lostExecution.LatestOutputTail
			c.task.LastCommandResult = &summary
			c.removeExecutionLocked(execution.ID)
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
		failedExecution := *current
		failedExecution.State = CommandExecutionFailed
		failedExecution.Error = err.Error()
		failedExecution.LatestOutputTail = c.bestEffortRecentOutputLocked()
		completedAt := time.Now()
		failedExecution.CompletedAt = &completedAt
		c.removeExecutionLocked(execution.ID)
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
			c.session.CurrentShell = &shellContext
			if shellContext.Directory != "" {
				c.session.WorkingDirectory = shellContext.Directory
			}
		}
		c.session.RecentShellOutput = result.Captured
		c.task.LastCommandResult = &summary
		c.removeExecutionLocked(execution.ID)
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
		c.session.CurrentShell = &shellContext
		if shellContext.Directory != "" {
			c.session.WorkingDirectory = shellContext.Directory
		}
	}
	c.session.RecentShellOutput = result.Captured
	c.task.LastCommandResult = &summary
	c.removeExecutionLocked(execution.ID)
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
	trackedShell := c.syncTrackedShellTarget(ctx)
	session.TrackedShell = trackedShell
	if trackedShell.SessionName != "" {
		session.SessionName = trackedShell.SessionName
	}
	if trackedShell.PaneID != "" {
		session.TopPaneID = trackedShell.PaneID
	}

	if strings.TrimSpace(execution.LatestOutputTail) != "" {
		session.RecentShellOutput = execution.LatestOutputTail
	}
	task.CurrentExecution = &execution
	task.PrimaryExecutionID = execution.ID
	task.ExecutionRegistry = []CommandExecution{execution}
	task.RecoverySnapshot = c.captureRecoverySnapshot(ctx, trackedShell.PaneID, &execution)

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
	trackedShell := c.syncTrackedShellTarget(ctx)
	if monitorRunner, ok := c.runner.(MonitoringShellRunner); ok {
		monitor, err := monitorRunner.StartTrackedCommand(ctx, trackedShell.PaneID, command, timeout)
		if err != nil {
			return shell.TrackedExecution{}, err
		}
		go c.consumeMonitorSnapshots(executionID, monitor)
		return monitor.Wait()
	}

	return c.runner.RunTrackedCommand(ctx, trackedShell.PaneID, command, timeout)
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

	completedAt := time.Now()
	execution.CompletedAt = &completedAt
	if result.ShellContext.PromptLine() != "" {
		contextCopy := result.ShellContext
		execution.ShellContextAfter = &contextCopy
		c.session.CurrentShell = &contextCopy
		if contextCopy.Directory != "" {
			c.session.WorkingDirectory = contextCopy.Directory
		}
	}
	if strings.TrimSpace(result.Captured) != "" {
		execution.LatestOutputTail = result.Captured
		c.session.RecentShellOutput = result.Captured
	}
	if result.ExitCode != 0 || result.State == shell.MonitorStateCompleted || result.State == shell.MonitorStateCanceled {
		exitCode := result.ExitCode
		execution.ExitCode = &exitCode
	}
	if err != nil {
		execution.Error = err.Error()
	}
	execution.State = commandExecutionStateFromMonitorState(result.State)

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
		Summary:        result.Captured,
	}
	if result.ShellContext.PromptLine() != "" {
		contextCopy := result.ShellContext
		summary.ShellContext = &contextCopy
	}
	c.task.LastCommandResult = &summary
	c.removeExecutionLocked(executionID)
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

	execution := c.executionLocked(executionID)
	if execution == nil {
		return
	}

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
	execution.SemanticShell = snapshot.SemanticShell
	execution.SemanticSource = snapshot.SemanticSource
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
	c.syncTaskExecutionViewsLocked()
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
	return cloneCommandExecution(c.primaryExecutionLocked())
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
	executionCopy.LatestOutputTail = c.bestEffortRecentOutputLocked()
	completedAt := time.Now()
	executionCopy.CompletedAt = &completedAt
	c.removeExecutionLocked(execution.ID)
	logging.Trace(
		"controller.active_execution.abandoned",
		"execution_id", executionCopy.ID,
		"command", executionCopy.Command,
		"origin", executionCopy.Origin,
		"reason", reason,
		"tail_preview", logging.Preview(executionCopy.LatestOutputTail, 1000),
	)
	return &executionCopy
}

func (c *LocalController) RefreshShellContext(ctx context.Context) (*shell.PromptContext, error) {
	trackedShell := c.syncTrackedShellTarget(ctx)
	if c.reader == nil || trackedShell.PaneID == "" {
		return nil, nil
	}

	promptContext, err := c.reader.CaptureShellContext(ctx, trackedShell.PaneID)
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
	trackedShell := c.syncTrackedShellTarget(ctx)
	if c.reader == nil || trackedShell.PaneID == "" {
		return "", nil
	}

	return c.reader.CaptureRecentOutput(ctx, trackedShell.PaneID, lines)
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

func cloneCommandExecution(execution *CommandExecution) *CommandExecution {
	if execution == nil {
		return nil
	}
	copy := *execution
	return &copy
}

func executionSortLess(left *CommandExecution, right *CommandExecution) bool {
	if left == nil || right == nil {
		return false
	}
	if !left.StartedAt.Equal(right.StartedAt) {
		return left.StartedAt.Before(right.StartedAt)
	}
	return left.ID < right.ID
}

func (c *LocalController) primaryExecutionLocked() *CommandExecution {
	c.bootstrapLegacyCurrentExecutionLocked()
	if len(c.executions) == 0 {
		return nil
	}
	if strings.TrimSpace(c.primaryExecution) != "" {
		if execution := c.executions[c.primaryExecution]; execution != nil {
			return execution
		}
	}

	var selected *CommandExecution
	for _, execution := range c.executions {
		if execution == nil {
			continue
		}
		if selected == nil || executionSortLess(selected, execution) {
			selected = execution
		}
	}
	if selected != nil {
		c.primaryExecution = selected.ID
	}
	return selected
}

func (c *LocalController) executionLocked(executionID string) *CommandExecution {
	c.bootstrapLegacyCurrentExecutionLocked()
	if strings.TrimSpace(executionID) == "" || len(c.executions) == 0 {
		return nil
	}
	return c.executions[executionID]
}

func (c *LocalController) bootstrapLegacyCurrentExecutionLocked() {
	if len(c.executions) != 0 || c.task.CurrentExecution == nil || strings.TrimSpace(c.task.CurrentExecution.ID) == "" {
		return
	}
	c.ensureExecutionRegistryLocked()
	executionCopy := *c.task.CurrentExecution
	c.executions[executionCopy.ID] = &executionCopy
	if strings.TrimSpace(c.primaryExecution) == "" {
		c.primaryExecution = executionCopy.ID
	}
}

func (c *LocalController) ensureExecutionRegistryLocked() {
	if c.executions == nil {
		c.executions = make(map[string]*CommandExecution)
	}
}

func (c *LocalController) registerExecutionLocked(execution CommandExecution) *CommandExecution {
	c.ensureExecutionRegistryLocked()
	executionCopy := execution
	c.executions[executionCopy.ID] = &executionCopy
	c.primaryExecution = executionCopy.ID
	c.syncTaskExecutionViewsLocked()
	return c.executions[executionCopy.ID]
}

func (c *LocalController) removeExecutionLocked(executionID string) {
	if len(c.executions) == 0 {
		return
	}
	delete(c.executions, executionID)
	if c.primaryExecution == executionID {
		c.primaryExecution = ""
	}
	c.syncTaskExecutionViewsLocked()
}

func (c *LocalController) syncTaskExecutionViewsLocked() {
	if len(c.executions) == 0 {
		c.task.PrimaryExecutionID = ""
		c.task.ExecutionRegistry = nil
		c.task.CurrentExecution = nil
		return
	}

	registry := make([]*CommandExecution, 0, len(c.executions))
	for _, execution := range c.executions {
		if execution != nil {
			registry = append(registry, execution)
		}
	}
	sort.Slice(registry, func(i int, j int) bool {
		return executionSortLess(registry[i], registry[j])
	})

	snapshots := make([]CommandExecution, 0, len(registry))
	for _, execution := range registry {
		snapshots = append(snapshots, *execution)
	}
	c.task.ExecutionRegistry = snapshots

	primary := c.primaryExecutionLocked()
	if primary == nil {
		c.task.PrimaryExecutionID = ""
		c.task.CurrentExecution = nil
		return
	}
	c.task.PrimaryExecutionID = primary.ID
	c.task.CurrentExecution = cloneCommandExecution(primary)
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
		ID:            fmt.Sprintf("cmd-%d", c.counter.Add(1)),
		Command:       command,
		Origin:        origin,
		TrackedShell:  c.session.TrackedShell,
		OwnershipMode: CommandOwnershipExclusive,
		State:         CommandExecutionRunning,
		StartedAt:     time.Now(),
	}
	if c.session.CurrentShell != nil {
		contextCopy := *c.session.CurrentShell
		execution.ShellContextBefore = &contextCopy
	}
	return execution
}

func (c *LocalController) bestEffortRecentOutputLocked() string {
	paneID := strings.TrimSpace(c.session.TrackedShell.PaneID)
	if paneID == "" {
		paneID = strings.TrimSpace(c.session.TopPaneID)
	}
	if c.reader == nil || paneID == "" {
		return ""
	}

	output, err := c.reader.CaptureRecentOutput(context.Background(), paneID, 20)
	if err != nil {
		return ""
	}

	return output
}

func (c *LocalController) TopPaneID() string {
	return c.TrackedShellTarget().PaneID
}

func (c *LocalController) TrackedShellTarget() TrackedShellTarget {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = normalizeSessionContext(c.session)
	return c.session.TrackedShell
}

func (c *LocalController) syncTrackedShellTarget(ctx context.Context) TrackedShellTarget {
	c.mu.Lock()
	c.session = normalizeSessionContext(c.session)
	current := c.session.TrackedShell
	reader := c.reader
	runner := c.runner
	c.mu.Unlock()

	if current.PaneID == "" {
		return current
	}

	var resolver TrackedPaneResolver
	if reader != nil {
		resolver, _ = reader.(TrackedPaneResolver)
	}
	if resolver == nil && runner != nil {
		resolver, _ = runner.(TrackedPaneResolver)
	}
	if resolver == nil {
		return current
	}

	resolved, err := resolver.ResolveTrackedPane(ctx, current.PaneID)
	if err != nil || strings.TrimSpace(resolved) == "" {
		return current
	}
	resolved = strings.TrimSpace(resolved)

	c.mu.Lock()
	previous := c.session.TrackedShell
	c.session.TrackedShell.SessionName = current.SessionName
	c.session.TopPaneID = resolved
	c.session.TrackedShell.PaneID = resolved
	c.session = normalizeSessionContext(c.session)
	updated := c.session.TrackedShell
	c.mu.Unlock()

	if previous.PaneID != updated.PaneID || previous.SessionName != updated.SessionName {
		logging.Trace(
			"controller.tracked_shell.updated",
			"session", updated.SessionName,
			"previous_pane", previous.PaneID,
			"current_pane", updated.PaneID,
		)
	}

	return updated
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

func blocksSerialShellSubmission(execution *CommandExecution) bool {
	if execution == nil {
		return false
	}

	switch execution.State {
	case CommandExecutionCompleted, CommandExecutionFailed, CommandExecutionCanceled, CommandExecutionLost:
		return false
	default:
		return true
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

func normalizePlanStepText(step string) string {
	step = strings.TrimSpace(step)
	if step == "" {
		return ""
	}

	lower := strings.ToLower(step)
	for _, prefix := range []string{"[done]", "[in_progress]", "[pending]", "[in progress]", "done:", "in_progress:", "in progress:", "pending:"} {
		if strings.HasPrefix(lower, prefix) {
			step = strings.TrimSpace(step[len(prefix):])
			lower = strings.ToLower(step)
		}
	}

	return step
}

func isPlanComplete(plan ActivePlan) bool {
	if len(plan.Steps) == 0 {
		return false
	}
	for _, step := range plan.Steps {
		if step.Status != PlanStepDone {
			return false
		}
	}
	return true
}

func buildAutoContinuePrompt(task TaskContext) string {
	prompt := autoContinuePrompt
	if !shouldPreferSerialContinuation(task) {
		return prompt
	}
	return prompt + " " + autoContinuePromptSerialSuffix
}

func shouldPreferSerialContinuation(task TaskContext) bool {
	userPrompt := strings.ToLower(strings.TrimSpace(latestUserTranscriptMessage(task.PriorTranscript)))
	if userPrompt == "" {
		return false
	}

	return containsAnySubstring(
		userPrompt,
		"serial",
		"one at a time",
		"one command at a time",
		"don't lump",
		"dont lump",
		"do not lump",
		"then ",
		" then",
		"after ",
		" next",
		"step ",
	)
}

func latestUserTranscriptMessage(events []TranscriptEvent) string {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Kind != EventUserMessage {
			continue
		}
		payload, _ := event.Payload.(TextPayload)
		if strings.TrimSpace(payload.Text) != "" {
			return payload.Text
		}
	}
	return ""
}

func containsAnySubstring(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
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

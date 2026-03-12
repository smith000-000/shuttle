package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aiterm/internal/shell"
)

const autoContinuePrompt = "The previously approved or proposed command has completed. Continue the task using the latest shell output and command result. If another action is needed, propose it. If risky, request approval. If the task is complete, answer briefly."
const continuePlanPrompt = "Continue the active plan from the current step. Propose the next safe action if one is needed. If the next action is risky, request approval. If the plan is complete, answer briefly."
const resumeAfterTakeControlPrompt = "The user temporarily took control of the shell to handle an interactive step such as a password prompt, remote login, or fullscreen terminal app. Reassess the latest shell state and continue the task. If another action is needed, propose it. If risky, request approval. If the task is complete, answer briefly."

type ShellRunner interface {
	RunTrackedCommand(ctx context.Context, paneID string, command string, timeout time.Duration) (shell.TrackedExecution, error)
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
	Patch       string
	Description string
}

type CommandStartPayload struct {
	Command string
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
	return c.submitAgentTurn(ctx, prompt, nil, true)
}

func (c *LocalController) SubmitRefinement(ctx context.Context, approval ApprovalRequest, note string) ([]TranscriptEvent, error) {
	return c.submitAgentTurn(ctx, note, &approval, true)
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

	return c.submitAgentTurn(ctx, continuePlanPrompt, nil, false)
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

	events, err := c.submitAgentTurn(ctx, autoContinuePrompt, nil, false)
	if planEvent != nil {
		events = append([]TranscriptEvent{*planEvent}, events...)
	}
	return events, err
}

func (c *LocalController) ResumeAfterTakeControl(ctx context.Context) ([]TranscriptEvent, error) {
	return c.submitAgentTurn(ctx, resumeAfterTakeControlPrompt, nil, false)
}

func (c *LocalController) SubmitInteractiveShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error) {
	return c.SubmitShellCommand(ctx, command)
}

func (c *LocalController) submitAgentTurn(ctx context.Context, prompt string, refinement *ApprovalRequest, emitUserMessage bool) ([]TranscriptEvent, error) {
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
		events = append(events, c.newEvent(EventUserMessage, TextPayload{Text: prompt}))
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

	input := AgentInput{
		Session: session,
		Task:    c.task,
		Prompt:  prompt,
	}
	if refinement != nil {
		refinementCopy := *refinement
		input.Task.PendingApproval = &refinementCopy
	}

	response, err := c.agent.Respond(ctx, input)
	if err != nil {
		errEvent := c.newEvent(EventError, TextPayload{Text: err.Error()})
		c.appendEvents(events...)
		c.appendEvents(errEvent)
		return append(append([]TranscriptEvent(nil), events...), errEvent), nil
	}

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
	c.mu.Lock()
	startEvent := c.newEvent(EventCommandStart, CommandStartPayload{Command: command})
	c.appendEvents(startEvent)
	c.mu.Unlock()

	if c.runner == nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		errEvent := c.newEvent(EventError, TextPayload{Text: "shell command runner is not configured"})
		c.appendEvents(errEvent)
		return []TranscriptEvent{startEvent, errEvent}, nil
	}

	result, err := c.runner.RunTrackedCommand(ctx, c.session.TopPaneID, command, shell.CommandTimeout(command))

	c.mu.Lock()
	defer c.mu.Unlock()

	if err != nil {
		errEvent := c.newEvent(EventError, TextPayload{Text: err.Error()})
		c.appendEvents(errEvent)
		return []TranscriptEvent{startEvent, errEvent}, nil
	}

	summary := CommandResultSummary{
		CommandID: result.CommandID,
		Command:   result.Command,
		ExitCode:  result.ExitCode,
		Summary:   result.Captured,
	}
	if result.ShellContext.PromptLine() != "" {
		shellContext := result.ShellContext
		summary.ShellContext = &shellContext
		c.session.CurrentShell = &shellContext
		if shellContext.Directory != "" {
			c.session.WorkingDirectory = shellContext.Directory
		}
	}
	c.session.RecentShellOutput = result.Captured
	c.task.LastCommandResult = &summary
	resultEvent := c.newEvent(EventCommandResult, summary)
	c.appendEvents(resultEvent)
	return []TranscriptEvent{startEvent, resultEvent}, nil
}

func (c *LocalController) DecideApproval(ctx context.Context, approvalID string, decision ApprovalDecision, refineText string) ([]TranscriptEvent, error) {
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
		c.mu.Unlock()
		return c.SubmitShellCommand(ctx, command)
	default:
		c.mu.Unlock()
		return nil, fmt.Errorf("unsupported approval decision %q", decision)
	}
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

package controller

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"aiterm/internal/shell"
)

type ShellRunner interface {
	RunTrackedCommand(ctx context.Context, paneID string, command string, timeout time.Duration) (shell.TrackedExecution, error)
}

type ShellContextReader interface {
	CaptureRecentOutput(ctx context.Context, paneID string, lines int) (string, error)
}

type TextPayload struct {
	Text string
}

type PlanPayload struct {
	Summary string
	Steps   []string
}

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
	return c.submitAgentPrompt(ctx, prompt, nil)
}

func (c *LocalController) SubmitRefinement(ctx context.Context, approval ApprovalRequest, note string) ([]TranscriptEvent, error) {
	return c.submitAgentPrompt(ctx, note, &approval)
}

func (c *LocalController) submitAgentPrompt(ctx context.Context, prompt string, refinement *ApprovalRequest) ([]TranscriptEvent, error) {
	recentOutput := ""
	if c.reader != nil && c.session.TopPaneID != "" {
		captured, err := c.reader.CaptureRecentOutput(ctx, c.session.TopPaneID, 120)
		if err == nil {
			recentOutput = captured
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	events := []TranscriptEvent{
		c.newEvent(EventUserMessage, TextPayload{Text: prompt}),
	}

	if c.agent == nil {
		errEvent := c.newEvent(EventError, TextPayload{Text: "agent runtime is not configured"})
		c.appendEvents(events...)
		c.appendEvents(errEvent)
		return []TranscriptEvent{events[0], errEvent}, nil
	}

	session := c.session
	session.RecentShellOutput = recentOutput
	if recentOutput != "" {
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
		return []TranscriptEvent{events[0], errEvent}, nil
	}

	newEvents := []TranscriptEvent{events[0]}

	if response.Message != "" {
		newEvents = append(newEvents, c.newEvent(EventAgentMessage, TextPayload{Text: response.Message}))
	}

	if response.Plan != nil {
		newEvents = append(newEvents, c.newEvent(EventPlan, PlanPayload{
			Summary: response.Plan.Summary,
			Steps:   append([]string(nil), response.Plan.Steps...),
		}))
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

	result, err := c.runner.RunTrackedCommand(ctx, c.session.TopPaneID, command, 10*time.Second)

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

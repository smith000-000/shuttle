package controller

import (
	"context"
	"strings"
)

func (c *LocalController) SubmitExternalPrompt(ctx context.Context, prompt string) ([]TranscriptEvent, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		c.mu.Lock()
		defer c.mu.Unlock()
		errEvent := c.newEvent(EventError, TextPayload{Text: "external prompt is empty"})
		c.appendEvents(errEvent)
		return []TranscriptEvent{errEvent}, nil
	}

	c.mu.Lock()
	agent, ok := c.agent.(handoffCapableAgent)
	c.mu.Unlock()
	if !ok {
		c.mu.Lock()
		defer c.mu.Unlock()
		errEvent := c.newEvent(EventError, TextPayload{Text: "external routing is not supported by the active agent"})
		c.appendEvents(errEvent)
		return []TranscriptEvent{errEvent}, nil
	}

	return c.submitAgentTurnWith(ctx, prompt, prompt, nil, true, func(ctx context.Context, input AgentInput) (AgentResponse, error) {
		return agent.SubmitExternalPrompt(ctx, input, prompt)
	})
}

func (c *LocalController) DecideHandoff(ctx context.Context, handoffID string, accept bool) ([]TranscriptEvent, error) {
	c.refreshUserShellContext(ctx, true)

	c.mu.Lock()
	pending := c.task.PendingHandoff
	if pending == nil || pending.ID != handoffID {
		errEvent := c.newEvent(EventError, TextPayload{Text: "handoff request not found"})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return []TranscriptEvent{errEvent}, nil
	}
	agent, ok := c.agent.(handoffCapableAgent)
	if !ok {
		errEvent := c.newEvent(EventError, TextPayload{Text: "external handoff is not supported by the active agent"})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return []TranscriptEvent{errEvent}, nil
	}
	if !accept {
		c.task.PendingHandoff = nil
		event := c.newEvent(EventSystemNotice, TextPayload{Text: "Stayed in Shuttle. External handoff dismissed."})
		c.appendEvents(event)
		c.mu.Unlock()
		return []TranscriptEvent{event}, nil
	}

	session := c.session
	task := c.task
	task.RecoverySnapshot = c.captureRecoverySnapshot(ctx, executionTarget(task.CurrentExecution, session.TrackedShell).PaneID, task.CurrentExecution)
	c.mu.Unlock()

	response, err := agent.ActivateExternal(ctx, AgentInput{
		Session: session,
		Task:    task,
		Prompt:  "",
	}, *pending)
	if err != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		errEvent := c.newEvent(EventError, TextPayload{Text: err.Error()})
		c.appendEvents(errEvent)
		return []TranscriptEvent{errEvent}, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.task.PendingHandoff = nil
	events := []TranscriptEvent{
		c.newEvent(EventSystemNotice, TextPayload{Text: "Handing the task to the external coding agent."}),
	}
	events = append(events, c.eventsFromAgentResponseLocked(normalizeAgentResponse(response))...)
	c.appendEvents(events...)
	return append([]TranscriptEvent(nil), events...), nil
}

func (c *LocalController) ResumeExternal(ctx context.Context) ([]TranscriptEvent, error) {
	c.refreshUserShellContext(ctx, true)

	c.mu.Lock()
	agent, ok := c.agent.(handoffCapableAgent)
	if !ok {
		errEvent := c.newEvent(EventError, TextPayload{Text: "external resume is not supported by the active agent"})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return []TranscriptEvent{errEvent}, nil
	}
	session := c.session
	task := c.task
	task.RecoverySnapshot = c.captureRecoverySnapshot(ctx, executionTarget(task.CurrentExecution, session.TrackedShell).PaneID, task.CurrentExecution)
	c.mu.Unlock()

	response, err := agent.ResumeExternal(ctx, AgentInput{
		Session: session,
		Task:    task,
		Prompt:  "",
	})
	if err != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		errEvent := c.newEvent(EventError, TextPayload{Text: err.Error()})
		c.appendEvents(errEvent)
		return []TranscriptEvent{errEvent}, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.task.PendingHandoff = nil
	events := []TranscriptEvent{
		c.newEvent(EventSystemNotice, TextPayload{Text: "Resumed external coding-agent context for this workspace."}),
	}
	events = append(events, c.eventsFromAgentResponseLocked(normalizeAgentResponse(response))...)
	c.appendEvents(events...)
	return append([]TranscriptEvent(nil), events...), nil
}

func (c *LocalController) ReturnToBuiltin(_ context.Context) ([]TranscriptEvent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	agent, ok := c.agent.(handoffCapableAgent)
	if !ok {
		errEvent := c.newEvent(EventError, TextPayload{Text: "return to Shuttle is not supported by the active agent"})
		c.appendEvents(errEvent)
		return []TranscriptEvent{errEvent}, nil
	}
	if err := agent.ReturnToBuiltin(); err != nil {
		errEvent := c.newEvent(EventError, TextPayload{Text: err.Error()})
		c.appendEvents(errEvent)
		return []TranscriptEvent{errEvent}, nil
	}
	event := c.newEvent(EventSystemNotice, TextPayload{Text: "Returned control to Shuttle builtin mode."})
	c.appendEvents(event)
	return []TranscriptEvent{event}, nil
}

func (c *LocalController) ExternalState() ExternalState {
	if agent, ok := c.agent.(handoffCapableAgent); ok {
		return agent.ExternalState()
	}
	return ExternalState{}
}

func (c *LocalController) RuntimeActivity() RuntimeActivitySnapshot {
	if agent, ok := c.agent.(handoffCapableAgent); ok {
		return agent.RuntimeActivity()
	}
	return RuntimeActivitySnapshot{}
}

func (c *LocalController) eventsFromAgentResponseLocked(response AgentResponse) []TranscriptEvent {
	events := make([]TranscriptEvent, 0, 6)
	if response.Message != "" {
		events = append(events, c.newEvent(EventAgentMessage, TextPayload{Text: response.Message}))
	}
	for _, runtimeEvent := range response.RuntimeEvents {
		text := runtimeEvent.Title
		if runtimeEvent.Body != "" {
			if text != "" {
				text += ": "
			}
			text += runtimeEvent.Body
		}
		if text != "" {
			events = append(events, c.newEvent(EventSystemNotice, TextPayload{Text: text}))
		}
	}
	if response.ModelInfo != nil {
		events = append(events, c.newEvent(EventModelInfo, *response.ModelInfo))
	}
	if response.Plan != nil {
		activePlan := buildActivePlan(*response.Plan)
		c.task.ActivePlan = &activePlan
		events = append(events, c.newEvent(EventPlan, activePlan))
	}
	if response.Handoff != nil {
		handoffCopy := *response.Handoff
		c.task.PendingHandoff = &handoffCopy
		events = append(events, c.newEvent(EventHandoff, handoffCopy))
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
	return events
}

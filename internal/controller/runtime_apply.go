package controller

import (
	"context"
	"errors"

	"aiterm/internal/agentruntime"
)

func (c *LocalController) handleRuntimeRequest(ctx context.Context, req agentruntime.Request, emitUserMessage bool) ([]TranscriptEvent, error) {
	events := make([]TranscriptEvent, 0, 4)
	if emitUserMessage && req.UserPrompt != "" {
		c.mu.Lock()
		userEvent := c.newEvent(EventUserMessage, TextPayload{Text: req.UserPrompt})
		c.appendEvents(userEvent)
		c.mu.Unlock()
		events = append(events, userEvent)
	}
	if c.runtime == nil {
		c.mu.Lock()
		errEvent := c.newEvent(EventError, TextPayload{Text: "agent runtime is not configured"})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return append(events, errEvent), nil
	}
	if c.runtimeHost == nil {
		c.mu.Lock()
		errEvent := c.newEvent(EventError, TextPayload{Text: "agent runtime is not configured"})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return append(events, errEvent), nil
	}

	outcome, err := c.runtime.Handle(ctx, c.runtimeHost, req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return events, err
		}
		c.mu.Lock()
		errEvent := c.newEvent(EventError, TextPayload{Text: err.Error()})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return append(events, errEvent), nil
	}

	applied, autoAction := c.applyRuntimeOutcome(outcome, emitUserMessage, req.UserPrompt)
	events = append(events, applied...)

	if autoAction.patch != "" {
		patchEvents, patchErr := c.applyPatch(ctx, autoAction.patch, autoAction.patchTarget)
		events = append(events, patchEvents...)
		if patchErr != nil {
			return events, patchErr
		}
	}
	if autoAction.command != "" {
		commandEvents, commandErr := c.submitShellCommand(ctx, autoAction.command, CommandOriginAgentAuto)
		events = append(events, commandEvents...)
		if commandErr != nil {
			return events, commandErr
		}
	}
	return events, nil
}

func (c *LocalController) applyRuntimeOutcome(outcome agentruntime.Outcome, emitUserMessage bool, userPrompt string) ([]TranscriptEvent, automaticAction) {
	response := controllerOutcomeToRuntimeInput(outcome)

	c.mu.Lock()
	defer c.mu.Unlock()

	if shouldSuppressReturnedPlan(response.Plan, emitUserMessage, userPrompt, c.task.ActivePlan) {
		response.Plan = nil
	}
	completedPlan := completionPlanFromContinuation(response, emitUserMessage, c.task.ActivePlan)
	autoAction := automaticActionFromResponse(c.session, response)
	if autoAction.command != "" || autoAction.patch != "" {
		if response.Approval != nil {
			response.Approval = nil
			response.Proposal = nil
		} else {
			response.Proposal = nil
		}
	}

	events := make([]TranscriptEvent, 0, 6)
	if response.Message != "" {
		events = append(events, c.newEvent(EventAgentMessage, TextPayload{Text: response.Message}))
	}
	if response.ModelInfo != nil {
		modelInfo := *response.ModelInfo
		events = append(events, c.newEvent(EventModelInfo, modelInfo))
	}
	if completedPlan != nil {
		c.task.ActivePlan = nil
		events = append(events, c.newEvent(EventPlan, *completedPlan))
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
			PatchTarget: response.Proposal.PatchTarget,
			Description: response.Proposal.Description,
		}))
	}
	if response.Approval != nil {
		approvalCopy := *response.Approval
		c.task.PendingApproval = &approvalCopy
		events = append(events, c.newEvent(EventApproval, approvalCopy))
	}
	if autoAction.command != "" {
		notice := autoRunNotice(autoAction.command)
		if c.session.ApprovalMode == ApprovalModeDanger {
			notice = "Auto-running agent command under /approvals dangerous: " + autoAction.command
		}
		events = append(events, c.newEvent(EventSystemNotice, TextPayload{Text: notice}))
	}
	if autoAction.patch != "" {
		events = append(events, c.newEvent(EventSystemNotice, TextPayload{Text: "Auto-applying agent patch under /approvals dangerous."}))
	}
	c.appendEvents(events...)
	return events, autoAction
}

package controller

import (
	"context"
	"errors"
	"strings"

	"aiterm/internal/agentruntime"
)

type builtinRuntimeHost struct {
	controller *LocalController
	agent      Agent
}

func newBuiltinRuntimeHost(controller *LocalController, agent Agent) agentruntime.Host {
	if agent == nil {
		return nil
	}
	return builtinRuntimeHost{controller: controller, agent: agent}
}

func (h builtinRuntimeHost) Respond(ctx context.Context, req agentruntime.Request) (agentruntime.Outcome, error) {
	c := h.controller
	if _, err := c.RefreshShellContext(ctx); err != nil && req.Kind != agentruntime.RequestExecutionCheckIn && req.Kind != agentruntime.RequestLostExecutionRecovery {
		return agentruntime.Outcome{}, err
	}
	if req.Kind != agentruntime.RequestExecutionCheckIn && req.Kind != agentruntime.RequestLostExecutionRecovery {
		c.refreshUserShellContext(ctx, false)
	}

	c.mu.Lock()
	if h.agent == nil {
		c.mu.Unlock()
		return agentruntime.Outcome{}, errors.New("agent runtime is not configured")
	}
	session := c.session
	task := c.task
	if req.Kind == agentruntime.RequestApprovalRefinement && req.Approval != nil {
		approval := runtimeApprovalToController(*req.Approval)
		task.PendingApproval = &approval
	}
	if req.Kind == agentruntime.RequestExecutionCheckIn || req.Kind == agentruntime.RequestLostExecutionRecovery {
		if task.CurrentExecution != nil && strings.TrimSpace(task.CurrentExecution.LatestOutputTail) != "" {
			session.RecentShellOutput = task.CurrentExecution.LatestOutputTail
		}
	}
	task.RecoverySnapshot = c.captureRecoverySnapshot(ctx, executionTarget(task.CurrentExecution, session.TrackedShell).PaneID, task.CurrentExecution)
	c.mu.Unlock()

	response, err := h.agent.Respond(ctx, AgentInput{
		Session: session,
		Task:    task,
		Prompt:  req.Prompt,
	})
	if err != nil {
		return agentruntime.Outcome{}, err
	}
	return runtimeOutcomeFromController(response), nil
}

func (h builtinRuntimeHost) InspectContext(ctx context.Context, _ agentruntime.Request) error {
	c := h.controller
	summary, promptContext, err := c.inspectProposedContextSummary(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.task.LastCommandResult = &summary
	if promptContext != nil {
		contextCopy := *promptContext
		c.applyPromptContextLocked(&contextCopy)
	}
	c.mu.Unlock()
	return nil
}

func (h builtinRuntimeHost) SynthesizeStructuredEdit(ctx context.Context, outcome agentruntime.Outcome) (agentruntime.Outcome, error) {
	c := h.controller
	response, err := c.synthesizeStructuredEditResponse(ctx, controllerOutcomeToRuntimeInput(outcome))
	if err != nil {
		return agentruntime.Outcome{}, err
	}
	return runtimeOutcomeFromController(response), nil
}

func (h builtinRuntimeHost) ValidatePatch(ctx context.Context, patch string, target string) error {
	c := h.controller
	return c.validatePatchPayload(ctx, patch, PatchTarget(strings.TrimSpace(target)))
}

func runtimeOutcomeFromController(response AgentResponse) agentruntime.Outcome {
	outcome := agentruntime.Outcome{
		Message: response.Message,
	}
	if response.Plan != nil {
		outcome.Plan = &agentruntime.Plan{
			Summary: response.Plan.Summary,
			Steps:   append([]string(nil), response.Plan.Steps...),
		}
	}
	if response.Proposal != nil {
		proposal := agentruntime.Proposal{
			Kind:        response.Proposal.Kind,
			Command:     response.Proposal.Command,
			Keys:        response.Proposal.Keys,
			Patch:       response.Proposal.Patch,
			PatchTarget: response.Proposal.PatchTarget,
			Description: response.Proposal.Description,
		}
		if response.Proposal.Edit != nil {
			proposal.Edit = &agentruntime.EditIntent{
				Target:     response.Proposal.Edit.Target,
				Path:       response.Proposal.Edit.Path,
				Operation:  response.Proposal.Edit.Operation,
				AnchorText: response.Proposal.Edit.AnchorText,
				OldText:    response.Proposal.Edit.OldText,
				NewText:    response.Proposal.Edit.NewText,
				StartLine:  response.Proposal.Edit.StartLine,
				EndLine:    response.Proposal.Edit.EndLine,
			}
		}
		outcome.Proposal = &proposal
	}
	if response.Approval != nil {
		outcome.Approval = &agentruntime.ApprovalRequest{
			ID:          response.Approval.ID,
			Kind:        response.Approval.Kind,
			Title:       response.Approval.Title,
			Summary:     response.Approval.Summary,
			Command:     response.Approval.Command,
			Patch:       response.Approval.Patch,
			PatchTarget: response.Approval.PatchTarget,
			Risk:        response.Approval.Risk,
		}
	}
	if response.ModelInfo != nil {
		outcome.ModelInfo = &agentruntime.ModelInfo{
			ProviderPreset:  response.ModelInfo.ProviderPreset,
			RequestedModel:  response.ModelInfo.RequestedModel,
			ResponseModel:   response.ModelInfo.ResponseModel,
			ResponseBaseURL: response.ModelInfo.ResponseBaseURL,
		}
	}
	return outcome
}

func controllerOutcomeToRuntimeInput(outcome agentruntime.Outcome) AgentResponse {
	response := AgentResponse{
		Message: outcome.Message,
	}
	if outcome.Plan != nil {
		response.Plan = &Plan{
			Summary: outcome.Plan.Summary,
			Steps:   append([]string(nil), outcome.Plan.Steps...),
		}
	}
	if outcome.Proposal != nil {
		proposal := Proposal{
			Kind:        outcome.Proposal.Kind,
			Command:     outcome.Proposal.Command,
			Keys:        outcome.Proposal.Keys,
			Patch:       outcome.Proposal.Patch,
			PatchTarget: outcome.Proposal.PatchTarget,
			Description: outcome.Proposal.Description,
		}
		if outcome.Proposal.Edit != nil {
			proposal.Edit = &EditIntent{
				Target:     outcome.Proposal.Edit.Target,
				Path:       outcome.Proposal.Edit.Path,
				Operation:  outcome.Proposal.Edit.Operation,
				AnchorText: outcome.Proposal.Edit.AnchorText,
				OldText:    outcome.Proposal.Edit.OldText,
				NewText:    outcome.Proposal.Edit.NewText,
				StartLine:  outcome.Proposal.Edit.StartLine,
				EndLine:    outcome.Proposal.Edit.EndLine,
			}
		}
		response.Proposal = &proposal
	}
	if outcome.Approval != nil {
		approval := runtimeApprovalToController(*outcome.Approval)
		response.Approval = &approval
	}
	if outcome.ModelInfo != nil {
		response.ModelInfo = &AgentModelInfo{
			ProviderPreset:  outcome.ModelInfo.ProviderPreset,
			RequestedModel:  outcome.ModelInfo.RequestedModel,
			ResponseModel:   outcome.ModelInfo.ResponseModel,
			ResponseBaseURL: outcome.ModelInfo.ResponseBaseURL,
		}
	}
	return response
}

func runtimeApprovalToController(approval agentruntime.ApprovalRequest) ApprovalRequest {
	return ApprovalRequest{
		ID:          approval.ID,
		Kind:        approval.Kind,
		Title:       approval.Title,
		Summary:     approval.Summary,
		Command:     approval.Command,
		Patch:       approval.Patch,
		PatchTarget: approval.PatchTarget,
		Risk:        approval.Risk,
	}
}

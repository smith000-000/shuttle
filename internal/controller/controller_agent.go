package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"aiterm/internal/logging"
)

func (c *LocalController) SubmitAgentPrompt(ctx context.Context, prompt string) ([]TranscriptEvent, error) {
	logging.Trace("controller.submit_agent_prompt", "prompt", prompt)
	return c.submitAgentTurn(ctx, prompt, buildInitialAgentPrompt(prompt), nil, true)
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

func (c *LocalController) submitAgentTurn(ctx context.Context, userPrompt string, agentPrompt string, refinement *ApprovalRequest, emitUserMessage bool) ([]TranscriptEvent, error) {
	return c.submitAgentTurnWithInspectBudget(ctx, userPrompt, agentPrompt, refinement, emitUserMessage, 2)
}

func (c *LocalController) submitAgentTurnWithInspectBudget(ctx context.Context, userPrompt string, agentPrompt string, refinement *ApprovalRequest, emitUserMessage bool, inspectBudget int) ([]TranscriptEvent, error) {
	logging.Trace(
		"controller.agent_turn.start",
		"user_prompt", userPrompt,
		"agent_prompt_preview", logging.Preview(agentPrompt, 800),
		"emit_user_message", emitUserMessage,
		"has_refinement", refinement != nil,
	)
	c.refreshUserShellContext(ctx, true)

	events := make([]TranscriptEvent, 0, 4)

	c.mu.Lock()
	if emitUserMessage {
		events = append(events, c.newEvent(EventUserMessage, TextPayload{Text: userPrompt}))
	}
	if c.agent == nil {
		errEvent := c.newEvent(EventError, TextPayload{Text: "agent runtime is not configured"})
		c.appendEvents(events...)
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return append(append([]TranscriptEvent(nil), events...), errEvent), nil
	}

	session := c.session
	task := c.task
	task.RecoverySnapshot = c.captureRecoverySnapshot(ctx, executionTarget(task.CurrentExecution, session.TrackedShell).PaneID, task.CurrentExecution)
	c.mu.Unlock()

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
		c.mu.Lock()
		defer c.mu.Unlock()
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
	response, err = c.synthesizeStructuredEditResponse(ctx, response)
	if err != nil {
		logging.TraceError("controller.agent_turn.edit_synthesis_error", err)
		c.mu.Lock()
		defer c.mu.Unlock()
		if errors.Is(err, context.Canceled) {
			if emitUserMessage {
				c.appendEvents(events...)
			}
			return append([]TranscriptEvent(nil), events...), err
		}
		errEvent := c.newEvent(EventError, TextPayload{Text: err.Error()})
		c.appendEvents(events...)
		c.appendEvents(errEvent)
		return append(append([]TranscriptEvent(nil), events...), errEvent), nil
	}
	response, repaired, repairErr := c.repairInvalidPatchResponse(ctx, input, response)
	if repairErr != nil {
		logging.TraceError("controller.agent_turn.patch_repair_error", repairErr)
	}
	if repaired {
		logging.Trace("controller.agent_turn.patch_repaired")
	}
	if response.Proposal != nil && response.Proposal.Kind == ProposalInspectContext {
		if inspectBudget <= 0 {
			logging.Trace("controller.agent_turn.inspect_context.exhausted")
			response.Proposal = nil
			if strings.TrimSpace(response.Message) == "" {
				response.Message = "I could not stabilize shell context well enough to continue reliably."
			}
		} else {
			logging.Trace("controller.agent_turn.inspect_context.internal")
			summary, promptContext, inspectErr := c.inspectProposedContextSummary(ctx)
			if inspectErr != nil {
				c.mu.Lock()
				defer c.mu.Unlock()
				if errors.Is(inspectErr, context.Canceled) {
					if emitUserMessage {
						c.appendEvents(events...)
					}
					return append([]TranscriptEvent(nil), events...), inspectErr
				}
				errEvent := c.newEvent(EventError, TextPayload{Text: inspectErr.Error()})
				c.appendEvents(events...)
				c.appendEvents(errEvent)
				return append(append([]TranscriptEvent(nil), events...), errEvent), nil
			}
			c.mu.Lock()
			c.task.LastCommandResult = &summary
			if promptContext != nil {
				contextCopy := *promptContext
				c.applyPromptContextLocked(&contextCopy)
			}
			c.mu.Unlock()
			return c.submitAgentTurnWithInspectBudget(ctx, userPrompt, agentPrompt, refinement, emitUserMessage, inspectBudget-1)
		}
	}

	c.mu.Lock()
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
			PatchTarget: response.Proposal.PatchTarget,
			Description: response.Proposal.Description,
		}))
	}

	if response.Approval != nil {
		approvalCopy := *response.Approval
		c.task.PendingApproval = &approvalCopy
		newEvents = append(newEvents, c.newEvent(EventApproval, approvalCopy))
	}
	if autoAction.command != "" {
		notice := autoRunNotice(autoAction.command)
		if c.session.ApprovalMode == ApprovalModeDanger {
			notice = fmt.Sprintf("Auto-running agent command under /approvals dangerous: %s", strings.TrimSpace(autoAction.command))
		}
		newEvents = append(newEvents, c.newEvent(EventSystemNotice, TextPayload{Text: notice}))
	}
	if autoAction.patch != "" {
		newEvents = append(newEvents, c.newEvent(EventSystemNotice, TextPayload{Text: "Auto-applying agent patch under /approvals dangerous."}))
	}

	c.appendEvents(newEvents...)
	c.mu.Unlock()
	logging.Trace(
		"controller.agent_turn.complete",
		"event_kinds", eventKinds(newEvents),
		"message_preview", logging.Preview(response.Message, 600),
		"has_plan", response.Plan != nil,
		"has_proposal", response.Proposal != nil,
		"has_approval", response.Approval != nil,
	)
	if autoAction.patch != "" {
		patchEvents, patchErr := c.applyPatch(ctx, autoAction.patch, autoAction.patchTarget)
		newEvents = append(newEvents, patchEvents...)
		if patchErr != nil {
			return newEvents, patchErr
		}
	}
	if autoAction.command != "" {
		origin := CommandOriginAgentAuto
		if c.session.ApprovalMode == ApprovalModeDanger {
			origin = CommandOriginAgentAuto
		}
		commandEvents, commandErr := c.submitShellCommand(ctx, autoAction.command, origin)
		newEvents = append(newEvents, commandEvents...)
		if commandErr != nil {
			return newEvents, commandErr
		}
	}
	return newEvents, nil
}

func (c *LocalController) repairInvalidPatchResponse(ctx context.Context, input AgentInput, response AgentResponse) (AgentResponse, bool, error) {
	kind, target, patch, err := c.invalidPatchInResponse(ctx, response)
	if err == nil {
		return response, false, nil
	}

	repairPrompt := buildInvalidPatchRepairPrompt(kind, target, patch, err)
	repairInput := input
	repairInput.Prompt = repairPrompt

	repairedResponse, repairErr := c.agent.Respond(ctx, repairInput)
	if repairErr != nil {
		return response, false, repairErr
	}
	repairedResponse = normalizeAgentResponse(repairedResponse)
	if _, _, _, repairedErr := c.invalidPatchInResponse(ctx, repairedResponse); repairedErr != nil {
		if strings.TrimSpace(repairedResponse.Message) == "" {
			repairedResponse.Message = invalidPatchProposalNotice + " " + strings.TrimSpace(repairedErr.Error())
		} else {
			repairedResponse.Message = strings.TrimSpace(repairedResponse.Message) + "\n\n" + invalidPatchProposalNotice + " " + strings.TrimSpace(repairedErr.Error())
		}
		if repairedResponse.Proposal != nil && repairedResponse.Proposal.Kind == ProposalPatch {
			repairedResponse.Proposal = nil
		}
		if repairedResponse.Approval != nil && repairedResponse.Approval.Kind == ApprovalPatch {
			repairedResponse.Approval = nil
		}
		return repairedResponse, true, nil
	}
	return repairedResponse, true, nil
}

func (c *LocalController) invalidPatchInResponse(ctx context.Context, response AgentResponse) (string, PatchTarget, string, error) {
	if response.Proposal != nil && response.Proposal.Kind == ProposalPatch && strings.TrimSpace(response.Proposal.Patch) != "" {
		err := c.validatePatchPayload(ctx, response.Proposal.Patch, response.Proposal.PatchTarget)
		return "proposal", response.Proposal.PatchTarget, response.Proposal.Patch, err
	}
	if response.Approval != nil && response.Approval.Kind == ApprovalPatch && strings.TrimSpace(response.Approval.Patch) != "" {
		err := c.validatePatchPayload(ctx, response.Approval.Patch, response.Approval.PatchTarget)
		return "approval", response.Approval.PatchTarget, response.Approval.Patch, err
	}
	return "", "", "", nil
}

func (c *LocalController) validatePatchPayload(ctx context.Context, patch string, target PatchTarget) error {
	patch = strings.TrimSpace(patch)
	if patch == "" {
		return nil
	}
	if target == PatchTargetRemoteShell {
		_, err := parseRemotePatchFiles(patch)
		return err
	}
	c.mu.Lock()
	applier := c.patches
	patchInitErr := c.patchInitErr
	c.mu.Unlock()
	if patchInitErr != nil {
		return patchInitErr
	}
	if applier == nil {
		return nil
	}
	_, err := applier.Validate(ctx, patch)
	return err
}

func buildInvalidPatchRepairPrompt(kind string, target PatchTarget, patch string, err error) string {
	targetValue := string(target)
	if strings.TrimSpace(targetValue) == "" {
		targetValue = string(PatchTargetLocalWorkspace)
	}
	lines := []string{
		"The previous " + kind + " patch is invalid and was intercepted before it became actionable.",
		"Return a corrected JSON response that preserves the same user intent.",
		"Do not explain the patch. Do not apologize. Fix the patch payload only.",
		"Leave every unrelated field empty. Do not emit a plan. Do not switch to a command or keys proposal.",
		"If the invalid action was a proposal, return only a patch proposal. If it was an approval, return only a patch approval.",
		"If you still need a patch, treat proposal_patch or approval_patch as a strict tool payload.",
		"Requirements for a valid patch payload:",
		"- raw unified diff only",
		"- starts with the first diff header line, with no prose, no bullets, no code fence, no preamble",
		"- exact hunk headers and counts",
		"- complete diff body with no truncation",
		"- for insertion hunks, the new line count must equal the old line count plus the number of inserted lines",
		"- prefer one-file, minimum-context diffs when the edit is small",
		"- patch target must remain " + targetValue,
		"- do not switch to apply_patch, git apply, patch, or heredoc patch commands",
		"Valid insertion example:",
		"diff --git a/foo.txt b/foo.txt",
		"--- a/foo.txt",
		"+++ b/foo.txt",
		"@@ -2,2 +2,5 @@",
		" keep this line",
		"+new inserted line one",
		"+new inserted line two",
		"+new inserted line three",
		" keep this line after the insertion",
		"Invalid insertion example:",
		"@@ -2,2 +2,1 @@",
		" keep this line",
		"+new inserted line one",
		" keep this line after the insertion",
		"Invalid patch error: " + strings.TrimSpace(err.Error()),
	}
	if guidance := patchValidationGuidance(err); guidance != "" {
		lines = append(lines, "Patch-specific correction guidance: "+guidance)
	}
	lines = append(lines,
		"Invalid patch payload:",
		patch,
	)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func patchValidationGuidance(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(message, "fragment header miscounts lines"):
		return "Recompute the @@ -old_start,old_count +new_start,new_count @@ header so the old count matches the number of removed-plus-context lines and the new count matches the number of added-plus-context lines."
	case strings.Contains(message, "unexpected eof"):
		return "The diff body was truncated. Return the complete diff and ensure every hunk includes all required context, removal, and addition lines."
	case strings.Contains(message, "unsupported preamble before the first diff"):
		return "Remove every line before the first diff header. The patch must begin immediately with diff --git or --- / +++ lines."
	default:
		return ""
	}
}

func normalizeAgentResponse(response AgentResponse) AgentResponse {
	if response.Proposal != nil {
		proposal := normalizePatchToolProposal(*response.Proposal)
		if !isActionableProposal(proposal) {
			if strings.TrimSpace(response.Message) == "" && strings.TrimSpace(proposal.Description) != "" {
				response.Message = strings.TrimSpace(proposal.Description)
			}
			response.Proposal = nil
		} else {
			response.Proposal = &proposal
		}
	}

	if response.Approval != nil {
		approval := normalizePatchToolApproval(*response.Approval)
		response.Approval = &approval
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

func normalizePatchToolProposal(proposal Proposal) Proposal {
	if patch, ok := extractInlinePatchPayload(proposal.Command); ok {
		proposal.Kind = ProposalPatch
		proposal.Command = ""
		proposal.Patch = patch
		if strings.TrimSpace(proposal.Description) == "" {
			proposal.Description = "Apply the proposed workspace patch."
		}
	}
	return proposal
}

func normalizePatchToolApproval(approval ApprovalRequest) ApprovalRequest {
	if patch, ok := extractInlinePatchPayload(approval.Command); ok {
		approval.Kind = ApprovalPatch
		approval.Command = ""
		approval.Patch = patch
		if strings.TrimSpace(approval.Summary) == "" {
			approval.Summary = "Apply the proposed workspace patch."
		}
	}
	return approval
}

func extractInlinePatchPayload(command string) (string, bool) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return "", false
	}

	firstLine, remainder, ok := strings.Cut(trimmed, "\n")
	if !ok {
		return "", false
	}

	heredocIndex := strings.Index(firstLine, "<<")
	if heredocIndex < 0 {
		return "", false
	}

	if !isInlinePatchTool(strings.Fields(strings.TrimSpace(firstLine[:heredocIndex]))) {
		return "", false
	}

	delimiter := strings.TrimSpace(firstLine[heredocIndex+2:])
	delimiter = strings.TrimPrefix(delimiter, "-")
	delimiter = strings.Trim(strings.TrimSpace(delimiter), `'"`)
	if delimiter == "" {
		return "", false
	}

	lines := strings.Split(remainder, "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) != delimiter {
			continue
		}
		patch := strings.TrimSpace(strings.Join(lines[:index], "\n"))
		if patch == "" {
			return "", false
		}
		return patch, true
	}

	return "", false
}

func isInlinePatchTool(fields []string) bool {
	if len(fields) == 0 {
		return false
	}

	switch fields[0] {
	case "apply_patch":
		return true
	case "git":
		return len(fields) >= 2 && fields[1] == "apply"
	case "patch":
		return true
	default:
		return false
	}
}

func isActionableProposal(proposal Proposal) bool {
	return strings.TrimSpace(proposal.Command) != "" ||
		proposal.Keys != "" ||
		strings.TrimSpace(proposal.Patch) != "" ||
		proposal.Edit != nil ||
		proposal.Kind == ProposalInspectContext
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
		"checklist is complete",
		"checklist completed",
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

func buildAutoContinuePrompt(task TaskContext) string {
	prompt := autoContinuePrompt
	if shouldRequestChecklistContinuation(task) {
		prompt += " " + autoContinuePromptChecklistSuffix
	}
	if shouldForceNextActionAfterInspection(task) {
		prompt += " " + autoContinuePromptUnresolvedInspectionSuffix
	}
	if shouldForcePatchRebaseAfterInspection(task) {
		prompt += " " + autoContinuePromptPatchRebaseSuffix
	}
	if !shouldPreferSerialContinuation(task) {
		return prompt
	}
	return prompt + " " + autoContinuePromptSerialSuffix
}

func buildInitialAgentPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	if !shouldRequestChecklistForPrompt(prompt) {
		return prompt
	}
	return prompt + "\n\n" + initialChecklistPromptSuffix
}

func shouldPreferSerialContinuation(task TaskContext) bool {
	userPrompt := strings.ToLower(strings.TrimSpace(latestUserTranscriptMessage(task.PriorTranscript)))
	if userPrompt == "" {
		return false
	}

	return containsAnySubstring(
		userPrompt,
		"serial",
		"ordered shell work",
		"step by step",
		"one at a time",
		"one step at a time",
		"one command at a time",
		"don't lump",
		"dont lump",
		"do not lump",
		"then when you see",
		"after that",
	)
}

func shouldRequestChecklistContinuation(task TaskContext) bool {
	if task.ActivePlan != nil {
		return false
	}
	return shouldRequestChecklistForPrompt(latestUserTranscriptMessage(task.PriorTranscript))
}

func shouldRequestChecklistForPrompt(prompt string) bool {
	prompt = strings.ToLower(strings.TrimSpace(prompt))
	if prompt == "" {
		return false
	}
	if isExplicitPlanRequest(prompt) {
		return true
	}
	if containsAnySubstring(
		prompt,
		"step by step",
		"workflow",
		"ordered",
		"in order",
		"one step at a time",
		"one command at a time",
		"checklist",
	) {
		return true
	}

	sequenceSignals := 0
	for _, needle := range []string{
		" then ",
		" after ",
		" before ",
		" after that",
		"run it again",
		"change it back",
		"change back",
		"revert it",
		"switch it back",
		"show the results",
		"show results",
	} {
		if strings.Contains(prompt, needle) {
			sequenceSignals++
		}
	}
	return sequenceSignals >= 2
}

func shouldForceNextActionAfterInspection(task TaskContext) bool {
	result := task.LastCommandResult
	if result == nil || result.State != CommandExecutionCompleted {
		return false
	}
	if !isReadOnlyInspectionCommand(result.Command) {
		return false
	}
	return transcriptShowsRecentUnresolvedFailure(task.PriorTranscript)
}

func shouldForcePatchRebaseAfterInspection(task TaskContext) bool {
	result := task.LastCommandResult
	if result == nil || result.State != CommandExecutionCompleted {
		return false
	}
	if !isReadOnlyInspectionCommand(result.Command) {
		return false
	}
	lastPatch := task.LastPatchApplyResult
	if lastPatch == nil || lastPatch.Applied {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(lastPatch.Error)), "conflict: fragment line does not match src line")
}

func isReadOnlyInspectionCommand(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return false
	}

	switch fields[0] {
	case "cat", "sed", "grep", "rg", "find", "ls", "head", "tail", "nl":
		return true
	case "git":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "status", "diff", "show", "log", "grep":
			return true
		}
	}

	return false
}

func transcriptShowsRecentUnresolvedFailure(events []TranscriptEvent) bool {
	for index := len(events) - 1; index >= 0 && len(events)-index <= 8; index-- {
		if transcriptEventContainsFailure(events[index]) {
			return true
		}
	}
	return false
}

func transcriptEventContainsFailure(event TranscriptEvent) bool {
	var text string
	switch payload := event.Payload.(type) {
	case TextPayload:
		text = payload.Text
	case CommandResultSummary:
		text = payload.Summary
	case *CommandResultSummary:
		if payload != nil {
			text = payload.Summary
		}
	}

	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}

	return containsAnySubstring(
		text,
		"nameerror",
		"not defined",
		"undefined",
		"exception",
		"traceback",
		"failed",
		"still needs fixing",
		"still needs repair",
		"unresolved",
		"did not apply cleanly",
		"compile error",
		"test failed",
		"exit=1",
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

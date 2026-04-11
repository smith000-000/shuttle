package controller

import (
	"context"
	"path/filepath"
	"strings"

	"aiterm/internal/agentruntime"
	"aiterm/internal/logging"
	"aiterm/internal/shell"
)

func (c *LocalController) SubmitAgentPrompt(ctx context.Context, prompt string) ([]TranscriptEvent, error) {
	c.mu.Lock()
	if ShouldReplaceActivePlanForUserPrompt(c.task.ActivePlan, prompt) {
		c.task.ActivePlan = nil
	}
	c.mu.Unlock()
	logging.Trace("controller.submit_agent_prompt", "prompt", prompt)
	return c.submitAgentTurn(ctx, prompt, buildInitialAgentPrompt(prompt), nil, true)
}

func (c *LocalController) SubmitRefinement(ctx context.Context, approval ApprovalRequest, note string) ([]TranscriptEvent, error) {
	c.mu.Lock()
	if ShouldReplaceActivePlanForRefinement(c.task.ActivePlan, note) {
		c.task.ActivePlan = nil
	}
	c.mu.Unlock()
	logging.Trace("controller.submit_refinement", "approval_id", approval.ID, "note", note)
	return c.submitAgentTurn(ctx, note, note, &approval, true)
}

func (c *LocalController) SubmitProposalRefinement(ctx context.Context, proposal ProposalPayload, note string) ([]TranscriptEvent, error) {
	c.mu.Lock()
	if ShouldReplaceActivePlanForRefinement(c.task.ActivePlan, note) {
		c.task.ActivePlan = nil
	}
	c.mu.Unlock()
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
	c.mu.Unlock()

	prompt := buildAutoContinuePrompt(c.task)
	logging.Trace("controller.continue_after_command")
	return c.submitAgentTurn(ctx, "", prompt, nil, false)
}

func (c *LocalController) submitAgentTurn(ctx context.Context, userPrompt string, agentPrompt string, refinement *ApprovalRequest, emitUserMessage bool) ([]TranscriptEvent, error) {
	return c.submitAgentTurnWithInspectBudget(ctx, userPrompt, agentPrompt, refinement, emitUserMessage, 2)
}

func (c *LocalController) submitAgentTurnWithInspectBudget(ctx context.Context, userPrompt string, agentPrompt string, refinement *ApprovalRequest, emitUserMessage bool, inspectBudget int) ([]TranscriptEvent, error) {
	if _, err := c.RefreshShellContext(ctx); err == nil {
		c.refreshLocalHostContext()
		c.refreshUserShellContext(ctx, false)
	}
	c.mu.Lock()
	task := c.task
	session := c.session
	c.mu.Unlock()
	agentPrompt = decorateAgentPrompt(session, task, agentPrompt)
	logging.Trace(
		"controller.agent_turn.start",
		"user_prompt", userPrompt,
		"agent_prompt_preview", logging.Preview(agentPrompt, 800),
		"emit_user_message", emitUserMessage,
		"has_refinement", refinement != nil,
	)
	req := agentruntime.Request{
		Kind:          agentruntime.RequestUserTurn,
		Prompt:        agentPrompt,
		UserPrompt:    userPrompt,
		InspectBudget: inspectBudget,
	}
	if refinement != nil {
		req.Kind = agentruntime.RequestApprovalRefinement
		req.Approval = &agentruntime.ApprovalRequest{
			ID:          refinement.ID,
			Kind:        refinement.Kind,
			Title:       refinement.Title,
			Summary:     refinement.Summary,
			Command:     refinement.Command,
			Patch:       refinement.Patch,
			PatchTarget: refinement.PatchTarget,
			Risk:        refinement.Risk,
		}
	}
	newEvents, err := c.handleRuntimeRequest(ctx, req, emitUserMessage)
	if err != nil {
		logging.TraceError("controller.agent_turn.error", err, "user_prompt", userPrompt, "agent_prompt_preview", logging.Preview(agentPrompt, 800))
		return newEvents, err
	}
	logging.Trace("controller.agent_turn.complete", "event_kinds", eventKinds(newEvents))
	return newEvents, nil
}

func decorateAgentPrompt(session SessionContext, task TaskContext, prompt string) string {
	prompt = strings.TrimSpace(prompt)
	prompt = appendPromptSuffix(prompt, stateAuthorityPromptSuffix)
	if shouldAddRerunOrContextShiftGuidance(session, task, prompt) {
		prompt = appendPromptSuffix(prompt, rerunOrContextShiftPromptSuffix)
	}
	if task.ActivePlan == nil {
		return prompt
	}
	if strings.Contains(prompt, activePlanStatusCheckPromptSuffix) {
		return prompt
	}
	if prompt == "" {
		return activePlanStatusCheckPromptSuffix
	}
	return prompt + "\n\n" + activePlanStatusCheckPromptSuffix
}

func appendPromptSuffix(prompt string, suffix string) string {
	prompt = strings.TrimSpace(prompt)
	suffix = strings.TrimSpace(suffix)
	if suffix == "" || strings.Contains(prompt, suffix) {
		return prompt
	}
	if prompt == "" {
		return suffix
	}
	return prompt + "\n\n" + suffix
}

func shouldAddRerunOrContextShiftGuidance(session SessionContext, task TaskContext, prompt string) bool {
	return promptExplicitlyRequestsRerun(prompt) || shellContextMateriallyChangedSinceLastResult(session, task.LastCommandResult)
}

func promptExplicitlyRequestsRerun(prompt string) bool {
	prompt = strings.ToLower(strings.TrimSpace(prompt))
	if prompt == "" {
		return false
	}

	return containsAnySubstring(
		prompt,
		"rerun",
		"re-run",
		"run again",
		"try again",
		"retry",
		"repeat",
		"do it again",
		"check again",
		"recheck",
		"re-check",
		"test again",
		"rerun the test",
		"run the test again",
	)
}

func shellContextMateriallyChangedSinceLastResult(session SessionContext, lastResult *CommandResultSummary) bool {
	if lastResult == nil || lastResult.ShellContext == nil {
		return false
	}
	currentPrompt := session.CurrentShell
	if currentPrompt == nil {
		return false
	}

	currentLocation := effectiveShellLocation(session.CurrentShellLocation, currentPrompt)
	previousLocation := shell.InferShellLocation(*lastResult.ShellContext, "")

	if currentLocation.Kind != previousLocation.Kind {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(currentLocation.User), strings.TrimSpace(previousLocation.User)) {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(currentLocation.Host), strings.TrimSpace(previousLocation.Host)) {
		return true
	}

	currentDirectory := normalizeComparableShellDirectory(currentPrompt.Directory, currentLocation)
	previousDirectory := normalizeComparableShellDirectory(lastResult.ShellContext.Directory, previousLocation)
	if currentDirectory != previousDirectory {
		return true
	}

	return false
}

func normalizeComparableShellDirectory(directory string, location shell.ShellLocation) string {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return ""
	}
	if location.Kind == shell.ShellLocationRemote {
		if directory == "~" {
			return "~"
		}
		if strings.HasPrefix(directory, "~/") {
			return "~/" + strings.TrimPrefix(filepath.Clean(strings.TrimPrefix(directory, "~/")), "./")
		}
		return filepath.Clean(directory)
	}
	return normalizeWorkingDirectory(directory)
}

func ShouldReplaceActivePlanForUserPrompt(activePlan *ActivePlan, prompt string) bool {
	if activePlan == nil {
		return false
	}

	prompt = strings.ToLower(strings.TrimSpace(prompt))
	if prompt == "" {
		return false
	}

	if activePlanResetRequested(prompt) {
		return true
	}

	for _, marker := range []string{
		"continue",
		"resume",
		"keep going",
		"go on",
		"what next",
		"what's next",
		"whats next",
		"next step",
		"next steps",
		"current plan",
		"active plan",
		"current checklist",
		"active checklist",
	} {
		if strings.Contains(prompt, marker) {
			return false
		}
	}

	return true
}

func ShouldReplaceActivePlanForRefinement(activePlan *ActivePlan, note string) bool {
	if activePlan == nil {
		return false
	}

	return activePlanResetRequested(note)
}

func activePlanResetRequested(prompt string) bool {
	prompt = strings.ToLower(strings.TrimSpace(prompt))
	if prompt == "" {
		return false
	}

	if containsAnySubstring(
		prompt,
		"abandon the plan",
		"drop the plan",
		"old plan",
		"wrong plan",
		"stuck in the old plan",
		"stop following",
		"don't follow",
		"do not follow",
		"replan",
		"re-plan",
		"switch plans",
		"change the plan",
		"different plan",
	) {
		return true
	}

	doNotRun := containsAnySubstring(
		prompt,
		"don't run",
		"do not run",
		"not run here",
		"won't run",
		"will not run",
		"defer",
		"deferred",
	)
	externalRun := containsAnySubstring(
		prompt,
		"another shell",
		"another tmux shell",
		"different shell",
		"outside shuttle",
		"outside this shell",
		"i will run",
		"i'll run",
		"you can stop here",
		"manually",
	)
	return doNotRun && externalRun
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
	lines = append(lines, "Invalid patch payload:", patch)
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
	if !messageIndicatesPlanCompletion(response.Message) && !shouldTreatContinuationMessageAsFinalPlanStep(response.Message, *activePlan) {
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
		"task is completed",
		"all requested work is complete",
		"all requested work is completed",
		"everything is complete",
		"everything is done",
		"that completes the task",
		"that completes the workflow",
	)
}

func shouldTreatContinuationMessageAsFinalPlanStep(message string, plan ActivePlan) bool {
	if strings.TrimSpace(message) == "" {
		return false
	}

	remaining := remainingPlanSteps(plan)
	if len(remaining) != 1 {
		return false
	}

	return isInformationalPlanStep(remaining[0].Text)
}

func remainingPlanSteps(plan ActivePlan) []PlanStep {
	remaining := make([]PlanStep, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		if step.Status != PlanStepDone {
			remaining = append(remaining, step)
		}
	}
	return remaining
}

func isInformationalPlanStep(step string) bool {
	step = strings.ToLower(strings.TrimSpace(step))
	if step == "" {
		return false
	}

	return containsAnySubstring(
		step,
		"report",
		"summarize",
		"summarise",
		"tell the user",
		"share the result",
		"share results",
		"present the result",
		"present results",
		"confirm completion",
		"wrap up",
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
	if shouldRequestChecklistForInvestigativePrompt(prompt) {
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
	if result == nil {
		return false
	}
	if result.State != CommandExecutionCompleted && result.State != CommandExecutionFailed {
		return false
	}
	if !isReadOnlyInspectionCommand(result.Command) {
		return false
	}
	if transcriptShowsRecentUnresolvedFailure(task.PriorTranscript) {
		return true
	}
	return shouldContinueUnresolvedReadOnlyTask(task)
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

func shouldRequestChecklistForInvestigativePrompt(prompt string) bool {
	if containsAnySubstring(prompt,
		"figure out",
		"find out",
		"determine",
		"track down",
		"investigate",
		"diagnose",
		"debug",
	) {
		return true
	}

	if containsAnySubstring(prompt,
		"how was",
		"where was",
		"why is",
		"why was",
		"which process",
		"which service",
		"what process",
		"what service",
		"what file",
		"what config",
		"what configuration",
		"what env",
		"what environment variable",
	) {
		return true
	}

	return containsAnySubstring(prompt,
		"installed",
		"installation",
		"configured",
		"configuration",
		"running",
		"started",
		"listening",
		"using this port",
		"binding this port",
		"loading this",
		"setting this",
		"coming from",
	)
}

func shouldContinueUnresolvedReadOnlyTask(task TaskContext) bool {
	if task.ActivePlan != nil {
		return false
	}
	userPrompt := strings.ToLower(strings.TrimSpace(latestUserTranscriptMessage(task.PriorTranscript)))
	if !shouldRequestChecklistForInvestigativePrompt(userPrompt) {
		return false
	}
	result := task.LastCommandResult
	if result == nil {
		return false
	}
	if result.State == CommandExecutionFailed {
		return true
	}
	if result.State != CommandExecutionCompleted {
		return false
	}
	return commandResultLooksInconclusive(result)
}

func controllerCommandResultSummary(result *CommandResultSummary) string {
	if result == nil {
		return ""
	}
	if strings.TrimSpace(result.DisplaySummary) != "" {
		return result.DisplaySummary
	}
	return result.Summary
}

func commandResultLooksInconclusive(result *CommandResultSummary) bool {
	if result == nil {
		return false
	}
	summary := strings.ToLower(strings.TrimSpace(controllerCommandResultSummary(result)))
	if summary == "" {
		return true
	}
	return containsAnySubstring(summary,
		"not found",
		"no such file",
		"not installed",
		"unable to locate",
		"no packages found",
		"no matches found",
		"no results",
		"not running",
		"inactive",
		"unknown",
		"permission denied",
	)
}

func isReadOnlyInspectionCommand(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return false
	}

	switch fields[0] {
	case "cat", "sed", "grep", "rg", "find", "ls", "head", "tail", "nl", "which", "whereis", "ss", "ps", "lsof", "env", "printenv":
		return true
	case "command":
		return len(fields) >= 2 && fields[1] == "-v"
	case "dpkg":
		return len(fields) >= 2 && (fields[1] == "-l" || fields[1] == "-s")
	case "rpm":
		return len(fields) >= 2 && fields[1] == "-q"
	case "flatpak", "snap", "brew":
		return true
	case "pacman":
		return len(fields) >= 2 && strings.HasPrefix(fields[1], "-Q")
	case "systemctl":
		return len(fields) >= 2 && (fields[1] == "status" || fields[1] == "show" || fields[1] == "cat")
	case "launchctl":
		return len(fields) >= 2 && (fields[1] == "list" || fields[1] == "print")
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

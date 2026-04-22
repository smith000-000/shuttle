package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type RequestKind string

const (
	RequestUserTurn               RequestKind = "user_turn"
	RequestApprovalRefinement     RequestKind = "approval_refinement"
	RequestProposalRefinement     RequestKind = "proposal_refinement"
	RequestContinuePlan           RequestKind = "continue_plan"
	RequestContinueAfterCommand   RequestKind = "continue_after_command"
	RequestContinueAfterPatch     RequestKind = "continue_after_patch_apply"
	RequestCompactTask            RequestKind = "compact_task"
	RequestExecutionCheckIn       RequestKind = "execution_checkin"
	RequestLostExecutionRecovery  RequestKind = "lost_execution_recovery"
	RequestResolveApproval        RequestKind = "resolve_approval"
	RequestResumeAfterTakeControl RequestKind = "resume_after_take_control"
)

type Request struct {
	Kind             RequestKind
	Prompt           string
	UserPrompt       string
	SessionName      string
	TaskID           string
	InspectBudget    int
	Proposal         *Proposal
	Approval         *ApprovalRequest
	ApprovalDecision ApprovalDecision
	ApprovalNote     string
}

type Outcome struct {
	Message          string
	Plan             *Plan
	PlanStatuses     []PlanStepStatus
	Proposal         *Proposal
	Approval         *ApprovalRequest
	ModelInfo        *ModelInfo
	NativeCompaction bool
}

type Host interface {
	Respond(ctx context.Context, req Request) (Outcome, error)
	InspectContext(ctx context.Context, req Request) error
	SynthesizeStructuredEdit(ctx context.Context, outcome Outcome) (Outcome, error)
	ValidatePatch(ctx context.Context, patch string, target string) error
}

type Runtime interface {
	Handle(ctx context.Context, host Host, req Request) (Outcome, error)
}

type BuiltinRuntime struct{}

const (
	RuntimeAuthorityAuthoritative = "authoritative"
	RuntimeAuthorityBuiltin       = "builtin"
)

func NewBuiltin() Runtime {
	return BuiltinRuntime{}
}

func (BuiltinRuntime) Handle(ctx context.Context, host Host, req Request) (Outcome, error) {
	return handleRuntimeTurn(ctx, host, req, host.Respond)
}

type runtimeTurnResponder func(context.Context, Request) (Outcome, error)

func handleRuntimeTurn(ctx context.Context, host Host, req Request, respond runtimeTurnResponder) (Outcome, error) {
	if host == nil {
		return Outcome{}, errors.New("runtime host is required")
	}
	if respond == nil {
		return Outcome{}, errors.New("runtime responder is required")
	}
	if req.InspectBudget <= 0 {
		req.InspectBudget = 2
	}

	for {
		outcome, err := respond(ctx, req)
		if err != nil {
			return Outcome{}, err
		}

		outcome = normalizeOutcome(outcome)
		outcome, err = host.SynthesizeStructuredEdit(ctx, outcome)
		if err != nil {
			return Outcome{}, err
		}
		outcome, repaired, err := repairInvalidPatch(ctx, host, req, outcome, respond)
		if err != nil {
			return Outcome{}, err
		}
		if repaired {
			outcome = normalizeOutcome(outcome)
		}

		if outcome.Proposal != nil && outcome.Proposal.Kind == ProposalInspectContext {
			if req.InspectBudget <= 0 {
				outcome.Proposal = nil
				if strings.TrimSpace(outcome.Message) == "" {
					outcome.Message = "I could not stabilize shell context well enough to continue reliably."
				}
				return outcome, nil
			}
			if err := host.InspectContext(ctx, req); err != nil {
				return Outcome{}, err
			}
			req.InspectBudget--
			continue
		}

		return outcome, nil
	}
}

func repairInvalidPatch(ctx context.Context, host Host, req Request, outcome Outcome, respond runtimeTurnResponder) (Outcome, bool, error) {
	kind, target, patch, err := invalidPatchInOutcome(ctx, host, outcome)
	if err == nil {
		return outcome, false, nil
	}

	repairReq := req
	repairReq.Prompt = buildInvalidPatchRepairPrompt(kind, target, patch, err)
	repaired, repairErr := respond(ctx, repairReq)
	if repairErr != nil {
		return Outcome{}, false, repairErr
	}
	repaired = normalizeOutcome(repaired)
	if _, _, _, repairedErr := invalidPatchInOutcome(ctx, host, repaired); repairedErr != nil {
		if strings.TrimSpace(repaired.Message) == "" {
			repaired.Message = invalidPatchProposalNotice + " " + strings.TrimSpace(repairedErr.Error())
		} else {
			repaired.Message = strings.TrimSpace(repaired.Message) + "\n\n" + invalidPatchProposalNotice + " " + strings.TrimSpace(repairedErr.Error())
		}
		if repaired.Proposal != nil && repaired.Proposal.Kind == ProposalPatch {
			repaired.Proposal = nil
		}
		if repaired.Approval != nil && repaired.Approval.Kind == ApprovalPatch {
			repaired.Approval = nil
		}
		return repaired, true, nil
	}
	return repaired, true, nil
}

func invalidPatchInOutcome(ctx context.Context, host Host, outcome Outcome) (string, string, string, error) {
	if outcome.Proposal != nil && outcome.Proposal.Kind == ProposalPatch && strings.TrimSpace(outcome.Proposal.Patch) != "" {
		err := host.ValidatePatch(ctx, outcome.Proposal.Patch, string(outcome.Proposal.PatchTarget))
		return "proposal", string(outcome.Proposal.PatchTarget), outcome.Proposal.Patch, err
	}
	if outcome.Approval != nil && outcome.Approval.Kind == ApprovalPatch && strings.TrimSpace(outcome.Approval.Patch) != "" {
		err := host.ValidatePatch(ctx, outcome.Approval.Patch, string(outcome.Approval.PatchTarget))
		return "approval", string(outcome.Approval.PatchTarget), outcome.Approval.Patch, err
	}
	return "", "", "", nil
}

const invalidPatchProposalNotice = "Generated patch was invalid and was intercepted before it became an actionable proposal."

func buildInvalidPatchRepairPrompt(kind string, target string, patch string, err error) string {
	targetValue := strings.TrimSpace(target)
	if targetValue == "" {
		targetValue = "local_workspace"
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

func normalizeOutcome(outcome Outcome) Outcome {
	if outcome.Proposal != nil {
		proposal := normalizePatchToolProposal(*outcome.Proposal)
		if !isActionableProposal(proposal) {
			if strings.TrimSpace(outcome.Message) == "" && strings.TrimSpace(proposal.Description) != "" {
				outcome.Message = strings.TrimSpace(proposal.Description)
			}
			outcome.Proposal = nil
		} else {
			outcome.Proposal = &proposal
		}
	}

	if outcome.Approval != nil {
		approval := normalizePatchToolApproval(*outcome.Approval)
		outcome.Approval = &approval
	}

	if outcome.Approval == nil || outcome.Proposal == nil {
		return outcome
	}

	approval := *outcome.Approval
	proposal := *outcome.Proposal
	if approval.Kind == ApprovalCommand && strings.TrimSpace(approval.Command) == "" && proposal.Kind == ProposalCommand && strings.TrimSpace(proposal.Command) != "" {
		approval.Command = strings.TrimSpace(proposal.Command)
	}
	if approval.Kind == ApprovalPatch && strings.TrimSpace(approval.Patch) == "" && proposal.Kind == ProposalPatch && strings.TrimSpace(proposal.Patch) != "" {
		approval.Patch = strings.TrimSpace(proposal.Patch)
	}
	outcome.Approval = &approval
	return outcome
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

type RuntimeMetadata struct {
	Type           string
	Command        string
	ProviderPreset string
	Model          string
	StateDir       string
}

type CodexSDKTurnHandler interface {
	Respond(context.Context, Host, Request) (Outcome, error)
}

type codexSDKRuntime struct {
	meta    RuntimeMetadata
	handler CodexSDKTurnHandler
}

type hostBackedCodexSDKHandler struct{}

type unsupportedRuntime struct {
	meta   RuntimeMetadata
	reason string
}

func NewSelectedRuntime(meta RuntimeMetadata) Runtime {
	meta.Type = normalizeRuntimeSelection(meta.Type)
	if meta.Type == "" {
		meta.Type = RuntimeBuiltin
	}
	switch meta.Type {
	case RuntimeBuiltin:
		return NewBuiltin()
	case RuntimePi:
		return unsupportedRuntime{
			meta:   meta,
			reason: "runtime \"pi\" is not yet supported as an authoritative Shuttle runtime",
		}
	case RuntimeCodexAppServer:
		return NewCodexAppServerRuntime(meta, nil)
	case RuntimeCodexSDK:
		return NewCodexSDKRuntime(meta, nil)
	default:
		return unsupportedRuntime{
			meta:   meta,
			reason: fmt.Sprintf("runtime %q is not supported", meta.Type),
		}
	}
}

func NewCodexSDKRuntime(meta RuntimeMetadata, handler CodexSDKTurnHandler) Runtime {
	meta.Type = normalizeRuntimeSelection(meta.Type)
	if meta.Type == "" {
		meta.Type = RuntimeCodexSDK
	}
	return codexSDKRuntime{meta: meta, handler: handler}
}

func (r codexSDKRuntime) Handle(ctx context.Context, host Host, req Request) (Outcome, error) {
	handler := r.handler
	if handler == nil {
		handler = hostBackedCodexSDKHandler{}
	}
	outcome, err := handleRuntimeTurn(ctx, host, req, func(ctx context.Context, req Request) (Outcome, error) {
		return handler.Respond(ctx, host, req)
	})
	if err != nil {
		return Outcome{}, err
	}
	return annotateRuntimeMetadata(outcome, r.meta), nil
}

func (hostBackedCodexSDKHandler) Respond(ctx context.Context, host Host, req Request) (Outcome, error) {
	if host == nil {
		return Outcome{}, errors.New("runtime host is required")
	}
	req.Prompt = buildCodexSDKPrompt(req)
	return host.Respond(ctx, req)
}

func buildNativeDelegatedRuntimePrompt(req Request) string {
	sections := []string{
		"Shuttle delegated runtime turn",
		"You are the authoritative runtime for this task. Execute tools directly when appropriate instead of emitting Shuttle proposals.",
		"Keep command execution, file changes, and context management inside the runtime thread. Only surface a final user-facing result, a plan update, a runtime-native approval request, or a genuine blocked/help-needed state back to Shuttle.",
		"If the controller instructions mention a proposal or say to propose the next action, interpret that as taking the next action directly unless approval is required or the task is complete.",
	}
	if guidance := strings.TrimSpace(nativeDelegatedKindGuidance(req.Kind)); guidance != "" {
		sections = append(sections, "Turn guidance:\n"+guidance)
	}

	contextLines := []string{"request_kind: " + string(normalizeCodexSDKRequestKind(req.Kind))}
	if userPrompt := strings.TrimSpace(req.UserPrompt); userPrompt != "" {
		contextLines = append(contextLines, "user_prompt: "+userPrompt)
	}
	if req.Proposal != nil {
		contextLines = append(contextLines, formatCodexSDKProposalContext(*req.Proposal)...)
	}
	if req.Approval != nil {
		contextLines = append(contextLines, formatCodexSDKApprovalContext(*req.Approval)...)
	}
	sections = append(sections, "Turn context:\n"+strings.Join(contextLines, "\n"))

	if prompt := strings.TrimSpace(req.Prompt); prompt != "" {
		sections = append(sections, "Controller instructions:\n"+prompt)
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func buildCodexSDKPrompt(req Request) string {
	sections := []string{
		"Shuttle Codex runtime turn",
		"Respond with the next Shuttle structured outcome for this turn only.",
		"Do not describe runtime metadata or mention fallback behavior in the user-facing message.",
	}
	if guidance := strings.TrimSpace(codexSDKKindGuidance(req.Kind)); guidance != "" {
		sections = append(sections, "Turn guidance:\n"+guidance)
	}

	contextLines := []string{"request_kind: " + string(normalizeCodexSDKRequestKind(req.Kind))}
	if userPrompt := strings.TrimSpace(req.UserPrompt); userPrompt != "" {
		contextLines = append(contextLines, "user_prompt: "+userPrompt)
	}
	if req.Proposal != nil {
		contextLines = append(contextLines, formatCodexSDKProposalContext(*req.Proposal)...)
	}
	if req.Approval != nil {
		contextLines = append(contextLines, formatCodexSDKApprovalContext(*req.Approval)...)
	}
	sections = append(sections, "Turn context:\n"+strings.Join(contextLines, "\n"))

	if prompt := strings.TrimSpace(req.Prompt); prompt != "" {
		sections = append(sections, "Controller instructions:\n"+prompt)
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func normalizeCodexSDKRequestKind(kind RequestKind) RequestKind {
	if strings.TrimSpace(string(kind)) == "" {
		return RequestUserTurn
	}
	return kind
}

func codexSDKKindGuidance(kind RequestKind) string {
	switch normalizeCodexSDKRequestKind(kind) {
	case RequestUserTurn:
		return "Handle the user turn directly. Use plans only when they materially help, and otherwise respond or propose the next action."
	case RequestApprovalRefinement:
		return "Revise the pending approval in light of the user note. Keep the same underlying intent unless the note explicitly changes it."
	case RequestProposalRefinement:
		return "Refine the pending proposal using the proposal context and user note. Preserve the original task unless the note clearly redirects it."
	case RequestContinuePlan:
		return "Continue the active plan from its current state. Prefer updating plan step statuses and the next concrete action over restating the whole plan."
	case RequestContinueAfterCommand:
		return "Interpret the latest command result and decide the next agent action. Avoid rerunning commands unless the result or context justifies it."
	case RequestContinueAfterPatch:
		return "Interpret the patch application result and continue the task from the updated workspace state."
	case RequestCompactTask:
		return "Produce a compact but sufficient task continuation summary for later recovery."
	case RequestExecutionCheckIn:
		return "Interpret the active execution check-in and decide whether to wait, report status, or intervene."
	case RequestLostExecutionRecovery:
		return "Recover from lost execution tracking using the latest execution snapshot and shell context."
	case RequestResolveApproval:
		return "Resolve the pending runtime-owned approval using the supplied approval context and user decision, then continue the suspended turn."
	case RequestResumeAfterTakeControl:
		return "Resume the task after the operator took control. Reconcile the current shell state before choosing the next action."
	default:
		return "Respond with the best Shuttle structured outcome for the provided turn context."
	}
}

func nativeDelegatedKindGuidance(kind RequestKind) string {
	switch normalizeCodexSDKRequestKind(kind) {
	case RequestUserTurn:
		return "Handle the user turn directly inside the runtime. Use tools when needed, update the plan only when it materially helps, and stop once the user's request is satisfied."
	case RequestApprovalRefinement:
		return "Revise the pending approval in light of the user note, then continue the task inside the runtime thread."
	case RequestProposalRefinement:
		return "Refine the pending proposal intent, but execute the revised work directly inside the runtime thread instead of returning a Shuttle proposal."
	case RequestContinuePlan:
		return "Continue the active plan from its current state. Update plan step statuses if helpful, then take the next action directly unless approval is required."
	case RequestContinueAfterCommand:
		return "Interpret the latest command result and continue the task directly. Avoid rerunning commands unless the result or context justifies it."
	case RequestContinueAfterPatch:
		return "Interpret the patch application result and continue from the updated workspace state."
	case RequestCompactTask:
		return "Compact the task context using the runtime's own context-management mechanism."
	case RequestExecutionCheckIn:
		return "Interpret the active execution check-in and either keep waiting, explain the current blocked state, or request help if the user needs to intervene."
	case RequestLostExecutionRecovery:
		return "Recover from lost execution tracking using the supplied execution snapshot and shell context, and explain the best next step."
	case RequestResolveApproval:
		return "Resolve the pending runtime-owned approval using the supplied approval context and user decision, then continue the suspended turn."
	case RequestResumeAfterTakeControl:
		return "Resume the task after the operator took control. Reconcile the current shell state before choosing the next action."
	default:
		return "Handle the turn directly inside the runtime and surface only the resulting message, plan, approval request, or blocked/help-needed state."
	}
}

func formatCodexSDKProposalContext(proposal Proposal) []string {
	lines := []string{"proposal.kind: " + strings.TrimSpace(string(proposal.Kind))}
	if command := strings.TrimSpace(proposal.Command); command != "" {
		lines = append(lines, "proposal.command: "+command)
	}
	if keys := strings.TrimSpace(string(proposal.Keys)); keys != "" {
		lines = append(lines, "proposal.keys: "+keys)
	}
	if target := strings.TrimSpace(string(proposal.PatchTarget)); target != "" {
		lines = append(lines, "proposal.patch_target: "+target)
	}
	if patch := strings.TrimSpace(proposal.Patch); patch != "" {
		lines = append(lines, "proposal.patch_present: true", "proposal.patch_preview: "+previewCodexSDKBlock(patch, 240))
	}
	if description := strings.TrimSpace(proposal.Description); description != "" {
		lines = append(lines, "proposal.description: "+description)
	}
	if proposal.Edit != nil {
		lines = append(lines, formatCodexSDKEditContext("proposal.edit", *proposal.Edit)...)
	}
	return lines
}

func formatCodexSDKApprovalContext(approval ApprovalRequest) []string {
	lines := []string{"approval.kind: " + strings.TrimSpace(string(approval.Kind))}
	if id := strings.TrimSpace(approval.ID); id != "" {
		lines = append(lines, "approval.id: "+id)
	}
	if title := strings.TrimSpace(approval.Title); title != "" {
		lines = append(lines, "approval.title: "+title)
	}
	if summary := strings.TrimSpace(approval.Summary); summary != "" {
		lines = append(lines, "approval.summary: "+summary)
	}
	if command := strings.TrimSpace(approval.Command); command != "" {
		lines = append(lines, "approval.command: "+command)
	}
	if target := strings.TrimSpace(string(approval.PatchTarget)); target != "" {
		lines = append(lines, "approval.patch_target: "+target)
	}
	if patch := strings.TrimSpace(approval.Patch); patch != "" {
		lines = append(lines, "approval.patch_present: true", "approval.patch_preview: "+previewCodexSDKBlock(patch, 240))
	}
	if risk := strings.TrimSpace(string(approval.Risk)); risk != "" {
		lines = append(lines, "approval.risk: "+risk)
	}
	return lines
}

func formatCodexSDKEditContext(prefix string, edit EditIntent) []string {
	lines := []string{}
	if target := strings.TrimSpace(string(edit.Target)); target != "" {
		lines = append(lines, prefix+".target: "+target)
	}
	if path := strings.TrimSpace(edit.Path); path != "" {
		lines = append(lines, prefix+".path: "+path)
	}
	if operation := strings.TrimSpace(string(edit.Operation)); operation != "" {
		lines = append(lines, prefix+".operation: "+operation)
	}
	if anchor := strings.TrimSpace(edit.AnchorText); anchor != "" {
		lines = append(lines, prefix+".anchor_text: "+previewCodexSDKBlock(anchor, 120))
	}
	if oldText := strings.TrimSpace(edit.OldText); oldText != "" {
		lines = append(lines, prefix+".old_text: "+previewCodexSDKBlock(oldText, 120))
	}
	if newText := strings.TrimSpace(edit.NewText); newText != "" {
		lines = append(lines, prefix+".new_text: "+previewCodexSDKBlock(newText, 120))
	}
	if edit.StartLine > 0 {
		lines = append(lines, fmt.Sprintf("%s.start_line: %d", prefix, edit.StartLine))
	}
	if edit.EndLine > 0 {
		lines = append(lines, fmt.Sprintf("%s.end_line: %d", prefix, edit.EndLine))
	}
	return lines
}

func previewCodexSDKBlock(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return strings.TrimSpace(value[:limit-3]) + "..."
}

func annotateRuntimeMetadata(outcome Outcome, meta RuntimeMetadata) Outcome {
	if outcome.ModelInfo == nil {
		outcome.ModelInfo = &ModelInfo{}
	}
	if strings.TrimSpace(outcome.ModelInfo.ProviderPreset) == "" {
		outcome.ModelInfo.ProviderPreset = strings.TrimSpace(meta.ProviderPreset)
	}
	if strings.TrimSpace(outcome.ModelInfo.RequestedModel) == "" {
		outcome.ModelInfo.RequestedModel = strings.TrimSpace(meta.Model)
	}
	outcome.ModelInfo.SelectedRuntime = strings.TrimSpace(meta.Type)
	outcome.ModelInfo.EffectiveRuntime = strings.TrimSpace(meta.Type)
	outcome.ModelInfo.RuntimeCommand = strings.TrimSpace(meta.Command)
	outcome.ModelInfo.RuntimeAuthority = RuntimeAuthorityAuthoritative
	return outcome
}

func (r unsupportedRuntime) Handle(context.Context, Host, Request) (Outcome, error) {
	return Outcome{}, errors.New(strings.TrimSpace(r.reason))
}

func normalizeRuntimeSelection(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", RuntimeBuiltin:
		return RuntimeBuiltin
	case RuntimeAuto:
		return RuntimeAuto
	case "pi-runtime":
		return RuntimePi
	case "codex-sdk":
		return RuntimeCodexSDK
	case "codex-app-server", "codex-appserver":
		return RuntimeCodexAppServer
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

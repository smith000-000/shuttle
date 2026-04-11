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
	RequestResumeAfterTakeControl RequestKind = "resume_after_take_control"
)

type Request struct {
	Kind          RequestKind
	Prompt        string
	UserPrompt    string
	InspectBudget int
	Proposal      *Proposal
	Approval      *ApprovalRequest
}

type Outcome struct {
	Message      string
	Plan         *Plan
	PlanStatuses []PlanStepStatus
	Proposal     *Proposal
	Approval     *ApprovalRequest
	ModelInfo    *ModelInfo
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

func NewBuiltin() Runtime {
	return BuiltinRuntime{}
}

func (BuiltinRuntime) Handle(ctx context.Context, host Host, req Request) (Outcome, error) {
	if host == nil {
		return Outcome{}, errors.New("runtime host is required")
	}
	if req.InspectBudget <= 0 {
		req.InspectBudget = 2
	}

	for {
		outcome, err := host.Respond(ctx, req)
		if err != nil {
			return Outcome{}, err
		}

		outcome = normalizeOutcome(outcome)
		outcome, err = host.SynthesizeStructuredEdit(ctx, outcome)
		if err != nil {
			return Outcome{}, err
		}
		outcome, repaired, err := repairInvalidPatch(ctx, host, req, outcome)
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

func repairInvalidPatch(ctx context.Context, host Host, req Request, outcome Outcome) (Outcome, bool, error) {
	kind, target, patch, err := invalidPatchInOutcome(ctx, host, outcome)
	if err == nil {
		return outcome, false, nil
	}

	repairReq := req
	repairReq.Prompt = buildInvalidPatchRepairPrompt(kind, target, patch, err)
	repaired, repairErr := host.Respond(ctx, repairReq)
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
}

type metadataRuntime struct {
	delegate Runtime
	meta     RuntimeMetadata
}

type piRuntime struct {
	metadataRuntime
}

type codexSDKRuntime struct {
	metadataRuntime
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
		return piRuntime{metadataRuntime{delegate: NewBuiltin(), meta: meta}}
	case RuntimeCodexSDK:
		return codexSDKRuntime{metadataRuntime{delegate: NewBuiltin(), meta: meta}}
	default:
		return metadataRuntime{delegate: NewBuiltin(), meta: meta}
	}
}

func WrapRuntime(delegate Runtime, meta RuntimeMetadata) Runtime {
	meta.Type = normalizeRuntimeSelection(meta.Type)
	if meta.Type == "" || meta.Type == RuntimeBuiltin {
		return delegate
	}
	switch meta.Type {
	case RuntimePi:
		return piRuntime{metadataRuntime{delegate: delegate, meta: meta}}
	case RuntimeCodexSDK:
		return codexSDKRuntime{metadataRuntime{delegate: delegate, meta: meta}}
	default:
		return metadataRuntime{delegate: delegate, meta: meta}
	}
}

func (m metadataRuntime) Handle(ctx context.Context, host Host, req Request) (Outcome, error) {
	outcome, err := m.delegate.Handle(ctx, host, req)
	if err != nil {
		return Outcome{}, err
	}
	prefix := fmt.Sprintf("[runtime=%s", m.meta.Type)
	if strings.TrimSpace(m.meta.Command) != "" {
		prefix += fmt.Sprintf(" command=%q", m.meta.Command)
	}
	if strings.TrimSpace(m.meta.ProviderPreset) != "" {
		prefix += fmt.Sprintf(" provider=%s", m.meta.ProviderPreset)
	}
	if strings.TrimSpace(m.meta.Model) != "" {
		prefix += fmt.Sprintf(" model=%s", m.meta.Model)
	}
	prefix += "] "
	outcome.Message = strings.TrimSpace(prefix + outcome.Message)
	if outcome.ModelInfo == nil {
		outcome.ModelInfo = &ModelInfo{}
	}
	if strings.TrimSpace(outcome.ModelInfo.ProviderPreset) == "" {
		outcome.ModelInfo.ProviderPreset = strings.TrimSpace(m.meta.ProviderPreset)
	}
	if strings.TrimSpace(outcome.ModelInfo.RequestedModel) == "" {
		outcome.ModelInfo.RequestedModel = strings.TrimSpace(m.meta.Model)
	}
	return outcome, nil
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
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

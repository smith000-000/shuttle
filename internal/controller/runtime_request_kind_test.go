package controller

import (
	"aiterm/internal/agentruntime"
	"context"
	"testing"
)

func TestSubmitProposalRefinementUsesProposalRefinementRequestKind(t *testing.T) {
	runtime := &captureRuntime{}
	controller := New(&stubAgent{}, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	controller.SetRuntime(runtime)

	_, err := controller.SubmitProposalRefinement(context.Background(), ProposalPayload{
		Kind:        ProposalCommand,
		Command:     "sleep 5",
		Description: "Run a short sleep.",
	}, "Make it one second.")
	if err != nil {
		t.Fatalf("SubmitProposalRefinement() error = %v", err)
	}
	if len(runtime.requests) != 1 {
		t.Fatalf("expected one runtime request, got %d", len(runtime.requests))
	}
	if runtime.requests[0].Kind != agentruntime.RequestProposalRefinement {
		t.Fatalf("expected proposal refinement request kind, got %#v", runtime.requests[0])
	}
	if runtime.requests[0].Proposal == nil || runtime.requests[0].Proposal.Command != "sleep 5" {
		t.Fatalf("expected proposal context in runtime request, got %#v", runtime.requests[0].Proposal)
	}
}

func TestContinueActivePlanUsesContinuePlanRequestKind(t *testing.T) {
	runtime := &captureRuntime{}
	controller := New(&stubAgent{}, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	controller.SetRuntime(runtime)
	controller.task.ActivePlan = &ActivePlan{
		Summary: "Inspect and repair the workspace.",
		Steps:   []PlanStep{{Text: "Review the current files.", Status: PlanStepInProgress}},
	}

	_, err := controller.ContinueActivePlan(context.Background())
	if err != nil {
		t.Fatalf("ContinueActivePlan() error = %v", err)
	}
	if len(runtime.requests) != 1 || runtime.requests[0].Kind != agentruntime.RequestContinuePlan {
		t.Fatalf("expected continue-plan request kind, got %#v", runtime.requests)
	}
}

func TestContinueAfterPatchApplyUsesContinueAfterPatchRequestKind(t *testing.T) {
	runtime := &captureRuntime{}
	controller := New(&stubAgent{}, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	controller.SetRuntime(runtime)
	controller.task.LastPatchApplyResult = &PatchApplySummary{Applied: true, Target: PatchTargetLocalWorkspace}

	_, err := controller.ContinueAfterPatchApply(context.Background())
	if err != nil {
		t.Fatalf("ContinueAfterPatchApply() error = %v", err)
	}
	if len(runtime.requests) != 1 || runtime.requests[0].Kind != agentruntime.RequestContinueAfterPatch {
		t.Fatalf("expected continue-after-patch request kind, got %#v", runtime.requests)
	}
}

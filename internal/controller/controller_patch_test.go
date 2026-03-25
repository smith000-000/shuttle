package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	"aiterm/internal/patchapply"
)

func TestLocalControllerApplyProposedPatch(t *testing.T) {
	applier := &stubPatchApplier{
		result: patchapply.Result{
			WorkspaceRoot: "/repo",
			Validation:    "native",
			Updated:       1,
			Files: []patchapply.FileChange{
				{Operation: patchapply.OperationUpdate, NewPath: "README.md"},
			},
		},
	}
	controller := New(nil, nil, nil, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		LocalWorkspaceRoot: "/repo",
	})
	controller.patches = applier
	controller.patchInitErr = nil

	events, err := controller.ApplyProposedPatch(context.Background(), diffFixture("README.md", "before\n", "after\n"))
	if err != nil {
		t.Fatalf("ApplyProposedPatch() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventPatchApplyResult {
		t.Fatalf("expected patch apply result event, got %#v", events)
	}

	payload, ok := events[0].Payload.(PatchApplySummary)
	if !ok {
		t.Fatalf("expected patch apply payload, got %#v", events[0].Payload)
	}
	if !payload.Applied || payload.Updated != 1 || len(payload.Files) != 1 {
		t.Fatalf("unexpected patch apply payload %#v", payload)
	}
	if controller.task.LastPatchApplyResult == nil || !controller.task.LastPatchApplyResult.Applied {
		t.Fatalf("expected controller task to store patch result, got %#v", controller.task.LastPatchApplyResult)
	}
}

func TestLocalControllerApplyProposedPatchFailureEmitsResultAndError(t *testing.T) {
	applier := &stubPatchApplier{err: errors.New("conflict: README.md")}
	controller := New(nil, nil, nil, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		LocalWorkspaceRoot: "/repo",
	})
	controller.patches = applier
	controller.patchInitErr = nil

	events, err := controller.ApplyProposedPatch(context.Background(), diffFixture("README.md", "before\n", "after\n"))
	if err != nil {
		t.Fatalf("ApplyProposedPatch() error = %v", err)
	}
	if len(events) != 2 || events[0].Kind != EventPatchApplyResult || events[1].Kind != EventError {
		t.Fatalf("expected patch result + error, got %#v", events)
	}

	payload, _ := events[0].Payload.(PatchApplySummary)
	if payload.Applied || payload.Error == "" {
		t.Fatalf("expected failed patch result, got %#v", payload)
	}
}

func TestLocalControllerApprovePatchRunsPatchPath(t *testing.T) {
	applier := &stubPatchApplier{
		result: patchapply.Result{
			WorkspaceRoot: "/repo",
			Validation:    "native",
			Created:       1,
			Files: []patchapply.FileChange{
				{Operation: patchapply.OperationCreate, NewPath: "new.txt"},
			},
		},
	}
	controller := New(nil, nil, nil, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		LocalWorkspaceRoot: "/repo",
	})
	controller.patches = applier
	controller.patchInitErr = nil
	controller.task.PendingApproval = &ApprovalRequest{
		ID:    "approval-1",
		Kind:  ApprovalPatch,
		Title: "Apply patch",
		Patch: diffNewFileFixture("new.txt", "hello\n"),
	}

	events, err := controller.DecideApproval(context.Background(), "approval-1", DecisionApprove, "")
	if err != nil {
		t.Fatalf("DecideApproval() error = %v", err)
	}
	if len(applier.patches) != 1 {
		t.Fatalf("expected patch applier call, got %#v", applier.patches)
	}
	if len(events) != 1 || events[0].Kind != EventPatchApplyResult {
		t.Fatalf("expected patch apply result, got %#v", events)
	}
}

func TestLocalControllerContinueAfterPatchApply(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Patch applied. Next I can run tests.",
		},
	}
	controller := New(agent, nil, nil, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		LocalWorkspaceRoot: "/repo",
	})
	controller.task.LastPatchApplyResult = &PatchApplySummary{
		WorkspaceRoot: "/repo",
		Applied:       true,
		Updated:       1,
	}

	events, err := controller.ContinueAfterPatchApply(context.Background())
	if err != nil {
		t.Fatalf("ContinueAfterPatchApply() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventAgentMessage {
		t.Fatalf("expected agent message, got %#v", events)
	}
	if agent.lastInput.Prompt != continueAfterPatchApplyPrompt {
		t.Fatalf("expected patch continuation prompt, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, "Do not propose extra verification or follow-up edits") {
		t.Fatalf("expected stop-biased patch continuation prompt, got %q", agent.lastInput.Prompt)
	}
}

func TestLocalControllerContinueAfterFailedPatchApply(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "The patch failed to parse. I can retry with one corrected unified diff.",
		},
	}
	controller := New(agent, nil, nil, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		LocalWorkspaceRoot: "/repo",
	})
	controller.task.ActivePlan = &ActivePlan{
		Summary: "Repair hello.py.",
		Steps: []PlanStep{
			{Text: "Apply a patch.", Status: PlanStepInProgress},
			{Text: "Run the script.", Status: PlanStepPending},
		},
	}
	controller.task.LastPatchApplyResult = &PatchApplySummary{
		WorkspaceRoot: "/repo",
		Applied:       false,
		Error:         "parse patch: gitdiff: invalid line operation",
	}

	events, err := controller.ContinueAfterPatchApply(context.Background())
	if err != nil {
		t.Fatalf("ContinueAfterPatchApply() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventAgentMessage {
		t.Fatalf("expected agent message, got %#v", events)
	}
	if agent.lastInput.Prompt != continueAfterPatchFailurePrompt {
		t.Fatalf("expected failed patch continuation prompt, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, "Do not emit a shell command that invokes apply_patch") {
		t.Fatalf("expected shell patch tool prohibition, got %q", agent.lastInput.Prompt)
	}
	if controller.task.ActivePlan == nil || controller.task.ActivePlan.Steps[0].Status != PlanStepInProgress {
		t.Fatalf("expected active plan to remain unchanged after failed patch, got %#v", controller.task.ActivePlan)
	}
}

func diffFixture(path string, oldBody string, newBody string) string {
	return "--- a/" + path + "\n" +
		"+++ b/" + path + "\n" +
		"@@ -1 +1 @@\n" +
		"-" + trimTrailingNewline(oldBody) + "\n" +
		"+" + trimTrailingNewline(newBody) + "\n"
}

func diffNewFileFixture(path string, body string) string {
	return "diff --git a/" + path + " b/" + path + "\n" +
		"new file mode 100644\n" +
		"--- /dev/null\n" +
		"+++ b/" + path + "\n" +
		"@@ -0,0 +1 @@\n" +
		"+" + trimTrailingNewline(body) + "\n"
}

func trimTrailingNewline(value string) string {
	for len(value) > 0 && value[len(value)-1] == '\n' {
		value = value[:len(value)-1]
	}
	return value
}

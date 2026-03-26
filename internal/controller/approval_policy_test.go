package controller

import (
	"context"
	"strings"
	"testing"

	"aiterm/internal/shell"
)

func TestSetApprovalModeStoresSessionMode(t *testing.T) {
	controller := New(nil, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SetApprovalMode(context.Background(), ApprovalModeAuto)
	if err != nil {
		t.Fatalf("SetApprovalMode() error = %v", err)
	}
	if got := controller.ApprovalMode(); got != ApprovalModeAuto {
		t.Fatalf("expected auto approval mode, got %q", got)
	}
	if len(events) != 1 || events[0].Kind != EventSystemNotice {
		t.Fatalf("expected system notice event, got %#v", events)
	}
	payload, _ := events[0].Payload.(TextPayload)
	if !strings.Contains(payload.Text, "Approvals set to auto") {
		t.Fatalf("expected auto-mode notice, got %q", payload.Text)
	}
}

func TestCommandQualifiesForAutoRun(t *testing.T) {
	local := SessionContext{}
	remote := SessionContext{
		CurrentShell: &shell.PromptContext{Remote: true},
	}

	tests := []struct {
		name    string
		session SessionContext
		command string
		want    bool
	}{
		{name: "ls", session: local, command: "ls -lah", want: true},
		{name: "git status", session: local, command: "git status --short", want: true},
		{name: "go test", session: local, command: "go test ./...", want: true},
		{name: "npm test", session: local, command: "npm test -- --runInBand", want: true},
		{name: "remote shell", session: remote, command: "ls -lah", want: false},
		{name: "pipe rejected", session: local, command: "ls -lah | head", want: false},
		{name: "env assignment rejected", session: local, command: "FOO=bar go test ./...", want: false},
		{name: "absolute path rejected", session: local, command: "cat /etc/passwd", want: false},
		{name: "parent traversal rejected", session: local, command: "grep TODO ../README.md", want: false},
		{name: "sed in-place rejected", session: local, command: "sed -i 's/a/b/' README.md", want: false},
		{name: "find delete rejected", session: local, command: "find . -delete", want: false},
		{name: "go test compile binary rejected", session: local, command: "go test -c ./...", want: false},
		{name: "git mutation rejected", session: local, command: "git checkout -b tmp", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := commandQualifiesForAutoRun(test.session, test.command); got != test.want {
				t.Fatalf("commandQualifiesForAutoRun(%q) = %t, want %t", test.command, got, test.want)
			}
		})
	}
}

func TestLocalControllerSubmitAgentPromptAutoRunsSafeProposalCommand(t *testing.T) {
	runner := &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-safe",
			Command:   "ls -lah",
			ExitCode:  0,
			Captured:  "ok",
		},
	}
	agent := &stubAgent{
		response: AgentResponse{
			Message: "I can inspect the current directory.",
			Proposal: &Proposal{
				Kind:        ProposalCommand,
				Command:     "ls -lah",
				Description: "Inspect the current directory.",
			},
		},
	}
	controller := New(agent, runner, &stubContextReader{}, SessionContext{
		TrackedShell: TrackedShellTarget{PaneID: "%0"},
		ApprovalMode: ApprovalModeAuto,
	})

	events, err := controller.SubmitAgentPrompt(context.Background(), "list files")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	if got := len(runner.commands); got != 1 || runner.commands[0] != "ls -lah" {
		t.Fatalf("expected auto-run shell command, got %#v", runner.commands)
	}
	if agent.lastInput.Session.ApprovalMode != ApprovalModeAuto {
		t.Fatalf("expected approval mode in agent input, got %q", agent.lastInput.Session.ApprovalMode)
	}
	for _, event := range events {
		if event.Kind == EventProposal {
			t.Fatalf("expected proposal card to be suppressed, got %#v", events)
		}
	}
	if events[2].Kind != EventSystemNotice {
		t.Fatalf("expected auto-run notice before command events, got %#v", events)
	}
	notice, _ := events[2].Payload.(TextPayload)
	if !strings.Contains(notice.Text, "Auto-running safe local command") {
		t.Fatalf("expected auto-run notice, got %#v", events[2].Payload)
	}
	result, _ := events[len(events)-1].Payload.(CommandResultSummary)
	if result.Origin != CommandOriginAgentAuto {
		t.Fatalf("expected auto-run origin, got %#v", result)
	}
}

func TestLocalControllerSubmitAgentPromptAutoRunsSafeApprovalCommand(t *testing.T) {
	runner := &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-safe-approval",
			Command:   "go test ./...",
			ExitCode:  0,
			Captured:  "ok",
		},
	}
	agent := &stubAgent{
		response: AgentResponse{
			Message: "I can run the targeted tests.",
			Approval: &ApprovalRequest{
				ID:      "approval-1",
				Kind:    ApprovalCommand,
				Title:   "Run tests",
				Summary: "Run the targeted test suite.",
				Command: "go test ./...",
				Risk:    RiskLow,
			},
		},
	}
	controller := New(agent, runner, &stubContextReader{}, SessionContext{
		TrackedShell: TrackedShellTarget{PaneID: "%0"},
		ApprovalMode: ApprovalModeAuto,
	})

	events, err := controller.SubmitAgentPrompt(context.Background(), "run the tests")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	if got := len(runner.commands); got != 1 || runner.commands[0] != "go test ./..." {
		t.Fatalf("expected safe approval command to auto-run, got %#v", runner.commands)
	}
	if controller.task.PendingApproval != nil {
		t.Fatalf("expected pending approval to remain clear, got %#v", controller.task.PendingApproval)
	}
	for _, event := range events {
		if event.Kind == EventApproval {
			t.Fatalf("expected approval card to be suppressed, got %#v", events)
		}
	}
}

func TestLocalControllerSubmitAgentPromptKeepsUnsafeApprovalInAutoMode(t *testing.T) {
	runner := &stubRunner{}
	agent := &stubAgent{
		response: AgentResponse{
			Message: "This action requires approval.",
			Approval: &ApprovalRequest{
				ID:      "approval-1",
				Kind:    ApprovalCommand,
				Title:   "Delete files",
				Summary: "Delete tmp recursively.",
				Command: "rm -rf tmp",
				Risk:    RiskHigh,
			},
		},
	}
	controller := New(agent, runner, &stubContextReader{}, SessionContext{
		TrackedShell: TrackedShellTarget{PaneID: "%0"},
		ApprovalMode: ApprovalModeAuto,
	})

	events, err := controller.SubmitAgentPrompt(context.Background(), "delete tmp")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	if len(runner.commands) != 0 {
		t.Fatalf("expected unsafe command to remain gated, got %#v", runner.commands)
	}
	if controller.task.PendingApproval == nil || controller.task.PendingApproval.Command != "rm -rf tmp" {
		t.Fatalf("expected pending approval to remain, got %#v", controller.task.PendingApproval)
	}
	if events[len(events)-1].Kind != EventApproval {
		t.Fatalf("expected approval event, got %#v", events)
	}
}

func TestLocalControllerSubmitAgentPromptDangerousAutoRunsUnsafeCommand(t *testing.T) {
	runner := &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-danger",
			Command:   "rm -rf tmp",
			ExitCode:  0,
			Captured:  "done",
		},
	}
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Removing the temporary directory.",
			Approval: &ApprovalRequest{
				ID:      "approval-1",
				Kind:    ApprovalCommand,
				Title:   "Remove tmp",
				Summary: "Delete tmp recursively.",
				Command: "rm -rf tmp",
				Risk:    RiskHigh,
			},
		},
	}
	controller := New(agent, runner, &stubContextReader{}, SessionContext{
		TrackedShell: TrackedShellTarget{PaneID: "%0"},
		ApprovalMode: ApprovalModeDanger,
	})

	events, err := controller.SubmitAgentPrompt(context.Background(), "remove tmp")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	if got := len(runner.commands); got != 1 || runner.commands[0] != "rm -rf tmp" {
		t.Fatalf("expected dangerous mode to auto-run command, got %#v", runner.commands)
	}
	for _, event := range events {
		if event.Kind == EventApproval {
			t.Fatalf("expected dangerous mode to suppress approval card, got %#v", events)
		}
	}
	if controller.task.PendingApproval != nil {
		t.Fatalf("expected pending approval to remain clear, got %#v", controller.task.PendingApproval)
	}
}

func TestLocalControllerSubmitAgentPromptDangerousAutoAppliesPatch(t *testing.T) {
	applier := &stubPatchApplier{}
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Applying the patch directly.",
			Proposal: &Proposal{
				Kind:        ProposalPatch,
				Patch:       "diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-old\n+new\n",
				Description: "Update the readme.",
			},
		},
	}
	controller := New(agent, nil, &stubContextReader{}, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		LocalWorkspaceRoot: "/tmp/work",
		ApprovalMode:       ApprovalModeDanger,
	})
	controller.patches = applier
	controller.patchInitErr = nil

	events, err := controller.SubmitAgentPrompt(context.Background(), "apply the patch")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	if got := len(applier.patches); got != 1 {
		t.Fatalf("expected dangerous mode to auto-apply patch, got %#v", applier.patches)
	}
	for _, event := range events {
		if event.Kind == EventProposal {
			t.Fatalf("expected dangerous mode to suppress patch proposal card, got %#v", events)
		}
	}
	lastResult := controller.task.LastPatchApplyResult
	if lastResult == nil || !lastResult.Applied {
		t.Fatalf("expected stored patch apply result, got %#v", lastResult)
	}
}

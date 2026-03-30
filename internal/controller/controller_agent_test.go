package controller

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"aiterm/internal/shell"
)

type sequenceAgent struct {
	responses  []AgentResponse
	lastInputs []AgentInput
}

func (s *sequenceAgent) Respond(_ context.Context, input AgentInput) (AgentResponse, error) {
	s.lastInputs = append(s.lastInputs, input)
	if len(s.responses) == 0 {
		return AgentResponse{}, nil
	}
	response := s.responses[0]
	s.responses = s.responses[1:]
	return response, nil
}

func TestLocalControllerSubmitAgentPrompt(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "hello",
			Proposal: &Proposal{
				Kind:    ProposalCommand,
				Command: "ls -lah",
			},
		},
	}
	controller := New(agent, nil, &stubContextReader{
		output: "recent shell output",
	}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitAgentPrompt(context.Background(), "list files")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	if events[0].Kind != EventUserMessage || events[1].Kind != EventAgentMessage || events[2].Kind != EventProposal {
		t.Fatalf("unexpected event sequence: %#v", events)
	}

	if agent.lastInput.Session.RecentShellOutput != "recent shell output" {
		t.Fatalf("expected recent shell output in agent input, got %q", agent.lastInput.Session.RecentShellOutput)
	}
}

func TestLocalControllerSubmitAgentPromptIncludesRecentManualShellContext(t *testing.T) {
	historyFile := t.TempDir() + "/shell_history"
	if err := os.WriteFile(historyFile, []byte(strings.Join([]string{
		": 1710000000:0;ls",
		"mv foo.md foo_new.md",
		"touch chicken.mmd",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	agent := &stubAgent{
		response: AgentResponse{
			Message: "ready",
		},
	}
	controller := New(agent, nil, &stubContextReader{
		output: "recent shell output",
		context: shell.PromptContext{
			User:         "localuser",
			Host:         "workstation",
			Directory:    "/workspace/project",
			PromptSymbol: "%",
			RawLine:      "localuser@workstation ~/workspace/project %",
		},
	}, SessionContext{
		UserShellHistoryFile: historyFile,
	})

	if _, err := controller.SubmitAgentPrompt(context.Background(), "what changed?"); err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	if got := strings.Join(agent.lastInput.Session.RecentManualCommands, "\n"); !strings.Contains(got, "mv foo.md foo_new.md") || !strings.Contains(got, "touch chicken.mmd") {
		t.Fatalf("expected recent manual commands in agent input, got %#v", agent.lastInput.Session.RecentManualCommands)
	}
	if got := strings.Join(agent.lastInput.Session.RecentManualActions, "\n"); !strings.Contains(got, "renamed foo.md -> foo_new.md") || !strings.Contains(got, "touched chicken.mmd") {
		t.Fatalf("expected recent manual actions in agent input, got %#v", agent.lastInput.Session.RecentManualActions)
	}
}

func TestLocalControllerSubmitAgentPromptCreatesActivePlan(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Plan: &Plan{
				Summary: "Inspect and repair the workspace.",
				Steps: []string{
					"Review the current files.",
					"Apply the next patch.",
					"Run tests.",
				},
			},
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitAgentPrompt(context.Background(), "make a plan")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	planEvent, ok := events[1].Payload.(PlanPayload)
	if !ok {
		t.Fatalf("expected plan payload, got %#v", events[1].Payload)
	}

	if len(planEvent.Steps) != 3 || planEvent.Steps[0].Status != PlanStepInProgress || planEvent.Steps[1].Status != PlanStepPending {
		t.Fatalf("unexpected active plan state: %#v", planEvent.Steps)
	}
}

func TestLocalControllerSubmitAgentPromptAddsChecklistGuidanceForOrderedWorkflow(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "I can start with the first step.",
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	prompt := "update hello.py to change the sort type from whatever is in there now into a different sort algorithm. Show the results. Then change it back to the original sort and run it again."
	if _, err := controller.SubmitAgentPrompt(context.Background(), prompt); err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	if !strings.Contains(agent.lastInput.Prompt, initialChecklistPromptSuffix) {
		t.Fatalf("expected ordered-workflow checklist guidance, got %q", agent.lastInput.Prompt)
	}
}

func TestLocalControllerSubmitAgentPromptIgnoresAnswerProposal(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "The selection is complete.",
			Proposal: &Proposal{
				Kind:        ProposalAnswer,
				Description: "No further action is needed.",
			},
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitAgentPrompt(context.Background(), "continue")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected user and agent events only, got %#v", events)
	}
	if events[0].Kind != EventUserMessage || events[1].Kind != EventAgentMessage {
		t.Fatalf("unexpected event sequence: %#v", events)
	}
}

func TestLocalControllerInspectProposedContextReturnsAuthoritativeShellState(t *testing.T) {
	controller := New(nil, nil, &stubContextReader{
		context: shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "/home/openclaw",
			System:       "Linux",
			GitBranch:    "main",
			PromptSymbol: "$",
			RawLine:      "openclaw@openclaw ~ $",
			Remote:       true,
		},
	}, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		WorkingDirectory:   "/home/openclaw",
		LocalWorkspaceRoot: "/workspace/project",
		RemoteCapabilities: &RemoteCapabilitySummary{
			Identity:                "openclaw@openclaw",
			LastSuccessfulTransport: PatchTransportPython,
			Git:                     true,
			Python3:                 true,
			Base64:                  true,
			Mktemp:                  true,
		},
	})

	events, err := controller.InspectProposedContext(context.Background())
	if err != nil {
		t.Fatalf("InspectProposedContext() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one command result event, got %#v", events)
	}
	if events[0].Kind != EventCommandResult {
		t.Fatalf("expected command result, got %#v", events[0])
	}
	result, ok := events[0].Payload.(CommandResultSummary)
	if !ok {
		t.Fatalf("expected command result payload, got %#v", events[0].Payload)
	}
	if result.Command != inspectContextCommandLabel {
		t.Fatalf("unexpected inspect command label %q", result.Command)
	}
	if !strings.Contains(result.Summary, "user_host=openclaw@openclaw") ||
		!strings.Contains(result.Summary, "shell_target=openclaw@openclaw") ||
		!strings.Contains(result.Summary, "cwd=/home/openclaw") ||
		!strings.Contains(result.Summary, "remote=true") ||
		!strings.Contains(result.Summary, "system=Linux") ||
		!strings.Contains(result.Summary, "git_branch=main") ||
		!strings.Contains(result.Summary, "local_workspace_root=/workspace/project") ||
		!strings.Contains(result.Summary, "workspace_relation=outside_local_workspace") ||
		!strings.Contains(result.Summary, "remote_patch_root=/home/openclaw") ||
		!strings.Contains(result.Summary, "remote_capabilities=git,python3,base64,mktemp") ||
		!strings.Contains(result.Summary, "remote_last_patch_transport=python3") ||
		!strings.Contains(result.Summary, "remote_identity=openclaw@openclaw") {
		t.Fatalf("unexpected shell identity summary %q", result.Summary)
	}
	if controller.task.LastCommandResult == nil || controller.task.LastCommandResult.Command != inspectContextCommandLabel {
		t.Fatalf("expected task last command result to be updated, got %#v", controller.task.LastCommandResult)
	}
}

func TestSubmitAgentPromptHandlesInspectContextInternally(t *testing.T) {
	agent := &sequenceAgent{
		responses: []AgentResponse{
			{
				Message: "I should refresh shell context first.",
				Proposal: &Proposal{
					Kind:        ProposalInspectContext,
					Description: "Refresh the active shell identity and current working directory.",
				},
			},
			{
				Message: "I can inspect the file now.",
				Proposal: &Proposal{
					Kind:        ProposalCommand,
					Command:     "sed -n '1,40p' foo.txt",
					Description: "Read the file before editing it.",
				},
			},
		},
	}
	controller := New(agent, nil, &stubContextReader{
		context: shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "/home/openclaw",
			PromptSymbol: "$",
			RawLine:      "openclaw@openclaw ~ $",
			Remote:       true,
		},
	}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitAgentPrompt(context.Background(), "show me foo.txt")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}
	if len(agent.lastInputs) != 2 {
		t.Fatalf("expected two agent turns, got %d", len(agent.lastInputs))
	}
	if got := agent.lastInputs[1].Task.LastCommandResult; got == nil || got.Command != inspectContextCommandLabel || !strings.Contains(got.Summary, "cwd=/home/openclaw") {
		t.Fatalf("expected inspected shell context in second turn, got %#v", agent.lastInputs[1].Task.LastCommandResult)
	}
	for _, event := range events {
		if event.Kind == EventProposal {
			payload, _ := event.Payload.(ProposalPayload)
			if payload.Kind == ProposalInspectContext {
				t.Fatalf("did not expect visible inspect_context proposal in events: %#v", events)
			}
		}
		if event.Kind == EventCommandResult {
			payload, _ := event.Payload.(CommandResultSummary)
			if payload.Command == inspectContextCommandLabel {
				t.Fatalf("did not expect visible inspect_context command result in events: %#v", events)
			}
		}
	}
	last := events[len(events)-1]
	if last.Kind != EventProposal {
		t.Fatalf("expected final visible proposal, got %#v", events)
	}
	payload, _ := last.Payload.(ProposalPayload)
	if payload.Kind != ProposalCommand || payload.Command != "sed -n '1,40p' foo.txt" {
		t.Fatalf("unexpected final proposal %#v", payload)
	}
}

func TestNormalizeAgentResponseConvertsInlinePatchToolProposalToPatch(t *testing.T) {
	response := normalizeAgentResponse(AgentResponse{
		Proposal: &Proposal{
			Kind: ProposalCommand,
			Command: strings.Join([]string{
				"apply_patch <<'PATCH'",
				"diff --git a/hello.txt b/hello.txt",
				"--- a/hello.txt",
				"+++ b/hello.txt",
				"@@ -1 +1 @@",
				"-hello",
				"+hello world",
				"PATCH",
			}, "\n"),
		},
	})

	if response.Proposal == nil {
		t.Fatal("expected normalized proposal")
	}
	if response.Proposal.Kind != ProposalPatch {
		t.Fatalf("expected patch proposal, got %#v", response.Proposal)
	}
	if response.Proposal.Command != "" {
		t.Fatalf("expected shell command to be cleared, got %#v", response.Proposal)
	}
	if !strings.Contains(response.Proposal.Patch, "diff --git a/hello.txt b/hello.txt") {
		t.Fatalf("expected unified diff payload, got %#v", response.Proposal)
	}
}

func TestNormalizeAgentResponseConvertsInlinePatchToolApprovalToPatch(t *testing.T) {
	response := normalizeAgentResponse(AgentResponse{
		Approval: &ApprovalRequest{
			ID:      "approval-1",
			Kind:    ApprovalCommand,
			Command: "git apply <<'PATCH'\ndiff --git a/hello.txt b/hello.txt\n--- a/hello.txt\n+++ b/hello.txt\n@@ -1 +1 @@\n-hello\n+hello world\nPATCH",
		},
	})

	if response.Approval == nil {
		t.Fatal("expected normalized approval")
	}
	if response.Approval.Kind != ApprovalPatch {
		t.Fatalf("expected patch approval, got %#v", response.Approval)
	}
	if response.Approval.Command != "" {
		t.Fatalf("expected shell command to be cleared, got %#v", response.Approval)
	}
	if !strings.Contains(response.Approval.Patch, "diff --git a/hello.txt b/hello.txt") {
		t.Fatalf("expected unified diff payload, got %#v", response.Approval)
	}
}

func TestLocalControllerSubmitProposalRefinementBuildsAgentPrompt(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Revised proposal ready.",
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitProposalRefinement(context.Background(), ProposalPayload{
		Kind:        ProposalCommand,
		Command:     "sleep 5",
		Description: "Run a short sleep.",
	}, "Make it one second.")
	if err != nil {
		t.Fatalf("SubmitProposalRefinement() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected user + agent events, got %d", len(events))
	}
	if events[0].Kind != EventUserMessage {
		t.Fatalf("expected visible user note, got %#v", events[0])
	}
	if !strings.Contains(agent.lastInput.Prompt, "Original command: sleep 5") {
		t.Fatalf("expected proposal context in agent prompt, got %q", agent.lastInput.Prompt)
	}
	if events[0].Payload.(TextPayload).Text != "Make it one second." {
		t.Fatalf("expected visible refinement note, got %#v", events[0].Payload)
	}
}

func TestSubmitAgentTurnRepairsInvalidRemotePatchProposalBeforeSurfacing(t *testing.T) {
	agent := &sequenceAgent{
		responses: []AgentResponse{
			{
				Message: "Initial invalid patch.",
				Proposal: &Proposal{
					Kind:        ProposalPatch,
					PatchTarget: PatchTargetRemoteShell,
					Patch: strings.Join([]string{
						"Here is the patch:",
						"diff --git a/foo.txt b/foo.txt",
						"--- a/foo.txt",
						"+++ b/foo.txt",
						"@@ -1 +1 @@",
						"-hello",
						"+hello world",
					}, "\n"),
				},
			},
			{
				Message: "Corrected patch.",
				Proposal: &Proposal{
					Kind:        ProposalPatch,
					PatchTarget: PatchTargetRemoteShell,
					Patch: strings.Join([]string{
						"diff --git a/foo.txt b/foo.txt",
						"--- a/foo.txt",
						"+++ b/foo.txt",
						"@@ -1 +1 @@",
						"-hello",
						"+hello world",
					}, "\n"),
				},
			},
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitAgentPrompt(context.Background(), "fix foo.txt remotely")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}
	if len(agent.lastInputs) != 2 {
		t.Fatalf("expected repair turn, got %d inputs", len(agent.lastInputs))
	}
	if !strings.Contains(agent.lastInputs[1].Prompt, "invalid and was intercepted before it became actionable") {
		t.Fatalf("expected invalid patch repair prompt, got %q", agent.lastInputs[1].Prompt)
	}
	last := events[len(events)-1]
	if last.Kind != EventProposal {
		t.Fatalf("expected repaired proposal event, got %#v", last)
	}
	payload := last.Payload.(ProposalPayload)
	if payload.Kind != ProposalPatch || !strings.HasPrefix(payload.Patch, "diff --git") {
		t.Fatalf("expected corrected patch proposal, got %#v", payload)
	}
}

func TestSubmitAgentTurnDropsPatchProposalAfterFailedRepair(t *testing.T) {
	agent := &sequenceAgent{
		responses: []AgentResponse{
			{
				Message: "Initial invalid patch.",
				Proposal: &Proposal{
					Kind:        ProposalPatch,
					PatchTarget: PatchTargetRemoteShell,
					Patch:       "patch:\ndiff --git a/foo.txt b/foo.txt",
				},
			},
			{
				Message: "Still invalid.",
				Proposal: &Proposal{
					Kind:        ProposalPatch,
					PatchTarget: PatchTargetRemoteShell,
					Patch:       "still not a real diff",
				},
			},
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitAgentPrompt(context.Background(), "fix foo.txt remotely")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}
	for _, event := range events {
		if event.Kind == EventProposal {
			t.Fatalf("did not expect actionable invalid patch proposal, got %#v", events)
		}
	}
	last := events[len(events)-1]
	if last.Kind != EventAgentMessage {
		t.Fatalf("expected final agent message, got %#v", last)
	}
	if !strings.Contains(last.Payload.(TextPayload).Text, invalidPatchProposalNotice) {
		t.Fatalf("expected invalid patch notice, got %#v", last.Payload)
	}
}

func TestBuildInvalidPatchRepairPromptIncludesHunkCountGuidance(t *testing.T) {
	prompt := buildInvalidPatchRepairPrompt(
		"proposal",
		PatchTargetRemoteShell,
		"diff --git a/foo.txt b/foo.txt\n--- a/foo.txt\n+++ b/foo.txt\n@@ -2,2 +2,1 @@\n keep\n+new line\n keep after",
		errors.New("parse patch: gitdiff: line 4: fragment header miscounts lines: +0 old, +1 new"),
	)

	if !strings.Contains(prompt, "Return a corrected JSON response") {
		t.Fatalf("expected repair prompt preamble, got %q", prompt)
	}
	if !strings.Contains(prompt, "If the invalid action was a proposal, return only a patch proposal.") {
		t.Fatalf("expected proposal-only guidance, got %q", prompt)
	}
	if !strings.Contains(prompt, "Valid insertion example:") {
		t.Fatalf("expected insertion example, got %q", prompt)
	}
	if !strings.Contains(prompt, "Patch-specific correction guidance: Recompute the @@ -old_start,old_count +new_start,new_count @@ header") {
		t.Fatalf("expected hunk-count correction guidance, got %q", prompt)
	}
}

func TestLocalControllerApproveRunsCommand(t *testing.T) {
	runner := &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-approve",
			Command:   "rm -rf tmp",
			ExitCode:  0,
			Captured:  "",
		},
	}
	controller := New(&stubAgent{
		response: AgentResponse{
			Approval: &ApprovalRequest{
				ID:      "approval-1",
				Kind:    ApprovalCommand,
				Command: "rm -rf tmp",
			},
		},
	}, runner, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitAgentPrompt(context.Background(), "remove tmp")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected agent events")
	}

	approveEvents, err := controller.DecideApproval(context.Background(), "approval-1", DecisionApprove, "")
	if err != nil {
		t.Fatalf("DecideApproval() error = %v", err)
	}

	if len(approveEvents) != 2 {
		t.Fatalf("expected 2 approval events, got %d", len(approveEvents))
	}

	if runner.commands[0] != "rm -rf tmp" {
		t.Fatalf("expected approved command to run, got %q", runner.commands[0])
	}
}

func TestLocalControllerProposalCommandFillsApprovalCommand(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Approve this test command.",
			Proposal: &Proposal{
				Kind:        ProposalCommand,
				Command:     "bash -lc 'for i in {1..20}; do echo \"$i\"; sleep 1; done'",
				Description: "Streaming loop.",
			},
			Approval: &ApprovalRequest{
				ID:      "approval-1",
				Kind:    ApprovalCommand,
				Title:   "Approve test command",
				Summary: "Run the streaming loop.",
				Risk:    RiskLow,
			},
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitAgentPrompt(context.Background(), "propose a streaming loop and ask for approval")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	var approval ApprovalRequest
	foundApproval := false
	for _, event := range events {
		if event.Kind != EventApproval {
			continue
		}
		payload, ok := event.Payload.(ApprovalRequest)
		if !ok {
			t.Fatalf("expected approval payload, got %#v", event.Payload)
		}
		approval = payload
		foundApproval = true
	}
	if !foundApproval {
		t.Fatal("expected approval event")
	}
	if approval.Command != "bash -lc 'for i in {1..20}; do echo \"$i\"; sleep 1; done'" {
		t.Fatalf("expected approval to inherit proposal command, got %q", approval.Command)
	}
}

func TestLocalControllerApproveWithoutCommandReturnsError(t *testing.T) {
	controller := New(nil, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	controller.task.PendingApproval = &ApprovalRequest{
		ID:      "approval-1",
		Kind:    ApprovalCommand,
		Title:   "Broken approval",
		Summary: "Missing command",
		Risk:    RiskLow,
	}

	events, err := controller.DecideApproval(context.Background(), "approval-1", DecisionApprove, "")
	if err != nil {
		t.Fatalf("DecideApproval() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventError {
		t.Fatalf("expected single error event, got %#v", events)
	}
}

func TestLocalControllerRunnerError(t *testing.T) {
	controller := New(nil, &stubRunner{err: errors.New("boom")}, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	events, err := controller.SubmitShellCommand(context.Background(), "ls")
	if err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}
	if events[len(events)-1].Kind != EventError {
		t.Fatalf("expected trailing error event, got %s", events[len(events)-1].Kind)
	}
}

func TestLocalControllerRefineClearsApproval(t *testing.T) {
	controller := New(&stubAgent{
		response: AgentResponse{
			Approval: &ApprovalRequest{
				ID:      "approval-1",
				Kind:    ApprovalCommand,
				Command: "rm -rf tmp",
			},
		},
	}, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	if _, err := controller.SubmitAgentPrompt(context.Background(), "remove tmp"); err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	events, err := controller.DecideApproval(context.Background(), "approval-1", DecisionRefine, "Refine this proposed command")
	if err != nil {
		t.Fatalf("DecideApproval() error = %v", err)
	}

	if len(events) != 1 || events[0].Kind != EventSystemNotice {
		t.Fatalf("expected single system notice, got %#v", events)
	}
}

func TestLocalControllerSubmitRefinementIncludesApprovalContext(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Refinement noted.",
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	approval := ApprovalRequest{
		ID:      "approval-1",
		Kind:    ApprovalCommand,
		Title:   "Destructive command approval",
		Command: "rm -rf tmp",
		Risk:    RiskHigh,
	}

	events, err := controller.SubmitRefinement(context.Background(), approval, "Use a safer option.")
	if err != nil {
		t.Fatalf("SubmitRefinement() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 refinement events, got %d", len(events))
	}

	if agent.lastInput.Task.PendingApproval == nil || agent.lastInput.Task.PendingApproval.Command != "rm -rf tmp" {
		t.Fatalf("expected pending approval in agent input, got %#v", agent.lastInput.Task.PendingApproval)
	}

	if agent.lastInput.Prompt != "Use a safer option." {
		t.Fatalf("expected note prompt, got %q", agent.lastInput.Prompt)
	}
}

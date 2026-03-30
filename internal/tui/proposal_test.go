package tui

import (
	"aiterm/internal/controller"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
	"time"
)

func TestPlanEntryIsCollapsedInTranscriptButPreservedInDetail(t *testing.T) {
	events := []controller.TranscriptEvent{
		{
			Kind: controller.EventPlan,
			Payload: controller.PlanPayload{
				Summary: "Inspect and repair the workspace.",
				Steps: []controller.PlanStep{
					{Text: "Review the current files.", Status: controller.PlanStepDone},
					{Text: "Apply the next patch.", Status: controller.PlanStepInProgress},
					{Text: "Run tests.", Status: controller.PlanStepPending},
					{Text: "Summarize the result.", Status: controller.PlanStepPending},
				},
			},
		},
	}

	entries := eventsToEntries(events, true)
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}

	if !strings.Contains(entries[0].Body, "... (2 more steps, Ctrl+O to inspect)") {
		t.Fatalf("expected collapsed plan preview, got %q", entries[0].Body)
	}

	if !strings.Contains(entries[0].Detail, "[ ] 4. Summarize the result.") {
		t.Fatalf("expected full plan detail, got %q", entries[0].Detail)
	}
}

func TestPlanEventUpdatesActivePlanCard(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 100
	model.height = 24

	updated, _ := model.Update(controllerEventsMsg{
		events: []controller.TranscriptEvent{
			{
				Kind: controller.EventPlan,
				Payload: controller.PlanPayload{
					Summary: "Inspect and repair the workspace.",
					Steps: []controller.PlanStep{
						{Text: "Review the current files.", Status: controller.PlanStepDone},
						{Text: "Apply the next patch.", Status: controller.PlanStepInProgress},
						{Text: "Run tests.", Status: controller.PlanStepPending},
					},
				},
			},
		},
	})
	model = updated.(Model)

	view := model.View()
	if !strings.Contains(view, "Active Plan") {
		t.Fatalf("expected active plan card, got %q", view)
	}
	if !strings.Contains(view, "[x] 1. Review the current files.") {
		t.Fatalf("expected completed step in card, got %q", view)
	}
	if !strings.Contains(view, "Plan 2/3") {
		t.Fatalf("expected progress summary, got %q", view)
	}
	if !strings.Contains(view, "Informational only. Ctrl+G continues the plan.") {
		t.Fatalf("expected informational plan footer, got %q", view)
	}
	if strings.Contains(view, "Y continue") {
		t.Fatalf("did not expect approval-style Y affordance in plan card, got %q", view)
	}
}

func TestCompletedPlanEventClearsActivePlanCard(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.activePlan = &controller.ActivePlan{
		Summary: "Inspect and repair the workspace.",
		Steps: []controller.PlanStep{
			{Text: "Review the current files.", Status: controller.PlanStepInProgress},
		},
	}

	updated, _ := model.Update(controllerEventsMsg{
		events: []controller.TranscriptEvent{
			{
				Kind: controller.EventPlan,
				Payload: controller.PlanPayload{
					Summary: "Inspect and repair the workspace.",
					Steps: []controller.PlanStep{
						{Text: "Review the current files.", Status: controller.PlanStepDone},
					},
				},
			},
		},
	})
	next := updated.(Model)

	if next.activePlan != nil {
		t.Fatalf("expected completed plan to clear, got %#v", next.activePlan)
	}
}

func TestProposalEntryIsCollapsedInTranscriptButPreservedInDetail(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/app.go b/app.go",
		"--- a/app.go",
		"+++ b/app.go",
		"@@ -1 +1,3 @@",
		"-old line",
		"+new line 1",
		"+new line 2",
		"+new line 3",
	}, "\n")
	events := []controller.TranscriptEvent{
		{
			Kind: controller.EventProposal,
			Payload: controller.ProposalPayload{
				Kind:        controller.ProposalPatch,
				Description: "Apply a targeted patch.",
				Patch:       patch,
			},
		},
	}

	entries := eventsToEntries(events, true)
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}

	if !strings.Contains(entries[0].Body, "patch attached (8 lines, Ctrl+O to inspect)") {
		t.Fatalf("expected collapsed proposal preview, got %q", entries[0].Body)
	}

	if !strings.Contains(entries[0].Detail, "kind: patch") || !strings.Contains(entries[0].Detail, "diff --git a/app.go b/app.go") {
		t.Fatalf("expected full proposal detail, got %q", entries[0].Detail)
	}
}

func TestApprovalEntryIsCollapsedInTranscriptButPreservedInDetail(t *testing.T) {
	events := []controller.TranscriptEvent{
		{
			Kind: controller.EventApproval,
			Payload: controller.ApprovalRequest{
				ID:      "approval-1",
				Kind:    controller.ApprovalCommand,
				Title:   "Destructive command approval",
				Summary: "Please review this carefully before execution.",
				Command: "rm -rf tmp",
				Risk:    controller.RiskHigh,
			},
		},
	}

	entries := eventsToEntries(events, true)
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}

	if !strings.Contains(entries[0].Body, "... (1 more lines, Ctrl+O to inspect)") {
		t.Fatalf("expected collapsed approval preview, got %q", entries[0].Body)
	}

	if !strings.Contains(entries[0].Detail, "kind: command") || !strings.Contains(entries[0].Detail, "risk: high") {
		t.Fatalf("expected full approval detail, got %q", entries[0].Detail)
	}
}

func TestAgentProposalCanRunThroughController(t *testing.T) {
	ctrl := &fakeController{
		agentEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventUserMessage,
				Payload: controller.TextPayload{Text: "list files"},
			},
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "I can inspect the current directory contents."},
			},
			{
				Kind: controller.EventProposal,
				Payload: controller.ProposalPayload{
					Kind:        controller.ProposalCommand,
					Command:     "ls -lah",
					Description: "List files with permissions and sizes.",
				},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "list files"

	updated, cmd := model.submit()
	next := updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if next.pendingProposal == nil || next.pendingProposal.Command != "ls -lah" {
		t.Fatalf("expected pending proposal command, got %#v", next.pendingProposal)
	}

	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	next = updated.(Model)
	msg = controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if len(ctrl.shellCommands) != 1 || ctrl.shellCommands[0] != "ls -lah" {
		t.Fatalf("expected proposal command to run, got %#v", ctrl.shellCommands)
	}

	if next.pendingProposal != nil {
		t.Fatalf("expected pending proposal to clear after execution, got %#v", next.pendingProposal)
	}
}

func TestProposalCanBeRejected(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalCommand,
		Command:     "sleep 5",
		Description: "Run a short sleep.",
	}
	initialEntries := len(model.entries)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	next := updated.(Model)

	if next.pendingProposal != nil {
		t.Fatalf("expected pending proposal to clear, got %#v", next.pendingProposal)
	}
	if len(next.entries) != initialEntries+1 {
		t.Fatalf("expected a system entry after rejection, got %d entries", len(next.entries))
	}
	if next.entries[len(next.entries)-1].Body != "Proposal dismissed." {
		t.Fatalf("unexpected dismissal entry: %#v", next.entries[len(next.entries)-1])
	}
}

func TestAgentModeKeysProposalBecomesPendingProposal(t *testing.T) {
	ctrl := &fakeController{
		agentEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventUserMessage,
				Payload: controller.TextPayload{Text: "go ahead and press enter"},
			},
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Sending Enter to the active prompt to continue."},
			},
			{
				Kind: controller.EventProposal,
				Payload: controller.ProposalPayload{
					Kind:        controller.ProposalKeys,
					Keys:        "\n",
					Description: "Send Enter to the active terminal.",
				},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "go ahead and press enter"

	updated, cmd := model.submit()
	next := updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if next.pendingProposal == nil {
		t.Fatal("expected pending keys proposal")
	}
	if next.pendingProposal.Kind != controller.ProposalKeys {
		t.Fatalf("expected keys proposal kind, got %#v", next.pendingProposal)
	}
	if next.pendingProposal.Keys != "\n" {
		t.Fatalf("expected enter key payload, got %#v", next.pendingProposal.Keys)
	}
}

func TestAgentModeAnswerProposalDoesNotBecomePendingProposal(t *testing.T) {
	ctrl := &fakeController{
		agentEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventUserMessage,
				Payload: controller.TextPayload{Text: "continue"},
			},
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Selection is complete."},
			},
			{
				Kind: controller.EventProposal,
				Payload: controller.ProposalPayload{
					Kind:        controller.ProposalAnswer,
					Description: "No further action is needed.",
				},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "continue"

	updated, cmd := model.submit()
	next := updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if next.pendingProposal != nil {
		t.Fatalf("expected answer proposal to stay out of pending action state, got %#v", next.pendingProposal)
	}
}

func TestPrimaryActionRunsEnterOnlyKeysProposal(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.takeControl = takeControlConfig{
		SocketName:    "shuttle-test",
		SessionName:   "shuttle-test",
		TrackedPaneID: "%0",
		DetachKey:     TakeControlKey,
	}
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   `bash -lc 'read -n 1 -s -r -p "Press any key" _'`,
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionAwaitingInput,
		StartedAt: time.Now(),
	}
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalKeys,
		Keys:        "\n",
		Description: "Send Enter to the active terminal.",
	}

	updated, cmd := model.primaryAction()
	next := updated.(Model)

	if cmd == nil {
		t.Fatal("expected keys proposal primary action to return a send command")
	}
	if next.pendingProposal != nil {
		t.Fatalf("expected pending proposal to clear after sending keys, got %#v", next.pendingProposal)
	}
}

func TestPrimaryActionRunsInspectContextProposal(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalInspectContext,
		Description: "Refresh the active shell identity and current working directory.",
	}

	updated, cmd := model.primaryAction()
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected inspect_context proposal primary action to run")
	}
	msg := controllerEventsFromCmd(t, cmd)
	updated, cmd = next.Update(msg)
	next = updated.(Model)

	if ctrl.inspectContextCalls != 1 {
		t.Fatalf("expected one inspect_context call, got %d", ctrl.inspectContextCalls)
	}
	if next.pendingProposal != nil {
		t.Fatalf("expected pending proposal to clear after inspect_context, got %#v", next.pendingProposal)
	}
	if ctrl.continueCalls != 0 {
		t.Fatalf("expected auto-continue to not run before processing follow-up cmd, got %d", ctrl.continueCalls)
	}
	if cmd == nil {
		t.Fatal("expected inspect_context result to trigger follow-up continuation")
	}
	msg = controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)
	if ctrl.continueCalls != 1 {
		t.Fatalf("expected inspect_context to flow into ContinueAfterCommand, got %d", ctrl.continueCalls)
	}
	if next.busy {
		t.Fatal("expected inspect_context continuation to settle cleanly")
	}
}

func TestProposalCanBeRefined(t *testing.T) {
	ctrl := &fakeController{
		agentEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Revised proposal ready."},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalCommand,
		Command:     "sleep 5",
		Description: "Run a short sleep.",
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	next := updated.(Model)
	if next.refiningProposal == nil {
		t.Fatal("expected refining proposal state")
	}
	if next.mode != AgentMode {
		t.Fatalf("expected agent mode during proposal refinement, got %s", next.mode)
	}

	next.input = "Make it just one second."
	updated, cmd := next.submit()
	next = updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if len(ctrl.refinements) != 1 {
		t.Fatalf("expected one proposal refinement call, got %d", len(ctrl.refinements))
	}
	if ctrl.refinements[0].proposal.Command != "sleep 5" {
		t.Fatalf("expected original proposal command to be preserved, got %#v", ctrl.refinements[0].proposal)
	}
	if ctrl.refinements[0].note != "Make it just one second." {
		t.Fatalf("unexpected refinement note %q", ctrl.refinements[0].note)
	}
}

func TestProposalCommandCanBeTweakedLocally(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalCommand,
		Command:     "grep -R foo .",
		Description: "Search the current directory tree.",
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	next := updated.(Model)

	if next.editingProposal == nil {
		t.Fatal("expected editing proposal state")
	}
	if next.input != "grep -R foo ." {
		t.Fatalf("expected original command in composer, got %q", next.input)
	}
	if next.pendingProposal != nil {
		t.Fatalf("expected pending proposal to clear while editing, got %#v", next.pendingProposal)
	}

	next.input = "grep -R foo ~/ "
	updated, _ = next.submit()
	next = updated.(Model)

	if next.editingProposal != nil {
		t.Fatal("expected editing state to clear after save")
	}
	if next.pendingProposal == nil || next.pendingProposal.Command != "grep -R foo ~/" {
		t.Fatalf("expected updated proposal command, got %#v", next.pendingProposal)
	}
	if next.entries[len(next.entries)-1].Title != "proposal" {
		t.Fatalf("expected updated proposal entry, got %#v", next.entries[len(next.entries)-1])
	}
}

func TestEscapeRestoresProposalWhileEditingCommand(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.editingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalCommand,
		Command:     "grep -R foo .",
		Description: "Search the current directory tree.",
	}
	model.input = "grep -R foo ~/"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next := updated.(Model)

	if next.editingProposal != nil {
		t.Fatal("expected editing proposal state to clear")
	}
	if next.pendingProposal == nil || next.pendingProposal.Command != "grep -R foo ." {
		t.Fatalf("expected original proposal to be restored, got %#v", next.pendingProposal)
	}
}

func TestShellInterruptMsgClearsActiveExecutionState(t *testing.T) {
	ctrl := &fakeController{
		activeExecution: &controller.CommandExecution{
			ID:        "cmd-1",
			Command:   "sleep 60",
			Origin:    controller.CommandOriginAgentProposal,
			State:     controller.CommandExecutionRunning,
			StartedAt: time.Now(),
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	canceled := false
	model.busy = true
	model.proposalRunPending = true
	model.activeExecution = ctrl.activeExecution
	model.inFlightCancel = func() {
		canceled = true
	}
	initialEntries := len(model.entries)

	updated, _ := model.Update(shellInterruptMsg{})
	next := updated.(Model)

	if !canceled {
		t.Fatal("expected interrupt to cancel in-flight command wait")
	}
	if next.activeExecution != nil {
		t.Fatalf("expected active execution to clear, got %#v", next.activeExecution)
	}
	if next.proposalRunPending {
		t.Fatal("expected proposal run pending to clear")
	}
	if next.busy {
		t.Fatal("expected busy state to clear")
	}
	if len(next.entries) != initialEntries+1 {
		t.Fatalf("expected one interrupt entry, got %d", len(next.entries)-initialEntries)
	}
	last := next.entries[len(next.entries)-1]
	if last.Title != "result" || !strings.Contains(last.Body, "status=canceled") {
		t.Fatalf("expected canceled result entry, got %#v", last)
	}
}

func TestDirectShellSubmissionShowsExpandedResultInTranscript(t *testing.T) {
	ctrl := &fakeController{
		shellEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventCommandStart,
				Payload: controller.CommandStartPayload{Command: "ls"},
			},
			{
				Kind: controller.EventCommandResult,
				Payload: controller.CommandResultSummary{
					Command:  "ls",
					ExitCode: 0,
					Summary:  "file-a\nfile-b\nfile-c\nfile-d\nfile-e\nfile-f\nfile-g",
				},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.input = "ls"

	updated, cmd := model.submit()
	model = updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	last := model.entries[len(model.entries)-1]
	if last.Title != "result" {
		t.Fatalf("expected result entry, got %s", last.Title)
	}

	if strings.Contains(last.Body, "Ctrl+O to inspect") {
		t.Fatalf("expected direct shell result to stay expanded, got %q", last.Body)
	}

	if !strings.Contains(last.Body, "file-g") {
		t.Fatalf("expected full shell output in transcript, got %q", last.Body)
	}
}

func TestAgentRunResultStaysCollapsedInTranscript(t *testing.T) {
	ctrl := &fakeController{
		agentEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventUserMessage,
				Payload: controller.TextPayload{Text: "list files"},
			},
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "I can inspect the current directory contents."},
			},
			{
				Kind: controller.EventProposal,
				Payload: controller.ProposalPayload{
					Kind:        controller.ProposalCommand,
					Command:     "ls -lah",
					Description: "List files with permissions and sizes.",
				},
			},
		},
		shellEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventCommandStart,
				Payload: controller.CommandStartPayload{Command: "ls -lah"},
			},
			{
				Kind: controller.EventCommandResult,
				Payload: controller.CommandResultSummary{
					Command:  "ls -lah",
					ExitCode: 0,
					Summary:  makeMultilineBody(10),
				},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "list files"

	updated, cmd := model.submit()
	model = updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	model = updated.(Model)
	msg = controllerEventsFromCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	last := model.entries[len(model.entries)-1]
	if last.Title != "result" {
		t.Fatalf("expected result entry, got %s", last.Title)
	}

	if last.Command != "ls -lah" {
		t.Fatalf("expected command metadata on combined result entry, got %#v", last)
	}
	if !strings.Contains(last.Body, "line 9") {
		t.Fatalf("expected inline output on result entry, got %q", last.Body)
	}
	for _, entry := range model.entries {
		if entry.Title == "shell" && entry.Body == "ls -lah" {
			t.Fatalf("expected shell start row to collapse into the final result entry, got %#v", model.entries)
		}
	}
}

func TestProposalRunAutomaticallyContinuesAgentLoop(t *testing.T) {
	ctrl := &fakeController{
		agentEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventUserMessage,
				Payload: controller.TextPayload{Text: "list files"},
			},
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "I can inspect the current directory contents."},
			},
			{
				Kind: controller.EventProposal,
				Payload: controller.ProposalPayload{
					Kind:        controller.ProposalCommand,
					Command:     "ls -lah",
					Description: "List files with permissions and sizes.",
				},
			},
		},
		shellEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventCommandStart,
				Payload: controller.CommandStartPayload{Command: "ls -lah"},
			},
			{
				Kind: controller.EventCommandResult,
				Payload: controller.CommandResultSummary{
					Command:  "ls -lah",
					ExitCode: 0,
					Summary:  "file-a\nfile-b",
				},
			},
		},
		continueEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "I reviewed the result and can continue."},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "list files"

	updated, cmd := model.submit()
	model = updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	model = updated.(Model)
	msg = controllerEventsFromCmd(t, cmd)
	updated, cmd = model.Update(msg)
	model = updated.(Model)

	if cmd == nil {
		t.Fatal("expected auto-continue command")
	}

	msg = controllerEventsFromCmd(t, cmd)
	if ctrl.continueCalls != 1 {
		t.Fatalf("expected one auto-continue call, got %d", ctrl.continueCalls)
	}
	updated, _ = model.Update(msg)
	model = updated.(Model)

	last := model.entries[len(model.entries)-1]
	if last.Title != "agent" {
		t.Fatalf("expected trailing agent continuation, got %s", last.Title)
	}
}

func TestDirectShellCommandDoesNotAutoContinueAgentLoop(t *testing.T) {
	ctrl := &fakeController{
		shellEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventCommandStart,
				Payload: controller.CommandStartPayload{Command: "ls"},
			},
			{
				Kind: controller.EventCommandResult,
				Payload: controller.CommandResultSummary{
					Command:  "ls",
					ExitCode: 0,
					Summary:  "file-a\nfile-b",
				},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.input = "ls"

	updated, cmd := model.submit()
	model = updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, nextCmd := model.Update(msg)
	model = updated.(Model)

	if ctrl.continueCalls != 0 {
		t.Fatalf("expected no auto-continue call, got %d", ctrl.continueCalls)
	}
	if nextCmd != nil {
		t.Fatal("expected no shell-tail refresh after direct shell result")
	}
	if model.showShellTail {
		t.Fatal("expected shell-tail preview to clear after direct shell result")
	}
}

func TestPatchProposalRunsThroughPatchControllerPath(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/README.md b/README.md",
		"--- a/README.md",
		"+++ b/README.md",
		"@@ -1 +1 @@",
		"-before",
		"+after",
	}, "\n")
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalPatch,
		Patch:       patch,
		Description: "Update the readme.",
	}

	updated, cmd := model.primaryAction()
	next := updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, followCmd := next.Update(msg)
	next = updated.(Model)

	if len(ctrl.appliedPatches) != 1 || ctrl.appliedPatches[0] != patch {
		t.Fatalf("expected patch apply call, got %#v", ctrl.appliedPatches)
	}
	if next.pendingProposal != nil {
		t.Fatalf("expected patch proposal to clear, got %#v", next.pendingProposal)
	}
	if next.showShellTail {
		t.Fatal("expected patch apply to avoid shell-tail mode")
	}
	if followCmd == nil {
		t.Fatal("expected auto-continue after patch apply")
	}
}

func TestPatchProposalActionCardOmitsEditAffordance(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalPatch,
		Patch:       "diff --git a/a b/a",
		Description: "Apply a local patch.",
	}

	spec := model.currentActionCardSpec()
	if spec == nil {
		t.Fatal("expected action card")
	}
	if spec.title != "Proposed Patch" {
		t.Fatalf("expected patch title, got %#v", spec)
	}
	for _, button := range spec.buttons {
		if button.action == actionCardEditProposal {
			t.Fatalf("did not expect edit affordance for patch proposal: %#v", spec.buttons)
		}
	}
}

func TestPatchProposalAutoContinueUsesPatchContinuation(t *testing.T) {
	ctrl := &fakeController{
		patchEvents: []controller.TranscriptEvent{
			{
				Kind: controller.EventPatchApplyResult,
				Payload: controller.PatchApplySummary{
					Applied: true,
					Updated: 1,
				},
			},
		},
		continueAfterPatchEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Patch applied and reviewed."},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.pendingProposal = &controller.ProposalPayload{
		Kind:  controller.ProposalPatch,
		Patch: "diff --git a/README.md b/README.md",
	}

	updated, cmd := model.primaryAction()
	model = updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, cmd = model.Update(msg)
	model = updated.(Model)

	if cmd == nil {
		t.Fatal("expected auto-continue command")
	}
	_ = controllerEventsFromCmd(t, cmd)
	if ctrl.continueAfterPatchCalls != 1 {
		t.Fatalf("expected patch auto-continue call, got %d", ctrl.continueAfterPatchCalls)
	}
}

func TestPatchProposalFailureStillAutoContinuesThroughPatchContinuation(t *testing.T) {
	ctrl := &fakeController{
		patchEvents: []controller.TranscriptEvent{
			{
				Kind: controller.EventPatchApplyResult,
				Payload: controller.PatchApplySummary{
					Applied: false,
					Error:   "parse patch: invalid line operation",
				},
			},
			{
				Kind:    controller.EventError,
				Payload: controller.TextPayload{Text: "patch apply failed: parse patch: invalid line operation"},
			},
		},
		continueAfterPatchEvents: []controller.TranscriptEvent{
			{
				Kind: controller.EventProposal,
				Payload: controller.ProposalPayload{
					Kind:  controller.ProposalPatch,
					Patch: "diff --git a/hello.txt b/hello.txt",
				},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalPatch,
		Patch:       "diff --git a/hello.txt b/hello.txt",
		Description: "Retry the patch.",
	}

	updated, cmd := model.primaryAction()
	model = updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, cmd = model.Update(msg)
	model = updated.(Model)

	if cmd == nil {
		t.Fatal("expected failed patch apply to trigger patch continuation")
	}
	msg = controllerEventsFromCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	if ctrl.continueAfterPatchCalls != 1 {
		t.Fatalf("expected patch continuation call, got %d", ctrl.continueAfterPatchCalls)
	}
	if model.pendingProposal == nil || model.pendingProposal.Kind != controller.ProposalPatch {
		t.Fatalf("expected follow-up patch proposal after failure, got %#v", model.pendingProposal)
	}
}

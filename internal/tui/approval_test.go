package tui

import (
	"aiterm/internal/controller"
	"aiterm/internal/provider"
	"aiterm/internal/shell"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
)

func TestCtrlGContinuesActivePlan(t *testing.T) {
	ctrl := &fakeController{
		continueEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Continuing the plan."},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.activePlan = &controller.ActivePlan{
		Summary: "Inspect and repair the workspace.",
		Steps: []controller.PlanStep{
			{Text: "Review the current files.", Status: controller.PlanStepInProgress},
			{Text: "Run tests.", Status: controller.PlanStepPending},
		},
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected continue-plan command")
	}

	msg := controllerEventsFromCmd(t, cmd)
	if ctrl.continueCalls != 1 {
		t.Fatalf("expected continue plan call, got %d", ctrl.continueCalls)
	}

	updated, _ = model.Update(msg)
	model = updated.(Model)
	last := model.entries[len(model.entries)-1]
	if last.Title != "agent" {
		t.Fatalf("expected trailing agent continuation, got %s", last.Title)
	}
}

func TestCtrlEApprovesPendingApproval(t *testing.T) {
	ctrl := &fakeController{
		agentEvents: []controller.TranscriptEvent{
			{
				Kind: controller.EventApproval,
				Payload: controller.ApprovalRequest{
					ID:      "approval-1",
					Kind:    controller.ApprovalCommand,
					Title:   "Destructive command approval",
					Command: "rm -rf tmp",
					Risk:    controller.RiskHigh,
				},
			},
		},
		decisionEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventCommandStart,
				Payload: controller.CommandStartPayload{Command: "rm -rf tmp"},
			},
			{
				Kind: controller.EventCommandResult,
				Payload: controller.CommandResultSummary{
					Command:  "rm -rf tmp",
					ExitCode: 0,
					Summary:  "(no output)",
				},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.pendingApproval = &controller.ApprovalRequest{
		ID:      "approval-1",
		Kind:    controller.ApprovalCommand,
		Command: "rm -rf tmp",
		Risk:    controller.RiskHigh,
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected approval command")
	}

	_ = controllerEventsFromCmd(t, cmd)
	if len(ctrl.decisions) != 1 || ctrl.decisions[0].decision != controller.DecisionApprove {
		t.Fatalf("expected Ctrl+E to approve, got %#v", ctrl.decisions)
	}
}

func TestCtrlEContinuesActivePlan(t *testing.T) {
	ctrl := &fakeController{
		continueEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Continuing the plan."},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.activePlan = &controller.ActivePlan{
		Summary: "Inspect and repair the workspace.",
		Steps: []controller.PlanStep{
			{Text: "Review the current files.", Status: controller.PlanStepInProgress},
		},
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected continue-plan command")
	}

	_ = controllerEventsFromCmd(t, cmd)
	if ctrl.continueCalls != 1 {
		t.Fatalf("expected Ctrl+E to continue active plan, got %d", ctrl.continueCalls)
	}
}

func TestResultDetailShowsCommandBeforeOutput(t *testing.T) {
	entries := eventsToEntries([]controller.TranscriptEvent{
		{
			Kind: controller.EventCommandResult,
			Payload: controller.CommandResultSummary{
				Command:  "bash -lc '\nprintf \"hello\\n\"\n'",
				ExitCode: 0,
				Summary:  "hello",
			},
		},
	}, true)

	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}

	detail := entries[0].Detail
	outputIndex := strings.Index(detail, "hello")
	commandIndex := strings.Index(detail, "command:\nbash -lc '")
	if commandIndex == -1 {
		t.Fatalf("expected command section in detail, got %q", detail)
	}
	if outputIndex == -1 {
		t.Fatalf("expected output in detail, got %q", detail)
	}
	if commandIndex > outputIndex {
		t.Fatalf("expected command before output, got %q", detail)
	}
}

func TestDetailViewShowsDownIndicatorWhenMoreBelow(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 12
	model.entries = []Entry{
		{Title: "result", Detail: makeMultilineBody(20)},
	}
	model.detailOpen = true

	view := model.View()
	if !strings.Contains(view, "↓") {
		t.Fatalf("expected down indicator in detail footer, got %q", view)
	}
}

func TestDetailViewShowsUpIndicatorAfterScroll(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 12
	model.entries = []Entry{
		{Title: "result", Detail: makeMultilineBody(20)},
	}
	model.detailOpen = true
	model.detailScroll = 2

	view := model.View()
	if !strings.Contains(view, "↑") {
		t.Fatalf("expected up indicator in detail footer, got %q", view)
	}
}

func TestApprovalApproveUsesControllerDecision(t *testing.T) {
	ctrl := &fakeController{
		agentEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventUserMessage,
				Payload: controller.TextPayload{Text: "rm -rf tmp"},
			},
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "This action requires approval."},
			},
			{
				Kind: controller.EventApproval,
				Payload: controller.ApprovalRequest{
					ID:      "approval-1",
					Kind:    controller.ApprovalCommand,
					Title:   "Destructive command approval",
					Command: "rm -rf tmp",
					Risk:    controller.RiskHigh,
				},
			},
		},
		decisionEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventCommandStart,
				Payload: controller.CommandStartPayload{Command: "rm -rf tmp"},
			},
			{
				Kind: controller.EventCommandResult,
				Payload: controller.CommandResultSummary{
					Command:  "rm -rf tmp",
					ExitCode: 0,
					Summary:  "(no output)",
				},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "rm -rf tmp"

	updated, cmd := model.submit()
	next := updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if next.pendingApproval == nil || next.pendingApproval.ID != "approval-1" {
		t.Fatalf("expected pending approval, got %#v", next.pendingApproval)
	}

	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	next = updated.(Model)
	msg = controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if len(ctrl.decisions) != 1 {
		t.Fatalf("expected one approval decision, got %d", len(ctrl.decisions))
	}

	if ctrl.decisions[0].decision != controller.DecisionApprove {
		t.Fatalf("expected approve decision, got %s", ctrl.decisions[0].decision)
	}

	if next.pendingApproval != nil {
		t.Fatalf("expected pending approval to clear after approval, got %#v", next.pendingApproval)
	}
}

func TestPatchApprovalUsesPatchControllerPath(t *testing.T) {
	ctrl := &fakeController{
		decisionEvents: []controller.TranscriptEvent{
			{
				Kind: controller.EventPatchApplyResult,
				Payload: controller.PatchApplySummary{
					Applied: true,
					Updated: 1,
				},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.pendingApproval = &controller.ApprovalRequest{
		ID:      "approval-1",
		Kind:    controller.ApprovalPatch,
		Title:   "Apply patch",
		Summary: "Update README",
		Patch: strings.Join([]string{
			"diff --git a/README.md b/README.md",
			"--- a/README.md",
			"+++ b/README.md",
			"@@ -1 +1 @@",
			"-before",
			"+after",
		}, "\n"),
		Risk: controller.RiskLow,
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected approval command")
	}
	msg := controllerEventsFromCmd(t, cmd)
	updated, followCmd := model.Update(msg)
	model = updated.(Model)

	if len(ctrl.decisions) != 1 || ctrl.decisions[0].decision != controller.DecisionApprove {
		t.Fatalf("expected approve decision, got %#v", ctrl.decisions)
	}
	if model.showShellTail {
		t.Fatal("expected patch approval to avoid shell-tail mode")
	}
	if followCmd == nil {
		t.Fatal("expected auto-continue after patch approval")
	}
}

func TestPatchApprovalActionCardUsesApplyCopy(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.pendingApproval = &controller.ApprovalRequest{
		ID:      "approval-1",
		Kind:    controller.ApprovalPatch,
		Title:   "Apply patch",
		Summary: "Update README",
		Patch:   "diff --git a/README.md b/README.md",
		Risk:    controller.RiskLow,
	}

	spec := model.currentActionCardSpec()
	if spec == nil {
		t.Fatal("expected approval card")
	}
	if spec.buttons[0].label != "Y apply" {
		t.Fatalf("expected patch apply copy, got %#v", spec.buttons)
	}
}

func TestApprovalHidesSameTurnProposalCard(t *testing.T) {
	ctrl := &fakeController{
		agentEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventUserMessage,
				Payload: controller.TextPayload{Text: "search for foo"},
			},
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "This needs approval."},
			},
			{
				Kind: controller.EventProposal,
				Payload: controller.ProposalPayload{
					Kind:        controller.ProposalCommand,
					Description: "Search the home directory for foo.",
					Command:     "rg -l foo ~",
				},
			},
			{
				Kind: controller.EventApproval,
				Payload: controller.ApprovalRequest{
					ID:      "approval-1",
					Kind:    controller.ApprovalCommand,
					Title:   "Approve ripgrep search",
					Command: "rg -l foo ~",
					Risk:    controller.RiskLow,
				},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "search for foo"

	updated, cmd := model.submit()
	next := updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if next.pendingApproval == nil || next.pendingApproval.ID != "approval-1" {
		t.Fatalf("expected pending approval, got %#v", next.pendingApproval)
	}
	if next.pendingProposal != nil {
		t.Fatalf("expected same-turn proposal to stay hidden behind approval, got %#v", next.pendingProposal)
	}
}

func TestApprovalApproveClearsStaleProposalState(t *testing.T) {
	ctrl := &fakeController{
		decisionEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventCommandStart,
				Payload: controller.CommandStartPayload{Command: "rg -l foo ~"},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.pendingApproval = &controller.ApprovalRequest{
		ID:      "approval-1",
		Kind:    controller.ApprovalCommand,
		Title:   "Approve ripgrep search",
		Command: "rg -l foo ~",
		Risk:    controller.RiskLow,
	}
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalCommand,
		Description: "Search the home directory for foo.",
		Command:     "rg -l foo ~",
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected approval command")
	}
	if next.pendingProposal != nil {
		t.Fatalf("expected stale proposal to clear on approval, got %#v", next.pendingProposal)
	}

	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if next.pendingProposal != nil {
		t.Fatalf("expected stale proposal to stay cleared after approval events, got %#v", next.pendingProposal)
	}
}

func TestApprovalApproveAutomaticallyContinuesAgentLoop(t *testing.T) {
	ctrl := &fakeController{
		agentEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventUserMessage,
				Payload: controller.TextPayload{Text: "rm -rf tmp"},
			},
			{
				Kind: controller.EventApproval,
				Payload: controller.ApprovalRequest{
					ID:      "approval-1",
					Kind:    controller.ApprovalCommand,
					Title:   "Destructive command approval",
					Command: "rm -rf tmp",
					Risk:    controller.RiskHigh,
				},
			},
		},
		decisionEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventCommandStart,
				Payload: controller.CommandStartPayload{Command: "rm -rf tmp"},
			},
			{
				Kind: controller.EventCommandResult,
				Payload: controller.CommandResultSummary{
					Command:  "rm -rf tmp",
					ExitCode: 0,
					Summary:  "(no output)",
				},
			},
		},
		continueEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "The approved command completed."},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "rm -rf tmp"

	updated, cmd := model.submit()
	model = updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
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

func TestApprovalRefineUsesSeparateNoteFlow(t *testing.T) {
	ctrl := &fakeController{
		agentEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventUserMessage,
				Payload: controller.TextPayload{Text: "rm -rf tmp"},
			},
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "This action requires approval."},
			},
			{
				Kind: controller.EventApproval,
				Payload: controller.ApprovalRequest{
					ID:      "approval-1",
					Kind:    controller.ApprovalCommand,
					Title:   "Destructive command approval",
					Summary: "rm -rf tmp",
					Command: "rm -rf tmp",
					Risk:    controller.RiskHigh,
				},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "rm -rf tmp"

	updated, cmd := model.submit()
	next := updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	next = updated.(Model)
	msg = controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if len(ctrl.decisions) != 1 || ctrl.decisions[0].decision != controller.DecisionRefine {
		t.Fatalf("expected refine decision, got %#v", ctrl.decisions)
	}

	if next.mode != AgentMode {
		t.Fatalf("expected Agent mode after refine, got %s", next.mode)
	}

	if next.input != "" {
		t.Fatalf("expected blank composer for refinement note, got %q", next.input)
	}

	if next.pendingApproval != nil {
		t.Fatalf("expected pending approval to clear after refine, got %#v", next.pendingApproval)
	}

	if next.refiningApproval == nil || next.refiningApproval.Command != "rm -rf tmp" {
		t.Fatalf("expected refining approval context, got %#v", next.refiningApproval)
	}

	next.input = "Use a safer option."
	updated, cmd = next.submit()
	next = updated.(Model)
	msg = controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if len(ctrl.refinements) != 1 {
		t.Fatalf("expected one refinement submission, got %d", len(ctrl.refinements))
	}

	if ctrl.refinements[0].approval.Command != "rm -rf tmp" {
		t.Fatalf("expected original command in refinement, got %#v", ctrl.refinements[0].approval)
	}

	if ctrl.refinements[0].note != "Use a safer option." {
		t.Fatalf("expected refinement note, got %q", ctrl.refinements[0].note)
	}
}

func TestSlashOnboardOpensConfigureProvidersSettings(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.mode = AgentMode
	model.input = "/onboard"
	model = model.WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"}}}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			return []provider.ModelOption{{ID: profile.Model}}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)

	updated, cmd := model.submit()
	next := updated.(Model)
	if cmd != nil {
		t.Fatal("expected onboarding command to open synchronously")
	}
	if !next.settingsOpen || next.settingsStep != settingsStepProviders {
		t.Fatalf("expected /onboard to open configure providers, got open=%t step=%q", next.settingsOpen, next.settingsStep)
	}
	if next.input != "" {
		t.Fatalf("expected composer to clear, got %q", next.input)
	}
}

package tui

import (
	"context"
	"fmt"
	"testing"

	"aiterm/internal/controller"
	"aiterm/internal/tmux"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTabTogglesMode(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	next := updated.(Model)

	if next.mode != AgentMode {
		t.Fatalf("expected AgentMode, got %s", next.mode)
	}
}

func TestAgentModeSubmissionAddsPlaceholderResponse(t *testing.T) {
	ctrl := &fakeController{
		agentEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventUserMessage,
				Payload: controller.TextPayload{Text: "help me"},
			},
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Mock agent received: help me"},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "help me"

	updated, cmd := model.submit()
	next := updated.(Model)
	msg := cmd().(controllerEventsMsg)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if len(next.entries) < 4 {
		t.Fatalf("expected transcript entries, got %d", len(next.entries))
	}

	if next.entries[len(next.entries)-1].Title != "agent" {
		t.Fatalf("expected final entry to be agent, got %s", next.entries[len(next.entries)-1].Title)
	}
}

func TestSpaceKeyAddsSpaceToComposer(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.input = "ls"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	next := updated.(Model)

	if next.input != "ls " {
		t.Fatalf("expected input %q, got %q", "ls ", next.input)
	}
}

func TestEscapeClearsComposer(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.input = "tail -10 roadmap.md"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next := updated.(Model)

	if next.input != "" {
		t.Fatalf("expected cleared input, got %q", next.input)
	}
}

func TestShellSubmitUsesCurrentInputEachTime(t *testing.T) {
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
					Summary:  "file.txt",
				},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)

	model.input = "ls"
	updated, cmd := model.submit()
	next := updated.(Model)
	msg := cmd().(controllerEventsMsg)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	next.input = "ls -lah"
	updated, cmd = next.submit()
	_ = updated.(Model)
	_ = cmd().(controllerEventsMsg)

	if len(ctrl.shellCommands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(ctrl.shellCommands))
	}

	if ctrl.shellCommands[0] != "ls" {
		t.Fatalf("expected first command ls, got %q", ctrl.shellCommands[0])
	}

	if ctrl.shellCommands[1] != "ls -lah" {
		t.Fatalf("expected second command ls -lah, got %q", ctrl.shellCommands[1])
	}
}

func TestShellHistoryCyclesWithUpAndDown(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.input = "ls"
	updated, cmd := model.submit()
	model = updated.(Model)
	msg := cmd().(controllerEventsMsg)
	updated, _ = model.Update(msg)
	model = updated.(Model)
	model.input = "pwd"
	updated, cmd = model.submit()
	model = updated.(Model)
	msg = cmd().(controllerEventsMsg)
	updated, _ = model.Update(msg)
	model = updated.(Model)
	model.input = "draft"

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(Model)
	if model.input != "pwd" {
		t.Fatalf("expected most recent command, got %q", model.input)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(Model)
	if model.input != "ls" {
		t.Fatalf("expected older command, got %q", model.input)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.input != "pwd" {
		t.Fatalf("expected newer command, got %q", model.input)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.input != "draft" {
		t.Fatalf("expected draft restoration, got %q", model.input)
	}
}

func TestAgentAndShellHistoryStaySeparate(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.input = "ls"
	updated, cmd := model.submit()
	model = updated.(Model)
	msg := cmd().(controllerEventsMsg)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	model.input = "show plan"
	updated, cmd = model.submit()
	model = updated.(Model)
	msg = cmd().(controllerEventsMsg)
	updated, _ = model.Update(msg)
	model = updated.(Model)
	model.input = ""

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(Model)
	if model.input != "show plan" {
		t.Fatalf("expected agent history entry, got %q", model.input)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	model.input = ""
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(Model)
	if model.input != "ls" {
		t.Fatalf("expected shell history entry, got %q", model.input)
	}
}

func TestTranscriptScrollKeysMoveViewport(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 100
	model.height = 24
	model.entries = makeTranscriptEntries(24)

	maxScroll := model.maxTranscriptScroll()
	if maxScroll == 0 {
		t.Fatal("expected transcript overflow for scroll test")
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(Model)
	if model.transcriptFollow {
		t.Fatal("expected page up to disable transcript follow")
	}
	if model.transcriptScroll >= maxScroll {
		t.Fatalf("expected transcript to move above bottom, got %d with max %d", model.transcriptScroll, maxScroll)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnd})
	model = updated.(Model)
	if !model.transcriptFollow {
		t.Fatal("expected End to return transcript to follow mode")
	}
	if model.transcriptScroll != model.maxTranscriptScroll() {
		t.Fatalf("expected scroll at bottom, got %d", model.transcriptScroll)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyHome})
	model = updated.(Model)
	if model.transcriptScroll != 0 {
		t.Fatalf("expected Home to jump to top, got %d", model.transcriptScroll)
	}
}

func TestTranscriptPinnedStateControlsAutoFollow(t *testing.T) {
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
					Summary:  "file.txt",
				},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.width = 100
	model.height = 24
	model.entries = makeTranscriptEntries(24)

	model.scrollTranscriptToTop()
	model.input = "ls"
	updatedAny, cmd := model.submit()
	model = updatedAny.(Model)
	msg := cmd().(controllerEventsMsg)
	updatedAny, _ = model.Update(msg)
	model = updatedAny.(Model)
	if model.transcriptScroll != 0 {
		t.Fatalf("expected scrolled-up transcript to stay put, got %d", model.transcriptScroll)
	}

	model.scrollTranscriptToBottom()
	model.input = "pwd"
	updatedAny, cmd = model.submit()
	model = updatedAny.(Model)
	msg = cmd().(controllerEventsMsg)
	updatedAny, _ = model.Update(msg)
	model = updatedAny.(Model)
	if !model.transcriptFollow {
		t.Fatal("expected pinned transcript to stay in follow mode")
	}
	if model.transcriptScroll != model.maxTranscriptScroll() {
		t.Fatalf("expected transcript to stay at bottom, got %d", model.transcriptScroll)
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
	msg := cmd().(controllerEventsMsg)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if next.pendingProposal == nil || next.pendingProposal.Command != "ls -lah" {
		t.Fatalf("expected pending proposal command, got %#v", next.pendingProposal)
	}

	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	next = updated.(Model)
	msg = cmd().(controllerEventsMsg)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if len(ctrl.shellCommands) != 1 || ctrl.shellCommands[0] != "ls -lah" {
		t.Fatalf("expected proposal command to run, got %#v", ctrl.shellCommands)
	}

	if next.pendingProposal != nil {
		t.Fatalf("expected pending proposal to clear after execution, got %#v", next.pendingProposal)
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
	msg := cmd().(controllerEventsMsg)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if next.pendingApproval == nil || next.pendingApproval.ID != "approval-1" {
		t.Fatalf("expected pending approval, got %#v", next.pendingApproval)
	}

	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	next = updated.(Model)
	msg = cmd().(controllerEventsMsg)
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
	msg := cmd().(controllerEventsMsg)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	next = updated.(Model)
	msg = cmd().(controllerEventsMsg)
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
	msg = cmd().(controllerEventsMsg)
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

func fakeWorkspace() tmux.Workspace {
	return tmux.Workspace{
		SessionName: "shuttle-test",
		TopPane: tmux.Pane{
			ID: "%0",
		},
		BottomPane: tmux.Pane{
			ID: "%1",
		},
	}
}

func makeTranscriptEntries(count int) []Entry {
	entries := make([]Entry, 0, count)
	for index := 0; index < count; index++ {
		entries = append(entries, Entry{
			Title: "result",
			Body:  fmt.Sprintf("line %d", index),
		})
	}

	return entries
}

type fakeController struct {
	agentEvents    []controller.TranscriptEvent
	shellEvents    []controller.TranscriptEvent
	shellCommands  []string
	decisionEvents []controller.TranscriptEvent
	decisions      []approvalDecisionCall
	refinements    []refinementCall
}

func (f *fakeController) SubmitAgentPrompt(_ context.Context, _ string) ([]controller.TranscriptEvent, error) {
	return append([]controller.TranscriptEvent(nil), f.agentEvents...), nil
}

func (f *fakeController) SubmitRefinement(_ context.Context, approval controller.ApprovalRequest, note string) ([]controller.TranscriptEvent, error) {
	f.refinements = append(f.refinements, refinementCall{
		approval: approval,
		note:     note,
	})
	return append([]controller.TranscriptEvent(nil), f.agentEvents...), nil
}

func (f *fakeController) SubmitShellCommand(_ context.Context, command string) ([]controller.TranscriptEvent, error) {
	f.shellCommands = append(f.shellCommands, command)
	if len(f.shellEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind:    controller.EventCommandStart,
				Payload: controller.CommandStartPayload{Command: command},
			},
			{
				Kind: controller.EventCommandResult,
				Payload: controller.CommandResultSummary{
					Command:  command,
					ExitCode: 0,
					Summary:  command,
				},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.shellEvents...), nil
}

func (f *fakeController) DecideApproval(_ context.Context, approvalID string, decision controller.ApprovalDecision, refineText string) ([]controller.TranscriptEvent, error) {
	f.decisions = append(f.decisions, approvalDecisionCall{
		approvalID: approvalID,
		decision:   decision,
		refineText: refineText,
	})
	if len(f.decisionEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind:    controller.EventSystemNotice,
				Payload: controller.TextPayload{Text: "approval handled"},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.decisionEvents...), nil
}

type approvalDecisionCall struct {
	approvalID string
	decision   controller.ApprovalDecision
	refineText string
}

type refinementCall struct {
	approval controller.ApprovalRequest
	note     string
}

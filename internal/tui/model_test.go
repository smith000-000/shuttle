package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"aiterm/internal/controller"
	"aiterm/internal/shell"
	"aiterm/internal/tmux"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	msg := controllerEventsFromCmd(t, cmd)
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

func TestF2CancelsInFlightWorkAndStartsTakeControl(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)
	canceled := false
	model.busy = true
	model.approvalInFlight = true
	model.inFlightCancel = func() {
		canceled = true
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyF2})
	next := updated.(Model)

	if !canceled {
		t.Fatal("expected in-flight operation to be canceled")
	}
	if !next.suppressCancelErr {
		t.Fatal("expected canceled operation error to be suppressed")
	}
	if !next.resumeAfterHandoff {
		t.Fatal("expected handoff to resume the interrupted agent task")
	}
	if next.busy {
		t.Fatal("expected model to leave busy state during take control")
	}
	if cmd == nil {
		t.Fatal("expected take-control command")
	}
}

func TestCanceledControllerEventSuppressedAfterTakeControl(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.suppressCancelErr = true
	initialEntries := len(model.entries)

	updated, _ := model.Update(controllerEventsMsg{err: context.Canceled})
	next := updated.(Model)

	if len(next.entries) != initialEntries {
		t.Fatalf("expected canceled error to be suppressed, got %d entries", len(next.entries))
	}
	if next.suppressCancelErr {
		t.Fatal("expected cancel suppression flag to clear")
	}
}

func TestControllerErrorIncludesShellTail(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.liveShellTail = "[sudo] password for jsmith:"
	model.showShellTail = true

	updated, _ := model.Update(controllerEventsMsg{err: context.DeadlineExceeded})
	next := updated.(Model)

	last := next.entries[len(next.entries)-1]
	if last.Title != "error" {
		t.Fatalf("expected error entry, got %q", last.Title)
	}
	if !strings.Contains(last.Body, "context deadline exceeded") {
		t.Fatalf("expected error body to include deadline, got %q", last.Body)
	}
	if !strings.Contains(last.Body, "[sudo] password for jsmith:") {
		t.Fatalf("expected error body to include shell tail, got %q", last.Body)
	}
}

func TestAgentSubmitClearsShellTailPreview(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.mode = AgentMode
	model.input = "summarize this repo"
	model.showShellTail = true
	model.liveShellTail = "old shell output"

	updated, _ := model.submit()
	next := updated.(Model)

	if next.showShellTail {
		t.Fatal("expected agent submit to hide shell tail")
	}
	if next.liveShellTail != "" {
		t.Fatalf("expected agent submit to clear shell tail, got %q", next.liveShellTail)
	}
}

func TestShellSubmitEnablesShellTailPreview(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.input = "ls"

	updated, _ := model.submit()
	next := updated.(Model)

	if !next.showShellTail {
		t.Fatal("expected shell submit to enable shell tail")
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
	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	next.input = "ls -lah"
	updated, cmd = next.submit()
	_ = updated.(Model)
	_ = controllerEventsFromCmd(t, cmd)

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
	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)
	model.input = "pwd"
	updated, cmd = model.submit()
	model = updated.(Model)
	msg = controllerEventsFromCmd(t, cmd)
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
	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	model.input = "show plan"
	updated, cmd = model.submit()
	model = updated.(Model)
	msg = controllerEventsFromCmd(t, cmd)
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
	msg := controllerEventsFromCmd(t, cmd)
	updatedAny, _ = model.Update(msg)
	model = updatedAny.(Model)
	if model.transcriptScroll != 0 {
		t.Fatalf("expected scrolled-up transcript to stay put, got %d", model.transcriptScroll)
	}

	model.scrollTranscriptToBottom()
	model.input = "pwd"
	updatedAny, cmd = model.submit()
	model = updatedAny.(Model)
	msg = controllerEventsFromCmd(t, cmd)
	updatedAny, _ = model.Update(msg)
	model = updatedAny.(Model)
	if !model.transcriptFollow {
		t.Fatal("expected pinned transcript to stay in follow mode")
	}
	if model.transcriptScroll != model.maxTranscriptScroll() {
		t.Fatalf("expected transcript to stay at bottom, got %d", model.transcriptScroll)
	}
}

func TestMainViewDoesNotRenderHeaderOrSideRails(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 20

	view := model.View()
	if strings.Contains(view, "Shuttle") {
		t.Fatalf("expected header to be removed from main view, got %q", view)
	}
	if strings.Contains(view, "│") {
		t.Fatalf("expected no side rails in main view, got %q", view)
	}
}

func TestBusyStatusLineRendersAboveComposer(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 20
	model.busy = true
	model.busyStartedAt = time.Now()
	model.shellContext = shell.PromptContext{
		User:         "jsmith",
		Host:         "linuxdesktop",
		Directory:    "~/source/repos/aiterm",
		GitBranch:    "main",
		PromptSymbol: "%",
	}

	view := model.View()
	if !strings.Contains(view, "Working (") {
		t.Fatalf("expected busy status line, got %q", view)
	}
	if !strings.Contains(view, "jsmith@linuxdesktop ~/source/repos/aiterm git:(main) %") {
		t.Fatalf("expected shell context line, got %q", view)
	}
}

func TestRemoteShellContextRendersRemoteBadge(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 20
	model.shellContext = shell.PromptContext{
		User:         "root",
		Host:         "web01",
		Directory:    "/srv/app",
		PromptSymbol: "#",
		Root:         true,
		Remote:       true,
	}

	view := model.View()
	if !strings.Contains(view, "REMOTE") {
		t.Fatalf("expected remote badge, got %q", view)
	}
	if !strings.Contains(view, "root@web01 /srv/app #") {
		t.Fatalf("expected remote shell context line, got %q", view)
	}
}

func TestComposerPrefixUsesAgentAndRootPrompts(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 20

	model.mode = AgentMode
	model.input = ""
	view := model.View()
	if !strings.Contains(view, "Œ>") {
		t.Fatalf("expected agent prompt prefix, got %q", view)
	}

	model.mode = ShellMode
	model.shellContext = shell.PromptContext{Root: true}
	view = model.View()
	if !strings.Contains(view, "#>") {
		t.Fatalf("expected root shell prompt prefix, got %q", view)
	}
}

func TestTranscriptLinesWrapToViewportWidth(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.entries = []Entry{
		{
			Title: "agent",
			Body:  "This is a deliberately long line that should wrap inside a narrow transcript viewport instead of overflowing the screen.",
		},
	}

	lines := model.transcriptLines(32)
	if len(lines) < 3 {
		t.Fatalf("expected wrapped transcript lines, got %#v", lines)
	}
	for _, line := range lines {
		if lipgloss.Width(line) > 32 {
			t.Fatalf("expected wrapped line within viewport width, got %d for %q", lipgloss.Width(line), line)
		}
	}
}

func TestTranscriptEntrySelectionUsesAltArrows(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.entries = makeTranscriptEntries(5)
	model.selectedEntry = 4

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	model = updated.(Model)
	if model.selectedEntry != 3 {
		t.Fatalf("expected selected entry 3, got %d", model.selectedEntry)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown, Alt: true})
	model = updated.(Model)
	if model.selectedEntry != 4 {
		t.Fatalf("expected selected entry 4, got %d", model.selectedEntry)
	}
}

func TestDetailViewOpensAndCloses(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.entries = []Entry{
		{Title: "result", Body: "line 1\nline 2\nline 3"},
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(Model)
	if !model.detailOpen {
		t.Fatal("expected detail view to open")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.detailOpen {
		t.Fatal("expected detail view to close")
	}
}

func TestDetailViewScrolls(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.height = 20
	model.entries = []Entry{
		{Title: "result", Body: makeMultilineBody(20)},
	}
	model.selectedEntry = 0
	model.detailOpen = true

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	model = updated.(Model)
	if model.detailScroll == 0 {
		t.Fatal("expected detail scroll to move down")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyHome})
	model = updated.(Model)
	if model.detailScroll != 0 {
		t.Fatalf("expected detail scroll reset to 0, got %d", model.detailScroll)
	}
}

func TestCommandResultEntryIsCollapsedInTranscriptButPreservedInDetail(t *testing.T) {
	events := []controller.TranscriptEvent{
		{
			Kind: controller.EventCommandResult,
			Payload: controller.CommandResultSummary{
				Command:  "seq 1 10",
				ExitCode: 0,
				Summary:  makeMultilineBody(10),
			},
		},
	}

	entries := eventsToEntries(events, true)
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}

	if !strings.Contains(entries[0].Body, "... (4 more lines, Ctrl+O to inspect)") {
		t.Fatalf("expected collapsed preview, got %q", entries[0].Body)
	}

	if !strings.Contains(entries[0].Detail, "command:\nseq 1 10") {
		t.Fatalf("expected detail to retain command metadata, got %q", entries[0].Detail)
	}

	if !strings.Contains(entries[0].Detail, "line 9") {
		t.Fatalf("expected detail to retain full output, got %q", entries[0].Detail)
	}
}

func TestCommandResultEntryCanRenderExpandedForDirectShellUse(t *testing.T) {
	events := []controller.TranscriptEvent{
		{
			Kind: controller.EventCommandResult,
			Payload: controller.CommandResultSummary{
				Command:  "seq 1 10",
				ExitCode: 0,
				Summary:  makeMultilineBody(10),
			},
		},
	}

	entries := eventsToEntries(events, false)
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}

	if strings.Contains(entries[0].Body, "Ctrl+O to inspect") {
		t.Fatalf("expected expanded shell result without inspect hint, got %q", entries[0].Body)
	}

	if !strings.Contains(entries[0].Body, "line 9") {
		t.Fatalf("expected full shell result in transcript, got %q", entries[0].Body)
	}
}

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
}

func TestProposalEntryIsCollapsedInTranscriptButPreservedInDetail(t *testing.T) {
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: app.go",
		"+new line 1",
		"+new line 2",
		"+new line 3",
		"*** End Patch",
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

	if !strings.Contains(entries[0].Body, "patch attached (6 lines, Ctrl+O to inspect)") {
		t.Fatalf("expected collapsed proposal preview, got %q", entries[0].Body)
	}

	if !strings.Contains(entries[0].Detail, "kind: patch") || !strings.Contains(entries[0].Detail, "*** Begin Patch") {
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

	if !strings.Contains(last.Body, "Ctrl+O to inspect") {
		t.Fatalf("expected agent-triggered result to stay collapsed, got %q", last.Body)
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
	if nextCmd == nil {
		t.Fatal("expected shell-tail refresh command")
	}
}

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

func makeMultilineBody(count int) string {
	lines := make([]string, 0, count)
	for index := 0; index < count; index++ {
		lines = append(lines, fmt.Sprintf("line %d", index))
	}

	return strings.Join(lines, "\n")
}

type fakeController struct {
	agentEvents    []controller.TranscriptEvent
	continueEvents []controller.TranscriptEvent
	shellEvents    []controller.TranscriptEvent
	shellCommands  []string
	decisionEvents []controller.TranscriptEvent
	decisions      []approvalDecisionCall
	refinements    []refinementCall
	continueCalls  int
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

func (f *fakeController) ContinueAfterCommand(_ context.Context) ([]controller.TranscriptEvent, error) {
	f.continueCalls++
	if len(f.continueEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Reviewed the last command result."},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.continueEvents...), nil
}

func (f *fakeController) ResumeAfterTakeControl(_ context.Context) ([]controller.TranscriptEvent, error) {
	f.continueCalls++
	if len(f.continueEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Resuming after take control."},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.continueEvents...), nil
}

func (f *fakeController) ContinueActivePlan(_ context.Context) ([]controller.TranscriptEvent, error) {
	f.continueCalls++
	if len(f.continueEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Continuing the active plan."},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.continueEvents...), nil
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

func (f *fakeController) RefreshShellContext(_ context.Context) (*shell.PromptContext, error) {
	return &shell.PromptContext{
		User:      "jsmith",
		Host:      "linuxdesktop",
		Directory: "/home/jsmith/source/repos/aiterm",
	}, nil
}

func (f *fakeController) PeekShellTail(_ context.Context, _ int) (string, error) {
	return "waiting for input", nil
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

func controllerEventsFromCmd(t *testing.T, cmd tea.Cmd) controllerEventsMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}

	msg := cmd()
	switch typed := msg.(type) {
	case controllerEventsMsg:
		return typed
	case tea.BatchMsg:
		for _, candidate := range typed {
			if candidate == nil {
				continue
			}
			nested := candidate()
			if eventMsg, ok := nested.(controllerEventsMsg); ok {
				return eventMsg
			}
		}
		t.Fatalf("expected controllerEventsMsg in batch, got %#v", typed)
	default:
		t.Fatalf("expected controllerEventsMsg or tea.BatchMsg, got %T", msg)
	}

	return controllerEventsMsg{}
}

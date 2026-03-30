package tui

import (
	"aiterm/internal/controller"
	"aiterm/internal/provider"
	"aiterm/internal/shell"
	"context"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
	"time"
)

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

	if len(next.entries) < 3 {
		t.Fatalf("expected transcript entries, got %d", len(next.entries))
	}

	if next.entries[len(next.entries)-1].Title != "agent" {
		t.Fatalf("expected final entry to be agent, got %s", next.entries[len(next.entries)-1].Title)
	}
}

func TestSpaceKeyAddsSpaceToComposer(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.input = "ls"
	model.cursor = len([]rune(model.input))

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	next := updated.(Model)

	if next.input != "ls " {
		t.Fatalf("expected input %q, got %q", "ls ", next.input)
	}
}

func TestPasteStripsANSIFormattingFromComposerInput(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)

	updated, _ := model.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("\x1b[44mhello\x1b[0m\tworld\n\x1b]8;;https://example.com\x07link\x1b]8;;\x07"),
		Paste: true,
	})
	next := updated.(Model)

	if next.input != "hello\tworld\nlink" {
		t.Fatalf("expected pasted ANSI formatting to be stripped, got %q", next.input)
	}
}

func TestMultilineComposerRendersEveryLine(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.width = 80
	model.height = 24
	model.setInput("Hello\nmy\nname")

	rendered := model.renderComposer(80)
	lines := strings.Split(rendered, "\n")
	if len(lines) < 3 || !strings.Contains(lines[0], "Hello") || !strings.Contains(lines[1], "my") || !strings.Contains(lines[2], "name") {
		t.Fatalf("expected multiline composer content to render on separate lines, got %q", rendered)
	}
}

func TestComposerViewportClipsOldLinesAfterFifteenRows(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.width = 80
	model.height = 24

	lines := make([]string, 0, 18)
	for index := 1; index <= 18; index++ {
		lines = append(lines, fmt.Sprintf("line-%02d", index))
	}
	model.setInput(strings.Join(lines, "\n"))

	rendered := model.renderComposer(80)
	if strings.Contains(rendered, "line-01") || strings.Contains(rendered, "line-02") || strings.Contains(rendered, "line-03") {
		t.Fatalf("expected oldest lines to scroll off the top, got %q", rendered)
	}
	if !strings.Contains(rendered, "line-04") || !strings.Contains(rendered, "line-18") {
		t.Fatalf("expected last fifteen lines to remain visible, got %q", rendered)
	}
	if got := len(strings.Split(rendered, "\n")); got != composerMaxVisibleLines {
		t.Fatalf("expected %d visible composer lines, got %d", composerMaxVisibleLines, got)
	}
}

func TestCurrentProviderModelLabelIncludesPresetAndModel(t *testing.T) {
	label := currentProviderModelLabel(provider.Profile{
		Name:   "Codex CLI",
		Preset: provider.PresetCodexCLI,
		Model:  "gpt-5-codex",
	})

	if label != "Codex CLI (codex_cli) / gpt-5-codex" {
		t.Fatalf("unexpected label %q", label)
	}
}

func TestLeftRightMovesComposerCursor(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.setInput("abcd")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	model = updated.(Model)

	if model.input != "abXcd" {
		t.Fatalf("expected insertion at cursor, got %q", model.input)
	}
	if model.cursor != 3 {
		t.Fatalf("expected cursor after inserted rune, got %d", model.cursor)
	}
}

func TestUpDownMoveWithinMultilineComposer(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.setInput("alpha\nbeta\ngamma")
	model.cursor = 2

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.cursor != 8 {
		t.Fatalf("expected cursor to move to matching column on next line, got %d", model.cursor)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.cursor != 13 {
		t.Fatalf("expected cursor to move to matching column on third line, got %d", model.cursor)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(Model)
	if model.cursor != 8 {
		t.Fatalf("expected cursor to move back to prior line, got %d", model.cursor)
	}
}

func TestHomeEndMoveWithinComposerLine(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.setInput("alpha\nbeta\ngamma")
	model.cursor = 8

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyHome})
	model = updated.(Model)
	if model.cursor != 6 {
		t.Fatalf("expected Home to move to start of current line, got %d", model.cursor)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnd})
	model = updated.(Model)
	if model.cursor != 10 {
		t.Fatalf("expected End to move to end of current line, got %d", model.cursor)
	}
}

func TestInsertTogglesComposerOverwriteMode(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.setInput("abc")
	model.cursor = 1

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyInsert})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Z'}})
	model = updated.(Model)

	if !model.overwriteMode {
		t.Fatal("expected overwrite mode to remain enabled")
	}
	if model.input != "aZc" {
		t.Fatalf("expected overwrite insertion, got %q", model.input)
	}
}

func TestShellContextPollRefreshesRemotePromptHint(t *testing.T) {
	ctrl := &fakeController{
		refreshedShellContext: &shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "~",
			PromptSymbol: "$",
			RawLine:      "openclaw@openclaw ~ $",
			Remote:       true,
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)

	updated, cmd := model.Update(shellContextPollTickMsg(time.Now()))
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected shell-context poll command")
	}
	msg := cmd()
	var refreshMsg refreshedShellContextMsg
	switch typed := msg.(type) {
	case refreshedShellContextMsg:
		refreshMsg = typed
	case tea.BatchMsg:
		found := false
		for _, nested := range typed {
			if nested == nil {
				continue
			}
			if candidate, ok := nested().(refreshedShellContextMsg); ok {
				refreshMsg = candidate
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected refreshedShellContextMsg in batch, got %#v", typed)
		}
	default:
		t.Fatalf("expected refreshedShellContextMsg, got %T", msg)
	}
	updated, _ = model.Update(refreshMsg)
	model = updated.(Model)

	if !model.shellContext.Remote || model.shellContext.Host != "openclaw" {
		t.Fatalf("expected refreshed remote shell context, got %#v", model.shellContext)
	}
	if ctrl.refreshShellContextCalls == 0 {
		t.Fatal("expected refresh shell context call")
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

func TestEscapeCancelsBusyAgentTurn(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	canceled := false
	model.busy = true
	model.inFlightCancel = func() {
		canceled = true
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next := updated.(Model)

	if !canceled {
		t.Fatal("expected escape to cancel in-flight work")
	}
	if next.busy {
		t.Fatal("expected busy state to clear after escape interrupt")
	}
}

func TestUppercaseEEntersProposalCommandEditMode(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalCommand,
		Command:     "grep -R foo .",
		Description: "Search current tree.",
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	next := updated.(Model)

	if next.editingProposal == nil {
		t.Fatal("expected uppercase E to enter proposal edit mode")
	}
	if next.input != "grep -R foo ." {
		t.Fatalf("expected proposal command in composer, got %q", next.input)
	}
}

func TestLowercaseKDuringActiveExecutionDoesNotInterrupt(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.mode = AgentMode
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 30",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionRunning,
		StartedAt: time.Now(),
	}
	interrupted := false
	model.inFlightCancel = func() {
		interrupted = true
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	next := updated.(Model)

	if interrupted {
		t.Fatal("expected lowercase k to remain normal input")
	}
	if next.input != "k" {
		t.Fatalf("expected input %q, got %q", "k", next.input)
	}
}

func TestUppercaseNRejectsProposal(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalCommand,
		Command:     "sleep 5",
		Description: "Run a short sleep.",
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	next := updated.(Model)

	if next.pendingProposal != nil {
		t.Fatalf("expected proposal to be dismissed, got %#v", next.pendingProposal)
	}
}

func TestLowercaseYRunsPendingProposal(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalCommand,
		Command:     "ls -lah",
		Description: "Inspect files.",
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	next := updated.(Model)

	if cmd == nil {
		t.Fatal("expected proposal action command for lowercase y")
	}
	if !next.busy {
		t.Fatal("expected model to enter busy state after lowercase y")
	}
}

func TestComposerIsLockedWhileProposalCardIsActive(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalCommand,
		Command:     "ls -lah",
		Description: "Inspect files.",
	}
	model.setInput("draft")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	next := updated.(Model)

	if next.input != "draft" {
		t.Fatalf("expected composer input to remain locked, got %q", next.input)
	}
}

func TestAwaitingInputAllowsSendKeysMode(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   `bash -lc 'read -n 1 -s -r -p "Press any key" _'`,
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionAwaitingInput,
		StartedAt: time.Now(),
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	next := updated.(Model)

	if !next.sendingFullscreenKeys {
		t.Fatal("expected send-keys mode to activate for awaiting_input")
	}
}

func TestSendKeysModeBypassesComposerLockOnEnter(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.sendingFullscreenKeys = true
	model.pendingApproval = &controller.ApprovalRequest{
		ID:      "approval-1",
		Kind:    controller.ApprovalCommand,
		Title:   "Approve",
		Summary: "Run interactive command",
		Command: "read",
	}
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "read",
		Origin:    controller.CommandOriginAgentApproval,
		State:     controller.CommandExecutionAwaitingInput,
		StartedAt: time.Now(),
	}
	model.setInput("hello")
	model.takeControl = takeControlConfig{SocketName: "sock", SessionName: "sess", TrackedPaneID: "%0", DetachKey: TakeControlKey}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(Model)

	if cmd == nil {
		t.Fatal("expected send-keys command even while composer is otherwise locked")
	}
	if next.sendingFullscreenKeys {
		t.Fatal("expected send-keys mode to submit and exit")
	}
}

func TestAgentSubmitAllowedDuringActiveExecution(t *testing.T) {
	ctrl := &fakeController{
		agentEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Press F2 or use KEYS>."},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.busy = true
	model.showShellTail = true
	model.liveShellTail = "Press any key"
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "read",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionAwaitingInput,
		StartedAt: time.Now(),
	}
	model.setInput("what should I do now?")

	updated, cmd := model.submit()
	next := updated.(Model)

	if cmd == nil {
		t.Fatal("expected agent submit command during active execution")
	}
	if next.activeExecution == nil || next.activeExecution.ID != "cmd-1" {
		t.Fatalf("expected active execution to remain visible, got %#v", next.activeExecution)
	}
	if !next.showShellTail {
		t.Fatal("expected shell tail visibility to be preserved during recovery prompt")
	}
}

func TestRemoteManualInterruptNoticeIsNotDuplicated(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithTakeControl("sock", "sess", "%0", TakeControlKey)
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-remote",
		Command:   "sleep 60",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionRunning,
		StartedAt: time.Now(),
		ShellContextAfter: &shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "~",
			PromptSymbol: "$",
			Remote:       true,
		},
	}

	initial := len(model.entries)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	afterFirst := len(model.entries)
	if afterFirst != initial+1 {
		t.Fatalf("expected one interrupt notice, entries=%d -> %d", initial, afterFirst)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if len(model.entries) != afterFirst {
		t.Fatalf("expected duplicate interrupt notice to be suppressed, got %d entries", len(model.entries))
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
	if len(next.entries) != len(model.entries) {
		t.Fatalf("expected take-control not to append transcript noise, got %d entries", len(next.entries))
	}
}

func TestF2DoesNotCancelActiveShellExecution(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)
	canceled := false
	model.busy = true
	model.directShellPending = true
	model.showShellTail = true
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 15",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionRunning,
		StartedAt: time.Now(),
	}
	model.inFlightCancel = func() {
		canceled = true
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyF2})
	next := updated.(Model)

	if canceled {
		t.Fatal("expected active shell execution to keep running during handoff")
	}
	if next.activeExecution == nil || next.activeExecution.State != controller.CommandExecutionHandoffActive {
		t.Fatalf("expected handoff-active execution, got %#v", next.activeExecution)
	}
	if !next.directShellPending {
		t.Fatal("expected direct shell pending to remain set")
	}
}

func TestF2DoesNotMarkOwnedExecutionAsHandoff(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 15",
		Origin:    controller.CommandOriginAgentProposal,
		State:     controller.CommandExecutionRunning,
		StartedAt: time.Now(),
		TrackedShell: controller.TrackedShellTarget{
			SessionName: "shuttle-test",
			PaneID:      "%9",
		},
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyF2})
	next := updated.(Model)

	if next.activeExecution == nil || next.activeExecution.State != controller.CommandExecutionRunning {
		t.Fatalf("expected owned execution to remain running, got %#v", next.activeExecution)
	}
	if next.handoffVisible {
		t.Fatal("expected owned execution not to enter handoff-visible state")
	}
}

func TestF2MarksOwnedInteractiveExecutionAsHandoff(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "sudo apt update",
		Origin:    controller.CommandOriginAgentProposal,
		State:     controller.CommandExecutionAwaitingInput,
		StartedAt: time.Now(),
		TrackedShell: controller.TrackedShellTarget{
			SessionName: "shuttle-test",
			PaneID:      "%9",
		},
	}
	model.takeControl.TrackedPaneID = "%9"
	model.takeControl.TemporaryPane = true

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyF2})
	next := updated.(Model)

	if next.activeExecution == nil || next.activeExecution.State != controller.CommandExecutionHandoffActive {
		t.Fatalf("expected owned interactive execution to enter handoff, got %#v", next.activeExecution)
	}
	if !next.handoffVisible {
		t.Fatal("expected owned interactive execution to show handoff state")
	}
}

func TestTakeControlFinishedSyncsTrackedTopPane(t *testing.T) {
	ctrl := &fakeController{sessionName: "shuttle-recovered", trackedPaneID: "%7"}
	model := NewModel(fakeWorkspace(), ctrl).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)

	updated, _ := model.Update(takeControlFinishedMsg{})
	next := updated.(Model)

	if next.workspace.SessionName != "shuttle-recovered" {
		t.Fatalf("expected workspace session shuttle-recovered, got %q", next.workspace.SessionName)
	}
	if next.workspace.TopPane.ID != "%7" {
		t.Fatalf("expected workspace top pane %%7, got %q", next.workspace.TopPane.ID)
	}
	if next.takeControl.SessionName != "shuttle-recovered" {
		t.Fatalf("expected take-control session shuttle-recovered, got %q", next.takeControl.SessionName)
	}
	if next.takeControl.TrackedPaneID != "%7" {
		t.Fatalf("expected take-control tracked pane %%7, got %q", next.takeControl.TrackedPaneID)
	}
}

func TestTakeControlFinishedResumesControllerWithoutLocalExecution(t *testing.T) {
	activeExecution := &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 20",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionRunning,
		StartedAt: time.Now(),
	}
	ctrl := &fakeController{
		continueEvents: []controller.TranscriptEvent{
			{
				Kind: controller.EventCommandStart,
				Payload: controller.CommandStartPayload{
					Command:   "sleep 20",
					Execution: *activeExecution,
				},
			},
		},
		activeExecution: activeExecution,
	}
	model := NewModel(fakeWorkspace(), ctrl).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)

	updated, cmd := model.Update(takeControlFinishedMsg{})
	next := updated.(Model)

	msg := controllerEventsFromCmd(t, cmd)
	updated, _ = next.Update(msg)
	next = updated.(Model)

	if ctrl.continueCalls != 1 {
		t.Fatalf("expected ResumeAfterTakeControl to be called once, got %d", ctrl.continueCalls)
	}
	if next.activeExecution == nil || next.activeExecution.Command != "sleep 20" {
		t.Fatalf("expected returned handoff command to become active, got %#v", next.activeExecution)
	}
}

func TestActiveExecutionReattachPreservesObservedStartTimeAcrossHandoff(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)
	startedAt := time.Now().Add(-12 * time.Second)
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionHandoffActive,
		StartedAt: startedAt,
		TrackedShell: controller.TrackedShellTarget{
			SessionName: "shuttle-test",
			PaneID:      "%0",
		},
		OwnershipMode: controller.CommandOwnershipSharedObserver,
	}
	model.handoffVisible = true

	updated, _ := model.Update(activeExecutionMsg{
		execution: &controller.CommandExecution{
			ID:        "cmd-2",
			Command:   "sleep",
			Origin:    controller.CommandOriginUserShell,
			State:     controller.CommandExecutionRunning,
			StartedAt: time.Now(),
			TrackedShell: controller.TrackedShellTarget{
				SessionName: "shuttle-test",
				PaneID:      "%0",
			},
			OwnershipMode: controller.CommandOwnershipSharedObserver,
		},
	})
	next := updated.(Model)

	if next.activeExecution == nil {
		t.Fatal("expected active execution to remain visible")
	}
	if !next.activeExecution.StartedAt.Equal(startedAt) {
		t.Fatalf("expected preserved startedAt %v, got %v", startedAt, next.activeExecution.StartedAt)
	}
}

func TestRefreshedShellContextSyncsTrackedTopPane(t *testing.T) {
	ctrl := &fakeController{trackedPaneID: "%8"}
	model := NewModel(fakeWorkspace(), ctrl).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)

	updated, _ := model.Update(refreshedShellContextMsg{context: &shell.PromptContext{Directory: "/tmp"}})
	next := updated.(Model)

	if next.workspace.TopPane.ID != "%8" {
		t.Fatalf("expected workspace top pane %%8, got %q", next.workspace.TopPane.ID)
	}
	if next.takeControl.TrackedPaneID != "%8" {
		t.Fatalf("expected take-control tracked pane %%8, got %q", next.takeControl.TrackedPaneID)
	}
}

func TestShellInterruptClearsControllerActiveExecution(t *testing.T) {
	ctrl := &fakeController{
		activeExecution: &controller.CommandExecution{
			ID:        "cmd-1",
			Command:   "sleep 60",
			Origin:    controller.CommandOriginUserShell,
			State:     controller.CommandExecutionRunning,
			StartedAt: time.Now(),
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.activeExecution = ctrl.activeExecution

	updated, _ := model.Update(shellInterruptMsg{})
	next := updated.(Model)

	if ctrl.activeExecution != nil {
		t.Fatal("expected controller active execution to clear")
	}
	if next.activeExecution != nil {
		t.Fatal("expected model active execution to clear")
	}
	if next.showShellTail {
		t.Fatal("expected shell tail to hide after interrupt")
	}
	last := next.entries[len(next.entries)-1]
	if last.Title != "result" || !strings.Contains(last.Body, "status=canceled") {
		t.Fatalf("expected canceled result entry, got %#v", last)
	}
}

func TestInterruptInFlightRefusesRemoteExecution(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)
	model.activeExecution = &controller.CommandExecution{
		ID:      "cmd-1",
		Command: "ssh host 'btop'",
		Origin:  controller.CommandOriginUserShell,
		State:   controller.CommandExecutionInteractiveFullscreen,
		ShellContextBefore: &shell.PromptContext{
			User:         "root",
			Host:         "remote",
			Directory:    "/root",
			PromptSymbol: "#",
			Remote:       true,
			RawLine:      "root@remote:~#",
		},
		StartedAt: time.Now(),
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next := updated.(Model)

	if cmd != nil {
		t.Fatal("expected no interrupt command for remote execution")
	}
	last := next.entries[len(next.entries)-1]
	if last.Title != "system" || !strings.Contains(last.Body, "Fullscreen app is still active") {
		t.Fatalf("expected manual interrupt notice, got %#v", last)
	}
}

func TestRenderShellTailHiddenForInteractiveFullscreen(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.showShellTail = true
	model.liveShellTail = "hidden tail"
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "btop",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionInteractiveFullscreen,
		StartedAt: time.Now(),
	}

	if got := model.renderShellTail(80); got != "" {
		t.Fatalf("expected fullscreen execution to hide tail, got %q", got)
	}
}

func TestCanAttemptLocalInterruptPrefersCurrentRemoteShellContext(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.shellContext = shell.PromptContext{
		User:         "openclaw",
		Host:         "openclaw",
		Directory:    "~",
		PromptSymbol: "$",
		Remote:       true,
		RawLine:      "openclaw@openclaw ~ $",
	}
	model.activeExecution = &controller.CommandExecution{
		ID:      "cmd-1",
		Command: "nano SYSTEM_TWEAKS.md",
		Origin:  controller.CommandOriginUserShell,
		State:   controller.CommandExecutionInteractiveFullscreen,
		ShellContextBefore: &shell.PromptContext{
			User:         "localuser",
			Host:         "workstation",
			Directory:    "/workspace/project",
			PromptSymbol: "%",
			RawLine:      "localuser@workstation ~/workspace/project %",
		},
	}

	if model.canAttemptLocalInterrupt() {
		t.Fatal("expected remote current shell context to suppress local interrupt")
	}
}

func TestCanAttemptLocalInterruptRemoteEvidenceWinsOverStaleLocalContext(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.activeExecution = &controller.CommandExecution{
		ID:      "cmd-1",
		Command: "nano SYSTEM_TWEAKS.md",
		Origin:  controller.CommandOriginUserShell,
		State:   controller.CommandExecutionInteractiveFullscreen,
		ShellContextBefore: &shell.PromptContext{
			User:         "localuser",
			Host:         "workstation",
			Directory:    "/workspace/project",
			PromptSymbol: "%",
			RawLine:      "localuser@workstation ~/workspace/project %",
		},
		ShellContextAfter: &shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "~",
			PromptSymbol: "$",
			Remote:       true,
			RawLine:      "openclaw@openclaw ~ $",
		},
	}

	if model.canAttemptLocalInterrupt() {
		t.Fatal("expected remote execution context to suppress local interrupt")
	}
}

func TestActiveExecutionCardShowsFullscreenKeyHintsWithoutKill(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.shellContext = shell.PromptContext{
		User:         "localuser",
		Host:         "workstation",
		Directory:    "/workspace/project",
		PromptSymbol: "%",
		RawLine:      "localuser@workstation ~/workspace/project %",
	}
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "wrapped-btop",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionInteractiveFullscreen,
		StartedAt: time.Now(),
	}

	card := model.renderActiveExecutionCard(100)
	if !strings.Contains(card, "Fullscreen terminal app detected.") {
		t.Fatalf("expected fullscreen notice, got %q", card)
	}
	if !strings.Contains(card, "F2 take control  S send keys") {
		t.Fatalf("expected fullscreen key hints, got %q", card)
	}
	if strings.Contains(card, "K attempt local interrupt") {
		t.Fatalf("expected fullscreen card to suppress dangerous kill hint, got %q", card)
	}
}

func TestActiveExecutionCardExplainsOwnedExecutionPane(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 20",
		Origin:    controller.CommandOriginAgentProposal,
		State:     controller.CommandExecutionRunning,
		StartedAt: time.Now(),
		TrackedShell: controller.TrackedShellTarget{
			SessionName: "shuttle-test",
			PaneID:      "%9",
		},
		LatestOutputTail: "still running",
	}

	card := model.renderActiveExecutionCard(100)
	if !strings.Contains(card, "owned execution pane") {
		t.Fatalf("expected owned execution explanation, got %q", card)
	}
	if strings.Contains(card, "F2 take control") {
		t.Fatalf("expected owned execution card not to advertise F2 takeover, got %q", card)
	}
}

func TestActiveExecutionCardOffersTakeControlForOwnedInteractivePane(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "sudo apt update",
		Origin:    controller.CommandOriginAgentProposal,
		State:     controller.CommandExecutionAwaitingInput,
		StartedAt: time.Now(),
		TrackedShell: controller.TrackedShellTarget{
			SessionName: "shuttle-test",
			PaneID:      "%9",
		},
		LatestOutputTail: "[sudo] password for localuser:",
	}
	model.takeControl.TrackedPaneID = "%9"
	model.takeControl.TemporaryPane = true

	card := model.renderActiveExecutionCard(100)
	if !strings.Contains(card, "F2 take control") {
		t.Fatalf("expected owned interactive card to advertise F2 takeover, got %q", card)
	}
	if !strings.Contains(card, "Temporary Shuttle execution pane") {
		t.Fatalf("expected owned interactive card to explain temporary pane, got %q", card)
	}
}

func TestSubmitShellCommandWhileFullscreenPromptsForConfirmation(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = ShellMode
	model.input = "ls"
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "nano file.txt",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionInteractiveFullscreen,
		StartedAt: time.Now(),
	}

	updated, cmd := model.submit()
	next := updated.(Model)

	if cmd != nil {
		t.Fatal("expected fullscreen submit to stop for confirmation")
	}
	if next.pendingFullscreen == nil || next.pendingFullscreen.Command != "ls" {
		t.Fatalf("expected pending fullscreen confirmation, got %#v", next.pendingFullscreen)
	}
	if len(ctrl.shellCommands) != 0 {
		t.Fatalf("expected no shell command dispatch before confirmation, got %#v", ctrl.shellCommands)
	}
}

func TestFullscreenConfirmationYRunsShellCommand(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.pendingFullscreen = &fullscreenAction{
		Kind:    fullscreenActionShellSubmit,
		Command: "ls",
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected confirmation to run shell command")
	}

	_ = controllerEventsFromCmd(t, cmd)
	if len(ctrl.shellCommands) != 1 || ctrl.shellCommands[0] != "ls" {
		t.Fatalf("expected confirmed shell command to run, got %#v", ctrl.shellCommands)
	}
	if next.pendingFullscreen != nil {
		t.Fatalf("expected fullscreen confirmation to clear, got %#v", next.pendingFullscreen)
	}
}

func TestSStartsFullscreenKeyMode(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "btop",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionInteractiveFullscreen,
		StartedAt: time.Now(),
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	next := updated.(Model)

	if !next.sendingFullscreenKeys {
		t.Fatal("expected fullscreen key mode to start")
	}
}

func TestSubmitFullscreenKeysBypassesShellCommandPath(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)
	model.sendingFullscreenKeys = true
	model.input = "q"

	updated, cmd := model.submit()
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected fullscreen key send command")
	}
	if next.sendingFullscreenKeys {
		t.Fatal("expected fullscreen key mode to clear after submit")
	}
	if len(ctrl.shellCommands) != 0 {
		t.Fatalf("expected fullscreen keys to bypass shell submit, got %#v", ctrl.shellCommands)
	}
}

func TestCarriageReturnRuneSubmitsFullscreenKeys(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)
	model.sendingFullscreenKeys = true
	model.input = "hello"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\r'}})
	next := updated.(Model)

	if cmd == nil {
		t.Fatal("expected carriage return rune to submit fullscreen keys")
	}
	if next.sendingFullscreenKeys {
		t.Fatal("expected fullscreen key mode to clear after carriage return submit")
	}
}

func TestEnterSubmitsFullscreenKeysWhileBusy(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)
	model.busy = true
	model.sendingFullscreenKeys = true
	model.input = "hello"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(Model)

	if cmd == nil {
		t.Fatal("expected Enter to submit fullscreen keys while busy")
	}
	if next.sendingFullscreenKeys {
		t.Fatal("expected fullscreen key mode to clear after busy Enter submit")
	}
}

func TestFullscreenKeysForSubmitPreservesExactSendByDefault(t *testing.T) {
	if got := fullscreenKeysForSubmit("password", false); got != "password" {
		t.Fatalf("expected exact submit keys, got %q", got)
	}
	if got := fullscreenKeysForSubmit("password", true); got != "password\n" {
		t.Fatalf("expected optional trailing enter, got %q", got)
	}
	if got := fullscreenKeysForSubmit("line1\nline2", false); got != "line1\nline2" {
		t.Fatalf("expected explicit multiline keys to remain unchanged, got %q", got)
	}
}

func TestCtrlYSubmitsFullscreenKeysWithTrailingEnter(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)
	model.sendingFullscreenKeys = true
	model.input = "password"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	next := updated.(Model)

	if cmd == nil {
		t.Fatal("expected Ctrl+Y to submit fullscreen keys")
	}
	if next.sendingFullscreenKeys {
		t.Fatal("expected fullscreen key mode to clear after Ctrl+Y submit")
	}
}

func TestFullscreenKeysFooterShowsSendHints(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.sendingFullscreenKeys = true

	footer := strings.Join(model.footerParts(120), "  ")
	for _, fragment := range []string{"[Enter] send exact", "[Ctrl+Y] send + Enter", "[Ctrl+J] insert Enter"} {
		if !strings.Contains(footer, fragment) {
			t.Fatalf("expected fullscreen keys footer to contain %q, got %q", fragment, footer)
		}
	}
}

func TestFullscreenKeysSentMessageUpdatesActiveCardFeedback(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "nano foo.txt",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionInteractiveFullscreen,
		StartedAt: time.Now(),
	}

	updated, _ := model.Update(fullscreenKeysSentMsg{keys: "hello"})
	next := updated.(Model)

	if next.lastFullscreenKeys != "hello" {
		t.Fatalf("expected last fullscreen keys to be recorded, got %q", next.lastFullscreenKeys)
	}
	card := next.renderActiveExecutionCard(100)
	if !strings.Contains(card, "last keys: hello") {
		t.Fatalf("expected active execution card to show last keys, got %q", card)
	}
}

func TestSyncActiveExecutionClearsFullscreenKeysWhenExecutionEnds(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.lastFullscreenKeys = "hello"
	model.lastFullscreenKeysAt = time.Now()
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "nano foo.txt",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionInteractiveFullscreen,
		StartedAt: time.Now(),
	}

	model.syncActiveExecution(nil)

	if model.lastFullscreenKeys != "" {
		t.Fatalf("expected fullscreen key preview to clear, got %q", model.lastFullscreenKeys)
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
	model.liveShellTail = "[sudo] password for localuser:"
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
	if !strings.Contains(last.Body, "[sudo] password for localuser:") {
		t.Fatalf("expected error body to include shell tail, got %q", last.Body)
	}
}

func TestTakeControlFinishedRestartsTickingForActiveExecution(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{
		activeExecution: &controller.CommandExecution{
			ID:        "cmd-1",
			Command:   "sleep 15",
			Origin:    controller.CommandOriginUserShell,
			State:     controller.CommandExecutionRunning,
			StartedAt: time.Now(),
		},
	})
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 15",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionHandoffActive,
		StartedAt: time.Now(),
	}
	model.handoffVisible = true
	model.handoffPriorState = controller.CommandExecutionRunning

	updated, cmd := model.Update(takeControlFinishedMsg{})
	next := updated.(Model)

	if next.activeExecution == nil {
		t.Fatal("expected active execution to remain after detach")
	}
	if next.activeExecution.State != controller.CommandExecutionRunning {
		t.Fatalf("expected handoff state to restore to running, got %#v", next.activeExecution)
	}
	if next.handoffVisible {
		t.Fatal("expected handoff display state to clear after detach")
	}
	if cmd == nil {
		t.Fatal("expected follow-up commands after detach")
	}
}

func TestTakeControlFinishedRestoresMouseTracking(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.handoffVisible = true

	_, cmd := model.Update(takeControlFinishedMsg{})

	if !cmdContainsMessageType(t, cmd, tea.EnableMouseCellMotion()) {
		t.Fatal("expected take-control return to re-enable mouse tracking")
	}
}

func TestTakeControlFinishedPreservesExecutionAcrossTransientNilPoll(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl).WithTakeControl("shuttle-test", "shuttle-test", "%0", TakeControlKey)
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 15",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionHandoffActive,
		StartedAt: time.Now(),
	}
	model.handoffVisible = true
	model.handoffPriorState = controller.CommandExecutionRunning

	updated, _ := model.Update(takeControlFinishedMsg{})
	model = updated.(Model)

	updated, cmd := model.Update(activeExecutionMsg{execution: nil})
	next := updated.(Model)

	if next.activeExecution == nil {
		t.Fatal("expected transient nil poll after handoff to preserve active execution")
	}
	if next.activeExecution.State != controller.CommandExecutionRunning {
		t.Fatalf("expected running execution to remain visible, got %#v", next.activeExecution)
	}
	if cmd == nil {
		t.Fatal("expected a follow-up execution poll after preserving active execution")
	}
}

func TestActiveExecutionNilPollRequiresConfirmationBeforeClear(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 15",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionRunning,
		StartedAt: time.Now(),
	}

	updated, cmd := model.Update(activeExecutionMsg{execution: nil})
	next := updated.(Model)

	if next.activeExecution == nil {
		t.Fatal("expected first nil execution poll to preserve active execution pending confirmation")
	}
	if next.activeExecutionMissingSince.IsZero() {
		t.Fatal("expected missing-execution confirmation window to start")
	}
	if cmd == nil {
		t.Fatal("expected follow-up poll while confirming missing execution")
	}
}

func TestActiveExecutionClearsAfterConfirmedMissingPolls(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 15",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionRunning,
		StartedAt: time.Now(),
	}
	model.activeExecutionMissingSince = time.Now().Add(-4 * time.Second)

	updated, _ := model.Update(activeExecutionMsg{execution: nil})
	next := updated.(Model)

	if next.activeExecution != nil {
		t.Fatalf("expected confirmed missing execution to clear, got %#v", next.activeExecution)
	}
}

func TestMaybeExecutionCheckInCmdChecksInForAgentOwnedExecution(t *testing.T) {
	ctrl := &fakeController{
		checkInEvents: []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Still running."},
			},
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-agent",
		Command:   "sleep 60",
		Origin:    controller.CommandOriginAgentProposal,
		State:     controller.CommandExecutionRunning,
		StartedAt: time.Now().Add(-15 * time.Second),
	}

	cmd := model.maybeExecutionCheckInCmd(time.Now())
	if cmd == nil {
		t.Fatal("expected check-in command for long-running agent execution")
	}
	if !model.checkInInFlight {
		t.Fatal("expected check-in to be marked in flight")
	}

	raw := cmd()
	msg, ok := raw.(activeExecutionCheckInMsg)
	if !ok {
		t.Fatalf("expected activeExecutionCheckInMsg, got %T", raw)
	}
	if msg.executionID != "cmd-agent" {
		t.Fatalf("expected execution id cmd-agent, got %q", msg.executionID)
	}
	if ctrl.checkInCalls != 1 {
		t.Fatalf("expected 1 check-in call, got %d", ctrl.checkInCalls)
	}
}

func TestMaybeExecutionCheckInCmdSkipsUserShellExecution(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-user",
		Command:   "sleep 60",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionRunning,
		StartedAt: time.Now().Add(-15 * time.Second),
	}

	cmd := model.maybeExecutionCheckInCmd(time.Now())
	if cmd != nil {
		t.Fatal("expected no check-in command for user-shell execution")
	}
	if ctrl.checkInCalls != 0 {
		t.Fatalf("expected no check-in calls, got %d", ctrl.checkInCalls)
	}
}

func TestActiveExecutionCheckInMsgAppendsTranscriptWithoutClearingExecution(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-agent",
		Command:   "sleep 60",
		Origin:    controller.CommandOriginAgentProposal,
		State:     controller.CommandExecutionBackgroundMonitor,
		StartedAt: time.Now().Add(-20 * time.Second),
	}
	model.checkInInFlight = true
	initialEntries := len(model.entries)

	updated, _ := model.Update(activeExecutionCheckInMsg{
		executionID: "cmd-agent",
		events: []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Still running."},
			},
		},
	})
	next := updated.(Model)

	if next.activeExecution == nil {
		t.Fatal("expected active execution to remain visible after check-in")
	}
	if next.checkInInFlight {
		t.Fatal("expected check-in in-flight flag to clear")
	}
	if len(next.entries) != initialEntries+1 {
		t.Fatalf("expected one new transcript entry, got %d", len(next.entries)-initialEntries)
	}
	if next.entries[len(next.entries)-1].Title != "agent" {
		t.Fatalf("expected agent entry, got %q", next.entries[len(next.entries)-1].Title)
	}
}

func TestInteractiveCheckInPausesAfterRetryLimit(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-agent",
		Command:   "sudo apt update",
		Origin:    controller.CommandOriginAgentProposal,
		State:     controller.CommandExecutionAwaitingInput,
		StartedAt: time.Now().Add(-2 * time.Minute),
	}
	model.checkInInFlight = true
	model.interactiveCheckInCount = maxInteractiveCheckIns - 1

	updated, _ := model.Update(activeExecutionCheckInMsg{
		executionID: "cmd-agent",
		events: []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Still waiting for the sudo password."},
			},
		},
	})
	next := updated.(Model)

	if !next.interactiveCheckInPaused {
		t.Fatal("expected interactive check-ins to pause after retry limit")
	}
	last := next.entries[len(next.entries)-1]
	if last.Title != "system" || !strings.Contains(last.Body, "Paused automatic agent check-ins") {
		t.Fatalf("expected pause notice, got %#v", last)
	}
}

func TestCtrlGResumesPausedInteractiveCheckIns(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-agent",
		Command:   "sudo apt update",
		Origin:    controller.CommandOriginAgentProposal,
		State:     controller.CommandExecutionAwaitingInput,
		StartedAt: time.Now().Add(-2 * time.Minute),
	}
	model.interactiveCheckInPaused = true
	model.interactiveCheckInCount = maxInteractiveCheckIns
	model.lastCheckInAt = time.Now()

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	next := updated.(Model)

	if next.interactiveCheckInPaused {
		t.Fatal("expected Ctrl+G to resume paused interactive check-ins")
	}
	if next.interactiveCheckInCount != 0 {
		t.Fatalf("expected paused interactive retry count to reset, got %d", next.interactiveCheckInCount)
	}
	if cmd == nil {
		t.Fatal("expected Ctrl+G resume to schedule follow-up work")
	}
}

func TestInteractivePauseActionRFocusesAgentComposer(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.mode = ShellMode
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-agent",
		Command:   "sudo apt update",
		Origin:    controller.CommandOriginAgentProposal,
		State:     controller.CommandExecutionAwaitingInput,
		StartedAt: time.Now().Add(-2 * time.Minute),
	}
	model.interactiveCheckInPaused = true

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	next := updated.(Model)

	if next.mode != AgentMode {
		t.Fatalf("expected R to focus the agent composer, got mode %s", next.mode)
	}
}

func TestParseFullscreenKeyEventsSupportsControlTokens(t *testing.T) {
	events := parseFullscreenKeyEvents("password<Ctrl+C><Esc>\n")
	if len(events) != 4 {
		t.Fatalf("expected 4 key events, got %#v", events)
	}
	if events[0].literal != "password" {
		t.Fatalf("expected literal password segment, got %#v", events[0])
	}
	if events[1].tmuxKey != "C-c" {
		t.Fatalf("expected Ctrl+C token to map to tmux key, got %#v", events[1])
	}
	if events[2].tmuxKey != "Escape" {
		t.Fatalf("expected Esc token to map to tmux key, got %#v", events[2])
	}
	if events[3].tmuxKey != "Enter" {
		t.Fatalf("expected newline to map to Enter, got %#v", events[3])
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

func TestShellSubmitWaitsForControllerToReportActiveExecution(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.input = "sleep 5"

	updated, _ := model.submit()
	next := updated.(Model)

	if next.activeExecution != nil {
		t.Fatalf("expected TUI to wait for controller execution state, got %#v", next.activeExecution)
	}
	if !next.busy {
		t.Fatal("expected shell submit to remain busy while awaiting controller response")
	}
}

func TestCommandResultClearsLiveShellTailPreview(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.showShellTail = true
	model.liveShellTail = "1\n2\n3"
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 5",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionRunning,
		StartedAt: time.Now(),
	}

	updated, _ := model.Update(controllerEventsMsg{
		events: []controller.TranscriptEvent{
			{
				Kind: controller.EventCommandResult,
				Payload: controller.CommandResultSummary{
					ExecutionID: "cmd-1",
					Command:     "sleep 5",
					Origin:      controller.CommandOriginUserShell,
					ExitCode:    0,
					Summary:     "1\n2\n3",
				},
			},
		},
	})
	next := updated.(Model)

	if next.showShellTail {
		t.Fatal("expected live shell tail to clear after command result")
	}
	if next.liveShellTail != "" {
		t.Fatalf("expected cleared shell tail text, got %q", next.liveShellTail)
	}
	if next.activeExecution != nil {
		t.Fatalf("expected active execution to clear after command result, got %#v", next.activeExecution)
	}
}

func TestCommandResultFallsBackToLiveShellTailWhenSummaryEmpty(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.showShellTail = true
	model.liveShellTail = "file-a\nfile-b\nfile-c"
	model.activeExecution = &controller.CommandExecution{
		ID:               "cmd-1",
		Command:          "ls",
		Origin:           controller.CommandOriginUserShell,
		State:            controller.CommandExecutionRunning,
		StartedAt:        time.Now(),
		LatestOutputTail: "file-a\nfile-b\nfile-c",
	}

	updated, _ := model.Update(controllerEventsMsg{
		events: []controller.TranscriptEvent{
			{
				Kind: controller.EventCommandResult,
				Payload: controller.CommandResultSummary{
					ExecutionID: "cmd-1",
					Command:     "ls",
					Origin:      controller.CommandOriginUserShell,
					ExitCode:    0,
					Summary:     "",
				},
			},
		},
	})
	next := updated.(Model)

	last := next.entries[len(next.entries)-1]
	if last.Title != "result" {
		t.Fatalf("expected result entry, got %#v", last)
	}
	if !strings.Contains(last.Body, "file-c") {
		t.Fatalf("expected fallback output in result body, got %q", last.Body)
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

package tui

import (
	"aiterm/internal/controller"
	"aiterm/internal/provider"
	"aiterm/internal/shell"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShiftTabTogglesMode(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	next := updated.(Model)

	if next.mode != AgentMode {
		t.Fatalf("expected AgentMode, got %s", next.mode)
	}
}

func TestF1OpensAndClosesHelpView(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyF1})
	next := updated.(Model)
	if !next.helpOpen {
		t.Fatal("expected F1 to open help view")
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyF1})
	next = updated.(Model)
	if next.helpOpen {
		t.Fatal("expected F1 to close help view when already open")
	}
}

func TestHelpViewContainsSlashCommandsAndShortcuts(t *testing.T) {
	view := strings.Join(helpContentLines(120, ShellMode, true), "\n")
	for _, fragment := range []string{"/help", "/approvals", "/approvals dangerous", "/new", "/compact", "/onboard", "/provider", "/model", "/quit", "Shift-Tab", "F2", "Shift-drag", "Ctrl+Shift+C / Ctrl+Shift+V", "KEYS> Enter", "KEYS> Ctrl+Y", "KEYS> Ctrl+J"} {
		if !strings.Contains(view, fragment) {
			t.Fatalf("expected help view to contain %q, got %q", fragment, view)
		}
	}
}

func TestSlashHelpOpensHelpView(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.mode = AgentMode
	model.input = "/help"

	updated, cmd := model.submit()
	next := updated.(Model)
	if cmd != nil {
		t.Fatal("expected /help to open synchronously")
	}
	if !next.helpOpen {
		t.Fatal("expected /help to open the help view")
	}
}

func TestHelpFooterUsesShorterHintsAtNarrowWidths(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)

	narrow := model.renderHelpFooter(32)
	if strings.Contains(narrow, "Home/End") || strings.Contains(narrow, "PgUp/PgDn") {
		t.Fatalf("expected narrow help footer to omit long hints, got %q", narrow)
	}
	if !strings.Contains(narrow, "[Pg]") {
		t.Fatalf("expected narrow help footer to use short paging hint, got %q", narrow)
	}

	wide := model.renderHelpFooter(80)
	if !strings.Contains(wide, "Home/End") || !strings.Contains(wide, "PgUp/PgDn") {
		t.Fatalf("expected wide help footer to keep full hints, got %q", wide)
	}
}

func TestTabInsertsTabIntoComposer(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	next := updated.(Model)

	if next.input != "\t" {
		t.Fatalf("expected tab inserted into composer, got %q", next.input)
	}
}

func TestMouseReportRunesDoNotLeakIntoComposer(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<64;85;43M<64;85;43M")})
	next := updated.(Model)

	if next.input != "" {
		t.Fatalf("expected mouse report fragments to be ignored, got %q", next.input)
	}
}

func TestAgentSubmitClearsDisplayedSupersededPlan(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.mode = AgentMode
	model.activePlan = &controller.ActivePlan{
		Summary: "Old task plan",
		Steps: []controller.PlanStep{
			{Text: "Do the old thing.", Status: controller.PlanStepInProgress},
		},
	}
	model.input = "inspect the current repo status instead"
	model.cursor = len([]rune(model.input))

	updated, _ := model.submit()
	next := updated.(Model)

	if next.activePlan != nil {
		t.Fatalf("expected superseded displayed plan to clear, got %#v", next.activePlan)
	}
}

func TestSlashCompletionAcceptsGhostWithRightArrow(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.mode = AgentMode
	model.setInput("/pr")

	if ghost := model.currentCompletionGhostText(); ghost != "ovider" {
		t.Fatalf("expected provider ghost text, got %q", ghost)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	next := updated.(Model)
	if next.input != "/provider" {
		t.Fatalf("expected right arrow to accept slash completion, got %q", next.input)
	}
}

func TestTabCyclesSlashCompletionCandidatesInline(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.mode = AgentMode
	model.setInput("/pr")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	next := updated.(Model)
	if next.input != "/pr" {
		t.Fatalf("expected tab cycle to keep input unchanged, got %q", next.input)
	}
	if ghost := next.currentCompletionGhostText(); ghost != "oviders" {
		t.Fatalf("expected providers ghost text after tab cycle, got %q", ghost)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyRight})
	next = updated.(Model)
	if next.input != "/providers" {
		t.Fatalf("expected right arrow to accept cycled slash completion, got %q", next.input)
	}
}

func TestSlashCompletionIncludesHelpCommand(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.mode = AgentMode
	model.setInput("/he")

	if ghost := model.currentCompletionGhostText(); ghost != "lp" {
		t.Fatalf("expected help ghost text, got %q", ghost)
	}
}

func TestShellHistoryCompletionAcceptsGhostWithRightArrow(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.mode = ShellMode
	model.shellContext = shell.PromptContext{Directory: t.TempDir()}
	model.shellHistory.record("git status")
	model.shellHistory.record("git stash")
	model.setInput("git sta")

	if ghost := model.currentCompletionGhostText(); ghost != "sh" {
		t.Fatalf("expected most recent history suggestion ghost text, got %q", ghost)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	next := updated.(Model)
	if next.input != "git stash" {
		t.Fatalf("expected right arrow to accept history completion, got %q", next.input)
	}
}

func TestAgentHistoryCompletionCyclesWithTab(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.mode = AgentMode
	model.agentHistory.record("summarize the diff")
	model.agentHistory.record("summarize the test failures")
	model.setInput("summarize the ")

	if ghost := model.currentCompletionGhostText(); ghost != "test failures" {
		t.Fatalf("expected most recent agent history ghost text, got %q", ghost)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	next := updated.(Model)
	if ghost := next.currentCompletionGhostText(); ghost != "diff" {
		t.Fatalf("expected tab to cycle agent history suggestions, got %q", ghost)
	}
}

func TestShellPathCompletionAcceptsGhostWithRightArrow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write alpha.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpine.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write alpine.txt: %v", err)
	}

	model := NewModel(fakeWorkspace(), nil)
	model.shellContext = shell.PromptContext{Directory: dir}
	model.setInput("cat al")

	if ghost := model.currentCompletionGhostText(); ghost != "pha.txt" {
		t.Fatalf("expected alpha.txt ghost text, got %q", ghost)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	next := updated.(Model)
	if next.input != "cat alpha.txt" {
		t.Fatalf("expected right arrow to accept path completion, got %q", next.input)
	}
}

func TestTabCyclesShellPathCompletionCandidatesInline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write alpha.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpine.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write alpine.txt: %v", err)
	}

	model := NewModel(fakeWorkspace(), nil)
	model.shellContext = shell.PromptContext{Directory: dir}
	model.setInput("cat al")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	next := updated.(Model)
	if next.input != "cat al" {
		t.Fatalf("expected tab cycle to keep shell input unchanged, got %q", next.input)
	}
	if ghost := next.currentCompletionGhostText(); ghost != "pine.txt" {
		t.Fatalf("expected alpine.txt ghost text after tab cycle, got %q", ghost)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyRight})
	next = updated.(Model)
	if next.input != "cat alpine.txt" {
		t.Fatalf("expected right arrow to accept cycled shell completion, got %q", next.input)
	}
}

func TestMouseClickTranscriptTagOpensDetailView(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.width = 80
	model.height = 24
	model.entries = []Entry{
		{Title: "agent", Body: "Hello", Detail: "detail line one\ndetail line two"},
		{Title: "system", Body: "Later"},
	}
	model.selectedEntry = 1

	y := transcriptLineIndexForEntry(model, 0)
	updated, _ := model.Update(tea.MouseMsg{
		X:      2,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)

	if next.selectedEntry != 0 {
		t.Fatalf("expected clicked entry to become selected, got %d", next.selectedEntry)
	}
	if !next.detailOpen {
		t.Fatal("expected transcript tag click to open detail view")
	}
}

func TestMouseClickTranscriptBodyDoesNotOpenDetailView(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.width = 80
	model.height = 24
	model.entries = []Entry{
		{Title: "agent", Body: "Hello", Detail: "detail line one"},
	}
	model.selectedEntry = 0

	y := transcriptLineIndexForEntry(model, 0)
	updated, _ := model.Update(tea.MouseMsg{
		X:      12,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)

	if next.detailOpen {
		t.Fatal("expected transcript body click to leave detail view closed")
	}
}

func TestMouseClickLongResultCommandTogglesExpansion(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.width = 80
	model.height = 24
	model.entries = []Entry{
		{
			Title:   "result",
			Command: "printf 'this command header is intentionally long enough to cross the transcript expansion threshold for mouse testing'",
			Body:    "line one\nline two",
			Detail:  "command:\nprintf 'this command header is intentionally long enough to cross the transcript expansion threshold for mouse testing'\n\nexit=0\n\nline one\nline two",
			TagKind: entryTagResultSuccess,
		},
	}
	model.selectedEntry = 0
	lines := model.transcriptWindowDisplay(model.transcriptDisplayLines(model.currentTranscriptWidth()), model.currentTranscriptHeight())
	y := transcriptLineIndexForEntry(model, 0)
	line := lines[y]
	if !line.commandClickable {
		t.Fatal("expected long command header to be clickable")
	}

	updated, _ := model.Update(tea.MouseMsg{
		X:      line.commandStart,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)

	if next.expandedCommandEntry != 0 {
		t.Fatalf("expected long command header to expand, got %d", next.expandedCommandEntry)
	}
	y = transcriptLineIndexForEntry(next, 0)
	lines = next.transcriptWindowDisplay(next.transcriptDisplayLines(next.currentTranscriptWidth()), next.currentTranscriptHeight())
	line = lines[y]

	updated, _ = next.Update(tea.MouseMsg{
		X:      line.commandStart,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next = updated.(Model)

	if next.expandedCommandEntry != -1 {
		t.Fatalf("expected second click to collapse expanded command header, got %d", next.expandedCommandEntry)
	}
}

func TestMouseClickResultOverflowHintOpensDetailView(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.width = 80
	model.height = 8
	model.entries = []Entry{
		{
			Title:   "result",
			Command: "seq 1 30",
			Body:    makeMultilineBody(30),
			Detail:  "command:\nseq 1 30\n\nexit=0\n\n" + makeMultilineBody(30),
			TagKind: entryTagResultSuccess,
		},
	}
	model.selectedEntry = 0

	lines := model.transcriptWindowDisplay(model.transcriptDisplayLines(model.currentTranscriptWidth()), model.currentTranscriptHeight())
	targetY := -1
	targetX := -1
	for index, line := range lines {
		if !line.detailClickable {
			continue
		}
		targetY = index
		targetX = line.detailStart
		break
	}
	if targetY < 0 {
		t.Fatal("expected overflow hint line to be present")
	}

	updated, _ := model.Update(tea.MouseMsg{
		X:      targetX,
		Y:      targetY,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)

	if !next.detailOpen {
		t.Fatal("expected overflow hint click to open detail view")
	}
}

func TestMouseWheelScrollsTranscript(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.width = 80
	model.height = 8
	model.entries = []Entry{
		{Title: "system", Body: "one"},
		{Title: "system", Body: "two"},
		{Title: "system", Body: "three"},
		{Title: "system", Body: "four"},
		{Title: "system", Body: "five"},
		{Title: "system", Body: "six"},
		{Title: "system", Body: "seven"},
		{Title: "system", Body: "eight"},
		{Title: "system", Body: "nine"},
		{Title: "system", Body: "ten"},
	}

	updated, _ := model.Update(tea.MouseMsg{
		X:      0,
		Y:      0,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)

	if next.transcriptFollow {
		t.Fatal("expected wheel up to unpin transcript follow")
	}
	if next.transcriptScroll >= next.maxTranscriptScroll() {
		t.Fatalf("expected wheel up to move transcript above the bottom, got scroll=%d max=%d", next.transcriptScroll, next.maxTranscriptScroll())
	}
}

func TestMouseWheelOverComposerStillScrollsTranscript(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil)
	model.width = 80
	model.height = 8
	model.entries = []Entry{
		{Title: "system", Body: "one"},
		{Title: "system", Body: "two"},
		{Title: "system", Body: "three"},
		{Title: "system", Body: "four"},
		{Title: "system", Body: "five"},
		{Title: "system", Body: "six"},
		{Title: "system", Body: "seven"},
		{Title: "system", Body: "eight"},
		{Title: "system", Body: "nine"},
		{Title: "system", Body: "ten"},
	}
	model.mode = AgentMode
	model.setInput("draft prompt")
	model.agentHistory.entries = []string{"older prompt"}

	composerY := model.currentTranscriptHeight() + 1
	updated, _ := model.Update(tea.MouseMsg{
		X:      2,
		Y:      composerY,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)

	if next.transcriptFollow {
		t.Fatal("expected wheel over composer to unpin transcript follow")
	}
	if next.transcriptScroll >= next.maxTranscriptScroll() {
		t.Fatalf("expected wheel over composer to move transcript above bottom, got scroll=%d max=%d", next.transcriptScroll, next.maxTranscriptScroll())
	}
	if next.input != "draft prompt" {
		t.Fatalf("expected composer input to remain unchanged, got %q", next.input)
	}
}

func TestMouseClickShellTailLabelStartsTakeControl(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil).WithTakeControl("sock", "sess", "%0", TakeControlKey)
	model.width = 80
	model.height = 24
	model.showShellTail = true
	model.liveShellTail = "line one\nline two"

	startY, ok := model.shellTailStartY()
	if !ok {
		t.Fatal("expected shell tail start position")
	}

	updated, cmd := model.Update(tea.MouseMsg{
		X:      model.styles.tail.GetHorizontalPadding() + 1,
		Y:      startY,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)

	if cmd == nil {
		t.Fatal("expected shell tail label click to trigger take-control command")
	}
	if next.busy {
		t.Fatal("expected take-control click not to leave model busy")
	}
}

func TestMouseClickApprovalRejectButtonDecidesApproval(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.width = 80
	model.height = 24
	model.pendingApproval = &controller.ApprovalRequest{
		ID:      "approval-1",
		Title:   "Run it?",
		Summary: "Need approval",
		Command: "rm -rf tmp",
	}

	x, y := actionCardButtonPoint(t, model, 1)
	updated, cmd := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	model = updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	if len(ctrl.decisions) != 1 || ctrl.decisions[0].decision != controller.DecisionReject {
		t.Fatalf("expected reject decision from mouse click, got %#v", ctrl.decisions)
	}
	updated, _ = model.Update(msg)
	model = updated.(Model)
	if model.pendingApproval != nil {
		t.Fatalf("expected approval to clear after reject, got %#v", model.pendingApproval)
	}
}

func TestMouseClickProposalRefineButtonBeginsRefinement(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 24
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalCommand,
		Command:     "git status",
		Description: "Inspect the worktree.",
	}

	x, y := actionCardButtonPoint(t, model, 2)
	updated, cmd := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)
	if cmd != nil {
		t.Fatal("expected proposal refine click to stay local")
	}
	if next.refiningProposal == nil || next.pendingProposal != nil || next.mode != AgentMode {
		t.Fatalf("expected refine proposal mode from mouse click, got pending=%#v refining=%#v mode=%s", next.pendingProposal, next.refiningProposal, next.mode)
	}
}

func TestMouseClickStartupContinueDismissesWarning(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{
			Preset:       provider.PresetOpenAI,
			Name:         "OpenAI Responses",
			Model:        "gpt-5",
			BaseURL:      "https://api.openai.com/v1",
			APIKeyEnvVar: "local_file",
		},
		nil,
		nil,
		nil,
		nil,
	)
	model.width = 80
	model.height = 24

	x, y := actionCardButtonPoint(t, model, 0)
	updated, cmd := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)
	if cmd != nil {
		t.Fatal("expected startup continue click to stay local")
	}
	if next.startupNotice != nil {
		t.Fatalf("expected startup notice cleared, got %#v", next.startupNotice)
	}
}

func TestMouseClickFullscreenConfirmRunsShellCommand(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.width = 80
	model.height = 24
	model.pendingFullscreen = &fullscreenAction{
		Kind:    fullscreenActionShellSubmit,
		Command: "ls",
	}

	x, y := actionCardButtonPoint(t, model, 0)
	updated, cmd := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected fullscreen confirm click to submit shell command")
	}
	if next.pendingFullscreen != nil {
		t.Fatalf("expected fullscreen confirmation to clear, got %#v", next.pendingFullscreen)
	}
	msg := controllerEventsFromCmd(t, cmd)
	if len(ctrl.shellCommands) != 1 || ctrl.shellCommands[0] != "ls" {
		t.Fatalf("expected fullscreen confirm to run shell command, got %#v", ctrl.shellCommands)
	}
	updated, _ = next.Update(msg)
	next = updated.(Model)
	if next.busy {
		t.Fatal("expected model to leave busy state after command result")
	}
}

func TestMouseClickFullscreenCancelClearsPending(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 24
	model.pendingFullscreen = &fullscreenAction{
		Kind:    fullscreenActionShellSubmit,
		Command: "ls",
	}

	x, y := actionCardButtonPoint(t, model, 1)
	updated, cmd := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)
	if cmd != nil {
		t.Fatal("expected fullscreen cancel click to stay local")
	}
	if next.pendingFullscreen != nil {
		t.Fatalf("expected fullscreen confirmation cleared, got %#v", next.pendingFullscreen)
	}
}

func TestMouseClickFullscreenTakeControlStartsHandoff(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithTakeControl("sock", "sess", "%0", TakeControlKey)
	model.width = 80
	model.height = 24
	model.pendingFullscreen = &fullscreenAction{
		Kind:    fullscreenActionShellSubmit,
		Command: "ls",
	}

	x, y := actionCardButtonPoint(t, model, 2)
	updated, cmd := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected fullscreen take-control click to trigger handoff")
	}
	if next.busy {
		t.Fatal("expected take-control click not to leave model busy")
	}
}

func TestMouseClickApprovalApproveDecidesApproval(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.width = 80
	model.height = 24
	model.pendingApproval = &controller.ApprovalRequest{
		ID:      "approval-1",
		Title:   "Run it?",
		Summary: "Need approval",
		Command: "echo hi",
	}

	x, y := actionCardButtonPoint(t, model, 0)
	updated, cmd := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	model = updated.(Model)
	msg := controllerEventsFromCmd(t, cmd)
	if len(ctrl.decisions) != 1 || ctrl.decisions[0].decision != controller.DecisionApprove {
		t.Fatalf("expected approve decision from mouse click, got %#v", ctrl.decisions)
	}
	updated, _ = model.Update(msg)
	model = updated.(Model)
	if model.pendingApproval != nil {
		t.Fatalf("expected approval to clear after approve, got %#v", model.pendingApproval)
	}
}

func TestMouseClickApprovalRefineBeginsRefinement(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.width = 80
	model.height = 24
	model.pendingApproval = &controller.ApprovalRequest{
		ID:      "approval-1",
		Title:   "Run it?",
		Summary: "Need approval",
		Command: "echo hi",
	}

	x, y := actionCardButtonPoint(t, model, 2)
	updated, cmd := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected approval refine click to return command")
	}
	if next.refiningApproval == nil || next.mode != AgentMode {
		t.Fatalf("expected approval refine mode from mouse click, got refining=%#v mode=%s", next.refiningApproval, next.mode)
	}
}

func TestMouseClickPatchApprovalButtonsWork(t *testing.T) {
	t.Run("approve", func(t *testing.T) {
		ctrl := &fakeController{}
		model := NewModel(fakeWorkspace(), ctrl)
		model.width = 80
		model.height = 24
		model.pendingApproval = &controller.ApprovalRequest{
			ID:      "approval-1",
			Kind:    controller.ApprovalPatch,
			Title:   "Apply patch?",
			Summary: strings.Repeat("This patch updates several files and should still leave the card wrapped correctly. ", 2),
			Patch:   "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n",
		}

		x, y := actionCardButtonPoint(t, model, 0)
		updated, cmd := model.Update(tea.MouseMsg{
			X:      x,
			Y:      y,
			Button: tea.MouseButtonLeft,
			Action: tea.MouseActionPress,
		})
		model = updated.(Model)
		msg := controllerEventsFromCmd(t, cmd)
		if len(ctrl.decisions) != 1 || ctrl.decisions[0].decision != controller.DecisionApprove {
			t.Fatalf("expected patch approve decision from mouse click, got %#v", ctrl.decisions)
		}
		updated, _ = model.Update(msg)
		model = updated.(Model)
		if model.pendingApproval != nil {
			t.Fatalf("expected patch approval to clear after approve, got %#v", model.pendingApproval)
		}
	})

	t.Run("reject", func(t *testing.T) {
		ctrl := &fakeController{}
		model := NewModel(fakeWorkspace(), ctrl)
		model.width = 80
		model.height = 24
		model.pendingApproval = &controller.ApprovalRequest{
			ID:      "approval-1",
			Kind:    controller.ApprovalPatch,
			Title:   "Apply patch?",
			Summary: "Need approval",
			Patch:   "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n",
		}

		x, y := actionCardButtonPoint(t, model, 1)
		updated, cmd := model.Update(tea.MouseMsg{
			X:      x,
			Y:      y,
			Button: tea.MouseButtonLeft,
			Action: tea.MouseActionPress,
		})
		model = updated.(Model)
		msg := controllerEventsFromCmd(t, cmd)
		if len(ctrl.decisions) != 1 || ctrl.decisions[0].decision != controller.DecisionReject {
			t.Fatalf("expected patch reject decision from mouse click, got %#v", ctrl.decisions)
		}
		updated, _ = model.Update(msg)
		model = updated.(Model)
		if model.pendingApproval != nil {
			t.Fatalf("expected patch approval to clear after reject, got %#v", model.pendingApproval)
		}
	})

	t.Run("refine", func(t *testing.T) {
		ctrl := &fakeController{}
		model := NewModel(fakeWorkspace(), ctrl)
		model.width = 80
		model.height = 24
		model.pendingApproval = &controller.ApprovalRequest{
			ID:      "approval-1",
			Kind:    controller.ApprovalPatch,
			Title:   "Apply patch?",
			Summary: "Need approval",
			Patch:   "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n",
		}

		x, y := actionCardButtonPoint(t, model, 2)
		updated, cmd := model.Update(tea.MouseMsg{
			X:      x,
			Y:      y,
			Button: tea.MouseButtonLeft,
			Action: tea.MouseActionPress,
		})
		next := updated.(Model)
		if cmd == nil {
			t.Fatal("expected patch approval refine click to return command")
		}
		if next.refiningApproval == nil || next.mode != AgentMode {
			t.Fatalf("expected patch approval refine mode from mouse click, got refining=%#v mode=%s", next.refiningApproval, next.mode)
		}
	})
}

func TestMouseClickProposalApproveRunsCommand(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.width = 80
	model.height = 24
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalCommand,
		Command:     "git status",
		Description: "Inspect the worktree.",
	}

	x, y := actionCardButtonPoint(t, model, 0)
	updated, cmd := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected proposal approve click to run command")
	}
	msg := controllerEventsFromCmd(t, cmd)
	if len(ctrl.shellCommands) != 1 || ctrl.shellCommands[0] != "git status" {
		t.Fatalf("expected proposal approve to run command, got %#v", ctrl.shellCommands)
	}
	updated, _ = next.Update(msg)
	next = updated.(Model)
	if next.pendingProposal != nil {
		t.Fatalf("expected proposal to clear after approve, got %#v", next.pendingProposal)
	}
}

func TestMouseClickProposalRejectDismissesProposal(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 24
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalCommand,
		Command:     "git status",
		Description: "Inspect the worktree.",
	}

	x, y := actionCardButtonPoint(t, model, 1)
	updated, cmd := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)
	if cmd != nil {
		t.Fatal("expected proposal reject click to stay local")
	}
	if next.pendingProposal != nil {
		t.Fatalf("expected proposal cleared after reject, got %#v", next.pendingProposal)
	}
	last := next.entries[len(next.entries)-1]
	if last.Title != "system" || !strings.Contains(last.Body, "Proposal dismissed.") {
		t.Fatalf("expected proposal dismissal notice, got %#v", last)
	}
}

func TestMouseClickProposalEditBeginsEditingOnWrappedButtons(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 28
	model.height = 24
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalCommand,
		Command:     "git status",
		Description: "Inspect the worktree.",
	}

	x, y := actionCardButtonPoint(t, model, 3)
	updated, cmd := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)
	if cmd != nil {
		t.Fatal("expected proposal edit click to stay local")
	}
	if next.editingProposal == nil || next.pendingProposal != nil || next.input != "git status" {
		t.Fatalf("expected proposal editing mode from wrapped-button click, got editing=%#v pending=%#v input=%q", next.editingProposal, next.pendingProposal, next.input)
	}
}

func TestMouseClickProposalSendKeysRunsKeySend(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithTakeControl("sock", "sess", "%0", TakeControlKey)
	model.width = 80
	model.height = 24
	model.activeExecution = &controller.CommandExecution{
		ID:        "cmd-1",
		Command:   "vim",
		Origin:    controller.CommandOriginUserShell,
		State:     controller.CommandExecutionInteractiveFullscreen,
		StartedAt: time.Now(),
	}
	model.pendingProposal = &controller.ProposalPayload{
		Kind:        controller.ProposalKeys,
		Keys:        ":q!\n",
		Description: "Exit the fullscreen app.",
	}
	model.liveShellTail = "vim"
	model.observeActiveKeysLease("test")

	x, y := actionCardButtonPoint(t, model, 0)
	updated, cmd := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected proposal send-keys click to return command")
	}
	if next.pendingProposal != nil {
		t.Fatalf("expected key proposal to clear after click, got %#v", next.pendingProposal)
	}
}

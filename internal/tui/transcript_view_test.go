package tui

import (
	"aiterm/internal/controller"
	"aiterm/internal/provider"
	"aiterm/internal/shell"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"strings"
	"testing"
	"time"
)

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

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
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

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
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

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlEnd})
	model = updated.(Model)
	if !model.transcriptFollow {
		t.Fatal("expected Ctrl+End to return transcript to follow mode")
	}
	if model.transcriptScroll != model.maxTranscriptScroll() {
		t.Fatalf("expected scroll at bottom, got %d", model.transcriptScroll)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlHome})
	model = updated.(Model)
	if model.transcriptScroll != 0 {
		t.Fatalf("expected Ctrl+Home to jump to top, got %d", model.transcriptScroll)
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
	last := model.entries[len(model.entries)-1]
	if last.Title != "shell" || last.Body != "pwd" {
		t.Fatalf("expected optimistic shell entry, got %#v", last)
	}
	if !model.transcriptFollow {
		t.Fatal("expected optimistic shell entry to keep follow mode")
	}
	if model.transcriptScroll != model.maxTranscriptScroll() {
		t.Fatalf("expected optimistic shell entry at bottom, got %d", model.transcriptScroll)
	}
	msg = controllerEventsFromCmd(t, cmd)
	updatedAny, _ = model.Update(msg)
	model = updatedAny.(Model)
	if !model.transcriptFollow {
		t.Fatal("expected pinned transcript to stay in follow mode")
	}
	if model.transcriptScroll != model.maxTranscriptScroll() {
		t.Fatalf("expected transcript to stay at bottom, got %d", model.transcriptScroll)
	}
	shellCount := 0
	for _, entry := range model.entries {
		if entry.Title == "shell" && entry.Body == "pwd" {
			shellCount++
		}
	}
	if shellCount != 1 {
		t.Fatalf("expected one shell entry after controller echo dedupe, got %d", shellCount)
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

func TestViewFitsActualTerminalSizeWhenNarrow(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 24
	model.height = 8

	view := model.View()
	lines := strings.Split(view, "\n")
	if len(lines) != model.height {
		t.Fatalf("expected %d rendered lines, got %d in %q", model.height, len(lines), view)
	}
	for _, line := range lines {
		if lipgloss.Width(line) > model.width {
			t.Fatalf("expected rendered line width <= %d, got %d in %q", model.width, lipgloss.Width(line), line)
		}
	}
}

func TestMainFooterStaysOnBottomRow(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 12

	view := model.View()
	lines := strings.Split(view, "\n")
	if len(lines) != model.height {
		t.Fatalf("expected %d rendered lines, got %d", model.height, len(lines))
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, "quit") {
		t.Fatalf("expected footer on last rendered line, got %q", last)
	}
}

func TestShortTranscriptSitsAboveComposer(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 20
	model.entries = []Entry{{Title: "system", Body: "one visible line"}}
	model.selectedEntry = 0

	window := model.transcriptWindow(model.transcriptLines(model.currentTranscriptWidth()), model.currentTranscriptHeight())
	if got := strings.TrimSpace(window[len(window)-1]); !strings.Contains(got, "one visible line") {
		t.Fatalf("expected transcript window to bottom-align final line, got last=%q full=%#v", got, window)
	}

	lines := strings.Split(model.View(), "\n")
	anchor := -1
	for index, line := range lines {
		if strings.Contains(line, "one visible line") {
			anchor = index
			break
		}
	}
	if anchor < 0 {
		t.Fatalf("expected transcript line in view, got %q", model.View())
	}

	composer := -1
	for index, line := range lines {
		if strings.Contains(line, shellComposerPrompt) || strings.Contains(line, rootComposerPrompt) || strings.Contains(line, agentComposerPrompt) || strings.Contains(line, keysComposerPrompt) {
			composer = index
			break
		}
	}
	if composer < 0 {
		t.Fatalf("expected composer line in view, got %q", model.View())
	}
	if anchor != composer-1 {
		t.Fatalf("expected short transcript to sit directly above composer, got transcript line %d composer line %d", anchor, composer)
	}
}

func TestCtrlCClearsComposerThenArmsExit(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 20
	model.input = "draft command"

	updatedAny, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = updatedAny.(Model)
	if model.input != "" {
		t.Fatalf("expected first Ctrl-C to clear composer, got %q", model.input)
	}
	if !model.exitConfirmActive() {
		t.Fatal("expected exit confirm to be armed after first Ctrl-C")
	}
	if cmd == nil {
		t.Fatal("expected exit confirm timer command")
	}
	if !strings.Contains(model.View(), "ctrl-c again to exit") {
		t.Fatalf("expected exit warning in status line, got %q", model.View())
	}

	updatedAny, cmd = model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = updatedAny.(Model)
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected second Ctrl-C to quit")
	}
}

func TestCtrlCArmsExitWhenComposerEmptyAndExpires(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 20

	updatedAny, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = updatedAny.(Model)
	if !model.exitConfirmActive() {
		t.Fatal("expected exit confirm to be armed")
	}
	if cmd == nil {
		t.Fatal("expected exit confirm timer command")
	}
	expiredMsg, ok := cmd().(exitConfirmExpiredMsg)
	if !ok {
		t.Fatalf("expected exitConfirmExpiredMsg, got %T", cmd())
	}
	updatedAny, _ = model.Update(expiredMsg)
	model = updatedAny.(Model)
	if model.exitConfirmActive() {
		t.Fatal("expected exit confirm to clear after timeout")
	}
}

func TestStartupWarningAppearsForPlaintextProviderSecret(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{
			Preset:       provider.PresetOpenAI,
			Name:         "OpenAI Responses",
			Model:        "gpt-5-nano-2025-08-07",
			BaseURL:      "https://api.openai.com/v1",
			APIKeyEnvVar: "local_file",
		},
		nil,
		nil,
		nil,
		nil,
	)
	model.width = 100
	model.height = 24

	view := model.View()
	if !strings.Contains(view, "Less Secure Secret Storage") {
		t.Fatalf("expected startup security notice, got %q", view)
	}
	if !strings.Contains(view, "Y continue") {
		t.Fatalf("expected startup notice actions, got %q", view)
	}
}

func TestStartupWarningBlocksComposerUntilDismissed(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{
			Preset:       provider.PresetAnthropic,
			Name:         "Anthropic Messages",
			Model:        "claude-sonnet-4-6",
			BaseURL:      "https://api.anthropic.com",
			APIKeyEnvVar: "local_file",
		},
		nil,
		nil,
		nil,
		nil,
	)
	model.width = 100
	model.height = 24
	model.mode = ShellMode
	model.input = "pwd"

	updatedAny, cmd := model.submit()
	model = updatedAny.(Model)
	if cmd != nil {
		t.Fatal("expected submit to stay blocked while startup notice is active")
	}
	if model.startupNotice == nil {
		t.Fatal("expected startup notice to remain active")
	}

	updatedAny, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updatedAny.(Model)
	if model.startupNotice != nil {
		t.Fatal("expected Enter to dismiss startup notice")
	}
}

func TestStartupWarningDismissesOnY(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{
			Preset:       provider.PresetOpenAI,
			Name:         "OpenAI Responses",
			Model:        "gpt-5-nano-2025-08-07",
			BaseURL:      "https://api.openai.com/v1",
			APIKeyEnvVar: "local_file",
		},
		nil,
		nil,
		nil,
		nil,
	)

	updatedAny, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updatedAny.(Model)
	if model.startupNotice != nil {
		t.Fatal("expected Y to dismiss startup notice")
	}
}

func TestBusyStatusLineRendersAboveComposer(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 20
	model.busy = true
	model.busyStartedAt = time.Now()
	model.shellContext = shell.PromptContext{
		User:         "localuser",
		Host:         "workstation",
		Directory:    "~/workspace/project",
		GitBranch:    "main",
		PromptSymbol: "%",
	}

	view := model.View()
	if !strings.Contains(view, "Working ") {
		t.Fatalf("expected busy status line, got %q", view)
	}
	if !strings.Contains(view, "( 0s)") {
		t.Fatalf("expected fixed-width busy timer, got %q", view)
	}
	if !strings.Contains(view, "localuser@workstation ~/workspace/project git:(main) %") {
		t.Fatalf("expected shell context line, got %q", view)
	}
}

func TestInitialWorkspaceReadyNoticeIsHiddenFromTranscript(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 20

	view := model.View()
	if strings.Contains(view, "Workspace ready. Top pane:") {
		t.Fatalf("expected startup notice to stay out of the visible transcript, got %q", view)
	}
	if len(model.entries) == 0 || !model.entries[0].Hidden {
		t.Fatalf("expected startup notice to remain in trace as a hidden entry, got %#v", model.entries)
	}
	if !strings.Contains(model.entries[0].Body, "Workspace ready. Top pane:") {
		t.Fatalf("expected startup notice body to remain available, got %#v", model.entries[0])
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
	if !strings.Contains(view, "root") {
		t.Fatalf("expected root badge, got %q", view)
	}
	if !strings.Contains(view, "remote") {
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
	if !strings.Contains(view, agentComposerPrompt) {
		t.Fatalf("expected agent prompt prefix, got %q", view)
	}

	model.mode = ShellMode
	model.shellContext = shell.PromptContext{Root: true}
	view = model.View()
	if !strings.Contains(view, rootComposerPrompt) {
		t.Fatalf("expected root shell prompt prefix, got %q", view)
	}
}

func TestTranscriptUsesEmojiTagsWhenUTF8LocaleIsAvailable(t *testing.T) {
	t.Setenv("LC_ALL", "en_US.UTF-8")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "")

	model := NewModel(fakeWorkspace(), &fakeController{})
	model.entries = []Entry{{Title: "agent", Body: "Hello"}}

	lines := model.transcriptLines(40)
	if len(lines) == 0 || !strings.Contains(lines[0], "🤖") {
		t.Fatalf("expected emoji transcript tag, got %#v", lines)
	}
}

func TestTranscriptFallsBackToTextTagsWithoutUTF8Locale(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "C")

	model := NewModel(fakeWorkspace(), &fakeController{})
	model.entries = []Entry{{Title: "agent", Body: "Hello"}}

	lines := model.transcriptLines(40)
	if len(lines) == 0 || !strings.Contains(lines[0], "AGENT") {
		t.Fatalf("expected text transcript tag fallback, got %#v", lines)
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
	if strings.Contains(model.View(), "›") {
		t.Fatalf("expected transcript selection to use background highlight instead of arrow marker, got %q", model.View())
	}
}

func TestTranscriptSelectionScrollsEntryIntoView(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 8
	model.entries = makeTranscriptEntries(12)
	model.transcriptFollow = false
	model.transcriptScroll = 3
	model.selectedEntry = 3

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	model = updated.(Model)
	if model.selectedEntry != 2 {
		t.Fatalf("expected selected entry 2, got %d", model.selectedEntry)
	}
	if model.transcriptScroll != 2 {
		t.Fatalf("expected alt-up to align selected entry at top, got scroll=%d", model.transcriptScroll)
	}

	model.transcriptScroll = 2
	model.transcriptFollow = false
	model.selectedEntry = 5
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown, Alt: true})
	model = updated.(Model)
	if model.selectedEntry != 6 {
		t.Fatalf("expected selected entry 6, got %d", model.selectedEntry)
	}
	if model.transcriptScroll != 3 {
		t.Fatalf("expected alt-down to align selected entry at bottom, got scroll=%d", model.transcriptScroll)
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

func TestDetailViewTypingFiltersVisibleLines(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 20
	model.entries = []Entry{
		{Title: "result", Detail: "alpha\nbeta\nbeta-two\ngamma"},
	}
	model.selectedEntry = 0
	model.detailOpen = true

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	model = updated.(Model)

	view := model.renderDetailView()
	if !strings.Contains(view, "Filter: be  (2 matching lines)") {
		t.Fatalf("expected detail filter summary, got %q", view)
	}
	if !strings.Contains(view, "beta") || !strings.Contains(view, "beta-two") {
		t.Fatalf("expected filtered lines to remain visible, got %q", view)
	}
	if strings.Contains(view, "alpha") || strings.Contains(view, "gamma") {
		t.Fatalf("expected non-matching lines to be hidden, got %q", view)
	}
}

func TestDetailViewEscClearsFilterBeforeClosing(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 20
	model.entries = []Entry{
		{Title: "result", Detail: "alpha\nbeta"},
	}
	model.selectedEntry = 0
	model.detailOpen = true
	model.detailFilter = "be"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if !model.detailOpen {
		t.Fatal("expected first Esc to keep detail open")
	}
	if model.detailFilter != "" {
		t.Fatalf("expected first Esc to clear filter, got %q", model.detailFilter)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.detailOpen {
		t.Fatal("expected second Esc to close detail view")
	}
}

func TestDetailViewFilterNoMatchesRendersEmptyState(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 20
	model.entries = []Entry{
		{Title: "result", Detail: "alpha\nbeta"},
	}
	model.selectedEntry = 0
	model.detailOpen = true
	model.detailFilter = "zzz"

	view := model.renderDetailView()
	if !strings.Contains(view, "No detail lines match the current filter.") {
		t.Fatalf("expected no-match detail message, got %q", view)
	}
}

func TestCommandResultEntryIsCollapsedInTranscriptButPreservedInDetail(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 80
	model.height = 8
	events := []controller.TranscriptEvent{
		{
			Kind: controller.EventCommandResult,
			Payload: controller.CommandResultSummary{
				Command:  "seq 1 30",
				ExitCode: 0,
				Summary:  makeMultilineBody(30),
			},
		},
	}

	entries := eventsToEntries(events, true)
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}

	model.entries = entries
	renderedLines := model.renderEntryLines(0, entries[0], model.currentTranscriptWidth())
	rendered := make([]string, 0, len(renderedLines))
	for _, line := range renderedLines {
		rendered = append(rendered, line.text)
	}

	if !strings.Contains(strings.Join(rendered, "\n"), "Ctrl+O to inspect") {
		t.Fatalf("expected viewport-capped preview, got %q", strings.Join(rendered, "\n"))
	}

	if !strings.Contains(rendered[0], "seq 1 30") {
		t.Fatalf("expected command header on first line, got %q", rendered[0])
	}

	if !strings.Contains(entries[0].Detail, "command:\nseq 1 30") {
		t.Fatalf("expected detail to retain command metadata, got %q", entries[0].Detail)
	}

	if !strings.Contains(entries[0].Detail, "line 29") {
		t.Fatalf("expected detail to retain full output, got %q", entries[0].Detail)
	}
}

func TestModelInfoMovesIntoDetailInsteadOfVisibleTranscriptRow(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.width = 100
	model.height = 20

	updated, _ := model.Update(controllerEventsMsg{events: []controller.TranscriptEvent{
		{Kind: controller.EventAgentMessage, Payload: controller.TextPayload{Text: "done"}},
		{Kind: controller.EventModelInfo, Payload: controller.AgentModelInfo{
			ProviderPreset: "ollama",
			RequestedModel: "qwen3.5:35b-a3b",
			ResponseModel:  "qwen3.5:35b-a3b",
		}},
	}})
	model = updated.(Model)

	view := model.View()
	if strings.Contains(view, "reply model:") {
		t.Fatalf("expected model info to stay out of visible transcript, got %q", view)
	}
	if len(model.entries) < 2 {
		t.Fatalf("expected agent entry to remain in trace, got %#v", model.entries)
	}
	detail := model.entries[len(model.entries)-1].Detail
	if !strings.Contains(detail, "reply model:\nqwen3.5:35b-a3b") {
		t.Fatalf("expected model info in entry detail, got %q", detail)
	}
	if !strings.Contains(detail, "provider:\nOllama") {
		t.Fatalf("expected provider info in entry detail, got %q", detail)
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

	if entries[0].Command != "seq 1 10" {
		t.Fatalf("expected command metadata on result entry, got %#v", entries[0])
	}

	if !strings.Contains(entries[0].Body, "line 9") {
		t.Fatalf("expected full shell result in transcript, got %q", entries[0].Body)
	}
}

func TestSilentSuccessResultEntryOmitsExitZeroNoise(t *testing.T) {
	entries := eventsToEntries([]controller.TranscriptEvent{
		{
			Kind: controller.EventCommandResult,
			Payload: controller.CommandResultSummary{
				Command:  "touch note.txt",
				ExitCode: 0,
				State:    controller.CommandExecutionCompleted,
				Summary:  "(no output)",
			},
		},
	}, true)

	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}
	if entries[0].Body != "" {
		t.Fatalf("expected silent success body to be empty, got %q", entries[0].Body)
	}
	if !strings.Contains(entries[0].Detail, "exit=0") {
		t.Fatalf("expected detail to retain exit code, got %q", entries[0].Detail)
	}
}

func TestSilentDirectoryChangeResultShowsUpdatedDirectory(t *testing.T) {
	entries := eventsToEntries([]controller.TranscriptEvent{
		{
			Kind: controller.EventCommandResult,
			Payload: controller.CommandResultSummary{
				Command:  "cd go_learn",
				ExitCode: 0,
				State:    controller.CommandExecutionCompleted,
				Summary:  "(no output)",
				ShellContext: &shell.PromptContext{
					Directory: "~/workspace/other-project",
				},
			},
		},
	}, true)

	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}
	if entries[0].Body != "~/workspace/other-project" {
		t.Fatalf("expected updated directory in silent success body, got %q", entries[0].Body)
	}
}

func TestCommandResultEntryClassifiesExitCodeTags(t *testing.T) {
	entries := eventsToEntries([]controller.TranscriptEvent{
		{
			Kind: controller.EventCommandResult,
			Payload: controller.CommandResultSummary{
				Command:  "missing-cmd",
				ExitCode: 127,
				State:    controller.CommandExecutionFailed,
				Summary:  "command not found",
			},
		},
		{
			Kind: controller.EventCommandResult,
			Payload: controller.CommandResultSummary{
				Command:  "sleep 20",
				ExitCode: shell.InterruptedExitCode,
				State:    controller.CommandExecutionCanceled,
				Summary:  "^C",
			},
		},
		{
			Kind: controller.EventCommandResult,
			Payload: controller.CommandResultSummary{
				Command:  "bad-wrapper",
				ExitCode: 255,
				State:    controller.CommandExecutionFailed,
				Summary:  "fatal wrapper failure",
			},
		},
	}, true)

	if len(entries) != 3 {
		t.Fatalf("expected three entries, got %d", len(entries))
	}
	if entries[0].TagKind != entryTagResultNotFound {
		t.Fatalf("expected not-found tag, got %#v", entries[0])
	}
	if entries[1].TagKind != entryTagResultSigInt {
		t.Fatalf("expected sigint tag, got %#v", entries[1])
	}
	if entries[2].TagKind != entryTagResultFatal {
		t.Fatalf("expected fatal tag, got %#v", entries[2])
	}
}

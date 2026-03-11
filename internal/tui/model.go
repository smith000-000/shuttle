package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aiterm/internal/controller"
	"aiterm/internal/tmux"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Mode string

const (
	AgentMode Mode = "AGENT"
	ShellMode Mode = "SHELL"
)

type Entry struct {
	Title string
	Body  string
}

type controllerEventsMsg struct {
	events []controller.TranscriptEvent
	err    error
}

type Model struct {
	workspace          tmux.Workspace
	ctrl               controller.Controller
	mode               Mode
	input              string
	entries            []Entry
	width              int
	height             int
	busy               bool
	transcriptScroll   int
	transcriptFollow   bool
	shellHistory       composerHistory
	agentHistory       composerHistory
	pendingApproval    *controller.ApprovalRequest
	pendingProposal    *controller.ProposalPayload
	refiningApproval   *controller.ApprovalRequest
	approvalInFlight   bool
	proposalRunPending bool
	styles             styles
}

func NewModel(workspace tmux.Workspace, ctrl controller.Controller) Model {
	return Model{
		workspace:        workspace,
		ctrl:             ctrl,
		mode:             ShellMode,
		transcriptFollow: true,
		entries: []Entry{
			{
				Title: "system",
				Body:  fmt.Sprintf("Workspace ready. Top pane: %s. Bottom pane TUI is active.", workspace.TopPane.ID),
			},
			{
				Title: "system",
				Body:  "Tab mode. Up/Down history. PgUp/PgDn scroll. Enter submit. Esc clear. Ctrl+C quit.",
			},
		},
		styles: newStyles(),
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampTranscriptScroll()
		return m, nil
	case controllerEventsMsg:
		pinned := m.isTranscriptPinned()
		m.busy = false
		if msg.err != nil {
			m.approvalInFlight = false
			m.proposalRunPending = false
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  msg.err.Error(),
			})
			if pinned {
				m.scrollTranscriptToBottom()
			} else {
				m.clampTranscriptScroll()
			}
			return m, nil
		}

		m.entries = append(m.entries, eventsToEntries(msg.events)...)
		m.syncActionState(msg.events)
		if pinned {
			m.scrollTranscriptToBottom()
		} else {
			m.clampTranscriptScroll()
		}
		if containsEventKind(msg.events, controller.EventError) {
			return m, nil
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEsc:
			m.input = ""
			return m, nil
		case tea.KeyTab:
			m.currentHistory().reset()
			if m.mode == ShellMode {
				m.mode = AgentMode
			} else {
				m.mode = ShellMode
			}
			return m, nil
		case tea.KeyUp:
			m.input = m.currentHistory().previous(m.input)
			return m, nil
		case tea.KeyDown:
			m.input = m.currentHistory().next(m.input)
			return m, nil
		case tea.KeyPgUp:
			m.scrollTranscriptBy(-m.pageScrollSize())
			return m, nil
		case tea.KeyPgDown:
			m.scrollTranscriptBy(m.pageScrollSize())
			return m, nil
		case tea.KeyHome:
			m.scrollTranscriptToTop()
			return m, nil
		case tea.KeyEnd:
			m.scrollTranscriptToBottom()
			return m, nil
		case tea.KeyCtrlU:
			m.scrollTranscriptBy(-m.halfPageScrollSize())
			return m, nil
		case tea.KeyCtrlD:
			m.scrollTranscriptBy(m.halfPageScrollSize())
			return m, nil
		case tea.KeyCtrlJ:
			m.input += "\n"
			return m, nil
		case tea.KeyCtrlE:
			return m.runProposalCommand()
		case tea.KeyCtrlY:
			return m.decideApproval(controller.DecisionApprove)
		case tea.KeyCtrlN:
			return m.decideApproval(controller.DecisionReject)
		case tea.KeyCtrlR:
			return m.refineApproval()
		case tea.KeyEnter:
			return m.submit()
		case tea.KeyBackspace, tea.KeyDelete:
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
			return m, nil
		case tea.KeySpace:
			m.input += " "
			return m, nil
		default:
			if !msg.Alt && msg.Type == tea.KeyRunes {
				m.input += string(msg.Runes)
				return m, nil
			}
		}
	}

	return m, nil
}

func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	screenWidth := max(40, width)
	headerWidth := m.contentWidthFor(screenWidth, m.styles.header)
	transcriptWidth := m.contentWidthFor(screenWidth, m.styles.transcript)
	actionWidth := m.contentWidthFor(screenWidth, m.styles.actionCard)
	composerWidth := m.contentWidthFor(screenWidth, m.activeComposerStyle())
	footerWidth := m.contentWidthFor(screenWidth, m.styles.footer)

	header := m.renderHeader(headerWidth)
	actionCard := m.renderActionCard(actionWidth)
	composer := m.renderComposer(composerWidth)
	footer := m.renderFooter(footerWidth)

	screenHeight := m.height
	if screenHeight <= 0 {
		screenHeight = 24
	}

	transcriptHeight := m.transcriptViewportHeight(header, actionCard, composer, footer, screenHeight)

	transcript := m.renderTranscript(transcriptWidth, transcriptHeight, header)

	sections := []string{transcript}
	if actionCard != "" {
		sections = append(sections, actionCard)
	}
	sections = append(sections, composer, footer)

	return m.styles.screen.
		Width(screenWidth).
		Render(lipgloss.JoinVertical(lipgloss.Left, sections...))
}

func (m Model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input)
	if text == "" || m.busy {
		return m, nil
	}

	m.input = ""
	m.currentHistory().record(text)

	if m.mode == AgentMode {
		if m.ctrl == nil {
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  "controller is not available",
			})
			return m, nil
		}

		m.busy = true
		prompt := text
		refining := m.refiningApproval
		m.refiningApproval = nil
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			var (
				events []controller.TranscriptEvent
				err    error
			)
			if refining != nil {
				events, err = m.ctrl.SubmitRefinement(ctx, *refining, prompt)
			} else {
				events, err = m.ctrl.SubmitAgentPrompt(ctx, prompt)
			}
			return controllerEventsMsg{
				events: events,
				err:    err,
			}
		}
	}

	if m.ctrl == nil {
		m.entries = append(m.entries, Entry{
			Title: "error",
			Body:  "controller is not available",
		})
		return m, nil
	}

	m.busy = true
	command := text
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		events, err := m.ctrl.SubmitShellCommand(ctx, command)
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}
}

func (m Model) transcriptLines() []string {
	lines := make([]string, 0, len(m.entries)*2)
	for _, entry := range m.entries {
		lines = append(lines, m.renderTag(entry.Title))
		for _, line := range strings.Split(entry.Body, "\n") {
			lines = append(lines, m.styles.body.Render("  "+line))
		}
	}

	return lines
}

func (m Model) renderHeader(width int) string {
	modeStyle := m.styles.modeShell
	if m.mode == AgentMode {
		modeStyle = m.styles.modeAgent
	}

	mode := modeStyle.Render(string(m.mode))
	meta := []string{m.styles.headerTitle.Render("Shuttle"), mode}
	switch {
	case width >= 100:
		meta = append(meta,
			m.styles.headerMeta.Render("session="+m.workspace.SessionName),
			m.styles.headerMeta.Render("top="+m.workspace.TopPane.ID),
		)
	case width >= 72:
		meta = append(meta, m.styles.headerMeta.Render("top="+m.workspace.TopPane.ID))
	}
	if m.busy {
		meta = append(meta, m.styles.modeBusy.Render("BUSY"))
	}
	if m.pendingApproval != nil {
		risk := strings.ToUpper(string(m.pendingApproval.Risk))
		if risk == "" {
			risk = "REVIEW"
		}
		meta = append(meta, m.styles.modeApproval.Render("APPROVAL "+risk))
	} else if m.refiningApproval != nil {
		meta = append(meta, m.styles.modeProposal.Render("REFINING"))
	} else if m.pendingProposal != nil && m.pendingProposal.Command != "" {
		meta = append(meta, m.styles.modeProposal.Render("PROPOSAL"))
	}

	status := m.styles.header.Render(strings.Join(meta, " "))
	ruleWidth := width - lipgloss.Width(status)
	if ruleWidth > 1 {
		status = lipgloss.JoinHorizontal(lipgloss.Left, status, m.styles.headerRule.Render(strings.Repeat("━", ruleWidth-1)))
	}

	return status
}

func (m Model) renderActionCard(width int) string {
	if m.pendingApproval != nil {
		body := []string{
			m.pendingApproval.Title,
			m.pendingApproval.Summary,
		}
		if m.pendingApproval.Command != "" {
			body = append(body, "command: "+m.pendingApproval.Command)
		}
		if m.pendingApproval.Risk != "" {
			body = append(body, "risk: "+string(m.pendingApproval.Risk))
		}
		body = append(body, "Ctrl+Y approve  Ctrl+N reject  Ctrl+R refine")
		content := lipgloss.JoinVertical(
			lipgloss.Left,
			m.styles.actionTitle.Render("Approval Required"),
			m.styles.actionBody.Render(strings.Join(body, "\n")),
		)
		return m.styles.actionCard.BorderForeground(lipgloss.Color("160")).Width(width).Render(content)
	}

	if m.refiningApproval != nil {
		body := []string{
			m.refiningApproval.Title,
			m.refiningApproval.Summary,
		}
		if m.refiningApproval.Command != "" {
			body = append(body, "command: "+m.refiningApproval.Command)
		}
		if m.refiningApproval.Risk != "" {
			body = append(body, "risk: "+string(m.refiningApproval.Risk))
		}
		body = append(body, "Enter a refinement note in the composer and press Enter")
		content := lipgloss.JoinVertical(
			lipgloss.Left,
			m.styles.actionTitle.Render("Refining Approval"),
			m.styles.actionBody.Render(strings.Join(body, "\n")),
		)
		return m.styles.actionCard.BorderForeground(lipgloss.Color("214")).Width(width).Render(content)
	}

	if m.pendingProposal != nil && m.pendingProposal.Command != "" {
		body := []string{}
		if m.pendingProposal.Description != "" {
			body = append(body, m.pendingProposal.Description)
		}
		body = append(body, "command: "+m.pendingProposal.Command)
		body = append(body, "Ctrl+E run command")
		content := lipgloss.JoinVertical(
			lipgloss.Left,
			m.styles.actionTitle.Render("Proposed Command"),
			m.styles.actionBody.Render(strings.Join(body, "\n")),
		)
		return m.styles.actionCard.BorderForeground(lipgloss.Color("31")).Width(width).Render(content)
	}

	return ""
}

func (m Model) renderTranscript(width int, height int, header string) string {
	lines := m.transcriptLines()
	if header != "" {
		headerLines := strings.Split(header, "\n")
		bodyHeight := height - len(headerLines)
		if bodyHeight < 0 {
			bodyHeight = 0
		}
		lines = append(headerLines, m.transcriptWindow(lines, bodyHeight)...)
	} else {
		lines = m.transcriptWindow(lines, height)
	}

	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}

	return renderWithSideRails(lines, width)
}

func (m Model) renderComposer(width int) string {
	input := m.input
	if input == "" {
		input = " "
	}

	lines := strings.Split(input, "\n")
	firstLine := lines[0]
	if firstLine == " " {
		firstLine = ""
	}

	badgeStyle := m.styles.composerBadgeShell
	composerStyle := m.styles.composerShell
	label := "SHELL"
	if m.refiningApproval != nil {
		badgeStyle = m.styles.composerBadgeRefine
		composerStyle = m.styles.composerRefine
		label = "REFINE"
	} else if m.mode == AgentMode {
		badgeStyle = m.styles.composerBadgeAgent
		composerStyle = m.styles.composerAgent
		label = "AGENT"
	}

	rendered := []string{
		lipgloss.JoinHorizontal(lipgloss.Left, badgeStyle.Render(label), m.styles.input.Render(" > "+firstLine)),
	}
	for _, line := range lines[1:] {
		rendered = append(rendered, m.styles.input.Render("   "+line))
	}

	return composerStyle.Width(width).Render(strings.Join(rendered, "\n"))
}

func (m Model) renderFooter(width int) string {
	parts := m.footerParts(width)
	return m.styles.footer.Width(width).Render(strings.Join(parts, "  "))
}

func (m Model) footerParts(width int) []string {
	switch {
	case width < 72:
		parts := []string{"[Tab]", "[Pg]", "[Enter]", "[Esc]", "[Ctrl+C]"}
		if m.pendingApproval != nil {
			parts = append(parts, "[Y/N/R]")
		} else if m.pendingProposal != nil && m.pendingProposal.Command != "" {
			parts = append(parts, "[Ctrl+E]")
		}
		return parts
	case width < 100:
		parts := []string{"[Tab] mode", "[PgUp/PgDn] scroll", "[Enter] submit", "[Esc] clear", "[Ctrl+C] quit"}
		if m.pendingApproval != nil {
			parts = append(parts, "[Ctrl+Y/N/R]")
		} else if m.pendingProposal != nil && m.pendingProposal.Command != "" {
			parts = append(parts, "[Ctrl+E] run")
		}
		return parts
	}

	parts := []string{"[Tab] mode", "[Up/Down] history", "[PgUp/PgDn] scroll", "[Ctrl+U/D] half-page", "[Home/End] bounds", "[Enter] submit", "[Esc] clear", "[Ctrl+J] newline"}
	if m.pendingProposal != nil && m.pendingProposal.Command != "" {
		parts = append(parts, "[Ctrl+E] run proposal")
	}
	if m.pendingApproval != nil {
		parts = append(parts, "[Ctrl+Y] approve", "[Ctrl+N] reject", "[Ctrl+R] refine")
	} else if m.refiningApproval != nil {
		parts = append(parts, "[Enter] submit refine note")
	}
	parts = append(parts, "[Ctrl+C] quit")
	return parts
}

func (m *Model) currentHistory() *composerHistory {
	if m.mode == AgentMode {
		return &m.agentHistory
	}

	return &m.shellHistory
}

func (m Model) transcriptViewportHeight(header string, actionCard string, composer string, footer string, screenHeight int) int {
	reservedHeight := lipgloss.Height(actionCard) + lipgloss.Height(composer) + lipgloss.Height(footer)
	transcriptChromeHeight := m.styles.transcript.GetVerticalFrameSize()
	transcriptHeight := screenHeight - reservedHeight - transcriptChromeHeight
	if transcriptHeight < 4 {
		transcriptHeight = 4
	}

	return transcriptHeight
}

func (m Model) transcriptWindow(lines []string, height int) []string {
	start := m.transcriptScroll
	maxStart := m.maxTranscriptScrollFor(lines, height)
	if m.transcriptFollow {
		start = maxStart
	}
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}

	end := start + height
	if end > len(lines) {
		end = len(lines)
	}

	window := append([]string(nil), lines[start:end]...)
	if len(window) < height {
		padding := make([]string, height-len(window))
		window = append(window, padding...)
	}

	return window
}

func (m Model) transcriptLineCount() int {
	return len(m.transcriptLines())
}

func (m Model) maxTranscriptScroll() int {
	return m.maxTranscriptScrollFor(m.transcriptLines(), m.currentTranscriptHeight())
}

func (m Model) maxTranscriptScrollFor(lines []string, height int) int {
	if len(lines) <= height {
		return 0
	}

	return len(lines) - height
}

func (m Model) currentTranscriptHeight() int {
	width := m.width
	if width <= 0 {
		width = 100
	}
	screenWidth := max(40, width)
	header := m.renderHeader(m.contentWidthFor(screenWidth, m.styles.header))
	actionCard := m.renderActionCard(m.contentWidthFor(screenWidth, m.styles.actionCard))
	composer := m.renderComposer(m.contentWidthFor(screenWidth, m.activeComposerStyle()))
	footer := m.renderFooter(m.contentWidthFor(screenWidth, m.styles.footer))

	screenHeight := m.height
	if screenHeight <= 0 {
		screenHeight = 24
	}

	return m.transcriptViewportHeight(header, actionCard, composer, footer, screenHeight)
}

func (m Model) activeComposerStyle() lipgloss.Style {
	if m.refiningApproval != nil {
		return m.styles.composerRefine
	}
	if m.mode == AgentMode {
		return m.styles.composerAgent
	}

	return m.styles.composerShell
}

func (m Model) contentWidthFor(totalWidth int, style lipgloss.Style) int {
	width := totalWidth - style.GetHorizontalFrameSize()
	if width < 10 {
		return 10
	}

	return width
}

func renderWithSideRails(lines []string, width int) string {
	innerWidth := width - 2
	if innerWidth < 0 {
		innerWidth = 0
	}

	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		renderedLine := lipgloss.NewStyle().Width(innerWidth).MaxWidth(innerWidth).Render(line)
		lineWidth := lipgloss.Width(renderedLine)
		if lineWidth < innerWidth {
			renderedLine += strings.Repeat(" ", innerWidth-lineWidth)
		}
		rendered = append(rendered, "│"+renderedLine+"│")
	}

	return strings.Join(rendered, "\n")
}

func (m *Model) clampTranscriptScroll() {
	maxScroll := m.maxTranscriptScroll()
	if m.transcriptScroll < 0 {
		m.transcriptScroll = 0
		return
	}
	if m.transcriptScroll > maxScroll {
		m.transcriptScroll = maxScroll
	}
}

func (m *Model) scrollTranscriptBy(delta int) {
	if m.transcriptFollow {
		m.transcriptScroll = m.maxTranscriptScroll()
	}
	m.transcriptScroll += delta
	m.clampTranscriptScroll()
	m.transcriptFollow = m.transcriptScroll >= m.maxTranscriptScroll()
}

func (m *Model) scrollTranscriptToTop() {
	m.transcriptScroll = 0
	m.transcriptFollow = false
}

func (m *Model) scrollTranscriptToBottom() {
	m.transcriptScroll = m.maxTranscriptScroll()
	m.transcriptFollow = true
}

func (m Model) isTranscriptPinned() bool {
	return m.transcriptFollow || m.transcriptScroll >= m.maxTranscriptScroll()
}

func (m Model) pageScrollSize() int {
	height := m.currentTranscriptHeight()
	if height <= 1 {
		return 1
	}

	return max(1, height-2)
}

func (m Model) halfPageScrollSize() int {
	return max(1, m.currentTranscriptHeight()/2)
}

func (m Model) renderTag(title string) string {
	text := strings.ToUpper(title)

	switch title {
	case "system":
		return m.styles.tagSystem.Render(text)
	case "user":
		return m.styles.tagShell.Render(text)
	case "shell":
		return m.styles.tagShell.Render(text)
	case "result":
		return m.styles.tagResult.Render(text)
	case "agent":
		return m.styles.tagAgent.Render(text)
	case "plan":
		return m.styles.tagAgent.Render(text)
	case "proposal":
		return m.styles.tagAgent.Render(text)
	case "approval":
		return m.styles.tagError.Render(text)
	case "error":
		return m.styles.tagError.Render(text)
	default:
		return m.styles.tagSystem.Render(text)
	}
}

func (m Model) runProposalCommand() (tea.Model, tea.Cmd) {
	if m.busy || m.ctrl == nil || m.pendingProposal == nil || m.pendingProposal.Command == "" {
		return m, nil
	}

	m.busy = true
	m.proposalRunPending = true
	command := m.pendingProposal.Command

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		events, err := m.ctrl.SubmitShellCommand(ctx, command)
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}
}

func (m Model) decideApproval(decision controller.ApprovalDecision) (tea.Model, tea.Cmd) {
	if m.busy || m.ctrl == nil || m.pendingApproval == nil {
		return m, nil
	}

	m.busy = true
	m.approvalInFlight = true
	approvalID := m.pendingApproval.ID

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		events, err := m.ctrl.DecideApproval(ctx, approvalID, decision, "")
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}
}

func (m Model) refineApproval() (tea.Model, tea.Cmd) {
	if m.busy || m.ctrl == nil || m.pendingApproval == nil {
		return m, nil
	}

	approval := *m.pendingApproval
	m.refiningApproval = &approval
	m.input = ""
	m.mode = AgentMode
	m.busy = true
	m.approvalInFlight = true
	approvalID := m.pendingApproval.ID

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		events, err := m.ctrl.DecideApproval(ctx, approvalID, controller.DecisionRefine, "")
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}
}

func (m *Model) syncActionState(events []controller.TranscriptEvent) {
	newApproval := latestApproval(events)
	if newApproval != nil {
		m.pendingApproval = newApproval
		m.refiningApproval = nil
		m.pendingProposal = nil
	}

	newProposal := latestProposal(events)
	if newProposal != nil {
		m.pendingProposal = newProposal
		if newApproval == nil {
			m.pendingApproval = nil
		}
	}

	if m.approvalInFlight && !containsEventKind(events, controller.EventApproval) {
		m.pendingApproval = nil
	}
	if m.proposalRunPending {
		m.pendingProposal = nil
	}

	m.approvalInFlight = false
	m.proposalRunPending = false
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func latestApproval(events []controller.TranscriptEvent) *controller.ApprovalRequest {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != controller.EventApproval {
			continue
		}

		payload, ok := events[index].Payload.(controller.ApprovalRequest)
		if !ok {
			continue
		}

		approval := payload
		return &approval
	}

	return nil
}

func latestProposal(events []controller.TranscriptEvent) *controller.ProposalPayload {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != controller.EventProposal {
			continue
		}

		payload, ok := events[index].Payload.(controller.ProposalPayload)
		if !ok || payload.Command == "" {
			continue
		}

		proposal := payload
		return &proposal
	}

	return nil
}

type composerHistory struct {
	entries  []string
	index    int
	draft    string
	browsing bool
}

func (h *composerHistory) record(value string) {
	if value == "" {
		h.reset()
		return
	}

	if len(h.entries) == 0 || h.entries[len(h.entries)-1] != value {
		h.entries = append(h.entries, value)
	}

	h.reset()
}

func (h *composerHistory) previous(current string) string {
	if len(h.entries) == 0 {
		return current
	}

	if !h.browsing {
		h.draft = current
		h.index = len(h.entries) - 1
		h.browsing = true
		return h.entries[h.index]
	}

	if h.index > 0 {
		h.index--
	}

	return h.entries[h.index]
}

func (h *composerHistory) next(current string) string {
	if len(h.entries) == 0 || !h.browsing {
		return current
	}

	if h.index < len(h.entries)-1 {
		h.index++
		return h.entries[h.index]
	}

	draft := h.draft
	h.reset()
	return draft
}

func (h *composerHistory) reset() {
	h.index = 0
	h.draft = ""
	h.browsing = false
}

func eventsToEntries(events []controller.TranscriptEvent) []Entry {
	entries := make([]Entry, 0, len(events))
	for _, event := range events {
		switch event.Kind {
		case controller.EventUserMessage:
			payload, _ := event.Payload.(controller.TextPayload)
			entries = append(entries, Entry{Title: "user", Body: payload.Text})
		case controller.EventAgentMessage:
			payload, _ := event.Payload.(controller.TextPayload)
			entries = append(entries, Entry{Title: "agent", Body: payload.Text})
		case controller.EventPlan:
			payload, _ := event.Payload.(controller.PlanPayload)
			body := payload.Summary
			if len(payload.Steps) > 0 {
				stepLines := make([]string, 0, len(payload.Steps))
				for index, step := range payload.Steps {
					stepLines = append(stepLines, fmt.Sprintf("%d. %s", index+1, step))
				}
				if body != "" {
					body += "\n"
				}
				body += strings.Join(stepLines, "\n")
			}
			entries = append(entries, Entry{Title: "plan", Body: body})
		case controller.EventProposal:
			payload, _ := event.Payload.(controller.ProposalPayload)
			bodyParts := make([]string, 0, 3)
			if payload.Description != "" {
				bodyParts = append(bodyParts, payload.Description)
			}
			if payload.Command != "" {
				bodyParts = append(bodyParts, "command: "+payload.Command)
			}
			if payload.Patch != "" {
				bodyParts = append(bodyParts, payload.Patch)
			}
			entries = append(entries, Entry{Title: "proposal", Body: strings.Join(bodyParts, "\n")})
		case controller.EventApproval:
			payload, _ := event.Payload.(controller.ApprovalRequest)
			bodyParts := []string{payload.Title, payload.Summary}
			if payload.Command != "" {
				bodyParts = append(bodyParts, "command: "+payload.Command)
			}
			if payload.Risk != "" {
				bodyParts = append(bodyParts, "risk: "+string(payload.Risk))
			}
			entries = append(entries, Entry{Title: "approval", Body: strings.Join(bodyParts, "\n")})
		case controller.EventCommandStart:
			payload, _ := event.Payload.(controller.CommandStartPayload)
			entries = append(entries, Entry{Title: "shell", Body: payload.Command})
		case controller.EventCommandResult:
			payload, _ := event.Payload.(controller.CommandResultSummary)
			body := strings.TrimSpace(payload.Summary)
			if body == "" {
				body = "(no output)"
			}
			entries = append(entries, Entry{
				Title: "result",
				Body:  fmt.Sprintf("exit=%d\n%s", payload.ExitCode, body),
			})
		case controller.EventSystemNotice:
			payload, _ := event.Payload.(controller.TextPayload)
			entries = append(entries, Entry{Title: "system", Body: payload.Text})
		case controller.EventError:
			payload, _ := event.Payload.(controller.TextPayload)
			entries = append(entries, Entry{Title: "error", Body: payload.Text})
		}
	}
	return entries
}

func containsEventKind(events []controller.TranscriptEvent, kind controller.TranscriptEventKind) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

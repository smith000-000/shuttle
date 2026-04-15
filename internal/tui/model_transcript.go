package tui

import (
	"fmt"
	"os"
	"strings"

	"aiterm/internal/controller"
	"aiterm/internal/provider"
	"aiterm/internal/shell"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

const transcriptSelectedBackground = "236"

func (m Model) transcriptLines(width int) []string {
	displayLines := m.transcriptDisplayLines(width)
	lines := make([]string, 0, len(displayLines))
	for _, line := range displayLines {
		lines = append(lines, line.text)
	}

	return lines
}

func (m Model) transcriptDisplayLines(width int) []transcriptRenderLine {
	lines := make([]transcriptRenderLine, 0, len(m.entries)*2)
	for index, entry := range m.entries {
		lines = append(lines, m.renderEntryLines(index, entry, width)...)
	}

	return lines
}

func (m Model) renderEntryLines(index int, entry Entry, width int) []transcriptRenderLine {
	if entry.Hidden {
		return nil
	}
	prefix := "  "
	if entry.Title == "result" && strings.TrimSpace(entry.Command) != "" {
		return m.renderResultEntryLines(index, entry, width, prefix)
	}

	selected := index == m.selectedEntry
	tag := m.renderTag(entry, selected)
	tagStart := lipgloss.Width(prefix)
	tagEnd := tagStart + lipgloss.Width(tag)
	tagWidth := lipgloss.Width(tag)
	bodyWidth := max(10, width-lipgloss.Width(prefix)-tagWidth-1)
	bodyStyle := m.renderBodyStyle(entry)
	prefixStyle := lipgloss.NewStyle()
	if selected {
		prefixStyle = prefixStyle.Background(lipgloss.Color(transcriptSelectedBackground))
	}
	indent := strings.Repeat(" ", lipgloss.Width(prefix)+tagWidth+1)

	rawLines := strings.Split(entry.Body, "\n")
	if len(rawLines) == 0 {
		rawLines = []string{""}
	}

	rendered := make([]transcriptRenderLine, 0, len(rawLines))
	firstBody := wrapText(rawLines[0], bodyWidth)
	if len(firstBody) == 0 {
		firstBody = []string{""}
	}
	rendered = append(rendered, transcriptRenderLine{
		text:       prefixStyle.Render(prefix) + tag + " " + bodyStyle.Render(firstBody[0]),
		entryIndex: index,
		tagStart:   tagStart,
		tagEnd:     tagEnd,
	})
	for _, wrapped := range firstBody[1:] {
		rendered = append(rendered, transcriptRenderLine{text: prefixStyle.Render(indent) + bodyStyle.Render(wrapped), entryIndex: index, tagStart: -1, tagEnd: -1})
	}

	for _, rawLine := range rawLines[1:] {
		wrappedLines := wrapText(rawLine, bodyWidth)
		if len(wrappedLines) == 0 {
			wrappedLines = []string{""}
		}
		for _, wrapped := range wrappedLines {
			rendered = append(rendered, transcriptRenderLine{text: prefixStyle.Render(indent) + bodyStyle.Render(wrapped), entryIndex: index, tagStart: -1, tagEnd: -1})
		}
	}

	return rendered
}

func (m Model) renderResultEntryLines(index int, entry Entry, width int, prefix string) []transcriptRenderLine {
	selected := index == m.selectedEntry
	prefixWidth := lipgloss.Width(prefix)
	blockWidth := max(10, width-prefixWidth)
	innerWidth := max(8, blockWidth-2)
	command := strings.TrimSpace(entry.Command)
	if command == "" {
		command = "(unknown command)"
	}

	headerContent := m.renderResultTag(entry, false) + " " + command
	commandExpandable := xansi.StringWidth(headerContent) > min(innerWidth, m.resultCommandThreshold(width))
	commandText := headerContent
	if commandExpandable && m.expandedCommandEntry != index {
		commandText = xansi.Truncate(headerContent, innerWidth, "…")
	}

	commandLines := wrapText(commandText, innerWidth)
	if len(commandLines) == 0 {
		commandLines = []string{""}
	}

	prefixStyle := lipgloss.NewStyle()
	if selected {
		prefixStyle = prefixStyle.Background(lipgloss.Color(transcriptSelectedBackground))
	}

	type resultBoxLine struct {
		text             string
		style            lipgloss.Style
		commandClickable bool
		detailClickable  bool
	}

	boxLines := make([]resultBoxLine, 0, len(commandLines)+4)
	for lineIndex, line := range commandLines {
		boxLines = append(boxLines, resultBoxLine{
			text:             line,
			style:            m.styles.resultHeader,
			commandClickable: lineIndex == 0 && commandExpandable,
		})
	}

	if body := strings.TrimSpace(entry.Body); body != "" {
		for _, rawLine := range strings.Split(body, "\n") {
			wrappedLines := wrapText(rawLine, innerWidth)
			if len(wrappedLines) == 0 {
				wrappedLines = []string{""}
			}
			for _, wrapped := range wrappedLines {
				boxLines = append(boxLines, resultBoxLine{text: wrapped, style: m.styles.resultOutput})
			}
		}
	}

	limit := m.resultDisplayLineLimit()
	if len(boxLines) > limit {
		hidden := len(boxLines) - (limit - 1)
		boxLines = append(boxLines[:limit-1], resultBoxLine{
			text:            fmt.Sprintf("... (%d more lines, Ctrl+O to inspect)", hidden),
			style:           m.styles.resultOutput,
			detailClickable: true,
		})
	}

	rendered := make([]transcriptRenderLine, 0, len(boxLines)+1)
	for lineIndex, line := range boxLines {
		leftBorder := "│"
		rightBorder := "│"
		if lineIndex == 0 {
			leftBorder = "┌"
			rightBorder = "┐"
		}

		renderedLine := transcriptRenderLine{
			text:       prefixStyle.Render(prefix) + m.styles.resultBorder.Render(leftBorder) + line.style.Render(padANSIWidth(line.text, innerWidth)) + m.styles.resultBorder.Render(rightBorder),
			entryIndex: index,
			tagStart:   -1,
			tagEnd:     -1,
		}
		if line.commandClickable {
			renderedLine.commandStart = prefixWidth + 1
			renderedLine.commandEnd = renderedLine.commandStart + innerWidth
			renderedLine.commandClickable = true
		}
		if line.detailClickable {
			renderedLine.detailStart = prefixWidth + 1
			renderedLine.detailEnd = renderedLine.detailStart + xansi.StringWidth(line.text)
			renderedLine.detailClickable = true
		}
		rendered = append(rendered, renderedLine)
	}

	rendered = append(rendered, transcriptRenderLine{
		text:       prefixStyle.Render(prefix) + m.styles.resultBorder.Render("└"+strings.Repeat("─", innerWidth)+"┘"),
		entryIndex: index,
		tagStart:   -1,
		tagEnd:     -1,
	})
	return rendered
}

func padANSIWidth(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if xansi.StringWidth(value) > width {
		return xansi.Truncate(value, width, "…")
	}
	padding := width - xansi.StringWidth(value)
	if padding <= 0 {
		return value
	}
	return value + strings.Repeat(" ", padding)
}

func (m Model) renderBodyStyle(entry Entry) lipgloss.Style {
	switch entry.Title {
	case "system":
		return m.styles.bodySystem
	case "user", "shell":
		return m.styles.bodyShell
	case "result":
		if entry.TagKind == entryTagResultSuccess || entry.TagKind == entryTagDefault {
			return m.styles.bodyResult
		}
		return m.styles.bodyError
	case "agent", "plan", "proposal":
		return m.styles.bodyAgent
	case "approval", "error":
		return m.styles.bodyError
	default:
		return m.styles.bodyShell
	}
}

func (m Model) renderTranscript(width int, height int) string {
	displayLines := m.transcriptWindowDisplay(m.transcriptDisplayLines(width), height)
	lines := make([]string, 0, len(displayLines))
	for _, line := range displayLines {
		text := line.text
		if text == "" {
			text = strings.Repeat(" ", max(1, width))
		}
		lines = append(lines, m.styles.transcript.Width(width).MaxWidth(width).Render(text))
	}
	return strings.Join(lines, "\n")
}

func (m Model) transcriptViewportHeight(actionCard string, planCard string, activeExecutionCard string, statusLine string, shellTail string, composer string, footer string, screenHeight int) int {
	reservedHeight := lipgloss.Height(actionCard) + lipgloss.Height(planCard) + lipgloss.Height(activeExecutionCard) + lipgloss.Height(statusLine) + lipgloss.Height(shellTail) + lipgloss.Height(composer) + lipgloss.Height(footer)
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
		window = append(padding, window...)
	}

	return window
}

func (m Model) transcriptWindowDisplay(lines []transcriptRenderLine, height int) []transcriptRenderLine {
	start := m.transcriptScroll
	maxStart := m.maxTranscriptScrollForDisplay(lines, height)
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

	window := append([]transcriptRenderLine(nil), lines[start:end]...)
	if len(window) < height {
		padding := make([]transcriptRenderLine, 0, height-len(window))
		for i := len(window); i < height; i++ {
			padding = append(padding, transcriptRenderLine{entryIndex: -1})
		}
		window = append(padding, window...)
	}

	return window
}

func (m Model) transcriptLineCount() int {
	return len(m.transcriptLines(m.currentTranscriptWidth()))
}

func (m Model) maxTranscriptScroll() int {
	return m.maxTranscriptScrollFor(m.transcriptLines(m.currentTranscriptWidth()), m.currentTranscriptHeight())
}

func (m Model) maxTranscriptScrollForDisplay(lines []transcriptRenderLine, height int) int {
	if len(lines) <= height {
		return 0
	}

	return len(lines) - height
}

func (m Model) maxTranscriptScrollFor(lines []string, height int) int {
	if len(lines) <= height {
		return 0
	}

	return len(lines) - height
}

func (m Model) currentTranscriptHeight() int {
	if m.detailOpen {
		return max(4, m.height-4)
	}

	width := m.currentTranscriptWidth()
	actionCard := m.renderActionCard(m.contentWidthFor(width, m.styles.actionCard))
	planCard := m.renderPlanCard(m.contentWidthFor(width, m.styles.actionCard))
	activeExecutionCard := m.renderActiveExecutionCard(m.contentWidthFor(width, m.styles.actionCard))
	statusLine := m.renderStatusLine(m.contentWidthFor(width, m.styles.status))
	shellTail := m.renderShellTail(m.contentWidthFor(width, m.styles.tail))
	composer := m.renderComposer(width)
	footer := m.renderFooter(width)

	screenHeight := m.height
	if screenHeight <= 0 {
		screenHeight = 24
	}

	return m.resolvedTranscriptHeight(width, screenHeight, actionCard, planCard, activeExecutionCard, statusLine, shellTail, composer, footer)
}

func (m Model) currentTranscriptWidth() int {
	width := m.width
	if width <= 0 {
		width = 100
	}

	return max(10, width)
}

func (m Model) selectedEntryValue() Entry {
	if len(m.entries) == 0 {
		return Entry{}
	}

	index := m.selectedEntry
	if index < 0 {
		index = 0
	}
	if index >= len(m.entries) {
		index = len(m.entries) - 1
	}

	return m.entries[index]
}

func (e Entry) DetailBody() string {
	if strings.TrimSpace(e.Detail) != "" {
		return e.Detail
	}

	return e.Body
}

func (m *Model) clampSelection() {
	if len(m.entries) == 0 {
		m.selectedEntry = 0
		return
	}
	if m.selectedEntry < 0 {
		m.selectedEntry = 0
	}
	if m.selectedEntry >= len(m.entries) {
		m.selectedEntry = len(m.entries) - 1
	}
}

func (m *Model) selectPreviousEntry() {
	m.clampSelection()
	if m.selectedEntry > 0 {
		m.selectedEntry--
		m.ensureSelectedEntryVisible(-1)
	}
}

func (m *Model) selectNextEntry() {
	m.clampSelection()
	if m.selectedEntry < len(m.entries)-1 {
		m.selectedEntry++
		m.ensureSelectedEntryVisible(1)
	}
}

func (m Model) rawTranscriptViewportHeight() int {
	width := m.currentTranscriptWidth()
	actionCard := m.renderActionCard(m.contentWidthFor(width, m.styles.actionCard))
	planCard := m.renderPlanCard(m.contentWidthFor(width, m.styles.actionCard))
	activeExecutionCard := m.renderActiveExecutionCard(m.contentWidthFor(width, m.styles.actionCard))
	statusLine := m.renderStatusLine(m.contentWidthFor(width, m.styles.status))
	shellTail := m.renderShellTail(m.contentWidthFor(width, m.styles.tail))
	composer := m.renderComposer(width)
	footer := m.renderFooter(width)

	screenHeight := m.height
	if screenHeight <= 0 {
		screenHeight = 24
	}

	return m.transcriptViewportHeight(actionCard, planCard, activeExecutionCard, statusLine, shellTail, composer, footer, screenHeight)
}

func (m Model) resultDisplayLineLimit() int {
	return max(4, m.rawTranscriptViewportHeight()*3)
}

func (m Model) resultCommandThreshold(width int) int {
	return max(10, width*9/10)
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Shift {
		return m, nil
	}

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scrollTranscriptBy(-3)
		return m, nil
	case tea.MouseButtonWheelDown:
		m.scrollTranscriptBy(3)
		return m, nil
	}

	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	if m.mouseOnShellTailLabel(msg.X, msg.Y) {
		return m.takeControlPersistentShellNow()
	}
	if action, ok := m.actionCardActionAtMouse(msg.X, msg.Y); ok {
		return m.performActionCardAction(action)
	}

	entryIndex, ok := m.transcriptTagEntryAtMouse(msg.X, msg.Y)
	if ok {
		m.selectedEntry = entryIndex
		m.clampSelection()
		m.ensureSelectedEntryVisible(0)
		return m.openDetail()
	}

	entryIndex, ok = m.transcriptDetailEntryAtMouse(msg.X, msg.Y)
	if ok {
		m.selectedEntry = entryIndex
		m.clampSelection()
		m.ensureSelectedEntryVisible(0)
		return m.openDetail()
	}

	entryIndex, ok = m.transcriptCommandEntryAtMouse(msg.X, msg.Y)
	if !ok {
		return m, nil
	}

	m.selectedEntry = entryIndex
	m.clampSelection()
	m.ensureSelectedEntryVisible(0)
	if m.expandedCommandEntry == entryIndex {
		m.expandedCommandEntry = -1
		return m, nil
	}
	m.expandedCommandEntry = entryIndex
	return m, nil
}

func (m Model) mouseInTranscript(x int, y int) bool {
	if x < 0 || y < 0 || x >= m.currentTranscriptWidth() {
		return false
	}

	lines := m.transcriptWindowDisplay(m.transcriptDisplayLines(m.currentTranscriptWidth()), m.currentTranscriptHeight())
	if y >= len(lines) {
		return false
	}

	return lines[y].entryIndex >= 0
}

func (m Model) transcriptTagEntryAtMouse(x int, y int) (int, bool) {
	if !m.mouseInTranscript(x, y) {
		return 0, false
	}

	lines := m.transcriptWindowDisplay(m.transcriptDisplayLines(m.currentTranscriptWidth()), m.currentTranscriptHeight())
	line := lines[y]
	if line.tagStart < 0 || line.tagEnd <= line.tagStart || x < line.tagStart || x >= line.tagEnd {
		return 0, false
	}

	entryIndex := line.entryIndex
	if entryIndex < 0 || entryIndex >= len(m.entries) {
		return 0, false
	}

	return entryIndex, true
}

func (m Model) transcriptCommandEntryAtMouse(x int, y int) (int, bool) {
	if !m.mouseInTranscript(x, y) {
		return 0, false
	}

	lines := m.transcriptWindowDisplay(m.transcriptDisplayLines(m.currentTranscriptWidth()), m.currentTranscriptHeight())
	line := lines[y]
	if !line.commandClickable || line.commandStart < 0 || line.commandEnd <= line.commandStart || x < line.commandStart || x >= line.commandEnd {
		return 0, false
	}

	entryIndex := line.entryIndex
	if entryIndex < 0 || entryIndex >= len(m.entries) {
		return 0, false
	}

	return entryIndex, true
}

func (m Model) transcriptDetailEntryAtMouse(x int, y int) (int, bool) {
	if !m.mouseInTranscript(x, y) {
		return 0, false
	}

	lines := m.transcriptWindowDisplay(m.transcriptDisplayLines(m.currentTranscriptWidth()), m.currentTranscriptHeight())
	line := lines[y]
	if !line.detailClickable || line.detailStart < 0 || line.detailEnd <= line.detailStart || x < line.detailStart || x >= line.detailEnd {
		return 0, false
	}

	entryIndex := line.entryIndex
	if entryIndex < 0 || entryIndex >= len(m.entries) {
		return 0, false
	}

	return entryIndex, true
}

func (m *Model) ensureSelectedEntryVisible(direction int) {
	firstLine, lastLine, ok := m.selectedEntryLineBounds()
	if !ok {
		return
	}

	height := m.currentTranscriptHeight()
	if height <= 0 {
		return
	}

	start := m.transcriptScroll
	if m.transcriptFollow {
		start = m.maxTranscriptScroll()
	}
	end := start + height - 1

	switch {
	case direction < 0 && firstLine < start:
		m.transcriptScroll = firstLine
		m.transcriptFollow = false
	case direction > 0 && lastLine > end:
		m.transcriptScroll = max(0, lastLine-height+1)
		m.transcriptFollow = false
	case direction == 0 && (firstLine < start || lastLine > end):
		m.transcriptScroll = max(0, min(firstLine, max(0, lastLine-height+1)))
		m.transcriptFollow = false
	}
	m.clampTranscriptScroll()
}

func (m Model) selectedEntryLineBounds() (int, int, bool) {
	lines := m.transcriptDisplayLines(m.currentTranscriptWidth())
	firstLine := -1
	lastLine := -1
	for index, line := range lines {
		if line.entryIndex != m.selectedEntry {
			continue
		}
		if firstLine < 0 {
			firstLine = index
		}
		lastLine = index
	}
	if firstLine < 0 {
		return 0, 0, false
	}
	return firstLine, lastLine, true
}

func (m Model) actionCardActionAtMouse(x int, y int) (actionCardAction, bool) {
	spec := m.currentActionCardSpec()
	if spec == nil || len(spec.buttons) == 0 || x < 0 || y < 0 {
		return "", false
	}

	startY, ok := m.actionCardStartY()
	if !ok {
		return "", false
	}

	contentWidth := m.contentWidthFor(m.currentTranscriptWidth(), m.styles.actionCard)
	bodyLines := actionCardBodyLines(spec.body, contentWidth)
	buttonLines := layoutActionCardButtons(spec.buttons, contentWidth)
	lineIndex := y - (startY + m.styles.actionCard.GetBorderTopSize() + 1 + len(bodyLines))
	if lineIndex < 0 || lineIndex >= len(buttonLines) {
		return "", false
	}

	textX := x - (m.styles.actionCard.GetBorderLeftSize() + m.styles.actionCard.GetPaddingLeft())
	if textX < 0 {
		return "", false
	}

	for _, hit := range buttonLines[lineIndex].hits {
		if textX >= hit.start && textX < hit.end {
			return hit.action, true
		}
	}

	return "", false
}

func (m Model) actionCardStartY() (int, bool) {
	if m.currentActionCardSpec() == nil {
		return 0, false
	}
	return m.currentTranscriptHeight(), true
}

func (m Model) performActionCardAction(action actionCardAction) (tea.Model, tea.Cmd) {
	switch action {
	case actionCardContinueStartup:
		m.startupNotice = nil
		return m, nil
	case actionCardConfirmDangerous:
		return m.confirmDangerousApprovalMode()
	case actionCardCancelDangerous:
		m.pendingDangerousConfirm = nil
		return m, nil
	case actionCardConfirmFullscreen:
		return m.confirmFullscreenAction()
	case actionCardCancelFullscreen:
		m.pendingFullscreen = nil
		return m, nil
	case actionCardTakeControl:
		if m.executionTakeControlConfig().enabled() {
			return m.takeControlExecutionNow()
		}
		return m.takeControlPersistentShellNow()
	case actionCardResumeInteractive:
		if m.pendingApproval == nil && m.pendingProposal == nil && m.pendingContinueAfterCommand {
			return m.continueAfterLatestCommand()
		}
		return m.resumePausedInteractiveCheckIns()
	case actionCardApprove:
		return m.primaryAction()
	case actionCardReject:
		if m.pendingProposal != nil {
			return m.rejectProposal()
		}
		return m.decideApproval(controller.DecisionReject)
	case actionCardRefine:
		if m.pendingProposal != nil {
			return m.refineProposal()
		}
		if m.pendingApproval == nil && m.pendingProposal == nil && m.pendingContinueAfterCommand {
			return m.focusAgentComposerForActiveExecution()
		}
		if m.pendingApproval == nil && m.interactiveCheckInPaused && executionNeedsUserDrivenResume(m.activeExecution) {
			return m.focusAgentComposerForActiveExecution()
		}
		return m.refineApproval()
	case actionCardEditProposal:
		if m.pendingProposal != nil {
			return m.editProposalCommand()
		}
	}

	return m, nil
}

func (m Model) mouseOnShellTailLabel(x int, y int) bool {
	if x < 0 || y < 0 {
		return false
	}

	startY, ok := m.shellTailStartY()
	if !ok {
		return false
	}
	if y != startY {
		return false
	}

	labelStart := m.styles.tail.GetHorizontalPadding()
	labelEnd := labelStart + lipgloss.Width(m.styles.tailLabel.Render("shell"))
	return x >= labelStart && x < labelEnd
}

func (m Model) shellTailStartY() (int, bool) {
	if !m.showShellTail || m.activeExecution != nil && m.activeExecution.State == controller.CommandExecutionInteractiveFullscreen {
		return 0, false
	}
	if strings.TrimSpace(m.liveShellTail) == "" {
		return 0, false
	}

	width := m.currentTranscriptWidth()
	actionWidth := m.contentWidthFor(width, m.styles.actionCard)
	statusWidth := m.contentWidthFor(width, m.styles.status)

	y := m.currentTranscriptHeight()
	if actionCard := m.renderActionCard(actionWidth); actionCard != "" {
		y += lipgloss.Height(actionCard)
	}
	if planCard := m.renderPlanCard(actionWidth); planCard != "" {
		y += lipgloss.Height(planCard)
	}
	if activeExecutionCard := m.renderActiveExecutionCard(actionWidth); activeExecutionCard != "" {
		y += lipgloss.Height(activeExecutionCard)
	}
	if statusLine := m.renderStatusLine(statusWidth); statusLine != "" {
		y += lipgloss.Height(statusLine)
	}

	return y, true
}

func (m Model) openDetail() (tea.Model, tea.Cmd) {
	if len(m.entries) == 0 {
		return m, nil
	}

	m.clampSelection()
	m.detailOpen = true
	m.detailScroll = 0
	m.detailFilter = ""
	m.clampDetailScroll()
	return m, nil
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyF2:
		return m.takeControlPersistentShellNow()
	case tea.KeyF3:
		return m.takeControlExecutionNow()
	case tea.KeyEsc:
		if strings.TrimSpace(m.detailFilter) != "" {
			m.detailFilter = ""
			m.detailScroll = 0
			m.clampDetailScroll()
			return m, nil
		}
		m.detailOpen = false
		m.detailScroll = 0
		m.detailFilter = ""
		return m, nil
	case tea.KeyUp:
		if m.detailScroll > 0 {
			m.detailScroll--
		}
		return m, nil
	case tea.KeyDown:
		m.detailScroll++
		m.clampDetailScroll()
		return m, nil
	case tea.KeyPgUp:
		m.detailScroll -= m.detailPageSize()
		m.clampDetailScroll()
		return m, nil
	case tea.KeyPgDown:
		m.detailScroll += m.detailPageSize()
		m.clampDetailScroll()
		return m, nil
	case tea.KeyHome:
		m.detailScroll = 0
		return m, nil
	case tea.KeyEnd:
		m.detailScroll = m.maxDetailScroll()
		return m, nil
	case tea.KeyBackspace:
		if strings.TrimSpace(m.detailFilter) == "" {
			return m, nil
		}
		m.detailFilter = trimLastRune(m.detailFilter)
		m.detailScroll = 0
		m.clampDetailScroll()
		return m, nil
	case tea.KeyDelete:
		if strings.TrimSpace(m.detailFilter) == "" {
			return m, nil
		}
		m.detailFilter = ""
		m.detailScroll = 0
		m.clampDetailScroll()
		return m, nil
	case tea.KeyRunes:
		if msg.Alt || len(msg.Runes) == 0 {
			return m, nil
		}
		inserted := string(msg.Runes)
		if strings.TrimSpace(inserted) == "" && strings.TrimSpace(m.detailFilter) == "" {
			return m, nil
		}
		m.detailFilter += inserted
		m.detailScroll = 0
		m.clampDetailScroll()
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) renderDetailFooter(width int) string {
	left := "Type filter  Esc close  Up/Down scroll  PgUp/PgDn page"
	if strings.TrimSpace(m.detailFilter) != "" {
		left = "Type filter  Backspace edit  Esc clear/close"
	}
	right := m.detailScrollIndicator()
	if right == "" {
		return left
	}

	padding := width - lipgloss.Width(left) - lipgloss.Width(right)
	if padding < 1 {
		return left + " " + right
	}

	return left + strings.Repeat(" ", padding) + right
}

func (m Model) detailScrollIndicator() string {
	maxScroll := m.maxDetailScroll()
	if maxScroll <= 0 {
		return ""
	}

	switch {
	case m.detailScroll <= 0:
		return "↓"
	case m.detailScroll >= maxScroll:
		return "↑"
	default:
		return "↑↓"
	}
}

func (m Model) detailBodyLines(contentWidth int) ([]string, int, bool) {
	entry := m.selectedEntryValue()
	body := strings.ReplaceAll(entry.DetailBody(), "\r\n", "\n")
	rawLines := strings.Split(body, "\n")
	filter := strings.ToLower(strings.TrimSpace(m.detailFilter))
	filtered := make([]string, 0, len(rawLines))
	matchCount := 0
	if filter == "" {
		filtered = rawLines
	} else {
		for _, line := range rawLines {
			if strings.Contains(strings.ToLower(line), filter) {
				filtered = append(filtered, line)
				matchCount++
			}
		}
	}
	if len(filtered) == 0 {
		if filter == "" {
			filtered = []string{""}
		} else {
			return []string{"No detail lines match the current filter."}, 0, true
		}
	}

	lines := make([]string, 0, len(filtered)*2)
	for _, line := range filtered {
		wrapped := wrapText(line, max(10, contentWidth))
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		lines = append(lines, wrapped...)
	}
	if filter == "" {
		matchCount = len(rawLines)
	}
	return lines, matchCount, false
}

func detailWindow(lines []string, start int, height int) []string {
	if start < 0 {
		start = 0
	}
	if start > len(lines) {
		start = len(lines)
	}
	end := start + height
	if end > len(lines) {
		end = len(lines)
	}

	window := append([]string(nil), lines[start:end]...)
	for len(window) < height {
		window = append(window, "")
	}

	return window
}

func compactResultPreview(body string, maxLines int) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 {
		return "(no output)"
	}

	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}

	preview := append([]string(nil), lines[:maxLines]...)
	preview = append(preview, fmt.Sprintf("... (%d more lines, Ctrl+O to inspect)", len(lines)-maxLines))
	return strings.Join(preview, "\n")
}

func formatResultDetail(command string, exitCode int, output string) string {
	command = strings.TrimSpace(command)
	output = strings.TrimSpace(output)
	if output == "" {
		output = "(no output)"
	}

	sections := []string{
		"command:",
		command,
		"",
		fmt.Sprintf("exit=%d", exitCode),
		"",
		output,
	}

	return strings.Join(sections, "\n")
}

func resultSummaryHasVisibleOutput(summary string) bool {
	trimmed := strings.TrimSpace(summary)
	return trimmed != "" && trimmed != "(no output)"
}

func commandLikelyChangesDirectory(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return false
	}

	switch fields[0] {
	case "cd", "pushd", "popd":
		return true
	default:
		return false
	}
}

func silentSuccessTranscriptBody(payload controller.CommandResultSummary) string {
	if payload.ExitCode != 0 || payload.State != controller.CommandExecutionCompleted {
		return ""
	}
	if resultSummaryHasVisibleOutput(commandResultDisplaySummary(&payload)) {
		return ""
	}
	if payload.ShellContext != nil && commandLikelyChangesDirectory(payload.Command) {
		return strings.TrimSpace(payload.ShellContext.Directory)
	}
	return ""
}

func classifyResultTagKind(payload controller.CommandResultSummary) entryTagKind {
	if payload.State == controller.CommandExecutionLost {
		return entryTagResultFatal
	}

	switch payload.ExitCode {
	case 0:
		return entryTagResultSuccess
	case shell.InterruptedExitCode:
		return entryTagResultSigInt
	case 126:
		return entryTagResultNoExec
	case 127:
		return entryTagResultNotFound
	case 255:
		return entryTagResultFatal
	}

	switch {
	case payload.ExitCode >= 1 && payload.ExitCode <= 125:
		return entryTagResultError
	case payload.ExitCode >= 128 && payload.ExitCode <= 165:
		return entryTagResultSignal
	case payload.ExitCode >= 166 && payload.ExitCode <= 254:
		return entryTagResultCustom
	default:
		return entryTagResultError
	}
}

func compactPlanEntry(summary string, steps []controller.PlanStep) Entry {
	detailLines := make([]string, 0, len(steps)+2)
	if strings.TrimSpace(summary) != "" {
		detailLines = append(detailLines, summary)
	}
	for index, step := range steps {
		detailLines = append(detailLines, fmt.Sprintf("%s %d. %s", planStepMarker(step.Status), index+1, step.Text))
	}
	if len(detailLines) == 0 {
		detailLines = append(detailLines, "(empty plan)")
	}

	previewLines := make([]string, 0, 3)
	if strings.TrimSpace(summary) != "" {
		previewLines = append(previewLines, summary)
	}
	visibleSteps := min(2, len(steps))
	for index := 0; index < visibleSteps; index++ {
		previewLines = append(previewLines, fmt.Sprintf("%s %d. %s", planStepMarker(steps[index].Status), index+1, steps[index].Text))
	}
	if hiddenSteps := len(steps) - visibleSteps; hiddenSteps > 0 {
		previewLines = append(previewLines, fmt.Sprintf("... (%d more steps, Ctrl+O to inspect)", hiddenSteps))
	}
	if len(previewLines) == 0 {
		previewLines = append(previewLines, "(empty plan)")
	}

	return Entry{
		Title:  "plan",
		Body:   strings.Join(previewLines, "\n"),
		Detail: strings.Join(detailLines, "\n"),
	}
}

func compactProposalEntry(payload controller.ProposalPayload) Entry {
	detailLines := make([]string, 0, 5)
	if payload.Kind != "" {
		detailLines = append(detailLines, "kind: "+string(payload.Kind))
	}
	if payload.Description != "" {
		detailLines = append(detailLines, payload.Description)
	}
	if payload.Command != "" {
		detailLines = append(detailLines, "command: "+payload.Command)
	}
	if payload.Keys != "" {
		detailLines = append(detailLines, "keys: "+previewFullscreenKeys(payload.Keys))
	}
	if payload.Patch != "" {
		detailLines = append(detailLines, "patch target: "+string(payload.PatchTarget))
		if len(detailLines) > 0 {
			detailLines = append(detailLines, "")
		}
		detailLines = append(detailLines, "patch:")
		detailLines = append(detailLines, payload.Patch)
	}
	if len(detailLines) == 0 {
		detailLines = append(detailLines, "(empty proposal)")
	}

	previewLines := make([]string, 0, 3)
	if payload.Description != "" {
		previewLines = append(previewLines, payload.Description)
	}
	switch {
	case payload.Command != "":
		previewLines = append(previewLines, "command: "+payload.Command)
	case payload.Keys != "":
		previewLines = append(previewLines, "keys: "+previewFullscreenKeys(payload.Keys))
	case payload.Patch != "":
		if payload.PatchTarget != "" {
			previewLines = append(previewLines, "target: "+string(payload.PatchTarget))
		}
		previewLines = append(previewLines, fmt.Sprintf("patch attached (%d lines, Ctrl+O to inspect)", countNonEmptyLines(payload.Patch)))
	case payload.Kind != "":
		previewLines = append(previewLines, "kind: "+string(payload.Kind))
	}
	if len(previewLines) == 0 {
		previewLines = append(previewLines, "(empty proposal)")
	}

	return Entry{
		Title:  "proposal",
		Body:   strings.Join(previewLines, "\n"),
		Detail: strings.Join(detailLines, "\n"),
	}
}

func compactApprovalEntry(payload controller.ApprovalRequest) Entry {
	detailLines := make([]string, 0, 7)
	if payload.Title != "" {
		detailLines = append(detailLines, payload.Title)
	}
	if payload.Summary != "" {
		detailLines = append(detailLines, payload.Summary)
	}
	if payload.Kind != "" {
		detailLines = append(detailLines, "kind: "+string(payload.Kind))
	}
	if payload.Risk != "" {
		detailLines = append(detailLines, "risk: "+string(payload.Risk))
	}
	if payload.Command != "" {
		detailLines = append(detailLines, "command: "+payload.Command)
	}
	if payload.Patch != "" {
		detailLines = append(detailLines, "patch target: "+string(payload.PatchTarget))
		if len(detailLines) > 0 {
			detailLines = append(detailLines, "")
		}
		detailLines = append(detailLines, "patch:")
		detailLines = append(detailLines, payload.Patch)
	}
	if len(detailLines) == 0 {
		detailLines = append(detailLines, "(empty approval)")
	}

	previewLines := make([]string, 0, 4)
	if payload.Title != "" {
		previewLines = append(previewLines, payload.Title)
	}
	if payload.Summary != "" {
		previewLines = append(previewLines, payload.Summary)
	}
	if payload.Command != "" {
		previewLines = append(previewLines, "command: "+payload.Command)
	}
	if payload.Patch != "" {
		if payload.PatchTarget != "" {
			previewLines = append(previewLines, "target: "+string(payload.PatchTarget))
		}
		previewLines = append(previewLines, fmt.Sprintf("patch attached (%d lines, Ctrl+O to inspect)", countNonEmptyLines(payload.Patch)))
	}
	if payload.Risk != "" {
		previewLines = append(previewLines, "risk: "+string(payload.Risk))
	}
	if len(previewLines) == 0 {
		previewLines = append(previewLines, "(empty approval)")
	}
	if len(previewLines) > 3 {
		previewLines = append(previewLines[:3], fmt.Sprintf("... (%d more lines, Ctrl+O to inspect)", len(previewLines)-3))
	}

	return Entry{
		Title:  "approval",
		Body:   strings.Join(previewLines, "\n"),
		Detail: strings.Join(detailLines, "\n"),
	}
}

func compactPatchApplyEntry(payload controller.PatchApplySummary) Entry {
	previewLines := []string{
		fmt.Sprintf("applied=%t created=%d updated=%d deleted=%d renamed=%d", payload.Applied, payload.Created, payload.Updated, payload.Deleted, payload.Renamed),
	}
	if strings.TrimSpace(string(payload.Transport)) != "" {
		previewLines = append(previewLines, "transport="+string(payload.Transport))
	}
	if strings.TrimSpace(payload.Error) != "" {
		previewLines = append(previewLines, payload.Error)
	}

	detailLines := make([]string, 0, len(payload.Files)+8)
	detailLines = append(detailLines, fmt.Sprintf("applied: %t", payload.Applied))
	if strings.TrimSpace(string(payload.Target)) != "" {
		detailLines = append(detailLines, "target: "+string(payload.Target))
	}
	if strings.TrimSpace(payload.TargetLabel) != "" {
		detailLines = append(detailLines, "target_label: "+payload.TargetLabel)
	}
	if strings.TrimSpace(string(payload.Transport)) != "" {
		detailLines = append(detailLines, "transport: "+string(payload.Transport))
	}
	if strings.TrimSpace(payload.CapabilitySource) != "" {
		detailLines = append(detailLines, "capability_source: "+payload.CapabilitySource)
	}
	if strings.TrimSpace(payload.WorkspaceRoot) != "" {
		detailLines = append(detailLines, "workspace_root: "+payload.WorkspaceRoot)
	}
	if strings.TrimSpace(payload.Validation) != "" {
		detailLines = append(detailLines, "validation: "+payload.Validation)
	}
	detailLines = append(detailLines,
		fmt.Sprintf("created: %d", payload.Created),
		fmt.Sprintf("updated: %d", payload.Updated),
		fmt.Sprintf("deleted: %d", payload.Deleted),
		fmt.Sprintf("renamed: %d", payload.Renamed),
	)
	if strings.TrimSpace(payload.Error) != "" {
		detailLines = append(detailLines, "", "error:", payload.Error)
	}
	if len(payload.Files) > 0 {
		detailLines = append(detailLines, "", "files:")
		for _, file := range payload.Files {
			line := file.Operation
			switch {
			case strings.TrimSpace(file.OldPath) != "" && strings.TrimSpace(file.NewPath) != "" && file.OldPath != file.NewPath:
				line += " " + file.OldPath + " -> " + file.NewPath
			case strings.TrimSpace(file.NewPath) != "":
				line += " " + file.NewPath
			default:
				line += " " + file.OldPath
			}
			detailLines = append(detailLines, line)
		}
	}

	tagKind := entryTagResultSuccess
	if !payload.Applied {
		tagKind = entryTagResultError
	}
	return Entry{
		Title:   "result",
		Body:    strings.Join(previewLines, "\n"),
		Detail:  strings.Join(detailLines, "\n"),
		TagKind: tagKind,
	}
}

func countNonEmptyLines(value string) int {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return 0
	}

	return len(lines)
}

func (m Model) detailPageSize() int {
	return max(1, m.height/2)
}

func (m *Model) clampDetailScroll() {
	if m.detailScroll < 0 {
		m.detailScroll = 0
		return
	}
	if m.detailScroll > m.maxDetailScroll() {
		m.detailScroll = m.maxDetailScroll()
	}
}

func (m Model) maxDetailScroll() int {
	width := m.width
	if width <= 0 {
		width = 100
	}
	contentWidth := m.contentWidthFor(width, m.styles.detail)
	lines, _, _ := m.detailBodyLines(contentWidth)
	height := m.height
	if height <= 0 {
		height = 24
	}
	viewportHeight := height - 8
	if viewportHeight < 4 {
		viewportHeight = 4
	}
	if len(lines) <= viewportHeight {
		return 0
	}

	return len(lines) - viewportHeight
}

func (m *Model) clampHelpScroll() {
	if m.helpScroll < 0 {
		m.helpScroll = 0
		return
	}
	if m.helpScroll > m.maxHelpScroll() {
		m.helpScroll = m.maxHelpScroll()
	}
}

func (m Model) maxHelpScroll() int {
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	contentWidth := m.contentWidthFor(width, m.styles.detail)
	headerHeight := lipgloss.Height(strings.Join([]string{
		m.styles.detailTitle.Render("HELP"),
		m.styles.detailMeta.Render("Shuttle controls, modes, slash commands, and mouse actions"),
		"",
	}, "\n"))
	viewportHeight := height - headerHeight - m.styles.detail.GetVerticalFrameSize() - 2
	if viewportHeight < 4 {
		viewportHeight = 4
	}
	lines := helpContentLines(contentWidth, m.mode, m.canSendActiveKeys())
	if len(lines) <= viewportHeight {
		return 0
	}

	return len(lines) - viewportHeight
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

func (m *Model) refreshTranscriptViewport() {
	if m.transcriptFollow {
		m.scrollTranscriptToBottom()
		return
	}
	m.clampTranscriptScroll()
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

func (m Model) renderTag(entry Entry, selected bool) string {
	title := entry.Title
	if transcriptEmojiEnabled() {
		switch entry.TagKind {
		case entryTagResultSuccess:
			return m.transcriptTagStyle(m.styles.glyphResult, selected).Render("✅")
		case entryTagResultError:
			return m.transcriptTagStyle(m.styles.glyphError, selected).Render("❌")
		case entryTagResultNoExec:
			return m.transcriptTagStyle(m.styles.glyphError, selected).Render("🚫")
		case entryTagResultNotFound:
			return m.transcriptTagStyle(m.styles.glyphError, selected).Render("❓")
		case entryTagResultSignal:
			return m.transcriptTagStyle(m.styles.glyphSystem, selected).Render("💥")
		case entryTagResultSigInt:
			return m.transcriptTagStyle(m.styles.glyphSystem, selected).Render("⚡")
		case entryTagResultCustom:
			return m.transcriptTagStyle(m.styles.glyphSystem, selected).Render("🛠️")
		case entryTagResultFatal:
			return m.transcriptTagStyle(m.styles.glyphError, selected).Render("🧨")
		}
		switch title {
		case "system":
			return m.transcriptTagStyle(m.styles.glyphSystem, selected).Render("⚙")
		case "user":
			return m.transcriptTagStyle(m.styles.glyphShell, selected).Render("👤")
		case "shell":
			return m.transcriptTagStyle(m.styles.glyphShell, selected).Render("💻")
		case "result":
			return m.transcriptTagStyle(m.styles.glyphResult, selected).Render("✅")
		case "agent":
			return m.transcriptTagStyle(m.styles.glyphAgent, selected).Render("🤖")
		case "plan":
			return m.transcriptTagStyle(m.styles.glyphAgent, selected).Render("🗺️")
		case "proposal":
			return m.transcriptTagStyle(m.styles.glyphAgent, selected).Render("📝")
		case "approval":
			return m.transcriptTagStyle(m.styles.glyphError, selected).Render("⚠️")
		case "error":
			return m.transcriptTagStyle(m.styles.glyphError, selected).Render("⛔")
		default:
			return m.transcriptTagStyle(m.styles.glyphSystem, selected).Render("•")
		}
	}

	text := strings.ToUpper(title)
	switch entry.TagKind {
	case entryTagResultSuccess:
		return m.transcriptTagStyle(m.styles.tagResult, selected).Render("RESULT")
	case entryTagResultError:
		return m.transcriptTagStyle(m.styles.tagError, selected).Render("ERROR")
	case entryTagResultNoExec:
		return m.transcriptTagStyle(m.styles.tagError, selected).Render("NOEXEC")
	case entryTagResultNotFound:
		return m.transcriptTagStyle(m.styles.tagError, selected).Render("MISSING")
	case entryTagResultSignal:
		return m.transcriptTagStyle(m.styles.tagSystem, selected).Render("SIGNAL")
	case entryTagResultSigInt:
		return m.transcriptTagStyle(m.styles.tagSystem, selected).Render("INT")
	case entryTagResultCustom:
		return m.transcriptTagStyle(m.styles.tagSystem, selected).Render("CUSTOM")
	case entryTagResultFatal:
		return m.transcriptTagStyle(m.styles.tagError, selected).Render("FATAL")
	}

	switch title {
	case "system":
		return m.transcriptTagStyle(m.styles.tagSystem, selected).Render(text)
	case "user":
		return m.transcriptTagStyle(m.styles.tagShell, selected).Render(text)
	case "shell":
		return m.transcriptTagStyle(m.styles.tagShell, selected).Render(text)
	case "result":
		return m.transcriptTagStyle(m.styles.tagResult, selected).Render(text)
	case "agent":
		return m.transcriptTagStyle(m.styles.tagAgent, selected).Render(text)
	case "plan":
		return m.transcriptTagStyle(m.styles.tagAgent, selected).Render(text)
	case "proposal":
		return m.transcriptTagStyle(m.styles.tagAgent, selected).Render(text)
	case "approval":
		return m.transcriptTagStyle(m.styles.tagError, selected).Render(text)
	case "error":
		return m.transcriptTagStyle(m.styles.tagError, selected).Render(text)
	default:
		return m.transcriptTagStyle(m.styles.tagSystem, selected).Render(text)
	}
}

func (m Model) renderResultTag(entry Entry, selected bool) string {
	if transcriptEmojiEnabled() {
		return m.transcriptTagStyle(m.styles.glyphShell, selected).Render("💻") + m.renderTag(entry, selected)
	}

	return m.transcriptTagStyle(m.styles.tagShell, selected).Render("CMD") + " " + m.renderTag(entry, selected)
}

func (m Model) transcriptTagStyle(style lipgloss.Style, selected bool) lipgloss.Style {
	if !selected {
		return style
	}
	return style.Copy().Background(lipgloss.Color(transcriptSelectedBackground))
}

func transcriptEmojiEnabled() bool {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		value := strings.ToUpper(strings.TrimSpace(os.Getenv(key)))
		if strings.Contains(value, "UTF-8") || strings.Contains(value, "UTF8") {
			return true
		}
	}
	return false
}

func eventsToEntries(events []controller.TranscriptEvent, collapseResults bool) []Entry {
	_ = collapseResults
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
			entries = append(entries, compactPlanEntry(payload.Summary, payload.Steps))
		case controller.EventProposal:
			payload, _ := event.Payload.(controller.ProposalPayload)
			entries = append(entries, compactProposalEntry(payload))
		case controller.EventApproval:
			payload, _ := event.Payload.(controller.ApprovalRequest)
			entries = append(entries, compactApprovalEntry(payload))
		case controller.EventCommandStart:
			payload, _ := event.Payload.(controller.CommandStartPayload)
			entries = append(entries, Entry{Title: "shell", Body: payload.Command})
		case controller.EventCommandResult:
			payload, _ := event.Payload.(controller.CommandResultSummary)
			tagKind := classifyResultTagKind(payload)
			fullBody := strings.TrimSpace(commandResultDisplaySummary(&payload))
			hasVisibleOutput := resultSummaryHasVisibleOutput(fullBody)
			detailBody := fullBody
			if detailBody == "" {
				detailBody = "(no output)"
			}
			body := fullBody
			if !hasVisibleOutput {
				body = silentSuccessTranscriptBody(payload)
			}
			if payload.State == controller.CommandExecutionCanceled {
				body = strings.TrimSpace(strings.TrimPrefix("status=canceled\n"+body, "\n"))
				detail := []string{
					"command:",
					payload.Command,
					"",
					"status:",
					"canceled",
				}
				if strings.TrimSpace(fullBody) != "" && fullBody != "(no output)" {
					detail = append(detail, "", "output so far:", fullBody)
				}
				entries = append(entries, Entry{
					Title:   "result",
					Command: payload.Command,
					Body:    body,
					Detail:  strings.Join(detail, "\n"),
					TagKind: tagKind,
				})
				break
			}
			if payload.State == controller.CommandExecutionLost {
				body = strings.TrimSpace(strings.TrimPrefix("status=lost\n"+body, "\n"))
				detail := []string{
					"command:",
					payload.Command,
					"",
					"status:",
					"lost",
				}
				if payload.Cause != "" {
					detail = append(detail, "", "cause:", string(payload.Cause))
				}
				if payload.Confidence != "" {
					detail = append(detail, "confidence:", string(payload.Confidence))
				}
				if strings.TrimSpace(fullBody) != "" && fullBody != "(no output)" {
					detail = append(detail, "", "latest observed output:", fullBody)
				}
				entries = append(entries, Entry{
					Title:   "result",
					Command: payload.Command,
					Body:    body,
					Detail:  strings.Join(detail, "\n"),
					TagKind: tagKind,
				})
				break
			}
			if body == "" && payload.ExitCode != 0 {
				body = fmt.Sprintf("exit=%d", payload.ExitCode)
			}
			entries = append(entries, Entry{
				Title:   "result",
				Command: payload.Command,
				Body:    body,
				Detail:  formatResultDetail(payload.Command, payload.ExitCode, detailBody),
				TagKind: tagKind,
			})
		case controller.EventPatchApplyResult:
			payload, _ := event.Payload.(controller.PatchApplySummary)
			entries = append(entries, compactPatchApplyEntry(payload))
		case controller.EventModelInfo:
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

func formatModelInfoDetail(payload controller.AgentModelInfo) string {
	lines := []string{}
	if providerLabel := strings.TrimSpace(payload.ProviderPreset); providerLabel != "" {
		lines = append(lines, "provider:", settingsProviderLabel(provider.ProviderPreset(providerLabel)), "")
	}
	if selectedRuntime := strings.TrimSpace(payload.SelectedRuntime); selectedRuntime != "" {
		lines = append(lines, "selected runtime:", selectedRuntime)
		if effectiveRuntime := strings.TrimSpace(payload.EffectiveRuntime); effectiveRuntime != "" && effectiveRuntime != selectedRuntime {
			lines = append(lines, "", "effective runtime:", effectiveRuntime)
		}
		if authority := strings.TrimSpace(payload.RuntimeAuthority); authority != "" {
			lines = append(lines, "", "runtime authority:", authority)
		}
		if command := strings.TrimSpace(payload.RuntimeCommand); command != "" {
			lines = append(lines, "", "runtime command:", command)
		}
		if reason := strings.TrimSpace(payload.RuntimeFailureReason); reason != "" {
			lines = append(lines, "", "runtime note:", reason)
		}
		lines = append(lines, "")
	}

	responseModel := strings.TrimSpace(payload.ResponseModel)
	requestedModel := strings.TrimSpace(payload.RequestedModel)
	switch {
	case responseModel != "":
		lines = append(lines, "reply model:", responseModel)
	case requestedModel != "":
		lines = append(lines, "reply model:", requestedModel)
	default:
		lines = append(lines, "reply model:", "provider model metadata unavailable")
	}
	if requestedModel != "" && requestedModel != responseModel {
		lines = append(lines, "", "requested model:", requestedModel)
	}
	return strings.Join(lines, "\n")
}

func appendDetailSection(entry *Entry, section string) {
	section = strings.TrimSpace(section)
	if section == "" {
		return
	}
	base := strings.TrimSpace(entry.DetailBody())
	if base == "" {
		entry.Detail = section
		return
	}
	entry.Detail = base + "\n\n" + section
}

func containsEventKind(events []controller.TranscriptEvent, kind controller.TranscriptEventKind) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

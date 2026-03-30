package tui

import (
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

func (m *Model) currentHistory() *composerHistory {
	if m.mode == AgentMode {
		return &m.agentHistory
	}

	return &m.shellHistory
}

func (m *Model) setInput(value string) {
	m.input = value
	m.cursor = utf8.RuneCountInString(value)
	if strings.TrimSpace(value) != "" {
		m.clearExitConfirm()
	}
	m.recomputeCompletion()
}

func (m *Model) clampCursor() {
	maxCursor := utf8.RuneCountInString(m.input)
	if m.cursor < 0 {
		m.cursor = 0
		return
	}
	if m.cursor > maxCursor {
		m.cursor = maxCursor
	}
}

func (m *Model) moveCursor(delta int) {
	m.cursor += delta
	m.clampCursor()
	m.recomputeCompletion()
}

func (m *Model) insertTextAtCursor(value string) {
	runes := []rune(m.input)
	index := m.cursor
	if index < 0 {
		index = 0
	}
	if index > len(runes) {
		index = len(runes)
	}
	inserted := []rune(value)
	if m.overwriteMode && len(inserted) > 0 && index < len(runes) {
		end := index + len(inserted)
		if end > len(runes) {
			end = len(runes)
		}
		runes = append(runes[:index], append(inserted, runes[end:]...)...)
	} else {
		runes = append(runes[:index], append(inserted, runes[index:]...)...)
	}
	m.input = string(runes)
	m.cursor = index + len(inserted)
	if strings.TrimSpace(m.input) != "" {
		m.clearExitConfirm()
	}
	m.recomputeCompletion()
}

func sanitizePastedText(value string) string {
	sanitized := ansiOSCPattern.ReplaceAllString(value, "")
	sanitized = ansiCSIPattern.ReplaceAllString(sanitized, "")
	sanitized = ansiEscPattern.ReplaceAllString(sanitized, "")

	filtered := strings.Builder{}
	filtered.Grow(len(sanitized))
	for _, r := range sanitized {
		switch {
		case r == '\n' || r == '\t':
			filtered.WriteRune(r)
		case r == '\r':
			continue
		case unicode.IsControl(r):
			continue
		default:
			filtered.WriteRune(r)
		}
	}

	return filtered.String()
}

func (m *Model) backspaceAtCursor() {
	runes := []rune(m.input)
	if m.cursor <= 0 || len(runes) == 0 {
		return
	}
	index := m.cursor
	if index > len(runes) {
		index = len(runes)
	}
	runes = append(runes[:index-1], runes[index:]...)
	m.input = string(runes)
	m.cursor = index - 1
	if strings.TrimSpace(m.input) != "" {
		m.clearExitConfirm()
	}
	m.recomputeCompletion()
}

func (m *Model) deleteAtCursor() {
	runes := []rune(m.input)
	if len(runes) == 0 || m.cursor >= len(runes) {
		return
	}
	index := m.cursor
	if index < 0 {
		index = 0
	}
	runes = append(runes[:index], runes[index+1:]...)
	m.input = string(runes)
	m.cursor = index
	if strings.TrimSpace(m.input) != "" {
		m.clearExitConfirm()
	}
	m.recomputeCompletion()
}

func (m *Model) toggleOverwriteMode() {
	m.overwriteMode = !m.overwriteMode
}

func (m *Model) inputHasMultipleLines() bool {
	return strings.Contains(m.input, "\n")
}

func (m *Model) moveCursorVertical(delta int) {
	runes := []rune(m.input)
	if len(runes) == 0 || delta == 0 {
		return
	}

	lineStarts := composerLineStarts(runes)
	lineIndex, column := composerCursorLineColumn(runes, m.cursor, lineStarts)
	targetLine := lineIndex + delta
	if targetLine < 0 {
		targetLine = 0
	}
	if targetLine >= len(lineStarts) {
		targetLine = len(lineStarts) - 1
	}
	targetStart := lineStarts[targetLine]
	targetEnd := composerLineEnd(runes, targetStart)
	targetColumn := column
	if targetStart+targetColumn > targetEnd {
		targetColumn = targetEnd - targetStart
	}
	m.cursor = targetStart + targetColumn
	m.clampCursor()
	m.recomputeCompletion()
}

func (m *Model) moveCursorToLineBoundary(end bool) {
	runes := []rune(m.input)
	if len(runes) == 0 {
		m.cursor = 0
		m.recomputeCompletion()
		return
	}
	lineStarts := composerLineStarts(runes)
	lineIndex, _ := composerCursorLineColumn(runes, m.cursor, lineStarts)
	start := lineStarts[lineIndex]
	if !end {
		m.cursor = start
		m.recomputeCompletion()
		return
	}
	m.cursor = composerLineEnd(runes, start)
	m.recomputeCompletion()
}

func composerLineStarts(runes []rune) []int {
	starts := []int{0}
	for index, r := range runes {
		if r == '\n' {
			starts = append(starts, index+1)
		}
	}
	return starts
}

func composerCursorLineColumn(runes []rune, cursor int, starts []int) (int, int) {
	if len(starts) == 0 {
		return 0, 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	lineIndex := 0
	for index := len(starts) - 1; index >= 0; index-- {
		if cursor >= starts[index] {
			lineIndex = index
			break
		}
	}
	return lineIndex, cursor - starts[lineIndex]
}

func composerLineEnd(runes []rune, start int) int {
	if start < 0 {
		start = 0
	}
	if start > len(runes) {
		start = len(runes)
	}
	for index := start; index < len(runes); index++ {
		if runes[index] == '\n' {
			return index
		}
	}
	return len(runes)
}

func (m *Model) clearCompletion() bool {
	if m.completion == nil {
		return false
	}
	m.completion = nil
	return true
}

func (m *Model) currentCompletionGhostText() string {
	if m.completion == nil || len(m.completion.Candidates) == 0 {
		return ""
	}
	index := m.completion.Index
	if index < 0 || index >= len(m.completion.Candidates) {
		return ""
	}
	candidate := m.completion.Candidates[index]
	if !strings.HasPrefix(candidate, m.completion.Fragment) {
		return ""
	}
	return candidate[len(m.completion.Fragment):]
}

func (m *Model) acceptCompletion() bool {
	if m.completion == nil || len(m.completion.Candidates) == 0 {
		return false
	}
	index := m.completion.Index
	if index < 0 || index >= len(m.completion.Candidates) {
		return false
	}
	candidate := m.completion.Candidates[index]
	if candidate == m.completion.Fragment {
		return false
	}
	m.replaceCompletionFragment(candidate)
	return true
}

func (m *Model) advanceCompletion() bool {
	next := m.computeCompletion()
	if next == nil || len(next.Candidates) == 0 {
		m.completion = nil
		return false
	}

	if m.completion != nil && sameCompletionQuery(*m.completion, *next) && len(next.Candidates) > 0 {
		next.Index = (m.completion.Index + 1) % len(next.Candidates)
	}
	m.completion = next
	return true
}

func (m *Model) recomputeCompletion() {
	m.completion = m.computeCompletion()
}

func (m *Model) computeCompletion() *composerCompletion {
	if m.composerLocked() || m.sendingFullscreenKeys {
		return nil
	}

	runes := []rune(m.input)
	if m.cursor != len(runes) {
		return nil
	}
	if strings.Contains(m.input, "\n") {
		return nil
	}

	var completion *composerCompletion
	switch {
	case m.editingProposal != nil || m.mode == ShellMode:
		completion = m.computeShellCompletion(runes)
	case m.mode == AgentMode && m.refiningApproval == nil && m.refiningProposal == nil:
		completion = m.computeSlashCompletion(runes)
	}
	if completion == nil && !(m.mode == AgentMode && strings.HasPrefix(m.input, "/")) {
		completion = m.computeHistoryCompletion(runes)
	}
	if completion == nil || len(completion.Candidates) == 0 {
		return nil
	}

	if m.completion != nil && sameCompletionQuery(*m.completion, *completion) {
		current := m.completion.Candidates[m.completion.Index]
		if index := indexOfString(completion.Candidates, current); index >= 0 {
			completion.Index = index
		}
	}

	if completion.Index < 0 || completion.Index >= len(completion.Candidates) {
		completion.Index = 0
	}
	return completion
}

func (m *Model) computeSlashCompletion(runes []rune) *composerCompletion {
	input := string(runes)
	if !strings.HasPrefix(input, "/") || strings.ContainsAny(input, " \t") {
		return nil
	}

	commands := availableSlashCommands()
	candidates := make([]string, 0, len(commands))
	for _, command := range commands {
		if strings.HasPrefix(command, input) {
			candidates = append(candidates, command)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	return &composerCompletion{
		Start:      0,
		End:        len(runes),
		Fragment:   input,
		Candidates: candidates,
	}
}

func (m *Model) computeShellCompletion(runes []rune) *composerCompletion {
	start := lastTokenStart(runes)
	fragment := string(runes[start:])
	if fragment == "" {
		return nil
	}

	var candidates []string
	if start == 0 && !strings.Contains(fragment, "/") && !strings.HasPrefix(fragment, ".") && !strings.HasPrefix(fragment, "~") {
		candidates = executableCompletionCandidates(fragment)
	} else {
		candidates = pathCompletionCandidates(m.currentWorkingDirectory(), fragment)
	}
	if len(candidates) == 0 {
		return nil
	}
	return &composerCompletion{
		Start:      start,
		End:        len(runes),
		Fragment:   fragment,
		Candidates: candidates,
	}
}

func (m *Model) computeHistoryCompletion(runes []rune) *composerCompletion {
	input := string(runes)
	if strings.TrimSpace(input) == "" {
		return nil
	}

	history := m.currentHistory()
	if len(history.entries) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(history.entries))
	candidates := make([]string, 0, len(history.entries))
	for index := len(history.entries) - 1; index >= 0; index-- {
		entry := history.entries[index]
		if entry == input || !strings.HasPrefix(entry, input) {
			continue
		}
		if _, ok := seen[entry]; ok {
			continue
		}
		seen[entry] = struct{}{}
		candidates = append(candidates, entry)
	}
	if len(candidates) == 0 {
		return nil
	}

	return &composerCompletion{
		Start:      0,
		End:        len(runes),
		Fragment:   input,
		Candidates: candidates,
	}
}

func (m *Model) replaceCompletionFragment(candidate string) {
	if m.completion == nil {
		return
	}
	runes := []rune(m.input)
	start := m.completion.Start
	end := m.completion.End
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}
	if start > end {
		start = end
	}
	updated := append(append(append([]rune{}, runes[:start]...), []rune(candidate)...), runes[end:]...)
	m.input = string(updated)
	m.cursor = start + len([]rune(candidate))
	if strings.TrimSpace(m.input) != "" {
		m.clearExitConfirm()
	}
	m.recomputeCompletion()
}

func (m Model) currentWorkingDirectory() string {
	dir := strings.TrimSpace(m.shellContext.Directory)
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			return cwd
		}
		return "."
	}
	if strings.HasPrefix(dir, "~") {
		if currentUser, err := user.Current(); err == nil {
			switch dir {
			case "~":
				return currentUser.HomeDir
			case "~/":
				return currentUser.HomeDir
			default:
				if strings.HasPrefix(dir, "~/") {
					return filepath.Join(currentUser.HomeDir, strings.TrimPrefix(dir, "~/"))
				}
			}
		}
	}
	return dir
}

func sameCompletionQuery(current composerCompletion, next composerCompletion) bool {
	return current.Start == next.Start && current.End == next.End && current.Fragment == next.Fragment
}

func availableSlashCommands() []string {
	return []string{
		"/approvals",
		"/compact",
		"/exit",
		"/help",
		"/model",
		"/models",
		"/new",
		"/onboard",
		"/onboarding",
		"/provider",
		"/providers",
		"/quit",
	}
}

func lastTokenStart(runes []rune) int {
	for index := len(runes) - 1; index >= 0; index-- {
		if unicode.IsSpace(runes[index]) {
			return index + 1
		}
	}
	return 0
}

func executableCompletionCandidates(prefix string) []string {
	pathValue := strings.TrimSpace(os.Getenv("PATH"))
	if pathValue == "" {
		return nil
	}
	seen := map[string]struct{}{}
	candidates := []string{}
	for _, dir := range filepath.SplitList(pathValue) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if _, ok := seen[name]; ok || !strings.HasPrefix(name, prefix) {
				continue
			}
			info, err := entry.Info()
			if err != nil || info.Mode().IsDir() || info.Mode()&0o111 == 0 {
				continue
			}
			seen[name] = struct{}{}
			candidates = append(candidates, name)
		}
	}
	sort.Strings(candidates)
	return candidates
}

func pathCompletionCandidates(workingDir string, fragment string) []string {
	searchDir := workingDir
	prefixDir := ""
	basePrefix := fragment
	if slash := strings.LastIndex(fragment, "/"); slash >= 0 {
		prefixDir = fragment[:slash+1]
		basePrefix = fragment[slash+1:]
		searchDir = resolveCompletionDir(workingDir, strings.TrimSuffix(prefixDir, "/"))
	}

	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}
	candidates := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, basePrefix) {
			continue
		}
		candidate := prefixDir + name
		if entry.IsDir() {
			candidate += "/"
		}
		candidates = append(candidates, candidate)
	}
	sort.Strings(candidates)
	return candidates
}

func resolveCompletionDir(workingDir string, fragment string) string {
	if fragment == "" {
		return workingDir
	}
	if strings.HasPrefix(fragment, "~") {
		if currentUser, err := user.Current(); err == nil {
			switch fragment {
			case "~":
				return currentUser.HomeDir
			default:
				if strings.HasPrefix(fragment, "~/") {
					return filepath.Join(currentUser.HomeDir, strings.TrimPrefix(fragment, "~/"))
				}
			}
		}
	}
	if filepath.IsAbs(fragment) {
		return fragment
	}
	return filepath.Join(workingDir, fragment)
}

func indexOfString(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}

func (m Model) handleComposerCtrlC() (tea.Model, tea.Cmd) {
	if m.exitConfirmActive() {
		return m, tea.Quit
	}

	if m.input != "" {
		m.setInput("")
	}
	return m.armExitConfirm()
}

func (m Model) armExitConfirm() (tea.Model, tea.Cmd) {
	m.exitConfirmToken++
	token := m.exitConfirmToken
	m.exitConfirmUntil = time.Now().Add(3 * time.Second)
	return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg {
		return exitConfirmExpiredMsg{token: token}
	})
}

func (m *Model) clearExitConfirm() {
	m.exitConfirmUntil = time.Time{}
	m.exitConfirmToken = 0
}

func (m Model) exitConfirmActive() bool {
	return !m.exitConfirmUntil.IsZero() && time.Now().Before(m.exitConfirmUntil)
}

func tickBusy() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return busyTickMsg(t)
	})
}

func tickShellContext() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return shellContextPollTickMsg(t)
	})
}

func wrapParagraphs(value string, width int) []string {
	paragraphs := strings.Split(value, "\n")
	lines := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		lines = append(lines, wrapText(paragraph, width)...)
	}
	return lines
}

func wrapText(value string, width int) []string {
	if width <= 0 {
		return []string{value}
	}
	if value == "" {
		return []string{""}
	}

	return strings.Split(xansi.Wrap(value, width, " \t"), "\n")
}

func trimLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[:len(runes)-1])
}

type composerLine struct {
	Before string
	Cursor string
	After  string
	Ghost  string
}

const composerMaxVisibleLines = 15

func composerDisplayLines(input string, cursor int, ghost string) []composerLine {
	runes := []rune(input)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}

	lines := strings.Split(string(runes), "\n")
	lineIndex := 0
	column := cursor
	for lineIndex < len(lines) {
		lineRunes := []rune(lines[lineIndex])
		if column <= len(lineRunes) {
			break
		}
		column -= len(lineRunes) + 1
		lineIndex++
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	if lineIndex >= len(lines) {
		lineIndex = len(lines) - 1
		column = len([]rune(lines[lineIndex]))
	}

	display := make([]composerLine, 0, len(lines))
	for index, line := range lines {
		lineRunes := []rune(line)
		if index != lineIndex {
			display = append(display, composerLine{Before: line})
			continue
		}

		if column < len(lineRunes) {
			display = append(display, composerLine{
				Before: string(lineRunes[:column]),
				Cursor: string(lineRunes[column]),
				After:  string(lineRunes[column+1:]),
			})
			continue
		}

		display = append(display, composerLine{
			Before: line,
			Cursor: " ",
			Ghost:  ghost,
		})
	}

	if len(display) == 0 {
		display = append(display, composerLine{Cursor: " ", Ghost: ghost})
	}

	return display
}

func composerViewportLines(lines []composerLine, maxLines int) []composerLine {
	if maxLines <= 0 || len(lines) <= maxLines {
		return lines
	}

	cursorLine := len(lines) - 1
	for index, line := range lines {
		if line.Cursor != "" {
			cursorLine = index
			break
		}
	}

	start := cursorLine - maxLines + 1
	if start < 0 {
		start = 0
	}
	if start+maxLines > len(lines) {
		start = len(lines) - maxLines
	}
	return lines[start : start+maxLines]
}

func renderComposerLine(line composerLine, cursorStyle lipgloss.Style, inputStyle lipgloss.Style, ghostStyle lipgloss.Style) string {
	return lipgloss.JoinHorizontal(
		lipgloss.Left,
		inputStyle.Render(line.Before),
		cursorStyle.Render(line.Cursor),
		inputStyle.Render(line.After),
		ghostStyle.Render(line.Ghost),
	)
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

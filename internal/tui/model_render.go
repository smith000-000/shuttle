package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"aiterm/internal/controller"
	"aiterm/internal/provider"
	"aiterm/internal/shell"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

func (m Model) View() string {
	if m.helpOpen {
		return m.renderScreen(m.renderHelpView())
	}
	if m.settingsOpen {
		return m.renderScreen(m.renderSettingsView())
	}
	if m.onboardingOpen {
		return m.renderScreen(m.renderOnboardingView())
	}
	if m.detailOpen {
		return m.renderScreen(m.renderDetailView())
	}

	width := m.width
	if width <= 0 {
		width = 100
	}
	screenWidth := width
	transcriptWidth := screenWidth
	actionWidth := m.contentWidthFor(screenWidth, m.styles.actionCard)
	statusWidth := m.contentWidthFor(screenWidth, m.styles.status)
	composerWidth := m.contentWidthFor(screenWidth, m.activeComposerStyle())
	footerWidth := screenWidth

	actionCard := m.renderActionCard(actionWidth)
	planCard := m.renderPlanCard(actionWidth)
	activeExecutionCard := m.renderActiveExecutionCard(actionWidth)
	statusLine := m.renderStatusLine(statusWidth)
	shellTail := m.renderShellTail(statusWidth)
	composer := m.renderComposer(composerWidth)
	footer := m.renderFooter(footerWidth)

	screenHeight := m.height
	if screenHeight <= 0 {
		screenHeight = 24
	}

	transcriptHeight := m.transcriptViewportHeight(actionCard, planCard, activeExecutionCard, statusLine, shellTail, composer, footer, screenHeight)

	transcript := m.renderTranscript(transcriptWidth, transcriptHeight)

	sections := []string{transcript}
	if actionCard != "" {
		sections = append(sections, actionCard)
	}
	if planCard != "" {
		sections = append(sections, planCard)
	}
	if activeExecutionCard != "" {
		sections = append(sections, activeExecutionCard)
	}
	if statusLine != "" {
		sections = append(sections, statusLine)
	}
	if shellTail != "" {
		sections = append(sections, shellTail)
	}
	footerSections := []string{composer, footer}
	bodyHeight := lipgloss.Height(strings.Join(append(append([]string(nil), sections...), footerSections...), "\n"))
	if fillerHeight := screenHeight - bodyHeight; fillerHeight > 0 {
		sections = append(sections, blankBlock(fillerHeight))
	}
	sections = append(sections, footerSections...)

	return m.renderScreen(lipgloss.JoinVertical(lipgloss.Left, sections...))
}

func (m Model) currentShellContext() *shell.PromptContext {
	if m.shellContext.PromptLine() == "" {
		return nil
	}

	contextCopy := m.shellContext
	return &contextCopy
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
	} else if m.pendingProposal != nil && (m.pendingProposal.Command != "" || m.pendingProposal.Keys != "" || m.pendingProposal.Patch != "") {
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

	if m.refiningProposal != nil {
		body := []string{}
		if m.refiningProposal.Description != "" {
			body = append(body, m.refiningProposal.Description)
		}
		if m.refiningProposal.Command != "" {
			body = append(body, "command: "+m.refiningProposal.Command)
		}
		body = append(body, "Enter a refinement note in the composer and press Enter")
		content := lipgloss.JoinVertical(
			lipgloss.Left,
			m.styles.actionTitle.Render("Refining Proposal"),
			m.styles.actionBody.Render(strings.Join(body, "\n")),
		)
		return m.styles.actionCard.BorderForeground(lipgloss.Color("214")).Width(width).Render(content)
	}

	if m.editingProposal != nil {
		body := []string{}
		if m.editingProposal.Description != "" {
			body = append(body, m.editingProposal.Description)
		}
		if m.editingProposal.Command != "" {
			body = append(body, "command: "+m.editingProposal.Command)
		}
		body = append(body, "Edit the command directly. Enter saves changes. Esc cancels.")
		content := lipgloss.JoinVertical(
			lipgloss.Left,
			m.styles.actionTitle.Render("Editing Proposed Command"),
			m.styles.actionBody.Render(strings.Join(body, "\n")),
		)
		return m.styles.actionCard.BorderForeground(lipgloss.Color("111")).Width(width).Render(content)
	}

	spec := m.currentActionCardSpec()
	if spec == nil {
		return ""
	}
	body := actionCardBodyLines(spec.body, width)
	buttonLines := layoutActionCardButtons(spec.buttons, width)
	for _, line := range buttonLines {
		body = append(body, line.text)
	}
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		m.styles.actionTitle.Render(spec.title),
		m.styles.actionBody.Render(strings.Join(body, "\n")),
	)
	return m.styles.actionCard.BorderForeground(spec.borderColor).Width(width).Render(content)
}

func (m Model) currentActionCardSpec() *actionCardSpec {
	if m.pendingFullscreen != nil {
		return &actionCardSpec{
			title: "Fullscreen Still Active",
			body: []string{
				"A fullscreen terminal app still appears active in the shell pane.",
				"command: " + m.pendingFullscreen.Command,
			},
			buttons: []actionCardButton{
				{label: "Y send anyway", action: actionCardConfirmFullscreen},
				{label: "N cancel", action: actionCardCancelFullscreen},
				{label: "F2 take control", action: actionCardTakeControl},
			},
			borderColor: lipgloss.Color("214"),
		}
	}

	if m.startupNotice != nil {
		return &actionCardSpec{
			title: m.startupNotice.Title,
			body:  []string{m.startupNotice.Body},
			buttons: []actionCardButton{
				{label: "Y continue", action: actionCardContinueStartup},
			},
			borderColor: lipgloss.Color("214"),
		}
	}

	if m.pendingApproval != nil {
		body := []string{
			m.pendingApproval.Title,
			m.pendingApproval.Summary,
		}
		if m.pendingApproval.Command != "" {
			body = append(body, "command: "+m.pendingApproval.Command)
		}
		if m.pendingApproval.Patch != "" {
			body = append(body, fmt.Sprintf("patch attached (%d lines, Ctrl+O to inspect)", countNonEmptyLines(m.pendingApproval.Patch)))
		}
		if m.pendingApproval.Risk != "" {
			body = append(body, "risk: "+string(m.pendingApproval.Risk))
		}
		approveLabel := "Y continue"
		if m.pendingApproval.Kind == controller.ApprovalPatch {
			approveLabel = "Y apply"
		}
		return &actionCardSpec{
			title: "Approval Required",
			body:  body,
			buttons: []actionCardButton{
				{label: approveLabel, action: actionCardApprove},
				{label: "N reject", action: actionCardReject},
				{label: "R refine", action: actionCardRefine},
			},
			borderColor: lipgloss.Color("160"),
		}
	}

	if m.pendingProposal != nil && m.pendingProposal.Keys != "" {
		body := []string{}
		if m.pendingProposal.Description != "" {
			body = append(body, m.pendingProposal.Description)
		}
		body = append(body, "keys: "+previewFullscreenKeys(m.pendingProposal.Keys))
		return &actionCardSpec{
			title: "Proposed Terminal Input",
			body:  body,
			buttons: []actionCardButton{
				{label: "Y send keys", action: actionCardApprove},
				{label: "N reject", action: actionCardReject},
				{label: "R ask agent", action: actionCardRefine},
			},
			borderColor: lipgloss.Color("31"),
		}
	}

	if m.pendingProposal != nil && m.pendingProposal.Patch != "" {
		body := []string{}
		if m.pendingProposal.Description != "" {
			body = append(body, m.pendingProposal.Description)
		}
		body = append(body, fmt.Sprintf("patch attached (%d lines, Ctrl+O to inspect)", countNonEmptyLines(m.pendingProposal.Patch)))
		return &actionCardSpec{
			title: "Proposed Patch",
			body:  body,
			buttons: []actionCardButton{
				{label: "Y apply", action: actionCardApprove},
				{label: "N reject", action: actionCardReject},
				{label: "R ask agent", action: actionCardRefine},
			},
			borderColor: lipgloss.Color("31"),
		}
	}

	if m.pendingProposal != nil && m.pendingProposal.Command != "" {
		body := []string{}
		if m.pendingProposal.Description != "" {
			body = append(body, m.pendingProposal.Description)
		}
		body = append(body, "command: "+m.pendingProposal.Command)
		return &actionCardSpec{
			title: "Proposed Command",
			body:  body,
			buttons: []actionCardButton{
				{label: "Y continue", action: actionCardApprove},
				{label: "N reject", action: actionCardReject},
				{label: "R ask agent", action: actionCardRefine},
				{label: "E tweak command", action: actionCardEditProposal},
			},
			borderColor: lipgloss.Color("31"),
		}
	}

	return nil
}

func (m Model) renderPlanCard(width int) string {
	if m.activePlan == nil {
		return ""
	}

	body := make([]string, 0, len(m.activePlan.Steps)+2)
	if strings.TrimSpace(m.activePlan.Summary) != "" {
		body = append(body, m.activePlan.Summary)
	}

	visibleSteps := len(m.activePlan.Steps)
	if visibleSteps > 6 {
		visibleSteps = 6
	}
	for index := 0; index < visibleSteps; index++ {
		step := m.activePlan.Steps[index]
		body = append(body, fmt.Sprintf("%s %d. %s", planStepMarker(step.Status), index+1, step.Text))
	}
	if hiddenSteps := len(m.activePlan.Steps) - visibleSteps; hiddenSteps > 0 {
		body = append(body, fmt.Sprintf("... (%d more steps)", hiddenSteps))
	}
	body = append(body, m.planProgressSummary())
	body = append(body, "Informational only. Ctrl+G continues the plan.")

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		m.styles.actionTitle.Render("Active Plan"),
		m.styles.actionBody.Render(strings.Join(body, "\n")),
	)
	return m.styles.actionCard.BorderForeground(lipgloss.Color("63")).Width(width).Render(content)
}

func (m Model) renderActiveExecutionCard(width int) string {
	if m.activeExecution == nil {
		return ""
	}

	body := []string{
		fmt.Sprintf("state: %s", humanizeExecutionState(m.activeExecution.State)),
		fmt.Sprintf("origin: %s", humanizeExecutionOrigin(m.activeExecution.Origin)),
		fmt.Sprintf("elapsed: %s", humanizeExecutionElapsed(m.activeExecution.StartedAt)),
		"command: " + m.activeExecution.Command,
	}
	usesTrackedShell := m.activeExecutionUsesTrackedShell()
	if m.activeExecution.State == controller.CommandExecutionInteractiveFullscreen {
		body = append(body, "Fullscreen terminal app detected.")
		if strings.TrimSpace(m.lastFullscreenKeys) != "" {
			body = append(body, "last keys: "+previewFullscreenKeys(m.lastFullscreenKeys))
		}
		if usesTrackedShell {
			body = append(body, "F2 take control  S send keys")
			body = append(body, "Exit or control the fullscreen app manually from the shell view.")
		} else {
			body = append(body, "S send keys")
			body = append(body, "This command is running in an owned execution pane. F2 opens the persistent user shell.")
		}
	} else if strings.TrimSpace(m.activeExecution.LatestOutputTail) != "" {
		lines := strings.Split(strings.TrimSpace(m.activeExecution.LatestOutputTail), "\n")
		if len(lines) > 2 {
			lines = lines[len(lines)-2:]
		}
		body = append(body, "tail: "+strings.Join(lines, " | "))
		if usesTrackedShell {
			body = append(body, "F2 take control")
		} else {
			body = append(body, "Running in owned execution pane. F2 opens the persistent user shell.")
		}
	} else {
		if usesTrackedShell {
			body = append(body, "F2 take control")
		} else {
			body = append(body, "Running in owned execution pane. F2 opens the persistent user shell.")
		}
	}

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		m.styles.actionTitle.Render("Active Command"),
		m.styles.actionBody.Render(strings.Join(body, "\n")),
	)

	borderColor := lipgloss.Color("31")
	if m.activeExecution.State == controller.CommandExecutionHandoffActive {
		borderColor = lipgloss.Color("214")
	}
	return m.styles.actionCard.BorderForeground(borderColor).Width(width).Render(content)
}

func (m Model) renderComposer(width int) string {
	composerStyle := m.styles.composerShell
	if m.refiningApproval != nil || m.refiningProposal != nil || m.editingProposal != nil {
		composerStyle = m.styles.composerRefine
	} else if m.mode == AgentMode {
		composerStyle = m.styles.composerAgent
	}

	promptStyle := m.styles.composerPromptShell
	prompt := "$>"
	switch {
	case m.sendingFullscreenKeys:
		promptStyle = m.styles.composerPromptRefine
		prompt = "KEYS>"
	case m.editingProposal != nil:
		promptStyle = m.styles.composerPromptRefine
		prompt = "CMD>"
	case m.refiningApproval != nil || m.refiningProposal != nil:
		promptStyle = m.styles.composerPromptRefine
		prompt = "Œ>"
	case m.mode == AgentMode:
		promptStyle = m.styles.composerPromptAgent
		prompt = "Œ>"
	case m.shellContext.Root:
		promptStyle = m.styles.composerPromptShell
		prompt = "#>"
	}

	inputStyle := m.styles.input.Copy().Background(composerStyle.GetBackground())
	ghostStyle := m.styles.inputGhost.Copy().Background(composerStyle.GetBackground())
	cursorStyle := inputStyle.Copy().Reverse(true)
	lines := composerDisplayLines(m.input, m.cursor, m.currentCompletionGhostText())
	prefixWidth := lipgloss.Width(prompt)
	rendered := make([]string, 0, len(lines))
	for index, line := range lines {
		lineBody := renderComposerLine(line, cursorStyle, inputStyle, ghostStyle)
		if index == 0 {
			rendered = append(rendered, lipgloss.JoinHorizontal(lipgloss.Left, promptStyle.Render(prompt), inputStyle.Render(" "), lineBody))
			continue
		}
		rendered = append(rendered, inputStyle.Render(strings.Repeat(" ", prefixWidth+1))+lineBody)
	}

	return composerStyle.Width(width).Render(strings.Join(rendered, "\n"))
}

func (m Model) renderFooter(width int) string {
	if m.detailOpen {
		parts := []string{"[Esc] close", "[Up/Down] scroll", "[PgUp/PgDn] page", "[Home/End] bounds", "[F2] shell", "[Ctrl+C] quit"}
		return m.styles.footer.Width(width).Render(strings.Join(parts, "  "))
	}

	parts := m.footerParts(width)
	return m.styles.footer.Width(width).Render(strings.Join(parts, "  "))
}

func (m Model) renderStatusLine(width int) string {
	left := m.renderShellContext()
	rightParts := make([]string, 0, 4)
	if m.exitConfirmActive() {
		rightParts = append(rightParts, m.styles.statusBusy.Render("Hit Ctrl-C again to exit"))
	}
	if m.shellContext.Root {
		rightParts = append(rightParts, m.styles.statusRoot.Render("ROOT"))
	}
	if m.shellContext.Remote {
		rightParts = append(rightParts, m.styles.statusRemote.Render("REMOTE"))
	}
	if m.lastModelInfo != nil {
		label := strings.TrimSpace(m.lastModelInfo.ResponseModel)
		if label == "" {
			label = strings.TrimSpace(m.lastModelInfo.RequestedModel)
		}
		if label != "" {
			providerLabel := strings.TrimSpace(m.lastModelInfo.ProviderPreset)
			if providerLabel != "" {
				providerLabel = settingsProviderLabel(provider.ProviderPreset(providerLabel))
			}
			if providerLabel != "" {
				rightParts = append(rightParts, m.styles.statusRemote.Render("MODEL "+providerLabel+" / "+label))
			} else {
				rightParts = append(rightParts, m.styles.statusRemote.Render("MODEL "+label))
			}
		}
	}
	if m.busy {
		elapsed := 0
		if !m.busyStartedAt.IsZero() {
			elapsed = int(time.Since(m.busyStartedAt).Seconds())
		}
		rightParts = append(rightParts, m.styles.statusBusy.Render(fmt.Sprintf("Working (%ds)", elapsed)))
	}
	right := strings.Join(rightParts, " ")

	if left == "" && right == "" {
		return ""
	}

	if right == "" {
		return m.styles.status.Render(runewidth.Truncate(left, width, "…"))
	}
	if left == "" {
		return m.styles.status.Render(right)
	}

	availableLeft := width - lipgloss.Width(right) - 1
	if availableLeft < 0 {
		availableLeft = 0
	}
	left = runewidth.Truncate(left, availableLeft, "…")
	padding := width - lipgloss.Width(left) - lipgloss.Width(right)
	if padding < 1 {
		padding = 1
	}

	return m.styles.status.Render(left + strings.Repeat(" ", padding) + right)
}

func (m Model) renderShellTail(width int) string {
	if !m.showShellTail {
		return ""
	}
	if m.activeExecution != nil && m.activeExecution.State == controller.CommandExecutionInteractiveFullscreen {
		return ""
	}

	tail := strings.TrimSpace(m.liveShellTail)
	if tail == "" {
		return ""
	}

	lines := strings.Split(tail, "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}

	contentWidth := max(10, width-2)
	rendered := make([]string, 0, len(lines)+1)
	rendered = append(rendered, m.styles.tailLabel.Render("shell"))
	for _, line := range lines {
		wrapped := wrapText(strings.TrimRight(line, "\r"), contentWidth)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for _, part := range wrapped {
			rendered = append(rendered, m.styles.tailBody.Render(part))
		}
	}
	if m.busy {
		rendered = append(rendered, m.styles.tailHint.Render("F2 to take control"))
	}

	return m.styles.tail.Width(width).Render(strings.Join(rendered, "\n"))
}

func (m Model) renderShellContext() string {
	return strings.TrimSpace(m.shellContext.PromptLine())
}

func (m Model) footerParts(width int) []string {
	escHint := "[Esc] clear"
	if m.busy || m.activeExecution != nil {
		escHint = "[Esc] interrupt"
	}
	if m.activeExecution != nil && !m.canAttemptLocalInterrupt() {
		escHint = "[Esc] manual"
	}

	switch {
	case width < 72:
		parts := []string{"[F1]", "[Ctrl+]]", "[Tab]", "[→]", "[Pg]", "[Enter]", "[Esc]", "[F2]", "[F10]", "[Ctrl+O]", "[Ctrl+C]"}
		if m.canSendActiveKeys() {
			parts = append(parts, "[S]")
		}
		if m.startupNotice != nil {
			parts = append(parts, "[Y]")
		} else if m.pendingFullscreen != nil {
			parts = append(parts, "[Y/N]")
		} else if m.pendingApproval != nil {
			parts = append(parts, "[Y/N/R]")
		} else if m.editingProposal != nil {
			parts = append(parts, "[Enter]")
		} else if m.refiningApproval != nil || m.refiningProposal != nil {
			parts = append(parts, "[Enter]")
		} else if m.pendingProposal != nil && (m.pendingProposal.Keys != "" || m.pendingProposal.Patch != "") {
			parts = append(parts, "[Y/N/R]")
		} else if m.pendingProposal != nil && m.pendingProposal.Command != "" {
			parts = append(parts, "[Y/N/R/E]")
		}
		return parts
	case width < 100:
		parts := []string{"[F1] help", "[Ctrl+]] mode", "[Tab] cycle/tab", "[→] accept", "[Alt+Up/Down] entry", "[Ctrl+O] detail", "[PgUp/PgDn] scroll", "[Enter] submit", escHint, "[F2] shell", "[F10] settings", "[Ctrl+C] quit"}
		if m.canSendActiveKeys() {
			parts = append(parts, "[S] keys")
		}
		if m.startupNotice != nil {
			parts = append(parts, "[Y] continue")
		} else if m.pendingFullscreen != nil {
			parts = append(parts, "[Y/N] fullscreen")
		} else if m.pendingApproval != nil {
			parts = append(parts, "[Y/N/R]")
		} else if m.editingProposal != nil {
			parts = append(parts, "[Enter] save")
		} else if m.refiningApproval != nil || m.refiningProposal != nil {
			parts = append(parts, "[Enter] refine")
		} else if m.pendingProposal != nil && (m.pendingProposal.Keys != "" || m.pendingProposal.Patch != "") {
			parts = append(parts, "[Y/N/R]")
		} else if m.pendingProposal != nil && m.pendingProposal.Command != "" {
			parts = append(parts, "[Y/N/R/E]")
		}
		return parts
	}

	parts := []string{"[F1] help", "[Ctrl+]] mode", "[Tab] cycle/tab", "[→] accept", "[Up/Down] history", "[Alt+Up/Down] entry", "[Ctrl+O] detail", "[PgUp/PgDn] scroll", "[Ctrl+U/D] half-page", "[Home/End] bounds", "[Enter] submit", escHint, "[Ctrl+J] newline", "[F2] shell", "[F10] settings"}
	if m.canSendActiveKeys() {
		parts = append(parts, "[S] send keys")
	}
	if m.startupNotice != nil {
		parts = append(parts, "[Y] continue")
	} else if m.pendingFullscreen != nil {
		parts = append(parts, "[Y] send anyway", "[N] cancel")
	} else if m.pendingApproval != nil {
		if m.pendingApproval.Kind == controller.ApprovalPatch {
			parts = append(parts, "[Y] apply", "[N] reject", "[R] refine")
		} else {
			parts = append(parts, "[Y] continue", "[N] reject", "[R] refine")
		}
	} else if m.editingProposal != nil {
		parts = append(parts, "[Enter] save edited command")
	} else if m.refiningApproval != nil || m.refiningProposal != nil {
		parts = append(parts, "[Enter] submit refine note")
	} else if m.pendingProposal != nil && m.pendingProposal.Keys != "" {
		parts = append(parts, "[Y] send keys", "[N] reject", "[R] ask agent")
	} else if m.pendingProposal != nil && m.pendingProposal.Patch != "" {
		parts = append(parts, "[Y] apply", "[N] reject", "[R] ask agent")
	} else if m.pendingProposal != nil && m.pendingProposal.Command != "" {
		parts = append(parts, "[Y] continue", "[N] reject", "[R] ask agent", "[E] tweak command")
	}
	parts = append(parts, "[Ctrl+C] quit")
	return parts
}

func startupNoticeForProfile(profile provider.Profile) *startupSecurityNotice {
	if strings.TrimSpace(profile.APIKeyEnvVar) != "local_file" {
		return nil
	}
	return &startupSecurityNotice{
		Title: "Less Secure Secret Storage",
		Body:  fmt.Sprintf("The active provider %s is using a locally stored plaintext secret file. This is less secure than OS keyring storage.", profile.Name),
	}
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

func (m Model) renderScreen(body string) string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	lines := strings.Split(body, "\n")
	switch {
	case len(lines) < height:
		lines = append(lines, make([]string, height-len(lines))...)
	case len(lines) > height:
		lines = lines[:height]
	}

	return m.styles.screen.Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func blankBlock(height int) string {
	if height <= 0 {
		return ""
	}
	lines := make([]string, height)
	return strings.Join(lines, "\n")
}

func (m Model) renderDetailView() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	entry := m.selectedEntryValue()
	contentWidth := m.contentWidthFor(width, m.styles.detail)
	lines := []string{
		m.styles.detailTitle.Render(strings.ToUpper(entry.Title)),
		m.styles.detailMeta.Render(fmt.Sprintf("entry %d/%d", m.selectedEntry+1, max(1, len(m.entries)))),
		"",
	}
	bodyLines := wrapParagraphs(entry.DetailBody(), max(10, contentWidth))
	viewportHeight := height - lipgloss.Height(strings.Join(lines, "\n")) - m.styles.detail.GetVerticalFrameSize() - 2
	if viewportHeight < 4 {
		viewportHeight = 4
	}
	bodyLines = detailWindow(bodyLines, m.detailScroll, viewportHeight)
	for _, line := range bodyLines {
		lines = append(lines, m.styles.detailBody.Render(line))
	}
	lines = append(lines, "", m.styles.detailMeta.Render(m.renderDetailFooter(contentWidth)))

	return m.styles.detail.Width(contentWidth).Height(height).Render(strings.Join(lines, "\n"))
}

func (m Model) renderHelpView() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	contentWidth := m.contentWidthFor(width, m.styles.detail)
	header := []string{
		m.styles.detailTitle.Render("HELP"),
		m.styles.detailMeta.Render("Shuttle controls, modes, slash commands, and mouse actions"),
		"",
	}

	bodyLines := helpContentLines(contentWidth, m.mode, m.canSendActiveKeys())
	viewportHeight := height - lipgloss.Height(strings.Join(header, "\n")) - m.styles.detail.GetVerticalFrameSize() - 2
	if viewportHeight < 4 {
		viewportHeight = 4
	}
	bodyLines = detailWindow(bodyLines, m.helpScroll, viewportHeight)

	lines := append([]string(nil), header...)
	for _, line := range bodyLines {
		if strings.HasPrefix(line, "# ") {
			lines = append(lines, m.styles.detailBody.Render(strings.TrimPrefix(line, "# ")))
			continue
		}
		if strings.HasPrefix(line, "> ") {
			lines = append(lines, m.styles.detailMeta.Render(strings.TrimPrefix(line, "> ")))
			continue
		}
		lines = append(lines, m.styles.detailBody.Render(line))
	}
	lines = append(lines, "", m.styles.detailMeta.Render(m.renderHelpFooter(contentWidth)))
	return m.styles.detail.Width(contentWidth).Height(height).Render(strings.Join(lines, "\n"))
}

func (m Model) renderOnboardingView() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	contentWidth := m.contentWidthFor(width, m.styles.detail)
	lines := []string{m.styles.detailTitle.Render("PROVIDER ONBOARDING")}
	if m.onboardingStep == onboardingStepConfig && m.onboardingForm != nil {
		lines = append(lines, m.styles.detailMeta.Render(m.onboardingForm.title))
	} else if m.onboardingStep == onboardingStepModels && m.onboardingSelected != nil {
		lines = append(lines, m.styles.detailMeta.Render("Select a model for "+m.onboardingSelected.Profile.Name))
	} else {
		lines = append(lines, m.styles.detailMeta.Render("Choose a saved, detected, or manual provider setup"))
	}
	lines = append(lines, "", m.styles.detailBody.Render("Current"), m.styles.detailMeta.Render(providerSummaryLine(m.activeProvider)), "")

	if m.onboardingStep == onboardingStepConfig && m.onboardingForm != nil {
		lines = append(lines, m.renderOnboardingConfig(contentWidth)...)
	} else if m.onboardingStep == onboardingStepModels && m.onboardingSelected != nil {
		lines = append(lines, m.renderOnboardingModels(contentWidth)...)
	} else {
		lines = append(lines, m.renderOnboardingProviders(contentWidth)...)
	}

	lines = append(lines, "", m.styles.detailMeta.Render(onboardingFooter(width, m.onboardingStep)))
	return m.styles.detail.Width(contentWidth).Height(height).Render(strings.Join(lines, "\n"))
}

func (m Model) renderSettingsView() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	contentWidth := m.contentWidthFor(width, m.styles.detail)
	lines := []string{
		m.styles.detailTitle.Render("SETTINGS"),
		m.styles.detailMeta.Render("Manage providers and choose the active model"),
	}

	switch m.settingsStep {
	case settingsStepProviders:
		lines = append(lines, m.styles.detailBody.Render("Providers"))
		lines = append(lines, m.styles.detailMeta.Render("Edit provider settings and save them for future sessions."))
		lines = append(lines, m.renderSettingsProviders(contentWidth)...)
	case settingsStepActiveProvider:
		lines = append(lines, m.styles.detailBody.Render("Active Provider"))
		lines = append(lines, m.styles.detailMeta.Render("Choose which configured provider Shuttle should use right now."))
		lines = append(lines, m.renderSettingsProviders(contentWidth)...)
	case settingsStepActiveModels:
		lines = append(lines, m.styles.detailBody.Render("Active Model"))
		lines = append(lines, m.styles.detailMeta.Render("Choose the provider/model Shuttle should use right now."))
		lines = append(lines, m.renderSettingsModels(contentWidth)...)
	case settingsStepProviderForm:
		if m.settingsConfig != nil {
			lines = append(lines, m.styles.detailBody.Render(m.settingsConfig.title))
			lines = append(lines, m.styles.detailMeta.Render(m.settingsConfig.intro))
			lines = append(lines, m.renderSettingsConfig(contentWidth)...)
		}
	default:
		lines = append(lines, m.styles.detailBody.Render("Current"))
		lines = append(lines, m.styles.detailMeta.Render(providerSummaryLine(m.activeProvider)))
		lines = append(lines, m.renderSettingsMenu(contentWidth)...)
	}

	lines = append(lines, "", m.styles.detailMeta.Render(settingsFooter(width, m.settingsStep)))
	return m.styles.detail.Width(contentWidth).Height(height).Render(strings.Join(lines, "\n"))
}

func helpContentLines(width int, mode Mode, canSendKeys bool) []string {
	lines := []string{
		"# Slash Commands",
		"/onboard or /onboarding: open provider onboarding",
		"/provider or /providers: open the active provider picker",
		"/model or /models: open the active model picker",
		"/quit or /exit: leave Shuttle",
		"> Slash commands only trigger in agent mode. In shell mode, leading / stays a path.",
		"",
		"# Global Keys",
		"F1: open or close this help view",
		"F2: jump to the shell pane / take control",
		"F10: open settings",
		"Ctrl+C: quit Shuttle",
		"Ctrl+]: toggle between agent and shell mode",
		"Ctrl+O: open the selected transcript entry in the full detail view",
		"PgUp/PgDn: scroll transcript",
		"Ctrl+U / Ctrl+D: half-page transcript scroll",
		"Home / End: jump transcript to top or bottom",
		"Alt+Up / Alt+Down: move transcript selection",
	}
	if mode == ShellMode {
		lines = append(lines, "Up / Down: shell command history")
	} else {
		lines = append(lines, "Up / Down: agent prompt history")
	}
	lines = append(lines,
		"Right Arrow: accept the current ghost-text completion",
		"Esc: clear the composer, collapse inline transcript detail, or interrupt active work",
		"",
		"# Composer",
		"Enter: submit the composer",
		"Ctrl+J: insert a newline",
		"Tab: cycle completion candidates, or insert a literal tab if no completion is available",
		"> In shell mode the first token completes PATH executables and later tokens complete filesystem paths.",
		"> In agent mode leading / offers Shuttle slash-command completion.",
		"",
		"# Action Cards",
		"Y: primary action for the current card",
		"N: reject or cancel when available",
		"R: refine when available",
		"E: edit a proposed command",
		"> Approval and proposal cards also support clickable actions with the mouse.",
	)
	if canSendKeys {
		lines = append(lines, "S: enter KEYS> mode to send raw keys to a fullscreen app")
	}
	lines = append(lines,
		"",
		"# Mouse",
		"Click a transcript icon/tag: expand or collapse inline detail for that entry",
		"Mouse wheel over transcript: scroll transcript",
		"Click shell label in the shell-tail block: same as F2 take control",
		"Shift-select: use terminal selection/copy while Bubble Tea mouse mode is active",
		"",
		"# Modes",
		"Shell mode: direct shell commands from $>",
		"Agent mode: send natural-language prompts from OE>",
		"> The current mode changes the composer prompt, history, slash-command behavior, and completion source.",
	)

	wrapped := make([]string, 0, len(lines)*2)
	for _, line := range lines {
		switch {
		case line == "":
			wrapped = append(wrapped, "")
		case strings.HasPrefix(line, "# "), strings.HasPrefix(line, "> "):
			wrapped = append(wrapped, line)
		default:
			wrapped = append(wrapped, wrapText(line, max(10, width))...)
		}
	}

	return wrapped
}

func (m Model) renderSettingsMenu(contentWidth int) []string {
	lines := make([]string, 0, len(settingsMenuEntries())+2)
	for index, entry := range settingsMenuEntries() {
		lines = append(lines, m.renderSettingsRow(entry.label, index == m.settingsIndex, false, false))
	}
	if contentWidth > 0 {
		lines = append(lines, m.styles.detailMeta.Render("Current model: "+currentProviderModelLabel(m.activeProvider)))
	}
	return lines
}

func (m Model) renderSettingsProviders(contentWidth int) []string {
	lines := make([]string, 0, len(m.settingsProviders)*3)
	for index, entry := range m.settingsProviders {
		label := entry.label
		if entry.disabled {
			label += " (coming soon)"
		}
		current := entry.candidate != nil && entry.candidate.Profile.Preset == m.activeProvider.Preset
		lines = append(lines, m.renderSettingsRow(label, index == m.settingsProviderIdx, current, entry.disabled))
		if entry.detail != "" {
			for _, line := range wrapParagraphs(entry.detail, max(10, contentWidth-2)) {
				lines = append(lines, m.renderSettingsMetaLine(line, index == m.settingsProviderIdx, current, entry.disabled))
			}
		}
		if entry.candidate != nil {
			lines = append(lines, m.renderSettingsMetaLine(providerSummaryLine(entry.candidate.Profile), index == m.settingsProviderIdx, current, entry.disabled))
		}
	}
	return lines
}

func (m Model) renderSettingsModels(contentWidth int) []string {
	lines := []string{m.renderSettingsCurrentLine("Current: " + currentProviderModelLabel(m.activeProvider))}
	filterLine := "Filter: type to search models"
	if m.settingsModelScope != "" {
		filterLine = fmt.Sprintf("Provider: %s  %s", settingsProviderLabel(m.settingsModelScope), filterLine)
	}
	if strings.TrimSpace(m.settingsModelFilter) != "" {
		filterLine = fmt.Sprintf("Filter: %s  (%d matches)", m.settingsModelFilter, len(m.settingsModels))
		if m.settingsModelScope != "" {
			filterLine = fmt.Sprintf("Provider: %s  %s", settingsProviderLabel(m.settingsModelScope), filterLine)
		}
	}
	lines = append(lines, m.styles.detailMeta.Render(filterLine))
	lines = append(lines, m.styles.detailMeta.Render("Shift+I shows extra model details for the highlighted row."))
	if settingsModelChoicesContainPreset(m.settingsModelCatalog, provider.PresetCodexCLI) {
		lines = append(lines, m.styles.detailMeta.Render("Codex CLI entries are suggested from the OpenAI catalog when available. The live codex CLI picker may differ, and manual entry is still allowed."))
	}
	if len(m.settingsModels) == 0 {
		if strings.TrimSpace(m.settingsModelFilter) != "" && len(m.settingsModelCatalog) > 0 {
			lines = append(lines, m.styles.detailBody.Render("No models match the current filter."))
			return lines
		}
		lines = append(lines, m.styles.detailBody.Render("No configured provider models are available yet."))
		return lines
	}

	start, end := onboardingModelWindow(len(m.settingsModels), m.settingsModelIdx, 12)
	lastProvider := ""
	for index := start; index < end; index++ {
		choice := m.settingsModels[index]
		if choice.profile.Name != lastProvider {
			lines = append(lines, m.styles.detailMeta.Render(choice.profile.Name))
			lastProvider = choice.profile.Name
		}
		current := choice.profile.Preset == m.activeProvider.Preset && choice.model.ID == m.activeProvider.Model
		label := fmt.Sprintf("%s / %s", settingsProviderLabel(choice.profile.Preset), choice.model.ID)
		lines = append(lines, m.renderSettingsRow(label, index == m.settingsModelIdx, current, false))
		detail := modelSummaryLine(choice.model)
		if detail != "" {
			for _, line := range wrapParagraphs(detail, max(10, contentWidth-2)) {
				lines = append(lines, m.renderSettingsMetaLine(line, index == m.settingsModelIdx, current, false))
			}
		}
		if index == m.settingsModelIdx && m.settingsModelInfo {
			for _, extra := range modelExtraDetailLines(choice.model) {
				for _, line := range wrapParagraphs(extra, max(10, contentWidth-2)) {
					lines = append(lines, m.renderSettingsMetaLine(line, true, current, false))
				}
			}
		}
	}

	return lines
}

func (m Model) toggleHelp() (tea.Model, tea.Cmd) {
	if m.helpOpen {
		m.helpOpen = false
		m.helpScroll = 0
		return m, nil
	}

	m.helpOpen = true
	m.helpScroll = 0
	return m, nil
}

func (m Model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyF2:
		return m.takeControlNow()
	case tea.KeyEsc:
		m.helpOpen = false
		m.helpScroll = 0
		return m, nil
	case tea.KeyUp:
		if m.helpScroll > 0 {
			m.helpScroll--
		}
		return m, nil
	case tea.KeyDown:
		m.helpScroll++
		m.clampHelpScroll()
		return m, nil
	case tea.KeyPgUp:
		m.helpScroll -= m.detailPageSize()
		m.clampHelpScroll()
		return m, nil
	case tea.KeyPgDown:
		m.helpScroll += m.detailPageSize()
		m.clampHelpScroll()
		return m, nil
	case tea.KeyHome:
		m.helpScroll = 0
		return m, nil
	case tea.KeyEnd:
		m.helpScroll = m.maxHelpScroll()
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) renderHelpFooter(width int) string {
	switch {
	case width < 40:
		return "[F1/Esc] close  [Up/Down]  [Pg]  [F2]  [Ctrl+C]"
	case width < 64:
		return "[F1/Esc] close  [Up/Down] scroll  [PgUp/PgDn] page  [F2] shell  [Ctrl+C] quit"
	default:
		return "[F1/Esc] close  [Up/Down] scroll  [PgUp/PgDn] page  [Home/End] bounds  [F2] shell  [Ctrl+C] quit"
	}
}

func (m Model) renderSettingsConfig(contentWidth int) []string {
	if m.settingsConfig == nil {
		return nil
	}

	lines := []string{m.styles.detailMeta.Render(providerSummaryLine(m.settingsConfig.profile))}
	for index, field := range m.settingsConfig.fields {
		value := field.value
		switch {
		case field.secret && strings.TrimSpace(value) != "":
			value = strings.Repeat("*", min(12, len(value)))
		case strings.TrimSpace(value) == "" && field.placeholder != "":
			value = "<" + field.placeholder + ">"
		case strings.TrimSpace(value) == "":
			value = "<empty>"
		}

		lines = append(lines, m.renderSettingsRow(fmt.Sprintf("%s: %s", field.label, value), index == m.settingsConfig.index, false, false))
	}
	lines = append(lines, m.styles.detailMeta.Render("API keys entered here are stored in the OS keyring."))
	return lines
}

func (m Model) renderOnboardingProviders(contentWidth int) []string {
	lines := make([]string, 0, len(m.onboardingChoices)*4)
	for index, choice := range m.onboardingChoices {
		prefix := "  "
		if index == m.onboardingIndex {
			prefix = "› "
		}

		label := fmt.Sprintf("%s%s", prefix, choice.Profile.Name)
		if choice.Profile.Preset == m.activeProvider.Preset && choice.Profile.Preset != "" {
			label += " (current)"
		}

		lines = append(lines, m.styles.detailBody.Render(label))
		lines = append(lines, m.styles.detailMeta.Render("   "+providerSummaryLine(choice.Profile)))
		if choice.Manual {
			lines = append(lines, m.styles.detailMeta.Render("   setup: manual entry"))
		}
		if choice.AuthSource != "" {
			lines = append(lines, m.styles.detailMeta.Render("   auth source: "+choice.AuthSource))
		}
		if strings.TrimSpace(choice.Reason) != "" {
			wrapped := wrapParagraphs(choice.Reason, max(10, contentWidth-3))
			for _, line := range wrapped {
				lines = append(lines, m.styles.detailMeta.Render("   "+line))
			}
		}
		lines = append(lines, "")
	}

	return lines
}

func (m Model) renderOnboardingConfig(contentWidth int) []string {
	lines := []string{}
	if m.onboardingForm == nil {
		return lines
	}

	if intro := strings.TrimSpace(m.onboardingForm.intro); intro != "" {
		for _, line := range wrapParagraphs(intro, max(10, contentWidth)) {
			lines = append(lines, m.styles.detailMeta.Render(line))
		}
		lines = append(lines, "")
	}

	lines = append(lines, m.styles.detailMeta.Render(providerSummaryLine(m.onboardingForm.profile)), "")
	for index, field := range m.onboardingForm.fields {
		prefix := "  "
		if index == m.onboardingForm.index {
			prefix = "› "
		}

		value := field.value
		switch {
		case field.secret && strings.TrimSpace(value) != "":
			value = strings.Repeat("*", min(12, len(value)))
		case strings.TrimSpace(value) == "" && field.placeholder != "":
			value = "<" + field.placeholder + ">"
		case strings.TrimSpace(value) == "":
			value = "<empty>"
		}

		label := fmt.Sprintf("%s%s: %s", prefix, field.label, value)
		lines = append(lines, m.styles.detailBody.Render(label))
	}

	lines = append(lines, "", m.styles.detailMeta.Render("API keys entered here are stored in the OS keyring."))
	return lines
}

func (m Model) renderOnboardingModels(contentWidth int) []string {
	lines := []string{}
	if m.onboardingSelected == nil {
		return lines
	}

	lines = append(lines, m.styles.detailMeta.Render(providerSummaryLine(m.onboardingSelected.Profile)), "")
	if len(m.onboardingModels) == 0 {
		lines = append(lines, m.styles.detailBody.Render("No models returned by this provider."))
		return lines
	}

	start, end := onboardingModelWindow(len(m.onboardingModels), m.onboardingModelIdx, 8)
	if start > 0 {
		lines = append(lines, m.styles.detailMeta.Render(fmt.Sprintf("... %d earlier models ...", start)))
	}
	for index := start; index < end; index++ {
		model := m.onboardingModels[index]
		prefix := "  "
		if index == m.onboardingModelIdx {
			prefix = "› "
		}
		label := fmt.Sprintf("%s%s", prefix, model.ID)
		if model.ID == m.activeProvider.Model {
			label += " (current)"
		}
		lines = append(lines, m.styles.detailBody.Render(label))
		detail := modelSummaryLine(model)
		if detail != "" {
			for _, line := range wrapParagraphs(detail, max(10, contentWidth-3)) {
				lines = append(lines, m.styles.detailMeta.Render("   "+line))
			}
		}
		lines = append(lines, "")
	}
	if end < len(m.onboardingModels) {
		lines = append(lines, m.styles.detailMeta.Render(fmt.Sprintf("... %d more models ...", len(m.onboardingModels)-end)))
	}

	return lines
}

func (m Model) renderSettingsRow(label string, selected bool, current bool, disabled bool) string {
	prefix := "  "
	if selected {
		prefix = "› "
	}

	style := m.settingsRowStyle(selected, current, disabled)
	return style.Render(prefix + label)
}

func (m Model) renderSettingsMetaLine(line string, selected bool, current bool, disabled bool) string {
	style := m.styles.detailMeta
	switch {
	case disabled:
		style = m.styles.detailDisabled
	case current && selected:
		style = m.styles.detailSelectedCurrent
	case current:
		style = m.styles.detailCurrent
	case selected:
		style = m.styles.detailSelected
	}
	return style.Render("  " + line)
}

func (m Model) renderSettingsCurrentLine(line string) string {
	return m.styles.detailCurrent.Render(line)
}

func (m Model) settingsRowStyle(selected bool, current bool, disabled bool) lipgloss.Style {
	switch {
	case disabled:
		return m.styles.detailDisabled
	case current && selected:
		return m.styles.detailSelectedCurrent
	case current:
		return m.styles.detailCurrent
	case selected:
		return m.styles.detailSelected
	default:
		return m.styles.detailBody
	}
}

func providerSummaryLine(profile provider.Profile) string {
	if profile.Preset == "" {
		return "provider not configured"
	}

	auth := providerAuthSourceLabel(profile)
	if strings.TrimSpace(auth) == "" {
		auth = "unknown"
	}
	return fmt.Sprintf("preset=%s  model=%s  base=%s  auth=%s", profile.Preset, profile.Model, profile.BaseURL, auth)
}

func providerAuthSourceLabel(profile provider.Profile) string {
	if strings.TrimSpace(profile.APIKeyEnvVar) != "" {
		if strings.TrimSpace(profile.APIKeyEnvVar) == "os_keyring" {
			return "OS keyring"
		}
		if strings.TrimSpace(profile.APIKeyEnvVar) == "local_file" {
			return "local file (less secure)"
		}
		return profile.APIKeyEnvVar
	}
	if strings.TrimSpace(profile.APIKey) != "" {
		return "session only"
	}
	if profile.AuthMethod == provider.AuthNone {
		return "none"
	}
	return string(profile.AuthMethod)
}

func providerPersistenceErrorBody(profile provider.Profile, err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, provider.ErrSecretStoreUnavailable) {
		return fmt.Sprintf("Provider settings for %s are active for this session, but the secret could not be persisted because secure key storage is unavailable: %v", profile.Name, err)
	}
	return fmt.Sprintf("save provider config: %v", err)
}

func currentProviderModelLabel(profile provider.Profile) string {
	if profile.Preset == "" {
		return "not configured"
	}
	providerLabel := profile.Name
	if strings.TrimSpace(providerLabel) == "" {
		providerLabel = settingsProviderLabel(profile.Preset)
	}
	if providerLabel == "" {
		providerLabel = string(profile.Preset)
	}
	if strings.TrimSpace(profile.Model) == "" {
		return fmt.Sprintf("%s (%s)", providerLabel, profile.Preset)
	}
	return fmt.Sprintf("%s (%s) / %s", providerLabel, profile.Preset, profile.Model)
}

func settingsMenuEntries() []settingsMenuEntry {
	return []settingsMenuEntry{
		{label: "Providers"},
		{label: "Active Provider"},
		{label: "Active Model"},
	}
}

func buildSettingsProviderEntries(candidates []provider.OnboardingCandidate) []settingsProviderEntry {
	chosen := map[provider.ProviderPreset]provider.OnboardingCandidate{}
	for _, preset := range settingsProviderOrder() {
		if candidate, ok := chooseSettingsCandidate(candidates, preset); ok {
			chosen[preset] = candidate
		}
	}

	entries := []settingsProviderEntry{}
	for _, preset := range settingsProviderOrder() {
		candidate, ok := chosen[preset]
		if !ok {
			continue
		}
		candidateCopy := candidate
		entries = append(entries, settingsProviderEntry{
			label:     settingsProviderLabel(preset),
			detail:    settingsProviderDetail(candidate),
			candidate: &candidateCopy,
		})
		if preset == provider.PresetAnthropic {
			entries = append(entries, settingsProviderEntry{
				label:    "Anthropic Agent SDK",
				detail:   "Reserved first-class Anthropic agent runtime integration.",
				disabled: true,
			})
		}
	}

	return entries
}

func chooseSettingsCandidate(candidates []provider.OnboardingCandidate, preset provider.ProviderPreset) (provider.OnboardingCandidate, bool) {
	var manual *provider.OnboardingCandidate
	for _, candidate := range candidates {
		if candidate.Profile.Preset != preset {
			continue
		}
		if !candidate.Manual {
			return candidate, true
		}
		if manual == nil {
			candidateCopy := candidate
			manual = &candidateCopy
		}
	}
	if manual != nil {
		return *manual, true
	}
	return provider.OnboardingCandidate{}, false
}

func settingsProviderOrder() []provider.ProviderPreset {
	return []provider.ProviderPreset{
		provider.PresetAnthropic,
		provider.PresetCodexCLI,
		provider.PresetOllama,
		provider.PresetOpenAI,
		provider.PresetOpenRouter,
		provider.PresetOpenWebUI,
		provider.PresetCustom,
	}
}

func settingsProviderLabel(preset provider.ProviderPreset) string {
	switch preset {
	case provider.PresetOpenAI:
		return "OpenAI"
	case provider.PresetOpenRouter:
		return "OpenRouter"
	case provider.PresetOpenWebUI:
		return "OpenWebUI"
	case provider.PresetCustom:
		return "OpenAI-Compatible"
	case provider.PresetCodexCLI:
		return "Codex CLI"
	case provider.PresetAnthropic:
		return "Anthropic"
	case provider.PresetOllama:
		return "Ollama"
	default:
		return string(preset)
	}
}

func settingsProviderDetail(candidate provider.OnboardingCandidate) string {
	status := "manual setup"
	if !candidate.Manual {
		status = "configured"
	}
	if candidate.AuthSource != "" {
		status += " via " + candidate.AuthSource
	}
	if strings.TrimSpace(candidate.Reason) == "" {
		return status
	}
	return status + ". " + strings.TrimSpace(candidate.Reason)
}

func settingsProfileKey(profile provider.Profile) string {
	return fmt.Sprintf("%s|%s|%s", profile.Preset, profile.BaseURL, profile.CLICommand)
}

func settingsModelChoiceKey(choice settingsModelChoice) string {
	return fmt.Sprintf("%s|%s|%s", choice.profile.Preset, choice.profile.BaseURL, choice.model.ID)
}

func findSettingsModelIndex(choices []settingsModelChoice, key string) int {
	for index, choice := range choices {
		if settingsModelChoiceKey(choice) == key {
			return index
		}
	}
	return -1
}

func filterSettingsModels(choices []settingsModelChoice, filter string) []settingsModelChoice {
	filter = strings.TrimSpace(strings.ToLower(filter))
	if filter == "" {
		return append([]settingsModelChoice(nil), choices...)
	}

	filtered := make([]settingsModelChoice, 0, len(choices))
	for _, choice := range choices {
		if settingsModelMatches(choice, filter) {
			filtered = append(filtered, choice)
		}
	}

	return filtered
}

func settingsModelMatches(choice settingsModelChoice, filter string) bool {
	tokens := strings.Fields(filter)
	if len(tokens) == 0 {
		tokens = []string{filter}
	}

	fields := []string{
		strings.ToLower(choice.model.ID),
		strings.ToLower(choice.model.Name),
		strings.ToLower(choice.profile.Name),
		strings.ToLower(string(choice.profile.Preset)),
	}

	for _, token := range tokens {
		matched := false
		for _, field := range fields {
			if strings.Contains(field, token) || fuzzySubsequenceMatch(field, token) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

func actionCardBodyLines(body []string, width int) []string {
	width = max(10, width)
	lines := make([]string, 0, len(body))
	for _, line := range body {
		lines = append(lines, wrapText(line, width)...)
	}
	return lines
}

func layoutActionCardButtons(buttons []actionCardButton, width int) []actionCardButtonLine {
	width = max(10, width)
	if len(buttons) == 0 {
		return nil
	}

	lines := make([]actionCardButtonLine, 0, len(buttons))
	current := actionCardButtonLine{}
	currentWidth := 0

	flush := func() {
		if current.text == "" {
			return
		}
		lines = append(lines, current)
		current = actionCardButtonLine{}
		currentWidth = 0
	}

	for _, button := range buttons {
		segments := wrapText(button.label, width)
		if len(segments) > 1 {
			flush()
			for _, segment := range segments {
				segmentWidth := runewidth.StringWidth(segment)
				lines = append(lines, actionCardButtonLine{
					text: segment,
					hits: []actionCardButtonHit{{
						action: button.action,
						start:  0,
						end:    segmentWidth,
					}},
				})
			}
			continue
		}

		label := button.label
		labelWidth := runewidth.StringWidth(label)
		gap := 0
		if current.text != "" {
			gap = 2
		}
		if current.text != "" && currentWidth+gap+labelWidth > width {
			flush()
			gap = 0
		}

		start := currentWidth + gap
		if gap > 0 {
			current.text += strings.Repeat(" ", gap)
		}
		current.text += label
		current.hits = append(current.hits, actionCardButtonHit{
			action: button.action,
			start:  start,
			end:    start + labelWidth,
		})
		currentWidth = start + labelWidth
	}

	flush()
	return lines
}

func fuzzySubsequenceMatch(field string, filter string) bool {
	filterRunes := []rune(filter)
	if len(filterRunes) == 0 {
		return true
	}

	index := 0
	for _, r := range field {
		if index < len(filterRunes) && r == filterRunes[index] {
			index++
			if index == len(filterRunes) {
				return true
			}
		}
	}

	return false
}

func containsModelOption(models []provider.ModelOption, target string) bool {
	target = strings.TrimSpace(target)
	for _, model := range models {
		if strings.TrimSpace(model.ID) == target {
			return true
		}
	}
	return false
}

func settingsModelChoicesContainPreset(choices []settingsModelChoice, preset provider.ProviderPreset) bool {
	for _, choice := range choices {
		if choice.profile.Preset == preset {
			return true
		}
	}
	return false
}

func shouldValidateProviderModel(profile provider.Profile) bool {
	if strings.TrimSpace(profile.Model) == "" {
		return false
	}

	switch profile.Preset {
	case provider.PresetOpenAI, provider.PresetOpenRouter, provider.PresetOpenWebUI, provider.PresetAnthropic, provider.PresetOllama:
		return true
	default:
		return false
	}
}

func onboardingFooter(width int, step onboardingStep) string {
	if width < 72 {
		if step == onboardingStepConfig {
			return "Type edit  Tab next  Enter save  Esc back  F2 shell"
		}
		if step == onboardingStepModels {
			return "Enter apply  Esc back  Up/Down move  Pg page  F2 shell"
		}
		return "Enter models  Esc close  Up/Down move  F2 shell"
	}

	if step == onboardingStepConfig {
		return "Type to edit fields  Tab/Up/Down move  Enter save and switch  Esc back  F2 shell"
	}
	if step == onboardingStepModels {
		return "Enter switch provider with selected model  Esc back  Up/Down move  PgUp/PgDn page  F2 shell"
	}

	return "Enter inspect models  Esc close  Up/Down move  F2 shell"
}

func settingsFooter(width int, step settingsStep) string {
	if width < 72 {
		switch step {
		case settingsStepProviders:
			return "Enter edit  Esc back  Up/Down move  F2 shell  F10 close"
		case settingsStepActiveProvider:
			return "Enter switch  Esc back  Up/Down move  F2 shell  F10 close"
		case settingsStepActiveModels:
			return "Type filter  Shift+I info  Enter activate  Esc clear/back  Pg page  F2 shell  F10 close"
		case settingsStepProviderForm:
			return "Type edit  Tab next  Enter save  F8 save+activate  Esc back  F2 shell  F10 close"
		default:
			return "Enter open  Esc close  Up/Down move  F2 shell  F10 close"
		}
	}

	switch step {
	case settingsStepProviders:
		return "Enter edit provider settings  Esc back  Up/Down move  F2 shell  F10 close"
	case settingsStepActiveProvider:
		return "Enter switch active provider  Esc back  Up/Down move  F2 shell  F10 close"
	case settingsStepActiveModels:
		return "Type to filter models  Shift+I toggle info  Enter switch active model  Esc clear filter/back  Up/Down move  PgUp/PgDn page  F2 shell  F10 close"
	case settingsStepProviderForm:
		return "Type to edit fields  Tab/Up/Down move  Enter save settings  F8 save and activate  Esc back  F2 shell  F10 close"
	default:
		return "Enter open section  Esc close  Up/Down move  F2 shell  F10 close"
	}
}

func onboardingModelWindow(total int, index int, size int) (int, int) {
	if total <= size {
		return 0, total
	}

	start := index - size/2
	if start < 0 {
		start = 0
	}
	end := start + size
	if end > total {
		end = total
		start = max(0, end-size)
	}

	return start, end
}

func modelSummaryLine(model provider.ModelOption) string {
	parts := make([]string, 0, 6)
	if model.Name != "" && model.Name != model.ID {
		parts = append(parts, model.Name)
	}
	if model.ContextWindow > 0 {
		parts = append(parts, fmt.Sprintf("context %d", model.ContextWindow))
	}
	if model.MaxCompletionTokens > 0 {
		parts = append(parts, fmt.Sprintf("max out %d", model.MaxCompletionTokens))
	}
	if model.PromptPrice != "" || model.CompletionPrice != "" {
		price := fmt.Sprintf("pricing p=%s c=%s", model.PromptPrice, model.CompletionPrice)
		parts = append(parts, price)
	}
	if model.Architecture.Modality != "" {
		parts = append(parts, "mode "+model.Architecture.Modality)
	}

	return strings.Join(parts, "  ")
}

func modelExtraDetailLines(model provider.ModelOption) []string {
	lines := make([]string, 0, 2)
	if len(model.SupportedParameters) > 0 {
		lines = append(lines, "params "+strings.Join(model.SupportedParameters, ","))
	}
	if model.Description != "" {
		lines = append(lines, model.Description)
	}
	return lines
}

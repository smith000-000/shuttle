package tui

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"aiterm/internal/controller"
	"aiterm/internal/provider"
	"aiterm/internal/shell"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const (
	shellComposerPrompt = "💲"
	rootComposerPrompt  = "#️⃣"
	agentComposerPrompt = "🤖"
	keysComposerPrompt  = "⌨️"
)

func canExecutionTakeControl(m Model) bool {
	overview := m.currentExecutionOverview()
	if overview.ActiveExecution == nil {
		return false
	}
	return strings.TrimSpace(overview.ActiveExecution.ExecutionTakeControlTarget.PaneID) != ""
}

func interactiveTakeControlHint(hasExecutionTarget bool) string {
	if hasExecutionTarget {
		return "Use F3 for direct control of the active command pane, or S for KEYS> if a few explicit tmux key events are enough."
	}
	return "Use F2 for direct control of the persistent shell, or S for KEYS> if a few explicit tmux key events are enough."
}

func interactiveTakeControlLabel(hasExecutionTarget bool) string {
	if hasExecutionTarget {
		return "F3 take control"
	}
	return "F2 take control"
}

func continueAfterCommandReason(result *controller.CommandResultSummary) string {
	if result == nil {
		return "Shuttle is waiting to continue from the latest command result."
	}

	switch result.State {
	case controller.CommandExecutionCanceled:
		return "Shuttle is waiting for confirmation to continue after the command was interrupted."
	case controller.CommandExecutionFailed:
		return "Shuttle is waiting to continue after the command failed."
	case controller.CommandExecutionCompleted:
		return "Shuttle is waiting to continue after the command completed."
	case controller.CommandExecutionLost:
		return "Shuttle is waiting to continue after command tracking was lost."
	default:
		return "Shuttle is waiting to continue from the latest command result."
	}
}

func continueAfterCommandPlanHint(plan *controller.ActivePlan) string {
	if plan == nil || len(plan.Steps) == 0 {
		return ""
	}

	for index, step := range plan.Steps {
		if step.Status == controller.PlanStepInProgress {
			return fmt.Sprintf("The agent will reassess plan step %d before continuing.", index+1)
		}
	}
	return "The agent will reassess the active plan before continuing."
}

func activeExecutionTakeControlAffordance(targetsExecution bool, hasExecutionTarget bool) string {
	switch {
	case targetsExecution:
		return "F3 take control"
	case hasExecutionTarget:
		return "F2 shell  F3 command"
	default:
		return "F2 take control"
	}
}

func (m Model) renderMainView() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	screenWidth := width
	transcriptWidth := screenWidth
	actionWidth := m.contentWidthFor(screenWidth, m.styles.actionCard)
	statusWidth := m.contentWidthFor(screenWidth, m.styles.status)
	composerWidth := screenWidth
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

	transcriptHeight := m.resolvedTranscriptHeight(transcriptWidth, screenHeight, actionCard, planCard, activeExecutionCard, statusLine, shellTail, composer, footer)
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
	sections = append(sections, footerSections...)

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m Model) View() string {
	if m.pendingDangerousConfirm != nil {
		return m.renderScreen(m.renderMainView())
	}
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

	return m.renderScreen(m.renderMainView())
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

	if m.pendingDangerousConfirm != nil {
		return &actionCardSpec{
			title: "Enable Dangerous Mode",
			body:  []string{controller.DangerousModeWarning()},
			buttons: []actionCardButton{
				{label: "Y enable dangerous", action: actionCardConfirmDangerous},
				{label: "N cancel", action: actionCardCancelDangerous},
			},
			borderColor: lipgloss.Color("160"),
		}
	}

	if m.pendingApproval == nil && m.pendingProposal == nil && m.interactiveCheckInPaused && executionNeedsUserDrivenResume(m.activeExecution) {
		command := "interactive command"
		if m.activeExecution != nil && strings.TrimSpace(m.activeExecution.Command) != "" {
			command = m.activeExecution.Command
		}
		stateLabel := "interactive screen"
		if m.activeExecution != nil && m.activeExecution.State == controller.CommandExecutionAwaitingInput {
			stateLabel = "input prompt"
		}
		return &actionCardSpec{
			title: "Interactive Wait Paused",
			body: []string{
				fmt.Sprintf("Automatic agent check-ins are paused while this %s is active.", stateLabel),
				"command: " + command,
				interactiveTakeControlHint(m.executionTakeControlConfig().enabled()),
			},
			buttons: []actionCardButton{
				{label: "Ctrl+G resume", action: actionCardResumeInteractive},
				{label: "R tell agent", action: actionCardRefine},
				{label: interactiveTakeControlLabel(m.executionTakeControlConfig().enabled()), action: actionCardTakeControl},
			},
			borderColor: lipgloss.Color("214"),
		}
	}

	if m.pendingApproval == nil && m.pendingProposal == nil && m.pendingContinueAfterCommand {
		body := []string{
			continueAfterCommandReason(m.lastCommandResult),
		}
		if m.lastCommandResult != nil {
			if strings.TrimSpace(m.lastCommandResult.Command) != "" {
				body = append(body, "command: "+m.lastCommandResult.Command)
			}
			body = append(body, "result: "+humanizeExecutionState(m.lastCommandResult.State))
		} else {
			body = append(body, "latest result is ready for follow-up.")
		}
		if hint := continueAfterCommandPlanHint(m.activePlan); hint != "" {
			body = append(body, hint)
		}
		return &actionCardSpec{
			title: "Command Ready For Follow-Up",
			body:  body,
			buttons: []actionCardButton{
				{label: "Ctrl+G continue from result", action: actionCardResumeInteractive},
				{label: "R tell agent", action: actionCardRefine},
			},
			borderColor: lipgloss.Color("63"),
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
			if m.pendingApproval.PatchTarget != "" {
				body = append(body, "target: "+string(m.pendingApproval.PatchTarget))
			}
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
		if m.pendingProposal.PatchTarget != "" {
			body = append(body, "target: "+string(m.pendingProposal.PatchTarget))
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
	footerLine := "Informational only. Ctrl+G continues the plan."

	renderedBody := make([]string, 0, len(body)+1)
	for _, line := range body {
		renderedBody = append(renderedBody, m.styles.actionBody.Render(line))
	}
	renderedBody = append(renderedBody, footerLine)
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		m.styles.actionTitle.Render("Active Plan"),
		lipgloss.JoinVertical(lipgloss.Left, renderedBody...),
	)
	return m.styles.actionCard.BorderForeground(lipgloss.Color("63")).Width(width).Render(content)
}

func (m Model) renderActiveExecutionCard(width int) string {
	if m.activeExecution == nil {
		return ""
	}
	overview := m.currentExecutionOverview()
	body := []string{
		fmt.Sprintf("state: %s", humanizeExecutionState(m.activeExecution.State)),
		fmt.Sprintf("origin: %s", humanizeExecutionOrigin(m.activeExecution.Origin)),
		fmt.Sprintf("elapsed: %s", humanizeExecutionElapsed(m.activeExecution.StartedAt)),
		"command: " + m.activeExecution.Command,
	}
	usesTrackedShell := m.activeExecutionUsesTrackedShell()
	takeControlTargetsExecution := m.takeControlTargetsActiveExecution()
	if overview.ActiveExecution != nil && !usesTrackedShell {
		body = append(body, "surface: owned execution pane")
	} else {
		body = append(body, "surface: tracked shell")
	}
	if m.activeExecution.State == controller.CommandExecutionInteractiveFullscreen {
		body = append(body, "Fullscreen terminal app detected.")
		if strings.TrimSpace(m.lastFullscreenKeys) != "" {
			body = append(body, "last keys: "+previewFullscreenKeys(m.lastFullscreenKeys))
		}
		if takeControlTargetsExecution || usesTrackedShell {
			body = append(body, activeExecutionTakeControlAffordance(takeControlTargetsExecution, canExecutionTakeControl(m))+"  S send keys")
			if usesTrackedShell {
				body = append(body, "Exit or control the fullscreen app manually from the shell view with F2.")
			} else {
				body = append(body, "Temporary Shuttle execution pane. F2 still targets the persistent shell; F3 takes control of the command pane.")
			}
		} else {
			body = append(body, "S send keys")
			body = append(body, "This command is running in an owned execution pane. F2 still targets the persistent shell; F3 takes control of the command pane.")
		}
	} else if displayTail := strings.TrimSpace(executionDisplayTail(m.activeExecution)); displayTail != "" {
		lines := strings.Split(displayTail, "\n")
		if len(lines) > 2 {
			lines = lines[len(lines)-2:]
		}
		if m.activeExecution.State == controller.CommandExecutionAwaitingInput {
			body = append(body, "Terminal input prompt detected.")
		}
		body = append(body, "tail: "+strings.Join(lines, " | "))
		if m.activeExecution.State == controller.CommandExecutionAwaitingInput {
			if takeControlTargetsExecution || usesTrackedShell {
				body = append(body, activeExecutionTakeControlAffordance(takeControlTargetsExecution, canExecutionTakeControl(m))+"  S send keys")
				if takeControlTargetsExecution && !usesTrackedShell {
					body = append(body, "Temporary Shuttle execution pane. F2 still targets the persistent shell; F3 takes control of the command pane.")
				}
			} else {
				body = append(body, "S send keys")
				body = append(body, "This command is running in an owned execution pane. F2 still targets the persistent shell; F3 takes control of the command pane.")
			}
		} else if takeControlTargetsExecution || usesTrackedShell {
			body = append(body, activeExecutionTakeControlAffordance(takeControlTargetsExecution, canExecutionTakeControl(m)))
			if takeControlTargetsExecution && !usesTrackedShell {
				body = append(body, "Temporary Shuttle execution pane. F2 still targets the persistent shell; F3 takes control of the command pane.")
			}
		} else {
			body = append(body, "Running in an owned execution pane. F2 still targets the persistent shell; F3 takes control of the command pane.")
		}
	} else {
		if takeControlTargetsExecution || usesTrackedShell {
			body = append(body, activeExecutionTakeControlAffordance(takeControlTargetsExecution, canExecutionTakeControl(m)))
			if takeControlTargetsExecution && !usesTrackedShell {
				body = append(body, "Temporary Shuttle execution pane. F2 still targets the persistent shell; F3 takes control of the command pane.")
			}
		} else {
			body = append(body, "Running in an owned execution pane. F2 still targets the persistent shell; F3 takes control of the command pane.")
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
	prompt := shellComposerPrompt
	switch {
	case m.sendingFullscreenKeys:
		promptStyle = m.styles.composerPromptRefine
		prompt = keysComposerPrompt
	case m.editingProposal != nil:
		promptStyle = m.styles.composerPromptRefine
		prompt = "CMD>"
	case m.refiningApproval != nil || m.refiningProposal != nil:
		promptStyle = m.styles.composerPromptRefine
		prompt = agentComposerPrompt
	case m.mode == AgentMode:
		promptStyle = m.styles.composerPromptAgent
		prompt = agentComposerPrompt
	case m.shellContext.Root:
		promptStyle = m.styles.composerPromptShell
		prompt = rootComposerPrompt
	}

	inputStyle := m.styles.input.Copy().Background(composerStyle.GetBackground())
	ghostStyle := m.styles.inputGhost.Copy().Background(composerStyle.GetBackground())
	cursorStyle := inputStyle.Copy().Reverse(true)
	lines := composerViewportLines(composerDisplayLines(m.input, m.cursor, m.currentCompletionGhostText()), composerMaxVisibleLines)
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
	rightParts := make([]string, 0, 7)
	if m.exitConfirmActive() {
		rightParts = append(rightParts, m.styles.statusDanger.Render("ctrl-c again to exit"))
	}
	if m.shellContext.Root {
		rightParts = append(rightParts, m.styles.statusRoot.Render("root"))
	}
	if m.shellContext.Remote {
		rightParts = append(rightParts, m.styles.statusRemote.Render("remote"))
	}
	if approvalLabel := m.renderApprovalModeLabel(m.hasModelStatus()); approvalLabel != "" {
		rightParts = append(rightParts, approvalLabel)
	}
	if modelStatus := m.renderModelStatus(); modelStatus != "" {
		rightParts = append(rightParts, modelStatus)
	}
	if usageLabel := m.renderContextUsageLabel(); usageLabel != "" {
		rightParts = append(rightParts, usageLabel)
	}
	if busyLabel := m.renderBusyStatus(); busyLabel != "" {
		rightParts = append(rightParts, busyLabel)
	}
	right := joinStatusSegments(m.styles.statusMuted.Render("*"), rightParts)

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

func (m Model) renderModelStatus() string {
	label, providerLabel := m.statusModelLabel()
	if label == "" {
		return ""
	}

	modelText := label
	if providerLabel != "" {
		modelText = providerLabel + " / " + label
	}
	if runtimeLabel := m.statusRuntimeLabel(); runtimeLabel != "" {
		modelText = runtimeLabel + " | " + modelText
	}
	return m.styles.statusRemote.Render(modelText)
}

func (m Model) statusRuntimeLabel() string {
	selected := strings.TrimSpace(m.activeRuntimeType)
	effective := ""
	if selected != "" {
		resolved := provider.ResolveRuntimeSelection(selected, m.activeRuntimeCommand)
		selected = strings.TrimSpace(resolved.RequestedType)
		effective = strings.TrimSpace(resolved.SelectedType)
	}
	if m.lastModelInfo != nil {
		if lastSelected := strings.TrimSpace(m.lastModelInfo.SelectedRuntime); lastSelected != "" {
			selected = lastSelected
		}
		if lastEffective := strings.TrimSpace(m.lastModelInfo.EffectiveRuntime); lastEffective != "" {
			effective = lastEffective
		}
	}
	if selected == "" {
		selected = effective
	}
	if selected == "" || selected == controller.RuntimeBuiltin {
		return ""
	}
	return runtimeStatusLabel(selected, effective)
}

func (m Model) renderApprovalModeLabel(includeDefault bool) string {
	if m.ctrl == nil {
		return ""
	}
	mode := m.ctrl.ApprovalMode()
	if mode != controller.ApprovalModeAuto && mode != controller.ApprovalModeDanger && !includeDefault {
		return ""
	}
	switch mode {
	case controller.ApprovalModeAuto:
		return m.styles.statusWarn.Render("auto")
	case controller.ApprovalModeDanger:
		return m.styles.statusDanger.Render("dangerous")
	default:
		return m.styles.statusConfirm.Render("confirm")
	}
}

func (m Model) statusModelLabel() (string, string) {
	if m.lastModelInfo != nil {
		label := strings.TrimSpace(m.lastModelInfo.ResponseModel)
		if label == "" {
			label = strings.TrimSpace(m.lastModelInfo.RequestedModel)
		}
		providerLabel := strings.TrimSpace(m.lastModelInfo.ProviderPreset)
		if providerLabel != "" {
			providerLabel = settingsProviderLabel(provider.ProviderPreset(providerLabel))
		}
		if label != "" {
			return label, providerLabel
		}
	}

	label := strings.TrimSpace(m.activeProvider.Model)
	if label == "" {
		return "", ""
	}
	return label, settingsProviderLabel(m.activeProvider.Preset)
}

func (m Model) renderContextUsageLabel() string {
	if m.ctrl == nil {
		return ""
	}

	usage := m.ctrl.EstimateContextUsage(m.contextUsagePrompt())
	window := currentModelContextWindow(m.activeProvider)
	if usage.ApproxPromptTokens <= 0 && window <= 0 {
		return ""
	}

	if window <= 0 {
		return m.styles.statusMuted.Render("~" + formatStatusTokenCount(usage.ApproxPromptTokens))
	}
	pct := 0.0
	if window > 0 {
		pct = float64(usage.ApproxPromptTokens) / float64(window)
	}
	style := m.contextUsageStyle(pct)
	bar := renderContextASCIIBar(12, pct)
	label := fmt.Sprintf("%s %s/%s", bar, formatStatusTokenCount(usage.ApproxPromptTokens), formatStatusTokenCount(window))
	return style.Render(label)
}

func (m Model) renderBusyStatus() string {
	if !m.busy {
		return ""
	}
	elapsed := time.Duration(0)
	if !m.busyStartedAt.IsZero() {
		elapsed = time.Since(m.busyStartedAt)
	}
	return m.styles.statusBusy.Render(fmt.Sprintf("%s Working %s", busySpinnerFrame(m.busyStartedAt), formatBusyElapsed(elapsed)))
}

func (m Model) hasModelStatus() bool {
	label, _ := m.statusModelLabel()
	return label != ""
}

func (m Model) contextUsageStyle(pct float64) lipgloss.Style {
	switch {
	case pct <= 0:
		return m.styles.statusMuted
	case pct < 0.40:
		return m.styles.statusConfirm
	case pct < 0.75:
		return m.styles.statusWarn
	default:
		return m.styles.statusDanger
	}
}

func busySpinnerFrame(startedAt time.Time) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	if startedAt.IsZero() {
		return frames[0]
	}
	step := int(time.Since(startedAt) / (100 * time.Millisecond))
	if step < 0 {
		step = 0
	}
	return frames[step%len(frames)]
}

func formatBusyElapsed(elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	seconds := int(elapsed / time.Second)
	if seconds < 100 {
		return fmt.Sprintf("(%2ds)", seconds)
	}
	rounded := elapsed.Round(time.Second)
	if rounded < time.Second {
		rounded = time.Second
	}
	return "(" + rounded.String() + ")"
}

func renderContextASCIIBar(width int, pct float64) string {
	if width <= 0 {
		return ""
	}
	pct = math.Max(0, math.Min(1, pct))
	filled := int(math.Round(pct * float64(width)))
	if pct > 0 && filled == 0 {
		filled = 1
	}
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat(".", width-filled) + "]"
}

func joinStatusSegments(separator string, segments []string) string {
	filtered := make([]string, 0, len(segments))
	for _, segment := range segments {
		if strings.TrimSpace(segment) == "" {
			continue
		}
		filtered = append(filtered, segment)
	}
	return strings.Join(filtered, " "+separator+" ")
}

func (m Model) contextUsagePrompt() string {
	if m.mode != AgentMode {
		return ""
	}

	text := strings.TrimSpace(m.input)
	if strings.HasPrefix(text, "/") {
		return ""
	}
	return text
}

func currentModelContextWindow(profile provider.Profile) int {
	if profile.SelectedModel == nil {
		return 0
	}
	if profile.SelectedModel.ContextWindow > 0 {
		return profile.SelectedModel.ContextWindow
	}
	if profile.SelectedModel.TopProvider.ContextWindow > 0 {
		return profile.SelectedModel.TopProvider.ContextWindow
	}
	return 0
}

func formatStatusTokenCount(value int) string {
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(value)/1_000_000)
	case value >= 10_000:
		return fmt.Sprintf("%dk", value/1000)
	case value >= 1000:
		return fmt.Sprintf("%.1fk", float64(value)/1000)
	default:
		return fmt.Sprintf("%d", value)
	}
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
	if m.sendingFullscreenKeys {
		switch {
		case width < 72:
			return []string{"[F1]", "[Enter]", "[Ctrl+Y]", "[Ctrl+J]", "[Esc]", "[F2]", "[Ctrl+C]"}
		case width < 100:
			return []string{"[F1] help", "[Enter] send", "[Ctrl+Y] send+Enter", "[Ctrl+J] insert Enter", "[Esc] cancel", "[F2] shell", "[Ctrl+C] quit"}
		default:
			return []string{"[F1] help", "[Enter] send exact", "[Ctrl+Y] send + Enter", "[Ctrl+J] insert Enter", "[Esc] cancel", "[F2] shell", "[Ctrl+C] quit"}
		}
	}

	switch {
	case width < 72:
		parts := []string{"[F1]", "[S-Tab]", "[Tab]", "[→]", "[Pg]", "[Enter]", "[Esc]", "[F2]", "[F10]", "[Ctrl+O]", "[Ctrl+C]"}
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
		} else if m.interactiveCheckInPaused && executionNeedsUserDrivenResume(m.activeExecution) {
			parts = append(parts, "[Ctrl+G]", "[R]")
		}
		return parts
	case width < 100:
		parts := []string{"[F1] help", "[Shift-Tab] mode", "[Tab] cycle/tab", "[→] accept", "[Alt+Up/Down] entry", "[Ctrl+O] detail", "[PgUp/PgDn] scroll", "[Enter] submit", escHint, "[F2] shell", "[F10] settings", "[Ctrl+C] quit"}
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
		} else if m.interactiveCheckInPaused && executionNeedsUserDrivenResume(m.activeExecution) {
			parts = append(parts, "[Ctrl+G] resume", "[R] agent")
		}
		return parts
	}

	parts := []string{"[F1] help", "[Shift-Tab] mode", "[Tab] cycle/tab", "[→] accept", "[Ctrl+O] detail", "[Enter] submit", escHint, "[Ctrl+J] newline", "[F2] shell", "[F10] settings"}
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
	} else if m.interactiveCheckInPaused && executionNeedsUserDrivenResume(m.activeExecution) {
		parts = append(parts, "[Ctrl+G] continue", "[R] tell agent")
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

func (m Model) resolvedTranscriptHeight(transcriptWidth int, screenHeight int, actionCard string, planCard string, activeExecutionCard string, statusLine string, shellTail string, composer string, footer string) int {
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
	sections = append(sections, composer, footer)
	bodyHeight := lipgloss.Height(strings.Join(sections, "\n"))
	if fillerHeight := screenHeight - bodyHeight; fillerHeight > 0 {
		transcriptHeight += fillerHeight
	}
	return transcriptHeight
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
	}
	filterLabel := "Filter: type to narrow visible lines"
	if strings.TrimSpace(m.detailFilter) != "" {
		filterLabel = fmt.Sprintf("Filter: %s", m.detailFilter)
	}
	bodyLines, matches, empty := m.detailBodyLines(contentWidth)
	if strings.TrimSpace(m.detailFilter) != "" {
		switch {
		case empty:
			filterLabel += "  (0 matches)"
		case matches == 1:
			filterLabel += "  (1 matching line)"
		default:
			filterLabel += fmt.Sprintf("  (%d matching lines)", matches)
		}
	}
	lines = append(lines, m.styles.detailMeta.Render(filterLabel), "")
	viewportHeight := height - lipgloss.Height(strings.Join(lines, "\n")) - m.styles.detail.GetVerticalFrameSize() - 2
	if viewportHeight < 4 {
		viewportHeight = 4
	}
	bodyLines = detailWindow(bodyLines, m.detailScroll, viewportHeight)
	for _, line := range bodyLines {
		if entry.Title == "result" {
			lines = append(lines, line)
			continue
		}
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
	if strings.TrimSpace(m.settingsBanner) != "" {
		lines = append(lines, m.styles.detailMeta.Render(m.settingsBanner), "")
	}

	switch m.settingsStep {
	case settingsStepSession:
		lines = append(lines, m.styles.detailBody.Render("Session Settings"))
		lines = append(lines, m.styles.detailMeta.Render("Adjust session-local behavior such as approval level."))
		lines = append(lines, m.renderSettingsApprovalModes(contentWidth)...)
	case settingsStepRuntime:
		lines = append(lines, m.styles.detailBody.Render("Runtime"))
		lines = append(lines, m.styles.detailMeta.Render("Select the session-authoritative coding runtime, review how it resolves, and persist it for future sessions."))
		lines = append(lines, m.renderSettingsRuntimes(contentWidth)...)
	case settingsStepProviders:
		lines = append(lines, m.styles.detailBody.Render("Configure Providers"))
		lines = append(lines, m.styles.detailMeta.Render("Edit provider settings and save them for future sessions."))
		lines = append(lines, m.renderSettingsProviders(contentWidth)...)
	case settingsStepProviderForm:
		if m.settingsConfig != nil {
			lines = append(lines, m.styles.detailBody.Render(m.settingsConfig.title))
			lines = append(lines, m.styles.detailMeta.Render("Edit provider settings and select a model on the same page."))
			lines = append(lines, m.renderSettingsConfig(contentWidth)...)
			lines = append(lines, "", m.styles.detailMeta.Render("Models"))
			lines = append(lines, m.renderSettingsModels(contentWidth)...)
		}
	default:
		lines = append(lines, m.styles.detailBody.Render("Current"))
		lines = append(lines, m.styles.detailMeta.Render(providerSummaryLine(m.activeProvider)))
		lines = append(lines, m.renderSettingsMenu(contentWidth)...)
	}

	lines = append(lines, "", m.styles.detailMeta.Render(settingsFooter(width, m.settingsStep, m)))
	return m.styles.detail.Width(contentWidth).Height(height).Render(strings.Join(lines, "\n"))
}

func helpContentLines(width int, mode Mode, canSendKeys bool) []string {
	lines := []string{
		"# Slash Commands",
		"/help: open the in-app help view",
		"/approvals: show the current session approval mode",
		"/approvals confirm: keep safe commands as proposals and require approval for risky actions",
		"/approvals auto: auto-run safe local inspection and test commands during agent work",
		"/approvals dangerous: disable Shuttle approval gating for agent commands and patches in this session",
		"/new: start a fresh task without restarting Shuttle or losing shell continuity",
		"/compact: summarize older task context and keep a shorter live context window",
		"/onboard or /onboarding: open Configure Providers in settings",
		"/provider or /providers: open Configure Providers in settings",
		"/model or /models: open the current provider detail focused on model selection",
		"/quit or /exit: leave Shuttle",
		"> Slash commands only trigger in agent mode. In shell mode, leading / stays a path.",
		"",
		"# Global Keys",
		"F1: open or close this help view",
		"F2: take control of the persistent shell",
		"F3: take control of the active tracked execution pane when Shuttle is running a separate owned command there",
		"F10: open settings",
		"Ctrl+C: quit Shuttle",
		"Ctrl+G: continue from the latest command result for an active plan, or resume paused interactive agent check-ins",
		"Shift-Tab: toggle between agent and shell mode",
		"Ctrl+O: open the selected transcript entry in the full detail view",
		"PgUp/PgDn: scroll transcript",
		"Ctrl+U / Ctrl+D: half-page transcript scroll",
		"Ctrl+Home / Ctrl+End: jump transcript to top or bottom",
		"Alt+Up / Alt+Down: move transcript selection",
	}
	if mode == ShellMode {
		lines = append(lines, "Up / Down: shell command history, or move within multiline composer text")
	} else {
		lines = append(lines, "Up / Down: agent prompt history, or move within multiline composer text")
	}
	lines = append(lines,
		"Home / End: move to the start or end of the current composer line",
		"Right Arrow: accept the current ghost-text completion",
		"Insert: toggle composer overwrite mode",
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
		lines = append(lines,
			"S: enter KEYS> mode to send raw keys to a fullscreen or waiting terminal app",
			"KEYS> Enter: send the current buffer exactly as typed",
			"KEYS> Ctrl+Y: send the current buffer plus Enter",
			"KEYS> Ctrl+J: insert a literal Enter/newline into the key sequence",
			"KEYS> Shift-Tab: dismiss KEYS> and suppress auto-reopen for the current prompt state",
			`KEYS> tokens like <Ctrl+C> or <Esc>: send tmux control-key events that the TUI cannot capture directly`,
			"Each KEYS> send requires a fresh observed read of the active terminal; Shuttle refreshes before retries.",
		)
	}
	lines = append(lines,
		"",
		"# Mouse",
		"Click a transcript icon/tag: open the full detail view for that entry",
		"Click a long shell-result command header: expand or collapse wrapped command text",
		"Mouse wheel over transcript: scroll transcript",
		"Click shell label in the shell-tail block: same as F2 take control",
		"Text selection while mouse mode is active: iTerm2 uses Option-drag; some other terminals use Shift-drag",
		"Ctrl+Shift+C / Ctrl+Shift+V: use your terminal copy and paste shortcuts for selected text and pasted input",
		"",
		"# Modes",
		"Shell mode: direct shell commands from "+shellComposerPrompt,
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
		lines = append(lines, m.styles.detailMeta.Render("Current runtime: "+currentRuntimeSummaryLine(m.activeRuntimeType, m.activeRuntimeCommand)))
		lines = append(lines, m.styles.detailMeta.Render("Current model: "+currentProviderModelLabel(m.activeProvider)))
	}
	return lines
}

func (m Model) renderSettingsApprovalModes(contentWidth int) []string {
	entries := settingsApprovalEntries()
	lines := make([]string, 0, len(entries)*3)
	currentMode := controller.ApprovalModeConfirm
	if m.ctrl != nil {
		currentMode = m.ctrl.ApprovalMode()
	}
	for index, entry := range entries {
		current := entry.mode == currentMode
		lines = append(lines, m.renderSettingsRow(entry.label, index == m.settingsApprovalIdx, current, false))
		lines = append(lines, m.renderSettingsMetaLine(controller.ApprovalModeStatusBody(entry.mode), index == m.settingsApprovalIdx, current, false))
	}
	_ = contentWidth
	return lines
}

func (m Model) renderSettingsRuntimes(contentWidth int) []string {
	lines := make([]string, 0, len(m.settingsRuntimes)*5)
	for index, entry := range m.settingsRuntimes {
		current := strings.TrimSpace(entry.runtimeType) == strings.TrimSpace(m.activeRuntimeType)
		label := entry.label
		if current {
			label += " (current)"
		}
		lines = append(lines, m.renderSettingsRow(label, index == m.settingsRuntimeIdx && !m.settingsRuntimeCommandFocus, current, entry.disabled))
		for _, line := range wrapParagraphs(entry.detail, max(10, contentWidth-2)) {
			lines = append(lines, m.renderSettingsMetaLine(line, index == m.settingsRuntimeIdx && !m.settingsRuntimeCommandFocus, current, entry.disabled))
		}
	}
	lines = append(lines, "", m.styles.detailMeta.Render("Command Path"))
	command := strings.TrimSpace(m.settingsRuntimeCommand)
	if command == "" {
		command = "(default command for selected runtime)"
	}
	prefix := "  "
	if m.settingsRuntimeCommandFocus {
		prefix = "> "
	}
	lines = append(lines, m.renderSettingsRow(prefix+command, m.settingsRuntimeCommandFocus, false, false))
	for _, line := range runtimeSettingsPreviewLines(m.activeRuntimeType, m.activeRuntimeCommand, selectedSettingsRuntimeType(m), m.settingsRuntimeCommand) {
		lines = append(lines, m.styles.detailMeta.Render(line))
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
		current := entry.candidate != nil && onboardingCandidateMatchesProfile(*entry.candidate, m.activeProvider)
		if current {
			label += " (current)"
		}
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
	profile := m.activeProvider
	if m.settingsConfig != nil {
		profile = m.settingsConfig.profile
	}
	lines := []string{m.renderSettingsCurrentLine("Current: " + currentProviderModelLabel(profile))}
	filterLine := "Type a model slug, or press Right Arrow to browse the model catalog."
	if m.isSettingsModelListFocused() {
		filterLine = "Enter to select  Esc to go back"
		if m.settingsModelBrowseAll {
			filterLine = fmt.Sprintf("Browsing full model list  (%d models)  Enter to select  Esc to go back", len(m.settingsModels))
		} else if strings.TrimSpace(m.settingsModelFilter) != "" {
			filterLine = fmt.Sprintf("Model filter: %s  (%d matches)  Enter to select  Esc to go back", m.settingsModelFilter, len(m.settingsModels))
		}
	} else if m.settingsModelBrowseAll {
		filterLine = fmt.Sprintf("Browsing full model list  (%d models)", len(m.settingsModels))
	} else if strings.TrimSpace(m.settingsModelFilter) != "" {
		filterLine = fmt.Sprintf("Model filter: %s  (%d matches)", m.settingsModelFilter, len(m.settingsModels))
	}
	lines = append(lines, m.styles.detailMeta.Render(filterLine))
	lines = append(lines, m.styles.detailMeta.Render("Shift+I shows extra model details for the highlighted row."))
	if helpText := settingsModelCatalogHelpText(m.settingsModelCatalog); helpText != "" {
		lines = append(lines, m.styles.detailMeta.Render(helpText))
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
		if choice.profile.Name != lastProvider && m.settingsStep != settingsStepProviderForm {
			lines = append(lines, m.styles.detailMeta.Render(choice.profile.Name))
			lastProvider = choice.profile.Name
		}
		current := choice.profile.Preset == profile.Preset && choice.model.ID == profile.Model
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
		return m.takeControlPersistentShellNow()
	case tea.KeyF3:
		return m.takeControlExecutionNow()
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
	hasExecutionTarget := canExecutionTakeControl(m)
	switch {
	case width < 40:
		if hasExecutionTarget {
			return "[F1/Esc] close  [Up/Down]  [Pg]  [F2] [F3]  [Ctrl+C]"
		}
		return "[F1/Esc] close  [Up/Down]  [Pg]  [F2]  [Ctrl+C]"
	case width < 64:
		if hasExecutionTarget {
			return "[F1/Esc] close  [Up/Down] scroll  [PgUp/PgDn] page  [F2] shell  [F3] cmd  [Ctrl+C] quit"
		}
		return "[F1/Esc] close  [Up/Down] scroll  [PgUp/PgDn] page  [F2] shell  [Ctrl+C] quit"
	default:
		if hasExecutionTarget {
			return "[F1/Esc] close  [Up/Down] scroll  [PgUp/PgDn] page  [Home/End] bounds  [F2] shell  [F3] cmd  [Ctrl+C] quit"
		}
		return "[F1/Esc] close  [Up/Down] scroll  [PgUp/PgDn] page  [Home/End] bounds  [F2] shell  [Ctrl+C] quit"
	}
}

func (m Model) renderSettingsConfig(contentWidth int) []string {
	if m.settingsConfig == nil {
		return nil
	}

	lines := []string{m.styles.detailMeta.Render(providerSummaryLine(m.settingsConfig.profile))}
	for index, field := range m.settingsConfig.fields {
		if field.hidden {
			continue
		}
		value := settingsFieldDisplayValue(field)
		lines = append(lines, m.renderSettingsRow(fmt.Sprintf("%s: %s", field.label, value), index == m.settingsConfig.index, false, false))
	}
	lines = append(lines, m.styles.detailMeta.Render("Use Left/Right or Space to toggle radio settings like Thinking."))
	lines = append(lines, m.styles.detailMeta.Render("API keys entered here are stored in the OS keyring."))
	return lines
}

func (m Model) renderOnboardingProviders(contentWidth int) []string {
	lines := make([]string, 0, len(m.onboardingChoices)*5)
	for index, choice := range m.onboardingChoices {
		prefix := "  "
		if index == m.onboardingIndex {
			prefix = "› "
		}

		current := onboardingCandidateMatchesProfile(choice, m.activeProvider)
		recommended := index == 0 && !current
		label := fmt.Sprintf("%s%s", prefix, onboardingCandidateLabel(choice, current, recommended))

		lines = append(lines, m.styles.detailBody.Render(label))
		lines = append(lines, m.styles.detailMeta.Render("   "+providerSummaryLine(choice.Profile)))
		lines = append(lines, m.styles.detailMeta.Render("   status: "+onboardingCandidateStatus(choice, current, recommended)))
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
		if field.hidden {
			continue
		}
		prefix := "  "
		if index == m.onboardingForm.index {
			prefix = "› "
		}
		label := fmt.Sprintf("%s%s: %s", prefix, field.label, settingsFieldDisplayValue(field))
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

func settingsChoiceOptionLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on":
		return "On"
	case "off":
		return "Off"
	case "xhigh":
		return "XHigh"
	default:
		if value == "" {
			return ""
		}
		return strings.ToUpper(value[:1]) + value[1:]
	}
}

func settingsFieldDisplayValue(field onboardingField) string {
	if len(field.options) > 0 {
		parts := make([]string, 0, len(field.options))
		for _, option := range field.options {
			marker := "( )"
			if option == field.value {
				marker = "(*)"
			}
			parts = append(parts, marker+" "+settingsChoiceOptionLabel(option))
		}
		return strings.Join(parts, "  ")
	}
	value := field.value
	switch {
	case field.secret && strings.TrimSpace(value) != "":
		return strings.Repeat("*", min(12, len(value)))
	case strings.TrimSpace(value) == "" && field.placeholder != "":
		return "<" + field.placeholder + ">"
	case strings.TrimSpace(value) == "":
		return "<empty>"
	default:
		return value
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
	parts := []string{fmt.Sprintf("preset=%s", profile.Preset), fmt.Sprintf("model=%s", profile.Model), fmt.Sprintf("base=%s", profile.BaseURL), fmt.Sprintf("auth=%s", auth)}
	if provider.SupportsThinking(profile) {
		parts = append(parts, fmt.Sprintf("thinking=%s", profile.Thinking))
	}
	if provider.SupportsReasoningEffort(profile) && provider.ThinkingEnabled(profile) {
		parts = append(parts, fmt.Sprintf("effort=%s", profile.ReasoningEffort))
	}
	return strings.Join(parts, "  ")
}

func onboardingCandidateLabel(candidate provider.OnboardingCandidate, current bool, recommended bool) string {
	flags := make([]string, 0, 2)
	if recommended {
		flags = append(flags, "recommended")
	}
	if current {
		flags = append(flags, "current")
	}
	if len(flags) == 0 {
		return candidate.Profile.Name
	}
	return candidate.Profile.Name + " (" + strings.Join(flags, ", ") + ")"
}

func onboardingCandidateStatus(candidate provider.OnboardingCandidate, current bool, recommended bool) string {
	status := onboardingCandidateSourceLabel(candidate)
	if current {
		status = "active " + status
	}
	if recommended {
		status = "best first-run match; " + status
	}
	return status
}

func onboardingCandidateSourceLabel(candidate provider.OnboardingCandidate) string {
	switch {
	case candidate.Manual || candidate.Source == provider.OnboardingCandidateManual:
		return "manual entry"
	case candidate.Source == provider.OnboardingCandidateStored:
		return "saved profile"
	case candidate.Source == provider.OnboardingCandidateDetected:
		return "detected working path"
	default:
		return "provider option"
	}
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
		{label: "Session Settings"},
		{label: "Runtime"},
		{label: "Configure Providers"},
	}
}

func settingsApprovalEntries() []settingsApprovalEntry {
	return []settingsApprovalEntry{
		{label: "Confirm", mode: controller.ApprovalModeConfirm},
		{label: "Auto", mode: controller.ApprovalModeAuto},
		{label: "Dangerous", mode: controller.ApprovalModeDanger},
	}
}

func settingsRuntimeEntries() []settingsRuntimeEntry {
	entries := []settingsRuntimeEntry{
		{label: runtimeLabel(provider.RuntimeAuto), runtimeType: provider.RuntimeAuto, detail: "Prefer the best installed authoritative runtime, then fall back to builtin when none are available."},
		{label: runtimeLabel(provider.RuntimeBuiltin), runtimeType: provider.RuntimeBuiltin, detail: "Use Shuttle's built-in runtime path for every reasoning turn."},
	}
	for _, candidate := range provider.DetectRuntimeInstallCandidates() {
		if strings.TrimSpace(candidate.Runtime) == "" || strings.TrimSpace(candidate.Runtime) == provider.RuntimePi {
			continue
		}
		detail := strings.TrimSpace(candidate.Name)
		if detail == "" {
			detail = runtimeLabel(candidate.Runtime)
		}
		detail = strings.ToUpper(detail[:1]) + detail[1:]
		switch {
		case candidate.Installed && candidate.Supported:
			detail += ". Installed and ready."
		case candidate.Installed && !candidate.Supported:
			detail += ". Installed but not currently usable: " + strings.TrimSpace(candidate.FailureReason)
		default:
			detail += ". Not currently found on PATH."
		}
		entries = append(entries, settingsRuntimeEntry{
			label:       runtimeLabel(candidate.Runtime),
			detail:      detail,
			runtimeType: candidate.Runtime,
		})
	}
	return entries
}

func currentRuntimeSummaryLine(runtimeType string, runtimeCommand string) string {
	resolved := provider.ResolveRuntimeSelection(runtimeType, runtimeCommand)
	label := runtimeLabel(resolved.RequestedType)
	if strings.TrimSpace(resolved.RequestedType) != strings.TrimSpace(resolved.SelectedType) && strings.TrimSpace(resolved.SelectedType) != "" {
		label += " -> " + runtimeLabel(resolved.SelectedType)
	}
	if command := strings.TrimSpace(resolved.Command); command != "" {
		return label + " / " + command
	}
	return label
}

func runtimeStatusLabel(selected string, effective string) string {
	selected = strings.TrimSpace(selected)
	effective = strings.TrimSpace(effective)
	if selected == "" {
		selected = effective
	}
	if selected == "" {
		return ""
	}
	if effective != "" && effective != selected {
		return runtimeLabel(selected) + "->" + runtimeLabel(effective)
	}
	return runtimeLabel(selected)
}

func runtimeSwitchBanner(runtimeType string, runtimeCommand string) string {
	resolved := provider.ResolveRuntimeSelection(runtimeType, runtimeCommand)
	return "Active runtime switched to " + currentRuntimeSummaryLine(resolved.RequestedType, resolved.Command) + "."
}

func runtimeSwitchNotice(runtimeType string, runtimeCommand string) string {
	resolved := provider.ResolveRuntimeSelection(runtimeType, runtimeCommand)
	label := "Runtime switched to " + currentRuntimeSummaryLine(resolved.RequestedType, resolved.Command) + "."
	if err := provider.ValidateResolvedRuntime(resolved); err != nil {
		return label + " Validation note: " + err.Error()
	}
	return label
}

func selectedSettingsRuntimeType(m Model) string {
	if len(m.settingsRuntimes) == 0 || m.settingsRuntimeIdx < 0 || m.settingsRuntimeIdx >= len(m.settingsRuntimes) {
		return m.activeRuntimeType
	}
	return m.settingsRuntimes[m.settingsRuntimeIdx].runtimeType
}

func runtimeSettingsPreviewLines(activeRuntimeType string, activeRuntimeCommand string, selectedRuntimeType string, selectedRuntimeCommand string) []string {
	lines := []string{}
	activeResolved := provider.ResolveRuntimeSelection(activeRuntimeType, activeRuntimeCommand)
	selectedResolved := provider.ResolveRuntimeSelection(selectedRuntimeType, selectedRuntimeCommand)
	lines = append(lines, "Current: "+currentRuntimeSummaryLine(activeResolved.RequestedType, activeResolved.Command))
	lines = append(lines, "Preview: "+currentRuntimeSummaryLine(selectedResolved.RequestedType, selectedResolved.Command))
	if selectedResolved.AutoSelected && strings.TrimSpace(selectedResolved.SelectedType) != "" {
		lines = append(lines, "Auto currently resolves to: "+runtimeLabel(selectedResolved.SelectedType))
	}
	if err := provider.ValidateResolvedRuntime(selectedResolved); err != nil {
		lines = append(lines, "Health: unavailable - "+err.Error())
	} else {
		lines = append(lines, "Health: ready")
	}
	if command := strings.TrimSpace(selectedResolved.Command); command != "" {
		lines = append(lines, "Effective command: "+command)
	}
	lines = append(lines, "Tab or Right Arrow focuses the command path. Enter applies the highlighted runtime with the current command path.")
	return lines
}

func runtimeLabel(runtimeType string) string {
	switch strings.TrimSpace(runtimeType) {
	case provider.RuntimeAuto:
		return "Auto"
	case provider.RuntimeBuiltin, "":
		return "Builtin"
	case provider.RuntimeCodexSDK:
		return "Codex CLI Bridge"
	case provider.RuntimeCodexAppServer:
		return "Codex App Server"
	case provider.RuntimePi:
		return "PI"
	default:
		return strings.TrimSpace(runtimeType)
	}
}

func buildSettingsProviderEntries(candidates []provider.OnboardingCandidate, active provider.Profile) []settingsProviderEntry {
	chosen := map[provider.ProviderPreset]provider.OnboardingCandidate{}
	for _, preset := range provider.OrderedProviderPresets() {
		if candidate, ok := chooseSettingsCandidate(candidates, preset, active); ok {
			chosen[preset] = candidate
		}
	}

	entries := []settingsProviderEntry{}
	for _, preset := range provider.OrderedProviderPresets() {
		candidate, ok := chosen[preset]
		if !ok {
			continue
		}
		candidateCopy := candidate
		entries = append(entries, settingsProviderEntry{
			label:     provider.ProviderLabel(preset),
			detail:    settingsProviderDetail(candidate),
			candidate: &candidateCopy,
		})
		if label, detail, ok := provider.ReservedSettingsEntry(preset); ok {
			entries = append(entries, settingsProviderEntry{
				label:    label,
				detail:   detail,
				disabled: true,
			})
		}
	}

	return entries
}

func chooseSettingsCandidate(candidates []provider.OnboardingCandidate, preset provider.ProviderPreset, active provider.Profile) (provider.OnboardingCandidate, bool) {
	var detected *provider.OnboardingCandidate
	var stored *provider.OnboardingCandidate
	var manual *provider.OnboardingCandidate
	for _, candidate := range candidates {
		if candidate.Profile.Preset != preset {
			continue
		}
		if onboardingCandidateMatchesProfile(candidate, active) {
			return candidate, true
		}
		candidateCopy := candidate
		switch candidate.Source {
		case provider.OnboardingCandidateStored:
			if stored == nil {
				stored = &candidateCopy
			}
		case provider.OnboardingCandidateManual:
			if manual == nil {
				manual = &candidateCopy
			}
		default:
			if candidate.Manual {
				if manual == nil {
					manual = &candidateCopy
				}
			} else if detected == nil {
				detected = &candidateCopy
			}
		}
	}
	if detected != nil {
		return *detected, true
	}
	if stored != nil {
		return *stored, true
	}
	if manual != nil {
		return *manual, true
	}
	return provider.OnboardingCandidate{}, false
}

func settingsProviderLabel(preset provider.ProviderPreset) string {
	return provider.ProviderLabel(preset)
}

func settingsProviderDetail(candidate provider.OnboardingCandidate) string {
	status := onboardingCandidateSourceLabel(candidate)
	if candidate.AuthSource != "" {
		status += " via " + candidate.AuthSource
	}
	if strings.TrimSpace(candidate.Reason) == "" {
		return status
	}
	return status + ". " + strings.TrimSpace(candidate.Reason)
}

func settingsProfileKey(profile provider.Profile) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s", profile.Preset, profile.BaseURL, profile.CLICommand, profile.Thinking, profile.ReasoningEffort)
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
						end:    segmentWidth + 1,
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
			start:  max(0, start-1),
			end:    start + labelWidth + 1,
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

func settingsModelCatalogHelpText(choices []settingsModelChoice) string {
	for _, choice := range choices {
		if helpText := provider.ModelCatalogHelpText(choice.profile.Preset); helpText != "" {
			return helpText
		}
	}
	return ""
}

func shouldValidateProviderModel(profile provider.Profile) bool {
	return provider.ShouldValidateModelSelection(profile)
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

func settingsFooter(width int, step settingsStep, m Model) string {
	if width < 72 {
		switch step {
		case settingsStepSession:
			return "Enter set mode  Esc back  Up/Down move  F2 shell  F10 close"
		case settingsStepRuntime:
			return "Enter apply  Tab focus cmd  Esc back  Up/Down move  F2 shell  F10 close"
		case settingsStepProviders:
			return "Enter edit  Esc back  Up/Down move  F2 shell  F10 close"
		case settingsStepProviderForm:
			return "Type edit  Left/Right toggle  Tab next  Enter pick/test  F7 test  F8 save+activate  Esc back  Pg page  F2 shell  F10 close"
		default:
			return "Enter open  Esc close  Up/Down move  F2 shell  F10 close"
		}
	}

	switch step {
	case settingsStepSession:
		return "Enter set session approval mode  Esc back  Up/Down move  F2 shell  F10 close"
	case settingsStepRuntime:
		return "Enter switch active runtime and persist it  Tab or Right Arrow focus command path  Left Arrow return to runtime list  Esc back  F2 shell  F10 close"
	case settingsStepProviders:
		return "Enter edit provider settings  Esc back  Up/Down move  F2 shell  F10 close"
	case settingsStepProviderForm:
		return "Type to edit fields  Left/Right or Space toggle radios  Tab/Up/Down move  Enter on Model to pick and test  F7 test provider  F8 save and activate  Esc back  PgUp/PgDn page  F2 shell  F10 close"
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

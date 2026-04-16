package tui

import (
	"context"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"aiterm/internal/controller"
	"aiterm/internal/logging"
	"aiterm/internal/provider"
	"aiterm/internal/shell"
	"aiterm/internal/tmux"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) refreshShellContextCmd() tea.Cmd {
	if m.ctrl == nil {
		return nil
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		promptContext, err := m.ctrl.RefreshShellContext(ctx)
		return refreshedShellContextMsg{
			context: promptContext,
			err:     err,
		}
	}
}

func (m Model) pollShellTailCmd() tea.Cmd {
	if m.ctrl == nil || !m.showShellTail {
		return nil
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), shellTailPollTimeout)
		defer cancel()

		tail, err := m.ctrl.PeekShellTail(ctx, shellTailPollLines)
		return shellTailMsg{
			tail: tail,
			err:  err,
		}
	}
}

func (m Model) shouldAcceptPolledShellTail() bool {
	if !m.showShellTail {
		return false
	}
	if m.activeExecution != nil {
		return true
	}
	return !(m.directShellPending || m.proposalRunPending || m.approvalInFlight)
}

func (m Model) pollActiveExecutionCmd() tea.Cmd {
	if m.ctrl == nil {
		return nil
	}
	epoch := m.activeExecutionPollEpoch

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		events, execution, err := m.ctrl.RefreshActiveExecution(ctx)
		return activeExecutionMsg{execution: execution, events: events, err: err, epoch: epoch}
	}
}

func (m Model) pollActiveExecutionAfter(delay time.Duration) tea.Cmd {
	if m.ctrl == nil {
		return nil
	}
	epoch := m.activeExecutionPollEpoch

	return tea.Tick(delay, func(time.Time) tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		events, execution, err := m.ctrl.RefreshActiveExecution(ctx)
		return activeExecutionMsg{execution: execution, events: events, err: err, epoch: epoch}
	})
}

func (m *Model) invalidateActiveExecutionPolls() {
	m.activeExecutionPollEpoch++
}

func (m Model) resumeAfterTakeControlAfter(delay time.Duration) tea.Cmd {
	if m.ctrl == nil {
		return nil
	}

	return tea.Tick(delay, func(time.Time) tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), m.currentAgentTurnTimeout())
		defer cancel()

		events, err := m.ctrl.ResumeAfterTakeControl(ctx)
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	})
}

func interruptShellCmd(config takeControlConfig, paneID string) tea.Cmd {
	if !config.enabled() || strings.TrimSpace(paneID) == "" {
		return nil
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		client, err := tmux.NewClient(config.SocketName)
		if err != nil {
			return shellInterruptMsg{err: err}
		}
		err = client.SendKeys(ctx, paneID, "C-c", false)
		return shellInterruptMsg{err: err}
	}
}

func sendFullscreenKeysCmd(config takeControlConfig, paneID string, keys string) tea.Cmd {
	if !config.enabled() || strings.TrimSpace(paneID) == "" {
		return nil
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		client, err := tmux.NewClient(config.SocketName)
		if err != nil {
			return fullscreenKeysSentMsg{keys: keys, err: err}
		}

		events := parseFullscreenKeyEvents(keys)
		for _, event := range events {
			if event.literal != "" {
				if err := client.SendLiteralKeys(ctx, paneID, event.literal); err != nil {
					return fullscreenKeysSentMsg{keys: keys, err: err}
				}
				continue
			}
			if event.tmuxKey != "" {
				if err := client.SendKeys(ctx, paneID, event.tmuxKey, false); err != nil {
					return fullscreenKeysSentMsg{keys: keys, err: err}
				}
			}
		}

		return fullscreenKeysSentMsg{keys: keys}
	}
}

const activeKeysLeaseTTL = 3 * time.Second
const autoFullscreenKeysCooldown = 1500 * time.Millisecond

var awaitingInputSubmitEnterPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)password(?: for [^:]+)?:\s*$`),
	regexp.MustCompile(`(?i)(?:^|[[:space:]])password(?: for [^:]+)?\s*$`),
	regexp.MustCompile(`(?i)passphrase(?: for [^:]+)?:\s*$`),
	regexp.MustCompile(`(?i)(?:^|[[:space:]])passphrase(?: for [^:]+)?\s*$`),
	regexp.MustCompile(`(?i)continue connecting.*\(yes/no`),
	regexp.MustCompile(`(?i)\(yes/no(?:/\[[^\]]+\])?\)\??\s*$`),
	regexp.MustCompile(`(?i)\[[yYnN]/[yYnN]\]\s*$`),
	regexp.MustCompile(`(?i)enter [^:]{1,80}:\s*$`),
	regexp.MustCompile(`(?i)(choice|selection|select|choose|option):\s*$`),
}

var awaitingInputCommandSubmitPrefixes = []string{
	"ssh ",
	"ssh\n",
	"sudo ",
	"sudo\n",
	"su ",
	"su\n",
	"passwd",
}

type fullscreenKeyEvent struct {
	literal string
	tmuxKey string
}

func parseFullscreenKeyEvents(keys string) []fullscreenKeyEvent {
	keys = strings.ReplaceAll(keys, "\r\n", "\n")
	events := make([]fullscreenKeyEvent, 0, len(keys))
	var literal strings.Builder

	flushLiteral := func() {
		if literal.Len() == 0 {
			return
		}
		events = append(events, fullscreenKeyEvent{literal: literal.String()})
		literal.Reset()
	}

	for len(keys) > 0 {
		if keys[0] == '\n' {
			flushLiteral()
			events = append(events, fullscreenKeyEvent{tmuxKey: "Enter"})
			keys = keys[1:]
			continue
		}
		if keys[0] == '<' {
			if end := strings.IndexByte(keys, '>'); end > 1 {
				if tmuxKey, ok := fullscreenTokenToTmuxKey(keys[1:end]); ok {
					flushLiteral()
					events = append(events, fullscreenKeyEvent{tmuxKey: tmuxKey})
					keys = keys[end+1:]
					continue
				}
			}
		}

		r, size := utf8.DecodeRuneInString(keys)
		literal.WriteRune(r)
		keys = keys[size:]
	}
	flushLiteral()
	return events
}

func fullscreenTokenToTmuxKey(token string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "ctrl+c", "ctrl-c", "c-c":
		return "C-c", true
	case "ctrl+d", "ctrl-d", "c-d":
		return "C-d", true
	case "ctrl+l", "ctrl-l", "c-l":
		return "C-l", true
	case "ctrl+z", "ctrl-z", "c-z":
		return "C-z", true
	case "ctrl+u", "ctrl-u", "c-u":
		return "C-u", true
	case "ctrl+w", "ctrl-w", "c-w":
		return "C-w", true
	case "ctrl+a", "ctrl-a", "c-a":
		return "C-a", true
	case "ctrl+e", "ctrl-e", "c-e":
		return "C-e", true
	case "enter", "return":
		return "Enter", true
	case "esc", "escape":
		return "Escape", true
	case "tab":
		return "Tab", true
	case "up":
		return "Up", true
	case "down":
		return "Down", true
	case "left":
		return "Left", true
	case "right":
		return "Right", true
	case "home":
		return "Home", true
	case "end":
		return "End", true
	case "pgup", "pageup":
		return "PageUp", true
	case "pgdn", "pagedown":
		return "PageDown", true
	case "delete", "del":
		return "Delete", true
	case "backspace", "bs":
		return "BSpace", true
	default:
		return "", false
	}
}

func (m *Model) handleInterruptedExecution() {
	logging.Trace("tui.execution.interrupted", "active_execution", activeExecutionID(m.activeExecution))
	m.busy = false
	m.busyStartedAt = time.Time{}
	m.showShellTail = false
	m.liveShellTail = ""
	m.suppressCancelErr = true
	m.handoffVisible = false
	m.handoffPriorState = ""
	if m.inFlightCancel != nil {
		m.inFlightCancel()
		m.inFlightCancel = nil
	}
	m.proposalRunPending = false
	m.approvalInFlight = false
	m.directShellPending = false
	m.syncActiveExecution(nil)
}

func (m *Model) abandonControllerExecution(reason string) *controller.CommandExecution {
	if m.ctrl == nil {
		return nil
	}

	execution := m.ctrl.AbandonActiveExecution(reason)
	if displayTail := executionDisplayTail(execution); strings.TrimSpace(displayTail) != "" {
		m.liveShellTail = displayTail
	}
	return execution
}

func executionDisplayTail(execution *controller.CommandExecution) string {
	if execution == nil {
		return ""
	}
	if strings.TrimSpace(execution.LatestDisplayTail) != "" {
		return execution.LatestDisplayTail
	}
	return execution.LatestOutputTail
}

func commandResultDisplaySummary(result *controller.CommandResultSummary) string {
	if result == nil {
		return ""
	}
	if strings.TrimSpace(result.DisplaySummary) != "" {
		return result.DisplaySummary
	}
	return result.Summary
}

func interruptedExecutionEntry(execution *controller.CommandExecution, summary string) Entry {
	if execution == nil {
		return Entry{
			Title: "system",
			Body:  summary,
		}
	}

	bodyLines := []string{"status=canceled"}
	displayTail := executionDisplayTail(execution)
	if strings.TrimSpace(displayTail) != "" {
		bodyLines = append(bodyLines, compactResultPreview(strings.TrimSpace(displayTail), 6))
	} else {
		bodyLines = append(bodyLines, "(no output)")
	}

	detail := []string{
		"command:",
		execution.Command,
		"",
		"status:",
		"canceled",
	}
	if strings.TrimSpace(summary) != "" {
		detail = append(detail, "", "summary:", summary)
	}
	if strings.TrimSpace(displayTail) != "" {
		detail = append(detail, "", "output so far:", strings.TrimSpace(displayTail))
	}

	return Entry{
		Title:   "result",
		Body:    strings.Join(bodyLines, "\n"),
		Detail:  strings.Join(detail, "\n"),
		TagKind: entryTagResultSigInt,
	}
}

func activeExecutionID(execution *controller.CommandExecution) string {
	if execution == nil {
		return ""
	}

	return execution.ID
}

func (m Model) currentExecutionOverview() controller.ExecutionOverview {
	localOverview := controller.ExecutionOverview{TrackedShell: controller.TrackedShellTarget{SessionName: strings.TrimSpace(m.takeControl.SessionName), PaneID: m.persistentTrackedPaneID()}}
	if m.activeExecution != nil {
		usesTrackedShell := true
		executionPane := strings.TrimSpace(m.activeExecution.TrackedShell.PaneID)
		if executionPane != "" && executionPane != m.persistentTrackedPaneID() {
			usesTrackedShell = false
		}
		active := controller.ActiveExecutionOverview{
			ID:               m.activeExecution.ID,
			Command:          m.activeExecution.Command,
			Origin:           m.activeExecution.Origin,
			State:            m.activeExecution.State,
			StartedAt:        m.activeExecution.StartedAt,
			UsesTrackedShell: usesTrackedShell,
		}
		if !usesTrackedShell && executionPane != "" {
			sessionName := strings.TrimSpace(m.activeExecution.TrackedShell.SessionName)
			if sessionName == "" {
				sessionName = strings.TrimSpace(localOverview.TrackedShell.SessionName)
			}
			active.ExecutionTakeControlTarget = controller.TrackedShellTarget{SessionName: sessionName, PaneID: executionPane}
		}
		localOverview.ActiveExecution = &active
	}
	if m.ctrl == nil {
		return localOverview
	}
	overview := m.ctrl.ExecutionOverview()
	if localOverview.ActiveExecution != nil {
		if overview.ActiveExecution == nil || strings.TrimSpace(overview.ActiveExecution.ID) != strings.TrimSpace(localOverview.ActiveExecution.ID) {
			return localOverview
		}
	}
	if overview.ActiveExecution != nil || strings.TrimSpace(overview.TrackedShell.PaneID) != "" {
		return overview
	}
	return localOverview
}

func (m Model) activeExecutionPaneID() string {
	if m.activeExecution != nil && strings.TrimSpace(m.activeExecution.TrackedShell.PaneID) != "" {
		return strings.TrimSpace(m.activeExecution.TrackedShell.PaneID)
	}
	return strings.TrimSpace(m.takeControl.TrackedPaneID)
}

func (m Model) persistentTrackedPaneID() string {
	if paneID := strings.TrimSpace(m.workspace.TopPane.ID); paneID != "" {
		return paneID
	}
	return strings.TrimSpace(m.takeControl.TrackedPaneID)
}

func (m Model) activeExecutionUsesTrackedShell() bool {
	overview := m.currentExecutionOverview()
	return overview.ActiveExecution != nil && overview.ActiveExecution.UsesTrackedShell
}

func (m Model) takeControlTargetsActiveExecution() bool {
	return strings.TrimSpace(m.activeExecutionTakeControlPaneID()) != ""
}

func (m Model) activeExecutionTakeControlPaneID() string {
	overview := m.currentExecutionOverview()
	if overview.ActiveExecution == nil {
		return ""
	}
	return strings.TrimSpace(overview.ActiveExecution.ExecutionTakeControlTarget.PaneID)
}

func errString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

func traceEntryTitles(entries []Entry) []string {
	titles := make([]string, 0, len(entries))
	for _, entry := range entries {
		titles = append(titles, entry.Title)
	}
	return titles
}

func (m *Model) maybeExecutionCheckInCmd(now time.Time) tea.Cmd {
	if m.ctrl == nil || m.checkInInFlight || m.activeExecution == nil {
		return nil
	}
	if !isAgentOwnedExecution(m.activeExecution.Origin) {
		return nil
	}
	switch m.activeExecution.State {
	case controller.CommandExecutionHandoffActive, controller.CommandExecutionCompleted, controller.CommandExecutionFailed, controller.CommandExecutionCanceled, controller.CommandExecutionLost:
		return nil
	}
	if executionNeedsUserDrivenResume(m.activeExecution) && m.interactiveCheckInPaused {
		return nil
	}

	dueAt := m.activeExecution.StartedAt.Add(firstCheckInDelay)
	if !m.lastCheckInAt.IsZero() {
		dueAt = m.lastCheckInAt.Add(repeatCheckInDelay)
	}
	if now.Before(dueAt) {
		return nil
	}

	m.checkInInFlight = true
	executionID := m.activeExecution.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), m.currentAgentTurnTimeout())
		defer cancel()

		events, err := m.ctrl.CheckActiveExecution(ctx)
		return activeExecutionCheckInMsg{
			executionID: executionID,
			events:      events,
			err:         err,
		}
	}
}

func executionNeedsUserDrivenResume(execution *controller.CommandExecution) bool {
	if execution == nil {
		return false
	}

	switch execution.State {
	case controller.CommandExecutionAwaitingInput, controller.CommandExecutionInteractiveFullscreen:
		return true
	default:
		return false
	}
}

func (m *Model) syncActiveExecution(execution *controller.CommandExecution) {
	currentID := ""
	previous := m.activeExecution
	if m.activeExecution != nil {
		currentID = m.activeExecution.ID
	}

	if execution == nil {
		m.invalidateActiveExecutionPolls()
		m.handoffReturnGraceUntil = time.Time{}
		m.activeExecutionMissingSince = time.Time{}
		m.activeExecution = nil
		m.exitFullscreenKeysMode()
		m.clearActiveKeysLease()
		m.activeKeysLease.lastNoticeID = ""
		m.dismissedAutoKeysFingerprint = ""
		m.suppressAutoKeysUntil = time.Time{}
		m.autoOpenedFullscreenKeys = false
		m.pendingFullscreen = nil
		m.lastFullscreenKeys = ""
		m.lastFullscreenKeysAt = time.Time{}
		m.checkInInFlight = false
		m.lastCheckInAt = time.Time{}
		m.interactiveCheckInCount = 0
		m.interactiveCheckInPaused = false
		m.pendingContinueAfterCommand = false
		m.lastInterruptNoticeID = ""
		m.handoffVisible = false
		m.handoffPriorState = ""
		return
	}

	if preserved := preserveObservedForegroundExecutionStart(previous, execution, m.handoffVisible || !m.handoffReturnGraceUntil.IsZero()); preserved != nil {
		execution = preserved
	}

	m.activeExecutionMissingSince = time.Time{}
	if currentID != execution.ID {
		m.clearActiveKeysLease()
		m.activeKeysLease.lastNoticeID = ""
		m.dismissedAutoKeysFingerprint = ""
		m.suppressAutoKeysUntil = time.Time{}
		m.autoOpenedFullscreenKeys = false
		m.lastFullscreenKeys = ""
		m.lastFullscreenKeysAt = time.Time{}
		m.checkInInFlight = false
		m.lastCheckInAt = time.Time{}
		m.interactiveCheckInCount = 0
		m.interactiveCheckInPaused = false
		m.pendingContinueAfterCommand = false
		m.lastInterruptNoticeID = ""
		m.handoffVisible = false
		m.handoffPriorState = ""
	}
	if m.activeExecution != nil &&
		m.activeExecution.ID == execution.ID &&
		m.handoffVisible &&
		m.activeExecution.State == controller.CommandExecutionHandoffActive &&
		execution.State != controller.CommandExecutionCompleted &&
		execution.State != controller.CommandExecutionFailed &&
		execution.State != controller.CommandExecutionCanceled &&
		execution.State != controller.CommandExecutionLost {
		executionCopy := *execution
		executionCopy.State = controller.CommandExecutionHandoffActive
		execution = &executionCopy
	}
	m.activeExecution = execution
	if execution.State != controller.CommandExecutionInteractiveFullscreen {
		m.pendingFullscreen = nil
		m.lastFullscreenKeys = ""
		m.lastFullscreenKeysAt = time.Time{}
	}
	if execution.State != controller.CommandExecutionInteractiveFullscreen &&
		execution.State != controller.CommandExecutionAwaitingInput {
		m.exitFullscreenKeysMode()
		m.clearActiveKeysLease()
		m.activeKeysLease.lastNoticeID = ""
		m.dismissedAutoKeysFingerprint = ""
		m.suppressAutoKeysUntil = time.Time{}
		m.autoOpenedFullscreenKeys = false
	}
	if !executionNeedsUserDrivenResume(execution) {
		m.interactiveCheckInCount = 0
		m.interactiveCheckInPaused = false
	}
}

func (m *Model) exitFullscreenKeysMode() {
	if !m.sendingFullscreenKeys {
		return
	}
	m.sendingFullscreenKeys = false
	m.autoOpenedFullscreenKeys = false
	m.setInput("")
}

func (m *Model) updatePendingContinueAfterCommand(events []controller.TranscriptEvent, autoContinue bool) {
	if autoContinue || m.activePlan == nil {
		m.pendingContinueAfterCommand = false
		return
	}
	if containsEventKind(events, controller.EventProposal) || containsEventKind(events, controller.EventApproval) || containsEventKind(events, controller.EventPatchApplyResult) {
		m.pendingContinueAfterCommand = false
		return
	}
	if containsEventKind(events, controller.EventCommandStart) {
		m.pendingContinueAfterCommand = false
		return
	}
	if containsEventKind(events, controller.EventCommandResult) {
		m.pendingContinueAfterCommand = true
	}
}

func preserveObservedForegroundExecutionStart(previous *controller.CommandExecution, next *controller.CommandExecution, handoffRelated bool) *controller.CommandExecution {
	if previous == nil || next == nil || !handoffRelated {
		return next
	}
	if previous.ID == next.ID {
		return next
	}
	if previous.StartedAt.IsZero() || next.StartedAt.IsZero() || !next.StartedAt.After(previous.StartedAt) {
		return next
	}
	if previous.Origin != controller.CommandOriginUserShell || next.Origin != controller.CommandOriginUserShell {
		return next
	}
	if previous.OwnershipMode != controller.CommandOwnershipSharedObserver || next.OwnershipMode != controller.CommandOwnershipSharedObserver {
		return next
	}
	if strings.TrimSpace(previous.Command) == "" || strings.TrimSpace(previous.Command) != strings.TrimSpace(next.Command) {
		return next
	}
	if strings.TrimSpace(previous.TrackedShell.SessionName) != strings.TrimSpace(next.TrackedShell.SessionName) {
		return next
	}
	if strings.TrimSpace(previous.TrackedShell.PaneID) != strings.TrimSpace(next.TrackedShell.PaneID) {
		return next
	}

	executionCopy := *next
	executionCopy.StartedAt = previous.StartedAt
	return &executionCopy
}

func (m *Model) syncTrackedShellTarget() {
	if m.ctrl == nil {
		return
	}

	previousSession := m.workspace.SessionName
	previousPane := m.workspace.TopPane.ID
	target := m.ctrl.TrackedShellTarget()
	sessionName := strings.TrimSpace(target.SessionName)
	paneID := strings.TrimSpace(target.PaneID)
	if paneID == "" {
		return
	}

	if sessionName != "" {
		m.workspace.SessionName = sessionName
		m.takeControl.SessionName = sessionName
	}
	m.workspace.TopPane.ID = paneID
	m.takeControl.TrackedPaneID = paneID
	m.takeControl.TemporaryPane = false

	if previousSession != m.workspace.SessionName || previousPane != m.workspace.TopPane.ID {
		logging.Trace(
			"tui.tracked_shell.synced",
			"previous_session", previousSession,
			"previous_pane", previousPane,
			"session", m.workspace.SessionName,
			"pane", m.workspace.TopPane.ID,
		)
	}
}

func (m Model) shouldPreserveExecutionAfterHandoff() bool {
	if m.activeExecution == nil || m.handoffReturnGraceUntil.IsZero() || time.Now().After(m.handoffReturnGraceUntil) {
		return false
	}
	switch m.activeExecution.State {
	case controller.CommandExecutionCompleted, controller.CommandExecutionFailed, controller.CommandExecutionCanceled, controller.CommandExecutionLost:
		return false
	default:
		return true
	}
}

func (m Model) shouldConfirmMissingActiveExecution() bool {
	if m.activeExecution == nil {
		return false
	}
	switch m.activeExecution.State {
	case controller.CommandExecutionCompleted, controller.CommandExecutionFailed, controller.CommandExecutionCanceled, controller.CommandExecutionLost:
		return false
	}
	if m.activeExecutionMissingSince.IsZero() {
		return true
	}
	return time.Since(m.activeExecutionMissingSince) < 3*time.Second
}

func (m Model) formatShellError(err error) string {
	if err == nil {
		return ""
	}

	message := err.Error()
	tail := strings.TrimSpace(m.liveShellTail)
	if tail == "" {
		return message
	}

	lines := strings.Split(tail, "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}

	return strings.TrimSpace(message + "\nlast shell output:\n" + strings.Join(lines, "\n"))
}

func (m *Model) syncActionState(events []controller.TranscriptEvent) {
	if shellContext := latestShellContext(events); shellContext != nil {
		m.shellContext = *shellContext
	}
	if result := latestCommandResult(events); result != nil {
		m.lastCommandResult = result
	}
	if modelInfo := latestModelInfo(events); modelInfo != nil {
		m.lastModelInfo = modelInfo
	}
	if containsEventKind(events, controller.EventCommandStart) && m.ctrl != nil {
		m.syncActiveExecution(m.ctrl.ActiveExecution())
	}

	newPlan := latestPlan(events)
	if newPlan != nil {
		if isCompletedPlan(*newPlan) {
			m.activePlan = nil
		} else {
			m.activePlan = newPlan
		}
	}

	newApproval := latestApproval(events)
	if newApproval != nil {
		m.pendingApproval = newApproval
		m.refiningApproval = nil
		m.refiningProposal = nil
		m.editingProposal = nil
		m.pendingProposal = nil
	}

	newProposal := latestProposal(events)
	if newProposal != nil && newApproval == nil {
		m.pendingProposal = newProposal
		m.refiningProposal = nil
		m.editingProposal = nil
		m.pendingApproval = nil
	}

	if m.approvalInFlight && !containsEventKind(events, controller.EventApproval) {
		m.pendingApproval = nil
	}
	if m.proposalRunPending {
		m.pendingProposal = nil
	}
	if containsEventKind(events, controller.EventPatchApplyResult) {
		m.showShellTail = false
		m.liveShellTail = ""
		m.syncActiveExecution(nil)
	}
	if containsEventKind(events, controller.EventCommandResult) || containsEventKind(events, controller.EventError) {
		m.showShellTail = false
		m.liveShellTail = ""
		m.syncActiveExecution(nil)
	}

	m.approvalInFlight = false
	m.proposalRunPending = false
	m.directShellPending = false
}

func (m Model) planProgressSummary() string {
	if m.activePlan == nil || len(m.activePlan.Steps) == 0 {
		return "Active plan ready"
	}

	done := 0
	current := 0
	for index, step := range m.activePlan.Steps {
		if step.Status == controller.PlanStepDone {
			done++
		}
		if step.Status == controller.PlanStepInProgress {
			current = index + 1
		}
	}
	if current == 0 && done < len(m.activePlan.Steps) {
		current = done + 1
	}
	if done == len(m.activePlan.Steps) {
		return fmt.Sprintf("Plan complete (%d/%d)", done, len(m.activePlan.Steps))
	}
	if done > 0 {
		return fmt.Sprintf("Step %d of %d in progress (%d complete)", current, len(m.activePlan.Steps), done)
	}
	return fmt.Sprintf("Step %d of %d in progress", current, len(m.activePlan.Steps))
}

func planStepMarker(status controller.PlanStepStatus) string {
	switch status {
	case controller.PlanStepDone:
		return "[x]"
	case controller.PlanStepInProgress:
		return "[>]"
	default:
		return "[ ]"
	}
}

func isCompletedPlan(plan controller.ActivePlan) bool {
	if len(plan.Steps) == 0 {
		return false
	}
	for _, step := range plan.Steps {
		if step.Status != controller.PlanStepDone {
			return false
		}
	}
	return true
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a int, b int) int {
	if a < b {
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

func latestPlan(events []controller.TranscriptEvent) *controller.ActivePlan {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != controller.EventPlan {
			continue
		}

		payload, ok := events[index].Payload.(controller.PlanPayload)
		if !ok {
			continue
		}

		plan := controller.ActivePlan{
			Summary: payload.Summary,
			Steps:   append([]controller.PlanStep(nil), payload.Steps...),
		}
		return &plan
	}

	return nil
}

func latestShellContext(events []controller.TranscriptEvent) *shell.PromptContext {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != controller.EventCommandResult {
			continue
		}

		payload, ok := events[index].Payload.(controller.CommandResultSummary)
		if !ok || payload.ShellContext == nil {
			continue
		}

		context := *payload.ShellContext
		return &context
	}

	return nil
}

func latestCommandResult(events []controller.TranscriptEvent) *controller.CommandResultSummary {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != controller.EventCommandResult {
			continue
		}

		payload, ok := events[index].Payload.(controller.CommandResultSummary)
		if !ok {
			continue
		}

		result := payload
		return &result
	}

	return nil
}

func latestProposal(events []controller.TranscriptEvent) *controller.ProposalPayload {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != controller.EventProposal {
			continue
		}

		payload, ok := events[index].Payload.(controller.ProposalPayload)
		if !ok || !isActionableProposalPayload(payload) {
			continue
		}

		proposal := payload
		return &proposal
	}

	return nil
}

func isActionableProposalPayload(payload controller.ProposalPayload) bool {
	return strings.TrimSpace(payload.Command) != "" ||
		payload.Keys != "" ||
		strings.TrimSpace(payload.Patch) != "" ||
		payload.Kind == controller.ProposalInspectContext
}

func latestModelInfo(events []controller.TranscriptEvent) *controller.AgentModelInfo {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != controller.EventModelInfo {
			continue
		}

		payload, ok := events[index].Payload.(controller.AgentModelInfo)
		if !ok {
			continue
		}

		info := payload
		return &info
	}

	return nil
}

func isAgentOwnedExecution(origin controller.CommandOrigin) bool {
	switch origin {
	case controller.CommandOriginAgentProposal, controller.CommandOriginAgentApproval, controller.CommandOriginAgentPlan:
		return true
	default:
		return false
	}
}

func (m Model) canAttemptLocalInterrupt() bool {
	if m.activeExecution == nil {
		return false
	}

	remoteSeen := false
	localSeen := false

	consider := func(context *shell.PromptContext) {
		if context == nil || context.PromptLine() == "" {
			return
		}
		if context.Remote {
			remoteSeen = true
			return
		}
		localSeen = true
	}

	contextCopy := m.shellContext
	consider(&contextCopy)
	consider(m.activeExecution.ShellContextAfter)
	consider(m.activeExecution.ShellContextBefore)

	if remoteSeen {
		return false
	}
	if localSeen {
		return true
	}

	return false
}

func (m Model) shouldConfirmFullscreenBeforeShellAction() bool {
	return m.pendingFullscreen == nil &&
		m.activeExecution != nil &&
		m.activeExecution.State == controller.CommandExecutionInteractiveFullscreen
}

func (m Model) canSendActiveKeys() bool {
	return m.pendingFullscreen == nil &&
		m.activeExecution != nil &&
		(m.activeExecution.State == controller.CommandExecutionInteractiveFullscreen ||
			m.activeExecution.State == controller.CommandExecutionAwaitingInput)
}

func (m *Model) observeActiveKeysLease(source string) {
	if !m.canSendActiveKeys() {
		m.clearActiveKeysLease()
		return
	}

	fingerprint := activeKeysLeaseFingerprint(m.activeExecution, m.liveShellTail)
	if fingerprint == "" {
		m.clearActiveKeysLease()
		return
	}

	noticeID := m.activeKeysLease.lastNoticeID
	m.activeKeysLease = activeKeysLease{
		executionID:  m.activeExecution.ID,
		state:        m.activeExecution.State,
		fingerprint:  fingerprint,
		observedAt:   time.Now(),
		source:       source,
		lastNoticeID: noticeID,
	}
}

func (m *Model) clearActiveKeysLease() {
	m.activeKeysLease.executionID = ""
	m.activeKeysLease.state = ""
	m.activeKeysLease.fingerprint = ""
	m.activeKeysLease.observedAt = time.Time{}
	m.activeKeysLease.source = ""
}

func activeKeysLeaseFingerprint(execution *controller.CommandExecution, shellTail string) string {
	if execution == nil {
		return ""
	}

	hash := fnv.New64a()
	_, _ = hash.Write([]byte(execution.ID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(execution.Command))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(execution.TrackedShell.SessionName))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(execution.TrackedShell.PaneID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(execution.State))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(execution.LatestOutputTail))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(shellTail))
	return fmt.Sprintf("%x", hash.Sum64())
}

func (m Model) hasFreshActiveKeysLease() bool {
	if !m.canSendActiveKeys() || m.activeExecution == nil {
		return false
	}
	if m.activeKeysLease.executionID != m.activeExecution.ID {
		return false
	}
	if m.activeKeysLease.state != m.activeExecution.State {
		return false
	}
	if m.activeKeysLease.fingerprint == "" {
		return false
	}
	if time.Since(m.activeKeysLease.observedAt) > activeKeysLeaseTTL {
		return false
	}
	return m.activeKeysLease.fingerprint == activeKeysLeaseFingerprint(m.activeExecution, m.liveShellTail)
}

func (m *Model) consumeFreshActiveKeysLease() bool {
	if !m.hasFreshActiveKeysLease() {
		return false
	}
	m.clearActiveKeysLease()
	return true
}

func (m *Model) activeKeysGuardCmd() tea.Cmd {
	if m.activeExecution == nil {
		return nil
	}

	noticeID := m.activeExecution.ID + "|" + string(m.activeExecution.State)
	if m.activeKeysLease.lastNoticeID != noticeID {
		m.appendTranscriptEntry(Entry{
			Title: "system",
			Body:  "Shuttle needs a fresh read of the active terminal before sending keys. It refreshed the active execution and shell tail; review the latest output, then retry KEYS>.",
		})
		m.activeKeysLease.lastNoticeID = noticeID
	}
	return tea.Batch(m.pollActiveExecutionCmd(), m.pollShellTailCmd())
}

func (m Model) shouldAutoOpenFullscreenKeys() bool {
	if m.sendingFullscreenKeys || m.activeExecution == nil {
		return false
	}
	if !m.suppressAutoKeysUntil.IsZero() && time.Now().Before(m.suppressAutoKeysUntil) {
		return false
	}
	if m.activeExecution.State != controller.CommandExecutionAwaitingInput {
		return false
	}
	if !m.hasFreshActiveKeysLease() {
		return false
	}
	if strings.TrimSpace(m.input) != "" {
		return false
	}
	if m.helpOpen || m.settingsOpen || m.onboardingOpen || m.detailOpen {
		return false
	}
	if m.pendingDangerousConfirm != nil || m.pendingFullscreen != nil || m.pendingProposal != nil || m.pendingApproval != nil {
		return false
	}
	if m.editingProposal != nil || m.refiningProposal != nil || m.refiningApproval != nil {
		return false
	}
	return m.dismissedAutoKeysFingerprint != m.activeKeysLease.fingerprint
}

func (m *Model) autoOpenFullscreenKeysIfNeeded() {
	if !m.shouldAutoOpenFullscreenKeys() {
		return
	}
	m.sendingFullscreenKeys = true
	m.autoOpenedFullscreenKeys = true
	m.setInput("")
}

func (m *Model) dismissFullscreenKeysAutoOpen() {
	if m.activeKeysLease.fingerprint != "" {
		m.dismissedAutoKeysFingerprint = m.activeKeysLease.fingerprint
	}
	m.sendingFullscreenKeys = false
	m.autoOpenedFullscreenKeys = false
	m.setInput("")
}

func (m *Model) suppressAutoFullscreenKeysForCurrentPrompt() {
	if m.activeExecution == nil {
		return
	}
	fingerprint := m.activeKeysLease.fingerprint
	if fingerprint == "" {
		fingerprint = activeKeysLeaseFingerprint(m.activeExecution, m.liveShellTail)
	}
	if fingerprint != "" {
		m.dismissedAutoKeysFingerprint = fingerprint
	}
	m.suppressAutoKeysUntil = time.Now().Add(autoFullscreenKeysCooldown)
	m.autoOpenedFullscreenKeys = false
}

func shouldAutoAppendEnterForAwaitingInput(tail string) bool {
	lines := recentNonEmptyLines(tail, 3)
	if len(lines) == 0 {
		return false
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.Trim(trimmed, `"'`)
		trimmed = strings.TrimSpace(trimmed)
		if trimmed == "" {
			continue
		}
		for _, pattern := range awaitingInputSubmitEnterPatterns {
			if pattern.MatchString(trimmed) {
				return true
			}
		}
	}
	return false
}

func shouldAutoAppendEnterForAwaitingInputCommand(command string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "" {
		return false
	}
	for _, prefix := range awaitingInputCommandSubmitPrefixes {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}

func shouldAutoAppendEnterForActiveExecution(execution *controller.CommandExecution, tails ...string) bool {
	for _, tail := range tails {
		if shouldAutoAppendEnterForAwaitingInput(tail) {
			return true
		}
	}
	if execution == nil || execution.State != controller.CommandExecutionAwaitingInput {
		return false
	}
	return shouldAutoAppendEnterForAwaitingInputCommand(execution.Command)
}

func recentNonEmptyLines(value string, limit int) []string {
	parts := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	lines := make([]string, 0, limit)
	for idx := len(parts) - 1; idx >= 0 && len(lines) < limit; idx-- {
		if strings.TrimSpace(parts[idx]) == "" {
			continue
		}
		lines = append(lines, parts[idx])
	}
	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}
	return lines
}

func (m *Model) appendInterruptNotice(body string) {
	if strings.TrimSpace(body) == "" {
		return
	}
	executionID := ""
	if m.activeExecution != nil {
		executionID = m.activeExecution.ID
	}
	if executionID != "" && m.lastInterruptNoticeID == executionID {
		return
	}
	pinned := m.isTranscriptPinned()
	m.entries = append(m.entries, Entry{
		Title: "system",
		Body:  body,
	})
	m.lastInterruptNoticeID = executionID
	if pinned {
		m.scrollTranscriptToBottom()
	} else {
		m.clampTranscriptScroll()
	}
	m.selectedEntry = len(m.entries) - 1
	m.clampSelection()
}

func (m *Model) pauseInteractiveCheckIns() {
	if m.interactiveCheckInPaused || m.activeExecution == nil {
		return
	}

	m.interactiveCheckInPaused = true
	m.appendTranscriptEntry(Entry{
		Title: "system",
		Body:  "Paused automatic agent check-ins for this interactive screen. Press Ctrl+G when you are ready to resume, or press R to tell the agent something else.",
	})
}

func previewFullscreenKeys(keys string) string {
	keys = strings.ReplaceAll(keys, "\n", "\\n")
	keys = strings.Trim(keys, "\r\n")
	if keys == "" {
		return "(empty)"
	}
	return logging.Preview(keys, 80)
}

func normalizeFullscreenKeys(keys string) string {
	keys = strings.ReplaceAll(keys, "\r\n", "\n")
	keys = strings.Trim(keys, "\r")
	return keys
}

func fullscreenKeysForSubmit(keys string, appendEnter bool) string {
	keys = normalizeFullscreenKeys(keys)
	if appendEnter {
		keys += "\n"
	}
	return keys
}

func humanizeExecutionState(state controller.CommandExecutionState) string {
	switch state {
	case controller.CommandExecutionRunning:
		return "running"
	case controller.CommandExecutionAwaitingInput:
		return "awaiting input"
	case controller.CommandExecutionInteractiveFullscreen:
		return "interactive fullscreen"
	case controller.CommandExecutionHandoffActive:
		return "handoff active"
	case controller.CommandExecutionBackgroundMonitor:
		return "background monitoring"
	case controller.CommandExecutionCompleted:
		return "completed"
	case controller.CommandExecutionFailed:
		return "failed"
	case controller.CommandExecutionCanceled:
		return "canceled"
	case controller.CommandExecutionLost:
		return "lost"
	default:
		return string(state)
	}
}

func humanizeExecutionOrigin(origin controller.CommandOrigin) string {
	switch origin {
	case controller.CommandOriginUserShell:
		return "shell"
	case controller.CommandOriginAgentProposal:
		return "agent proposal"
	case controller.CommandOriginAgentApproval:
		return "agent approval"
	case controller.CommandOriginAgentPlan:
		return "agent plan"
	default:
		return string(origin)
	}
}

func humanizeExecutionElapsed(startedAt time.Time) string {
	if startedAt.IsZero() {
		return "0s"
	}

	elapsed := time.Since(startedAt).Round(time.Second)
	if elapsed < time.Second {
		elapsed = time.Second
	}
	return elapsed.String()
}

func onboardingCandidateMatchesProfile(candidate provider.OnboardingCandidate, profile provider.Profile) bool {
	if candidate.Profile.Preset == "" || profile.Preset == "" {
		return false
	}
	return candidate.Profile.Preset == profile.Preset &&
		strings.TrimSpace(candidate.Profile.BaseURL) == strings.TrimSpace(profile.BaseURL) &&
		strings.TrimSpace(candidate.Profile.Model) == strings.TrimSpace(profile.Model) &&
		strings.TrimSpace(candidate.Profile.CLICommand) == strings.TrimSpace(profile.CLICommand) &&
		strings.TrimSpace(candidate.Profile.Thinking) == strings.TrimSpace(profile.Thinking) &&
		strings.TrimSpace(candidate.Profile.ReasoningEffort) == strings.TrimSpace(profile.ReasoningEffort)
}

func (m Model) currentProviderModelIndex() int {
	currentModel := m.activeProvider.Model
	if m.onboardingSelected != nil && strings.TrimSpace(m.onboardingSelected.Profile.Model) != "" {
		currentModel = m.onboardingSelected.Profile.Model
	}

	for index, model := range m.onboardingModels {
		if model.ID == currentModel {
			return index
		}
	}

	return 0
}

func (m Model) currentSettingsProviderIndex() int {
	fallback := 0
	for index, entry := range m.settingsProviders {
		if entry.candidate == nil {
			continue
		}
		if onboardingCandidateMatchesProfile(*entry.candidate, m.activeProvider) {
			return index
		}
		if entry.candidate.Profile.Preset == m.activeProvider.Preset {
			fallback = index
		}
	}
	return fallback
}

func currentSettingsApprovalIndex(ctrl controller.Controller) int {
	mode := controller.ApprovalModeConfirm
	if ctrl != nil {
		mode = ctrl.ApprovalMode()
	}
	for index, entry := range settingsApprovalEntries() {
		if entry.mode == mode {
			return index
		}
	}
	return 0
}

func (m Model) currentSettingsRuntimeIndex() int {
	current := strings.TrimSpace(m.activeRuntimeType)
	if current == "" {
		current = provider.RuntimeBuiltin
	}
	for index, entry := range m.settingsRuntimes {
		if strings.TrimSpace(entry.runtimeType) == current {
			return index
		}
	}
	return 0
}

func (m Model) currentSettingsModelIndex() int {
	profile := m.activeProvider
	if m.settingsConfig != nil {
		profile = m.settingsConfig.profile
	}
	for index, choice := range m.settingsModels {
		if choice.profile.Preset == profile.Preset && choice.model.ID == profile.Model {
			return index
		}
	}
	return 0
}

func (m *Model) appendTranscriptEntry(entry Entry) {
	pinned := m.isTranscriptPinned()
	m.entries = append(m.entries, entry)
	if pinned {
		m.scrollTranscriptToBottom()
	} else {
		m.clampTranscriptScroll()
	}
	m.selectedEntry = len(m.entries) - 1
	m.clampSelection()
}

func (m *Model) appendLocalTranscriptEcho(entry Entry) {
	m.appendTranscriptEntry(entry)
	echo := entry
	m.pendingLocalEcho = &echo
}

func (m *Model) consumePendingLocalEcho(entries []Entry) []Entry {
	if m.pendingLocalEcho == nil {
		return entries
	}

	echo := *m.pendingLocalEcho
	m.pendingLocalEcho = nil
	if len(entries) == 0 {
		return entries
	}
	if entries[0].Title == echo.Title && entries[0].Body == echo.Body {
		return entries[1:]
	}
	return entries
}

func (m *Model) collapseCommandEntries(entries []Entry) []Entry {
	if len(entries) == 0 {
		return entries
	}

	collapsed := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Title == "result" && strings.TrimSpace(entry.Command) != "" {
			command := strings.TrimSpace(entry.Command)
			if len(collapsed) > 0 {
				last := collapsed[len(collapsed)-1]
				if last.Title == "shell" && strings.TrimSpace(last.Body) == command {
					collapsed = collapsed[:len(collapsed)-1]
				}
			} else if len(m.entries) > 0 {
				lastIndex := len(m.entries) - 1
				last := m.entries[lastIndex]
				if last.Title == "shell" && strings.TrimSpace(last.Body) == command {
					m.entries = m.entries[:lastIndex]
				}
			}
		}
		collapsed = append(collapsed, entry)
	}

	return collapsed
}

func (m *Model) attachLatestModelInfo(events []controller.TranscriptEvent, entries []Entry) []Entry {
	info := latestModelInfo(events)
	if info == nil {
		return entries
	}
	detail := formatModelInfoDetail(*info)
	if attachModelInfoDetail(entries, detail) {
		return entries
	}
	if attachModelInfoDetail(m.entries, detail) {
		return entries
	}
	return append(entries, Entry{
		Title:  "system",
		Body:   detail,
		Detail: detail,
		Hidden: true,
	})
}

func attachModelInfoDetail(entries []Entry, detail string) bool {
	target := -1
	for index := len(entries) - 1; index >= 0; index-- {
		if entries[index].Hidden {
			continue
		}
		if entries[index].Title == "system" || entries[index].Title == "error" {
			continue
		}
		target = index
		break
	}
	if target == -1 {
		for index := len(entries) - 1; index >= 0; index-- {
			if entries[index].Hidden {
				continue
			}
			target = index
			break
		}
	}
	if target == -1 {
		return false
	}
	appendDetailSection(&entries[target], detail)
	return true
}

func (m *Model) applySettingsModelFilter() {
	selectedKey := ""
	if len(m.settingsModels) > 0 && m.settingsModelIdx >= 0 && m.settingsModelIdx < len(m.settingsModels) {
		selectedKey = settingsModelChoiceKey(m.settingsModels[m.settingsModelIdx])
	}

	m.settingsModelInfo = false
	if m.settingsModelBrowseAll {
		m.settingsModels = append([]settingsModelChoice(nil), m.settingsModelCatalog...)
	} else {
		m.settingsModels = filterSettingsModels(m.settingsModelCatalog, m.settingsModelFilter)
	}
	switch {
	case len(m.settingsModels) == 0:
		m.settingsModelIdx = 0
	case selectedKey != "":
		if index := findSettingsModelIndex(m.settingsModels, selectedKey); index >= 0 {
			m.settingsModelIdx = index
			return
		}
		fallthrough
	default:
		m.settingsModelIdx = m.currentSettingsModelIndex()
	}
}

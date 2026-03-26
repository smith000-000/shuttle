package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

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

func (m Model) pollActiveExecutionCmd() tea.Cmd {
	if m.ctrl == nil {
		return nil
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		events, execution, err := m.ctrl.RefreshActiveExecution(ctx)
		return activeExecutionMsg{execution: execution, events: events, err: err}
	}
}

func (m Model) pollActiveExecutionAfter(delay time.Duration) tea.Cmd {
	if m.ctrl == nil {
		return nil
	}

	return tea.Tick(delay, func(time.Time) tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		events, execution, err := m.ctrl.RefreshActiveExecution(ctx)
		return activeExecutionMsg{execution: execution, events: events, err: err}
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

		parts := strings.Split(strings.ReplaceAll(keys, "\r\n", "\n"), "\n")
		for index, part := range parts {
			if part != "" {
				if err := client.SendLiteralKeys(ctx, paneID, part); err != nil {
					return fullscreenKeysSentMsg{keys: keys, err: err}
				}
			}
			if index < len(parts)-1 {
				if err := client.SendKeys(ctx, paneID, "Enter", false); err != nil {
					return fullscreenKeysSentMsg{keys: keys, err: err}
				}
			}
		}

		return fullscreenKeysSentMsg{keys: keys}
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
	if execution != nil && strings.TrimSpace(execution.LatestOutputTail) != "" {
		m.liveShellTail = execution.LatestOutputTail
	}
	return execution
}

func interruptedExecutionEntry(execution *controller.CommandExecution, summary string) Entry {
	if execution == nil {
		return Entry{
			Title: "system",
			Body:  summary,
		}
	}

	bodyLines := []string{"status=canceled"}
	if strings.TrimSpace(execution.LatestOutputTail) != "" {
		bodyLines = append(bodyLines, compactResultPreview(strings.TrimSpace(execution.LatestOutputTail), 6))
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
	if strings.TrimSpace(execution.LatestOutputTail) != "" {
		detail = append(detail, "", "output so far:", strings.TrimSpace(execution.LatestOutputTail))
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

func (m Model) activeExecutionPaneID() string {
	if m.activeExecution != nil && strings.TrimSpace(m.activeExecution.TrackedShell.PaneID) != "" {
		return strings.TrimSpace(m.activeExecution.TrackedShell.PaneID)
	}
	return strings.TrimSpace(m.takeControl.TrackedPaneID)
}

func (m Model) activeExecutionUsesTrackedShell() bool {
	if m.activeExecution == nil {
		return false
	}
	executionPane := strings.TrimSpace(m.activeExecution.TrackedShell.PaneID)
	if executionPane == "" {
		return true
	}
	return executionPane == strings.TrimSpace(m.takeControl.TrackedPaneID)
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
		ctx, cancel := context.WithTimeout(context.Background(), agentTurnTimeout)
		defer cancel()

		events, err := m.ctrl.CheckActiveExecution(ctx)
		return activeExecutionCheckInMsg{
			executionID: executionID,
			events:      events,
			err:         err,
		}
	}
}

func (m *Model) syncActiveExecution(execution *controller.CommandExecution) {
	currentID := ""
	previous := m.activeExecution
	if m.activeExecution != nil {
		currentID = m.activeExecution.ID
	}

	if execution == nil {
		m.handoffReturnGraceUntil = time.Time{}
		m.activeExecutionMissingSince = time.Time{}
		m.activeExecution = nil
		m.pendingFullscreen = nil
		m.lastFullscreenKeys = ""
		m.lastFullscreenKeysAt = time.Time{}
		m.checkInInFlight = false
		m.lastCheckInAt = time.Time{}
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
		m.lastFullscreenKeys = ""
		m.lastFullscreenKeysAt = time.Time{}
		m.checkInInFlight = false
		m.lastCheckInAt = time.Time{}
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

	return fmt.Sprintf("Plan %d/%d", current, len(m.activePlan.Steps))
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
		strings.TrimSpace(payload.Patch) != ""
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

func (m Model) canSendFullscreenKeys() bool {
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

func (m Model) currentProviderChoiceIndex() int {
	for index, choice := range m.onboardingChoices {
		if choice.Profile.Preset == m.activeProvider.Preset && choice.Profile.Model == m.activeProvider.Model && choice.Profile.BaseURL == m.activeProvider.BaseURL {
			return index
		}
	}

	return 0
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
	for index, entry := range m.settingsProviders {
		if entry.candidate == nil {
			continue
		}
		if entry.candidate.Profile.Preset == m.activeProvider.Preset {
			return index
		}
	}
	return 0
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

func (m Model) currentSettingsModelIndex() int {
	for index, choice := range m.settingsModels {
		if choice.profile.Preset == m.activeProvider.Preset && choice.model.ID == m.activeProvider.Model {
			return index
		}
	}
	return 0
}

func (m Model) settingsConfiguredProfiles() []provider.Profile {
	profiles := []provider.Profile{}
	seen := map[string]struct{}{}

	if m.activeProvider.Preset != "" {
		key := settingsProfileKey(m.activeProvider)
		seen[key] = struct{}{}
		profiles = append(profiles, m.activeProvider)
	}

	for _, entry := range m.settingsProviders {
		if entry.candidate == nil || entry.candidate.Manual {
			continue
		}
		key := settingsProfileKey(entry.candidate.Profile)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		profiles = append(profiles, entry.candidate.Profile)
	}

	return profiles
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

func (m *Model) applySettingsModelFilter() {
	selectedKey := ""
	if len(m.settingsModels) > 0 && m.settingsModelIdx >= 0 && m.settingsModelIdx < len(m.settingsModels) {
		selectedKey = settingsModelChoiceKey(m.settingsModels[m.settingsModelIdx])
	}

	m.settingsModelInfo = false
	m.settingsModels = filterSettingsModels(m.settingsModelCatalog, m.settingsModelFilter)
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

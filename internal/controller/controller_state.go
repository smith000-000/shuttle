package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"aiterm/internal/logging"
)

func cloneCommandExecution(execution *CommandExecution) *CommandExecution {
	if execution == nil {
		return nil
	}
	copy := *execution
	return &copy
}

func executionSortLess(left *CommandExecution, right *CommandExecution) bool {
	if left == nil || right == nil {
		return false
	}
	if !left.StartedAt.Equal(right.StartedAt) {
		return left.StartedAt.Before(right.StartedAt)
	}
	return left.ID < right.ID
}

func (c *LocalController) primaryExecutionLocked() *CommandExecution {
	if len(c.executions) == 0 {
		return nil
	}
	if strings.TrimSpace(c.primaryExecution) != "" {
		if execution := c.executions[c.primaryExecution]; execution != nil {
			return execution
		}
	}

	var selected *CommandExecution
	for _, execution := range c.executions {
		if execution == nil {
			continue
		}
		if selected == nil || executionSortLess(selected, execution) {
			selected = execution
		}
	}
	if selected != nil {
		c.primaryExecution = selected.ID
	}
	return selected
}

func (c *LocalController) executionLocked(executionID string) *CommandExecution {
	if strings.TrimSpace(executionID) == "" || len(c.executions) == 0 {
		return nil
	}
	return c.executions[executionID]
}

func (c *LocalController) ensureExecutionRegistryLocked() {
	if c.executions == nil {
		c.executions = make(map[string]*CommandExecution)
	}
}

func (c *LocalController) ensureAttachedMonitorCancelsLocked() {
	if c.attachedMonitorCancels == nil {
		c.attachedMonitorCancels = make(map[string]context.CancelFunc)
	}
}

func (c *LocalController) ensureExecutionCleanupsLocked() {
	if c.executionCleanups == nil {
		c.executionCleanups = make(map[string]func(context.Context) error)
	}
}

func (c *LocalController) registerExecutionCleanupLocked(executionID string, cleanup func(context.Context) error) {
	if strings.TrimSpace(executionID) == "" || cleanup == nil {
		return
	}
	c.ensureExecutionCleanupsLocked()
	c.executionCleanups[executionID] = cleanup
}

func (c *LocalController) registerAttachedMonitorCancelLocked(executionID string, cancel context.CancelFunc) {
	if strings.TrimSpace(executionID) == "" || cancel == nil {
		return
	}
	c.ensureAttachedMonitorCancelsLocked()
	c.attachedMonitorCancels[executionID] = cancel
}

func (c *LocalController) cancelAttachedMonitorLocked(executionID string) {
	if strings.TrimSpace(executionID) == "" || len(c.attachedMonitorCancels) == 0 {
		return
	}
	cancel := c.attachedMonitorCancels[executionID]
	delete(c.attachedMonitorCancels, executionID)
	if cancel != nil {
		cancel()
	}
}

func (c *LocalController) takeExecutionCleanupLocked(executionID string) func(context.Context) error {
	if strings.TrimSpace(executionID) == "" || len(c.executionCleanups) == 0 {
		return nil
	}
	cleanup := c.executionCleanups[executionID]
	delete(c.executionCleanups, executionID)
	return cleanup
}

func (c *LocalController) registerExecutionLocked(execution CommandExecution) *CommandExecution {
	c.ensureExecutionRegistryLocked()
	executionCopy := execution
	c.executions[executionCopy.ID] = &executionCopy
	c.primaryExecution = executionCopy.ID
	c.syncTaskExecutionViewsLocked()
	return c.executions[executionCopy.ID]
}

func (c *LocalController) removeExecutionLocked(executionID string) func(context.Context) error {
	cleanup := c.takeExecutionCleanupLocked(executionID)
	if len(c.executions) == 0 {
		return cleanup
	}
	c.cancelAttachedMonitorLocked(executionID)
	delete(c.executions, executionID)
	if c.primaryExecution == executionID {
		c.primaryExecution = ""
	}
	c.syncTaskExecutionViewsLocked()
	return cleanup
}

func (c *LocalController) syncTaskExecutionViewsLocked() {
	if len(c.executions) == 0 {
		c.task.PrimaryExecutionID = ""
		c.task.ExecutionRegistry = nil
		c.task.CurrentExecution = nil
		return
	}

	registry := make([]*CommandExecution, 0, len(c.executions))
	for _, execution := range c.executions {
		if execution != nil {
			registry = append(registry, execution)
		}
	}
	sort.Slice(registry, func(i int, j int) bool {
		return executionSortLess(registry[i], registry[j])
	})

	snapshots := make([]CommandExecution, 0, len(registry))
	for _, execution := range registry {
		snapshots = append(snapshots, *execution)
	}
	c.task.ExecutionRegistry = snapshots

	primary := c.primaryExecutionLocked()
	if primary == nil {
		c.task.PrimaryExecutionID = ""
		c.task.CurrentExecution = nil
		return
	}
	c.task.PrimaryExecutionID = primary.ID
	c.task.CurrentExecution = cloneCommandExecution(primary)
}

func (c *LocalController) newEvent(kind TranscriptEventKind, payload any) TranscriptEvent {
	return TranscriptEvent{
		ID:        fmt.Sprintf("evt-%d", c.counter.Add(1)),
		Kind:      kind,
		Timestamp: time.Now(),
		Payload:   payload,
	}
}

func (c *LocalController) appendEvents(events ...TranscriptEvent) {
	c.task.PriorTranscript = append(c.task.PriorTranscript, events...)
}

func (c *LocalController) newExecutionLocked(command string, origin CommandOrigin) CommandExecution {
	execution := CommandExecution{
		ID:            fmt.Sprintf("cmd-%d", c.counter.Add(1)),
		Command:       command,
		Origin:        origin,
		TrackedShell:  c.session.TrackedShell,
		OwnershipMode: CommandOwnershipExclusive,
		State:         CommandExecutionRunning,
		StartedAt:     time.Now(),
	}
	if c.session.CurrentShell != nil {
		contextCopy := *c.session.CurrentShell
		execution.ShellContextBefore = &contextCopy
	}
	return execution
}

func (c *LocalController) bestEffortRecentOutputLocked() string {
	return c.bestEffortRecentOutputForExecutionLocked(c.primaryExecutionLocked())
}

func (c *LocalController) bestEffortRecentOutputForExecutionLocked(execution *CommandExecution) string {
	paneID := strings.TrimSpace(executionTarget(execution, c.session.TrackedShell).PaneID)
	if paneID == "" {
		paneID = strings.TrimSpace(c.session.TrackedShell.PaneID)
	}
	if c.reader == nil || paneID == "" {
		return ""
	}

	output, err := c.reader.CaptureRecentOutput(context.Background(), paneID, 20)
	if err != nil {
		return ""
	}

	return output
}

func executionTarget(execution *CommandExecution, fallback TrackedShellTarget) TrackedShellTarget {
	if execution == nil {
		return fallback
	}
	target := execution.TrackedShell
	if strings.TrimSpace(target.SessionName) == "" {
		target.SessionName = fallback.SessionName
	}
	if strings.TrimSpace(target.PaneID) == "" {
		target.PaneID = fallback.PaneID
	}
	return target
}

func executionSupportsDirectTakeControl(execution *CommandExecution) bool {
	if execution == nil {
		return false
	}

	switch execution.State {
	case CommandExecutionQueued, CommandExecutionCompleted, CommandExecutionFailed, CommandExecutionCanceled, CommandExecutionLost:
		return false
	default:
		return true
	}
}

func takeControlTargetForExecution(execution *CommandExecution, fallback TrackedShellTarget) TrackedShellTarget {
	if !executionSupportsDirectTakeControl(execution) {
		return fallback
	}
	return executionTarget(execution, fallback)
}

func sameTrackedShellTarget(left TrackedShellTarget, right TrackedShellTarget) bool {
	return strings.TrimSpace(left.SessionName) == strings.TrimSpace(right.SessionName) &&
		strings.TrimSpace(left.PaneID) == strings.TrimSpace(right.PaneID)
}

func shouldSyncExecutionIntoUserShellSession(execution *CommandExecution, session SessionContext) bool {
	return sameTrackedShellTarget(executionTarget(execution, session.TrackedShell), session.TrackedShell)
}

func (c *LocalController) TakeControlTarget() TrackedShellTarget {
	c.mu.Lock()
	defer c.mu.Unlock()
	return takeControlTargetForExecution(c.primaryExecutionLocked(), c.session.TrackedShell)
}

func runExecutionCleanup(cleanup func(context.Context) error) {
	if cleanup == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cleanup(ctx); err != nil {
		logging.TraceError("controller.execution.cleanup_error", err)
	}
}

func (c *LocalController) ownedExecutionStartDirLocked() string {
	if c.session.CurrentShell != nil && strings.TrimSpace(c.session.CurrentShell.Directory) != "" {
		if workingDirectory := normalizeShellWorkingDirectory(c.session.CurrentShell.Directory, c.session.CurrentShellLocation); workingDirectory != "" {
			return workingDirectory
		}
	}
	if workingDirectory := normalizeWorkingDirectory(c.session.WorkingDirectory); workingDirectory != "" {
		return workingDirectory
	}
	return "."
}

func (c *LocalController) appendSystemNotice(message string) TranscriptEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	event := c.newEvent(EventSystemNotice, TextPayload{Text: message})
	c.appendEvents(event)
	return event
}

func prependTranscriptEvent(events []TranscriptEvent, event *TranscriptEvent) []TranscriptEvent {
	if event == nil {
		return events
	}
	return append([]TranscriptEvent{*event}, events...)
}

func executionTransitionAllowed(from CommandExecutionState, to CommandExecutionState) bool {
	if from == "" || from == to {
		return true
	}
	switch from {
	case CommandExecutionCompleted, CommandExecutionFailed, CommandExecutionCanceled:
		return false
	case CommandExecutionLost:
		return to == CommandExecutionLost || to == CommandExecutionCompleted || to == CommandExecutionCanceled || to == CommandExecutionFailed
	default:
		return true
	}
}

func (c *LocalController) transitionExecutionStateLocked(execution *CommandExecution, next CommandExecutionState, source string) bool {
	if execution == nil || next == "" {
		return false
	}
	if !executionTransitionAllowed(execution.State, next) {
		logging.Trace(
			"controller.execution.transition_blocked",
			"execution_id", execution.ID,
			"from", execution.State,
			"to", next,
			"source", source,
		)
		return false
	}
	execution.State = next
	return true
}

func shouldCaptureRecoverySnapshot(execution *CommandExecution) bool {
	if execution == nil {
		return false
	}

	switch execution.State {
	case CommandExecutionAwaitingInput, CommandExecutionInteractiveFullscreen, CommandExecutionHandoffActive, CommandExecutionLost:
		return true
	default:
		return false
	}
}

func blocksSerialShellSubmission(execution *CommandExecution) bool {
	if execution == nil {
		return false
	}

	switch execution.State {
	case CommandExecutionCompleted, CommandExecutionFailed, CommandExecutionCanceled, CommandExecutionLost:
		return false
	default:
		return true
	}
}

func isAgentOwnedExecution(origin CommandOrigin) bool {
	switch origin {
	case CommandOriginAgentProposal, CommandOriginAgentApproval, CommandOriginAgentAuto, CommandOriginAgentPlan:
		return true
	default:
		return false
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

func eventKinds(events []TranscriptEvent) []string {
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, string(event.Kind))
	}
	return kinds
}

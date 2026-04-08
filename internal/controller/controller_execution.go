package controller

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"aiterm/internal/agentruntime"
	"aiterm/internal/logging"
	"aiterm/internal/shell"
)

func finalExecutionSummaryOutput(result shell.TrackedExecution, current *CommandExecution) string {
	if strings.TrimSpace(result.Captured) != "" {
		return result.Captured
	}
	if current != nil && strings.TrimSpace(current.LatestOutputTail) != "" {
		return current.LatestOutputTail
	}
	return result.Captured
}

func (c *LocalController) SubmitProposedShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error) {
	logging.Trace("controller.submit_proposed_shell_command", "command", command)
	if handled, events, err := c.handleInternalTmuxPaneProposal(ctx, command); handled {
		return events, err
	}
	if handled, events, err := c.handleRemoteLocalPathProposal(ctx, command); handled {
		return events, err
	}
	if handled, events, err := c.handleRemotePatchableProposal(ctx, command); handled {
		return events, err
	}
	return c.submitShellCommand(ctx, command, CommandOriginAgentProposal)
}

var tmuxPaneIDPattern = regexp.MustCompile(`%[0-9]+`)

func (c *LocalController) handleInternalTmuxPaneProposal(ctx context.Context, command string) (bool, []TranscriptEvent, error) {
	command = strings.TrimSpace(command)
	if command == "" || !isInternalTmuxInspectionCommand(command) {
		return false, nil, nil
	}

	c.mu.Lock()
	paneIDs := c.internalShuttlePaneIDsLocked()
	agentAvailable := c.runtimeHost != nil
	c.mu.Unlock()

	if !referencesAnyInternalPaneID(command, paneIDs) {
		return false, nil, nil
	}

	logging.Trace("controller.internal_tmux_guard.blocked", "command", command, "pane_ids", strings.Join(paneIDs, ","))
	if agentAvailable {
		events, err := c.submitAgentTurn(ctx, "", buildInternalTmuxRepairPrompt(command), nil, false)
		if err != nil {
			return true, events, err
		}
		if latest := latestProposalFromEvents(events); latest != nil && latest.Kind == ProposalCommand && !referencesAnyInternalPaneID(latest.Command, paneIDs) {
			return true, events, nil
		}
	}

	return true, c.blockInternalTmuxPaneProposal(command), nil
}

func (c *LocalController) SubmitShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error) {
	logging.Trace("controller.submit_shell_command", "command", command)
	return c.submitShellCommand(ctx, command, CommandOriginUserShell)
}

func (c *LocalController) handleRemotePatchableProposal(ctx context.Context, command string) (bool, []TranscriptEvent, error) {
	command = strings.TrimSpace(command)
	if command == "" || !isPatchReplaceableRemoteMutation(command) {
		return false, nil, nil
	}

	trackedShell := c.syncTrackedShellTarget(ctx)
	c.refreshUserShellContextForTarget(ctx, trackedShell, false)

	c.mu.Lock()
	currentShell := c.session.CurrentShell
	currentLocation := c.session.CurrentShellLocation
	agentAvailable := c.runtimeHost != nil
	c.mu.Unlock()

	if !isRemoteShellLocation(currentLocation, currentShell) {
		return false, nil, nil
	}

	logging.Trace("controller.remote_edit_guard.blocked", "command", command, "prompt", currentShell.PromptLine())
	if agentAvailable {
		events, err := c.submitAgentTurn(ctx, "", buildRemotePatchRepairPrompt(command, currentShell), nil, false)
		if err != nil {
			return true, events, err
		}
		if latest := latestProposalFromEvents(events); latest != nil && latest.Kind == ProposalPatch && strings.TrimSpace(latest.Patch) != "" && latest.PatchTarget == PatchTargetRemoteShell {
			return true, events, nil
		}
	}

	return true, c.enqueueRemoteMutationApproval(command, currentShell), nil
}

func (c *LocalController) handleRemoteLocalPathProposal(ctx context.Context, command string) (bool, []TranscriptEvent, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return false, nil, nil
	}

	trackedShell := c.syncTrackedShellTarget(ctx)
	c.refreshUserShellContextForTarget(ctx, trackedShell, false)

	c.mu.Lock()
	currentShell := c.session.CurrentShell
	currentLocation := c.session.CurrentShellLocation
	localWorkingDirectory := c.session.LocalWorkingDirectory
	localWorkspaceRoot := c.session.LocalWorkspaceRoot
	agentAvailable := c.runtimeHost != nil
	c.mu.Unlock()

	if !isRemoteShellLocation(currentLocation, currentShell) {
		return false, nil, nil
	}

	localHost := c.refreshLocalHostContext()
	if strings.TrimSpace(localWorkingDirectory) == "" {
		localWorkingDirectory = localHost.WorkingDirectory
	}
	localPaths := localOnlyPathsForRemoteGuard(localWorkspaceRoot, localWorkingDirectory, localHost.HomeDirectory)
	if len(localPaths) == 0 || !referencesAnyLocalOnlyPath(command, localPaths) {
		return false, nil, nil
	}

	logging.Trace("controller.remote_local_path_guard.blocked", "command", command, "prompt", currentShell.PromptLine(), "local_paths", strings.Join(localPaths, ","))
	if agentAvailable {
		events, err := c.submitAgentTurn(ctx, "", buildRemoteCommandPathRepairPrompt(command, currentShell, localPaths), nil, false)
		if err != nil {
			return true, events, err
		}
		if latest := latestProposalFromEvents(events); latest != nil && latest.Kind == ProposalCommand && !referencesAnyLocalOnlyPath(latest.Command, localPaths) {
			return true, events, nil
		}
	}

	return true, c.blockRemoteLocalPathProposal(command, currentShell, localPaths), nil
}

func buildRemotePatchRepairPrompt(command string, prompt *shell.PromptContext) string {
	location := "the active remote shell target"
	if prompt != nil && strings.TrimSpace(prompt.PromptLine()) != "" {
		location += " (" + strings.TrimSpace(prompt.PromptLine()) + ")"
	}
	cwd := ""
	if prompt != nil {
		location := shell.InferShellLocation(*prompt, "")
		cwd = normalizeShellWorkingDirectory(prompt.Directory, &location)
	}
	lines := []string{
		"The previous proposal was a raw remote shell file-edit command.",
		"Do not emit another shell mutation command.",
		"Revise it into exactly one proposal_kind:\"patch\" with proposal_patch_target:\"tracked_remote_shell\".",
		"The patch must target " + location + ".",
	}
	if cwd != "" {
		lines = append(lines, "Remote patch paths must be relative to this remote cwd: "+cwd)
	}
	lines = append(lines, "Original command: "+command)
	return strings.Join(lines, "\n")
}

func buildRemoteCommandPathRepairPrompt(command string, prompt *shell.PromptContext, localPaths []string) string {
	location := "the active remote shell target"
	if prompt != nil && strings.TrimSpace(prompt.PromptLine()) != "" {
		location += " (" + strings.TrimSpace(prompt.PromptLine()) + ")"
	}
	cwd := ""
	if prompt != nil {
		location := shell.InferShellLocation(*prompt, "")
		cwd = normalizeShellWorkingDirectory(prompt.Directory, &location)
	}
	lines := []string{
		"The previous proposal was a remote-shell command that referenced local machine paths.",
		"Do not emit another command that references local workspace or local home paths.",
		"Revise it into exactly one safe read-only proposal_kind:\"command\" for the active remote shell target.",
		"The command must target " + location + ".",
	}
	if cwd != "" {
		lines = append(lines, "Prefer paths relative to this remote cwd or remote home: "+cwd)
	}
	if len(localPaths) > 0 {
		lines = append(lines, "Do not reference these local-only paths: "+strings.Join(localPaths, ", "))
	}
	lines = append(lines, "Original command: "+command)
	return strings.Join(lines, "\n")
}

func latestProposalFromEvents(events []TranscriptEvent) *ProposalPayload {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != EventProposal {
			continue
		}
		payload, ok := events[i].Payload.(ProposalPayload)
		if !ok {
			continue
		}
		copyPayload := payload
		return &copyPayload
	}
	return nil
}

func (c *LocalController) internalShuttlePaneIDsLocked() []string {
	seen := map[string]struct{}{}
	paneIDs := []string{}
	appendPane := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		paneIDs = append(paneIDs, value)
	}

	appendPane(c.session.TrackedShell.PaneID)
	for _, execution := range c.executions {
		if execution == nil {
			continue
		}
		appendPane(execution.TrackedShell.PaneID)
	}
	return paneIDs
}

func isInternalTmuxInspectionCommand(command string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	if !strings.HasPrefix(command, "tmux ") {
		return false
	}
	return strings.Contains(command, "capture-pane") ||
		strings.Contains(command, "list-panes") ||
		strings.Contains(command, "display-message") ||
		strings.Contains(command, "show-messages")
}

func referencesAnyInternalPaneID(command string, paneIDs []string) bool {
	if len(paneIDs) == 0 {
		return false
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	commandPaneIDs := tmuxPaneIDPattern.FindAllString(command, -1)
	if len(commandPaneIDs) == 0 {
		return false
	}
	seen := map[string]struct{}{}
	for _, paneID := range paneIDs {
		seen[paneID] = struct{}{}
	}
	for _, paneID := range commandPaneIDs {
		if _, ok := seen[paneID]; ok {
			return true
		}
	}
	return false
}

func buildInternalTmuxRepairPrompt(command string) string {
	lines := []string{
		"The previous proposal targeted Shuttle-managed tmux pane IDs.",
		"Do not use tmux capture-pane, list-panes, display-message, or any direct tmux pane-id inspection command.",
		"Shuttle pane IDs are unstable implementation details, not a supported agent tool surface.",
		"If you need to verify interruption, completion, or shell state, use the latest command result, recovery snapshot, inspect_context, or one normal shell command that does not reference tmux pane IDs.",
		"Return exactly one revised next step or a brief answer if the task is already satisfied.",
		"Original command: " + command,
	}
	return strings.Join(lines, "\n")
}

func (c *LocalController) blockInternalTmuxPaneProposal(command string) []TranscriptEvent {
	c.mu.Lock()
	defer c.mu.Unlock()

	event := c.newEvent(EventSystemNotice, TextPayload{Text: "Blocked an agent proposal that targeted Shuttle-managed tmux pane IDs. Pane IDs are unstable implementation details; use command results, recovery context, or inspect_context instead."})
	c.appendEvents(event)
	return []TranscriptEvent{event}
}

type localHostContext struct {
	WorkingDirectory string
	HomeDirectory    string
	Username         string
	Hostname         string
}

func localOnlyPathsForRemoteGuard(localWorkspaceRoot string, localWorkingDirectory string, localHomeDirectory string) []string {
	seen := map[string]struct{}{}
	paths := []string{}
	appendPath := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || value == "/" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		paths = append(paths, value)
	}
	appendPath(localWorkspaceRoot)
	appendPath(localWorkingDirectory)
	appendPath(localHomeDirectory)
	return paths
}

func referencesAnyLocalOnlyPath(command string, localPaths []string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	for _, candidate := range localPaths {
		if candidate != "" && strings.Contains(command, candidate) {
			return true
		}
	}
	return false
}

func (c *LocalController) enqueueRemoteMutationApproval(command string, prompt *shell.PromptContext) []TranscriptEvent {
	c.mu.Lock()
	defer c.mu.Unlock()

	title := "Approve remote file-edit shell command"
	summary := "Remote file edits should prefer native patches. Approve this shell mutation command only if patching is not viable."
	if prompt != nil && strings.TrimSpace(prompt.PromptLine()) != "" {
		summary = "Remote file edits should prefer native patches on " + strings.TrimSpace(prompt.PromptLine()) + ". Approve this shell mutation command only if patching is not viable."
	}
	approval := ApprovalRequest{
		ID:      fmt.Sprintf("approval-%d", c.counter.Add(1)),
		Kind:    ApprovalCommand,
		Title:   title,
		Summary: summary,
		Command: command,
		Risk:    RiskMedium,
	}
	c.task.PendingApproval = &approval
	event := c.newEvent(EventApproval, approval)
	notice := c.newEvent(EventSystemNotice, TextPayload{Text: "Blocked a patch-replaceable remote edit command and converted it into an approval. Use Ctrl+O to inspect the original command."})
	c.appendEvents(event, notice)
	return []TranscriptEvent{event, notice}
}

func (c *LocalController) blockRemoteLocalPathProposal(command string, prompt *shell.PromptContext, localPaths []string) []TranscriptEvent {
	c.mu.Lock()
	defer c.mu.Unlock()

	text := "Blocked a remote-shell proposal that referenced local machine paths. Ask the agent to revise it for the active remote shell."
	if prompt != nil && strings.TrimSpace(prompt.PromptLine()) != "" {
		text = "Blocked a remote-shell proposal that referenced local machine paths on " + strings.TrimSpace(prompt.PromptLine()) + ". Ask the agent to revise it for the active remote shell."
	}
	if len(localPaths) > 0 {
		text += " Local-only paths: " + strings.Join(localPaths, ", ")
	}
	event := c.newEvent(EventError, TextPayload{Text: text})
	c.appendEvents(event)
	return []TranscriptEvent{event}
}

func isPatchReplaceableRemoteMutation(command string) bool {
	normalized := strings.ToLower(strings.TrimSpace(command))
	if normalized == "" {
		return false
	}
	patterns := []string{
		"sed -i",
		"sed --in-place",
		"perl -pi",
		"perl -0pi",
		"tee ",
		"cat >",
		"cat >>",
		" ed ",
		" ex ",
	}
	for _, pattern := range patterns {
		if strings.Contains(normalized, pattern) {
			return true
		}
	}
	if strings.Contains(normalized, " > ") || strings.Contains(normalized, " >> ") || strings.Contains(normalized, " 1>") {
		return true
	}
	return false
}

func (c *LocalController) prepareCommandExecutionTarget(ctx context.Context, trackedShell TrackedShellTarget, origin CommandOrigin) (TrackedShellTarget, func(context.Context) error, error) {
	if !isAgentOwnedExecution(origin) {
		return trackedShell, nil, nil
	}

	if promptContext := c.capturePromptContextForTarget(ctx, trackedShell); promptContext != nil {
		c.mu.Lock()
		c.applyPromptContextLocked(promptContext)
		currentLocation := c.session.CurrentShellLocation
		c.mu.Unlock()
		if isRemoteShellLocation(currentLocation, promptContext) {
			return trackedShell, nil, nil
		}
	}

	c.mu.Lock()
	currentShell := c.session.CurrentShell
	currentLocation := c.session.CurrentShellLocation
	c.mu.Unlock()
	if isRemoteShellLocation(currentLocation, currentShell) {
		return trackedShell, nil, nil
	}

	runner, ok := c.runner.(OwnedExecutionShellRunner)
	if !ok {
		return trackedShell, nil, nil
	}

	c.mu.Lock()
	startDir := c.ownedExecutionStartDirLocked()
	c.mu.Unlock()

	ownedPane, cleanup, err := runner.CreateOwnedExecutionPane(ctx, startDir)
	if err != nil {
		return TrackedShellTarget{}, nil, err
	}

	return TrackedShellTarget{
		SessionName: strings.TrimSpace(ownedPane.SessionName),
		PaneID:      strings.TrimSpace(ownedPane.PaneID),
	}, cleanup, nil
}

func (c *LocalController) submitShellCommand(ctx context.Context, command string, origin CommandOrigin) ([]TranscriptEvent, error) {
	trackedShell, trackedShellEvent := c.syncTrackedShellTargetWithNotice(ctx)
	c.refreshUserShellContextForTarget(ctx, trackedShell, false)
	executionTarget, executionCleanup, targetErr := c.prepareCommandExecutionTarget(ctx, trackedShell, origin)
	refreshTrackedShellAfterCommand := sameTrackedShellTarget(executionTarget, trackedShell)
	if targetErr != nil {
		errText := fmt.Sprintf("prepare command execution target: %v", targetErr)
		c.mu.Lock()
		errEvent := c.newEvent(EventError, TextPayload{Text: errText})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return prependTranscriptEvent([]TranscriptEvent{errEvent}, trackedShellEvent), nil
	}

	c.mu.Lock()
	if active := c.primaryExecutionLocked(); blocksSerialShellSubmission(active) {
		message := "another shell execution is already active; wait for it to finish or press F2 to take control"
		if active != nil && strings.TrimSpace(active.Command) != "" {
			message = fmt.Sprintf("shell command %q is still active; wait for it to finish or press F2 to take control", active.Command)
		}
		errEvent := c.newEvent(EventError, TextPayload{Text: message})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		logging.Trace(
			"controller.shell_command.rejected_active_execution",
			"command", command,
			"origin", origin,
			"active_execution_id", active.ID,
			"active_command", active.Command,
			"active_state", active.State,
		)
		return prependTranscriptEvent([]TranscriptEvent{errEvent}, trackedShellEvent), nil
	}
	execution := c.newExecutionLocked(command, origin)
	execution.TrackedShell = executionTarget
	registered := c.registerExecutionLocked(execution)
	if executionCleanup != nil {
		c.registerExecutionCleanupLocked(registered.ID, executionCleanup)
	}
	events := make([]TranscriptEvent, 0, 2)
	if executionCleanup != nil {
		events = append(events, c.newEvent(EventSystemNotice, TextPayload{Text: "Running command in owned execution pane."}))
	}
	startEvent := c.newEvent(EventCommandStart, CommandStartPayload{
		Command:   command,
		Execution: *registered,
	})
	events = append(events, startEvent)
	c.appendEvents(events...)
	c.mu.Unlock()

	logging.Trace(
		"controller.shell_command.start",
		"execution_id", execution.ID,
		"command", command,
		"origin", origin,
		"timeout_ms", shell.CommandTimeout(command).Milliseconds(),
	)

	if c.runner == nil {
		c.mu.Lock()
		failedExecution := execution
		failedExecution.State = CommandExecutionFailed
		failedExecution.Error = "shell command runner is not configured"
		cleanup := c.removeExecutionLocked(execution.ID)
		errEvent := c.newEvent(EventError, TextPayload{Text: "shell command runner is not configured"})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		runExecutionCleanup(cleanup)
		logging.Trace("controller.shell_command.runner_missing", "execution_id", execution.ID, "command", command)
		return prependTranscriptEvent(append(events, errEvent), trackedShellEvent), nil
	}

	result, err := c.runTrackedCommand(ctx, execution.ID, command)

	c.mu.Lock()

	current := c.executionLocked(execution.ID)
	if current == nil {
		c.mu.Unlock()
		logging.Trace(
			"controller.shell_command.stale_completion",
			"execution_id", execution.ID,
			"command", command,
			"origin", origin,
			"error", errString(err),
		)
		return nil, context.Canceled
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			cleanup := c.removeExecutionLocked(execution.ID)
			c.mu.Unlock()
			runExecutionCleanup(cleanup)
			logging.Trace("controller.shell_command.canceled", "execution_id", execution.ID, "command", command, "origin", origin)
			return prependTranscriptEvent(events, trackedShellEvent), err
		}
		if result.State == shell.MonitorStateLost {
			lostExecution := *current
			lostExecution.State = CommandExecutionLost
			lostExecution.Error = err.Error()
			if strings.TrimSpace(result.Captured) != "" {
				lostExecution.LatestOutputTail = result.Captured
			} else {
				lostExecution.LatestOutputTail = c.bestEffortRecentOutputLocked()
			}
			completedAt := time.Now()
			lostExecution.CompletedAt = &completedAt
			if result.ShellContext.PromptLine() != "" {
				shellContext := result.ShellContext
				lostExecution.ShellContextAfter = &shellContext
				c.applyPromptContextLocked(&shellContext)
			}
			if result.Location.Kind != "" {
				c.applyShellLocationLocked(result.Location)
			}

			summary := CommandResultSummary{
				ExecutionID:    execution.ID,
				CommandID:      result.CommandID,
				Command:        result.Command,
				Origin:         origin,
				State:          CommandExecutionLost,
				Cause:          result.Cause,
				Confidence:     result.Confidence,
				SemanticShell:  result.SemanticShell,
				SemanticSource: result.SemanticSource,
				ExitCode:       result.ExitCode,
				Summary:        lostExecution.LatestOutputTail,
			}
			if result.ShellContext.PromptLine() != "" {
				shellContext := result.ShellContext
				summary.ShellContext = &shellContext
			}
			c.task.LastCommandResult = &summary
			cleanup := c.removeExecutionLocked(execution.ID)
			resultEvent := c.newEvent(EventCommandResult, summary)
			c.appendEvents(resultEvent)
			c.mu.Unlock()
			runExecutionCleanup(cleanup)
			if refreshTrackedShellAfterCommand {
				c.refreshUserShellContext(ctx, true)
			}
			logging.Trace(
				"controller.shell_command.lost",
				"execution_id", execution.ID,
				"command", command,
				"origin", origin,
				"error", err.Error(),
				"tail_preview", logging.Preview(lostExecution.LatestOutputTail, 1000),
			)
			commandEvents := prependTranscriptEvent(append(events, resultEvent), trackedShellEvent)
			if isAgentOwnedExecution(origin) {
				recoveryEvents, recoveryErr := c.submitLostExecutionRecovery(ctx, lostExecution)
				if recoveryErr != nil {
					if errors.Is(recoveryErr, context.Canceled) {
						return commandEvents, recoveryErr
					}
					errEvent := c.appendRecoveryError(recoveryErr)
					if errEvent != nil {
						commandEvents = append(commandEvents, *errEvent)
					}
					return commandEvents, nil
				}
				commandEvents = append(commandEvents, recoveryEvents...)
			}
			return commandEvents, nil
		}
		failedExecution := *current
		failedExecution.State = CommandExecutionFailed
		failedExecution.Error = err.Error()
		failedExecution.LatestOutputTail = c.bestEffortRecentOutputForExecutionLocked(current)
		completedAt := time.Now()
		failedExecution.CompletedAt = &completedAt
		cleanup := c.removeExecutionLocked(execution.ID)
		errEvent := c.newEvent(EventError, TextPayload{Text: err.Error()})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		runExecutionCleanup(cleanup)
		logging.Trace(
			"controller.shell_command.error",
			"execution_id", execution.ID,
			"command", command,
			"origin", origin,
			"error", err.Error(),
			"tail_preview", logging.Preview(failedExecution.LatestOutputTail, 1000),
		)
		return prependTranscriptEvent(append(events, errEvent), trackedShellEvent), nil
	}

	if result.State == shell.MonitorStateCanceled {
		canceledExecution := *current
		canceledExecution.State = CommandExecutionCanceled
		finalOutput := finalExecutionSummaryOutput(result, current)
		canceledExecution.LatestOutputTail = finalOutput
		completedAt := time.Now()
		canceledExecution.CompletedAt = &completedAt
		exitCode := result.ExitCode
		canceledExecution.ExitCode = &exitCode

		summary := CommandResultSummary{
			ExecutionID:    execution.ID,
			CommandID:      result.CommandID,
			Command:        result.Command,
			Origin:         origin,
			State:          CommandExecutionCanceled,
			Cause:          result.Cause,
			Confidence:     result.Confidence,
			SemanticShell:  result.SemanticShell,
			SemanticSource: result.SemanticSource,
			ExitCode:       result.ExitCode,
			Summary:        finalOutput,
		}
		if result.ShellContext.PromptLine() != "" {
			shellContext := result.ShellContext
			summary.ShellContext = &shellContext
			canceledExecution.ShellContextAfter = &shellContext
			if shouldSyncExecutionIntoUserShellSession(current, c.session) {
				c.applyPromptContextLocked(&shellContext)
			}
		}
		if result.Location.Kind != "" && shouldSyncExecutionIntoUserShellSession(current, c.session) {
			c.applyShellLocationLocked(result.Location)
		}
		c.task.LastCommandResult = &summary
		cleanup := c.removeExecutionLocked(execution.ID)
		resultEvent := c.newEvent(EventCommandResult, summary)
		c.appendEvents(resultEvent)
		c.mu.Unlock()
		runExecutionCleanup(cleanup)
		if refreshTrackedShellAfterCommand {
			c.refreshUserShellContext(ctx, true)
		}
		logging.Trace(
			"controller.shell_command.canceled_result",
			"execution_id", execution.ID,
			"command", command,
			"origin", origin,
			"command_id", result.CommandID,
			"summary_preview", logging.Preview(finalOutput, 1000),
			"prompt", result.ShellContext.PromptLine(),
		)
		return prependTranscriptEvent(append(events, resultEvent), trackedShellEvent), nil
	}

	completedExecution := *current
	completedExecution.State = CommandExecutionCompleted
	finalOutput := finalExecutionSummaryOutput(result, current)
	completedExecution.LatestOutputTail = finalOutput
	completedAt := time.Now()
	completedExecution.CompletedAt = &completedAt
	exitCode := result.ExitCode
	completedExecution.ExitCode = &exitCode

	summary := CommandResultSummary{
		ExecutionID:    execution.ID,
		CommandID:      result.CommandID,
		Command:        result.Command,
		Origin:         origin,
		State:          CommandExecutionCompleted,
		Cause:          result.Cause,
		Confidence:     result.Confidence,
		SemanticShell:  result.SemanticShell,
		SemanticSource: result.SemanticSource,
		ExitCode:       result.ExitCode,
		Summary:        finalOutput,
	}
	if result.ShellContext.PromptLine() != "" {
		shellContext := result.ShellContext
		summary.ShellContext = &shellContext
		completedExecution.ShellContextAfter = &shellContext
		if shouldSyncExecutionIntoUserShellSession(current, c.session) {
			c.applyPromptContextLocked(&shellContext)
		}
	}
	if result.Location.Kind != "" && shouldSyncExecutionIntoUserShellSession(current, c.session) {
		c.applyShellLocationLocked(result.Location)
	}
	c.task.LastCommandResult = &summary
	cleanup := c.removeExecutionLocked(execution.ID)
	resultEvent := c.newEvent(EventCommandResult, summary)
	c.appendEvents(resultEvent)
	c.mu.Unlock()
	runExecutionCleanup(cleanup)
	if refreshTrackedShellAfterCommand {
		c.refreshUserShellContext(ctx, true)
	}
	logging.Trace(
		"controller.shell_command.complete",
		"execution_id", execution.ID,
		"command", command,
		"origin", origin,
		"command_id", result.CommandID,
		"exit_code", result.ExitCode,
		"summary_preview", logging.Preview(finalOutput, 1000),
		"prompt", result.ShellContext.PromptLine(),
	)
	return prependTranscriptEvent(append(events, resultEvent), trackedShellEvent), nil
}

func (c *LocalController) submitLostExecutionRecovery(ctx context.Context, execution CommandExecution) ([]TranscriptEvent, error) {
	if c.runtimeHost == nil {
		return nil, nil
	}

	c.refreshUserShellContext(ctx, false)
	c.mu.Lock()
	executionCopy := execution
	c.task.CurrentExecution = &executionCopy
	c.task.PrimaryExecutionID = execution.ID
	c.task.ExecutionRegistry = []CommandExecution{execution}
	c.task.RecoverySnapshot = c.captureRecoverySnapshot(ctx, executionTarget(&execution, c.session.TrackedShell).PaneID, &execution)
	c.mu.Unlock()

	outcome, err := c.runtime.Handle(ctx, c.runtimeHost, agentruntime.Request{
		Kind:   agentruntime.RequestLostExecutionRecovery,
		Prompt: appendPromptSuffix(lostTrackingCheckInPrompt, stateAuthorityPromptSuffix),
	})
	if err != nil {
		logging.TraceError(
			"controller.lost_recovery.error",
			err,
			"execution_id", execution.ID,
			"command", execution.Command,
		)
		return nil, err
	}
	events, _ := c.applyRuntimeOutcome(outcome, false, "")
	logging.Trace(
		"controller.lost_recovery.complete",
		"execution_id", execution.ID,
		"command", execution.Command,
		"event_kinds", eventKinds(events),
	)
	return events, nil
}

func (c *LocalController) appendRecoveryError(err error) *TranscriptEvent {
	if err == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	event := c.newEvent(EventError, TextPayload{Text: err.Error()})
	c.appendEvents(event)
	return &event
}

func (c *LocalController) DecideApproval(ctx context.Context, approvalID string, decision ApprovalDecision, refineText string) ([]TranscriptEvent, error) {
	logging.Trace("controller.decide_approval", "approval_id", approvalID, "decision", decision, "refine_text", refineText)
	c.mu.Lock()
	pending := c.task.PendingApproval
	if pending == nil || pending.ID != approvalID {
		errEvent := c.newEvent(EventError, TextPayload{Text: "approval request not found"})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return []TranscriptEvent{errEvent}, nil
	}

	switch decision {
	case DecisionReject:
		c.task.PendingApproval = nil
		event := c.newEvent(EventSystemNotice, TextPayload{Text: "Approval rejected."})
		c.appendEvents(event)
		c.mu.Unlock()
		return []TranscriptEvent{event}, nil
	case DecisionRefine:
		c.task.PendingApproval = nil
		message := "Approval sent back for refinement."
		if refineText != "" {
			message = "Refine request: " + refineText
		}
		event := c.newEvent(EventSystemNotice, TextPayload{Text: message})
		c.appendEvents(event)
		c.mu.Unlock()
		return []TranscriptEvent{event}, nil
	case DecisionApprove:
		patch := pending.Patch
		command := pending.Command
		c.task.PendingApproval = nil
		if pending.Kind == ApprovalPatch {
			if strings.TrimSpace(patch) == "" {
				event := c.newEvent(EventError, TextPayload{Text: "approval does not contain an applicable patch"})
				c.appendEvents(event)
				c.mu.Unlock()
				return []TranscriptEvent{event}, nil
			}
			c.mu.Unlock()
			return c.applyPatch(ctx, patch, pending.PatchTarget)
		}
		if strings.TrimSpace(command) == "" {
			event := c.newEvent(EventError, TextPayload{Text: "approval does not contain an executable command"})
			c.appendEvents(event)
			c.mu.Unlock()
			return []TranscriptEvent{event}, nil
		}
		c.mu.Unlock()
		return c.submitShellCommand(ctx, command, CommandOriginAgentApproval)
	default:
		c.mu.Unlock()
		return nil, fmt.Errorf("unsupported approval decision %q", decision)
	}
}

func (c *LocalController) ActiveExecution() *CommandExecution {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneCommandExecution(c.primaryExecutionLocked())
}

func (c *LocalController) RefreshActiveExecution(ctx context.Context) ([]TranscriptEvent, *CommandExecution, error) {
	trackedShell, trackedShellEvent := c.syncTrackedShellTargetWithNotice(ctx)

	c.mu.Lock()
	active := cloneCommandExecution(c.primaryExecutionLocked())
	attachInFlight := c.foregroundAttachInFlight
	c.mu.Unlock()
	if active != nil {
		logging.Trace("controller.refresh_active_execution", "outcome", "active", "execution_id", active.ID, "state", active.State)
		return prependTranscriptEvent(nil, trackedShellEvent), active, nil
	}
	if trackedShell.PaneID == "" {
		logging.Trace("controller.refresh_active_execution", "outcome", "no_tracked_pane")
		return prependTranscriptEvent(nil, trackedShellEvent), nil, nil
	}
	if attachInFlight {
		logging.Trace("controller.refresh_active_execution", "outcome", "attach_in_flight")
		return prependTranscriptEvent(nil, trackedShellEvent), nil, nil
	}

	events, attached, err := c.attachForegroundExecution(ctx)
	if err != nil {
		logging.TraceError("controller.refresh_active_execution.error", err)
		return nil, nil, err
	}
	if !attached {
		logging.Trace("controller.refresh_active_execution", "outcome", "no_attach")
		return events, nil, nil
	}

	c.mu.Lock()
	active = cloneCommandExecution(c.primaryExecutionLocked())
	c.mu.Unlock()
	if active != nil {
		logging.Trace("controller.refresh_active_execution", "outcome", "attached", "execution_id", active.ID, "state", active.State)
	}
	return events, active, nil
}

func (c *LocalController) AbandonActiveExecution(reason string) *CommandExecution {
	c.mu.Lock()
	defer c.mu.Unlock()

	execution := c.primaryExecutionLocked()
	if execution == nil {
		return nil
	}

	executionCopy := *execution
	executionCopy.State = CommandExecutionCanceled
	executionCopy.Error = strings.TrimSpace(reason)
	executionCopy.LatestOutputTail = c.bestEffortRecentOutputForExecutionLocked(execution)
	completedAt := time.Now()
	executionCopy.CompletedAt = &completedAt
	cleanup := c.removeExecutionLocked(execution.ID)
	c.appendEvents(c.newEvent(EventSystemNotice, TextPayload{Text: fmt.Sprintf("Abandoned active execution: %s", executionCopy.Command)}))
	logging.Trace(
		"controller.active_execution.abandoned",
		"execution_id", executionCopy.ID,
		"command", executionCopy.Command,
		"origin", executionCopy.Origin,
		"reason", reason,
		"tail_preview", logging.Preview(executionCopy.LatestOutputTail, 1000),
	)
	go runExecutionCleanup(cleanup)
	return &executionCopy
}

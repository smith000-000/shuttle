package controller

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	compactTaskTranscriptTail      = 8
	approxSystemPromptTokens       = 1462
	compactTaskPrompt              = "Summarize the current Shuttle task for future continuation. Return the summary only in message. Do not emit a plan, proposal, approval, keys proposal, patch, or shell command. Capture the user's goal, important work completed, relevant shell or workspace state, unresolved issues, and the next recommended step if one remains. Keep it concise but sufficient to resume the task later."
	startNewTaskSuccessNotice      = "Started a fresh task context. Shell continuity and provider settings were preserved."
	compactTaskSuccessNoticeFormat = "Compacted task context into a summary and kept %d recent transcript event(s)."
)

func (c *LocalController) StartNewTask(ctx context.Context) ([]TranscriptEvent, error) {
	c.refreshUserShellContext(ctx, true)

	c.mu.Lock()
	defer c.mu.Unlock()

	if blocked := c.contextActionBlockedLocked("start a new task"); blocked != nil {
		c.appendEvents(*blocked)
		return []TranscriptEvent{*blocked}, nil
	}

	c.task = TaskContext{TaskID: c.nextTaskIDLocked()}
	c.syncTaskExecutionViewsLocked()

	event := c.newEvent(EventSystemNotice, TextPayload{Text: startNewTaskSuccessNotice})
	c.appendEvents(event)
	return []TranscriptEvent{event}, nil
}

func (c *LocalController) CompactTask(ctx context.Context) ([]TranscriptEvent, error) {
	c.refreshUserShellContext(ctx, true)

	c.mu.Lock()
	if blocked := c.contextActionBlockedLocked("compact the current task"); blocked != nil {
		c.appendEvents(*blocked)
		c.mu.Unlock()
		return []TranscriptEvent{*blocked}, nil
	}
	if c.agent == nil {
		errEvent := c.newEvent(EventError, TextPayload{Text: "agent runtime is not configured"})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return []TranscriptEvent{errEvent}, nil
	}

	session := c.session
	task := c.task
	task.RecoverySnapshot = c.captureRecoverySnapshot(ctx, executionTarget(task.CurrentExecution, session.TrackedShell).PaneID, task.CurrentExecution)
	c.mu.Unlock()

	response, err := c.agent.Respond(ctx, AgentInput{
		Session: session,
		Task:    task,
		Prompt:  compactTaskPrompt,
	})
	if err != nil {
		if err == context.Canceled {
			return nil, err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		errEvent := c.newEvent(EventError, TextPayload{Text: err.Error()})
		c.appendEvents(errEvent)
		return []TranscriptEvent{errEvent}, nil
	}

	summary := strings.TrimSpace(response.Message)
	if summary == "" {
		c.mu.Lock()
		defer c.mu.Unlock()
		errEvent := c.newEvent(EventError, TextPayload{Text: "task compaction returned an empty summary"})
		c.appendEvents(errEvent)
		return []TranscriptEvent{errEvent}, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.task.CompactedSummary = summary
	c.task.RecoverySnapshot = ""
	c.task.PriorTranscript = tailTranscriptEvents(c.task.PriorTranscript, compactTaskTranscriptTail)

	event := c.newEvent(EventSystemNotice, TextPayload{
		Text: fmt.Sprintf(compactTaskSuccessNoticeFormat, len(c.task.PriorTranscript)),
	})
	c.appendEvents(event)
	return []TranscriptEvent{event}, nil
}

func (c *LocalController) EstimateContextUsage(prompt string) ContextWindowUsage {
	c.mu.Lock()
	input := AgentInput{
		Session: c.session,
		Task:    c.task,
		Prompt:  strings.TrimSpace(prompt),
	}
	c.mu.Unlock()

	return estimateContextUsage(input)
}

func (c *LocalController) contextActionBlockedLocked(action string) *TranscriptEvent {
	if execution := c.primaryExecutionLocked(); execution != nil {
		event := c.newEvent(EventError, TextPayload{
			Text: fmt.Sprintf("Cannot %s while command %q is still %s.", action, execution.Command, execution.State),
		})
		return &event
	}
	if c.task.PendingApproval != nil {
		event := c.newEvent(EventError, TextPayload{
			Text: fmt.Sprintf("Cannot %s while approval %q is still pending.", action, c.task.PendingApproval.Title),
		})
		return &event
	}
	return nil
}

func (c *LocalController) nextTaskIDLocked() string {
	current := strings.TrimSpace(c.task.TaskID)
	for {
		candidate := fmt.Sprintf("task-%d", c.counter.Add(1))
		if candidate != "" && candidate != current {
			return candidate
		}
	}
}

func tailTranscriptEvents(events []TranscriptEvent, limit int) []TranscriptEvent {
	if limit <= 0 || len(events) == 0 {
		return nil
	}
	if len(events) <= limit {
		return append([]TranscriptEvent(nil), events...)
	}
	return append([]TranscriptEvent(nil), events[len(events)-limit:]...)
}

func estimateContextUsage(input AgentInput) ContextWindowUsage {
	options := usageEstimateOptionsForTask(input.Task)
	var builder strings.Builder
	appendUsageSection(&builder, strings.TrimSpace(input.Prompt))

	if input.Session.CurrentShell != nil {
		appendUsageSection(&builder, input.Session.CurrentShell.PromptLine())
	}
	appendUsageSection(&builder, input.Session.SessionName)
	appendUsageSection(&builder, input.Session.TrackedShell.SessionName)
	appendUsageSection(&builder, input.Session.WorkingDirectory)
	appendUsageSection(&builder, input.Session.LocalWorkspaceRoot)
	appendUsageSection(&builder, compactUsageText(input.Session.RecentShellOutput, options.recentShellHeadLines, options.recentShellTailLines, options.recentShellMaxChars))
	appendUsageSection(&builder, strings.Join(input.Session.RecentManualCommands, "\n"))
	appendUsageSection(&builder, strings.Join(input.Session.RecentManualActions, "\n"))

	appendUsageSection(&builder, input.Task.CompactedSummary)
	appendUsageSection(&builder, summarizeUsageCommandResult(input.Task.LastCommandResult, options))
	appendUsageSection(&builder, summarizeUsagePatchResult(input.Task.LastPatchApplyResult))
	appendUsageSection(&builder, compactUsageText(input.Task.RecoverySnapshot, options.snapshotHeadLines, options.snapshotTailLines, options.snapshotMaxChars))
	appendUsageSection(&builder, summarizeUsagePlan(input.Task.ActivePlan))
	appendUsageSection(&builder, summarizeUsageApproval(input.Task.PendingApproval))
	appendUsageSection(&builder, summarizeUsageExecution(input.Task.CurrentExecution, options))
	appendUsageSection(&builder, summarizeUsageTranscript(input.Task.PriorTranscript, options.transcriptMaxEvents))

	runes := utf8.RuneCountInString(builder.String())
	if runes == 0 {
		return ContextWindowUsage{}
	}
	return ContextWindowUsage{
		ApproxPromptTokens: approxSystemPromptTokens + ((runes + 3) / 4),
	}
}

func appendUsageSection(builder *strings.Builder, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if builder.Len() > 0 {
		builder.WriteString("\n\n")
	}
	builder.WriteString(value)
}

type usageEstimateOptions struct {
	recentShellHeadLines    int
	recentShellTailLines    int
	recentShellMaxChars     int
	commandSummaryHeadLines int
	commandSummaryTailLines int
	commandSummaryMaxChars  int
	activeTailHeadLines     int
	activeTailTailLines     int
	activeTailMaxChars      int
	snapshotHeadLines       int
	snapshotTailLines       int
	snapshotMaxChars        int
	transcriptMaxEvents     int
}

func usageEstimateOptionsForTask(task TaskContext) usageEstimateOptions {
	if strings.TrimSpace(task.CompactedSummary) != "" {
		return usageEstimateOptions{
			recentShellHeadLines:    4,
			recentShellTailLines:    2,
			recentShellMaxChars:     600,
			commandSummaryHeadLines: 4,
			commandSummaryTailLines: 2,
			commandSummaryMaxChars:  400,
			activeTailHeadLines:     4,
			activeTailTailLines:     2,
			activeTailMaxChars:      300,
			snapshotHeadLines:       8,
			snapshotTailLines:       8,
			snapshotMaxChars:        1200,
			transcriptMaxEvents:     6,
		}
	}

	return usageEstimateOptions{
		recentShellHeadLines:    8,
		recentShellTailLines:    4,
		recentShellMaxChars:     1200,
		commandSummaryHeadLines: 8,
		commandSummaryTailLines: 4,
		commandSummaryMaxChars:  800,
		activeTailHeadLines:     6,
		activeTailTailLines:     3,
		activeTailMaxChars:      600,
		snapshotHeadLines:       20,
		snapshotTailLines:       20,
		snapshotMaxChars:        4000,
		transcriptMaxEvents:     16,
	}
}

func summarizeUsageCommandResult(result *CommandResultSummary, options usageEstimateOptions) string {
	if result == nil {
		return ""
	}
	return strings.TrimSpace(strings.Join([]string{
		result.Command,
		string(result.State),
		compactUsageText(result.Summary, options.commandSummaryHeadLines, options.commandSummaryTailLines, options.commandSummaryMaxChars),
	}, "\n"))
}

func summarizeUsagePatchResult(result *PatchApplySummary) string {
	if result == nil {
		return ""
	}
	return strings.TrimSpace(strings.Join([]string{
		result.WorkspaceRoot,
		result.Validation,
		result.Error,
	}, "\n"))
}

func summarizeUsagePlan(plan *ActivePlan) string {
	if plan == nil {
		return ""
	}

	lines := []string{plan.Summary}
	for _, step := range plan.Steps {
		lines = append(lines, string(step.Status)+" "+step.Text)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func summarizeUsageApproval(approval *ApprovalRequest) string {
	if approval == nil {
		return ""
	}
	return strings.TrimSpace(strings.Join([]string{
		approval.Title,
		approval.Summary,
		approval.Command,
		approval.Patch,
	}, "\n"))
}

func summarizeUsageExecution(execution *CommandExecution, options usageEstimateOptions) string {
	if execution == nil {
		return ""
	}
	return strings.TrimSpace(strings.Join([]string{
		execution.Command,
		string(execution.State),
		compactUsageText(execution.LatestOutputTail, options.activeTailHeadLines, options.activeTailTailLines, options.activeTailMaxChars),
	}, "\n"))
}

func summarizeUsageTranscript(events []TranscriptEvent, limit int) string {
	events = tailTranscriptEvents(events, limit)
	if len(events) == 0 {
		return ""
	}

	lines := make([]string, 0, len(events))
	for _, event := range events {
		lines = append(lines, summarizeUsageTranscriptEvent(event))
	}
	return strings.Join(lines, "\n")
}

func summarizeUsageTranscriptEvent(event TranscriptEvent) string {
	switch event.Kind {
	case EventUserMessage, EventAgentMessage, EventSystemNotice, EventError:
		payload, _ := event.Payload.(TextPayload)
		return clipUsageText(payload.Text, 240)
	case EventPlan:
		payload, _ := event.Payload.(PlanPayload)
		return clipUsageText(payload.Summary, 240)
	case EventProposal:
		payload, _ := event.Payload.(ProposalPayload)
		return clipUsageText(firstNonEmpty(payload.Description, payload.Command, payload.Patch), 240)
	case EventApproval:
		payload, _ := event.Payload.(ApprovalRequest)
		return clipUsageText(firstNonEmpty(payload.Summary, payload.Command, payload.Patch), 240)
	case EventCommandStart:
		payload, _ := event.Payload.(CommandStartPayload)
		return clipUsageText(payload.Command, 240)
	case EventCommandResult:
		payload, _ := event.Payload.(CommandResultSummary)
		return clipUsageText(payload.Command+"\n"+payload.Summary, 240)
	case EventPatchApplyResult:
		payload, _ := event.Payload.(PatchApplySummary)
		return clipUsageText(payload.Error, 240)
	case EventModelInfo:
		payload, _ := event.Payload.(AgentModelInfo)
		return clipUsageText(firstNonEmpty(payload.ResponseModel, payload.RequestedModel), 240)
	default:
		return string(event.Kind)
	}
}

func clipUsageText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxRunes <= 0 {
		return value
	}
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}

	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}

func compactUsageText(value string, headLines int, tailLines int, maxChars int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	lines := strings.Split(value, "\n")
	if headLines < 0 {
		headLines = 0
	}
	if tailLines < 0 {
		tailLines = 0
	}
	if len(lines) > headLines+tailLines && (headLines > 0 || tailLines > 0) {
		compacted := make([]string, 0, headLines+tailLines+1)
		if headLines > 0 {
			compacted = append(compacted, lines[:headLines]...)
		}
		omitted := len(lines) - headLines - tailLines
		if omitted > 0 {
			compacted = append(compacted, fmt.Sprintf("...(%d more lines omitted)...", omitted))
		}
		if tailLines > 0 {
			compacted = append(compacted, lines[len(lines)-tailLines:]...)
		}
		value = strings.Join(compacted, "\n")
	}

	if maxChars > 0 && utf8.RuneCountInString(value) > maxChars {
		return clipUsageText(value, maxChars)
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

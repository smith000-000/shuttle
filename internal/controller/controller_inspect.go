package controller

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"aiterm/internal/logging"
	"aiterm/internal/shell"
)

func (c *LocalController) InspectProposedContext(ctx context.Context) ([]TranscriptEvent, error) {
	logging.Trace("controller.inspect_proposed_context")
	summary, promptContext, err := c.inspectProposedContextSummary(ctx)
	if err != nil {
		return nil, err
	}

	resultEvent := c.newEvent(EventCommandResult, summary)

	c.mu.Lock()
	c.task.LastCommandResult = &summary
	if promptContext != nil {
		contextCopy := *promptContext
		c.applyPromptContextLocked(&contextCopy)
	}
	c.appendEvents(resultEvent)
	c.mu.Unlock()

	return []TranscriptEvent{resultEvent}, nil
}

func (c *LocalController) inspectProposedContextSummary(ctx context.Context) (CommandResultSummary, *shell.PromptContext, error) {
	logging.Trace("controller.inspect_proposed_context.summary")
	promptContext, err := c.RefreshShellContext(ctx)
	if err != nil {
		return CommandResultSummary{}, nil, err
	}

	summary := CommandResultSummary{
		CommandID:     fmt.Sprintf("inspect-%d", c.counter.Add(1)),
		Command:       inspectContextCommandLabel,
		Origin:        CommandOriginAgentProposal,
		State:         CommandExecutionCompleted,
		Cause:         shell.CompletionCausePromptReturn,
		Confidence:    shell.ConfidenceStrong,
		SemanticShell: true,
		ExitCode:      0,
		Summary:       c.summarizeInspectedShellContext(promptContext),
	}
	if promptContext != nil {
		contextCopy := *promptContext
		summary.ShellContext = &contextCopy
	}
	return summary, promptContext, nil
}

func (c *LocalController) summarizeInspectedShellContext(promptContext *shell.PromptContext) string {
	if promptContext == nil {
		return "shell context unavailable"
	}

	lines := make([]string, 0, 12)

	userHost := strings.TrimSpace(promptContext.User)
	if host := strings.TrimSpace(promptContext.Host); host != "" {
		if userHost != "" {
			userHost += "@"
		}
		userHost += host
	}
	if userHost != "" {
		lines = append(lines, fmt.Sprintf("user_host=%s", userHost))
		lines = append(lines, fmt.Sprintf("shell_target=%s", userHost))
	}
	location := effectiveShellLocation(c.session.CurrentShellLocation, promptContext)
	if cwd := strings.TrimSpace(location.Directory); cwd != "" {
		lines = append(lines, fmt.Sprintf("cwd=%s", cwd))
	}
	if location.DirectorySource != "" && location.DirectorySource != shell.ShellDirectorySourceUnknown {
		lines = append(lines, fmt.Sprintf("cwd_source=%s", location.DirectorySource))
	}
	if location.DirectoryConfidence != "" {
		lines = append(lines, fmt.Sprintf("cwd_confidence=%s", location.DirectoryConfidence))
	}
	lines = append(lines, fmt.Sprintf("cwd_authoritative=%t", location.DirectorySource == shell.ShellDirectorySourceProbe))
	remote := location.Kind == shell.ShellLocationRemote
	lines = append(lines, fmt.Sprintf("remote=%t", remote))
	if location.Kind != "" {
		lines = append(lines, fmt.Sprintf("shell_location=%s", location.Kind))
	}
	if system := strings.TrimSpace(promptContext.System); system != "" {
		lines = append(lines, fmt.Sprintf("system=%s", system))
	}
	if branch := strings.TrimSpace(promptContext.GitBranch); branch != "" {
		lines = append(lines, fmt.Sprintf("git_branch=%s", branch))
	}
	if prompt := strings.TrimSpace(promptContext.PromptLine()); prompt != "" {
		lines = append(lines, fmt.Sprintf("prompt=%s", prompt))
	}
	if localRoot := strings.TrimSpace(c.session.LocalWorkspaceRoot); localRoot != "" {
		lines = append(lines, fmt.Sprintf("local_workspace_root=%s", localRoot))
		if cwd := strings.TrimSpace(location.Directory); cwd != "" {
			lines = append(lines, fmt.Sprintf("workspace_relation=%s", workspaceRelation(cwd, localRoot)))
		}
	}
	if remote {
		if remoteRoot := strings.TrimSpace(c.session.WorkingDirectory); remoteRoot != "" {
			lines = append(lines, fmt.Sprintf("remote_patch_root=%s", remoteRoot))
		}
		if caps := c.session.RemoteCapabilities; caps != nil {
			if capLine := formatRemoteCapabilities(caps); capLine != "" {
				lines = append(lines, "remote_capabilities="+capLine)
			}
			if caps.LastSuccessfulTransport != PatchTransportNone {
				lines = append(lines, fmt.Sprintf("remote_last_patch_transport=%s", caps.LastSuccessfulTransport))
			}
			if identity := strings.TrimSpace(caps.Identity); identity != "" {
				lines = append(lines, fmt.Sprintf("remote_identity=%s", identity))
			}
		}
	}
	if len(lines) == 0 {
		return "shell context unavailable"
	}
	return strings.Join(lines, "\n")
}

func workspaceRelation(cwd string, localRoot string) string {
	cwd = strings.TrimSpace(cwd)
	localRoot = strings.TrimSpace(localRoot)
	if cwd == "" || localRoot == "" {
		return "unknown"
	}
	if cwd == localRoot {
		return "at_local_workspace_root"
	}
	if rel, err := filepath.Rel(localRoot, cwd); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "inside_local_workspace"
	}
	return "outside_local_workspace"
}

func formatRemoteCapabilities(caps *RemoteCapabilitySummary) string {
	if caps == nil {
		return ""
	}
	parts := make([]string, 0, 4)
	if caps.Git {
		parts = append(parts, "git")
	}
	if caps.Python3 {
		parts = append(parts, "python3")
	}
	if caps.Base64 {
		parts = append(parts, "base64")
	}
	if caps.Mktemp {
		parts = append(parts, "mktemp")
	}
	return strings.Join(parts, ",")
}

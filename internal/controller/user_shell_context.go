package controller

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"aiterm/internal/shell"
)

const (
	recentUserShellContextLines   = 120
	recentManualShellCommandLimit = 8
	recentManualShellActionLimit  = 6
)

func (c *LocalController) refreshUserShellContext(ctx context.Context, includeOutput bool) {
	trackedShell := c.syncTrackedShellTarget(ctx)
	c.refreshUserShellContextForTarget(ctx, trackedShell, includeOutput)
}

func (c *LocalController) captureObservedShellStateForTarget(ctx context.Context, trackedShell TrackedShellTarget) *shell.ObservedShellState {
	if c.reader == nil || trackedShell.PaneID == "" {
		return nil
	}
	observed, err := c.reader.CaptureObservedShellState(ctx, trackedShell.PaneID)
	if err != nil {
		return nil
	}
	copyObserved := observed
	return &copyObserved
}

func (c *LocalController) capturePromptContextForTarget(ctx context.Context, trackedShell TrackedShellTarget) *shell.PromptContext {
	observed := c.captureObservedShellStateForTarget(ctx, trackedShell)
	if observed == nil || !observed.HasPromptContext || observed.PromptContext.PromptLine() == "" {
		return nil
	}
	contextCopy := observed.PromptContext
	return &contextCopy
}

func (c *LocalController) refreshUserShellContextForTarget(ctx context.Context, trackedShell TrackedShellTarget, includeOutput bool) {
	observed := c.captureObservedShellStateForTarget(ctx, trackedShell)
	recentOutput := ""
	if c.reader != nil && trackedShell.PaneID != "" {
		if includeOutput {
			if capturedOutput, err := c.reader.CaptureRecentOutput(ctx, trackedShell.PaneID, recentUserShellContextLines); err == nil {
				recentOutput = strings.TrimSpace(capturedOutput)
			}
		}
	}

	commands, actions := loadRecentManualShellContext(c.session.UserShellHistoryFile)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.session.TrackedShell = trackedShell
	if trackedShell.SessionName != "" {
		c.session.SessionName = trackedShell.SessionName
	}
	if observed != nil {
		c.applyObservedShellStateLocked(observed)
	}
	if recentOutput != "" {
		c.session.RecentShellOutput = recentOutput
	}
	c.session.RecentManualCommands = commands
	c.session.RecentManualActions = actions
}

func (c *LocalController) applyPromptContextLocked(promptContext *shell.PromptContext) {
	if promptContext == nil {
		return
	}

	contextCopy := *promptContext
	c.session.CurrentShell = &contextCopy
	location := shell.InferShellLocation(contextCopy, "")
	c.session.CurrentShellLocation = &location
	if workingDirectory := normalizeShellWorkingDirectory(contextCopy.Directory, &location); workingDirectory != "" {
		c.session.WorkingDirectory = workingDirectory
	}
	c.refreshRemoteCapabilityHintLocked()
}

func (c *LocalController) applyShellLocationLocked(location shell.ShellLocation) {
	if location.Kind == "" {
		return
	}
	locationCopy := location
	c.session.CurrentShellLocation = &locationCopy
	if workingDirectory := normalizeShellWorkingDirectory(locationCopy.Directory, &locationCopy); workingDirectory != "" {
		c.session.WorkingDirectory = workingDirectory
	}
	c.refreshRemoteCapabilityHintLocked()
}

func (c *LocalController) applyObservedShellStateLocked(observed *shell.ObservedShellState) {
	if observed == nil {
		return
	}
	previousLocation := c.session.CurrentShellLocation
	locationCopy := observed.Location
	if locationCopy.Kind == "" {
		locationCopy = shell.InferShellLocation(observed.PromptContext, observed.CurrentPaneCommand)
	}
	if previousLocation != nil {
		locationCopy = carryForwardObservedDirectory(locationCopy, *previousLocation)
	}
	c.applyShellLocationLocked(locationCopy)
	if observed.HasPromptContext && observed.PromptContext.PromptLine() != "" {
		contextCopy := observed.PromptContext
		c.session.CurrentShell = &contextCopy
		workingDirectorySource := contextCopy.Directory
		if locationCopy.DirectorySource == shell.ShellDirectorySourceProbe && strings.TrimSpace(locationCopy.Directory) != "" {
			workingDirectorySource = locationCopy.Directory
		}
		if workingDirectory := normalizeShellWorkingDirectory(workingDirectorySource, &locationCopy); workingDirectory != "" {
			c.session.WorkingDirectory = workingDirectory
		}
	}
	c.refreshRemoteCapabilityHintLocked()
}

func shellContextWithResolvedDirectory(promptContext shell.PromptContext, location *shell.ShellLocation) shell.PromptContext {
	contextCopy := promptContext
	if location == nil {
		return contextCopy
	}

	resolvedDirectory := normalizeShellWorkingDirectory(location.Directory, location)
	if resolvedDirectory == "" {
		return contextCopy
	}

	promptDirectory := normalizeShellWorkingDirectory(contextCopy.Directory, location)
	if promptDirectory == "" || promptDirectory != resolvedDirectory {
		contextCopy.Directory = strings.TrimSpace(location.Directory)
	}
	return contextCopy
}

func isRemoteShellLocation(location *shell.ShellLocation, prompt *shell.PromptContext) bool {
	return effectiveShellLocation(location, prompt).Kind == shell.ShellLocationRemote
}

func effectiveShellLocation(location *shell.ShellLocation, prompt *shell.PromptContext) shell.ShellLocation {
	if location != nil {
		copyLocation := *location
		if prompt != nil && (copyLocation.Kind == "" || copyLocation.DirectorySource == "" || copyLocation.DirectoryConfidence == "") {
			inferred := shell.InferShellLocation(*prompt, "")
			if copyLocation.Kind == "" {
				copyLocation.Kind = inferred.Kind
			}
			if copyLocation.Directory == "" {
				copyLocation.Directory = inferred.Directory
			}
			if copyLocation.DirectorySource == "" {
				copyLocation.DirectorySource = inferred.DirectorySource
			}
			if copyLocation.DirectoryConfidence == "" {
				copyLocation.DirectoryConfidence = inferred.DirectoryConfidence
			}
			if copyLocation.User == "" {
				copyLocation.User = inferred.User
			}
			if copyLocation.Host == "" {
				copyLocation.Host = inferred.Host
			}
			if copyLocation.Confidence == "" {
				copyLocation.Confidence = inferred.Confidence
			}
		}
		return copyLocation
	}
	if prompt != nil {
		return shell.InferShellLocation(*prompt, "")
	}
	return shell.ShellLocation{}
}

func carryForwardObservedDirectory(current shell.ShellLocation, previous shell.ShellLocation) shell.ShellLocation {
	if current.Directory != "" || previous.Directory == "" {
		return current
	}
	if current.Kind == "" || previous.Kind == "" || current.Kind != previous.Kind {
		return current
	}
	if strings.TrimSpace(current.User) != "" && strings.TrimSpace(previous.User) != "" && !strings.EqualFold(strings.TrimSpace(current.User), strings.TrimSpace(previous.User)) {
		return current
	}
	if strings.TrimSpace(current.Host) != "" && strings.TrimSpace(previous.Host) != "" && !strings.EqualFold(strings.TrimSpace(current.Host), strings.TrimSpace(previous.Host)) {
		return current
	}
	return shell.CarryForwardShellLocationDirectory(current, previous)
}

func normalizeWorkingDirectory(directory string) string {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return ""
	}

	if directory == "~" || strings.HasPrefix(directory, "~/") {
		if homeDirectory, err := os.UserHomeDir(); err == nil && strings.TrimSpace(homeDirectory) != "" {
			if directory == "~" {
				return filepath.Clean(homeDirectory)
			}
			return filepath.Clean(filepath.Join(homeDirectory, strings.TrimPrefix(directory, "~/")))
		}
	}

	return filepath.Clean(directory)
}

func resolveLocalHomeDirectory(directory string) string {
	directory = normalizeWorkingDirectory(directory)
	if homeDirectory, err := os.UserHomeDir(); err == nil {
		if normalized := normalizeWorkingDirectory(homeDirectory); normalized != "" {
			return normalized
		}
	}
	return directory
}

func resolveLocalWorkingDirectory(directory string) string {
	directory = normalizeWorkingDirectory(directory)
	if workingDirectory, err := os.Getwd(); err == nil {
		if normalized := normalizeWorkingDirectory(workingDirectory); normalized != "" {
			return normalized
		}
	}
	return directory
}

func resolveLocalUsername(username string) string {
	username = strings.TrimSpace(username)
	if currentUser, err := user.Current(); err == nil {
		if resolved := strings.TrimSpace(currentUser.Username); resolved != "" {
			return resolved
		}
		if resolved := strings.TrimSpace(currentUser.Name); resolved != "" {
			return resolved
		}
	}
	return username
}

func resolveLocalHostname(hostname string) string {
	hostname = strings.TrimSpace(hostname)
	if resolved, err := os.Hostname(); err == nil {
		resolved = strings.TrimSpace(resolved)
		if resolved != "" {
			return resolved
		}
	}
	return hostname
}

func (c *LocalController) refreshLocalHostContext() localHostContext {
	c.mu.Lock()
	defer c.mu.Unlock()

	context := localHostContext{
		WorkingDirectory: resolveLocalWorkingDirectory(c.session.LocalWorkingDirectory),
		HomeDirectory:    resolveLocalHomeDirectory(c.session.LocalHomeDirectory),
		Username:         resolveLocalUsername(c.session.LocalUsername),
		Hostname:         resolveLocalHostname(c.session.LocalHostname),
	}
	c.session.LocalWorkingDirectory = context.WorkingDirectory
	c.session.LocalHomeDirectory = context.HomeDirectory
	c.session.LocalUsername = context.Username
	c.session.LocalHostname = context.Hostname
	if strings.TrimSpace(c.session.LocalWorkspaceRoot) == "" {
		c.session.LocalWorkspaceRoot = context.WorkingDirectory
	}
	return context
}

func normalizeShellWorkingDirectory(directory string, location *shell.ShellLocation) string {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return ""
	}
	if location != nil && location.Kind == shell.ShellLocationRemote {
		if directory == "~" || strings.HasPrefix(directory, "~/") {
			return directory
		}
	}
	return normalizeWorkingDirectory(directory)
}

func loadRecentManualShellContext(historyFile string) ([]string, []string) {
	historyFile = strings.TrimSpace(historyFile)
	if historyFile == "" {
		return nil, nil
	}

	data, err := os.ReadFile(historyFile)
	if err != nil || len(data) == 0 {
		return nil, nil
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	commands := make([]string, 0, len(lines))
	for _, line := range lines {
		command := normalizeHistoryCommand(line)
		if command == "" {
			continue
		}
		if len(commands) > 0 && commands[len(commands)-1] == command {
			continue
		}
		commands = append(commands, command)
	}
	if len(commands) == 0 {
		return nil, nil
	}

	recentCommands := tailStrings(commands, recentManualShellCommandLimit)
	actions := summarizeManualShellActions(commands, recentManualShellActionLimit)
	return recentCommands, actions
}

func normalizeHistoryCommand(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	if strings.HasPrefix(line, ": ") {
		if separator := strings.Index(line, ";"); separator >= 0 && separator+1 < len(line) {
			line = line[separator+1:]
		}
	}
	return strings.TrimSpace(line)
}

func summarizeManualShellActions(commands []string, limit int) []string {
	if limit <= 0 || len(commands) == 0 {
		return nil
	}

	actions := make([]string, 0, limit)
	for index := len(commands) - 1; index >= 0 && len(actions) < limit; index-- {
		action := summarizeManualShellAction(commands[index])
		if action == "" {
			continue
		}
		if len(actions) > 0 && actions[len(actions)-1] == action {
			continue
		}
		actions = append(actions, action)
	}
	for left, right := 0, len(actions)-1; left < right; left, right = left+1, right-1 {
		actions[left], actions[right] = actions[right], actions[left]
	}
	return actions
}

func summarizeManualShellAction(command string) string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return ""
	}

	switch fields[0] {
	case "git":
		if len(fields) >= 4 && fields[1] == "mv" {
			return "renamed " + fields[2] + " -> " + fields[3]
		}
	case "mv":
		if len(fields) >= 3 {
			return "renamed " + fields[len(fields)-2] + " -> " + fields[len(fields)-1]
		}
	case "cp":
		if len(fields) >= 3 {
			return "copied " + fields[len(fields)-2] + " -> " + fields[len(fields)-1]
		}
	case "rm":
		if len(fields) >= 2 {
			return "removed " + strings.Join(fields[1:], " ")
		}
	case "mkdir":
		if len(fields) >= 2 {
			return "created directory " + strings.Join(fields[1:], " ")
		}
	case "touch":
		if len(fields) >= 2 {
			return "touched " + strings.Join(fields[1:], " ")
		}
	}

	return ""
}

func tailStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) <= limit {
		return append([]string(nil), values...)
	}
	return append([]string(nil), values[len(values)-limit:]...)
}

package controller

import (
	"context"
	"os"
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

func (c *LocalController) refreshUserShellContextForTarget(ctx context.Context, trackedShell TrackedShellTarget, includeOutput bool) {
	var promptContext *shell.PromptContext
	recentOutput := ""
	if c.reader != nil && trackedShell.PaneID != "" {
		if capturedContext, err := c.reader.CaptureShellContext(ctx, trackedShell.PaneID); err == nil && capturedContext.PromptLine() != "" {
			contextCopy := capturedContext
			promptContext = &contextCopy
		}
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
	if promptContext != nil {
		c.applyPromptContextLocked(promptContext)
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
	if workingDirectory := normalizeWorkingDirectory(contextCopy.Directory); workingDirectory != "" {
		c.session.WorkingDirectory = workingDirectory
	}
	c.refreshRemoteCapabilityHintLocked()
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

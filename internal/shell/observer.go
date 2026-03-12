package shell

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"aiterm/internal/protocol"
	"aiterm/internal/tmux"
)

type TrackedExecution struct {
	CommandID    string
	Command      string
	ExitCode     int
	Captured     string
	ShellContext PromptContext
}

type Observer struct {
	client *tmux.Client
}

var wrappedSentinelSuffixPattern = regexp.MustCompile(`^[a-z0-9]{1,16}:\$\?$`)

const shellContextProbeCommand = `printf '__SHUTTLE_CTX_EXIT__=%s\n' "$?"
printf '__SHUTTLE_CTX_USER__=%s\n' "$(whoami 2>/dev/null || id -un 2>/dev/null)"
printf '__SHUTTLE_CTX_HOST__=%s\n' "$(hostname 2>/dev/null || uname -n 2>/dev/null)"
printf '__SHUTTLE_CTX_UNAME__=%s\n' "$(uname -sr 2>/dev/null)"
printf '__SHUTTLE_CTX_PWD__=%s\n' "$PWD"`

const (
	DefaultCommandTimeout           = 10 * time.Second
	ContextTransitionCommandTimeout = 60 * time.Second
)

func NewObserver(client *tmux.Client) *Observer {
	return &Observer{client: client}
}

func (o *Observer) CaptureRecentOutput(ctx context.Context, paneID string, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}

	captured, err := o.client.CapturePane(ctx, paneID, -lines)
	if err != nil {
		return "", fmt.Errorf("capture recent output: %w", err)
	}

	return sanitizeCapturedBody(captured), nil
}

func (o *Observer) CaptureShellContext(ctx context.Context, paneID string) (PromptContext, error) {
	captured, err := o.client.CapturePane(ctx, paneID, -80)
	if err != nil {
		return PromptContext{}, fmt.Errorf("capture shell context: %w", err)
	}

	context, ok := ParsePromptContextFromCapture(captured)
	if !ok {
		return PromptContext{}, nil
	}

	return context, nil
}

func (o *Observer) RunTrackedCommand(ctx context.Context, paneID string, command string, timeout time.Duration) (TrackedExecution, error) {
	if isContextTransitionCommand(command) {
		return o.runContextTransitionCommand(ctx, paneID, command, timeout)
	}

	markers := protocol.NewMarkers()
	wrapped := protocol.WrapCommand(command, markers)

	if err := o.client.SendKeys(ctx, paneID, wrapped, true); err != nil {
		return TrackedExecution{}, fmt.Errorf("send tracked command: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		captured, err := o.client.CapturePane(ctx, paneID, -200)
		if err != nil {
			return TrackedExecution{}, fmt.Errorf("capture pane: %w", err)
		}

		result, complete, err := protocol.ParseCommandResult(captured, markers)
		if err != nil {
			return TrackedExecution{}, fmt.Errorf("parse tracked command result: %w", err)
		}

		if complete {
			cleanBody := sanitizeCapturedBody(result.Body)
			cleanBody = stripEchoedCommand(cleanBody, command)
			shellContext, _ := ParsePromptContextFromCapture(captured)
			return TrackedExecution{
				CommandID:    result.CommandID,
				Command:      command,
				ExitCode:     result.ExitCode,
				Captured:     cleanBody,
				ShellContext: shellContext,
			}, nil
		}

		if time.Now().After(deadline) {
			return TrackedExecution{}, fmt.Errorf("timed out waiting for tracked command %s", markers.CommandID)
		}

		select {
		case <-ctx.Done():
			return TrackedExecution{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func CommandTimeout(command string) time.Duration {
	if isContextTransitionCommand(command) {
		return ContextTransitionCommandTimeout
	}

	return DefaultCommandTimeout
}

func IsInteractiveCommand(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return false
	}
	if isContextTransitionCommand(command) {
		return true
	}

	index := 0
	for index < len(fields) && strings.Contains(fields[index], "=") && !strings.HasPrefix(fields[index], "-") {
		index++
	}
	if index >= len(fields) {
		return false
	}

	commandName := fields[index]
	switch commandName {
	case "sudo", "doas", "passwd", "su":
		return true
	case "vi", "vim", "nvim", "nano", "emacs", "less", "more", "man", "top", "htop", "btop", "watch", "tmux", "screen":
		return true
	default:
		return false
	}
}

func (o *Observer) runContextTransitionCommand(ctx context.Context, paneID string, command string, timeout time.Duration) (TrackedExecution, error) {
	effectiveTimeout := timeout
	if effectiveTimeout < 45*time.Second {
		effectiveTimeout = 45 * time.Second
	}

	beforeCapture, err := o.client.CapturePane(ctx, paneID, -200)
	if err != nil {
		return TrackedExecution{}, fmt.Errorf("capture pane before context transition: %w", err)
	}

	baselineContext, _ := ParsePromptContextFromCapture(beforeCapture)
	if err := o.client.SendKeys(ctx, paneID, command, true); err != nil {
		return TrackedExecution{}, fmt.Errorf("send context transition command: %w", err)
	}

	deadline := time.Now().Add(effectiveTimeout)
	promptCapture := beforeCapture
	promptContext := baselineContext
	for {
		captured, err := o.client.CapturePane(ctx, paneID, -200)
		if err != nil {
			return TrackedExecution{}, fmt.Errorf("capture pane after context transition: %w", err)
		}

		candidate, ok := ParsePromptContextFromCapture(captured)
		if ok && promptReturnedAfterTransition(beforeCapture, baselineContext, candidate, captured) {
			promptCapture = captured
			promptContext = candidate
			break
		}

		if time.Now().After(deadline) {
			return TrackedExecution{}, fmt.Errorf("timed out waiting for context transition command to settle")
		}

		select {
		case <-ctx.Done():
			return TrackedExecution{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	probeResult, err := o.runProbeCommand(ctx, paneID, 10*time.Second)
	delta := sanitizeCapturedBody(capturePaneDelta(beforeCapture, promptCapture))
	delta = stripTrailingPromptLine(delta, promptContext)
	delta = strings.TrimSpace(delta)
	if delta == "" {
		line := strings.TrimSpace(promptContext.PromptLine())
		if line != "" {
			delta = "shell context updated: " + line
		} else {
			delta = "shell context updated"
		}
	}

	exitCode := 0
	commandID := ""
	if err == nil {
		commandID = probeResult.CommandID
		probeOutput, parsedContext, parsedExitCode := parseShellContextProbeOutput(probeResult.Captured, promptContext)
		if parsedContext.PromptLine() != "" {
			promptContext = parsedContext
		}
		exitCode = parsedExitCode
		if probeOutput != "" {
			delta = strings.TrimSpace(delta + "\n" + probeOutput)
		}
	}

	return TrackedExecution{
		CommandID:    commandID,
		Command:      command,
		ExitCode:     exitCode,
		Captured:     delta,
		ShellContext: promptContext,
	}, nil
}

func (o *Observer) runProbeCommand(ctx context.Context, paneID string, timeout time.Duration) (TrackedExecution, error) {
	markers := protocol.NewMarkers()
	wrapped := protocol.WrapCommand(shellContextProbeCommand, markers)

	if err := o.client.SendKeys(ctx, paneID, wrapped, true); err != nil {
		return TrackedExecution{}, fmt.Errorf("send shell context probe: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		captured, err := o.client.CapturePane(ctx, paneID, -200)
		if err != nil {
			return TrackedExecution{}, fmt.Errorf("capture pane for shell context probe: %w", err)
		}

		result, complete, err := protocol.ParseCommandResult(captured, markers)
		if err != nil {
			return TrackedExecution{}, fmt.Errorf("parse shell context probe result: %w", err)
		}

		if complete {
			cleanBody := sanitizeCapturedBody(result.Body)
			cleanBody = stripEchoedCommand(cleanBody, shellContextProbeCommand)
			shellContext, _ := ParsePromptContextFromCapture(captured)
			return TrackedExecution{
				CommandID:    result.CommandID,
				Command:      shellContextProbeCommand,
				ExitCode:     result.ExitCode,
				Captured:     cleanBody,
				ShellContext: shellContext,
			}, nil
		}

		if time.Now().After(deadline) {
			return TrackedExecution{}, fmt.Errorf("timed out waiting for shell context probe")
		}

		select {
		case <-ctx.Done():
			return TrackedExecution{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func sanitizeCapturedBody(body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}

	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	filtered := make([]string, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.Contains(trimmed, "__SHUTTLE_E__:") {
			continue
		}

		if strings.Contains(trimmed, "__SHUTTLE_B__:") {
			continue
		}

		if wrappedSentinelSuffixPattern.MatchString(trimmed) {
			continue
		}

		filtered = append(filtered, line)
	}

	return strings.TrimSpace(strings.Join(filtered, "\n"))
}

func stripEchoedCommand(body string, command string) string {
	if strings.TrimSpace(body) == "" || strings.TrimSpace(command) == "" {
		return strings.TrimSpace(body)
	}

	bodyLines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	commandLines := strings.Split(strings.ReplaceAll(command, "\r\n", "\n"), "\n")

	for len(commandLines) > 0 && commandLines[len(commandLines)-1] == "" {
		commandLines = commandLines[:len(commandLines)-1]
	}
	if len(commandLines) == 0 || len(bodyLines) < len(commandLines) {
		return strings.TrimSpace(body)
	}

	for index, commandLine := range commandLines {
		if !strings.HasSuffix(strings.TrimRight(bodyLines[index], " \t"), commandLine) {
			return strings.TrimSpace(body)
		}
	}

	return strings.TrimSpace(strings.Join(bodyLines[len(commandLines):], "\n"))
}

func isContextTransitionCommand(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return false
	}

	index := 0
	for index < len(fields) && strings.Contains(fields[index], "=") && !strings.HasPrefix(fields[index], "-") {
		index++
	}
	if index >= len(fields) {
		return false
	}

	commandName := fields[index]
	args := fields[index+1:]
	switch commandName {
	case "ssh", "slogin", "telnet", "mosh", "su", "exit", "logout":
		return true
	case "sudo":
		return hasAnyArg(args, "-i", "-s", "su")
	case "docker", "podman":
		return len(args) > 0 && args[0] == "exec" && hasAnyArg(args[1:], "-it", "-ti", "-i", "-t")
	case "kubectl":
		return len(args) > 0 && args[0] == "exec" && hasAnyArg(args[1:], "-it", "-ti", "-i", "-t")
	case "machinectl":
		return len(args) > 0 && args[0] == "shell"
	case "nsenter":
		return true
	default:
		return false
	}
}

func hasAnyArg(args []string, candidates ...string) bool {
	for _, arg := range args {
		for _, candidate := range candidates {
			if arg == candidate {
				return true
			}
		}
	}
	return false
}

func promptReturnedAfterTransition(beforeCapture string, baseline PromptContext, candidate PromptContext, captured string) bool {
	if candidate.PromptLine() == "" {
		return false
	}

	if baseline.RawLine != "" && candidate.RawLine != baseline.RawLine {
		return true
	}

	return strings.TrimSpace(captured) != strings.TrimSpace(beforeCapture)
}

func capturePaneDelta(before string, after string) string {
	beforeLines := strings.Split(strings.ReplaceAll(before, "\r\n", "\n"), "\n")
	afterLines := strings.Split(strings.ReplaceAll(after, "\r\n", "\n"), "\n")

	index := 0
	for index < len(beforeLines) && index < len(afterLines) && beforeLines[index] == afterLines[index] {
		index++
	}

	return strings.Join(afterLines[index:], "\n")
}

func stripTrailingPromptLine(body string, promptContext PromptContext) string {
	body = strings.TrimSpace(body)
	promptLine := strings.TrimSpace(promptContext.RawLine)
	if body == "" || promptLine == "" {
		return body
	}

	lines := strings.Split(body, "\n")
	if strings.TrimSpace(lines[len(lines)-1]) == promptLine {
		lines = lines[:len(lines)-1]
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func parseShellContextProbeOutput(body string, baseline PromptContext) (string, PromptContext, int) {
	context := baseline
	exitCode := 0

	for _, rawLine := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		switch {
		case strings.HasPrefix(line, "__SHUTTLE_CTX_EXIT__="):
			parsed, err := strconv.Atoi(strings.TrimPrefix(line, "__SHUTTLE_CTX_EXIT__="))
			if err == nil {
				exitCode = parsed
			}
		case strings.HasPrefix(line, "__SHUTTLE_CTX_USER__="):
			context.User = strings.TrimSpace(strings.TrimPrefix(line, "__SHUTTLE_CTX_USER__="))
			context.Root = context.User == "root" || context.PromptSymbol == "#"
		case strings.HasPrefix(line, "__SHUTTLE_CTX_HOST__="):
			context.Host = strings.TrimSpace(strings.TrimPrefix(line, "__SHUTTLE_CTX_HOST__="))
		case strings.HasPrefix(line, "__SHUTTLE_CTX_UNAME__="):
			context.System = strings.TrimSpace(strings.TrimPrefix(line, "__SHUTTLE_CTX_UNAME__="))
		case strings.HasPrefix(line, "__SHUTTLE_CTX_PWD__="):
			context.Directory = strings.TrimSpace(strings.TrimPrefix(line, "__SHUTTLE_CTX_PWD__="))
		}
	}

	localHost, _ := os.Hostname()
	context.Remote = isRemoteHost(context.Host, localHost)
	if context.Root && context.PromptSymbol == "" {
		context.PromptSymbol = "#"
	}
	if context.PromptSymbol == "" {
		context.PromptSymbol = "$"
	}

	return "", context, exitCode
}

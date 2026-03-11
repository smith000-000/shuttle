package shell

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"aiterm/internal/protocol"
	"aiterm/internal/tmux"
)

type TrackedExecution struct {
	CommandID string
	Command   string
	ExitCode  int
	Captured  string
}

type Observer struct {
	client *tmux.Client
}

var wrappedSentinelSuffixPattern = regexp.MustCompile(`^[a-z0-9]{1,16}:\$\?$`)

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

func (o *Observer) RunTrackedCommand(ctx context.Context, paneID string, command string, timeout time.Duration) (TrackedExecution, error) {
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
			return TrackedExecution{
				CommandID: result.CommandID,
				Command:   command,
				ExitCode:  result.ExitCode,
				Captured:  sanitizeCapturedBody(result.Body),
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

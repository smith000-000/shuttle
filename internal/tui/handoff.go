package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"aiterm/internal/logging"
	"aiterm/internal/tmux"

	tea "github.com/charmbracelet/bubbletea"
)

const TakeControlKey = "F2"

type takeControlConfig struct {
	SocketName    string
	SessionName   string
	TrackedPaneID string
	TemporaryPane bool
	StartDir      string
	DetachKey     string
}

func (c takeControlConfig) enabled() bool {
	return c.SessionName != "" && c.TrackedPaneID != "" && c.DetachKey != ""
}

type takeControlFinishedMsg struct {
	err error
}

type tmuxTakeControlCommand struct {
	config takeControlConfig
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func newTakeControlCmd(config takeControlConfig) tea.Cmd {
	if !config.enabled() {
		return nil
	}

	return tea.Exec(&tmuxTakeControlCommand{config: config}, func(err error) tea.Msg {
		return takeControlFinishedMsg{err: err}
	})
}

func (c *tmuxTakeControlCommand) SetStdin(r io.Reader) {
	c.stdin = r
}

func (c *tmuxTakeControlCommand) SetStdout(w io.Writer) {
	c.stdout = w
}

func (c *tmuxTakeControlCommand) SetStderr(w io.Writer) {
	c.stderr = w
}

func (c *tmuxTakeControlCommand) Run() error {
	targetPaneID, err := c.resolveTrackedPaneID(c.config.TrackedPaneID)
	if err != nil {
		return err
	}
	logging.Trace(
		"tui.take_control.target_resolved",
		"session", c.config.SessionName,
		"requested_pane", c.config.TrackedPaneID,
		"resolved_pane", targetPaneID,
	)

	zoomedHere, err := c.selectTakeControlTarget(targetPaneID)
	if err != nil {
		return err
	}
	cleanupAutoDetach, err := c.installTemporaryPaneAutoDetach(targetPaneID)
	if err != nil {
		logging.TraceError("tui.take_control.auto_detach_error", err, "pane", targetPaneID, "session", c.config.SessionName)
	}
	if cleanupAutoDetach != nil {
		defer cleanupAutoDetach()
	}
	if zoomedHere {
		defer func() {
			_ = c.runTmux("resize-pane", "-Z", "-t", targetPaneID)
		}()
	}

	command := exec.Command("tmux", c.tmuxArgs("attach-session", "-t", c.config.SessionName)...)
	if c.stdin != nil {
		command.Stdin = c.stdin
	} else {
		command.Stdin = os.Stdin
	}
	if c.stdout != nil {
		command.Stdout = c.stdout
	} else {
		command.Stdout = os.Stdout
	}
	if c.stderr != nil {
		command.Stderr = c.stderr
	} else {
		command.Stderr = os.Stderr
	}

	return command.Run()
}

func (c *tmuxTakeControlCommand) installTemporaryPaneAutoDetach(targetPaneID string) (func(), error) {
	if !c.config.TemporaryPane {
		return nil, nil
	}

	clientTTY, err := c.currentTTY()
	if err != nil {
		return nil, fmt.Errorf("resolve client tty: %w", err)
	}
	return c.installTemporaryPaneWindowHook(targetPaneID, fmt.Sprintf("detach-client -t %q", clientTTY))
}

func (c *tmuxTakeControlCommand) installTemporaryPaneWindowHook(targetPaneID string, hookCommand string) (func(), error) {
	if !c.config.TemporaryPane {
		return nil, nil
	}
	if strings.TrimSpace(targetPaneID) == "" {
		return nil, fmt.Errorf("target pane is required")
	}
	if strings.TrimSpace(hookCommand) == "" {
		return nil, fmt.Errorf("hook command is required")
	}

	hookName := fmt.Sprintf("window-unlinked[%d]", time.Now().UnixNano())
	if err := c.runTmux("set-hook", "-w", "-t", targetPaneID, "--", hookName, hookCommand); err != nil {
		return nil, fmt.Errorf("install temporary window hook: %w", err)
	}
	return func() {
		_ = c.runTmux("set-hook", "-w", "-u", "-t", targetPaneID, "--", hookName)
	}, nil
}

func (c *tmuxTakeControlCommand) currentTTY() (string, error) {
	command := exec.Command("tty")
	if c.stdin != nil {
		command.Stdin = c.stdin
	} else {
		command.Stdin = os.Stdin
	}
	output, err := command.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		if trimmed == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, trimmed)
	}
	if trimmed == "" {
		return "", fmt.Errorf("tty returned empty output")
	}
	return trimmed, nil
}

func (c *tmuxTakeControlCommand) selectTakeControlTarget(targetPaneID string) (bool, error) {
	windowID, err := c.captureTmux("display-message", "-p", "-t", targetPaneID, "#{window_id}")
	if err != nil {
		return false, fmt.Errorf("resolve target window: %w", err)
	}
	windowID = strings.TrimSpace(windowID)
	if windowID != "" {
		if err := c.runTmux("select-window", "-t", windowID); err != nil {
			return false, fmt.Errorf("select shell window: %w", err)
		}
	}
	if err := c.runTmux("select-pane", "-t", targetPaneID); err != nil {
		return false, fmt.Errorf("select shell pane: %w", err)
	}

	zoomed, err := c.captureTmux("display-message", "-p", "-t", targetPaneID, "#{window_zoomed_flag}")
	if err != nil {
		return false, fmt.Errorf("inspect tmux zoom state: %w", err)
	}
	if strings.TrimSpace(zoomed) == "1" {
		return false, nil
	}
	if err := c.runTmux("resize-pane", "-Z", "-t", targetPaneID); err != nil {
		return false, fmt.Errorf("zoom shell pane: %w", err)
	}
	return true, nil
}

func (c *tmuxTakeControlCommand) resolveTrackedPaneID(paneID string) (string, error) {
	if err := c.runTmux("select-pane", "-t", paneID); err == nil {
		return paneID, nil
	} else if shouldRecoverTakeControlSession(err) {
		logging.Trace(
			"tui.take_control.recovery_begin",
			"session", c.config.SessionName,
			"requested_pane", paneID,
			"reason", err.Error(),
		)
		return c.recoverTrackedPaneID()
	} else if c.config.TemporaryPane {
		return "", fmt.Errorf("temporary execution pane %s is no longer available", paneID)
	} else if !strings.Contains(strings.ToLower(err.Error()), "can't find pane") {
		return "", fmt.Errorf("select shell pane: %w", err)
	}

	output, err := c.captureTmux("list-panes", "-t", c.config.SessionName, "-F", "#{pane_id}\t#{pane_top}\t#{pane_left}")
	if err != nil {
		if shouldRecoverTakeControlSession(err) {
			return c.recoverTrackedPaneID()
		}
		return "", fmt.Errorf("resolve replacement shell pane: %w", err)
	}

	bestID := pickPreferredPaneID(output)
	if bestID == "" {
		return "", fmt.Errorf("select shell pane: can't find pane: %s", paneID)
	}
	logging.Trace(
		"tui.take_control.replacement_pane",
		"session", c.config.SessionName,
		"requested_pane", paneID,
		"replacement_pane", bestID,
	)
	return bestID, nil
}

func (c *tmuxTakeControlCommand) recoverTrackedPaneID() (string, error) {
	if err := c.ensureWorkspace(); err != nil {
		return "", fmt.Errorf("recover shell workspace: %w", err)
	}

	output, err := c.captureTmux("list-panes", "-t", c.config.SessionName, "-F", "#{pane_id}\t#{pane_top}\t#{pane_left}")
	if err != nil {
		return "", fmt.Errorf("resolve recovered shell pane: %w", err)
	}

	bestID := pickPreferredPaneID(output)
	if bestID == "" {
		return "", fmt.Errorf("select shell pane: can't find pane in session %s", c.config.SessionName)
	}
	logging.Trace(
		"tui.take_control.recovery_complete",
		"session", c.config.SessionName,
		"recovered_pane", bestID,
	)
	return bestID, nil
}

func (c *tmuxTakeControlCommand) ensureWorkspace() error {
	if strings.TrimSpace(c.config.SessionName) == "" {
		return fmt.Errorf("session name is required")
	}
	if strings.TrimSpace(c.config.StartDir) == "" {
		return fmt.Errorf("start directory is required")
	}

	client, err := tmux.NewClient(c.config.SocketName)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logging.Trace(
		"tui.take_control.ensure_session_begin",
		"session", c.config.SessionName,
		"start_dir", c.config.StartDir,
	)
	_, _, err = tmux.BootstrapShellSession(ctx, client, tmux.ShellSessionOptions{
		SessionName: c.config.SessionName,
		StartDir:    c.config.StartDir,
	})
	if err != nil {
		logging.TraceError("tui.take_control.ensure_session_error", err, "session", c.config.SessionName)
		return err
	}
	if strings.TrimSpace(c.config.DetachKey) != "" {
		if err := client.BindNoPrefixKey(ctx, c.config.DetachKey, "detach-client"); err != nil {
			logging.TraceError("tui.take_control.bind_detach_error", err, "session", c.config.SessionName, "detach_key", c.config.DetachKey)
			return err
		}
	}
	logging.Trace(
		"tui.take_control.ensure_session_complete",
		"session", c.config.SessionName,
		"detach_key", c.config.DetachKey,
	)
	return nil
}

func pickPreferredPaneID(output string) string {
	bestID := ""
	bestTop := 0
	bestLeft := 0
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) != 3 || strings.TrimSpace(fields[0]) == "" {
			continue
		}
		top, topErr := strconv.Atoi(strings.TrimSpace(fields[1]))
		left, leftErr := strconv.Atoi(strings.TrimSpace(fields[2]))
		if topErr != nil || leftErr != nil {
			continue
		}
		if bestID == "" || top < bestTop || (top == bestTop && left < bestLeft) {
			bestID = strings.TrimSpace(fields[0])
			bestTop = top
			bestLeft = left
		}
	}
	return bestID
}

func shouldRecoverTakeControlSession(err error) bool {
	if err == nil {
		return false
	}

	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no server running") || strings.Contains(text, "can't find session")
}

func (c *tmuxTakeControlCommand) tmuxArgs(args ...string) []string {
	commandArgs := make([]string, 0, len(args)+2)
	commandArgs = append(commandArgs, tmux.SocketFlagArgs(c.config.SocketName)...)
	commandArgs = append(commandArgs, args...)
	return commandArgs
}

func (c *tmuxTakeControlCommand) runTmux(args ...string) error {
	command := exec.Command("tmux", c.tmuxArgs(args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, trimmed)
	}
	return nil
}

func (c *tmuxTakeControlCommand) captureTmux(args ...string) (string, error) {
	command := exec.Command("tmux", c.tmuxArgs(args...)...)
	output, err := command.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		if trimmed == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, trimmed)
	}
	return trimmed, nil
}

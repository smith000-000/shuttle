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

	"aiterm/internal/tmux"

	tea "github.com/charmbracelet/bubbletea"
)

const TakeControlKey = "F2"

type takeControlConfig struct {
	SocketName  string
	SessionName string
	TopPaneID   string
	StartDir    string
	DetachKey   string
}

func (c takeControlConfig) enabled() bool {
	return c.SessionName != "" && c.TopPaneID != "" && c.DetachKey != ""
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
	targetPaneID, err := c.resolveTopPaneID(c.config.TopPaneID)
	if err != nil {
		return err
	}

	if err := c.runTmux("select-pane", "-t", targetPaneID); err != nil {
		return fmt.Errorf("select shell pane: %w", err)
	}

	zoomed, err := c.captureTmux("display-message", "-p", "-t", targetPaneID, "#{window_zoomed_flag}")
	if err != nil {
		return fmt.Errorf("inspect tmux zoom state: %w", err)
	}
	zoomedHere := false
	if strings.TrimSpace(zoomed) != "1" {
		if err := c.runTmux("resize-pane", "-Z", "-t", targetPaneID); err != nil {
			return fmt.Errorf("zoom shell pane: %w", err)
		}
		zoomedHere = true
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

func (c *tmuxTakeControlCommand) resolveTopPaneID(paneID string) (string, error) {
	if err := c.runTmux("select-pane", "-t", paneID); err == nil {
		return paneID, nil
	} else if shouldRecoverTakeControlSession(err) {
		return c.recoverTopPaneID()
	} else if !strings.Contains(strings.ToLower(err.Error()), "can't find pane") {
		return "", fmt.Errorf("select shell pane: %w", err)
	}

	output, err := c.captureTmux("list-panes", "-t", c.config.SessionName, "-F", "#{pane_id}\t#{pane_top}\t#{pane_left}")
	if err != nil {
		if shouldRecoverTakeControlSession(err) {
			return c.recoverTopPaneID()
		}
		return "", fmt.Errorf("resolve replacement shell pane: %w", err)
	}

	bestID := pickTopPaneID(output)
	if bestID == "" {
		return "", fmt.Errorf("select shell pane: can't find pane: %s", paneID)
	}
	return bestID, nil
}

func (c *tmuxTakeControlCommand) recoverTopPaneID() (string, error) {
	if err := c.ensureWorkspace(); err != nil {
		return "", fmt.Errorf("recover shell workspace: %w", err)
	}

	output, err := c.captureTmux("list-panes", "-t", c.config.SessionName, "-F", "#{pane_id}\t#{pane_top}\t#{pane_left}")
	if err != nil {
		return "", fmt.Errorf("resolve recovered shell pane: %w", err)
	}

	bestID := pickTopPaneID(output)
	if bestID == "" {
		return "", fmt.Errorf("select shell pane: can't find pane in session %s", c.config.SessionName)
	}
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

	_, _, err = tmux.BootstrapShellSession(ctx, client, tmux.ShellSessionOptions{
		SessionName: c.config.SessionName,
		StartDir:    c.config.StartDir,
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(c.config.DetachKey) != "" {
		if err := client.BindNoPrefixKey(ctx, c.config.DetachKey, "detach-client"); err != nil {
			return err
		}
	}
	return nil
}

func pickTopPaneID(output string) string {
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

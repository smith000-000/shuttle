package tui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const TakeControlKey = "F2"

type takeControlConfig struct {
	SocketName  string
	SessionName string
	TopPaneID   string
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
	if err := c.runTmux("select-pane", "-t", c.config.TopPaneID); err != nil {
		return fmt.Errorf("select shell pane: %w", err)
	}

	zoomed, err := c.captureTmux("display-message", "-p", "-t", c.config.TopPaneID, "#{window_zoomed_flag}")
	if err != nil {
		return fmt.Errorf("inspect tmux zoom state: %w", err)
	}
	zoomedHere := false
	if strings.TrimSpace(zoomed) != "1" {
		if err := c.runTmux("resize-pane", "-Z", "-t", c.config.TopPaneID); err != nil {
			return fmt.Errorf("zoom shell pane: %w", err)
		}
		zoomedHere = true
	}
	if zoomedHere {
		defer func() {
			_ = c.runTmux("resize-pane", "-Z", "-t", c.config.TopPaneID)
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

func (c *tmuxTakeControlCommand) tmuxArgs(args ...string) []string {
	if c.config.SocketName == "" {
		return args
	}

	commandArgs := make([]string, 0, len(args)+2)
	commandArgs = append(commandArgs, "-L", c.config.SocketName)
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

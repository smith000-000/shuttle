package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"aiterm/internal/logging"
)

var ErrNotInstalled = errors.New("tmux not found in PATH")

const paneFormat = "#{pane_id}\t#{pane_title}\t#{pane_active}\t#{pane_current_command}\t#{pane_pid}\t#{session_name}\t#{window_id}\t#{pane_top}\t#{pane_left}\t#{pane_height}\t#{pane_width}\t#{alternate_on}\t#{pane_tty}"

type Pane struct {
	ID             string
	Title          string
	Active         bool
	CurrentCommand string
	PID            int
	SessionName    string
	WindowID       string
	Top            int
	Left           int
	Height         int
	Width          int
	AlternateOn    bool
	TTY            string
}

type Client struct {
	binary     string
	socketName string
}

func ResolveSocketTarget(configured string) string {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return configured
	}

	tmuxEnv := strings.TrimSpace(os.Getenv("TMUX"))
	if tmuxEnv == "" {
		return ""
	}
	if comma := strings.Index(tmuxEnv, ","); comma >= 0 {
		tmuxEnv = tmuxEnv[:comma]
	}
	return strings.TrimSpace(tmuxEnv)
}

func SocketFlagArgs(socketTarget string) []string {
	socketTarget = strings.TrimSpace(socketTarget)
	if socketTarget == "" {
		return nil
	}
	if filepath.IsAbs(socketTarget) {
		return []string{"-S", socketTarget}
	}
	return []string{"-L", socketTarget}
}

func NewClient(socketName string) (*Client, error) {
	binary, err := exec.LookPath("tmux")
	if err != nil {
		return nil, ErrNotInstalled
	}

	return &Client{
		binary:     binary,
		socketName: socketName,
	}, nil
}

func (c *Client) HasSession(ctx context.Context, sessionName string) (bool, error) {
	_, err := c.run(ctx, "has-session", "-t", sessionName)
	if err == nil {
		return true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}

	return false, err
}

func (c *Client) NewDetachedSession(ctx context.Context, sessionName string, startDir string, env map[string]string) error {
	args := []string{"new-session", "-d", "-s", sessionName, "-c", startDir}
	args = append(args, environmentArgs(env)...)
	_, err := c.run(ctx, args...)
	return err
}

func (c *Client) SplitBottom(ctx context.Context, target string, percent int, startDir string) error {
	size := strconv.Itoa(percent) + "%"
	_, err := c.run(ctx, "split-window", "-v", "-l", size, "-t", target, "-c", startDir)
	return err
}

func (c *Client) ListPanes(ctx context.Context, target string) ([]Pane, error) {
	output, err := c.run(ctx, "list-panes", "-t", target, "-F", paneFormat)
	if err != nil {
		return nil, err
	}

	return parsePanesOutput(output)
}

func (c *Client) PaneInfo(ctx context.Context, target string) (Pane, error) {
	panes, err := c.ListPanes(ctx, target)
	if err != nil {
		return Pane{}, err
	}
	if len(panes) == 0 {
		return Pane{}, fmt.Errorf("no tmux pane found for target %q", target)
	}
	return panes[0], nil
}

func (c *Client) SendKeys(ctx context.Context, target string, text string, enter bool) error {
	args := []string{"send-keys", "-t", target, text}
	if enter {
		args = append(args, "C-m")
	}

	_, err := c.run(ctx, args...)
	return err
}

func (c *Client) SendLiteralKeys(ctx context.Context, target string, text string) error {
	args := []string{"send-keys", "-l", "-t", target, text}
	_, err := c.run(ctx, args...)
	return err
}

func (c *Client) InterruptForegroundProcess(ctx context.Context, target string) error {
	pane, err := c.PaneInfo(ctx, target)
	if err != nil {
		return err
	}
	pgid, err := foregroundProcessGroup(ctx, pane.PID)
	if err != nil {
		return err
	}
	if pgid <= 0 {
		return fmt.Errorf("invalid foreground process group %d", pgid)
	}
	return signalProcessGroup(ctx, pgid)
}

func (c *Client) CapturePane(ctx context.Context, target string, startLine int) (string, error) {
	return c.capturePane(ctx, target, startLine, false)
}

func (c *Client) CapturePaneEscaped(ctx context.Context, target string, startLine int) (string, error) {
	return c.capturePane(ctx, target, startLine, true)
}

func (c *Client) PipePaneOutput(ctx context.Context, target string, shellCommand string) error {
	if strings.TrimSpace(shellCommand) == "" {
		return fmt.Errorf("pipe-pane shell command cannot be empty")
	}
	_, err := c.run(ctx, "pipe-pane", "-O", "-t", target, shellCommand)
	return err
}

func (c *Client) ClosePipePane(ctx context.Context, target string) error {
	_, err := c.run(ctx, "pipe-pane", "-t", target)
	return err
}

func (c *Client) capturePane(ctx context.Context, target string, startLine int, escaped bool) (string, error) {
	if len(target) == 0 || target[0] != '%' {
		return "", fmt.Errorf("invalid pane target %q", target)
	}

	if _, err := strconv.Atoi(target[1:]); err != nil {
		return "", fmt.Errorf("invalid pane target %q", target)
	}

	args := []string{"capture-pane", "-p", "-t", target, "-S", strconv.Itoa(startLine)}
	if escaped {
		args = append(args, "-e")
	}
	output, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}

	return output, nil
}

func (c *Client) KillSession(ctx context.Context, sessionName string) error {
	_, err := c.run(ctx, "kill-session", "-t", sessionName)
	return err
}

func (c *Client) BindNoPrefixKey(ctx context.Context, key string, command ...string) error {
	args := []string{"bind-key", "-n", key}
	args = append(args, command...)
	_, err := c.run(ctx, args...)
	return err
}

func (c *Client) SetGlobalOption(ctx context.Context, name string, value string) error {
	_, err := c.run(ctx, "set-option", "-g", name, value)
	return err
}

func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	startedAt := time.Now()
	commandArgs := make([]string, 0, len(args)+2)
	commandArgs = append(commandArgs, SocketFlagArgs(c.socketName)...)
	commandArgs = append(commandArgs, args...)

	logging.Trace(
		"tmux.run.start",
		"socket", c.socketName,
		"args", strings.Join(args, " "),
	)

	command := exec.CommandContext(ctx, c.binary, commandArgs...)
	output, err := command.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		logging.TraceError(
			"tmux.run.error",
			err,
			"socket", c.socketName,
			"args", strings.Join(args, " "),
			"duration_ms", time.Since(startedAt).Milliseconds(),
			"output_preview", logging.Preview(trimmed, 600),
			"output_len", len(trimmed),
		)
		if trimmed == "" {
			return "", fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
		}

		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, trimmed)
	}

	logging.Trace(
		"tmux.run.complete",
		"socket", c.socketName,
		"args", strings.Join(args, " "),
		"duration_ms", time.Since(startedAt).Milliseconds(),
		"output_preview", logging.Preview(trimmed, 600),
		"output_len", len(trimmed),
	)

	return trimmed, nil
}

func parsePanesOutput(output string) ([]Pane, error) {
	if strings.TrimSpace(output) == "" {
		return nil, nil
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	panes := make([]Pane, 0, len(lines))

	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 13 {
			return nil, fmt.Errorf("unexpected pane field count %d in %q", len(fields), line)
		}

		pane, err := parsePaneFields(fields)
		if err != nil {
			return nil, err
		}

		panes = append(panes, pane)
	}

	return panes, nil
}

func parsePaneFields(fields []string) (Pane, error) {
	pid, err := strconv.Atoi(fields[4])
	if err != nil {
		return Pane{}, fmt.Errorf("parse pane pid %q: %w", fields[4], err)
	}

	top, err := strconv.Atoi(fields[7])
	if err != nil {
		return Pane{}, fmt.Errorf("parse pane top %q: %w", fields[7], err)
	}

	left, err := strconv.Atoi(fields[8])
	if err != nil {
		return Pane{}, fmt.Errorf("parse pane left %q: %w", fields[8], err)
	}

	height, err := strconv.Atoi(fields[9])
	if err != nil {
		return Pane{}, fmt.Errorf("parse pane height %q: %w", fields[9], err)
	}

	width, err := strconv.Atoi(fields[10])
	if err != nil {
		return Pane{}, fmt.Errorf("parse pane width %q: %w", fields[10], err)
	}

	return Pane{
		ID:             fields[0],
		Title:          fields[1],
		Active:         fields[2] == "1",
		CurrentCommand: fields[3],
		PID:            pid,
		SessionName:    fields[5],
		WindowID:       fields[6],
		Top:            top,
		Left:           left,
		Height:         height,
		Width:          width,
		AlternateOn:    fields[11] == "1",
		TTY:            fields[12],
	}, nil
}

func foregroundProcessGroup(ctx context.Context, pid int) (int, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid pane pid %d", pid)
	}
	command := exec.CommandContext(ctx, "ps", "-o", "tpgid=", "-p", strconv.Itoa(pid))
	output, err := command.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		if trimmed == "" {
			return 0, fmt.Errorf("ps tpgid for pid %d: %w", pid, err)
		}
		return 0, fmt.Errorf("ps tpgid for pid %d: %w: %s", pid, err, trimmed)
	}
	pgid, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("parse foreground process group %q: %w", trimmed, err)
	}
	return pgid, nil
}

func signalProcessGroup(ctx context.Context, pgid int) error {
	command := exec.CommandContext(ctx, "kill", "-INT", fmt.Sprintf("-%d", pgid))
	output, err := command.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		if trimmed == "" {
			return fmt.Errorf("kill process group %d: %w", pgid, err)
		}
		return fmt.Errorf("kill process group %d: %w: %s", pgid, err, trimmed)
	}
	return nil
}

func environmentArgs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}

	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	args := make([]string, 0, len(env)*2)
	for _, key := range keys {
		args = append(args, "-e", key+"="+env[key])
	}

	return args
}

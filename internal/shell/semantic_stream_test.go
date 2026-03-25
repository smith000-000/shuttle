package shell

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"aiterm/internal/tmux"
)

func TestPipePaneOutputPreservesSemanticOSCSequences(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socketName := fmt.Sprintf("shuttle-pipe-pane-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-pipe-pane-%d", time.Now().UnixNano())

	client, err := tmux.NewClient(socketName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Cleanup(func() {
		_ = client.ClosePipePane(context.Background(), "%0")
		_ = client.KillSession(context.Background(), sessionName)
	})

	workspace, _, err := tmux.BootstrapWorkspace(ctx, client, tmux.BootstrapOptions{
		SessionName:       sessionName,
		StartDir:          ".",
		BottomPanePercent: 30,
		HistoryFile:       filepath.Join(t.TempDir(), "shell_history"),
	})
	if err != nil {
		t.Fatalf("BootstrapWorkspace() error = %v", err)
	}

	streamPath := filepath.Join(t.TempDir(), "pipe.out")
	if err := client.PipePaneOutput(ctx, workspace.TopPane.ID, "cat > "+strconv.Quote(filepath.Clean(streamPath))); err != nil {
		t.Fatalf("PipePaneOutput() error = %v", err)
	}

	command := `bash -lc 'printf $'"'"'\e]133;B\e\\\e]133;C\e\\\e]7;file://localhost/tmp/osc-stream\e\\\e]133;D;130\e\\\e]133;A\e\\\n'"'"''`
	if err := client.SendKeys(ctx, workspace.TopPane.ID, command, true); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}

	var raw string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(streamPath)
		if readErr == nil && len(data) > 0 {
			raw = string(data)
			if strings.Contains(raw, "\x1b]133;A\x1b\\") && strings.Contains(raw, "\x1b]7;file://localhost/tmp/osc-stream\x1b\\") {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if raw == "" {
		t.Fatal("expected pipe-pane output to be captured")
	}
	if !strings.Contains(raw, "\x1b]133;A\x1b\\") {
		t.Fatalf("expected pipe-pane stream to preserve OSC 133 prompt marker, got %q", raw)
	}
	if !strings.Contains(raw, "\x1b]7;file://localhost/tmp/osc-stream\x1b\\") {
		t.Fatalf("expected pipe-pane stream to preserve OSC 7 cwd marker, got %q", raw)
	}

	state, ok := parseSemanticShellStateFromOSCCapture(raw)
	if !ok {
		t.Fatalf("expected semantic OSC state to parse from pipe-pane stream, got %q", raw)
	}
	if state.Event != semanticEventPrompt {
		t.Fatalf("expected prompt event, got %#v", state)
	}
	if !strings.Contains(raw, "\x1b]7;file://localhost/tmp/osc-stream\x1b\\") {
		t.Fatalf("expected raw stream to include target osc cwd marker, got %q", raw)
	}
	if state.ExitCode == nil || *state.ExitCode != 130 {
		t.Fatalf("expected exit code 130, got %#v", state.ExitCode)
	}
}

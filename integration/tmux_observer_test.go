package integration

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiterm/internal/shell"
	"aiterm/internal/tmux"
)

func TestRunTrackedCommand(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socketName := fmt.Sprintf("shuttle-track-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-track-%d", time.Now().UnixNano())

	client, err := tmux.NewClient(socketName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Cleanup(func() {
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

	observer := shell.NewObserver(client)
	result, err := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, "printf 'alpha\\n'; false", 10*time.Second)
	if err != nil {
		t.Fatalf("RunTrackedCommand() error = %v", err)
	}

	if result.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", result.ExitCode)
	}

	if !strings.Contains(result.Captured, "alpha") {
		t.Fatalf("expected captured body to contain alpha, got %q", result.Captured)
	}
}

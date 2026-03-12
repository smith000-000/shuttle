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

func TestRunTrackedInteractiveCommand(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socketName := fmt.Sprintf("shuttle-track-interactive-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-track-interactive-%d", time.Now().UnixNano())

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
	results := make(chan shell.TrackedExecution, 1)
	errs := make(chan error, 1)
	go func() {
		result, runErr := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, `bash -lc 'read -n 1 -s -r -p "Press any key to continue..." _; echo ready'`, 10*time.Second)
		if runErr != nil {
			errs <- runErr
			return
		}
		results <- result
	}()

	time.Sleep(500 * time.Millisecond)
	if err := client.SendKeys(ctx, workspace.TopPane.ID, "x", true); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}

	select {
	case runErr := <-errs:
		t.Fatalf("RunTrackedCommand() error = %v", runErr)
	case result := <-results:
		if result.ExitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", result.ExitCode)
		}
		if !strings.Contains(result.Captured, "ready") {
			t.Fatalf("expected captured output to contain ready, got %q", result.Captured)
		}
		if strings.Contains(result.Captured, "command not found: cho") {
			t.Fatalf("expected wrapper not to leak truncated sentinel command, got %q", result.Captured)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for interactive tracked command")
	}
}

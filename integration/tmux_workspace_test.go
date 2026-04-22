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

func TestBootstrapWorkspace(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socketName := fmt.Sprintf("shuttle-test-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-test-%d", time.Now().UnixNano())

	client, err := tmux.NewClient(socketName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtimeDir := t.TempDir()
	launchProfiles := shell.DefaultLaunchProfiles()
	persistentLaunch, err := shell.PersistentLaunchSpec(runtimeDir, launchProfiles)
	if err != nil {
		t.Fatalf("PersistentLaunchSpec() error = %v", err)
	}

	t.Cleanup(func() {
		_ = client.KillSession(context.Background(), sessionName)
	})

	workspace, created, err := tmux.BootstrapWorkspace(ctx, client, tmux.BootstrapOptions{
		SessionName:       sessionName,
		StartDir:          ".",
		BottomPanePercent: 30,
		HistoryFile:       filepath.Join(t.TempDir(), "shell_history"),
		Launch:            persistentLaunch,
	})
	if err != nil {
		t.Fatalf("BootstrapWorkspace() create error = %v", err)
	}

	if !created {
		t.Fatal("expected first bootstrap to create the workspace")
	}

	if workspace.TopPane.ID == "" || workspace.BottomPane.ID == "" {
		t.Fatalf("unexpected workspace panes: %#v", workspace)
	}

	workspaceAfterRestart, createdAgain, err := tmux.BootstrapWorkspace(ctx, client, tmux.BootstrapOptions{
		SessionName:       sessionName,
		StartDir:          ".",
		BottomPanePercent: 30,
		HistoryFile:       filepath.Join(t.TempDir(), "shell_history"),
		Launch:            persistentLaunch,
	})
	if err != nil {
		t.Fatalf("BootstrapWorkspace() rediscovery error = %v", err)
	}

	if createdAgain {
		t.Fatal("expected second bootstrap to rediscover the workspace")
	}

	if workspace.TopPane.ID != workspaceAfterRestart.TopPane.ID {
		t.Fatalf("expected stable top pane ID, got %s then %s", workspace.TopPane.ID, workspaceAfterRestart.TopPane.ID)
	}

	if err := client.SendKeys(ctx, workspace.TopPane.ID, "echo hello", true); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}

	observer := shell.NewObserver(client)
	result, err := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, "printf '%s\\n' \"$HISTFILE\"", 10*time.Second)
	if err != nil {
		t.Fatalf("RunTrackedCommand() history env error = %v", err)
	}

	if !strings.Contains(result.Captured, "shell_history") {
		t.Fatalf("expected isolated HISTFILE in captured output, got %q", result.Captured)
	}
}

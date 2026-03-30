package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"aiterm/internal/tmux"
)

func TestSelectTakeControlTargetSwitchesToPaneWindow(t *testing.T) {
	socketName := fmt.Sprintf("shuttle-handoff-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-handoff-%d", time.Now().UnixNano())

	client, err := tmux.NewClient(socketName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.NewDetachedSession(ctx, sessionName, t.TempDir(), nil); err != nil {
		t.Fatalf("NewDetachedSession() error = %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = client.KillSession(cleanupCtx, sessionName)
	})

	rootPanes, err := client.ListPanes(ctx, sessionName)
	if err != nil || len(rootPanes) == 0 {
		t.Fatalf("ListPanes(root) error = %v panes=%#v", err, rootPanes)
	}
	if err := client.SplitBottom(ctx, rootPanes[0].ID, 30, t.TempDir()); err != nil {
		t.Fatalf("SplitBottom(root) error = %v", err)
	}

	targetWindowPane, err := client.NewDetachedWindow(ctx, sessionName, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewDetachedWindow() error = %v", err)
	}
	if err := client.SplitBottom(ctx, targetWindowPane.ID, 30, t.TempDir()); err != nil {
		t.Fatalf("SplitBottom(target) error = %v", err)
	}
	targetPanes, err := client.ListPanes(ctx, targetWindowPane.WindowID)
	if err != nil || len(targetPanes) == 0 {
		t.Fatalf("ListPanes(target) error = %v panes=%#v", err, targetPanes)
	}
	targetPaneID := targetPanes[0].ID

	command := &tmuxTakeControlCommand{
		config: takeControlConfig{
			SocketName:    socketName,
			SessionName:   sessionName,
			TrackedPaneID: targetPaneID,
			DetachKey:     TakeControlKey,
		},
	}

	zoomedHere, err := command.selectTakeControlTarget(targetPaneID)
	if err != nil {
		t.Fatalf("selectTakeControlTarget() error = %v", err)
	}
	if !zoomedHere {
		t.Fatal("expected target window to be zoomed by take-control selection")
	}

	currentWindowID, err := command.captureTmux("display-message", "-p", "-t", sessionName, "#{window_id}")
	if err != nil {
		t.Fatalf("capture current window: %v", err)
	}
	if strings.TrimSpace(currentWindowID) != targetWindowPane.WindowID {
		t.Fatalf("expected current window %q, got %q", targetWindowPane.WindowID, currentWindowID)
	}

	currentPaneID, err := command.captureTmux("display-message", "-p", "-t", sessionName, "#{pane_id}")
	if err != nil {
		t.Fatalf("capture current pane: %v", err)
	}
	if strings.TrimSpace(currentPaneID) != targetPaneID {
		t.Fatalf("expected current pane %q, got %q", targetPaneID, currentPaneID)
	}
}

func TestInstallTemporaryPaneWindowHookFiresWhenWindowCloses(t *testing.T) {
	socketName := fmt.Sprintf("shuttle-handoff-hook-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-handoff-hook-%d", time.Now().UnixNano())

	client, err := tmux.NewClient(socketName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.NewDetachedSession(ctx, sessionName, t.TempDir(), nil); err != nil {
		t.Fatalf("NewDetachedSession() error = %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = client.KillSession(cleanupCtx, sessionName)
	})

	targetWindowPane, err := client.NewDetachedWindow(ctx, sessionName, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewDetachedWindow() error = %v", err)
	}
	targetPaneID := targetWindowPane.ID

	command := &tmuxTakeControlCommand{
		config: takeControlConfig{
			SocketName:    socketName,
			SessionName:   sessionName,
			TrackedPaneID: targetPaneID,
			TemporaryPane: true,
			DetachKey:     TakeControlKey,
		},
	}

	cleanupHook, err := command.installTemporaryPaneWindowHook(targetPaneID, "set-environment -g SHUTTLE_TEST_AUTO_DETACH 1")
	if err != nil {
		t.Fatalf("installTemporaryPaneWindowHook() error = %v", err)
	}
	defer cleanupHook()

	if err := client.KillWindow(ctx, targetWindowPane.WindowID); err != nil {
		t.Fatalf("KillWindow() error = %v", err)
	}

	output, err := command.captureTmux("show-environment", "-g")
	if err != nil {
		t.Fatalf("show-environment error = %v", err)
	}
	if !strings.Contains(output, "SHUTTLE_TEST_AUTO_DETACH=1") {
		t.Fatalf("expected window-unlinked hook to fire, got %q", output)
	}
}

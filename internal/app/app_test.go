package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"aiterm/internal/config"
	"aiterm/internal/tmux"
)

func TestShouldDestroyManagedSessionWhenCreated(t *testing.T) {
	cfg := config.Config{
		WorkspaceID: "79baac172b91",
		RuntimeDir:  "/run/user/1000/shuttle",
		TmuxSocket:  filepath.Join("/run/user/1000/shuttle", "tmux.sock"),
	}
	if !shouldDestroyManagedSession(cfg, true, "shuttle_79baac172b91") {
		t.Fatal("expected created session to be destroyed")
	}
}

func TestShouldDestroyManagedSessionOnManagedReattach(t *testing.T) {
	cfg := config.Config{
		WorkspaceID: "79baac172b91",
		RuntimeDir:  "/run/user/1000/shuttle",
		TmuxSocket:  filepath.Join("/run/user/1000/shuttle", "tmux.sock"),
	}
	if !shouldDestroyManagedSession(cfg, false, "shuttle_79baac172b91") {
		t.Fatal("expected managed reattached session to be destroyed")
	}
}

func TestShouldNotDestroyExplicitCustomSessionOnReattach(t *testing.T) {
	cfg := config.Config{
		WorkspaceID: "79baac172b91",
		RuntimeDir:  "/run/user/1000/shuttle",
		TmuxSocket:  "custom-socket",
		SessionName: "custom-session",
	}
	if shouldDestroyManagedSession(cfg, false, "custom-session") {
		t.Fatal("expected custom reattached session not to be destroyed automatically")
	}
}

type stubCleanupModel struct {
	sessionName string
}

func (stubCleanupModel) Init() tea.Cmd                         { return nil }
func (m stubCleanupModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (stubCleanupModel) View() string                          { return "" }
func (m stubCleanupModel) CleanupSessionName() string          { return m.sessionName }

func TestResolveCleanupSessionNamePrefersFinalModelSession(t *testing.T) {
	resolved := resolveCleanupSessionName(stubCleanupModel{sessionName: "shuttle-recovered"}, "shuttle-original")
	if resolved != "shuttle-recovered" {
		t.Fatalf("expected recovered cleanup session, got %q", resolved)
	}
}

func TestResolveCleanupSessionNameFallsBackWithoutProvider(t *testing.T) {
	resolved := resolveCleanupSessionName(stubCleanupModel{sessionName: ""}, "shuttle-original")
	if resolved != "shuttle-original" {
		t.Fatalf("expected fallback cleanup session, got %q", resolved)
	}
}

type stubRuntimeSocketProbe struct {
	panes []tmux.Pane
	err   error
}

func (s stubRuntimeSocketProbe) ListAllPanes(context.Context) ([]tmux.Pane, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]tmux.Pane(nil), s.panes...), nil
}

func TestCleanupStaleManagedSocketRemovesDeadAbsoluteSocketPath(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "tmux.sock")
	if err := os.WriteFile(socketPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := cleanupStaleManagedSocket(context.Background(), socketPath, stubRuntimeSocketProbe{err: errors.New("tmux list-panes -a: exit status 1: no server running on " + socketPath)}); err != nil {
		t.Fatalf("cleanupStaleManagedSocket() error = %v", err)
	}
	if _, err := os.Stat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale socket to be removed, stat err = %v", err)
	}
}

func TestCleanupStaleManagedSocketKeepsLiveOrNonAbsoluteTargets(t *testing.T) {
	dir := t.TempDir()
	liveSocket := filepath.Join(dir, "live.sock")
	if err := os.WriteFile(liveSocket, []byte("live"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := cleanupStaleManagedSocket(context.Background(), liveSocket, stubRuntimeSocketProbe{}); err != nil {
		t.Fatalf("cleanupStaleManagedSocket(live) error = %v", err)
	}
	if _, err := os.Stat(liveSocket); err != nil {
		t.Fatalf("expected live socket to remain, stat err = %v", err)
	}

	relativeSocket := filepath.Join("relative", "tmux.sock")
	if err := cleanupStaleManagedSocket(context.Background(), relativeSocket, stubRuntimeSocketProbe{err: errors.New("tmux list-panes -a: exit status 1: no server running")}); err != nil {
		t.Fatalf("cleanupStaleManagedSocket(relative) error = %v", err)
	}
}

func TestCleanupStaleManagedSocketRemovesDeadMissingSocketPathError(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "tmux.sock")
	if err := os.WriteFile(socketPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	err := errors.New("tmux list-panes -a: exit status 1: error connecting to " + socketPath + " (No such file or directory)")
	if err := cleanupStaleManagedSocket(context.Background(), socketPath, stubRuntimeSocketProbe{err: err}); err != nil {
		t.Fatalf("cleanupStaleManagedSocket() error = %v", err)
	}
	if _, err := os.Stat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale socket to be removed after missing-path error, stat err = %v", err)
	}
}

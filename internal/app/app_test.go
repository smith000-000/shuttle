package app

import (
	"path/filepath"
	"testing"

	"aiterm/internal/config"
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


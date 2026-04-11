package shell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"aiterm/internal/tmux"
)

type stubRuntimePaneLister struct {
	panes []tmux.Pane
	err   error
}

func (s stubRuntimePaneLister) ListAllPanes(context.Context) ([]tmux.Pane, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]tmux.Pane(nil), s.panes...), nil
}

func TestPrepareRuntimeArtifactsRemovesStaleArtifactsAndKeepsLiveShellState(t *testing.T) {
	runtimeDir := t.TempDir()
	commandsDir := filepath.Join(runtimeDir, "commands")
	integrationDir := filepath.Join(runtimeDir, "shell-integration")
	stateDir := filepath.Join(runtimeDir, "shell-state")
	streamDir := filepath.Join(runtimeDir, "semantic-stream")
	for _, dir := range []string{commandsDir, integrationDir, stateDir, streamDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(commandsDir, "stale.sh"), []byte("echo stale\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(commands) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(integrationDir, "stale.sh"), []byte("echo stale\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(shell-integration) error = %v", err)
	}
	liveTTY := "/dev/pts/7"
	liveStatePath := filepath.Join(stateDir, semanticStateFileName(liveTTY))
	staleStatePath := filepath.Join(stateDir, semanticStateFileName("/dev/pts/8"))
	if err := os.WriteFile(liveStatePath, []byte(`{"event":"prompt"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(live state) error = %v", err)
	}
	if err := os.WriteFile(staleStatePath, []byte(`{"event":"prompt"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(stale state) error = %v", err)
	}
	deadGeneration := filepath.Join(streamDir, "session-111-1")
	liveGeneration := filepath.Join(streamDir, "session-222-1")
	if err := os.MkdirAll(deadGeneration, 0o700); err != nil {
		t.Fatalf("MkdirAll(dead generation) error = %v", err)
	}
	if err := os.MkdirAll(liveGeneration, 0o700); err != nil {
		t.Fatalf("MkdirAll(live generation) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(deadGeneration, "tty.log"), []byte("dead\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(dead generation) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(liveGeneration, "tty.log"), []byte("live\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(live generation) error = %v", err)
	}

	original := semanticStreamProcessAlive
	semanticStreamProcessAlive = func(pid int) bool { return pid == 222 }
	defer func() { semanticStreamProcessAlive = original }()

	err := PrepareRuntimeArtifacts(context.Background(), runtimeDir, stubRuntimePaneLister{
		panes: []tmux.Pane{{ID: "%1", TTY: liveTTY}},
	})
	if err != nil {
		t.Fatalf("PrepareRuntimeArtifacts() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(commandsDir, "stale.sh")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale command script to be removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(integrationDir, "stale.sh")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale integration script to be removed, stat err = %v", err)
	}
	if _, err := os.Stat(liveStatePath); err != nil {
		t.Fatalf("expected live shell state to remain, stat err = %v", err)
	}
	if _, err := os.Stat(staleStatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale shell state to be removed, stat err = %v", err)
	}
	if _, err := os.Stat(deadGeneration); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected dead semantic stream generation to be removed, stat err = %v", err)
	}
	if _, err := os.Stat(liveGeneration); err != nil {
		t.Fatalf("expected live semantic stream generation to remain, stat err = %v", err)
	}
}

func TestPrepareRuntimeArtifactsTreatsMissingTmuxServerAsStaleState(t *testing.T) {
	runtimeDir := t.TempDir()
	stateDir := filepath.Join(runtimeDir, "shell-state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(shell-state) error = %v", err)
	}
	staleStatePath := filepath.Join(stateDir, semanticStateFileName("/dev/pts/9"))
	if err := os.WriteFile(staleStatePath, []byte(`{"event":"prompt"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(stale state) error = %v", err)
	}

	err := PrepareRuntimeArtifacts(context.Background(), runtimeDir, stubRuntimePaneLister{
		err: errors.New("tmux list-panes -a: exit status 1: no server running on /tmp/tmux-1000/default"),
	})
	if err != nil {
		t.Fatalf("PrepareRuntimeArtifacts() error = %v", err)
	}
	if _, err := os.Stat(staleStatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale shell state to be removed when tmux server is unavailable, stat err = %v", err)
	}
}

func TestPrepareRuntimeArtifactsNormalizesArtifactDirectoryPermissions(t *testing.T) {
	runtimeDir := t.TempDir()
	commandsDir := filepath.Join(runtimeDir, "commands")
	integrationDir := filepath.Join(runtimeDir, "shell-integration")
	stateDir := filepath.Join(runtimeDir, "shell-state")
	streamDir := filepath.Join(runtimeDir, "semantic-stream")
	for _, dir := range []string{commandsDir, integrationDir, stateDir, streamDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatalf("Chmod(%q) error = %v", dir, err)
		}
	}

	if err := PrepareRuntimeArtifacts(context.Background(), runtimeDir, nil); err != nil {
		t.Fatalf("PrepareRuntimeArtifacts() error = %v", err)
	}
	for _, dir := range []string{commandsDir, integrationDir, stateDir, streamDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", dir, err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("expected %q perms 0700, got %#o", dir, info.Mode().Perm())
		}
	}
}

func TestPrepareRuntimeArtifactsTreatsMissingSocketPathAsStaleState(t *testing.T) {
	runtimeDir := t.TempDir()
	stateDir := filepath.Join(runtimeDir, "shell-state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(shell-state) error = %v", err)
	}
	staleStatePath := filepath.Join(stateDir, semanticStateFileName("/dev/pts/10"))
	if err := os.WriteFile(staleStatePath, []byte(`{"event":"prompt"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(stale state) error = %v", err)
	}

	err := PrepareRuntimeArtifacts(context.Background(), runtimeDir, stubRuntimePaneLister{
		err: errors.New("tmux list-panes -a: exit status 1: error connecting to /run/user/1000/shuttle/tmux.sock (No such file or directory)"),
	})
	if err != nil {
		t.Fatalf("PrepareRuntimeArtifacts() error = %v", err)
	}
	if _, err := os.Stat(staleStatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale shell state to be removed when managed socket path is missing, stat err = %v", err)
	}
}

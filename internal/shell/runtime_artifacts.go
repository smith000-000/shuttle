package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aiterm/internal/securefs"
	"aiterm/internal/tmux"
)

type runtimePaneLister interface {
	ListAllPanes(context.Context) ([]tmux.Pane, error)
}

// PrepareRuntimeArtifacts normalizes short-lived runtime state under runtimeDir.
// Persistent operator state such as shell history and stored provider config stays in the state dir instead.
func PrepareRuntimeArtifacts(ctx context.Context, runtimeDir string, paneLister runtimePaneLister) error {
	runtimeDir = strings.TrimSpace(runtimeDir)
	if runtimeDir == "" {
		return nil
	}
	if err := securefs.EnsurePrivateDir(runtimeDir); err != nil {
		return fmt.Errorf("prepare runtime directory: %w", err)
	}
	if err := pruneRuntimeStagingDir(filepath.Join(runtimeDir, "commands")); err != nil {
		return err
	}
	if err := pruneRuntimeStagingDir(filepath.Join(runtimeDir, "shell-integration")); err != nil {
		return err
	}
	streamDir := filepath.Join(runtimeDir, "semantic-stream")
	if err := securefs.EnsurePrivateDir(streamDir); err != nil {
		return fmt.Errorf("prepare semantic stream directory: %w", err)
	}
	if err := pruneStaleSemanticStreamGenerations(streamDir, ""); err != nil {
		return err
	}
	stateDir := filepath.Join(runtimeDir, "shell-state")
	if err := securefs.EnsurePrivateDir(stateDir); err != nil {
		return fmt.Errorf("prepare semantic state directory: %w", err)
	}
	liveTTYs, err := runtimeLivePaneTTYs(ctx, paneLister)
	if err != nil {
		return err
	}
	if err := pruneSemanticStateDir(stateDir, liveTTYs); err != nil {
		return err
	}
	return nil
}

func pruneRuntimeStagingDir(path string) error {
	if err := securefs.EnsurePrivateDir(path); err != nil {
		return fmt.Errorf("prepare runtime artifact directory %q: %w", path, err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read runtime artifact directory %q: %w", path, err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(path, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale runtime artifact %q: %w", filepath.Join(path, entry.Name()), err)
		}
	}
	return nil
}

func runtimeLivePaneTTYs(ctx context.Context, paneLister runtimePaneLister) (map[string]struct{}, error) {
	liveTTYs := map[string]struct{}{}
	if paneLister == nil {
		return liveTTYs, nil
	}
	panes, err := paneLister.ListAllPanes(ctx)
	if err != nil {
		if RuntimeServerUnavailable(err) {
			return liveTTYs, nil
		}
		return nil, fmt.Errorf("list tmux panes for runtime cleanup: %w", err)
	}
	for _, pane := range panes {
		if tty := strings.TrimSpace(pane.TTY); tty != "" {
			liveTTYs[semanticStateFileName(tty)] = struct{}{}
		}
	}
	return liveTTYs, nil
}

func pruneSemanticStateDir(path string, liveTTYs map[string]struct{}) error {
	if err := securefs.EnsurePrivateDir(path); err != nil {
		return fmt.Errorf("prepare semantic state directory %q: %w", path, err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read semantic state directory %q: %w", path, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if _, ok := liveTTYs[name]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(path, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale semantic state %q: %w", filepath.Join(path, name), err)
		}
	}
	return nil
}

func RuntimeServerUnavailable(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no server running") ||
		strings.Contains(text, "failed to connect to server") ||
		strings.Contains(text, "error connecting to") ||
		strings.Contains(text, "no such file or directory")
}

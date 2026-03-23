package tmux

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"aiterm/internal/securefs"
)

const shuttleHistoryLimit = 50000

type BootstrapOptions struct {
	SessionName       string
	StartDir          string
	BottomPanePercent int
	HistoryFile       string
}

type Workspace struct {
	SessionName string
	WindowID    string
	TopPane     Pane
	BottomPane  Pane
}

type ShellSessionOptions struct {
	SessionName string
	StartDir    string
	HistoryFile string
}

func BootstrapWorkspace(ctx context.Context, client *Client, options BootstrapOptions) (Workspace, bool, error) {
	if client == nil {
		return Workspace{}, false, errors.New("tmux client is required")
	}

	if options.SessionName == "" {
		return Workspace{}, false, errors.New("session name is required")
	}

	if options.StartDir == "" {
		return Workspace{}, false, errors.New("start directory is required")
	}

	if options.BottomPanePercent <= 0 || options.BottomPanePercent >= 100 {
		return Workspace{}, false, fmt.Errorf("bottom pane percent must be between 1 and 99, got %d", options.BottomPanePercent)
	}

	sessionEnv := shellHistoryEnvironment(options.HistoryFile)
	if err := ensureSessionFiles(sessionEnv); err != nil {
		return Workspace{}, false, err
	}

	created := false
	exists, err := client.HasSession(ctx, options.SessionName)
	if err != nil {
		return Workspace{}, false, err
	}

	if !exists {
		if err := client.NewDetachedSession(ctx, options.SessionName, options.StartDir, sessionEnv); err != nil {
			return Workspace{}, false, fmt.Errorf("create session: %w", err)
		}

		panes, err := client.ListPanes(ctx, options.SessionName)
		if err != nil {
			return Workspace{}, false, fmt.Errorf("list panes after session create: %w", err)
		}

		if len(panes) != 1 {
			return Workspace{}, false, fmt.Errorf("expected 1 pane after new session, found %d", len(panes))
		}

		if err := client.SplitBottom(ctx, panes[0].ID, options.BottomPanePercent, options.StartDir); err != nil {
			return Workspace{}, false, fmt.Errorf("split bottom pane: %w", err)
		}

		created = true
	}

	if err := client.SetGlobalOption(ctx, "history-limit", fmt.Sprintf("%d", shuttleHistoryLimit)); err != nil {
		return Workspace{}, false, fmt.Errorf("set tmux history limit: %w", err)
	}

	panes, err := client.ListPanes(ctx, options.SessionName)
	if err != nil {
		return Workspace{}, false, fmt.Errorf("list panes for workspace: %w", err)
	}
	if len(panes) == 1 {
		if err := client.SplitBottom(ctx, panes[0].ID, options.BottomPanePercent, options.StartDir); err != nil {
			return Workspace{}, false, fmt.Errorf("split bottom pane for existing session: %w", err)
		}
		panes, err = client.ListPanes(ctx, options.SessionName)
		if err != nil {
			return Workspace{}, false, fmt.Errorf("list panes after session repair: %w", err)
		}
	}

	workspace, err := workspaceFromPanes(options.SessionName, panes)
	if err != nil {
		return Workspace{}, false, err
	}

	return workspace, created, nil
}

func workspaceFromPanes(sessionName string, panes []Pane) (Workspace, error) {
	if len(panes) != 2 {
		return Workspace{}, fmt.Errorf("workspace %q is malformed: expected 2 panes, found %d", sessionName, len(panes))
	}

	sorted := append([]Pane(nil), panes...)
	sort.Slice(sorted, func(i int, j int) bool {
		if sorted[i].Top == sorted[j].Top {
			return sorted[i].Left < sorted[j].Left
		}

		return sorted[i].Top < sorted[j].Top
	})

	if sorted[0].WindowID != sorted[1].WindowID {
		return Workspace{}, fmt.Errorf("workspace %q spans multiple windows (%s, %s)", sessionName, sorted[0].WindowID, sorted[1].WindowID)
	}

	return Workspace{
		SessionName: sessionName,
		WindowID:    sorted[0].WindowID,
		TopPane:     sorted[0],
		BottomPane:  sorted[1],
	}, nil
}

func BootstrapShellSession(ctx context.Context, client *Client, options ShellSessionOptions) (Pane, bool, error) {
	if client == nil {
		return Pane{}, false, errors.New("tmux client is required")
	}
	if options.SessionName == "" {
		return Pane{}, false, errors.New("session name is required")
	}
	if options.StartDir == "" {
		return Pane{}, false, errors.New("start directory is required")
	}

	sessionEnv := shellHistoryEnvironment(options.HistoryFile)
	if err := ensureSessionFiles(sessionEnv); err != nil {
		return Pane{}, false, err
	}

	created := false
	exists, err := client.HasSession(ctx, options.SessionName)
	if err != nil {
		return Pane{}, false, err
	}
	if !exists {
		if err := client.NewDetachedSession(ctx, options.SessionName, options.StartDir, sessionEnv); err != nil {
			return Pane{}, false, fmt.Errorf("create session: %w", err)
		}
		created = true
	}

	if err := client.SetGlobalOption(ctx, "history-limit", fmt.Sprintf("%d", shuttleHistoryLimit)); err != nil {
		return Pane{}, false, fmt.Errorf("set tmux history limit: %w", err)
	}

	panes, err := client.ListPanes(ctx, options.SessionName)
	if err != nil {
		return Pane{}, false, fmt.Errorf("list panes for session: %w", err)
	}
	topPane, err := topPaneFromPanes(options.SessionName, panes)
	if err != nil {
		return Pane{}, false, err
	}
	return topPane, created, nil
}

func topPaneFromPanes(sessionName string, panes []Pane) (Pane, error) {
	if len(panes) == 0 {
		return Pane{}, fmt.Errorf("session %q has no panes", sessionName)
	}

	sorted := append([]Pane(nil), panes...)
	sort.Slice(sorted, func(i int, j int) bool {
		if sorted[i].Top == sorted[j].Top {
			return sorted[i].Left < sorted[j].Left
		}
		return sorted[i].Top < sorted[j].Top
	})
	return sorted[0], nil
}

func shellHistoryEnvironment(historyFile string) map[string]string {
	if historyFile == "" {
		return nil
	}

	return map[string]string{
		"HISTFILE":         historyFile,
		"HISTSIZE":         "5000",
		"HISTFILESIZE":     "5000",
		"SHUTTLE_HISTFILE": historyFile,
	}
}

func ensureSessionFiles(env map[string]string) error {
	historyFile := env["HISTFILE"]
	if historyFile == "" {
		return nil
	}

	if err := securefs.EnsurePrivateDir(filepath.Dir(historyFile)); err != nil {
		return fmt.Errorf("create shell history directory: %w", err)
	}

	if err := securefs.EnsureFilePrivate(historyFile, 0o600); err != nil {
		return fmt.Errorf("create shell history file: %w", err)
	}
	return nil
}

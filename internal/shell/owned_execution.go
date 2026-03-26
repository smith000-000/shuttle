package shell

import (
	"context"
	"fmt"
	"strings"

	"aiterm/internal/logging"
)

const (
	ownedExecutionWindowName = "shuttle-exec [temporary]"
	ownedExecutionPaneTitle  = "shuttle-exec temporary"
)

type OwnedExecutionPane struct {
	SessionName string
	PaneID      string
	WindowID    string
}

func (o *Observer) CreateOwnedExecutionPane(ctx context.Context, startDir string) (OwnedExecutionPane, func(context.Context) error, error) {
	if o.tmuxClient == nil {
		return OwnedExecutionPane{}, nil, fmt.Errorf("tmux client is not available for owned execution panes")
	}
	if strings.TrimSpace(o.sessionName) == "" {
		return OwnedExecutionPane{}, nil, fmt.Errorf("session name is required for owned execution panes")
	}

	startDir = strings.TrimSpace(startDir)
	if startDir == "" {
		startDir = strings.TrimSpace(o.promptHint.Directory)
	}
	if startDir == "" {
		startDir = strings.TrimSpace(o.startDir)
	}
	if startDir == "" {
		startDir = "."
	}

	pane, err := o.tmuxClient.NewDetachedWindow(ctx, o.sessionName, startDir, nil)
	if err != nil && shouldRecoverObserverSession(err) {
		if ensureErr := o.ensureSessionAvailable(ctx); ensureErr != nil {
			return OwnedExecutionPane{}, nil, ensureErr
		}
		pane, err = o.tmuxClient.NewDetachedWindow(ctx, o.sessionName, startDir, nil)
	}
	if err != nil {
		return OwnedExecutionPane{}, nil, err
	}

	target := OwnedExecutionPane{
		SessionName: strings.TrimSpace(pane.SessionName),
		PaneID:      strings.TrimSpace(pane.ID),
		WindowID:    strings.TrimSpace(pane.WindowID),
	}
	if target.SessionName == "" {
		target.SessionName = strings.TrimSpace(o.sessionName)
	}
	o.markOwnedExecutionPane(ctx, target)

	cleanup := func(ctx context.Context) error {
		windowID := target.WindowID
		if windowID == "" {
			windowID = target.PaneID
		}
		if strings.TrimSpace(windowID) == "" {
			return nil
		}
		err := o.tmuxClient.KillWindow(ctx, windowID)
		if err != nil && isPaneNotFoundError(err) {
			return nil
		}
		return err
	}

	logging.Trace(
		"shell.owned_execution.created",
		"session", target.SessionName,
		"pane", target.PaneID,
		"window", target.WindowID,
		"start_dir", startDir,
	)

	return target, cleanup, nil
}

func (o *Observer) markOwnedExecutionPane(ctx context.Context, target OwnedExecutionPane) {
	windowID := strings.TrimSpace(target.WindowID)
	if windowID != "" {
		if err := o.tmuxClient.SetWindowOption(ctx, windowID, "automatic-rename", "off"); err != nil {
			logging.TraceError("shell.owned_execution.window_option_error", err, "window", windowID)
		}
		if err := o.tmuxClient.RenameWindow(ctx, windowID, ownedExecutionWindowName); err != nil {
			logging.TraceError("shell.owned_execution.rename_error", err, "window", windowID)
		}
	}
	if paneID := strings.TrimSpace(target.PaneID); paneID != "" {
		if err := o.tmuxClient.SetPaneTitle(ctx, paneID, ownedExecutionPaneTitle); err != nil {
			logging.TraceError("shell.owned_execution.pane_title_error", err, "pane", paneID)
		}
	}
}

package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"aiterm/internal/config"
	"aiterm/internal/controller"
	"aiterm/internal/provider"
	"aiterm/internal/shell"
	"aiterm/internal/tmux"
	"aiterm/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	agentPromptTimeout = 60 * time.Second
)

type App struct {
	cfg    config.Config
	logger *slog.Logger
}

type Result struct {
	Workspace       tmux.Workspace
	Created         bool
	AgentEvents     []controller.TranscriptEvent
	InjectedCommand string
	Tracked         *shell.TrackedExecution
	Interactive     bool
}

func New(cfg config.Config, logger *slog.Logger) *App {
	return &App{cfg: cfg, logger: logger}
}

func (a *App) Run(ctx context.Context) (Result, error) {
	client, err := tmux.NewClient(a.cfg.TmuxSocket)
	if err != nil {
		return Result{}, err
	}

	workspace, created, err := tmux.BootstrapWorkspace(
		ctx,
		client,
		tmux.BootstrapOptions{
			SessionName:       a.cfg.SessionName,
			StartDir:          a.cfg.StartDir,
			BottomPanePercent: 30,
			HistoryFile:       filepath.Join(a.cfg.StateDir, "shell_history"),
		},
	)
	if err != nil {
		return Result{}, err
	}

	a.logger.Info(
		"workspace ready",
		"session", workspace.SessionName,
		"created", created,
		"top_pane", workspace.TopPane.ID,
		"bottom_pane", workspace.BottomPane.ID,
	)

	result := Result{
		Workspace: workspace,
		Created:   created,
	}

	if a.cfg.AgentPrompt != "" {
		observer := shell.NewObserver(client)
		initialShellContext := initialPromptContext(ctx, observer, workspace.TopPane.ID, a.cfg.StartDir)
		agent, profile, err := provider.NewFromConfig(a.cfg, provider.FactoryOptions{})
		if err != nil {
			return Result{}, fmt.Errorf("configure provider: %w", err)
		}
		a.logger.Info(
			"provider ready",
			"preset", profile.Preset,
			"backend_family", profile.BackendFamily,
			"auth_method", profile.AuthMethod,
			"model", profile.Model,
			"base_url", profile.BaseURL,
			"api_key_env", profile.APIKeyEnvVar,
		)
		ctrl := controller.New(agent, observer, observer, controller.SessionContext{
			SessionName:      workspace.SessionName,
			TopPaneID:        workspace.TopPane.ID,
			BottomPaneID:     workspace.BottomPane.ID,
			WorkingDirectory: a.cfg.StartDir,
			CurrentShell:     initialShellContextPtr(initialShellContext),
		})
		agentCtx, cancel := context.WithTimeout(ctx, agentPromptTimeout)
		defer cancel()

		events, err := ctrl.SubmitAgentPrompt(agentCtx, a.cfg.AgentPrompt)
		if err != nil {
			return Result{}, fmt.Errorf("submit agent prompt: %w", err)
		}

		result.AgentEvents = events
		return result, nil
	}

	if a.cfg.TUI {
		observer := shell.NewObserver(client)
		if err := client.BindNoPrefixKey(ctx, tui.TakeControlKey, "detach-client"); err != nil {
			return Result{}, fmt.Errorf("configure take-control key: %w", err)
		}
		initialShellContext := initialPromptContext(ctx, observer, workspace.TopPane.ID, a.cfg.StartDir)
		agent, profile, err := provider.NewFromConfig(a.cfg, provider.FactoryOptions{})
		if err != nil {
			return Result{}, fmt.Errorf("configure provider: %w", err)
		}
		a.logger.Info(
			"provider ready",
			"preset", profile.Preset,
			"backend_family", profile.BackendFamily,
			"auth_method", profile.AuthMethod,
			"model", profile.Model,
			"base_url", profile.BaseURL,
			"api_key_env", profile.APIKeyEnvVar,
		)
		ctrl := controller.New(agent, observer, observer, controller.SessionContext{
			SessionName:      workspace.SessionName,
			TopPaneID:        workspace.TopPane.ID,
			BottomPaneID:     workspace.BottomPane.ID,
			WorkingDirectory: a.cfg.StartDir,
			CurrentShell:     initialShellContextPtr(initialShellContext),
		})
		model := tui.NewModel(workspace, ctrl).
			WithShellContext(initialShellContext).
			WithTakeControl(a.cfg.TmuxSocket, workspace.SessionName, workspace.TopPane.ID, tui.TakeControlKey)
		program := tea.NewProgram(model, tea.WithAltScreen())
		_, runErr := program.Run()
		cleanupErr := cleanupTUISession(created, client, workspace.SessionName)
		if runErr != nil {
			if cleanupErr != nil {
				return Result{}, fmt.Errorf("run tui: %w (cleanup error: %v)", runErr, cleanupErr)
			}

			return Result{}, fmt.Errorf("run tui: %w", runErr)
		}

		if cleanupErr != nil {
			return Result{}, fmt.Errorf("cleanup tui session: %w", cleanupErr)
		}

		result.Interactive = true
		return result, nil
	}

	if a.cfg.Inject != "" {
		if err := client.SendKeys(ctx, workspace.TopPane.ID, a.cfg.Inject, a.cfg.InjectEnter); err != nil {
			return Result{}, fmt.Errorf("inject command: %w", err)
		}

		a.logger.Info("command injected", "pane", workspace.TopPane.ID, "command", a.cfg.Inject)
		result.InjectedCommand = a.cfg.Inject
	}

	if a.cfg.Track != "" {
		observer := shell.NewObserver(client)
		tracked, err := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, a.cfg.Track, shell.CommandTimeout(a.cfg.Track))
		if err != nil {
			return Result{}, fmt.Errorf("track command: %w", err)
		}

		a.logger.Info(
			"tracked command complete",
			"pane", workspace.TopPane.ID,
			"command_id", tracked.CommandID,
			"exit_code", tracked.ExitCode,
		)
		result.Tracked = &tracked
	}

	return result, nil
}

func initialPromptContext(ctx context.Context, observer *shell.Observer, paneID string, startDir string) shell.PromptContext {
	if observer != nil && paneID != "" {
		if promptContext, err := observer.CaptureShellContext(ctx, paneID); err == nil && promptContext.PromptLine() != "" {
			return promptContext
		}
	}

	return shell.GuessLocalContext(startDir)
}

func initialShellContextPtr(promptContext shell.PromptContext) *shell.PromptContext {
	if promptContext.PromptLine() == "" {
		return nil
	}

	contextCopy := promptContext
	return &contextCopy
}

func cleanupTUISession(created bool, client *tmux.Client, sessionName string) error {
	if !created {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return client.KillSession(ctx, sessionName)
}

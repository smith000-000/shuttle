package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"aiterm/internal/agentruntime"
	"aiterm/internal/config"
	"aiterm/internal/controller"
	"aiterm/internal/logging"
	"aiterm/internal/provider"
	"aiterm/internal/securefs"
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
	socketTarget := tmux.ResolveSocketTarget(a.cfg.TmuxSocket)
	if filepath.IsAbs(socketTarget) {
		if err := securefs.EnsurePrivateDir(filepath.Dir(socketTarget)); err != nil {
			return Result{}, fmt.Errorf("prepare tmux runtime directory: %w", err)
		}
	}
	historyFile := filepath.Join(a.cfg.StateDir, "shell_history")
	ensureWorkspace := func(ctx context.Context, client *tmux.Client, startDir string) error {
		_, _, err := tmux.BootstrapShellSession(
			ctx,
			client,
			tmux.ShellSessionOptions{
				SessionName: a.cfg.SessionName,
				StartDir:    startDir,
				HistoryFile: historyFile,
			},
		)
		if err != nil {
			return err
		}
		return client.BindNoPrefixKey(ctx, tui.TakeControlKey, "detach-client")
	}
	logging.Trace(
		"app.run.begin",
		"workspace_id", a.cfg.WorkspaceID,
		"session", a.cfg.SessionName,
		"socket", socketTarget,
		"tui", a.cfg.TUI,
		"inject", a.cfg.Inject,
		"track", a.cfg.Track,
		"agent_prompt", a.cfg.AgentPrompt != "",
	)
	runtimeCfg, err := provider.ApplyStoredProviderConfig(a.cfg)
	if err != nil {
		return Result{}, fmt.Errorf("load stored provider config: %w", err)
	}

	client, err := tmux.NewClient(socketTarget)
	if err != nil {
		logging.TraceError("app.tmux_client.error", err, "socket", socketTarget)
		return Result{}, err
	}

	workspace, created, err := tmux.BootstrapWorkspace(
		ctx,
		client,
		tmux.BootstrapOptions{
			SessionName:       a.cfg.SessionName,
			StartDir:          a.cfg.StartDir,
			BottomPanePercent: 30,
			HistoryFile:       historyFile,
		},
	)
	if err != nil {
		logging.TraceError("app.bootstrap.error", err, "session", a.cfg.SessionName)
		return Result{}, err
	}

	logging.Trace(
		"app.bootstrap.complete",
		"session", workspace.SessionName,
		"created", created,
		"top_pane", workspace.TopPane.ID,
		"bottom_pane", workspace.BottomPane.ID,
	)

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
		observer := shell.NewObserver(client).WithStateDir(a.cfg.RuntimeDir).WithSessionName(workspace.SessionName).WithStartDir(runtimeCfg.StartDir).WithSessionEnsurer(func(ctx context.Context) error {
			return ensureWorkspace(ctx, client, runtimeCfg.StartDir)
		})
		initialShellContext := initialPromptContext(ctx, observer, workspace.TopPane.ID, runtimeCfg.StartDir)
		observer.WithPromptHint(initialShellContext)
		if err := observer.EnsureLocalShellIntegration(ctx, workspace.TopPane.ID); err != nil {
			logging.TraceError("app.shell_integration.error", err, "pane", workspace.TopPane.ID)
		}
		agent, profile, err := provider.NewFromConfig(runtimeCfg, provider.FactoryOptions{})
		if err != nil {
			logging.TraceError("app.provider.error", err, "provider", a.cfg.ProviderType)
			return Result{}, fmt.Errorf("configure provider: %w", err)
		}
		a.logger.Info(
			"provider ready",
			"preset", profile.Preset,
			"backend_family", profile.BackendFamily,
			"auth_method", profile.AuthMethod,
			"model", profile.Model,
			"base_url", profile.BaseURL,
			"auth_source", providerLogAuthSource(profile),
		)
		ctrl := controller.New(agent, observer, observer, controller.SessionContext{
			SessionName:          workspace.SessionName,
			BottomPaneID:         workspace.BottomPane.ID,
			TrackedShell:         controller.TrackedShellTarget{SessionName: workspace.SessionName, PaneID: workspace.TopPane.ID},
			WorkingDirectory:     runtimeCfg.StartDir,
			LocalWorkspaceRoot:   runtimeCfg.StartDir,
			StateDir:             runtimeCfg.StateDir,
			UserShellHistoryFile: historyFile,
			CurrentShell:         initialShellContextPtr(initialShellContext),
		})
		ctrl.SetRuntime(buildConfiguredRuntime(runtimeCfg, profile))
		agentCtx, cancel := context.WithTimeout(ctx, agentPromptTimeout)
		defer cancel()

		events, err := ctrl.SubmitAgentPrompt(agentCtx, a.cfg.AgentPrompt)
		if err != nil {
			logging.TraceError("app.agent_prompt.error", err, "provider", profile.Preset)
			return Result{}, fmt.Errorf("submit agent prompt: %w", err)
		}

		logging.Trace("app.agent_prompt.complete", "event_count", len(events))

		result.AgentEvents = events
		return result, nil
	}

	if a.cfg.TUI {
		observer := shell.NewObserver(client).WithStateDir(a.cfg.RuntimeDir).WithSessionName(workspace.SessionName).WithStartDir(runtimeCfg.StartDir).WithSessionEnsurer(func(ctx context.Context) error {
			return ensureWorkspace(ctx, client, runtimeCfg.StartDir)
		})
		if err := client.BindNoPrefixKey(ctx, tui.TakeControlKey, "detach-client"); err != nil {
			logging.TraceError("app.bind_take_control.error", err, "key", tui.TakeControlKey)
			return Result{}, fmt.Errorf("configure take-control key: %w", err)
		}
		if err := client.BindNoPrefixKey(ctx, tui.ExecutionTakeControlKey, "detach-client"); err != nil {
			logging.TraceError("app.bind_take_control.error", err, "key", tui.ExecutionTakeControlKey)
			return Result{}, fmt.Errorf("configure execution take-control key: %w", err)
		}
		initialShellContext := initialPromptContext(ctx, observer, workspace.TopPane.ID, runtimeCfg.StartDir)
		observer.WithPromptHint(initialShellContext)
		if err := observer.EnsureLocalShellIntegration(ctx, workspace.TopPane.ID); err != nil {
			logging.TraceError("app.shell_integration.error", err, "pane", workspace.TopPane.ID)
		}
		agent, profile, err := provider.NewFromConfig(runtimeCfg, provider.FactoryOptions{})
		if err != nil {
			logging.TraceError("app.provider.error", err, "provider", a.cfg.ProviderType)
			return Result{}, fmt.Errorf("configure provider: %w", err)
		}
		a.logger.Info(
			"provider ready",
			"preset", profile.Preset,
			"backend_family", profile.BackendFamily,
			"auth_method", profile.AuthMethod,
			"model", profile.Model,
			"base_url", profile.BaseURL,
			"auth_source", providerLogAuthSource(profile),
		)
		ctrl := controller.New(agent, observer, observer, controller.SessionContext{
			SessionName:          workspace.SessionName,
			BottomPaneID:         workspace.BottomPane.ID,
			TrackedShell:         controller.TrackedShellTarget{SessionName: workspace.SessionName, PaneID: workspace.TopPane.ID},
			WorkingDirectory:     runtimeCfg.StartDir,
			LocalWorkspaceRoot:   runtimeCfg.StartDir,
			StateDir:             runtimeCfg.StateDir,
			UserShellHistoryFile: historyFile,
			CurrentShell:         initialShellContextPtr(initialShellContext),
		})
		ctrl.SetRuntime(buildConfiguredRuntime(runtimeCfg, profile))
		switchProvider := func(profile provider.Profile, shellContext *shell.PromptContext) (controller.Controller, provider.Profile, error) {
			agent, err := provider.NewFromProfile(profile, provider.FactoryOptions{})
			if err != nil {
				return nil, provider.Profile{}, err
			}

			ctrl := controller.New(agent, observer, observer, controller.SessionContext{
				SessionName:          workspace.SessionName,
				BottomPaneID:         workspace.BottomPane.ID,
				TrackedShell:         controller.TrackedShellTarget{SessionName: workspace.SessionName, PaneID: workspace.TopPane.ID},
				WorkingDirectory:     runtimeCfg.StartDir,
				LocalWorkspaceRoot:   runtimeCfg.StartDir,
				StateDir:             runtimeCfg.StateDir,
				UserShellHistoryFile: historyFile,
				CurrentShell:         shellContext,
			})
			ctrl.SetRuntime(buildConfiguredRuntime(runtimeCfg, profile))
			return ctrl, profile, nil
		}
		model := tui.NewModel(workspace, ctrl).
			WithShellContext(initialShellContext).
			WithTakeControl(socketTarget, workspace.SessionName, workspace.TopPane.ID, tui.TakeControlKey).
			WithTakeControlStartDir(runtimeCfg.StartDir).
			WithProviderOnboarding(profile, func() ([]provider.OnboardingCandidate, error) {
				return provider.BuildOnboardingCandidates(runtimeCfg.StateDir)
			}, func(profile provider.Profile) ([]provider.ModelOption, error) {
				return provider.ListModels(profile, nil)
			}, switchProvider, func(profile provider.Profile) error {
				return provider.SaveStoredProviderConfigWithOptions(runtimeCfg.StateDir, profile, provider.SecretStoreOptions{
					AllowPlaintextFallback: a.cfg.AllowPlaintextProviderSecrets,
				})
			}).
			WithProviderTester(func(profile provider.Profile) error {
				return provider.CheckHealth(ctx, profile, provider.FactoryOptions{})
			})
		program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
		_, runErr := program.Run()
		cleanupErr := cleanupTUISession(a.cfg, created, client, workspace.SessionName)
		if runErr != nil {
			logging.TraceError("app.tui.error", runErr, "session", workspace.SessionName)
			if cleanupErr != nil {
				return Result{}, fmt.Errorf("run tui: %w (cleanup error: %v)", runErr, cleanupErr)
			}

			return Result{}, fmt.Errorf("run tui: %w", runErr)
		}

		if cleanupErr != nil {
			logging.TraceError("app.tui.cleanup_error", cleanupErr, "session", workspace.SessionName)
			return Result{}, fmt.Errorf("cleanup tui session: %w", cleanupErr)
		}

		logging.Trace("app.tui.complete", "session", workspace.SessionName)
		result.Interactive = true
		return result, nil
	}

	if a.cfg.Inject != "" {
		if err := client.SendKeys(ctx, workspace.TopPane.ID, a.cfg.Inject, a.cfg.InjectEnter); err != nil {
			logging.TraceError("app.inject.error", err, "pane", workspace.TopPane.ID, "command", a.cfg.Inject)
			return Result{}, fmt.Errorf("inject command: %w", err)
		}

		a.logger.Info("command injected", "pane", workspace.TopPane.ID, "command", a.cfg.Inject)
		result.InjectedCommand = a.cfg.Inject
	}

	if a.cfg.Track != "" {
		observer := shell.NewObserver(client).WithStateDir(a.cfg.RuntimeDir).WithSessionName(workspace.SessionName).WithStartDir(runtimeCfg.StartDir).WithSessionEnsurer(func(ctx context.Context) error {
			return ensureWorkspace(ctx, client, runtimeCfg.StartDir)
		})
		observer.WithPromptHint(shell.GuessLocalContext(a.cfg.StartDir))
		if err := observer.EnsureLocalShellIntegration(ctx, workspace.TopPane.ID); err != nil {
			logging.TraceError("app.shell_integration.error", err, "pane", workspace.TopPane.ID)
		}
		tracked, err := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, a.cfg.Track, shell.CommandTimeout(a.cfg.Track))
		if err != nil {
			logging.TraceError("app.track.error", err, "pane", workspace.TopPane.ID, "command", a.cfg.Track)
			return Result{}, fmt.Errorf("track command: %w", err)
		}

		a.logger.Info(
			"tracked command complete",
			"pane", workspace.TopPane.ID,
			"command_id", tracked.CommandID,
			"exit_code", tracked.ExitCode,
		)
		logging.Trace(
			"app.track.complete",
			"pane", workspace.TopPane.ID,
			"command_id", tracked.CommandID,
			"exit_code", tracked.ExitCode,
			"captured_preview", logging.Preview(tracked.Captured, 1000),
		)
		result.Tracked = &tracked
	}

	return result, nil
}

func providerLogAuthSource(profile provider.Profile) string {
	switch strings.TrimSpace(profile.APIKeyEnvVar) {
	case "":
		if strings.TrimSpace(profile.APIKey) != "" {
			return "session_only"
		}
	case "os_keyring":
		return "os_keyring"
	case "local_file":
		return "local_file"
	default:
		return "env_ref"
	}

	if profile.AuthMethod == provider.AuthNone {
		return "none"
	}
	return string(profile.AuthMethod)
}

func buildConfiguredRuntime(cfg config.Config, profile provider.Profile) agentruntime.Runtime {
	return agentruntime.WrapRuntime(agentruntime.NewBuiltin(), agentruntime.RuntimeMetadata{
		Type:           cfg.RuntimeType,
		Command:        cfg.RuntimeCommand,
		ProviderPreset: string(profile.Preset),
		Model:          strings.TrimSpace(profile.Model),
	})
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

func cleanupTUISession(cfg config.Config, created bool, client *tmux.Client, sessionName string) error {
	if !shouldDestroyManagedSession(cfg, created, sessionName) {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return client.KillSession(ctx, sessionName)
}

func shouldDestroyManagedSession(cfg config.Config, created bool, sessionName string) bool {
	if created {
		return true
	}
	sessionName = strings.TrimSpace(sessionName)
	if sessionName == "" {
		return false
	}
	managedSessionName := managedSessionNameForConfig(cfg)
	managedSocketPath := managedSocketPathForConfig(cfg)
	if managedSessionName == "" || managedSocketPath == "" {
		return false
	}
	return sessionName == managedSessionName && tmux.ResolveSocketTarget(cfg.TmuxSocket) == managedSocketPath
}

func managedSessionNameForConfig(cfg config.Config) string {
	workspaceID := strings.TrimSpace(cfg.WorkspaceID)
	if workspaceID == "" {
		return ""
	}
	return "shuttle_" + workspaceID
}

func managedSocketPathForConfig(cfg config.Config) string {
	runtimeDir := strings.TrimSpace(cfg.RuntimeDir)
	if runtimeDir == "" {
		return ""
	}
	return filepath.Join(runtimeDir, "tmux.sock")
}

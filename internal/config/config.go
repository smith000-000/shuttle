package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultSessionName = "shuttle"
	defaultStateDir    = ".shuttle"
	defaultLogName     = "shuttle.log"
)

type Config struct {
	SessionName          string
	StartDir             string
	TmuxSocket           string
	StateDir             string
	LogPath              string
	AgentPrompt          string
	Inject               string
	Track                string
	TUI                  bool
	InjectEnter          bool
	ProviderType         string
	ProviderAuthMethod   string
	ProviderModel        string
	ProviderBaseURL      string
	ProviderAPIKey       string
	ProviderAPIKeyEnvVar string
	ProviderCLICommand   string
	ProviderFlagsSet     bool
}

func Parse(args []string) (Config, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return Config{}, err
	}

	stateDir := envOrDefault("SHUTTLE_STATE_DIR", filepath.Join(workingDir, defaultStateDir))
	sessionName := envOrDefault("SHUTTLE_SESSION", defaultSessionName)
	socketName := os.Getenv("SHUTTLE_TMUX_SOCKET")
	providerType := envOrDefault("SHUTTLE_PROVIDER", "mock")
	providerAuthMethod := envOrDefault("SHUTTLE_AUTH", "auto")
	providerModel := os.Getenv("SHUTTLE_MODEL")
	providerBaseURL := os.Getenv("SHUTTLE_BASE_URL")
	providerCLICommand := os.Getenv("SHUTTLE_CLI_COMMAND")

	fs := flag.NewFlagSet("shuttle", flag.ContinueOnError)

	cfg := Config{}
	fs.StringVar(&cfg.SessionName, "session", sessionName, "tmux session name")
	fs.StringVar(&cfg.StartDir, "dir", workingDir, "working directory for new panes")
	fs.StringVar(&cfg.TmuxSocket, "socket", socketName, "tmux socket name for an isolated server")
	fs.StringVar(&cfg.StateDir, "state-dir", stateDir, "state directory for logs and future local state")
	fs.StringVar(&cfg.AgentPrompt, "agent", "", "submit a single agent prompt and print the structured response")
	fs.StringVar(&cfg.Inject, "inject", "", "inject a shell command into the top pane after bootstrap")
	fs.StringVar(&cfg.Track, "track", "", "inject a tracked shell command into the top pane and wait for its result")
	fs.BoolVar(&cfg.TUI, "tui", false, "run the minimal interactive TUI shell")
	fs.BoolVar(&cfg.InjectEnter, "enter", true, "append Enter when injecting a command")
	fs.StringVar(&cfg.ProviderType, "provider", providerType, "agent provider to use: mock, openai, openrouter, anthropic, ollama, codex_cli, or custom")
	fs.StringVar(&cfg.ProviderAuthMethod, "auth", providerAuthMethod, "auth method for the selected provider: auto, api_key, codex_login, or none")
	fs.StringVar(&cfg.ProviderModel, "model", providerModel, "model name for the selected provider")
	fs.StringVar(&cfg.ProviderBaseURL, "base-url", providerBaseURL, "base URL for the selected provider API")
	fs.StringVar(&cfg.ProviderCLICommand, "cli-command", providerCLICommand, "CLI command path for CLI-backed providers")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	fs.Visit(func(flag *flag.Flag) {
		switch flag.Name {
		case "provider", "auth", "model", "base-url", "cli-command":
			cfg.ProviderFlagsSet = true
		}
	})

	if cfg.SessionName == "" {
		return Config{}, errors.New("session name must not be empty")
	}

	startDir, err := filepath.Abs(cfg.StartDir)
	if err != nil {
		return Config{}, err
	}
	cfg.StartDir = startDir

	stateDirAbs, err := filepath.Abs(cfg.StateDir)
	if err != nil {
		return Config{}, err
	}
	cfg.StateDir = stateDirAbs
	cfg.LogPath = filepath.Join(cfg.StateDir, defaultLogName)
	cfg.ProviderType = normalizeProviderType(cfg.ProviderType)
	authMethod, err := normalizeProviderAuthMethod(cfg.ProviderAuthMethod)
	if err != nil {
		return Config{}, err
	}
	cfg.ProviderAuthMethod = authMethod
	cfg.ProviderAPIKey, cfg.ProviderAPIKeyEnvVar = resolveProviderAPIKey(cfg.ProviderType, cfg.ProviderAuthMethod)

	return cfg, nil
}

func envOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""
}

func normalizeProviderType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "mock"
	case "openai-responses":
		return "openai"
	case "anthropic":
		return "anthropic"
	case "ollama":
		return "ollama"
	case "codex-cli":
		return "codex_cli"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeProviderAuthMethod(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return "auto", nil
	case "api_key":
		return "api_key", nil
	case "codex_login":
		return "codex_login", nil
	case "none":
		return "none", nil
	default:
		return "", errors.New("auth method must be one of: auto, api_key, codex_login, none")
	}
}

func resolveProviderAPIKey(providerType string, authMethod string) (string, string) {
	if authMethod == "none" {
		return "", ""
	}

	switch normalizeProviderType(providerType) {
	case "ollama", "codex_cli":
		return "", ""
	case "anthropic":
		return firstNonEmptyEnv(
			"SHUTTLE_API_KEY",
			"ANTHROPIC_API_KEY",
		)
	case "openrouter":
		return firstNonEmptyEnv(
			"SHUTTLE_API_KEY",
			"OPENROUTER_API_KEY",
		)
	case "openai", "custom":
		return firstNonEmptyEnv(
			"SHUTTLE_API_KEY",
			"OPENAI_API_KEY",
		)
	default:
		return firstNonEmptyEnv("SHUTTLE_API_KEY")
	}
}

func firstNonEmptyEnv(keys ...string) (string, string) {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value, key
		}
	}

	return "", ""
}

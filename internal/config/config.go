package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultLogName    = "shuttle.log"
	defaultTraceName  = "trace.log"
	managedSocketName = "tmux.sock"
	workspaceIDLength = 12
)

type TraceMode string

const (
	TraceModeOff       TraceMode = "off"
	TraceModeSafe      TraceMode = "safe"
	TraceModeSensitive TraceMode = "sensitive"
)

type Config struct {
	Version                       bool
	WorkspaceID                   string
	SessionName                   string
	StartDir                      string
	TmuxSocket                    string
	StateDir                      string
	RuntimeDir                    string
	LogPath                       string
	Trace                         bool
	TraceMode                     TraceMode
	TraceConsent                  bool
	TracePath                     string
	AgentPrompt                   string
	Inject                        string
	Track                         string
	TUI                           bool
	InjectEnter                   bool
	ProviderType                  string
	ProviderAuthMethod            string
	ProviderModel                 string
	ProviderBaseURL               string
	ProviderThinking              string
	ProviderReasoningEffort       string
	ProviderAPIKey                string
	ProviderAPIKeyEnvVar          string
	ProviderCLICommand            string
	RuntimeType                   string
	RuntimeCommand                string
	AllowPlaintextProviderSecrets bool
	ProviderFlagsSet              bool
}

func Parse(args []string) (Config, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return Config{}, err
	}

	stateDir, err := defaultStateDir()
	if err != nil {
		return Config{}, err
	}
	if override := os.Getenv("SHUTTLE_STATE_DIR"); override != "" {
		stateDir = override
	}
	runtimeDir, err := defaultRuntimeDir()
	if err != nil {
		return Config{}, err
	}
	if override := os.Getenv("SHUTTLE_RUNTIME_DIR"); override != "" {
		runtimeDir = override
	}
	sessionName := strings.TrimSpace(os.Getenv("SHUTTLE_SESSION"))
	socketName := strings.TrimSpace(os.Getenv("SHUTTLE_TMUX_SOCKET"))
	providerType := envOrDefault("SHUTTLE_PROVIDER", "mock")
	providerAuthMethod := envOrDefault("SHUTTLE_AUTH", "auto")
	providerModel := os.Getenv("SHUTTLE_MODEL")
	providerBaseURL := os.Getenv("SHUTTLE_BASE_URL")
	providerThinking := os.Getenv("SHUTTLE_THINKING")
	providerReasoningEffort := os.Getenv("SHUTTLE_REASONING_EFFORT")
	runtimeType := envOrDefault("SHUTTLE_RUNTIME", "builtin")
	runtimeCommand := os.Getenv("SHUTTLE_RUNTIME_COMMAND")
	traceMode, err := resolveTraceMode(os.Getenv("SHUTTLE_TRACE"), os.Getenv("SHUTTLE_TRACE_MODE"))
	if err != nil {
		return Config{}, err
	}
	tracePath := os.Getenv("SHUTTLE_TRACE_PATH")
	traceConsent := envBool("SHUTTLE_TRACE_CONSENT")
	providerCLICommand := os.Getenv("SHUTTLE_CLI_COMMAND")
	allowPlaintextProviderSecrets := envBool("SHUTTLE_ALLOW_PLAINTEXT_PROVIDER_SECRETS")

	fs := flag.NewFlagSet("shuttle", flag.ContinueOnError)

	cfg := Config{}
	fs.BoolVar(&cfg.Version, "version", false, "print Shuttle version information and exit")
	fs.StringVar(&cfg.SessionName, "session", sessionName, "tmux session name")
	fs.StringVar(&cfg.StartDir, "dir", workingDir, "working directory for new panes")
	fs.StringVar(&cfg.TmuxSocket, "socket", socketName, "tmux socket name for an isolated server")
	fs.StringVar(&cfg.StateDir, "state-dir", stateDir, "state directory for logs and future local state")
	fs.StringVar(&cfg.RuntimeDir, "runtime-dir", runtimeDir, "runtime directory for staged shell scripts and semantic shell state")
	fs.BoolVar(&cfg.Trace, "trace", traceMode != TraceModeOff, "enable safe execution tracing")
	fs.StringVar((*string)(&cfg.TraceMode), "trace-mode", string(traceMode), "trace mode: off, safe, or sensitive")
	fs.BoolVar(&cfg.TraceConsent, "trace-consent", traceConsent, "acknowledge that sensitive trace may capture secrets")
	fs.StringVar(&cfg.TracePath, "trace-path", tracePath, "path for verbose trace output")
	fs.StringVar(&cfg.AgentPrompt, "agent", "", "submit a single agent prompt and print the structured response")
	fs.StringVar(&cfg.Inject, "inject", "", "inject a shell command into the top pane after bootstrap")
	fs.StringVar(&cfg.Track, "track", "", "inject a tracked shell command into the top pane and wait for its result")
	fs.BoolVar(&cfg.TUI, "tui", false, "run the minimal interactive TUI shell")
	fs.BoolVar(&cfg.InjectEnter, "enter", true, "append Enter when injecting a command")
	fs.StringVar(&cfg.ProviderType, "provider", providerType, "inference provider to use: mock, openai, openrouter, openwebui, anthropic, ollama, codex_cli, or custom")
	fs.StringVar(&cfg.ProviderAuthMethod, "auth", providerAuthMethod, "auth method for the selected provider: auto, api_key, codex_login, inherited_env, or none")
	fs.StringVar(&cfg.ProviderModel, "model", providerModel, "model name for the selected provider")
	fs.StringVar(&cfg.ProviderBaseURL, "base-url", providerBaseURL, "base URL for the selected provider API")
	fs.StringVar(&cfg.ProviderThinking, "thinking", providerThinking, "thinking mode for supported providers: on or off")
	fs.StringVar(&cfg.ProviderReasoningEffort, "reasoning-effort", providerReasoningEffort, "reasoning effort for supported providers: low, medium, high, or xhigh")
	fs.StringVar(&cfg.ProviderCLICommand, "cli-command", providerCLICommand, "CLI command path for CLI-backed providers")
	fs.StringVar(&cfg.RuntimeType, "runtime", runtimeType, "coding runtime to use: builtin, pi, codex_sdk, or auto")
	fs.StringVar(&cfg.RuntimeCommand, "runtime-command", runtimeCommand, "command path for selected coding runtime")
	fs.BoolVar(&cfg.AllowPlaintextProviderSecrets, "allow-plaintext-provider-secrets", allowPlaintextProviderSecrets, "allow less-secure local plaintext fallback for provider secrets when OS keyring is unavailable")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	fs.Visit(func(flag *flag.Flag) {
		switch flag.Name {
		case "provider", "auth", "model", "base-url", "thinking", "reasoning-effort", "cli-command", "runtime", "runtime-command":
			cfg.ProviderFlagsSet = true
		}
	})

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
	runtimeDirAbs, err := filepath.Abs(cfg.RuntimeDir)
	if err != nil {
		return Config{}, err
	}
	cfg.RuntimeDir = runtimeDirAbs
	cfg.WorkspaceID = workspaceIDForPath(cfg.StartDir)
	if strings.TrimSpace(cfg.SessionName) == "" {
		cfg.SessionName = managedSessionName(cfg.WorkspaceID)
	}
	cfg.SessionName = normalizeSessionName(cfg.SessionName)
	if strings.TrimSpace(cfg.TmuxSocket) == "" {
		cfg.TmuxSocket = managedSocketPath(cfg.RuntimeDir)
	}
	cfg.LogPath = filepath.Join(cfg.StateDir, defaultLogName)
	if strings.TrimSpace(cfg.TracePath) == "" {
		cfg.TracePath = filepath.Join(cfg.StateDir, defaultTraceName)
	} else {
		tracePathAbs, err := filepath.Abs(cfg.TracePath)
		if err != nil {
			return Config{}, err
		}
		cfg.TracePath = tracePathAbs
	}
	if cfg.TraceMode != TraceModeOff {
		cfg.Trace = true
	}
	if cfg.Trace {
		if strings.TrimSpace(string(cfg.TraceMode)) == "" || cfg.TraceMode == TraceModeOff {
			cfg.TraceMode = TraceModeSafe
		}
	} else {
		cfg.TraceMode = TraceModeOff
	}
	if !isValidTraceMode(cfg.TraceMode) {
		return Config{}, errors.New("trace mode must be one of: off, safe, sensitive")
	}
	cfg.ProviderType = normalizeProviderType(cfg.ProviderType)
	cfg.RuntimeType = normalizeRuntimeType(cfg.RuntimeType)
	authMethod, err := normalizeProviderAuthMethod(cfg.ProviderAuthMethod)
	if err != nil {
		return Config{}, err
	}
	cfg.ProviderAuthMethod = authMethod
	cfg.ProviderAPIKey, cfg.ProviderAPIKeyEnvVar = resolveProviderAPIKey(cfg.ProviderType, cfg.ProviderAuthMethod)

	return cfg, nil
}

func workspaceIDForPath(path string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	encoded := hex.EncodeToString(sum[:])
	if len(encoded) > workspaceIDLength {
		return encoded[:workspaceIDLength]
	}
	return encoded
}

func managedSessionName(workspaceID string) string {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return "shuttle"
	}
	return "shuttle_" + workspaceID
}

func normalizeSessionName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return strings.ReplaceAll(name, ":", "_")
}

func managedSocketPath(runtimeDir string) string {
	return filepath.Join(runtimeDir, managedSocketName)
}

func envOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}

func envBool(key string) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return false
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}

	return parsed
}

func defaultStateDir() (string, error) {
	if value := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); value != "" {
		return filepath.Join(value, "shuttle"), nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".local", "state", "shuttle"), nil
}

func defaultRuntimeDir() (string, error) {
	if value := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); value != "" {
		return filepath.Join(value, "shuttle"), nil
	}
	stateDir, err := defaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "runtime"), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""
}

func resolveTraceMode(traceValue string, traceModeValue string) (TraceMode, error) {
	if mode := strings.TrimSpace(traceModeValue); mode != "" {
		resolved := TraceMode(strings.ToLower(mode))
		if !isValidTraceMode(resolved) {
			return TraceModeOff, fmt.Errorf("trace mode must be one of: off, safe, sensitive")
		}
		return resolved, nil
	}

	value := strings.TrimSpace(strings.ToLower(traceValue))
	switch value {
	case "", "0", "false", "off", "none":
		return TraceModeOff, nil
	case "1", "true", "safe":
		return TraceModeSafe, nil
	case "sensitive":
		return TraceModeSensitive, nil
	default:
		return TraceModeOff, fmt.Errorf("SHUTTLE_TRACE must be boolean, off, safe, or sensitive")
	}
}

func isValidTraceMode(mode TraceMode) bool {
	switch mode {
	case TraceModeOff, TraceModeSafe, TraceModeSensitive:
		return true
	default:
		return false
	}
}

func normalizeProviderType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "mock"
	case "openai-responses":
		return "openai"
	case "anthropic":
		return "anthropic"
	case "openwebui":
		return "openwebui"
	case "ollama":
		return "ollama"
	case "codex-cli":
		return "codex_cli"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeRuntimeType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "builtin":
		return "builtin"
	case "auto":
		return "auto"
	case "codex-sdk":
		return "codex_sdk"
	case "pi-runtime":
		return "pi"
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
	case "inherited_env":
		return "inherited_env", nil
	case "none":
		return "none", nil
	default:
		return "", errors.New("auth method must be one of: auto, api_key, codex_login, inherited_env, none")
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
	case "openwebui":
		return firstNonEmptyEnv(
			"SHUTTLE_API_KEY",
			"OPENWEBUI_API_KEY",
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

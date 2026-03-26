package config

import (
	"path/filepath"
	"testing"
)

func TestParseDerivesManagedWorkspaceDefaults(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(tempDir, "state"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(tempDir, "runtime"))

	cfg, err := Parse([]string{"--dir", tempDir})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	expectedWorkspaceID := workspaceIDForPath(tempDir)
	if cfg.WorkspaceID != expectedWorkspaceID {
		t.Fatalf("expected workspace ID %q, got %q", expectedWorkspaceID, cfg.WorkspaceID)
	}
	if cfg.SessionName != managedSessionName(expectedWorkspaceID) {
		t.Fatalf("expected derived session name %q, got %q", managedSessionName(expectedWorkspaceID), cfg.SessionName)
	}
	expectedSocket := filepath.Join(filepath.Join(tempDir, "runtime"), "shuttle", "tmux.sock")
	if cfg.TmuxSocket != expectedSocket {
		t.Fatalf("expected derived socket path %q, got %q", expectedSocket, cfg.TmuxSocket)
	}
}

func TestParsePreservesExplicitTmuxOverrides(t *testing.T) {
	cfg, err := Parse([]string{"--session", "custom-session", "--socket", "custom-socket"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.SessionName != "custom-session" {
		t.Fatalf("expected explicit session override, got %q", cfg.SessionName)
	}
	if cfg.TmuxSocket != "custom-socket" {
		t.Fatalf("expected explicit socket override, got %q", cfg.TmuxSocket)
	}
}

func TestParseNormalizesColonSessionNamesForTmux(t *testing.T) {
	cfg, err := Parse([]string{"--session", "shuttle:custom"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.SessionName != "shuttle_custom" {
		t.Fatalf("expected normalized session name %q, got %q", "shuttle_custom", cfg.SessionName)
	}
}

func TestParsePreservesEnvTmuxOverrides(t *testing.T) {
	t.Setenv("SHUTTLE_SESSION", "env-session")
	t.Setenv("SHUTTLE_TMUX_SOCKET", "env-socket")

	cfg, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.SessionName != "env-session" {
		t.Fatalf("expected env session override, got %q", cfg.SessionName)
	}
	if cfg.TmuxSocket != "env-socket" {
		t.Fatalf("expected env socket override, got %q", cfg.TmuxSocket)
	}
}

func TestParseResolvesOpenAIAPIKeyByProvider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("OPENROUTER_API_KEY", "openrouter-key")

	cfg, err := Parse([]string{"--provider", "openai"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.ProviderAPIKey != "openai-key" {
		t.Fatalf("expected OpenAI API key, got %q", cfg.ProviderAPIKey)
	}
	if cfg.ProviderAPIKeyEnvVar != "OPENAI_API_KEY" {
		t.Fatalf("expected OPENAI_API_KEY source, got %q", cfg.ProviderAPIKeyEnvVar)
	}
}

func TestParseResolvesOpenRouterAPIKeyByProvider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("OPENROUTER_API_KEY", "openrouter-key")

	cfg, err := Parse([]string{"--provider", "openrouter"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.ProviderAPIKey != "openrouter-key" {
		t.Fatalf("expected OpenRouter API key, got %q", cfg.ProviderAPIKey)
	}
	if cfg.ProviderAPIKeyEnvVar != "OPENROUTER_API_KEY" {
		t.Fatalf("expected OPENROUTER_API_KEY source, got %q", cfg.ProviderAPIKeyEnvVar)
	}
}

func TestParseResolvesAnthropicAPIKeyByProvider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-key")

	cfg, err := Parse([]string{"--provider", "anthropic"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.ProviderAPIKey != "anthropic-key" {
		t.Fatalf("expected Anthropic API key, got %q", cfg.ProviderAPIKey)
	}
	if cfg.ProviderAPIKeyEnvVar != "ANTHROPIC_API_KEY" {
		t.Fatalf("expected ANTHROPIC_API_KEY source, got %q", cfg.ProviderAPIKeyEnvVar)
	}
}

func TestParseResolvesOpenWebUIAPIKeyByProvider(t *testing.T) {
	t.Setenv("OPENWEBUI_API_KEY", "openwebui-key")

	cfg, err := Parse([]string{"--provider", "openwebui"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.ProviderAPIKey != "openwebui-key" {
		t.Fatalf("expected OpenWebUI API key, got %q", cfg.ProviderAPIKey)
	}
	if cfg.ProviderAPIKeyEnvVar != "OPENWEBUI_API_KEY" {
		t.Fatalf("expected OPENWEBUI_API_KEY source, got %q", cfg.ProviderAPIKeyEnvVar)
	}
}

func TestParsePrefersShuttleAPIKeyOverride(t *testing.T) {
	t.Setenv("SHUTTLE_API_KEY", "shuttle-key")
	t.Setenv("OPENAI_API_KEY", "openai-key")

	cfg, err := Parse([]string{"--provider", "openai"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.ProviderAPIKey != "shuttle-key" {
		t.Fatalf("expected SHUTTLE_API_KEY override, got %q", cfg.ProviderAPIKey)
	}
	if cfg.ProviderAPIKeyEnvVar != "SHUTTLE_API_KEY" {
		t.Fatalf("expected SHUTTLE_API_KEY source, got %q", cfg.ProviderAPIKeyEnvVar)
	}
}

func TestParseCodexCLIDoesNotResolveAPIKey(t *testing.T) {
	t.Setenv("SHUTTLE_API_KEY", "shuttle-key")
	t.Setenv("OPENAI_API_KEY", "openai-key")

	cfg, err := Parse([]string{"--provider", "codex_cli"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.ProviderAPIKey != "" {
		t.Fatalf("expected no API key for codex_cli, got %q", cfg.ProviderAPIKey)
	}
	if cfg.ProviderAPIKeyEnvVar != "" {
		t.Fatalf("expected no API key source for codex_cli, got %q", cfg.ProviderAPIKeyEnvVar)
	}
}

func TestParseOllamaDoesNotResolveAPIKey(t *testing.T) {
	t.Setenv("SHUTTLE_API_KEY", "shuttle-key")

	cfg, err := Parse([]string{"--provider", "ollama"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.ProviderAPIKey != "" {
		t.Fatalf("expected no API key for ollama, got %q", cfg.ProviderAPIKey)
	}
	if cfg.ProviderAPIKeyEnvVar != "" {
		t.Fatalf("expected no API key source for ollama, got %q", cfg.ProviderAPIKeyEnvVar)
	}
}

func TestParseTracksExplicitProviderFlags(t *testing.T) {
	cfg, err := Parse([]string{"--provider", "openrouter", "--model", "openrouter/auto"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if !cfg.ProviderFlagsSet {
		t.Fatal("expected provider flags to be marked as explicit")
	}
}

func TestParseTracksExplicitCLICommandFlag(t *testing.T) {
	cfg, err := Parse([]string{"--cli-command", "/usr/local/bin/codex"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if !cfg.ProviderFlagsSet {
		t.Fatal("expected cli-command to mark provider flags as explicit")
	}
	if cfg.ProviderCLICommand != "/usr/local/bin/codex" {
		t.Fatalf("expected cli command to parse, got %q", cfg.ProviderCLICommand)
	}
}

func TestParseEnablesTraceFromEnv(t *testing.T) {
	t.Setenv("SHUTTLE_TRACE", "true")

	cfg, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if !cfg.Trace {
		t.Fatalf("expected trace to be enabled from env")
	}
	if cfg.TraceMode != TraceModeSafe {
		t.Fatalf("expected safe trace mode, got %q", cfg.TraceMode)
	}

	expected := filepath.Join(cfg.StateDir, defaultTraceName)
	if cfg.TracePath != expected {
		t.Fatalf("expected default trace path %q, got %q", expected, cfg.TracePath)
	}
}

func TestParseResolvesCustomTracePath(t *testing.T) {
	cfg, err := Parse([]string{"--trace", "--trace-path", "./tmp/trace.out"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if !cfg.Trace {
		t.Fatalf("expected trace flag to be enabled")
	}
	if !filepath.IsAbs(cfg.TracePath) {
		t.Fatalf("expected absolute trace path, got %q", cfg.TracePath)
	}
}

func TestParseDefaultsStateDirOutsideRepo(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/shuttle-state-home")

	cfg, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	expected := filepath.Join("/tmp/shuttle-state-home", "shuttle")
	if cfg.StateDir != expected {
		t.Fatalf("expected state dir %q, got %q", expected, cfg.StateDir)
	}
}

func TestParseDefaultsRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/tmp/shuttle-runtime-home")

	cfg, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	expected := filepath.Join("/tmp/shuttle-runtime-home", "shuttle")
	if cfg.RuntimeDir != expected {
		t.Fatalf("expected runtime dir %q, got %q", expected, cfg.RuntimeDir)
	}
}

func TestParseAcceptsSensitiveTraceMode(t *testing.T) {
	cfg, err := Parse([]string{"--trace-mode", "sensitive", "--trace-consent"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.TraceMode != TraceModeSensitive {
		t.Fatalf("expected sensitive trace mode, got %q", cfg.TraceMode)
	}
	if !cfg.Trace {
		t.Fatalf("expected trace to be enabled for sensitive mode")
	}
	if !cfg.TraceConsent {
		t.Fatalf("expected trace consent to be set")
	}
}

func TestParseAllowsPlaintextProviderSecretsFromEnv(t *testing.T) {
	t.Setenv("SHUTTLE_ALLOW_PLAINTEXT_PROVIDER_SECRETS", "true")

	cfg, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if !cfg.AllowPlaintextProviderSecrets {
		t.Fatalf("expected plaintext provider secret fallback to be enabled from env")
	}
}

func TestParseAllowsPlaintextProviderSecretsFromFlag(t *testing.T) {
	cfg, err := Parse([]string{"--allow-plaintext-provider-secrets"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if !cfg.AllowPlaintextProviderSecrets {
		t.Fatalf("expected plaintext provider secret fallback to be enabled from flag")
	}
}

func TestParseVersionFlag(t *testing.T) {
	cfg, err := Parse([]string{"--version"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if !cfg.Version {
		t.Fatal("expected version flag to be parsed")
	}
}

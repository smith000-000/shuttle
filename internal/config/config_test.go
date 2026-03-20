package config

import (
	"path/filepath"
	"testing"
)

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

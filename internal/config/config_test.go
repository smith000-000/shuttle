package config

import "testing"

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

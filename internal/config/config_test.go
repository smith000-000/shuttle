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

package provider

import (
	"testing"

	"aiterm/internal/config"
)

func TestResolveProfileOpenAIDefaults(t *testing.T) {
	profile, err := ResolveProfile(config.Config{
		ProviderType:         "openai",
		ProviderAuthMethod:   "api_key",
		ProviderAPIKey:       "test-key",
		ProviderAPIKeyEnvVar: "OPENAI_API_KEY",
	})
	if err != nil {
		t.Fatalf("ResolveProfile() error = %v", err)
	}

	if profile.BackendFamily != BackendResponsesHTTP {
		t.Fatalf("expected responses backend, got %s", profile.BackendFamily)
	}

	if profile.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("expected default OpenAI base URL, got %q", profile.BaseURL)
	}

	if profile.Model != "gpt-5-nano-2025-08-07" {
		t.Fatalf("expected default model gpt-5-nano-2025-08-07, got %q", profile.Model)
	}

	if profile.AuthMethod != AuthAPIKey {
		t.Fatalf("expected API key auth, got %s", profile.AuthMethod)
	}
}

func TestResolveProfileOpenRouterDefaults(t *testing.T) {
	profile, err := ResolveProfile(config.Config{
		ProviderType:         "openrouter",
		ProviderAuthMethod:   "api_key",
		ProviderAPIKey:       "test-key",
		ProviderAPIKeyEnvVar: "OPENROUTER_API_KEY",
	})
	if err != nil {
		t.Fatalf("ResolveProfile() error = %v", err)
	}

	if profile.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("expected default OpenRouter base URL, got %q", profile.BaseURL)
	}
	if profile.BackendFamily != BackendOpenRouter {
		t.Fatalf("expected OpenRouter backend, got %s", profile.BackendFamily)
	}

	if profile.Model != "openai/gpt-5" {
		t.Fatalf("expected default model openai/gpt-5, got %q", profile.Model)
	}

	if profile.APIKeyEnvVar != "OPENROUTER_API_KEY" {
		t.Fatalf("expected OPENROUTER_API_KEY source, got %q", profile.APIKeyEnvVar)
	}
}

func TestResolveProfileAnthropicDefaults(t *testing.T) {
	profile, err := ResolveProfile(config.Config{
		ProviderType:         "anthropic",
		ProviderAuthMethod:   "api_key",
		ProviderAPIKey:       "test-key",
		ProviderAPIKeyEnvVar: "ANTHROPIC_API_KEY",
	})
	if err != nil {
		t.Fatalf("ResolveProfile() error = %v", err)
	}

	if profile.BackendFamily != BackendAnthropic {
		t.Fatalf("expected anthropic backend, got %s", profile.BackendFamily)
	}
	if profile.BaseURL != "https://api.anthropic.com" {
		t.Fatalf("expected default Anthropic base URL, got %q", profile.BaseURL)
	}
	if profile.Model != "claude-sonnet-4-6" {
		t.Fatalf("expected default Anthropic model, got %q", profile.Model)
	}
	if profile.APIKeyEnvVar != "ANTHROPIC_API_KEY" {
		t.Fatalf("expected ANTHROPIC_API_KEY source, got %q", profile.APIKeyEnvVar)
	}
}

func TestResolveProfileOpenWebUIDefaults(t *testing.T) {
	profile, err := ResolveProfile(config.Config{
		ProviderType:         "openwebui",
		ProviderAuthMethod:   "api_key",
		ProviderAPIKey:       "test-key",
		ProviderAPIKeyEnvVar: "OPENWEBUI_API_KEY",
	})
	if err != nil {
		t.Fatalf("ResolveProfile() error = %v", err)
	}

	if profile.BackendFamily != BackendResponsesHTTP {
		t.Fatalf("expected responses backend, got %s", profile.BackendFamily)
	}
	if profile.BaseURL != "http://localhost:3000/api/v1" {
		t.Fatalf("expected default OpenWebUI base URL, got %q", profile.BaseURL)
	}
	if profile.APIKeyEnvVar != "OPENWEBUI_API_KEY" {
		t.Fatalf("expected OPENWEBUI_API_KEY source, got %q", profile.APIKeyEnvVar)
	}
}

func TestResolveProfileCustomDefaults(t *testing.T) {
	profile, err := ResolveProfile(config.Config{
		ProviderType:       "custom",
		ProviderAuthMethod: "none",
		ProviderBaseURL:    "https://example.test/custom/v1",
	})
	if err != nil {
		t.Fatalf("ResolveProfile() error = %v", err)
	}

	if profile.BaseURL != "https://example.test/custom/v1" {
		t.Fatalf("expected custom base URL, got %q", profile.BaseURL)
	}

	if profile.Model != "gpt-5-nano-2025-08-07" {
		t.Fatalf("expected default custom model, got %q", profile.Model)
	}

	if profile.AuthMethod != AuthNone {
		t.Fatalf("expected no auth, got %s", profile.AuthMethod)
	}
}

func TestResolveProfileCodexCLIDefaults(t *testing.T) {
	profile, err := ResolveProfile(config.Config{
		ProviderType: "codex_cli",
	})
	if err != nil {
		t.Fatalf("ResolveProfile() error = %v", err)
	}

	if profile.BackendFamily != BackendCLIAgent {
		t.Fatalf("expected cli backend, got %s", profile.BackendFamily)
	}
	if profile.AuthMethod != AuthCodexLogin {
		t.Fatalf("expected codex login auth, got %s", profile.AuthMethod)
	}
	if profile.CLICommand != defaultCodexCLICommand {
		t.Fatalf("expected default codex command, got %q", profile.CLICommand)
	}
}

func TestResolveProfileCodexCLIIgnoresAPIKeyAuthSelection(t *testing.T) {
	profile, err := ResolveProfile(config.Config{
		ProviderType:       "codex_cli",
		ProviderAuthMethod: "api_key",
	})
	if err != nil {
		t.Fatalf("ResolveProfile() error = %v", err)
	}
	if profile.AuthMethod != AuthCodexLogin {
		t.Fatalf("expected codex login auth, got %s", profile.AuthMethod)
	}
}

func TestResolveProfileOllamaDefaults(t *testing.T) {
	profile, err := ResolveProfile(config.Config{
		ProviderType: "ollama",
	})
	if err != nil {
		t.Fatalf("ResolveProfile() error = %v", err)
	}

	if profile.BackendFamily != BackendOllama {
		t.Fatalf("expected ollama backend, got %s", profile.BackendFamily)
	}
	if profile.BaseURL != "http://localhost:11434/api" {
		t.Fatalf("expected default ollama base URL, got %q", profile.BaseURL)
	}
	if profile.AuthMethod != AuthNone {
		t.Fatalf("expected no auth for ollama, got %s", profile.AuthMethod)
	}
}

func TestResolveProfileOllamaNormalizesHostAndPort(t *testing.T) {
	profile, err := ResolveProfile(config.Config{
		ProviderType:    "ollama",
		ProviderBaseURL: "localhost:22434",
	})
	if err != nil {
		t.Fatalf("ResolveProfile() error = %v", err)
	}

	if profile.BaseURL != "http://localhost:22434/api" {
		t.Fatalf("expected normalized ollama base URL, got %q", profile.BaseURL)
	}
}

func TestResolveProfileCustomRequiresBaseURL(t *testing.T) {
	_, err := ResolveProfile(config.Config{
		ProviderType:       "custom",
		ProviderAuthMethod: "none",
	})
	if err == nil {
		t.Fatal("expected error for custom provider without base URL")
	}
}

func TestNewFromProfileRejectsMissingAPIKey(t *testing.T) {
	_, err := NewFromProfile(Profile{
		BackendFamily: BackendResponsesHTTP,
		Preset:        PresetOpenAI,
		AuthMethod:    AuthAPIKey,
		BaseURL:       "https://api.openai.com/v1",
		Model:         "gpt-5-nano-2025-08-07",
		APIKeyEnvVar:  "OPENAI_API_KEY",
	}, FactoryOptions{})
	if err == nil {
		t.Fatal("expected missing API key error")
	}
}

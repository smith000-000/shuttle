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

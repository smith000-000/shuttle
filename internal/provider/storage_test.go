package provider

import (
	"errors"
	"os"
	"testing"

	"aiterm/internal/config"
)

func TestSaveStoredProviderConfigPrefersEnvReference(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "env-openai-key")

	keyringStore := withTestKeyring(t)
	profile := Profile{
		Preset:       PresetOpenAI,
		AuthMethod:   AuthAPIKey,
		BaseURL:      "https://api.openai.com/v1",
		Model:        "gpt-5-nano-2025-08-07",
		APIKey:       "env-openai-key",
		APIKeyEnvVar: "OPENAI_API_KEY",
	}

	if err := SaveStoredProviderConfig(tempDir, profile); err != nil {
		t.Fatalf("SaveStoredProviderConfig() error = %v", err)
	}

	stored, ok, err := LoadStoredProviderConfig(tempDir)
	if err != nil {
		t.Fatalf("LoadStoredProviderConfig() error = %v", err)
	}
	if !ok {
		t.Fatal("expected stored config")
	}
	if stored.APIKeyRef != "OPENAI_API_KEY" {
		t.Fatalf("expected env ref, got %q", stored.APIKeyRef)
	}
	if len(keyringStore) != 0 {
		t.Fatalf("expected no keyring writes, got %#v", keyringStore)
	}
	if string(mustReadFile(t, selectedProviderPath(tempDir))) != "openai\n" {
		t.Fatalf("expected openai to be selected, got %q", string(mustReadFile(t, selectedProviderPath(tempDir))))
	}
}

func TestSaveStoredProviderConfigStoresManualKeyPerPreset(t *testing.T) {
	tempDir := t.TempDir()
	keyringStore := withTestKeyring(t)

	profile := Profile{
		Preset:     PresetAnthropic,
		AuthMethod: AuthAPIKey,
		BaseURL:    "https://api.anthropic.com",
		Model:      "claude-sonnet-4-6",
		APIKey:     "manual-secret",
	}

	if err := SaveStoredProviderConfig(tempDir, profile); err != nil {
		t.Fatalf("SaveStoredProviderConfig() error = %v", err)
	}

	stored, ok, err := LoadStoredProviderConfig(tempDir)
	if err != nil {
		t.Fatalf("LoadStoredProviderConfig() error = %v", err)
	}
	if !ok {
		t.Fatal("expected stored config")
	}
	if stored.APIKeyRef != keyringSourceLabel {
		t.Fatalf("expected keyring ref, got %q", stored.APIKeyRef)
	}
	if keyringStore[keyringServiceName+"|"+providerKeyAccount(tempDir, PresetAnthropic)] != "manual-secret" {
		t.Fatalf("expected secret in keyring store, got %#v", keyringStore)
	}
}

func TestLoadStoredProviderProfilesReturnsMultipleProfiles(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "openai-key")
	withTestKeyring(t)

	if err := SaveStoredProviderConfig(tempDir, Profile{
		Preset:       PresetOpenAI,
		AuthMethod:   AuthAPIKey,
		BaseURL:      "https://api.openai.com/v1",
		Model:        "gpt-5-nano-2025-08-07",
		APIKey:       "openai-key",
		APIKeyEnvVar: "OPENAI_API_KEY",
	}); err != nil {
		t.Fatalf("SaveStoredProviderConfig(openai) error = %v", err)
	}
	if err := SaveStoredProviderConfig(tempDir, Profile{
		Preset:     PresetOllama,
		AuthMethod: AuthNone,
		BaseURL:    "http://localhost:11434/api",
		Model:      "qwen3.5:35b-a3b",
	}); err != nil {
		t.Fatalf("SaveStoredProviderConfig(ollama) error = %v", err)
	}

	profiles, selected, err := LoadStoredProviderProfiles(tempDir)
	if err != nil {
		t.Fatalf("LoadStoredProviderProfiles() error = %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}
	if selected != PresetOllama {
		t.Fatalf("expected ollama selected, got %q", selected)
	}
	if profiles[0].Preset != PresetOllama {
		t.Fatalf("expected selected preset first, got %s", profiles[0].Preset)
	}
}

func TestApplyStoredProviderConfigLoadsEnvReference(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("OPENROUTER_API_KEY", "env-openrouter-key")
	withTestKeyring(t)

	if err := os.MkdirAll(providersDirPath(tempDir), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(providerConfigPath(tempDir, PresetOpenRouter), []byte(`{
  "version": 1,
  "provider": "openrouter",
  "auth_method": "api_key",
  "base_url": "https://openrouter.ai/api/v1",
  "model": "openrouter/auto",
  "api_key_ref": "OPENROUTER_API_KEY"
}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(selectedProviderPath(tempDir), []byte("openrouter\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(selected) error = %v", err)
	}

	cfg, err := ApplyStoredProviderConfig(config.Config{StateDir: tempDir})
	if err != nil {
		t.Fatalf("ApplyStoredProviderConfig() error = %v", err)
	}

	if cfg.ProviderType != "openrouter" {
		t.Fatalf("expected openrouter, got %q", cfg.ProviderType)
	}
	if cfg.ProviderAPIKey != "env-openrouter-key" {
		t.Fatalf("expected env API key, got %q", cfg.ProviderAPIKey)
	}
	if cfg.ProviderAPIKeyEnvVar != "OPENROUTER_API_KEY" {
		t.Fatalf("expected env ref, got %q", cfg.ProviderAPIKeyEnvVar)
	}
}

func TestApplyStoredProviderConfigLoadsLegacyKeyringValue(t *testing.T) {
	tempDir := t.TempDir()
	keyringStore := withTestKeyring(t)
	keyringStore[keyringServiceName+"|"+legacyProviderKeyAccount(tempDir)] = "stored-secret"

	if err := os.WriteFile(legacyProviderConfigPath(tempDir), []byte(`{
  "version": 1,
  "provider": "anthropic",
  "auth_method": "api_key",
  "base_url": "https://api.anthropic.com",
  "model": "claude-sonnet-4-6",
  "api_key_ref": "os_keyring"
}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := ApplyStoredProviderConfig(config.Config{StateDir: tempDir})
	if err != nil {
		t.Fatalf("ApplyStoredProviderConfig() error = %v", err)
	}

	if cfg.ProviderAPIKey != "stored-secret" {
		t.Fatalf("expected keyring API key, got %q", cfg.ProviderAPIKey)
	}
	if cfg.ProviderAPIKeyEnvVar != keyringSourceLabel {
		t.Fatalf("expected keyring source, got %q", cfg.ProviderAPIKeyEnvVar)
	}
}

func TestApplyStoredProviderConfigSkipsWhenProviderFlagsExplicit(t *testing.T) {
	cfg, err := ApplyStoredProviderConfig(config.Config{
		StateDir:           t.TempDir(),
		ProviderType:       "openai",
		ProviderFlagsSet:   true,
		ProviderModel:      "gpt-5.2-codex",
		ProviderBaseURL:    "https://api.openai.com/v1",
		ProviderAuthMethod: "api_key",
	})
	if err != nil {
		t.Fatalf("ApplyStoredProviderConfig() error = %v", err)
	}

	if cfg.ProviderType != "openai" || cfg.ProviderModel != "gpt-5.2-codex" {
		t.Fatalf("expected explicit config to be preserved, got %#v", cfg)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return data
}

func withTestKeyring(t *testing.T) map[string]string {
	t.Helper()

	store := map[string]string{}
	previousSet := keyringSet
	previousGet := keyringGet
	previousDelete := keyringDelete
	keyringSet = func(service, user, password string) error {
		store[service+"|"+user] = password
		return nil
	}
	keyringGet = func(service, user string) (string, error) {
		value, ok := store[service+"|"+user]
		if !ok {
			return "", errors.New("not found")
		}
		return value, nil
	}
	keyringDelete = func(service, user string) error {
		delete(store, service+"|"+user)
		return nil
	}
	t.Cleanup(func() {
		keyringSet = previousSet
		keyringGet = previousGet
		keyringDelete = previousDelete
	})

	return store
}

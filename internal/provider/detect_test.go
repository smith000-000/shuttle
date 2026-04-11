package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectOnboardingCandidatesFindsSpecificProviderKeys(t *testing.T) {
	withTestCodexCommand(t, "/missing-codex")
	withTestOllamaProbe(t, false)
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("OPENROUTER_API_KEY", "openrouter-key")
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-key")
	t.Setenv("SHUTTLE_MODEL", "gpt-5-nano-2025-08-07")

	candidates, err := DetectOnboardingCandidates()
	if err != nil {
		t.Fatalf("DetectOnboardingCandidates() error = %v", err)
	}

	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}

	if candidates[0].Profile.Preset != PresetOpenAI {
		t.Fatalf("expected first candidate to be OpenAI, got %s", candidates[0].Profile.Preset)
	}

	if candidates[1].Profile.Preset != PresetOpenRouter {
		t.Fatalf("expected second candidate to be OpenRouter, got %s", candidates[1].Profile.Preset)
	}

	if candidates[1].Profile.Model != "openai/gpt-5" {
		t.Fatalf("expected OpenRouter default model, got %q", candidates[1].Profile.Model)
	}

	if candidates[1].AuthSource != "OPENROUTER_API_KEY" {
		t.Fatalf("expected OpenRouter auth source, got %q", candidates[1].AuthSource)
	}
	if candidates[2].Profile.Preset != PresetAnthropic {
		t.Fatalf("expected third candidate to be Anthropic, got %s", candidates[2].Profile.Preset)
	}
	if candidates[2].AuthSource != "ANTHROPIC_API_KEY" {
		t.Fatalf("expected Anthropic auth source, got %q", candidates[2].AuthSource)
	}
}

func TestDetectOnboardingCandidatesFallsBackToShuttleAPIKey(t *testing.T) {
	withTestCodexCommand(t, "/missing-codex")
	withTestOllamaProbe(t, false)
	t.Setenv("SHUTTLE_API_KEY", "shared-key")
	t.Setenv("SHUTTLE_PROVIDER", "openrouter")

	candidates, err := DetectOnboardingCandidates()
	if err != nil {
		t.Fatalf("DetectOnboardingCandidates() error = %v", err)
	}

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	if candidates[0].Profile.Preset != PresetOpenRouter {
		t.Fatalf("expected OpenRouter fallback candidate, got %s", candidates[0].Profile.Preset)
	}

	if candidates[0].AuthSource != "SHUTTLE_API_KEY" {
		t.Fatalf("expected SHUTTLE_API_KEY auth source, got %q", candidates[0].AuthSource)
	}
}

func TestDetectOnboardingCandidatesIncludesCustomEndpoint(t *testing.T) {
	withTestCodexCommand(t, "/missing-codex")
	withTestOllamaProbe(t, false)
	t.Setenv("SHUTTLE_BASE_URL", "https://example.test/custom/v1")
	t.Setenv("SHUTTLE_MODEL", "custom-model")

	candidates, err := DetectOnboardingCandidates()
	if err != nil {
		t.Fatalf("DetectOnboardingCandidates() error = %v", err)
	}

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	if candidates[0].Profile.Preset != PresetCustom {
		t.Fatalf("expected custom candidate, got %s", candidates[0].Profile.Preset)
	}

	if candidates[0].Profile.BaseURL != "https://example.test/custom/v1" {
		t.Fatalf("expected custom base URL, got %q", candidates[0].Profile.BaseURL)
	}
}

func TestDetectOnboardingCandidatesIncludesCodexCLI(t *testing.T) {
	script := writeFakeCodexCLI(t)
	withTestCodexCommand(t, script)
	withTestOllamaProbe(t, false)
	t.Setenv("FAKE_CODEX_LOGIN_STATUS", "Logged in using ChatGPT")
	t.Setenv("SHUTTLE_MODEL", "gpt-5.2-codex")

	candidates, err := DetectOnboardingCandidates()
	if err != nil {
		t.Fatalf("DetectOnboardingCandidates() error = %v", err)
	}

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Profile.Preset != PresetCodexCLI {
		t.Fatalf("expected codex_cli candidate, got %s", candidates[0].Profile.Preset)
	}
	if candidates[0].AuthSource != "codex login" {
		t.Fatalf("expected codex login auth source, got %q", candidates[0].AuthSource)
	}
	if candidates[0].Profile.Model != "gpt-5.2-codex" {
		t.Fatalf("expected model passthrough, got %q", candidates[0].Profile.Model)
	}
}

func TestDetectOnboardingCandidatesRanksCodexAheadOfAPIKeys(t *testing.T) {
	script := writeFakeCodexCLI(t)
	withTestCodexCommand(t, script)
	withTestOllamaProbe(t, false)
	t.Setenv("FAKE_CODEX_LOGIN_STATUS", "Logged in using ChatGPT")
	t.Setenv("OPENAI_API_KEY", "openai-key")

	candidates, err := DetectOnboardingCandidates()
	if err != nil {
		t.Fatalf("DetectOnboardingCandidates() error = %v", err)
	}
	if len(candidates) < 2 {
		t.Fatalf("expected codex and OpenAI candidates, got %d", len(candidates))
	}
	if candidates[0].Profile.Preset != PresetCodexCLI {
		t.Fatalf("expected Codex CLI candidate first, got %#v", candidates[0])
	}
	if candidates[1].Profile.Preset != PresetOpenAI {
		t.Fatalf("expected OpenAI candidate second, got %#v", candidates[1])
	}
}

func TestDetectOnboardingCandidatesIncludesOllama(t *testing.T) {
	withTestCodexCommand(t, "/missing-codex")
	withTestOllamaProbe(t, true)

	candidates, err := DetectOnboardingCandidates()
	if err != nil {
		t.Fatalf("DetectOnboardingCandidates() error = %v", err)
	}

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Profile.Preset != PresetOllama {
		t.Fatalf("expected ollama candidate, got %s", candidates[0].Profile.Preset)
	}
	if candidates[0].Profile.BaseURL != "http://localhost:11434/api" {
		t.Fatalf("expected default ollama base URL, got %q", candidates[0].Profile.BaseURL)
	}
}

func TestBuildOnboardingCandidatesIncludesStoredAndManualChoices(t *testing.T) {
	withTestCodexCommand(t, "/missing-codex")
	withTestOllamaProbe(t, false)
	withTestKeyring(t)
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "provider.json"), []byte(`{
  "version": 1,
  "provider": "openai",
  "auth_method": "api_key",
  "base_url": "https://api.openai.com/v1",
  "model": "gpt-5-nano-2025-08-07",
  "api_key_ref": "OPENAI_API_KEY"
}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	candidates, err := BuildOnboardingCandidates(stateDir)
	if err != nil {
		t.Fatalf("BuildOnboardingCandidates() error = %v", err)
	}

	if len(candidates) < 6 {
		t.Fatalf("expected stored plus manual candidates, got %d", len(candidates))
	}
	if candidates[0].Source != OnboardingCandidateStored {
		t.Fatalf("expected stored candidates first when nothing is freshly detected, got %#v", candidates[0])
	}

	manualFound := false
	for _, candidate := range candidates {
		if candidate.Manual && candidate.Profile.Preset == PresetAnthropic {
			manualFound = true
			break
		}
	}
	if !manualFound {
		t.Fatal("expected manual anthropic candidate")
	}
}

func TestBuildOnboardingCandidatesRanksDetectedAheadOfStoredAndManual(t *testing.T) {
	withTestCodexCommand(t, "/missing-codex")
	withTestOllamaProbe(t, false)
	withTestKeyring(t)
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "provider.json"), []byte(`{
  "version": 1,
  "provider": "openrouter",
  "auth_method": "api_key",
  "base_url": "https://openrouter.ai/api/v1",
  "model": "openai/gpt-5",
  "api_key_ref": "OPENROUTER_API_KEY"
}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("OPENAI_API_KEY", "openai-key")

	candidates, err := BuildOnboardingCandidates(stateDir)
	if err != nil {
		t.Fatalf("BuildOnboardingCandidates() error = %v", err)
	}
	if len(candidates) < 3 {
		t.Fatalf("expected at least three onboarding candidates, got %d", len(candidates))
	}
	if candidates[0].Profile.Preset != PresetOpenAI || candidates[0].Source != OnboardingCandidateDetected {
		t.Fatalf("expected detected OpenAI candidate first, got %#v", candidates[0])
	}
	storedIndex := -1
	manualIndex := -1
	for i, candidate := range candidates {
		if storedIndex == -1 && candidate.Source == OnboardingCandidateStored {
			storedIndex = i
		}
		if manualIndex == -1 && candidate.Source == OnboardingCandidateManual {
			manualIndex = i
		}
	}
	if storedIndex == -1 || manualIndex == -1 {
		t.Fatalf("expected stored and manual candidates in %#v", candidates)
	}
	if !(storedIndex < manualIndex) {
		t.Fatalf("expected stored candidates before manual candidates, got stored=%d manual=%d", storedIndex, manualIndex)
	}
}

func withTestCodexCommand(t *testing.T, command string) {
	t.Helper()

	previous := defaultCodexCLICommand
	defaultCodexCLICommand = command
	t.Cleanup(func() {
		defaultCodexCLICommand = previous
	})
}

func withTestOllamaProbe(t *testing.T, reachable bool) {
	t.Helper()

	previous := probeOllamaReachable
	probeOllamaReachable = func(baseURL string) bool {
		return reachable
	}
	t.Cleanup(func() {
		probeOllamaReachable = previous
	})
}

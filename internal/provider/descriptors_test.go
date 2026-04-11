package provider

import "testing"

func TestDescriptorForPresetOpenRouter(t *testing.T) {
	descriptor := DescriptorForPreset(PresetOpenRouter)
	if !descriptor.SupportsThinking {
		t.Fatal("expected openrouter to support thinking")
	}
	if !descriptor.SupportsReasoningEffort {
		t.Fatal("expected openrouter to support reasoning effort")
	}
	if descriptor.DefaultThinking != ThinkingOn {
		t.Fatalf("expected openrouter default thinking on, got %q", descriptor.DefaultThinking)
	}
	if len(descriptor.OnboardingFields) == 0 || descriptor.OnboardingFields[0] != OnboardingFieldBaseURL {
		t.Fatalf("expected openrouter onboarding fields, got %#v", descriptor.OnboardingFields)
	}
	if !descriptor.RequiredAPIKeyByDefault {
		t.Fatal("expected openrouter to require API key by default")
	}
	if len(descriptor.DetectedAPIKeyEnvVars) != 1 || descriptor.DetectedAPIKeyEnvVars[0] != "OPENROUTER_API_KEY" {
		t.Fatalf("expected openrouter detection env var, got %#v", descriptor.DetectedAPIKeyEnvVars)
	}
	if !descriptor.AllowGenericAPIKeyFallback {
		t.Fatal("expected openrouter to allow generic api key fallback")
	}
}

func TestResolveOnboardingAuthMethod(t *testing.T) {
	if got := ResolveOnboardingAuthMethod(PresetCodexCLI, "", Profile{}); got != AuthCodexLogin {
		t.Fatalf("expected codex login auth, got %q", got)
	}
	if got := ResolveOnboardingAuthMethod(PresetOpenWebUI, "", Profile{}); got != AuthNone {
		t.Fatalf("expected openwebui none auth without key, got %q", got)
	}
	if got := ResolveOnboardingAuthMethod(PresetCustom, "", Profile{AuthMethod: AuthAPIKey}); got != AuthAPIKey {
		t.Fatalf("expected custom auth to preserve existing api key mode, got %q", got)
	}
}

func TestProviderHelpersExposeOrderingLabelsAndValidation(t *testing.T) {
	ordered := OrderedProviderPresets()
	if len(ordered) == 0 || ordered[0] != PresetAnthropic {
		t.Fatalf("expected ordered presets starting with anthropic, got %#v", ordered)
	}
	if ProviderLabel(PresetCustom) != "OpenAI-Compatible" {
		t.Fatalf("expected custom label, got %q", ProviderLabel(PresetCustom))
	}
	if !ShouldValidateModelSelection(Profile{Preset: PresetOpenAI, Model: "gpt-5"}) {
		t.Fatal("expected openai model selection to be validated")
	}
	if ShouldValidateModelSelection(Profile{Preset: PresetCodexCLI, Model: "gpt-5-codex"}) {
		t.Fatal("expected codex cli model selection not to require provider catalog validation")
	}
	if ModelCatalogHelpText(PresetCodexCLI) == "" {
		t.Fatal("expected codex cli model catalog help text")
	}
	required, placeholder := ModelFieldConfig(PresetCodexCLI)
	if required || placeholder != "optional" {
		t.Fatalf("expected codex cli model field optional with placeholder, got required=%t placeholder=%q", required, placeholder)
	}
	label, detail, ok := ReservedSettingsEntry(PresetAnthropic)
	if !ok || label == "" || detail == "" {
		t.Fatalf("expected anthropic reserved settings entry, got label=%q detail=%q ok=%t", label, detail, ok)
	}
	if SuggestedModelDetailSuffix(PresetCodexCLI) == "" {
		t.Fatal("expected codex cli suggested model detail suffix")
	}
}

func TestProviderHelpersExposeOnboardingRankAndManualProfiles(t *testing.T) {
	if got := OnboardingPresetRank(PresetCodexCLI); got != 0 {
		t.Fatalf("expected codex cli onboarding rank 0, got %d", got)
	}
	if got := OnboardingPresetRank(PresetCustom); got != 90 {
		t.Fatalf("expected custom onboarding rank 90, got %d", got)
	}

	custom, ok := ManualOnboardingProfile(PresetCustom)
	if !ok {
		t.Fatal("expected custom manual profile")
	}
	if custom.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("expected custom example base URL, got %q", custom.BaseURL)
	}
	if custom.AuthMethod != AuthNone {
		t.Fatalf("expected custom manual profile without auth by default, got %q", custom.AuthMethod)
	}

	codex, ok := ManualOnboardingProfile(PresetCodexCLI)
	if !ok {
		t.Fatal("expected codex cli manual profile")
	}
	if codex.AuthMethod != AuthCodexLogin {
		t.Fatalf("expected codex cli manual profile to default to codex_login, got %q", codex.AuthMethod)
	}
}

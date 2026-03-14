package provider

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"aiterm/internal/config"
)

var probeOllamaReachable = func(baseURL string) bool {
	endpoint, err := ollamaTagsEndpoint(baseURL)
	if err != nil {
		return false
	}

	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(endpoint)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode < http.StatusBadRequest
}

type OnboardingCandidate struct {
	Profile    Profile
	Reason     string
	AuthSource string
	Manual     bool
}

func BuildOnboardingCandidates(stateDir string) ([]OnboardingCandidate, error) {
	candidates := make([]OnboardingCandidate, 0, 8)
	seen := map[string]struct{}{}

	if storedProfiles, selectedPreset, err := loadStoredOnboardingCandidates(stateDir); err != nil {
		return nil, err
	} else {
		for _, stored := range prioritizeStoredCandidates(storedProfiles, selectedPreset) {
			candidates = appendCandidate(candidates, seen, stored)
		}
	}

	detected, err := DetectOnboardingCandidates()
	if err != nil {
		return nil, err
	}
	for _, candidate := range detected {
		candidates = appendCandidate(candidates, seen, candidate)
	}

	for _, candidate := range manualOnboardingCandidates() {
		candidates = appendCandidate(candidates, seen, candidate)
	}

	return candidates, nil
}

func DetectOnboardingCandidates() ([]OnboardingCandidate, error) {
	candidates := make([]OnboardingCandidate, 0, 4)

	openAICandidate, ok, err := detectResponsesCandidate(PresetOpenAI, "OPENAI_API_KEY")
	if err != nil {
		return nil, err
	}
	if ok {
		candidates = append(candidates, openAICandidate)
	}

	openRouterCandidate, ok, err := detectResponsesCandidate(PresetOpenRouter, "OPENROUTER_API_KEY")
	if err != nil {
		return nil, err
	}
	if ok {
		candidates = append(candidates, openRouterCandidate)
	}

	openWebUICandidate, ok, err := detectResponsesCandidate(PresetOpenWebUI, "OPENWEBUI_API_KEY")
	if err != nil {
		return nil, err
	}
	if ok {
		candidates = append(candidates, openWebUICandidate)
	}

	anthropicCandidate, ok, err := detectResponsesCandidate(PresetAnthropic, "ANTHROPIC_API_KEY")
	if err != nil {
		return nil, err
	}
	if ok {
		candidates = append(candidates, anthropicCandidate)
	}

	ollamaCandidate, ok, err := detectOllamaCandidate()
	if err != nil {
		return nil, err
	}
	if ok {
		candidates = append(candidates, ollamaCandidate)
	}

	codexCandidate, ok, err := detectCodexCLICandidate()
	if err != nil {
		return nil, err
	}
	if ok {
		candidates = append(candidates, codexCandidate)
	}

	customCandidate, ok, err := detectCustomCandidate()
	if err != nil {
		return nil, err
	}
	if ok {
		candidates = append(candidates, customCandidate)
	}

	if len(candidates) > 0 {
		return candidates, nil
	}

	genericKey, genericSource := firstSetEnv("SHUTTLE_API_KEY")
	if genericKey == "" {
		return nil, nil
	}

	preset := normalizePreset(os.Getenv("SHUTTLE_PROVIDER"))
	switch preset {
	case PresetOpenRouter, PresetAnthropic, PresetCustom:
	default:
		preset = PresetOpenAI
	}

	cfg := config.Config{
		ProviderType:         string(preset),
		ProviderAuthMethod:   "api_key",
		ProviderAPIKey:       genericKey,
		ProviderAPIKeyEnvVar: genericSource,
	}
	if preset == PresetCustom {
		cfg.ProviderBaseURL = strings.TrimSpace(os.Getenv("SHUTTLE_BASE_URL"))
		cfg.ProviderModel = strings.TrimSpace(os.Getenv("SHUTTLE_MODEL"))
	}

	profile, err := ResolveProfile(cfg)
	if err != nil {
		return nil, err
	}

	return []OnboardingCandidate{
		{
			Profile:    profile,
			Reason:     fmt.Sprintf("Detected %s for the %s preset.", genericSource, profile.Preset),
			AuthSource: genericSource,
		},
	}, nil
}

func detectResponsesCandidate(preset ProviderPreset, specificEnvVar string) (OnboardingCandidate, bool, error) {
	apiKey, apiKeySource := firstSetEnv(specificEnvVar)
	if apiKey == "" {
		return OnboardingCandidate{}, false, nil
	}

	profile, err := ResolveProfile(config.Config{
		ProviderType:         string(preset),
		ProviderAuthMethod:   "api_key",
		ProviderAPIKey:       apiKey,
		ProviderAPIKeyEnvVar: apiKeySource,
	})
	if err != nil {
		return OnboardingCandidate{}, false, err
	}

	return OnboardingCandidate{
		Profile:    profile,
		Reason:     fmt.Sprintf("Detected %s for the %s preset.", apiKeySource, profile.Preset),
		AuthSource: apiKeySource,
	}, true, nil
}

func detectCustomCandidate() (OnboardingCandidate, bool, error) {
	baseURL := strings.TrimSpace(os.Getenv("SHUTTLE_BASE_URL"))
	if baseURL == "" {
		return OnboardingCandidate{}, false, nil
	}

	apiKey, apiKeySource := firstSetEnv("SHUTTLE_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY")
	authMethod := "none"
	if apiKey != "" {
		authMethod = "api_key"
	}

	profile, err := ResolveProfile(config.Config{
		ProviderType:         string(PresetCustom),
		ProviderAuthMethod:   authMethod,
		ProviderBaseURL:      baseURL,
		ProviderModel:        strings.TrimSpace(os.Getenv("SHUTTLE_MODEL")),
		ProviderAPIKey:       apiKey,
		ProviderAPIKeyEnvVar: apiKeySource,
	})
	if err != nil {
		return OnboardingCandidate{}, false, err
	}

	reason := "Detected SHUTTLE_BASE_URL for the custom preset."
	if apiKeySource != "" {
		reason = fmt.Sprintf("%s Using %s for authentication.", reason, apiKeySource)
	}

	return OnboardingCandidate{
		Profile:    profile,
		Reason:     reason,
		AuthSource: apiKeySource,
	}, true, nil
}

func detectCodexCLICandidate() (OnboardingCandidate, bool, error) {
	status, err := codexLoginStatus(defaultCodexCLICommand)
	if err != nil {
		return OnboardingCandidate{}, false, nil
	}
	if !codexStatusIsLoggedIn(status) {
		return OnboardingCandidate{}, false, nil
	}

	profile, err := ResolveProfile(config.Config{
		ProviderType:       string(PresetCodexCLI),
		ProviderAuthMethod: "codex_login",
		ProviderModel:      strings.TrimSpace(os.Getenv("SHUTTLE_MODEL")),
	})
	if err != nil {
		return OnboardingCandidate{}, false, err
	}

	return OnboardingCandidate{
		Profile:    profile,
		Reason:     "Detected an authenticated local Codex CLI session.",
		AuthSource: "codex login",
	}, true, nil
}

func detectOllamaCandidate() (OnboardingCandidate, bool, error) {
	profile, err := ResolveProfile(config.Config{
		ProviderType:       string(PresetOllama),
		ProviderAuthMethod: "none",
		ProviderBaseURL:    strings.TrimSpace(firstNonEmpty(os.Getenv("SHUTTLE_BASE_URL"), os.Getenv("OLLAMA_HOST"))),
		ProviderModel:      strings.TrimSpace(os.Getenv("SHUTTLE_MODEL")),
	})
	if err != nil {
		return OnboardingCandidate{}, false, err
	}
	if !probeOllamaReachable(profile.BaseURL) {
		return OnboardingCandidate{}, false, nil
	}

	return OnboardingCandidate{
		Profile:    profile,
		Reason:     fmt.Sprintf("Detected a reachable Ollama server at %s.", profile.BaseURL),
		AuthSource: "local/network",
	}, true, nil
}

func firstSetEnv(keys ...string) (string, string) {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value, key
		}
	}

	return "", ""
}

func loadStoredOnboardingCandidates(stateDir string) ([]OnboardingCandidate, ProviderPreset, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, "", nil
	}

	profiles, selectedPreset, err := LoadStoredProviderProfiles(stateDir)
	if err != nil {
		return nil, "", err
	}

	candidates := make([]OnboardingCandidate, 0, len(profiles))
	for _, profile := range profiles {
		authSource := strings.TrimSpace(profile.APIKeyEnvVar)
		if authSource == "" && profile.AuthMethod == AuthCodexLogin {
			authSource = "codex login"
		}
		reason := "Previously saved Shuttle provider configuration."
		if profile.Preset == selectedPreset {
			reason = "Currently selected Shuttle provider configuration."
		}
		candidates = append(candidates, OnboardingCandidate{
			Profile:    profile,
			Reason:     reason,
			AuthSource: authSource,
		})
	}

	return candidates, selectedPreset, nil
}

func manualOnboardingCandidates() []OnboardingCandidate {
	profiles := []Profile{
		resolveResponsesProfile(config.Config{
			ProviderType:       string(PresetOpenAI),
			ProviderAuthMethod: "api_key",
		}, responsesPresetDefaults(PresetOpenAI)),
		resolveResponsesProfile(config.Config{
			ProviderType:       string(PresetOpenRouter),
			ProviderAuthMethod: "api_key",
		}, responsesPresetDefaults(PresetOpenRouter)),
		resolveResponsesProfile(config.Config{
			ProviderType:       string(PresetOpenWebUI),
			ProviderAuthMethod: "api_key",
		}, responsesPresetDefaults(PresetOpenWebUI)),
		resolveAnthropicProfile(config.Config{
			ProviderType:       string(PresetAnthropic),
			ProviderAuthMethod: "api_key",
		}),
		resolveCodexCLIProfile(config.Config{
			ProviderType:       string(PresetCodexCLI),
			ProviderAuthMethod: "codex_login",
		}),
	}

	if ollamaProfile, err := resolveOllamaProfile(config.Config{
		ProviderType:       string(PresetOllama),
		ProviderAuthMethod: "none",
	}); err == nil {
		profiles = append(profiles, ollamaProfile)
	}

	if customProfile, err := ResolveProfile(config.Config{
		ProviderType:       string(PresetCustom),
		ProviderAuthMethod: "none",
		ProviderBaseURL:    "https://api.example.com/v1",
	}); err == nil {
		profiles = append(profiles, customProfile)
	}

	candidates := make([]OnboardingCandidate, 0, len(profiles))
	for _, profile := range profiles {
		candidates = append(candidates, OnboardingCandidate{
			Profile: profile,
			Reason:  "Manual setup. Enter provider settings and store them for future sessions.",
			Manual:  true,
		})
	}

	return candidates
}

func appendCandidate(dst []OnboardingCandidate, seen map[string]struct{}, candidate OnboardingCandidate) []OnboardingCandidate {
	key := onboardingCandidateKey(candidate)
	if _, ok := seen[key]; ok {
		return dst
	}
	seen[key] = struct{}{}
	return append(dst, candidate)
}

func onboardingCandidateKey(candidate OnboardingCandidate) string {
	parts := []string{
		string(candidate.Profile.Preset),
		strings.TrimSpace(candidate.Profile.BaseURL),
		strings.TrimSpace(candidate.Profile.Model),
		strings.TrimSpace(candidate.AuthSource),
	}
	if candidate.Manual {
		parts = append(parts, "manual")
	}
	return strings.Join(parts, "|")
}

func prioritizeStoredCandidates(candidates []OnboardingCandidate, selectedPreset ProviderPreset) []OnboardingCandidate {
	sort.SliceStable(candidates, func(i int, j int) bool {
		leftSelected := candidates[i].Profile.Preset == selectedPreset
		rightSelected := candidates[j].Profile.Preset == selectedPreset
		if leftSelected != rightSelected {
			return leftSelected
		}
		return candidates[i].Profile.Name < candidates[j].Profile.Name
	})
	return candidates
}

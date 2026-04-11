package provider

import (
	"strings"

	"aiterm/internal/config"
)

type OnboardingFieldKind string

const (
	OnboardingFieldBaseURL         OnboardingFieldKind = "base_url"
	OnboardingFieldModel           OnboardingFieldKind = "model"
	OnboardingFieldThinking        OnboardingFieldKind = "thinking"
	OnboardingFieldReasoningEffort OnboardingFieldKind = "reasoning_effort"
	OnboardingFieldAPIKey          OnboardingFieldKind = "api_key"
	OnboardingFieldCLICommand      OnboardingFieldKind = "cli_command"
)

type ProviderDescriptor struct {
	Preset                     ProviderPreset
	Name                       string
	DisplayLabel               string
	BackendFamily              BackendFamily
	DefaultBaseURL             string
	DefaultModel               string
	SupportsThinking           bool
	SupportsReasoningEffort    bool
	DefaultThinking            ThinkingMode
	OnboardingFields           []OnboardingFieldKind
	OnboardingIntro            string
	RequiredAPIKeyByDefault    bool
	OnboardingRank             int
	ValidateModelSelection     bool
	ModelCatalogHelpText       string
	DetectedAPIKeyEnvVars      []string
	AllowGenericAPIKeyFallback bool
	ModelFieldOptional         bool
	ModelFieldPlaceholder      string
	ReservedSettingsLabel      string
	ReservedSettingsDetail     string
	SuggestedModelDetailSuffix string
}

func DescriptorForPreset(preset ProviderPreset) ProviderDescriptor {
	switch preset {
	case PresetMock:
		return ProviderDescriptor{
			Preset:         preset,
			Name:           "Mock Provider",
			DisplayLabel:   "Mock",
			BackendFamily:  BackendBuiltin,
			OnboardingRank: 999,
		}
	case PresetOpenAI:
		return ProviderDescriptor{
			Preset:                  preset,
			Name:                    "OpenAI Responses",
			DisplayLabel:            "OpenAI",
			BackendFamily:           BackendResponsesHTTP,
			DefaultBaseURL:          "https://api.openai.com/v1",
			DefaultModel:            "gpt-5-nano-2025-08-07",
			SupportsThinking:        true,
			SupportsReasoningEffort: true,
			DefaultThinking:         ThinkingOff,
			OnboardingFields: []OnboardingFieldKind{
				OnboardingFieldBaseURL,
				OnboardingFieldModel,
				OnboardingFieldThinking,
				OnboardingFieldReasoningEffort,
				OnboardingFieldAPIKey,
			},
			RequiredAPIKeyByDefault:    true,
			OnboardingRank:             10,
			ValidateModelSelection:     true,
			DetectedAPIKeyEnvVars:      []string{"OPENAI_API_KEY"},
			AllowGenericAPIKeyFallback: true,
		}
	case PresetOpenRouter:
		return ProviderDescriptor{
			Preset:                  preset,
			Name:                    "OpenRouter Responses",
			DisplayLabel:            "OpenRouter",
			BackendFamily:           BackendOpenRouter,
			DefaultBaseURL:          "https://openrouter.ai/api/v1",
			DefaultModel:            "openai/gpt-5",
			SupportsThinking:        true,
			SupportsReasoningEffort: true,
			DefaultThinking:         ThinkingOn,
			OnboardingFields: []OnboardingFieldKind{
				OnboardingFieldBaseURL,
				OnboardingFieldModel,
				OnboardingFieldThinking,
				OnboardingFieldReasoningEffort,
				OnboardingFieldAPIKey,
			},
			RequiredAPIKeyByDefault:    true,
			OnboardingRank:             20,
			ValidateModelSelection:     true,
			DetectedAPIKeyEnvVars:      []string{"OPENROUTER_API_KEY"},
			AllowGenericAPIKeyFallback: true,
		}
	case PresetAnthropic:
		return ProviderDescriptor{
			Preset:           preset,
			Name:             "Anthropic Messages",
			DisplayLabel:     "Anthropic",
			BackendFamily:    BackendAnthropic,
			DefaultBaseURL:   "https://api.anthropic.com",
			DefaultModel:     "claude-sonnet-4-6",
			SupportsThinking: true,
			DefaultThinking:  ThinkingOff,
			OnboardingFields: []OnboardingFieldKind{
				OnboardingFieldBaseURL,
				OnboardingFieldModel,
				OnboardingFieldThinking,
				OnboardingFieldAPIKey,
			},
			RequiredAPIKeyByDefault:    true,
			OnboardingRank:             30,
			ValidateModelSelection:     true,
			DetectedAPIKeyEnvVars:      []string{"ANTHROPIC_API_KEY"},
			AllowGenericAPIKeyFallback: true,
			ReservedSettingsLabel:      "Anthropic Agent SDK",
			ReservedSettingsDetail:     "Reserved first-class Anthropic agent runtime integration.",
		}
	case PresetOpenWebUI:
		return ProviderDescriptor{
			Preset:         preset,
			Name:           "OpenWebUI",
			DisplayLabel:   "OpenWebUI",
			BackendFamily:  BackendResponsesHTTP,
			DefaultBaseURL: "http://localhost:3000/api/v1",
			OnboardingFields: []OnboardingFieldKind{
				OnboardingFieldBaseURL,
				OnboardingFieldModel,
				OnboardingFieldAPIKey,
			},
			OnboardingRank:         40,
			ValidateModelSelection: true,
			DetectedAPIKeyEnvVars:  []string{"OPENWEBUI_API_KEY"},
		}
	case PresetOllama:
		return ProviderDescriptor{
			Preset:           preset,
			Name:             "Ollama Chat",
			DisplayLabel:     "Ollama",
			BackendFamily:    BackendOllama,
			DefaultBaseURL:   "http://localhost:11434/api",
			SupportsThinking: true,
			DefaultThinking:  ThinkingOff,
			OnboardingFields: []OnboardingFieldKind{
				OnboardingFieldBaseURL,
				OnboardingFieldModel,
				OnboardingFieldThinking,
				OnboardingFieldAPIKey,
			},
			OnboardingRank:         50,
			ValidateModelSelection: true,
		}
	case PresetCustom:
		return ProviderDescriptor{
			Preset:        preset,
			Name:          "Custom Responses Endpoint",
			DisplayLabel:  "OpenAI-Compatible",
			BackendFamily: BackendResponsesHTTP,
			DefaultModel:  "gpt-5-nano-2025-08-07",
			OnboardingFields: []OnboardingFieldKind{
				OnboardingFieldBaseURL,
				OnboardingFieldModel,
				OnboardingFieldAPIKey,
			},
			OnboardingRank: 90,
		}
	case PresetCodexCLI:
		return ProviderDescriptor{
			Preset:        preset,
			Name:          "Codex CLI",
			DisplayLabel:  "Codex CLI",
			BackendFamily: BackendCLIAgent,
			OnboardingFields: []OnboardingFieldKind{
				OnboardingFieldCLICommand,
				OnboardingFieldModel,
			},
			OnboardingIntro:            "Use an installed Codex CLI and the existing local login.",
			OnboardingRank:             0,
			ModelCatalogHelpText:       "Codex CLI entries are suggested from the OpenAI catalog when available. The live codex CLI picker may differ, and manual entry is still allowed.",
			ModelFieldOptional:         true,
			ModelFieldPlaceholder:      "optional",
			SuggestedModelDetailSuffix: "Codex CLI's own picker may differ; manual entry is still allowed.",
		}
	default:
		return ProviderDescriptor{
			Preset:           preset,
			DisplayLabel:     string(preset),
			DefaultThinking:  ThinkingOff,
			OnboardingFields: []OnboardingFieldKind{OnboardingFieldModel},
			OnboardingRank:   200,
		}
	}
}

func ResolveOnboardingAuthMethod(preset ProviderPreset, apiKey string, existing Profile) AuthMethod {
	descriptor := DescriptorForPreset(preset)
	apiKey = strings.TrimSpace(apiKey)
	switch preset {
	case PresetCodexCLI:
		return AuthCodexLogin
	case PresetOllama, PresetCustom:
		if apiKey == "" {
			if existing.AuthMethod == AuthAPIKey {
				return AuthAPIKey
			}
			return AuthNone
		}
		return AuthAPIKey
	case PresetOpenWebUI:
		if apiKey == "" && existing.AuthMethod != AuthAPIKey {
			return AuthNone
		}
		return AuthAPIKey
	default:
		if descriptor.RequiredAPIKeyByDefault || apiKey != "" || existing.AuthMethod == AuthAPIKey {
			return AuthAPIKey
		}
		return AuthNone
	}
}

func OrderedProviderPresets() []ProviderPreset {
	return []ProviderPreset{
		PresetAnthropic,
		PresetCodexCLI,
		PresetOllama,
		PresetOpenAI,
		PresetOpenRouter,
		PresetOpenWebUI,
		PresetCustom,
	}
}

func ProviderLabel(preset ProviderPreset) string {
	descriptor := DescriptorForPreset(preset)
	if strings.TrimSpace(descriptor.DisplayLabel) != "" {
		return descriptor.DisplayLabel
	}
	if strings.TrimSpace(descriptor.Name) != "" {
		return descriptor.Name
	}
	return string(preset)
}

func ShouldValidateModelSelection(profile Profile) bool {
	if strings.TrimSpace(profile.Model) == "" {
		return false
	}
	return DescriptorForPreset(profile.Preset).ValidateModelSelection
}

func ModelCatalogHelpText(preset ProviderPreset) string {
	return strings.TrimSpace(DescriptorForPreset(preset).ModelCatalogHelpText)
}

func ModelFieldConfig(preset ProviderPreset) (required bool, placeholder string) {
	descriptor := DescriptorForPreset(preset)
	return !descriptor.ModelFieldOptional, strings.TrimSpace(descriptor.ModelFieldPlaceholder)
}

func ReservedSettingsEntry(preset ProviderPreset) (label string, detail string, ok bool) {
	descriptor := DescriptorForPreset(preset)
	label = strings.TrimSpace(descriptor.ReservedSettingsLabel)
	detail = strings.TrimSpace(descriptor.ReservedSettingsDetail)
	return label, detail, label != "" && detail != ""
}

func SuggestedModelDetailSuffix(preset ProviderPreset) string {
	return strings.TrimSpace(DescriptorForPreset(preset).SuggestedModelDetailSuffix)
}

func ManualOnboardingProfile(preset ProviderPreset) (Profile, bool) {
	cfg := config.Config{ProviderType: string(preset)}
	switch preset {
	case PresetOpenAI, PresetOpenRouter, PresetOpenWebUI, PresetAnthropic:
		cfg.ProviderAuthMethod = "api_key"
	case PresetCodexCLI:
		cfg.ProviderAuthMethod = "codex_login"
	case PresetOllama, PresetCustom:
		cfg.ProviderAuthMethod = "none"
	default:
		return Profile{}, false
	}
	if preset == PresetCustom {
		cfg.ProviderBaseURL = "https://api.example.com/v1"
	}
	profile, err := ResolveProfile(cfg)
	if err != nil {
		return Profile{}, false
	}
	return profile, true
}

func OnboardingPresetRank(preset ProviderPreset) int {
	return DescriptorForPreset(preset).OnboardingRank
}

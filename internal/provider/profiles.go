package provider

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"aiterm/internal/config"
)

type BackendFamily string

const (
	BackendBuiltin       BackendFamily = "builtin"
	BackendResponsesHTTP BackendFamily = "responses_http"
	BackendOpenRouter    BackendFamily = "openrouter"
	BackendAnthropic     BackendFamily = "anthropic"
	BackendOllama        BackendFamily = "ollama"
	BackendCLIAgent      BackendFamily = "cli_agent"
	BackendACPStdio      BackendFamily = "acp_stdio"
)

type ProviderPreset string

const (
	PresetMock       ProviderPreset = "mock"
	PresetOpenAI     ProviderPreset = "openai"
	PresetOpenRouter ProviderPreset = "openrouter"
	PresetOpenWebUI  ProviderPreset = "openwebui"
	PresetAnthropic  ProviderPreset = "anthropic"
	PresetOllama     ProviderPreset = "ollama"
	PresetCustom     ProviderPreset = "custom"
	PresetCodexCLI   ProviderPreset = "codex_cli"
)

type AuthMethod string

const (
	AuthNone       AuthMethod = "none"
	AuthAPIKey     AuthMethod = "api_key"
	AuthCodexLogin AuthMethod = "codex_login"
	AuthInherited  AuthMethod = "inherited_env"
)

type ProfileSource string

const (
	SourceFlags ProfileSource = "flags"
	SourceEnv   ProfileSource = "env"
)

type HealthStatus string

const (
	HealthUnknown HealthStatus = "unknown"
	HealthHealthy HealthStatus = "healthy"
	HealthFailed  HealthStatus = "failed"
)

type CapabilitySet struct {
	StructuredOutput bool
	ApprovalFlow     bool
}

type Profile struct {
	ID              string
	Name            string
	BackendFamily   BackendFamily
	Preset          ProviderPreset
	AuthMethod      AuthMethod
	BaseURL         string
	Model           string
	Thinking        string
	ReasoningEffort string
	APIKey          string
	APIKeyEnvVar    string
	CLICommand      string
	CLIArgs         []string
	SelectedModel   *ModelOption
	Capabilities    CapabilitySet
	Source          ProfileSource
	HealthStatus    HealthStatus
}

var ErrMissingAPIKey = errors.New("provider API key is not configured")

func ResolveProfile(cfg config.Config) (Profile, error) {
	preset := normalizePreset(cfg.ProviderType)
	profile, err := resolveProfileForPreset(preset, cfg)
	if err != nil {
		return Profile{}, err
	}
	return applyInteractiveProviderSettings(profile, cfg), nil
}

func resolveProfileForPreset(preset ProviderPreset, cfg config.Config) (Profile, error) {
	switch preset {
	case PresetMock:
		return Profile{
			ID:            "mock",
			Name:          "Mock Provider",
			BackendFamily: BackendBuiltin,
			Preset:        PresetMock,
			AuthMethod:    AuthNone,
			Source:        SourceFlags,
			HealthStatus:  HealthUnknown,
			Capabilities:  defaultProviderCapabilities(),
		}, nil
	case PresetOllama:
		return resolveOllamaProfile(cfg)
	case PresetCodexCLI:
		return resolveCodexCLIProfile(cfg), nil
	case PresetCustom:
		if strings.TrimSpace(cfg.ProviderBaseURL) == "" {
			return Profile{}, errors.New("custom provider requires --base-url")
		}
		descriptor := DescriptorForPreset(PresetCustom)
		descriptor.DefaultBaseURL = strings.TrimSpace(cfg.ProviderBaseURL)
		descriptor.DefaultModel = defaultModel(cfg.ProviderModel, descriptor.DefaultModel)
		return resolveDescriptorProfile(cfg, descriptor), nil
	default:
		descriptor := DescriptorForPreset(preset)
		if descriptor.BackendFamily == "" {
			return Profile{}, fmt.Errorf("unsupported provider preset %q", cfg.ProviderType)
		}
		return resolveDescriptorProfile(cfg, descriptor), nil
	}
}

func defaultProviderCapabilities() CapabilitySet {
	return CapabilitySet{StructuredOutput: true, ApprovalFlow: true}
}

func resolveDescriptorProfile(cfg config.Config, descriptor ProviderDescriptor) Profile {
	return Profile{
		ID:            string(descriptor.Preset),
		Name:          descriptor.Name,
		BackendFamily: descriptor.BackendFamily,
		Preset:        descriptor.Preset,
		AuthMethod:    resolveAuthMethod(cfg.ProviderAuthMethod),
		BaseURL:       defaultValue(cfg.ProviderBaseURL, descriptor.DefaultBaseURL),
		Model:         defaultModel(cfg.ProviderModel, descriptor.DefaultModel),
		APIKey:        cfg.ProviderAPIKey,
		APIKeyEnvVar:  cfg.ProviderAPIKeyEnvVar,
		Source:        SourceFlags,
		HealthStatus:  HealthUnknown,
		Capabilities:  defaultProviderCapabilities(),
	}
}

func applyInteractiveProviderSettings(profile Profile, cfg config.Config) Profile {
	if !SupportsThinking(profile) {
		profile.Thinking = ""
		profile.ReasoningEffort = ""
		return profile
	}
	profile.Thinking = string(NormalizeThinkingMode(cfg.ProviderThinking, profile))
	if SupportsReasoningEffort(profile) {
		profile.ReasoningEffort = string(NormalizeReasoningEffort(cfg.ProviderReasoningEffort))
	} else {
		profile.ReasoningEffort = ""
	}
	return profile
}

func resolveCodexCLIProfile(cfg config.Config) Profile {
	authMethod := AuthCodexLogin
	switch strings.ToLower(strings.TrimSpace(cfg.ProviderAuthMethod)) {
	case "none":
		authMethod = AuthNone
	case "codex_login", "auto", "", "api_key":
		authMethod = AuthCodexLogin
	}
	descriptor := DescriptorForPreset(PresetCodexCLI)
	return Profile{
		ID:            string(PresetCodexCLI),
		Name:          descriptor.Name,
		BackendFamily: descriptor.BackendFamily,
		Preset:        PresetCodexCLI,
		AuthMethod:    authMethod,
		Model:         strings.TrimSpace(cfg.ProviderModel),
		CLICommand:    defaultValue(strings.TrimSpace(cfg.ProviderCLICommand), defaultCodexCLICommand),
		Source:        SourceFlags,
		HealthStatus:  HealthUnknown,
		Capabilities:  defaultProviderCapabilities(),
	}
}

func resolveOllamaProfile(cfg config.Config) (Profile, error) {
	descriptor := DescriptorForPreset(PresetOllama)
	baseURL, err := normalizeOllamaBaseURL(firstNonEmpty(strings.TrimSpace(cfg.ProviderBaseURL), strings.TrimSpace(os.Getenv("OLLAMA_HOST")), descriptor.DefaultBaseURL))
	if err != nil {
		return Profile{}, err
	}

	authMethod := AuthNone
	if strings.EqualFold(strings.TrimSpace(cfg.ProviderAuthMethod), "api_key") {
		authMethod = AuthAPIKey
	}

	return Profile{
		ID:            string(PresetOllama),
		Name:          descriptor.Name,
		BackendFamily: descriptor.BackendFamily,
		Preset:        PresetOllama,
		AuthMethod:    authMethod,
		BaseURL:       baseURL,
		Model:         strings.TrimSpace(cfg.ProviderModel),
		APIKey:        cfg.ProviderAPIKey,
		APIKeyEnvVar:  cfg.ProviderAPIKeyEnvVar,
		Source:        SourceFlags,
		HealthStatus:  HealthUnknown,
		Capabilities:  defaultProviderCapabilities(),
	}, nil
}

func normalizeOllamaBaseURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = "http://localhost:11434"
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse ollama base URL: %w", err)
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "http"
	}
	if parsed.Host == "" && parsed.Path != "" {
		parsed.Host = parsed.Path
		parsed.Path = ""
	}

	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		path = "/api"
	} else if !strings.HasSuffix(path, "/api") {
		path += "/api"
	}
	parsed.Path = path

	return parsed.String(), nil
}

func normalizePreset(value string) ProviderPreset {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "mock":
		return PresetMock
	case "openai":
		return PresetOpenAI
	case "openrouter":
		return PresetOpenRouter
	case "openwebui":
		return PresetOpenWebUI
	case "anthropic":
		return PresetAnthropic
	case "ollama":
		return PresetOllama
	case "custom":
		return PresetCustom
	case "codex_cli", "codex-cli":
		return PresetCodexCLI
	default:
		return ProviderPreset(strings.ToLower(strings.TrimSpace(value)))
	}
}

func resolveAuthMethod(value string) AuthMethod {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none":
		return AuthNone
	case "api_key":
		return AuthAPIKey
	case "codex_login":
		return AuthCodexLogin
	case "inherited_env":
		return AuthInherited
	default:
		return AuthAPIKey
	}
}

func defaultValue(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}

	return fallback
}

func defaultModel(value string, fallback string) string {
	return defaultValue(value, fallback)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}

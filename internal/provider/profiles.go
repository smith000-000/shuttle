package provider

import (
	"errors"
	"fmt"
	"strings"

	"aiterm/internal/config"
)

type BackendFamily string

const (
	BackendBuiltin       BackendFamily = "builtin"
	BackendResponsesHTTP BackendFamily = "responses_http"
	BackendCLIAgent      BackendFamily = "cli_agent"
	BackendACPStdio      BackendFamily = "acp_stdio"
)

type ProviderPreset string

const (
	PresetMock       ProviderPreset = "mock"
	PresetOpenAI     ProviderPreset = "openai"
	PresetOpenRouter ProviderPreset = "openrouter"
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
	ID            string
	Name          string
	BackendFamily BackendFamily
	Preset        ProviderPreset
	AuthMethod    AuthMethod
	BaseURL       string
	Model         string
	APIKey        string
	APIKeyEnvVar  string
	CLICommand    string
	CLIArgs       []string
	Capabilities  CapabilitySet
	Source        ProfileSource
	HealthStatus  HealthStatus
}

var ErrMissingAPIKey = errors.New("provider API key is not configured")

func ResolveProfile(cfg config.Config) (Profile, error) {
	switch normalizePreset(cfg.ProviderType) {
	case PresetMock:
		return Profile{
			ID:            "mock",
			Name:          "Mock Provider",
			BackendFamily: BackendBuiltin,
			Preset:        PresetMock,
			AuthMethod:    AuthNone,
			Source:        SourceFlags,
			HealthStatus:  HealthUnknown,
			Capabilities: CapabilitySet{
				StructuredOutput: true,
				ApprovalFlow:     true,
			},
		}, nil
	case PresetOpenAI:
		return resolveResponsesProfile(
			cfg,
			PresetOpenAI,
			"OpenAI Responses",
			"https://api.openai.com/v1",
			"gpt-5",
		), nil
	case PresetOpenRouter:
		return resolveResponsesProfile(
			cfg,
			PresetOpenRouter,
			"OpenRouter Responses",
			"https://openrouter.ai/api/v1",
			"gpt-5",
		), nil
	case PresetCustom:
		if strings.TrimSpace(cfg.ProviderBaseURL) == "" {
			return Profile{}, errors.New("custom provider requires --base-url")
		}
		return resolveResponsesProfile(
			cfg,
			PresetCustom,
			"Custom Responses Endpoint",
			cfg.ProviderBaseURL,
			defaultModel(cfg.ProviderModel, "gpt-5"),
		), nil
	default:
		return Profile{}, fmt.Errorf("unsupported provider preset %q", cfg.ProviderType)
	}
}

func resolveResponsesProfile(cfg config.Config, preset ProviderPreset, name string, defaultBaseURL string, defaultModelName string) Profile {
	return Profile{
		ID:            string(preset),
		Name:          name,
		BackendFamily: BackendResponsesHTTP,
		Preset:        preset,
		AuthMethod:    resolveAuthMethod(cfg.ProviderAuthMethod),
		BaseURL:       defaultValue(cfg.ProviderBaseURL, defaultBaseURL),
		Model:         defaultModel(cfg.ProviderModel, defaultModelName),
		APIKey:        cfg.ProviderAPIKey,
		APIKeyEnvVar:  cfg.ProviderAPIKeyEnvVar,
		Source:        SourceFlags,
		HealthStatus:  HealthUnknown,
		Capabilities: CapabilitySet{
			StructuredOutput: true,
			ApprovalFlow:     true,
		},
	}
}

func normalizePreset(value string) ProviderPreset {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "mock":
		return PresetMock
	case "openai":
		return PresetOpenAI
	case "openrouter":
		return PresetOpenRouter
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

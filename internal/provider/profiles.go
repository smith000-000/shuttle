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
	SelectedModel *ModelOption
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
		defaults := responsesPresetDefaults(PresetOpenAI)
		return resolveResponsesProfile(cfg, defaults), nil
	case PresetOpenRouter:
		defaults := responsesPresetDefaults(PresetOpenRouter)
		return resolveResponsesProfile(cfg, defaults), nil
	case PresetOpenWebUI:
		defaults := responsesPresetDefaults(PresetOpenWebUI)
		return resolveResponsesProfile(cfg, defaults), nil
	case PresetAnthropic:
		return resolveAnthropicProfile(cfg), nil
	case PresetOllama:
		return resolveOllamaProfile(cfg)
	case PresetCodexCLI:
		return resolveCodexCLIProfile(cfg), nil
	case PresetCustom:
		if strings.TrimSpace(cfg.ProviderBaseURL) == "" {
			return Profile{}, errors.New("custom provider requires --base-url")
		}
		defaults := responsesPresetDefaults(PresetCustom)
		defaults.baseURL = cfg.ProviderBaseURL
		defaults.model = defaultModel(cfg.ProviderModel, defaults.model)
		return resolveResponsesProfile(cfg, defaults), nil
	default:
		return Profile{}, fmt.Errorf("unsupported provider preset %q", cfg.ProviderType)
	}
}

type responsesDefaults struct {
	preset        ProviderPreset
	name          string
	backendFamily BackendFamily
	baseURL       string
	model         string
}

func responsesPresetDefaults(preset ProviderPreset) responsesDefaults {
	switch preset {
	case PresetOpenAI:
		return responsesDefaults{
			preset:        PresetOpenAI,
			name:          "OpenAI Responses",
			backendFamily: BackendResponsesHTTP,
			baseURL:       "https://api.openai.com/v1",
			model:         "gpt-5-nano-2025-08-07",
		}
	case PresetOpenRouter:
		return responsesDefaults{
			preset:        PresetOpenRouter,
			name:          "OpenRouter Responses",
			backendFamily: BackendOpenRouter,
			baseURL:       "https://openrouter.ai/api/v1",
			model:         "openai/gpt-5",
		}
	case PresetOpenWebUI:
		return responsesDefaults{
			preset:        PresetOpenWebUI,
			name:          "OpenWebUI",
			backendFamily: BackendResponsesHTTP,
			baseURL:       "http://localhost:3000/api/v1",
			model:         "",
		}
	case PresetOllama:
		return responsesDefaults{
			preset:        PresetOllama,
			name:          "Ollama Chat",
			backendFamily: BackendOllama,
			baseURL:       "http://localhost:11434/api",
		}
	case PresetCustom:
		return responsesDefaults{
			preset:        PresetCustom,
			name:          "Custom Responses Endpoint",
			backendFamily: BackendResponsesHTTP,
			model:         "gpt-5-nano-2025-08-07",
		}
	default:
		return responsesDefaults{preset: preset}
	}
}

func resolveAnthropicProfile(cfg config.Config) Profile {
	return Profile{
		ID:            string(PresetAnthropic),
		Name:          "Anthropic Messages",
		BackendFamily: BackendAnthropic,
		Preset:        PresetAnthropic,
		AuthMethod:    resolveAuthMethod(cfg.ProviderAuthMethod),
		BaseURL:       defaultValue(cfg.ProviderBaseURL, "https://api.anthropic.com"),
		Model:         defaultModel(cfg.ProviderModel, "claude-sonnet-4-6"),
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

func resolveResponsesProfile(cfg config.Config, defaults responsesDefaults) Profile {
	return Profile{
		ID:            string(defaults.preset),
		Name:          defaults.name,
		BackendFamily: defaults.backendFamily,
		Preset:        defaults.preset,
		AuthMethod:    resolveAuthMethod(cfg.ProviderAuthMethod),
		BaseURL:       defaultValue(cfg.ProviderBaseURL, defaults.baseURL),
		Model:         defaultModel(cfg.ProviderModel, defaults.model),
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

func resolveCodexCLIProfile(cfg config.Config) Profile {
	authMethod := AuthCodexLogin
	switch strings.ToLower(strings.TrimSpace(cfg.ProviderAuthMethod)) {
	case "none":
		authMethod = AuthNone
	case "codex_login", "auto", "", "api_key":
		authMethod = AuthCodexLogin
	}

	return Profile{
		ID:            string(PresetCodexCLI),
		Name:          "Codex CLI",
		BackendFamily: BackendCLIAgent,
		Preset:        PresetCodexCLI,
		AuthMethod:    authMethod,
		Model:         strings.TrimSpace(cfg.ProviderModel),
		CLICommand:    defaultValue(strings.TrimSpace(cfg.ProviderCLICommand), defaultCodexCLICommand),
		Source:        SourceFlags,
		HealthStatus:  HealthUnknown,
		Capabilities: CapabilitySet{
			StructuredOutput: true,
			ApprovalFlow:     true,
		},
	}
}

func resolveOllamaProfile(cfg config.Config) (Profile, error) {
	baseURL, err := normalizeOllamaBaseURL(firstNonEmpty(strings.TrimSpace(cfg.ProviderBaseURL), strings.TrimSpace(os.Getenv("OLLAMA_HOST")), "http://localhost:11434"))
	if err != nil {
		return Profile{}, err
	}

	authMethod := AuthNone
	if strings.EqualFold(strings.TrimSpace(cfg.ProviderAuthMethod), "api_key") {
		authMethod = AuthAPIKey
	}

	return Profile{
		ID:            string(PresetOllama),
		Name:          "Ollama Chat",
		BackendFamily: BackendOllama,
		Preset:        PresetOllama,
		AuthMethod:    authMethod,
		BaseURL:       baseURL,
		Model:         strings.TrimSpace(cfg.ProviderModel),
		APIKey:        cfg.ProviderAPIKey,
		APIKeyEnvVar:  cfg.ProviderAPIKeyEnvVar,
		Source:        SourceFlags,
		HealthStatus:  HealthUnknown,
		Capabilities: CapabilitySet{
			StructuredOutput: true,
			ApprovalFlow:     true,
		},
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

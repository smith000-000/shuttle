package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"aiterm/internal/config"
	"aiterm/internal/controller"
)

type FactoryOptions struct {
	HTTPClient *http.Client
}

type healthCheckAgent interface {
	CheckHealth(ctx context.Context) error
}

type backendAgentConstructor func(Profile, *http.Client) (controller.Agent, error)

var backendAgentConstructors = map[BackendFamily]backendAgentConstructor{
	BackendBuiltin: func(profile Profile, _ *http.Client) (controller.Agent, error) {
		if profile.Preset != PresetMock {
			return nil, fmt.Errorf("unsupported builtin preset %q", profile.Preset)
		}
		return NewMockAgent(), nil
	},
	BackendCLIAgent: func(profile Profile, _ *http.Client) (controller.Agent, error) {
		return NewCodexCLIAgent(profile)
	},
	BackendAnthropic: func(profile Profile, client *http.Client) (controller.Agent, error) {
		return NewAnthropicAgent(profile, client)
	},
	BackendOllama: func(profile Profile, client *http.Client) (controller.Agent, error) {
		return NewOllamaAgent(profile, client)
	},
	BackendOpenRouter: func(profile Profile, client *http.Client) (controller.Agent, error) {
		return NewOpenRouterAgent(profile, client)
	},
	BackendResponsesHTTP: func(profile Profile, client *http.Client) (controller.Agent, error) {
		return NewResponsesAgent(profile, client)
	},
}

func NewFromConfig(cfg config.Config, options FactoryOptions) (controller.Agent, Profile, error) {
	profile, err := ResolveProfile(cfg)
	if err != nil {
		return nil, Profile{}, err
	}

	agent, err := NewFromProfile(profile, options)
	if err != nil {
		return nil, Profile{}, err
	}
	return agent, profile, nil
}

func NewFromProfile(profile Profile, options FactoryOptions) (controller.Agent, error) {
	backendFamily := profile.BackendFamily
	if backendFamily == "" {
		backendFamily = DescriptorForPreset(profile.Preset).BackendFamily
	}
	constructor, ok := backendAgentConstructors[backendFamily]
	if !ok {
		return nil, fmt.Errorf("unsupported backend family %q", backendFamily)
	}
	return constructor(profile, options.HTTPClient)
}

func CheckHealth(ctx context.Context, profile Profile, options FactoryOptions) error {
	agent, err := NewFromProfile(profile, options)
	if err != nil {
		return err
	}
	checker, ok := agent.(healthCheckAgent)
	if !ok {
		return fmt.Errorf("provider %q does not support health checks", profile.Preset)
	}
	return checker.CheckHealth(ctx)
}

func ExplainHealthCheckError(profile Profile, err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		message = "provider health check failed"
	}
	if errors.Is(err, ErrMissingAPIKey) {
		return message + ". Configure an API key for this provider or switch to an auth method that does not require one."
	}

	backendFamily := profile.BackendFamily
	if backendFamily == "" {
		backendFamily = DescriptorForPreset(profile.Preset).BackendFamily
	}

	switch backendFamily {
	case BackendCLIAgent:
		return message + ". Verify the CLI command is installed, runnable, and logged in on this machine."
	case BackendOllama:
		return message + ". Verify the Ollama server is reachable at the configured base URL."
	case BackendAnthropic, BackendOpenRouter, BackendResponsesHTTP:
		return message + ". Verify the base URL, model, and authentication settings for this provider."
	default:
		return message
	}
}

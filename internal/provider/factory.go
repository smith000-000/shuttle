package provider

import (
	"context"
	"fmt"
	"net/http"

	"aiterm/internal/config"
	"aiterm/internal/controller"
)

type FactoryOptions struct {
	HTTPClient *http.Client
}

type healthCheckAgent interface {
	CheckHealth(ctx context.Context) error
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
	switch profile.BackendFamily {
	case BackendBuiltin:
		if profile.Preset != PresetMock {
			return nil, fmt.Errorf("unsupported builtin preset %q", profile.Preset)
		}

		return NewMockAgent(), nil
	case BackendCLIAgent:
		return NewCodexCLIAgent(profile)
	case BackendAnthropic:
		return NewAnthropicAgent(profile, options.HTTPClient)
	case BackendOllama:
		return NewOllamaAgent(profile, options.HTTPClient)
	case BackendOpenRouter:
		return NewOpenRouterAgent(profile, options.HTTPClient)
	case BackendResponsesHTTP:
		return NewResponsesAgent(profile, options.HTTPClient)
	default:
		return nil, fmt.Errorf("unsupported backend family %q", profile.BackendFamily)
	}
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

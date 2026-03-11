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
	case BackendResponsesHTTP:
		return NewResponsesAgent(profile, options.HTTPClient)
	default:
		return nil, fmt.Errorf("unsupported backend family %q", profile.BackendFamily)
	}
}

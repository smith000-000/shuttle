package provider

import (
	"context"
	"testing"

	"aiterm/internal/config"
	"aiterm/internal/controller"
)

func TestNewFromConfigWrapsRuntimeForMockProvider(t *testing.T) {
	previous := runtimeLookPath
	runtimeLookPath = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}
	t.Cleanup(func() {
		runtimeLookPath = previous
	})

	agent, profile, err := NewFromConfig(config.Config{
		ProviderType: "mock",
		RuntimeType:  RuntimePi,
	}, FactoryOptions{})
	if err != nil {
		t.Fatalf("NewFromConfig() error = %v", err)
	}
	if profile.Preset != PresetMock {
		t.Fatalf("expected mock profile, got %s", profile.Preset)
	}
	response, err := agent.Respond(context.Background(), controller.AgentInput{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if response.Message == "" || response.ModelInfo == nil {
		t.Fatalf("expected wrapped response metadata, got %#v", response)
	}
}

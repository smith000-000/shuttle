package provider

import (
	"testing"

	"aiterm/internal/config"
)

func TestNewFromConfigLeavesRuntimeSelectionToRuntimeLayer(t *testing.T) {
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
	if agent == nil {
		t.Fatal("expected provider factory to return a base model agent")
	}
}

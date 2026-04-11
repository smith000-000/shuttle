package provider

import (
	"errors"
	"strings"
	"testing"
)

func TestExplainHealthCheckErrorAddsPresetSpecificGuidance(t *testing.T) {
	err := errors.New("dial tcp timeout")
	message := ExplainHealthCheckError(Profile{BackendFamily: BackendOllama}, err)
	if !strings.Contains(message, "dial tcp timeout") || !strings.Contains(message, "Ollama server is reachable") {
		t.Fatalf("expected ollama guidance, got %q", message)
	}
}

func TestExplainHealthCheckErrorExplainsMissingAPIKey(t *testing.T) {
	message := ExplainHealthCheckError(Profile{BackendFamily: BackendResponsesHTTP}, ErrMissingAPIKey)
	if !strings.Contains(message, "API key") || !strings.Contains(message, "auth method") {
		t.Fatalf("expected API key guidance, got %q", message)
	}
}

func TestNewFromProfileUsesPresetDescriptorWhenBackendFamilyMissing(t *testing.T) {
	agent, err := NewFromProfile(Profile{Preset: PresetMock}, FactoryOptions{})
	if err != nil {
		t.Fatalf("NewFromProfile() error = %v", err)
	}
	if agent == nil {
		t.Fatal("expected agent from preset-backed constructor")
	}
}

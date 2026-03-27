package runtime

import (
	"testing"

	"aiterm/internal/config"
	"aiterm/internal/provider"
	"aiterm/internal/search"
)

func TestSelectionFromConfigBuiltinUsesShuttleSearch(t *testing.T) {
	selection := selectionFromConfig(config.Config{
		RuntimeType:    "builtin",
		SearchProvider: search.ProviderBrave,
	}, provider.Profile{Preset: provider.PresetOpenAI}, WorkspaceState{})

	if selection.Search.Mode != search.AvailabilityShuttle {
		t.Fatalf("expected shuttle search, got %#v", selection.Search)
	}
	if selection.Search.Provider != search.ProviderBrave {
		t.Fatalf("expected brave provider, got %#v", selection.Search)
	}
}

func TestSelectionFromConfigExternalUsesNativeSearchWithFallback(t *testing.T) {
	selection := selectionFromConfig(config.Config{
		RuntimeType:    "pi",
		SearchProvider: search.ProviderPerplexity,
	}, provider.Profile{Preset: provider.PresetOpenAI}, WorkspaceState{})

	if selection.Search.Mode != search.AvailabilityRuntimeNativeWithFallback {
		t.Fatalf("expected runtime-native fallback mode, got %#v", selection.Search)
	}
	if selection.Search.Provider != search.ProviderPerplexity {
		t.Fatalf("expected perplexity fallback, got %#v", selection.Search)
	}
}

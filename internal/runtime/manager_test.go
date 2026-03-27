package runtime

import (
	"os/exec"
	"path/filepath"
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

func TestSelectionFromConfigFakePIUsesNativeSearchWithFallback(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}

	selection := selectionFromConfig(config.Config{
		RuntimeType:    "fake_pi",
		SearchProvider: search.ProviderBrave,
		StartDir:       repoRoot,
		StateDir:       t.TempDir(),
	}, provider.Profile{Preset: provider.PresetOpenAI}, WorkspaceState{})

	if selection.ID != RuntimeFakePi {
		t.Fatalf("expected fake_pi runtime, got %#v", selection.ID)
	}
	if !selection.ProviderAllowed {
		t.Fatalf("expected fake_pi to be selectable, got detail=%q", selection.Detail)
	}
	if selection.Search.Mode != search.AvailabilityRuntimeNativeWithFallback {
		t.Fatalf("expected runtime-native fallback mode, got %#v", selection.Search)
	}
}

func TestChoicesIncludeFakePIWhenHelperIsAvailable(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}

	choices := Choices(config.Config{
		StartDir: repoRoot,
		StateDir: t.TempDir(),
	}, provider.Profile{Preset: provider.PresetOpenAI})

	found := false
	for _, choice := range choices {
		if choice.Selection.ID == RuntimeFakePi {
			found = true
			if choice.Disabled {
				t.Fatalf("expected fake_pi choice to be enabled, got detail=%q", choice.Detail)
			}
		}
	}
	if !found {
		t.Fatal("expected fake_pi to appear in runtime choices")
	}
}

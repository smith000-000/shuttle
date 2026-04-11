package provider

import (
	"errors"
	"testing"
)

func TestDetectRuntimeInstallCandidates(t *testing.T) {
	previous := runtimeLookPath
	runtimeLookPath = func(file string) (string, error) {
		switch file {
		case defaultPiCommand, defaultCodexCLICommand:
			return "/usr/bin/" + file, nil
		default:
			return "", errors.New("missing")
		}
	}
	t.Cleanup(func() {
		runtimeLookPath = previous
	})

	candidates := DetectRuntimeInstallCandidates()
	if len(candidates) != 4 {
		t.Fatalf("expected 4 runtime candidates, got %d", len(candidates))
	}
	if !candidates[0].Installed || candidates[0].Runtime != RuntimePi {
		t.Fatalf("expected pi installed candidate, got %#v", candidates[0])
	}
	if !candidates[1].Installed || candidates[1].Runtime != RuntimeCodexSDK {
		t.Fatalf("expected codex sdk installed candidate, got %#v", candidates[1])
	}
	if candidates[2].Supported || candidates[3].Supported {
		t.Fatalf("expected claude/opencode to be scaffolding-only for now")
	}
}

func TestResolveRuntimeSelectionAutoPrefersInstalledSupportedRuntime(t *testing.T) {
	previous := runtimeLookPath
	runtimeLookPath = func(file string) (string, error) {
		switch file {
		case defaultPiCommand:
			return "/usr/bin/pi", nil
		default:
			return "", errors.New("missing")
		}
	}
	t.Cleanup(func() {
		runtimeLookPath = previous
	})

	resolved := ResolveRuntimeSelection(RuntimeAuto, "")
	if resolved.SelectedType != RuntimePi {
		t.Fatalf("expected auto runtime to choose pi, got %#v", resolved)
	}
	if resolved.Command != defaultPiCommand {
		t.Fatalf("expected pi command %q, got %#v", defaultPiCommand, resolved)
	}
	if !resolved.AutoSelected {
		t.Fatalf("expected auto-selected runtime, got %#v", resolved)
	}
}

func TestResolveRuntimeSelectionAutoFallsBackToBuiltin(t *testing.T) {
	previous := runtimeLookPath
	runtimeLookPath = func(string) (string, error) {
		return "", errors.New("missing")
	}
	t.Cleanup(func() {
		runtimeLookPath = previous
	})

	resolved := ResolveRuntimeSelection(RuntimeAuto, "")
	if resolved.SelectedType != RuntimeBuiltin || resolved.Command != "" {
		t.Fatalf("expected builtin fallback, got %#v", resolved)
	}
}

func TestResolveRuntimeSelectionFillsDefaultCommandForExplicitRuntime(t *testing.T) {
	previous := runtimeLookPath
	runtimeLookPath = func(string) (string, error) {
		return "", errors.New("missing")
	}
	t.Cleanup(func() {
		runtimeLookPath = previous
	})

	resolved := ResolveRuntimeSelection("codex-sdk", "")
	if resolved.SelectedType != RuntimeCodexSDK {
		t.Fatalf("expected normalized codex runtime, got %#v", resolved)
	}
	if resolved.Command != defaultCodexCLICommand {
		t.Fatalf("expected default codex command %q, got %#v", defaultCodexCLICommand, resolved)
	}
}

func TestResolveRuntimeSelectionPreservesExplicitCommand(t *testing.T) {
	resolved := ResolveRuntimeSelection(RuntimePi, "/custom/pi")
	if resolved.SelectedType != RuntimePi || resolved.Command != "/custom/pi" {
		t.Fatalf("expected explicit runtime command to remain intact, got %#v", resolved)
	}
}

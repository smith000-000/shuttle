package provider

import (
	"errors"
	"testing"
)

func TestDetectRuntimeInstallCandidates(t *testing.T) {
	previousLookPath := runtimeLookPath
	previousProbe := codexRuntimeVersionProbe
	runtimeLookPath = func(file string) (string, error) {
		switch file {
		case defaultPiCommand, defaultCodexCLICommand:
			return "/usr/bin/" + file, nil
		default:
			return "", errors.New("missing")
		}
	}
	codexRuntimeVersionProbe = func(string) (string, error) { return minimumCodexRuntimeVersion, nil }
	t.Cleanup(func() {
		runtimeLookPath = previousLookPath
		codexRuntimeVersionProbe = previousProbe
	})

	candidates := DetectRuntimeInstallCandidates()
	if len(candidates) != 5 {
		t.Fatalf("expected 5 runtime candidates, got %d", len(candidates))
	}
	if !candidates[0].Installed || candidates[0].Runtime != RuntimeCodexSDK || !candidates[0].Supported {
		t.Fatalf("expected codex sdk installed authoritative candidate, got %#v", candidates[0])
	}
	if !candidates[1].Installed || candidates[1].Runtime != RuntimeCodexAppServer || !candidates[1].Supported {
		t.Fatalf("expected codex app server installed authoritative candidate, got %#v", candidates[1])
	}
	if !candidates[2].Installed || candidates[2].Runtime != RuntimePi || candidates[2].Supported {
		t.Fatalf("expected pi installed but non-authoritative candidate, got %#v", candidates[2])
	}
	if candidates[3].Supported || candidates[4].Supported {
		t.Fatalf("expected claude/opencode to be scaffolding-only for now")
	}
}

func TestResolveRuntimeSelectionAutoPrefersInstalledSupportedRuntime(t *testing.T) {
	previousLookPath := runtimeLookPath
	previousProbe := codexRuntimeVersionProbe
	runtimeLookPath = func(file string) (string, error) {
		switch file {
		case defaultPiCommand, defaultCodexCLICommand:
			return "/usr/bin/" + file, nil
		default:
			return "", errors.New("missing")
		}
	}
	codexRuntimeVersionProbe = func(string) (string, error) { return minimumCodexRuntimeVersion, nil }
	t.Cleanup(func() {
		runtimeLookPath = previousLookPath
		codexRuntimeVersionProbe = previousProbe
	})

	resolved := ResolveRuntimeSelection(RuntimeAuto, "")
	if resolved.SelectedType != RuntimeCodexSDK {
		t.Fatalf("expected auto runtime to choose codex sdk, got %#v", resolved)
	}
	if resolved.Command != defaultCodexCLICommand {
		t.Fatalf("expected codex command %q, got %#v", defaultCodexCLICommand, resolved)
	}
	if !resolved.AutoSelected {
		t.Fatalf("expected auto-selected runtime, got %#v", resolved)
	}
}

func TestResolveRuntimeSelectionAutoFallsBackToBuiltin(t *testing.T) {
	previousLookPath := runtimeLookPath
	previousProbe := codexRuntimeVersionProbe
	runtimeLookPath = func(string) (string, error) { return "", errors.New("missing") }
	codexRuntimeVersionProbe = func(string) (string, error) { return minimumCodexRuntimeVersion, nil }
	t.Cleanup(func() {
		runtimeLookPath = previousLookPath
		codexRuntimeVersionProbe = previousProbe
	})

	resolved := ResolveRuntimeSelection(RuntimeAuto, "")
	if resolved.SelectedType != RuntimeBuiltin || resolved.Command != "" {
		t.Fatalf("expected builtin fallback, got %#v", resolved)
	}
}

func TestResolveRuntimeSelectionAutoSkipsIncompatibleCodex(t *testing.T) {
	previousLookPath := runtimeLookPath
	previousProbe := codexRuntimeVersionProbe
	runtimeLookPath = func(file string) (string, error) {
		switch file {
		case defaultPiCommand, defaultCodexCLICommand:
			return "/usr/bin/" + file, nil
		default:
			return "", errors.New("missing")
		}
	}
	codexRuntimeVersionProbe = func(string) (string, error) { return "0.117.0", nil }
	t.Cleanup(func() {
		runtimeLookPath = previousLookPath
		codexRuntimeVersionProbe = previousProbe
	})

	resolved := ResolveRuntimeSelection(RuntimeAuto, "")
	if resolved.SelectedType != RuntimeBuiltin || resolved.Command != "" {
		t.Fatalf("expected builtin fallback when codex is incompatible, got %#v", resolved)
	}
}

func TestResolveRuntimeSelectionFillsDefaultCommandForExplicitRuntime(t *testing.T) {
	previousLookPath := runtimeLookPath
	previousProbe := codexRuntimeVersionProbe
	runtimeLookPath = func(string) (string, error) { return "", errors.New("missing") }
	codexRuntimeVersionProbe = func(string) (string, error) { return minimumCodexRuntimeVersion, nil }
	t.Cleanup(func() {
		runtimeLookPath = previousLookPath
		codexRuntimeVersionProbe = previousProbe
	})

	resolved := ResolveRuntimeSelection("codex-sdk", "")
	if resolved.SelectedType != RuntimeCodexSDK {
		t.Fatalf("expected normalized codex runtime, got %#v", resolved)
	}
	if resolved.Command != defaultCodexCLICommand {
		t.Fatalf("expected default codex command %q, got %#v", defaultCodexCLICommand, resolved)
	}
}

func TestResolveRuntimeSelectionNormalizesCodexAppServer(t *testing.T) {
	resolved := ResolveRuntimeSelection("codex-app-server", "")
	if resolved.SelectedType != RuntimeCodexAppServer {
		t.Fatalf("expected normalized codex app server runtime, got %#v", resolved)
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

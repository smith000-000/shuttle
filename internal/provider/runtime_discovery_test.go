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

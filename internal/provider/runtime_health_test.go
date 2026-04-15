package provider

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateResolvedRuntimeBuiltinSkipsExternalChecks(t *testing.T) {
	if err := ValidateResolvedRuntime(ResolvedRuntime{SelectedType: RuntimeBuiltin}); err != nil {
		t.Fatalf("ValidateResolvedRuntime() error = %v", err)
	}
}

func TestValidateResolvedRuntimeCodexRejectsOldVersion(t *testing.T) {
	previousLookPath := runtimeLookPath
	previousProbe := codexRuntimeVersionProbe
	runtimeLookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	codexRuntimeVersionProbe = func(string) (string, error) { return "0.117.0", nil }
	t.Cleanup(func() {
		runtimeLookPath = previousLookPath
		codexRuntimeVersionProbe = previousProbe
	})

	err := ValidateResolvedRuntime(ResolvedRuntime{SelectedType: RuntimeCodexSDK, Command: defaultCodexCLICommand})
	if err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("expected old codex version error, got %v", err)
	}
}

func TestValidateResolvedRuntimeCodexRejectsMissingCommand(t *testing.T) {
	previousLookPath := runtimeLookPath
	previousProbe := codexRuntimeVersionProbe
	runtimeLookPath = func(string) (string, error) { return "", errors.New("missing") }
	codexRuntimeVersionProbe = func(string) (string, error) { return minimumCodexRuntimeVersion, nil }
	t.Cleanup(func() {
		runtimeLookPath = previousLookPath
		codexRuntimeVersionProbe = previousProbe
	})

	err := ValidateResolvedRuntime(ResolvedRuntime{SelectedType: RuntimeCodexSDK, Command: defaultCodexCLICommand})
	if err == nil || !strings.Contains(err.Error(), "find codex runtime command") {
		t.Fatalf("expected missing codex command error, got %v", err)
	}
}

func TestDetectRuntimeInstallCandidatesMarksIncompatibleCodexUnsupported(t *testing.T) {
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

	candidates := DetectRuntimeInstallCandidates()
	if !candidates[0].Installed || candidates[0].Supported {
		t.Fatalf("expected incompatible codex to be installed but unsupported, got %#v", candidates[0])
	}
	if !strings.Contains(candidates[0].FailureReason, "too old") {
		t.Fatalf("expected failure reason to mention old version, got %#v", candidates[0])
	}
}

func TestParseSemverLike(t *testing.T) {
	if got := parseSemverLike("OpenAI Codex v0.118.2 (research preview)"); got != "0.118.2" {
		t.Fatalf("expected parsed version, got %q", got)
	}
	if got := parseSemverLike("codex 0.118.0"); got != "0.118.0" {
		t.Fatalf("expected plain parsed version, got %q", got)
	}
	if got := parseSemverLike("unknown"); got != "" {
		t.Fatalf("expected empty version, got %q", got)
	}
}

func TestVersionAtLeast(t *testing.T) {
	if !versionAtLeast("0.118.0", minimumCodexRuntimeVersion) {
		t.Fatal("expected equal version to pass")
	}
	if !versionAtLeast("0.119.0", minimumCodexRuntimeVersion) {
		t.Fatal("expected newer version to pass")
	}
	if versionAtLeast("0.117.9", minimumCodexRuntimeVersion) {
		t.Fatal("expected older version to fail")
	}
}

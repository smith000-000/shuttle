package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aiterm/internal/config"
)

func TestConfigLaunchProfilesDefaults(t *testing.T) {
	profiles := ConfigLaunchProfiles(config.Config{})
	if profiles.Persistent.Mode != LaunchModeManagedPrompt {
		t.Fatalf("expected managed-prompt persistent default, got %#v", profiles.Persistent)
	}
	if profiles.Execution.Mode != LaunchModeManagedMinimal {
		t.Fatalf("expected managed-minimal execution default, got %#v", profiles.Execution)
	}
	if !profiles.Persistent.SourceUserRC || !profiles.Persistent.InheritEnv {
		t.Fatalf("expected persistent defaults to inherit rc/env, got %#v", profiles.Persistent)
	}
	if profiles.Execution.SourceUserRC {
		t.Fatalf("expected execution defaults to skip user rc, got %#v", profiles.Execution)
	}
}

func TestStoredLaunchProfilesRoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	want := LaunchProfiles{
		Persistent: LaunchProfile{
			Mode:         LaunchModeManagedMinimal,
			Shell:        ShellTypeBash,
			SourceUserRC: false,
			InheritEnv:   true,
		},
		Execution: LaunchProfile{
			Mode:         LaunchModeManagedPrompt,
			Shell:        ShellTypeZsh,
			SourceUserRC: true,
			InheritEnv:   false,
		},
	}
	if err := SaveStoredLaunchProfiles(stateDir, want); err != nil {
		t.Fatalf("SaveStoredLaunchProfiles() error = %v", err)
	}
	got, ok, err := LoadStoredLaunchProfiles(stateDir)
	if err != nil {
		t.Fatalf("LoadStoredLaunchProfiles() error = %v", err)
	}
	if !ok {
		t.Fatal("expected stored launch profiles to exist")
	}
	if got != NormalizeLaunchProfiles(want) {
		t.Fatalf("expected %#v, got %#v", NormalizeLaunchProfiles(want), got)
	}
}

func TestPersistentLaunchSpecManagedPromptUsesZDOTDIR(t *testing.T) {
	runtimeDir := t.TempDir()
	fakeZsh := filepath.Join(runtimeDir, "zsh")
	if err := os.WriteFile(fakeZsh, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(fake zsh) error = %v", err)
	}
	t.Setenv("SHELL", fakeZsh)
	spec, err := PersistentLaunchSpec(runtimeDir, LaunchProfiles{
		Persistent: LaunchProfile{
			Mode:         LaunchModeManagedPrompt,
			Shell:        ShellTypeAuto,
			SourceUserRC: true,
			InheritEnv:   true,
		},
		Execution: DefaultExecutionLaunchProfile(),
	})
	if err != nil {
		t.Fatalf("PersistentLaunchSpec() error = %v", err)
	}
	if !strings.Contains(spec.Command, fakeZsh) {
		t.Fatalf("expected zsh command, got %q", spec.Command)
	}
	if strings.TrimSpace(spec.Env["ZDOTDIR"]) == "" {
		t.Fatalf("expected ZDOTDIR in env, got %#v", spec.Env)
	}
	if strings.TrimSpace(spec.Env["SHUTTLE_RUNTIME_DIR"]) != runtimeDir {
		t.Fatalf("expected SHUTTLE_RUNTIME_DIR=%q, got %#v", runtimeDir, spec.Env)
	}
	data, err := os.ReadFile(filepath.Join(spec.Env["ZDOTDIR"], ".zshrc"))
	if err != nil {
		t.Fatalf("ReadFile(.zshrc) error = %v", err)
	}
	if !strings.Contains(string(data), "source \"$HOME/.zshrc\"") {
		t.Fatalf("expected managed-prompt zshrc to source user rc, got %q", string(data))
	}
	if !strings.Contains(string(data), "PROMPT='%n@%m %~ %# '") {
		t.Fatalf("expected deterministic prompt, got %q", string(data))
	}
	if !strings.Contains(string(data), "SHUTTLE_SEMANTIC_SHELL_STATE_FILE") {
		t.Fatalf("expected managed prompt zshrc to preload semantic state, got %q", string(data))
	}
	if !strings.Contains(string(data), "__shuttle_semantic_precmd") {
		t.Fatalf("expected managed prompt zshrc to register semantic precmd, got %q", string(data))
	}
}

func TestExecutionLaunchSpecManagedMinimalBashUsesEnvI(t *testing.T) {
	runtimeDir := t.TempDir()
	fakeBash := filepath.Join(runtimeDir, "bash")
	if err := os.WriteFile(fakeBash, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(fake bash) error = %v", err)
	}
	originalLookPath := shellLookPath
	shellLookPath = func(file string) (string, error) {
		if file == "bash" {
			return fakeBash, nil
		}
		return "", os.ErrNotExist
	}
	defer func() {
		shellLookPath = originalLookPath
	}()
	spec, err := ExecutionLaunchSpec(runtimeDir, LaunchProfiles{
		Persistent: DefaultPersistentLaunchProfile(),
		Execution: LaunchProfile{
			Mode:         LaunchModeManagedMinimal,
			Shell:        ShellTypeBash,
			SourceUserRC: false,
			InheritEnv:   false,
		},
	})
	if err != nil {
		t.Fatalf("ExecutionLaunchSpec() error = %v", err)
	}
	if spec.Env != nil {
		t.Fatalf("expected minimal env to inline env -i, got %#v", spec.Env)
	}
	if !strings.Contains(spec.Command, "env -i") {
		t.Fatalf("expected env -i command, got %q", spec.Command)
	}
	if !strings.Contains(spec.Command, "--rcfile") {
		t.Fatalf("expected bash rcfile launch, got %q", spec.Command)
	}
	rcPath := filepath.Join(runtimeDir, "shell-launch", "execution-bashrc")
	data, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("ReadFile(bashrc) error = %v", err)
	}
	if strings.Contains(string(data), ". \"$HOME/.bashrc\"") {
		t.Fatalf("did not expect managed-minimal bashrc to source user rc, got %q", string(data))
	}
	if !strings.Contains(string(data), "SHUTTLE_SEMANTIC_SHELL_STATE_FILE") {
		t.Fatalf("expected managed-minimal bashrc to preload semantic state, got %q", string(data))
	}
	if !strings.Contains(string(data), "__shuttle_semantic_precmd;__shuttle_reset_prompt") {
		t.Fatalf("expected bash prompt command to run semantic precmd before prompt reset, got %q", string(data))
	}
}

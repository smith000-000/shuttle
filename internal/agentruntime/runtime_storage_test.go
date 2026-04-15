package agentruntime

import (
	"os"
	"path/filepath"
	"testing"

	"aiterm/internal/config"
)

func TestSaveAndApplyStoredRuntimeConfig(t *testing.T) {
	stateDir := t.TempDir()
	command := filepath.Join(stateDir, "codex")
	if err := SaveStoredRuntimeConfig(stateDir, RuntimeCodexAppServer, command); err != nil {
		t.Fatalf("SaveStoredRuntimeConfig() error = %v", err)
	}

	cfg, err := ApplyStoredRuntimeConfig(config.Config{StateDir: stateDir})
	if err != nil {
		t.Fatalf("ApplyStoredRuntimeConfig() error = %v", err)
	}
	if cfg.RuntimeType != RuntimeCodexAppServer {
		t.Fatalf("expected stored runtime type, got %q", cfg.RuntimeType)
	}
	if cfg.RuntimeCommand != command {
		t.Fatalf("expected stored runtime command, got %q", cfg.RuntimeCommand)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "runtime.json")); err != nil {
		t.Fatalf("expected runtime.json to exist: %v", err)
	}
}

func TestApplyStoredRuntimeConfigSkipsExplicitFlags(t *testing.T) {
	stateDir := t.TempDir()
	if err := SaveStoredRuntimeConfig(stateDir, RuntimeCodexSDK, "/usr/local/bin/codex"); err != nil {
		t.Fatalf("SaveStoredRuntimeConfig() error = %v", err)
	}

	cfg, err := ApplyStoredRuntimeConfig(config.Config{
		StateDir:        stateDir,
		RuntimeType:     RuntimeBuiltin,
		RuntimeCommand:  "",
		RuntimeFlagsSet: true,
	})
	if err != nil {
		t.Fatalf("ApplyStoredRuntimeConfig() error = %v", err)
	}
	if cfg.RuntimeType != RuntimeBuiltin {
		t.Fatalf("expected explicit runtime to be preserved, got %q", cfg.RuntimeType)
	}
}

func TestSaveLoadAndDeleteStoredCodexAppServerThreadBinding(t *testing.T) {
	stateDir := t.TempDir()
	if err := SaveStoredCodexAppServerThreadBinding(stateDir, "shuttle", "task-123", "thread-abc"); err != nil {
		t.Fatalf("SaveStoredCodexAppServerThreadBinding() error = %v", err)
	}

	threadID, ok, err := LoadStoredCodexAppServerThreadBinding(stateDir, "shuttle", "task-123")
	if err != nil {
		t.Fatalf("LoadStoredCodexAppServerThreadBinding() error = %v", err)
	}
	if !ok || threadID != "thread-abc" {
		t.Fatalf("expected stored thread binding, got ok=%v thread=%q", ok, threadID)
	}

	if err := DeleteStoredCodexAppServerThreadBinding(stateDir, "shuttle", "task-123"); err != nil {
		t.Fatalf("DeleteStoredCodexAppServerThreadBinding() error = %v", err)
	}
	threadID, ok, err = LoadStoredCodexAppServerThreadBinding(stateDir, "shuttle", "task-123")
	if err != nil {
		t.Fatalf("LoadStoredCodexAppServerThreadBinding() after delete error = %v", err)
	}
	if ok || threadID != "" {
		t.Fatalf("expected binding removal, got ok=%v thread=%q", ok, threadID)
	}
}

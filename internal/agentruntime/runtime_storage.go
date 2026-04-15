package agentruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aiterm/internal/config"
	"aiterm/internal/securefs"
)

const storedRuntimeVersion = 1

type persistedRuntimeConfig struct {
	Version int    `json:"version"`
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
}

type persistedCodexAppServerThreads struct {
	Version  int                                      `json:"version"`
	Bindings map[string]persistedCodexAppServerThread `json:"bindings,omitempty"`
}

type persistedCodexAppServerThread struct {
	ThreadID string `json:"thread_id"`
}

func ApplyStoredRuntimeConfig(cfg config.Config) (config.Config, error) {
	if cfg.RuntimeFlagsSet {
		return cfg, nil
	}

	stored, ok, err := LoadStoredRuntimeConfig(cfg.StateDir)
	if err != nil || !ok {
		return cfg, err
	}

	cfg.RuntimeType = normalizeStoredRuntimeType(stored.Type)
	cfg.RuntimeCommand = strings.TrimSpace(stored.Command)
	return cfg, nil
}

func SaveStoredRuntimeConfig(stateDir string, runtimeType string, runtimeCommand string) error {
	if strings.TrimSpace(stateDir) == "" {
		return errors.New("state dir must not be empty")
	}
	if err := securefs.EnsurePrivateDir(stateDir); err != nil {
		return fmt.Errorf("create runtime state dir: %w", err)
	}

	stored := persistedRuntimeConfig{
		Version: storedRuntimeVersion,
		Type:    normalizeStoredRuntimeType(runtimeType),
		Command: strings.TrimSpace(runtimeCommand),
	}
	if stored.Type == "" {
		stored.Type = RuntimeBuiltin
	}
	if stored.Type == RuntimeBuiltin {
		stored.Command = ""
	}

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime config: %w", err)
	}
	if err := securefs.WriteAtomicPrivate(runtimeConfigPath(stateDir), data, 0o600); err != nil {
		return fmt.Errorf("write runtime config: %w", err)
	}
	return nil
}

func LoadStoredRuntimeConfig(stateDir string) (persistedRuntimeConfig, bool, error) {
	path := runtimeConfigPath(stateDir)
	data, _, err := securefs.ReadFileNoFollow(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return persistedRuntimeConfig{}, false, nil
		}
		return persistedRuntimeConfig{}, false, fmt.Errorf("read runtime config: %w", err)
	}

	var stored persistedRuntimeConfig
	if err := json.Unmarshal(data, &stored); err != nil {
		return persistedRuntimeConfig{}, false, fmt.Errorf("decode runtime config: %w", err)
	}
	stored.Type = normalizeStoredRuntimeType(stored.Type)
	stored.Command = strings.TrimSpace(stored.Command)
	if stored.Type == "" {
		stored.Type = RuntimeBuiltin
	}
	return stored, true, nil
}

func runtimeConfigPath(stateDir string) string {
	return filepath.Join(strings.TrimSpace(stateDir), "runtime.json")
}

func normalizeStoredRuntimeType(value string) string {
	switch strings.TrimSpace(value) {
	case RuntimeBuiltin, RuntimeAuto, RuntimePi, RuntimeCodexSDK, RuntimeCodexAppServer:
		return strings.TrimSpace(value)
	default:
		return RuntimeBuiltin
	}
}

func LoadStoredCodexAppServerThreadBinding(stateDir string, sessionName string, taskID string) (string, bool, error) {
	if strings.TrimSpace(stateDir) == "" || strings.TrimSpace(sessionName) == "" || strings.TrimSpace(taskID) == "" {
		return "", false, nil
	}

	stored, ok, err := loadStoredCodexAppServerThreads(stateDir)
	if err != nil || !ok {
		return "", false, err
	}
	binding, ok := stored.Bindings[codexAppServerThreadBindingKey(sessionName, taskID)]
	if !ok || strings.TrimSpace(binding.ThreadID) == "" {
		return "", false, nil
	}
	return strings.TrimSpace(binding.ThreadID), true, nil
}

func SaveStoredCodexAppServerThreadBinding(stateDir string, sessionName string, taskID string, threadID string) error {
	if strings.TrimSpace(stateDir) == "" || strings.TrimSpace(sessionName) == "" || strings.TrimSpace(taskID) == "" || strings.TrimSpace(threadID) == "" {
		return nil
	}
	stored, _, err := loadStoredCodexAppServerThreads(stateDir)
	if err != nil {
		return err
	}
	if stored.Version == 0 {
		stored.Version = storedRuntimeVersion
	}
	if stored.Bindings == nil {
		stored.Bindings = map[string]persistedCodexAppServerThread{}
	}
	stored.Bindings[codexAppServerThreadBindingKey(sessionName, taskID)] = persistedCodexAppServerThread{
		ThreadID: strings.TrimSpace(threadID),
	}
	return writeStoredCodexAppServerThreads(stateDir, stored)
}

func DeleteStoredCodexAppServerThreadBinding(stateDir string, sessionName string, taskID string) error {
	if strings.TrimSpace(stateDir) == "" || strings.TrimSpace(sessionName) == "" || strings.TrimSpace(taskID) == "" {
		return nil
	}
	stored, ok, err := loadStoredCodexAppServerThreads(stateDir)
	if err != nil || !ok {
		return err
	}
	delete(stored.Bindings, codexAppServerThreadBindingKey(sessionName, taskID))
	return writeStoredCodexAppServerThreads(stateDir, stored)
}

func loadStoredCodexAppServerThreads(stateDir string) (persistedCodexAppServerThreads, bool, error) {
	path := codexAppServerThreadsPath(stateDir)
	data, _, err := securefs.ReadFileNoFollow(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return persistedCodexAppServerThreads{}, false, nil
		}
		return persistedCodexAppServerThreads{}, false, fmt.Errorf("read codex app server threads: %w", err)
	}

	var stored persistedCodexAppServerThreads
	if err := json.Unmarshal(data, &stored); err != nil {
		return persistedCodexAppServerThreads{}, false, fmt.Errorf("decode codex app server threads: %w", err)
	}
	if stored.Bindings == nil {
		stored.Bindings = map[string]persistedCodexAppServerThread{}
	}
	return stored, true, nil
}

func writeStoredCodexAppServerThreads(stateDir string, stored persistedCodexAppServerThreads) error {
	if strings.TrimSpace(stateDir) == "" {
		return nil
	}
	if err := securefs.EnsurePrivateDir(stateDir); err != nil {
		return fmt.Errorf("create runtime state dir: %w", err)
	}
	if len(stored.Bindings) == 0 {
		if err := os.Remove(codexAppServerThreadsPath(stateDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove codex app server threads: %w", err)
		}
		return nil
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal codex app server threads: %w", err)
	}
	if err := securefs.WriteAtomicPrivate(codexAppServerThreadsPath(stateDir), data, 0o600); err != nil {
		return fmt.Errorf("write codex app server threads: %w", err)
	}
	return nil
}

func codexAppServerThreadsPath(stateDir string) string {
	return filepath.Join(strings.TrimSpace(stateDir), "codex_app_server_threads.json")
}

func codexAppServerThreadBindingKey(sessionName string, taskID string) string {
	return strings.TrimSpace(sessionName) + "\x00" + strings.TrimSpace(taskID)
}

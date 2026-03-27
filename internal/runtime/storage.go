package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aiterm/internal/config"
	"aiterm/internal/securefs"
)

const storedRuntimeVersion = 1

type storedRuntimeRegistry struct {
	Version    int                       `json:"version"`
	Workspaces map[string]WorkspaceState `json:"workspaces"`
}

func ApplyStoredRuntimeConfig(cfg config.Config) (config.Config, error) {
	if cfg.RuntimeFlagsSet {
		return cfg, nil
	}
	state, ok, err := LoadWorkspaceState(cfg.StateDir, cfg.WorkspaceID)
	if err != nil || !ok {
		return cfg, err
	}
	if strings.TrimSpace(string(state.RuntimeID)) != "" {
		cfg.RuntimeType = string(state.RuntimeID)
	}
	if strings.TrimSpace(state.RuntimeCommand) != "" {
		cfg.RuntimeCommand = state.RuntimeCommand
	}
	return cfg, nil
}

func LoadWorkspaceState(stateDir string, workspaceID string) (WorkspaceState, bool, error) {
	registry, ok, err := loadRegistry(stateDir)
	if err != nil || !ok {
		return WorkspaceState{}, false, err
	}
	state, ok := registry.Workspaces[strings.TrimSpace(workspaceID)]
	return state, ok, nil
}

func SaveWorkspaceState(stateDir string, workspaceID string, state WorkspaceState) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return errors.New("workspace id must not be empty")
	}
	registry, _, err := loadRegistry(stateDir)
	if err != nil {
		return err
	}
	if registry.Workspaces == nil {
		registry.Workspaces = map[string]WorkspaceState{}
	}
	registry.Version = storedRuntimeVersion
	registry.Workspaces[workspaceID] = state
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime registry: %w", err)
	}
	if err := securefs.WriteAtomicPrivate(registryPath(stateDir), data, 0o600); err != nil {
		return fmt.Errorf("write runtime registry: %w", err)
	}
	return nil
}

func GrantPIDirectTools(stateDir string, workspaceID string, granted bool) (WorkspaceState, error) {
	state, _, err := LoadWorkspaceState(stateDir, workspaceID)
	if err != nil {
		return WorkspaceState{}, err
	}
	state.PIDirectToolsOK = granted
	if err := SaveWorkspaceState(stateDir, workspaceID, state); err != nil {
		return WorkspaceState{}, err
	}
	return state, nil
}

func MarkExternalHistory(stateDir string, workspaceID string, runtimeID ID, resumable bool) (WorkspaceState, error) {
	state, _, err := LoadWorkspaceState(stateDir, workspaceID)
	if err != nil {
		return WorkspaceState{}, err
	}
	state.ExternalHasHistory = true
	state.ExternalRuntimeID = runtimeID
	state.ExternalWorkedAt = time.Now().UTC()
	state.ExternalResumable = resumable
	if err := SaveWorkspaceState(stateDir, workspaceID, state); err != nil {
		return WorkspaceState{}, err
	}
	return state, nil
}

func SaveConfirmationPreference(stateDir string, workspaceID string, confirm bool) error {
	state, _, err := LoadWorkspaceState(stateDir, workspaceID)
	if err != nil {
		return err
	}
	state.ConfirmExternalHandoff = boolPtr(confirm)
	return SaveWorkspaceState(stateDir, workspaceID, state)
}

func registryPath(stateDir string) string {
	return filepath.Join(stateDir, "runtime-registry.json")
}

func loadRegistry(stateDir string) (storedRuntimeRegistry, bool, error) {
	data, _, err := securefs.ReadFileNoFollow(registryPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return storedRuntimeRegistry{
			Version:    storedRuntimeVersion,
			Workspaces: map[string]WorkspaceState{},
		}, false, nil
	}
	if err != nil {
		return storedRuntimeRegistry{}, false, fmt.Errorf("read runtime registry: %w", err)
	}
	var registry storedRuntimeRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return storedRuntimeRegistry{}, false, fmt.Errorf("decode runtime registry: %w", err)
	}
	if registry.Version != storedRuntimeVersion {
		return storedRuntimeRegistry{}, false, fmt.Errorf("unsupported runtime registry version %d", registry.Version)
	}
	if registry.Workspaces == nil {
		registry.Workspaces = map[string]WorkspaceState{}
	}
	return registry, true, nil
}

func boolPtr(value bool) *bool {
	return &value
}

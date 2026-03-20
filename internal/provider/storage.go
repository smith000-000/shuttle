package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"aiterm/internal/config"

	"github.com/zalando/go-keyring"
)

const (
	storedProviderVersion = 1
	keyringServiceName    = "Shuttle"
	keyringSourceLabel    = "os_keyring"
)

var (
	keyringSet    = keyring.Set
	keyringGet    = keyring.Get
	keyringDelete = keyring.Delete
)

type persistedProviderConfig struct {
	Version    int    `json:"version"`
	Provider   string `json:"provider"`
	AuthMethod string `json:"auth_method"`
	BaseURL    string `json:"base_url"`
	Model      string `json:"model"`
	APIKeyRef  string `json:"api_key_ref,omitempty"`
	CLICommand string `json:"cli_command,omitempty"`
}

func ApplyStoredProviderConfig(cfg config.Config) (config.Config, error) {
	if cfg.ProviderFlagsSet {
		return cfg, nil
	}

	stored, ok, err := LoadStoredProviderConfig(cfg.StateDir)
	if err != nil || !ok {
		return cfg, err
	}

	return applyPersistedConfig(cfg, stored)
}

func SaveStoredProviderConfig(stateDir string, profile Profile) error {
	if strings.TrimSpace(stateDir) == "" {
		return errors.New("state dir must not be empty")
	}
	if err := os.MkdirAll(providersDirPath(stateDir), 0o700); err != nil {
		return fmt.Errorf("create provider state dir: %w", err)
	}

	stored, err := persistableProviderConfig(stateDir, profile)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal provider config: %w", err)
	}
	if err := os.WriteFile(providerConfigPath(stateDir, profile.Preset), data, 0o600); err != nil {
		return fmt.Errorf("write provider config: %w", err)
	}
	if err := os.WriteFile(selectedProviderPath(stateDir), []byte(strings.TrimSpace(string(profile.Preset))+"\n"), 0o600); err != nil {
		return fmt.Errorf("write selected provider: %w", err)
	}

	return nil
}

func LoadStoredProviderConfig(stateDir string) (persistedProviderConfig, bool, error) {
	selectedPreset, err := loadSelectedProviderPreset(stateDir)
	if err != nil {
		return persistedProviderConfig{}, false, err
	}
	if selectedPreset != "" {
		if stored, ok, err := loadPersistedProviderConfigAtPath(providerConfigPath(stateDir, selectedPreset)); err != nil {
			return persistedProviderConfig{}, false, err
		} else if ok {
			return stored, true, nil
		}
	}

	storedConfigs, err := loadStoredProviderConfigs(stateDir)
	if err != nil {
		return persistedProviderConfig{}, false, err
	}
	if len(storedConfigs) > 0 {
		return storedConfigs[0], true, nil
	}

	return loadLegacyStoredProviderConfig(stateDir)
}

func LoadStoredProviderProfiles(stateDir string) ([]Profile, ProviderPreset, error) {
	selectedPreset, err := loadSelectedProviderPreset(stateDir)
	if err != nil {
		return nil, "", err
	}

	storedConfigs, err := loadStoredProviderConfigs(stateDir)
	if err != nil {
		return nil, "", err
	}
	if len(storedConfigs) == 0 {
		legacy, ok, err := loadLegacyStoredProviderConfig(stateDir)
		if err != nil {
			return nil, "", err
		}
		if !ok {
			return nil, "", nil
		}
		storedConfigs = []persistedProviderConfig{legacy}
		if selectedPreset == "" {
			selectedPreset = ProviderPreset(strings.TrimSpace(legacy.Provider))
		}
	}

	profiles := make([]Profile, 0, len(storedConfigs))
	for _, stored := range storedConfigs {
		profile, err := persistedProfile(stateDir, stored)
		if err != nil {
			return nil, "", err
		}
		profiles = append(profiles, profile)
	}

	sort.Slice(profiles, func(i int, j int) bool {
		leftSelected := profiles[i].Preset == selectedPreset
		rightSelected := profiles[j].Preset == selectedPreset
		if leftSelected != rightSelected {
			return leftSelected
		}
		return profiles[i].Name < profiles[j].Name
	})

	return profiles, selectedPreset, nil
}

func persistableProviderConfig(stateDir string, profile Profile) (persistedProviderConfig, error) {
	apiKeyRef := ""
	if profile.AuthMethod == AuthAPIKey {
		switch {
		case shouldPersistProviderEnvRef(profile):
			apiKeyRef = strings.TrimSpace(profile.APIKeyEnvVar)
			_ = keyringDelete(keyringServiceName, providerKeyAccount(stateDir, profile.Preset))
		case strings.TrimSpace(profile.APIKey) != "":
			apiKeyRef = keyringSourceLabel
			if err := keyringSet(keyringServiceName, providerKeyAccount(stateDir, profile.Preset), profile.APIKey); err != nil {
				return persistedProviderConfig{}, fmt.Errorf("store provider API key: %w", err)
			}
		default:
			return persistedProviderConfig{}, errors.New("cannot persist provider without an API key value or env reference")
		}
	} else {
		_ = keyringDelete(keyringServiceName, providerKeyAccount(stateDir, profile.Preset))
	}

	return persistedProviderConfig{
		Version:    storedProviderVersion,
		Provider:   string(profile.Preset),
		AuthMethod: string(profile.AuthMethod),
		BaseURL:    strings.TrimSpace(profile.BaseURL),
		Model:      strings.TrimSpace(profile.Model),
		APIKeyRef:  apiKeyRef,
		CLICommand: strings.TrimSpace(profile.CLICommand),
	}, nil
}

func applyPersistedConfig(cfg config.Config, stored persistedProviderConfig) (config.Config, error) {
	cfg.ProviderType = stored.Provider
	cfg.ProviderAuthMethod = stored.AuthMethod
	cfg.ProviderBaseURL = stored.BaseURL
	cfg.ProviderModel = stored.Model
	cfg.ProviderCLICommand = stored.CLICommand
	cfg.ProviderAPIKey = ""
	cfg.ProviderAPIKeyEnvVar = stored.APIKeyRef

	profile, err := ResolveProfile(cfg)
	if err != nil {
		return cfg, err
	}
	if profile.AuthMethod == AuthAPIKey {
		apiKey, apiKeyRef, err := loadPersistedAPIKey(cfg.StateDir, ProviderPreset(strings.TrimSpace(stored.Provider)), stored.APIKeyRef)
		if err != nil {
			return cfg, err
		}
		cfg.ProviderAPIKey = apiKey
		cfg.ProviderAPIKeyEnvVar = apiKeyRef
	}

	return cfg, nil
}

func persistedProfile(stateDir string, stored persistedProviderConfig) (Profile, error) {
	cfg, err := applyPersistedConfig(config.Config{StateDir: stateDir}, stored)
	if err != nil {
		return Profile{}, err
	}
	return ResolveProfile(cfg)
}

func loadPersistedAPIKey(stateDir string, preset ProviderPreset, apiKeyRef string) (string, string, error) {
	switch strings.TrimSpace(apiKeyRef) {
	case "", keyringSourceLabel:
		apiKey, err := keyringGet(keyringServiceName, providerKeyAccount(stateDir, preset))
		if err == nil {
			return apiKey, keyringSourceLabel, nil
		}
		legacyKey, legacyErr := keyringGet(keyringServiceName, legacyProviderKeyAccount(stateDir))
		if legacyErr == nil {
			return legacyKey, keyringSourceLabel, nil
		}
		return "", "", fmt.Errorf("load stored provider API key: %w", err)
	default:
		return strings.TrimSpace(os.Getenv(apiKeyRef)), strings.TrimSpace(apiKeyRef), nil
	}
}

func loadStoredProviderConfigs(stateDir string) ([]persistedProviderConfig, error) {
	entries, err := os.ReadDir(providersDirPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read providers dir: %w", err)
	}

	storedConfigs := make([]persistedProviderConfig, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		stored, ok, err := loadPersistedProviderConfigAtPath(filepath.Join(providersDirPath(stateDir), entry.Name()))
		if err != nil {
			return nil, err
		}
		if ok {
			storedConfigs = append(storedConfigs, stored)
		}
	}

	sort.Slice(storedConfigs, func(i int, j int) bool {
		return storedConfigs[i].Provider < storedConfigs[j].Provider
	})

	return storedConfigs, nil
}

func loadPersistedProviderConfigAtPath(path string) (persistedProviderConfig, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return persistedProviderConfig{}, false, nil
	}
	if err != nil {
		return persistedProviderConfig{}, false, fmt.Errorf("read provider config: %w", err)
	}

	var stored persistedProviderConfig
	if err := json.Unmarshal(data, &stored); err != nil {
		return persistedProviderConfig{}, false, fmt.Errorf("decode provider config: %w", err)
	}
	if stored.Version != storedProviderVersion {
		return persistedProviderConfig{}, false, fmt.Errorf("unsupported provider config version %d", stored.Version)
	}
	if strings.TrimSpace(stored.Provider) == "" {
		return persistedProviderConfig{}, false, errors.New("stored provider config is missing provider")
	}

	return stored, true, nil
}

func loadLegacyStoredProviderConfig(stateDir string) (persistedProviderConfig, bool, error) {
	return loadPersistedProviderConfigAtPath(legacyProviderConfigPath(stateDir))
}

func loadSelectedProviderPreset(stateDir string) (ProviderPreset, error) {
	data, err := os.ReadFile(selectedProviderPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read selected provider: %w", err)
	}

	return ProviderPreset(strings.TrimSpace(string(data))), nil
}

func providersDirPath(stateDir string) string {
	return filepath.Join(stateDir, "providers")
}

func providerConfigPath(stateDir string, preset ProviderPreset) string {
	return filepath.Join(providersDirPath(stateDir), string(preset)+".json")
}

func selectedProviderPath(stateDir string) string {
	return filepath.Join(stateDir, "selected-provider")
}

func legacyProviderConfigPath(stateDir string) string {
	return filepath.Join(stateDir, "provider.json")
}

func providerKeyAccount(stateDir string, preset ProviderPreset) string {
	sum := sha256.Sum256([]byte(stateDir + "|" + string(preset)))
	return "provider-" + hex.EncodeToString(sum[:8])
}

func legacyProviderKeyAccount(stateDir string) string {
	sum := sha256.Sum256([]byte(stateDir))
	return "active-provider-" + hex.EncodeToString(sum[:8])
}

func shouldPersistProviderEnvRef(profile Profile) bool {
	ref := strings.TrimSpace(profile.APIKeyEnvVar)
	if ref == "" || ref == keyringSourceLabel {
		return false
	}
	apiKey := strings.TrimSpace(profile.APIKey)
	if apiKey == "" {
		return true
	}

	return strings.TrimSpace(os.Getenv(ref)) == apiKey
}

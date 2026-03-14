package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		switch strings.TrimSpace(stored.APIKeyRef) {
		case "", keyringSourceLabel:
			apiKey, err := keyringGet(keyringServiceName, providerKeyAccount(cfg.StateDir))
			if err != nil {
				return cfg, fmt.Errorf("load stored provider API key: %w", err)
			}
			cfg.ProviderAPIKey = apiKey
			cfg.ProviderAPIKeyEnvVar = keyringSourceLabel
		default:
			cfg.ProviderAPIKey = strings.TrimSpace(os.Getenv(stored.APIKeyRef))
		}
	}

	return cfg, nil
}

func SaveStoredProviderConfig(stateDir string, profile Profile) error {
	if strings.TrimSpace(stateDir) == "" {
		return errors.New("state dir must not be empty")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	apiKeyRef := ""
	if profile.AuthMethod == AuthAPIKey {
		switch {
		case shouldPersistProviderEnvRef(profile):
			apiKeyRef = strings.TrimSpace(profile.APIKeyEnvVar)
			_ = keyringDelete(keyringServiceName, providerKeyAccount(stateDir))
		case strings.TrimSpace(profile.APIKey) != "":
			apiKeyRef = keyringSourceLabel
			if err := keyringSet(keyringServiceName, providerKeyAccount(stateDir), profile.APIKey); err != nil {
				return fmt.Errorf("store provider API key: %w", err)
			}
		default:
			return errors.New("cannot persist provider without an API key value or env reference")
		}
	} else {
		_ = keyringDelete(keyringServiceName, providerKeyAccount(stateDir))
	}

	data, err := json.MarshalIndent(persistedProviderConfig{
		Version:    storedProviderVersion,
		Provider:   string(profile.Preset),
		AuthMethod: string(profile.AuthMethod),
		BaseURL:    strings.TrimSpace(profile.BaseURL),
		Model:      strings.TrimSpace(profile.Model),
		APIKeyRef:  apiKeyRef,
		CLICommand: strings.TrimSpace(profile.CLICommand),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal provider config: %w", err)
	}

	if err := os.WriteFile(providerConfigPath(stateDir), data, 0o600); err != nil {
		return fmt.Errorf("write provider config: %w", err)
	}

	return nil
}

func LoadStoredProviderConfig(stateDir string) (persistedProviderConfig, bool, error) {
	path := providerConfigPath(stateDir)
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

func providerConfigPath(stateDir string) string {
	return filepath.Join(stateDir, "provider.json")
}

func providerKeyAccount(stateDir string) string {
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

package controller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"aiterm/internal/shell"
)

const (
	remoteCapabilitiesFileName  = "remote_capabilities.json"
	remoteCapabilityFreshness   = 24 * time.Hour
	remoteCapabilityNegativeTTL = 10 * time.Minute
)

type remoteCapabilityStore struct {
	path    string
	mu      sync.Mutex
	loaded  bool
	entries map[string]remoteCapabilityRecord
}

type remoteCapabilityRecord struct {
	Key                     string         `json:"key"`
	User                    string         `json:"user"`
	Host                    string         `json:"host"`
	TargetKind              string         `json:"target_kind"`
	System                  string         `json:"system,omitempty"`
	OSRelease               string         `json:"os_release,omitempty"`
	ShellFamily             string         `json:"shell_family,omitempty"`
	Git                     bool           `json:"git"`
	Python3                 bool           `json:"python3"`
	Base64                  bool           `json:"base64"`
	Mktemp                  bool           `json:"mktemp"`
	LastProbeSucceeded      bool           `json:"last_probe_succeeded"`
	LastValidated           time.Time      `json:"last_validated"`
	LastSuccessfulTransport PatchTransport `json:"last_successful_transport,omitempty"`
}

func newRemoteCapabilityStore(stateDir string) *remoteCapabilityStore {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return nil
	}
	return &remoteCapabilityStore{
		path: filepath.Join(stateDir, remoteCapabilitiesFileName),
	}
}

func (s *remoteCapabilityStore) summaryForShell(location *shell.ShellLocation, prompt *shell.PromptContext) *RemoteCapabilitySummary {
	if s == nil {
		return nil
	}
	record, ok := s.recordForShell(location, prompt)
	if !ok {
		return nil
	}
	return record.summary("cached")
}

func (s *remoteCapabilityStore) recordForShell(location *shell.ShellLocation, prompt *shell.PromptContext) (remoteCapabilityRecord, bool) {
	key := remoteCapabilityKeyForShell(location, prompt)
	if s == nil || key == "" {
		return remoteCapabilityRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		s.loadLocked()
	}
	record, ok := s.entries[key]
	if !ok || record.stale() {
		return remoteCapabilityRecord{}, false
	}
	return record, true
}

func (s *remoteCapabilityStore) saveRecord(record remoteCapabilityRecord) {
	if s == nil || strings.TrimSpace(record.Key) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		s.loadLocked()
	}
	if s.entries == nil {
		s.entries = map[string]remoteCapabilityRecord{}
	}
	s.entries[record.Key] = record
	s.saveLocked()
}

func (s *remoteCapabilityStore) markTransport(location *shell.ShellLocation, prompt *shell.PromptContext, transport PatchTransport) {
	key := remoteCapabilityKeyForShell(location, prompt)
	if s == nil || key == "" || transport == PatchTransportNone {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		s.loadLocked()
	}
	record, ok := s.entries[key]
	if !ok {
		return
	}
	record.LastSuccessfulTransport = transport
	record.LastValidated = time.Now().UTC()
	s.entries[key] = record
	s.saveLocked()
}

func (s *remoteCapabilityStore) loadLocked() {
	s.loaded = true
	s.entries = map[string]remoteCapabilityRecord{}
	if strings.TrimSpace(s.path) == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil || len(data) == 0 {
		return
	}
	var entries []remoteCapabilityRecord
	if err := json.Unmarshal(data, &entries); err != nil {
		return
	}
	for _, entry := range entries {
		key := strings.TrimSpace(entry.Key)
		if key == "" {
			continue
		}
		s.entries[key] = entry
	}
}

func (s *remoteCapabilityStore) saveLocked() {
	if strings.TrimSpace(s.path) == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return
	}
	entries := make([]remoteCapabilityRecord, 0, len(s.entries))
	for _, entry := range s.entries {
		entries = append(entries, entry)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.path, data, 0o600)
}

func remoteCapabilityKey(user string, host string) string {
	user = strings.TrimSpace(strings.ToLower(user))
	host = strings.TrimSpace(strings.ToLower(host))
	if user == "" && host == "" {
		return ""
	}
	if user == "" {
		return host
	}
	if host == "" {
		return user
	}
	return user + "@" + host
}

func remoteCapabilityKeyForShell(location *shell.ShellLocation, prompt *shell.PromptContext) string {
	effective := effectiveShellLocation(location, prompt)
	if effective.Kind != shell.ShellLocationRemote {
		return ""
	}
	user := ""
	host := ""
	user = effective.User
	host = effective.Host
	return remoteCapabilityKey(user, host)
}

func (r remoteCapabilityRecord) stale() bool {
	if r.LastValidated.IsZero() {
		return true
	}
	ttl := remoteCapabilityFreshness
	if r.hasNegativeCapability() || !r.LastProbeSucceeded {
		ttl = remoteCapabilityNegativeTTL
	}
	return time.Since(r.LastValidated) > ttl
}

func (r remoteCapabilityRecord) hasNegativeCapability() bool {
	return !r.Git || !r.Python3 || !r.Base64 || !r.Mktemp
}

func (r remoteCapabilityRecord) summary(source string) *RemoteCapabilitySummary {
	return &RemoteCapabilitySummary{
		Identity:                r.Key,
		System:                  strings.TrimSpace(r.System),
		OSRelease:               strings.TrimSpace(r.OSRelease),
		ShellFamily:             strings.TrimSpace(r.ShellFamily),
		Source:                  strings.TrimSpace(source),
		LastSuccessfulTransport: r.LastSuccessfulTransport,
		Git:                     r.Git,
		Python3:                 r.Python3,
		Base64:                  r.Base64,
		Mktemp:                  r.Mktemp,
	}
}

func (c *LocalController) refreshRemoteCapabilityHintLocked() {
	if !isRemoteShellLocation(c.session.CurrentShellLocation, c.session.CurrentShell) {
		c.session.RemoteCapabilities = nil
		return
	}
	if c.remoteCaps == nil {
		return
	}
	c.session.RemoteCapabilities = c.remoteCaps.summaryForShell(c.session.CurrentShellLocation, c.session.CurrentShell)
}

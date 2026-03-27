package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"aiterm/internal/config"
	"aiterm/internal/controller"
	"aiterm/internal/provider"
)

func TestShouldStartPINewSession(t *testing.T) {
	tests := []struct {
		name                    string
		switchedSession         bool
		preserveExternalSession bool
		storedTaskID            string
		inputTaskID             string
		want                    bool
	}{
		{
			name:            "starts fresh when no session was restored",
			switchedSession: false,
			storedTaskID:    "task-1",
			inputTaskID:     "task-1",
			want:            true,
		},
		{
			name:                    "preserves restored session across Shuttle task changes",
			switchedSession:         true,
			preserveExternalSession: true,
			storedTaskID:            "task-1",
			inputTaskID:             "task-2",
			want:                    false,
		},
		{
			name:            "starts fresh when restored session is for a different Shuttle task",
			switchedSession: true,
			storedTaskID:    "task-1",
			inputTaskID:     "task-2",
			want:            true,
		},
		{
			name:            "keeps restored session for the same Shuttle task",
			switchedSession: true,
			storedTaskID:    "task-1",
			inputTaskID:     "task-1",
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldStartPINewSession(tt.switchedSession, tt.preserveExternalSession, tt.storedTaskID, tt.inputTaskID); got != tt.want {
				t.Fatalf("shouldStartPINewSession() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPIAgentPreservesSessionOnExplicitResumeAcrossTaskIDs(t *testing.T) {
	fakePI := buildFakePITestBinary(t)
	stateDir := t.TempDir()
	workspaceDir := t.TempDir()

	cfg := config.Config{
		StateDir:    stateDir,
		WorkspaceID: "workspace-test",
		StartDir:    workspaceDir,
	}
	profile := provider.Profile{
		Preset:     provider.PresetOpenAI,
		Name:       "OpenAI",
		Model:      "gpt-5-test",
		AuthMethod: provider.AuthAPIKey,
		APIKey:     "test-key",
	}
	selection := Selection{
		ID:              RuntimePi,
		Command:         fakePI,
		ProviderAllowed: true,
		Granted:         true,
	}

	firstAgent, err := NewPIAgent(cfg, profile, selection, WorkspaceState{}, nil)
	if err != nil {
		t.Fatalf("NewPIAgent(first) error = %v", err)
	}
	firstResponse, err := firstAgent.Respond(context.Background(), controller.AgentInput{
		Task:   controller.TaskContext{TaskID: "task-1"},
		Prompt: "start external work",
	})
	if err != nil {
		t.Fatalf("Respond(first) error = %v", err)
	}
	if firstResponse.Message == "" {
		t.Fatal("expected first PI response message")
	}

	state, ok, err := LoadWorkspaceState(stateDir, cfg.WorkspaceID)
	if err != nil {
		t.Fatalf("LoadWorkspaceState(first) error = %v", err)
	}
	if !ok {
		t.Fatal("expected persisted PI workspace state after first response")
	}
	firstSessionFile := state.PISessionFile
	firstSessionID := state.PISessionID
	if firstSessionFile == "" || firstSessionID == "" {
		t.Fatalf("expected persisted PI session metadata, got %#v", state)
	}

	resumeAgent, err := NewPIAgent(cfg, profile, selection, state, nil)
	if err != nil {
		t.Fatalf("NewPIAgent(resume) error = %v", err)
	}
	resumeResponse, err := resumeAgent.Respond(context.Background(), controller.AgentInput{
		Task:                    controller.TaskContext{TaskID: "task-2"},
		Prompt:                  "resume external work",
		PreserveExternalSession: true,
	})
	if err != nil {
		t.Fatalf("Respond(resume) error = %v", err)
	}
	if resumeResponse.Message == "" {
		t.Fatal("expected resume PI response message")
	}

	resumedState, ok, err := LoadWorkspaceState(stateDir, cfg.WorkspaceID)
	if err != nil {
		t.Fatalf("LoadWorkspaceState(resume) error = %v", err)
	}
	if !ok {
		t.Fatal("expected persisted PI workspace state after resume response")
	}
	if resumedState.PISessionFile != firstSessionFile {
		t.Fatalf("expected resumed session file %q, got %q", firstSessionFile, resumedState.PISessionFile)
	}
	if resumedState.PISessionID != firstSessionID {
		t.Fatalf("expected resumed session id %q, got %q", firstSessionID, resumedState.PISessionID)
	}
	if resumedState.PITaskID != "task-2" {
		t.Fatalf("expected Shuttle task id to advance to task-2, got %q", resumedState.PITaskID)
	}
}

func buildFakePITestBinary(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}

	buildDir := t.TempDir()
	binaryPath := filepath.Join(buildDir, "fake-pi-runtime")
	cacheDir := filepath.Join(buildDir, "gocache")
	tmpDir := filepath.Join(buildDir, "gotmp")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(gocache) error = %v", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(gotmpdir) error = %v", err)
	}

	command := exec.Command("go", "build", "-o", binaryPath, "./integration/harness/cmd/fakepi")
	command.Dir = repoRoot
	command.Env = append(os.Environ(),
		"GOCACHE="+cacheDir,
		"GOTMPDIR="+tmpDir,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go build fake PI runtime error = %v\n%s", err, string(output))
	}
	return binaryPath
}

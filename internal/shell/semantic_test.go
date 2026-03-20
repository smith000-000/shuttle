package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aiterm/internal/protocol"
	"aiterm/internal/tmux"
)

func TestParseSemanticShellStatePrompt(t *testing.T) {
	state, ok := parseSemanticShellState("{\"event\":\"prompt\",\"exit\":130,\"cwd\":\"/tmp/demo\",\"shell\":\"zsh\"}")
	if !ok {
		t.Fatal("expected semantic shell state to parse")
	}
	if state.Event != semanticEventPrompt {
		t.Fatalf("expected prompt event, got %#v", state)
	}
	if state.ExitCode == nil || *state.ExitCode != 130 {
		t.Fatalf("expected exit code 130, got %#v", state.ExitCode)
	}
	if state.Directory != "/tmp/demo" || state.Shell != "zsh" {
		t.Fatalf("unexpected semantic state %#v", state)
	}
}

func TestReadSemanticShellState(t *testing.T) {
	dir := t.TempDir()
	path := semanticStatePath(dir, "/dev/pts/7")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{\"event\":\"command\",\"exit\":null,\"cwd\":\"/home/jsmith\",\"shell\":\"bash\"}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	state, ok := readSemanticShellState(dir, "/dev/pts/7")
	if !ok {
		t.Fatal("expected semantic shell state to be read")
	}
	if state.Event != semanticEventCommand {
		t.Fatalf("expected command event, got %#v", state)
	}
	if state.ExitCode != nil {
		t.Fatalf("expected nil exit code, got %#v", state.ExitCode)
	}
	if state.Directory != "/home/jsmith" {
		t.Fatalf("unexpected directory %#v", state)
	}
	if state.UpdatedAt.IsZero() || time.Since(state.UpdatedAt) > time.Minute {
		t.Fatalf("expected recent mod time, got %v", state.UpdatedAt)
	}
}

func TestSynthesizePromptContextUsesSemanticDirectory(t *testing.T) {
	base := PromptContext{User: "jsmith", Host: "linuxdesktop", PromptSymbol: "%"}
	state := semanticShellState{Directory: "/home/jsmith/source/repos/aiterm"}

	context := synthesizePromptContext(base, state)
	if context.Directory != "~/source/repos/aiterm" {
		t.Fatalf("expected shortened semantic cwd, got %#v", context)
	}
}

func TestEnsureLocalShellIntegrationSendsSourceCommand(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane:    tmux.Pane{ID: "%0", CurrentCommand: "bash", TTY: "/dev/pts/7"},
		capture: "jsmith@linuxdesktop ~/source/repos/aiterm %",
	}
	dir := t.TempDir()
	observer := (&Observer{client: client}).WithStateDir(dir)

	if err := observer.EnsureLocalShellIntegration(context.Background(), "%0"); err != nil {
		t.Fatalf("EnsureLocalShellIntegration() error = %v", err)
	}
	if len(client.sent) != 1 {
		t.Fatalf("expected one install command, got %#v", client.sent)
	}
	if !strings.Contains(client.sent[0], "SHUTTLE_SEMANTIC_SHELL_V1") {
		t.Fatalf("expected install guard in command, got %q", client.sent[0])
	}
	if !strings.Contains(client.sent[0], ". '/") {
		t.Fatalf("expected sourced integration script, got %q", client.sent[0])
	}
}

func TestRunTrackedMonitorCompletesFromSemanticPromptState(t *testing.T) {
	dir := t.TempDir()
	client := &fakeSemanticPaneClient{
		pane:    tmux.Pane{ID: "%0", CurrentCommand: "bash", TTY: "/dev/pts/9"},
		capture: "jsmith@linuxdesktop ~/source/repos/aiterm %",
	}
	observer := (&Observer{client: client}).WithStateDir(dir).WithPromptHint(GuessLocalContext("/home/jsmith/source/repos/aiterm"))
	monitor := newTrackedCommandMonitor("cmd-1", "sleep 1")
	markers := protocol.Markers{
		CommandID: "cmd-1",
		BeginLine: "__SHUTTLE_B__:cmd-1",
		EndPrefix: "__SHUTTLE_E__:cmd-1:",
	}

	statePath := semanticStatePath(dir, client.pane.TTY)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	go func() {
		time.Sleep(80 * time.Millisecond)
		_ = os.WriteFile(statePath, []byte("{\"event\":\"command\",\"exit\":null,\"cwd\":\"/home/jsmith/source/repos/aiterm\",\"shell\":\"bash\"}\n"), 0o644)
		time.Sleep(80 * time.Millisecond)
		_ = os.WriteFile(statePath, []byte("{\"event\":\"prompt\",\"exit\":0,\"cwd\":\"/home/jsmith/source/repos/aiterm\",\"shell\":\"bash\"}\n"), 0o644)
	}()

	observer.runTrackedMonitor(context.Background(), monitor, "%0", "sleep 1", "sleep 1", 250*time.Millisecond, client.capture, markers, func() {})
	result, err := monitor.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if result.State != MonitorStateCompleted {
		t.Fatalf("expected semantic completion, got %#v", result)
	}
	if result.ExitCode != 0 || result.Confidence != ConfidenceStrong {
		t.Fatalf("expected strong semantic completion, got %#v", result)
	}
}

type fakeSemanticPaneClient struct {
	mu      sync.Mutex
	pane    tmux.Pane
	capture string
	sent    []string
}

func (f *fakeSemanticPaneClient) CapturePane(ctx context.Context, target string, startLine int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.capture, nil
}

func (f *fakeSemanticPaneClient) SendKeys(ctx context.Context, target string, command string, pressEnter bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, command)
	return nil
}

func (f *fakeSemanticPaneClient) PaneInfo(ctx context.Context, target string) (tmux.Pane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pane, nil
}

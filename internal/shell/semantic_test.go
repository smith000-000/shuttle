package shell

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
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
	projectDir := filepath.Join(dir, "workspace", "project")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{\"event\":\"command\",\"exit\":null,\"cwd\":\""+projectDir+"\",\"shell\":\"bash\"}\n"), 0o644); err != nil {
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
	if state.Directory != projectDir {
		t.Fatalf("unexpected directory %#v", state)
	}
	if state.UpdatedAt.IsZero() || time.Since(state.UpdatedAt) > time.Minute {
		t.Fatalf("expected recent mod time, got %v", state.UpdatedAt)
	}
}

func TestSynthesizePromptContextUsesSemanticDirectory(t *testing.T) {
	projectDir := shellTestProjectDir(t)
	base := PromptContext{User: shellTestUser, Host: shellTestHost, PromptSymbol: "%"}
	state := semanticShellState{Directory: projectDir}

	context := synthesizePromptContext(base, state)
	if context.Directory != "~/workspace/project" {
		t.Fatalf("expected shortened semantic cwd, got %#v", context)
	}
}

func TestEnsureLocalShellIntegrationSendsSourceCommand(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane:    tmux.Pane{ID: "%0", CurrentCommand: "bash", TTY: "/dev/pts/7"},
		capture: shellTestProjectPrompt(t),
	}
	dir := t.TempDir()
	observer := (&Observer{client: client}).WithStateDir(dir)

	if err := observer.EnsureLocalShellIntegration(context.Background(), "%0"); err != nil {
		t.Fatalf("EnsureLocalShellIntegration() error = %v", err)
	}
	if len(client.sent) != 1 {
		t.Fatalf("expected one install command, got %#v", client.sent)
	}
	if !strings.Contains(client.sent[0], "SHUTTLE_SEMANTIC_SHELL_V1_PID") || !strings.Contains(client.sent[0], "\"$$\"") {
		t.Fatalf("expected per-shell pid guard in command, got %q", client.sent[0])
	}
	if !strings.Contains(client.sent[0], "shell-integration/") {
		t.Fatalf("expected wrapped install command to source semantic integration script, got %q", client.sent[0])
	}
	if !strings.Contains(client.sent[0], "__SHUTTLE_B__:") || !strings.Contains(client.sent[0], "__SHUTTLE_E__:") {
		t.Fatalf("expected semantic install command to be wrapped for synchronous completion, got %q", client.sent[0])
	}
}

func TestSemanticShellScriptsUseSTTerminators(t *testing.T) {
	bashScript := bashSemanticShellIntegrationScript("/tmp/bash.state")
	zshScript := zshSemanticShellIntegrationScript("/tmp/zsh.state")

	if !strings.Contains(bashScript, `printf '\033]133;%s\033\\' "$1"`) {
		t.Fatalf("expected bash semantic script to emit OSC 133 with ST terminator, got %q", bashScript)
	}
	if !strings.Contains(zshScript, `printf '\033]133;%s\033\\' "$1"`) {
		t.Fatalf("expected zsh semantic script to emit OSC 133 with ST terminator, got %q", zshScript)
	}
	if !strings.Contains(bashScript, `printf '\033]7;file://%s%s\033\\'`) {
		t.Fatalf("expected bash semantic script to emit OSC 7 with ST terminator, got %q", bashScript)
	}
	if !strings.Contains(zshScript, `printf '\033]7;file://%s%s\033\\'`) {
		t.Fatalf("expected zsh semantic script to emit OSC 7 with ST terminator, got %q", zshScript)
	}
	if !strings.Contains(bashScript, `export SHUTTLE_SEMANTIC_SHELL_V1_PID=$$`) {
		t.Fatalf("expected bash semantic script to record owning shell pid, got %q", bashScript)
	}
	if !strings.Contains(zshScript, `export SHUTTLE_SEMANTIC_SHELL_V1_PID=$$`) {
		t.Fatalf("expected zsh semantic script to record owning shell pid, got %q", zshScript)
	}
}

func TestFinalizeTransitionStateBootstrapsLocalNestedShellAndClearsSuppression(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane:    tmux.Pane{ID: "%0", CurrentCommand: "bash", TTY: "/dev/pts/10"},
		capture: shellTestProjectPrompt(t),
	}
	dir := t.TempDir()
	observer := (&Observer{client: client}).WithStateDir(dir)

	observer.finalizeTransitionState(context.Background(), "%0", "bash", "bash", GuessLocalContext(shellTestProjectDir(t)))

	if got := observer.rememberedTransition("%0"); got != shellTransitionNone {
		t.Fatalf("expected local bootstrap to clear remembered transition, got %q", got)
	}
	if len(client.sent) != 1 {
		t.Fatalf("expected one bootstrap command, got %#v", client.sent)
	}
}

func TestFinalizeTransitionStateKeepsSuppressionWhenLocalShellIsUnsupported(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane:    tmux.Pane{ID: "%0", CurrentCommand: "fish", TTY: "/dev/pts/11"},
		capture: "~/workspace/project >",
	}
	dir := t.TempDir()
	observer := (&Observer{client: client}).WithStateDir(dir)

	observer.finalizeTransitionState(context.Background(), "%0", "fish", "fish", GuessLocalContext(shellTestProjectDir(t)))

	if got := observer.rememberedTransition("%0"); got != shellTransitionLocal {
		t.Fatalf("expected unsupported local shell to keep remembered local transition, got %q", got)
	}
	if len(client.sent) != 0 {
		t.Fatalf("expected no bootstrap command for unsupported shell, got %#v", client.sent)
	}
}

func TestRunTrackedMonitorCompletesFromSemanticPromptState(t *testing.T) {
	dir := t.TempDir()
	projectDir := shellTestProjectDir(t)
	client := &fakeSemanticPaneClient{
		pane:    tmux.Pane{ID: "%0", CurrentCommand: "bash", TTY: "/dev/pts/9"},
		capture: shellTestProjectPrompt(t),
	}
	observer := (&Observer{client: client}).WithStateDir(dir).WithPromptHint(GuessLocalContext(projectDir))
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
		_ = os.WriteFile(statePath, []byte("{\"event\":\"command\",\"exit\":null,\"cwd\":\""+projectDir+"\",\"shell\":\"bash\"}\n"), 0o644)
		time.Sleep(80 * time.Millisecond)
		_ = os.WriteFile(statePath, []byte("{\"event\":\"prompt\",\"exit\":0,\"cwd\":\""+projectDir+"\",\"shell\":\"bash\"}\n"), 0o644)
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
	mu                 sync.Mutex
	pane               tmux.Pane
	panes              []tmux.Pane
	panesByTarget      map[string]tmux.Pane
	paneErrByTarget    map[string]error
	listPanes          []tmux.Pane
	listPanesErr       error
	capture            string
	captures           []string
	capturesByTarget   map[string]string
	captureErrByTarget map[string]error
	escaped            string
	stream             string
	dir                string
	sent               []string
	piped              []string
	pipeTargets        []string
	pipeErrByTarget    map[string]error
	captureReads       int
	paneReads          int
}

func (f *fakeSemanticPaneClient) CapturePane(ctx context.Context, target string, startLine int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.captureErrByTarget[target]; err != nil {
		return "", err
	}
	if captured, ok := f.capturesByTarget[target]; ok {
		return captured, nil
	}
	if len(f.captures) > 0 {
		index := f.captureReads
		if index >= len(f.captures) {
			index = len(f.captures) - 1
		}
		f.captureReads++
		return f.captures[index], nil
	}
	return f.capture, nil
}

func (f *fakeSemanticPaneClient) SendKeys(ctx context.Context, target string, command string, pressEnter bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, command)
	if beginLine, endPrefix, ok := wrappedMarkerLines(command); ok && len(f.captures) == 0 {
		prompt := strings.TrimSpace(f.capture)
		if prompt == "" {
			prompt = "/workspace/project %"
		}
		f.captures = []string{
			prompt,
			prompt + "\n" + beginLine + "\n" + endPrefix + "0\n" + prompt,
		}
		f.captureReads = 0
	}
	return nil
}

func (f *fakeSemanticPaneClient) PaneInfo(ctx context.Context, target string) (tmux.Pane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.paneErrByTarget[target]; err != nil {
		return tmux.Pane{}, err
	}
	if pane, ok := f.panesByTarget[target]; ok {
		return pane, nil
	}
	if len(f.panes) > 0 {
		index := f.paneReads
		if index >= len(f.panes) {
			index = len(f.panes) - 1
		}
		f.paneReads++
		return f.panes[index], nil
	}
	return f.pane, nil
}

func (f *fakeSemanticPaneClient) ListPanes(ctx context.Context, target string) ([]tmux.Pane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listPanesErr != nil {
		return nil, f.listPanesErr
	}
	if len(f.listPanes) > 0 {
		panes := make([]tmux.Pane, len(f.listPanes))
		copy(panes, f.listPanes)
		return panes, nil
	}
	if len(f.panes) > 0 {
		panes := make([]tmux.Pane, len(f.panes))
		copy(panes, f.panes)
		return panes, nil
	}
	if (f.pane != tmux.Pane{}) {
		return []tmux.Pane{f.pane}, nil
	}
	return nil, nil
}

func (f *fakeSemanticPaneClient) CapturePaneEscaped(ctx context.Context, target string, startLine int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.escaped, nil
}

func (f *fakeSemanticPaneClient) PipePaneOutput(ctx context.Context, target string, shellCommand string) error {
	f.mu.Lock()
	if err := f.pipeErrByTarget[target]; err != nil {
		f.mu.Unlock()
		return err
	}
	f.piped = append(f.piped, shellCommand)
	f.pipeTargets = append(f.pipeTargets, target)
	stream := f.stream
	f.mu.Unlock()

	streamPath := pipePaneOutputPath(shellCommand)
	if strings.TrimSpace(streamPath) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(streamPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(streamPath, []byte(stream), 0o600)
}

var (
	beginLinePattern = regexp.MustCompile(`__SHUTTLE_B__:[a-z0-9]+`)
	endPrefixPattern = regexp.MustCompile(`__SHUTTLE_E__:[a-z0-9]+:`)
)

func wrappedMarkerLines(command string) (string, string, bool) {
	beginLine := beginLinePattern.FindString(command)
	endPrefix := endPrefixPattern.FindString(command)
	if beginLine == "" || endPrefix == "" {
		return "", "", false
	}
	return beginLine, endPrefix, true
}

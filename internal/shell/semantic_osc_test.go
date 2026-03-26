package shell

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestParseSemanticShellStateFromOSCCapture(t *testing.T) {
	raw := "\x1b]133;B\x07run\n\x1b]7;file://workstation/workspace/project\x07\x1b]133;D;130\x07\x1b]133;A\x07"
	state, ok := parseSemanticShellStateFromOSCCapture(raw)
	if !ok {
		t.Fatal("expected osc semantic state to parse")
	}
	if state.Event != semanticEventPrompt {
		t.Fatalf("expected prompt event, got %#v", state)
	}
	if state.ExitCode == nil || *state.ExitCode != 130 {
		t.Fatalf("expected exit code 130, got %#v", state.ExitCode)
	}
	if state.Directory != "/workspace/project" {
		t.Fatalf("unexpected directory %#v", state)
	}
}

func TestSemanticOSCStreamReducerHandlesIncrementalChunks(t *testing.T) {
	var reducer semanticOSCStreamReducer

	reducer.Feed([]byte("noise\x1b]133;B\x1b"), testTime(1))
	if _, ok := reducer.State(); ok {
		t.Fatal("expected incomplete OSC chunk not to produce state yet")
	}

	reducer.Feed([]byte("\\echo hi\n\x1b]133;C\x1b\\"), testTime(2))
	state, ok := reducer.State()
	if !ok {
		t.Fatal("expected command state after completed B/C markers")
	}
	if state.Event != semanticEventCommand {
		t.Fatalf("expected command event, got %#v", state)
	}
	if state.ExitCode != nil {
		t.Fatalf("expected nil exit code while command is running, got %#v", state.ExitCode)
	}

	reducer.Feed([]byte("\x1b]7;file://localhost/tmp/stream-demo\x1b\\\x1b]133;D;17\x1b\\"), testTime(3))
	state, ok = reducer.State()
	if !ok {
		t.Fatal("expected state after cwd update")
	}
	if state.Event != semanticEventCommand {
		t.Fatalf("expected command event before prompt marker, got %#v", state)
	}
	if state.Directory != "/tmp/stream-demo" {
		t.Fatalf("expected updated directory, got %#v", state)
	}
	if state.ExitCode != nil {
		t.Fatalf("expected exit code to remain pending until prompt marker, got %#v", state.ExitCode)
	}

	reducer.Feed([]byte("\x1b]133;A\x1b\\"), testTime(4))
	state, ok = reducer.State()
	if !ok {
		t.Fatal("expected prompt state after A marker")
	}
	if state.Event != semanticEventPrompt {
		t.Fatalf("expected prompt event, got %#v", state)
	}
	if state.ExitCode == nil || *state.ExitCode != 17 {
		t.Fatalf("expected exit code 17, got %#v", state.ExitCode)
	}
}

func testTime(second int) time.Time {
	return time.Unix(int64(second), 0)
}

func TestSemanticStreamCollectorUsesGenerationScopedPaths(t *testing.T) {
	dir := t.TempDir()

	first := newStreamSemanticCollector(fakePipePaneClient{}, dir)
	second := newStreamSemanticCollector(fakePipePaneClient{}, dir)

	firstStream := first.paneStream("%1", "/dev/pts/21")
	secondStream := second.paneStream("%1", "/dev/pts/21")

	if firstStream.path == secondStream.path {
		t.Fatalf("expected generation-scoped paths, got same path %q", firstStream.path)
	}
	if pipePaneGenerationID(firstStream.path) == pipePaneGenerationID(secondStream.path) {
		t.Fatalf("expected different generation ids, got %q and %q", firstStream.path, secondStream.path)
	}
}

func TestPipePaneOutputPathParsesQuotedTarget(t *testing.T) {
	command := "umask 077; cat > '/tmp/demo/'\"'\"'quoted'\"'\"'.log'"
	got := pipePaneOutputPath(command)
	want := "/tmp/demo/'quoted'.log"
	if got != want {
		t.Fatalf("pipePaneOutputPath() = %q, want %q", got, want)
	}
}

func TestCleanupStaleSemanticStreamGenerationsPrunesDeadProcessesOnly(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "semantic-stream")
	currentPID := os.Getpid()
	aliveGeneration := "session-4242-1"
	deadGeneration := "session-31337-1"
	currentGeneration := "session-" + strconv.Itoa(currentPID) + "-1"

	for _, generationID := range []string{aliveGeneration, deadGeneration, currentGeneration} {
		path := filepath.Join(root, generationID)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", path, err)
		}
		if err := os.WriteFile(filepath.Join(path, "pane.log"), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %q: %v", path, err)
		}
	}

	previous := semanticStreamProcessAlive
	semanticStreamProcessAlive = func(pid int) bool {
		return pid == 4242 || pid == currentPID
	}
	t.Cleanup(func() {
		semanticStreamProcessAlive = previous
	})

	collector := &streamSemanticCollector{stateDir: dir, generationID: currentGeneration}
	if err := collector.cleanupStaleGenerations(); err != nil {
		t.Fatalf("cleanupStaleGenerations() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, deadGeneration)); !os.IsNotExist(err) {
		t.Fatalf("expected dead generation to be pruned, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, aliveGeneration)); err != nil {
		t.Fatalf("expected alive generation to remain, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, currentGeneration)); err != nil {
		t.Fatalf("expected current generation to remain, stat err = %v", err)
	}
}

func TestSemanticStreamGenerationPIDParsesSessionIDs(t *testing.T) {
	pid, ok := semanticStreamGenerationPID("session-1234-5678")
	if !ok || pid != 1234 {
		t.Fatalf("semanticStreamGenerationPID() = (%d, %v), want (1234, true)", pid, ok)
	}
	if _, ok := semanticStreamGenerationPID("nonsense"); ok {
		t.Fatal("expected invalid generation id to fail parsing")
	}
}

type fakePipePaneClient struct{}

func (fakePipePaneClient) PipePaneOutput(context.Context, string, string) error { return nil }

package integration

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiterm/internal/shell"
	"aiterm/internal/tmux"
)

func waitForObservedPrompt(t *testing.T, ctx context.Context, observer *shell.Observer, paneID string) shell.PromptContext {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for {
		promptContext, err := observer.CaptureShellContext(ctx, paneID)
		if err == nil && promptContext.PromptLine() != "" {
			return promptContext
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("timed out waiting for prompt context: %v", err)
			}
			t.Fatal("timed out waiting for prompt context")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRunTrackedCommand(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socketName := fmt.Sprintf("shuttle-track-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-track-%d", time.Now().UnixNano())

	client, err := tmux.NewClient(socketName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Cleanup(func() {
		_ = client.KillSession(context.Background(), sessionName)
	})

	workspace, _, err := tmux.BootstrapWorkspace(ctx, client, tmux.BootstrapOptions{
		SessionName:       sessionName,
		StartDir:          ".",
		BottomPanePercent: 30,
		HistoryFile:       filepath.Join(t.TempDir(), "shell_history"),
	})
	if err != nil {
		t.Fatalf("BootstrapWorkspace() error = %v", err)
	}

	observer := shell.NewObserver(client)
	result, err := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, "printf 'alpha\\n'; false", 10*time.Second)
	if err != nil {
		t.Fatalf("RunTrackedCommand() error = %v", err)
	}

	if result.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", result.ExitCode)
	}

	if !strings.Contains(result.Captured, "alpha") {
		t.Fatalf("expected captured body to contain alpha, got %q", result.Captured)
	}
}

func TestManagedShellProfilesKeepTrackedOutputAndContextTransitionsClean(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socketName := fmt.Sprintf("shuttle-track-managed-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-track-managed-%d", time.Now().UnixNano())

	client, err := tmux.NewClient(socketName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	t.Cleanup(func() {
		_ = client.KillSession(context.Background(), sessionName)
	})

	runtimeDir := t.TempDir()
	launchProfiles := shell.DefaultLaunchProfiles()
	persistentLaunch, err := shell.PersistentLaunchSpec(runtimeDir, launchProfiles)
	if err != nil {
		t.Fatalf("PersistentLaunchSpec() error = %v", err)
	}

	workspace, _, err := tmux.BootstrapWorkspace(ctx, client, tmux.BootstrapOptions{
		SessionName:       sessionName,
		StartDir:          "..",
		BottomPanePercent: 30,
		HistoryFile:       filepath.Join(t.TempDir(), "shell_history"),
		Launch:            persistentLaunch,
	})
	if err != nil {
		t.Fatalf("BootstrapWorkspace() error = %v", err)
	}

	observer := shell.NewObserver(client).WithStateDir(runtimeDir).WithSessionName(sessionName).WithStartDir("..").WithLaunchProfiles(launchProfiles)

	lsResult, err := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, "ls", 10*time.Second)
	if err != nil {
		t.Fatalf("RunTrackedCommand(ls) error = %v", err)
	}
	if !strings.Contains(lsResult.Captured, "AGENTS.md") {
		t.Fatalf("expected managed shell ls output to survive, got %q", lsResult.Captured)
	}

	lsSingleColumnResult, err := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, "ls -1", 10*time.Second)
	if err != nil {
		t.Fatalf("RunTrackedCommand(ls -1) error = %v", err)
	}
	if !strings.Contains(lsSingleColumnResult.Captured, "AGENTS.md") {
		t.Fatalf("expected managed shell ls -1 output to survive, got %q", lsSingleColumnResult.Captured)
	}

	cdResult, err := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, "cd inprocess", shell.CommandTimeout("cd inprocess"))
	if err != nil {
		t.Fatalf("RunTrackedCommand(cd inprocess) error = %v", err)
	}
	if strings.Contains(cdResult.Captured, "shell-integration/") || strings.Contains(cdResult.Captured, "runtime/commands/") {
		t.Fatalf("expected clean managed-shell context transition output, got %q", cdResult.Captured)
	}

	pwdResult, err := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, "pwd", 10*time.Second)
	if err != nil {
		t.Fatalf("RunTrackedCommand(pwd) error = %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(pwdResult.Captured), "/inprocess") {
		t.Fatalf("expected managed-shell cwd to update after cd, got %q", pwdResult.Captured)
	}
}

func TestOwnedExecutionPanePreservesFindOutputUnderManagedExecutionProfile(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socketName := fmt.Sprintf("shuttle-track-owned-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-track-owned-%d", time.Now().UnixNano())

	client, err := tmux.NewClient(socketName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("filepath.Abs(..) error = %v", err)
	}

	t.Cleanup(func() {
		_ = client.KillSession(context.Background(), sessionName)
	})

	runtimeDir := t.TempDir()
	launchProfiles := shell.DefaultLaunchProfiles()
	persistentLaunch, err := shell.PersistentLaunchSpec(runtimeDir, launchProfiles)
	if err != nil {
		t.Fatalf("PersistentLaunchSpec() error = %v", err)
	}

	workspace, _, err := tmux.BootstrapWorkspace(ctx, client, tmux.BootstrapOptions{
		SessionName:       sessionName,
		StartDir:          repoRoot,
		BottomPanePercent: 30,
		HistoryFile:       filepath.Join(t.TempDir(), "shell_history"),
		Launch:            persistentLaunch,
	})
	if err != nil {
		t.Fatalf("BootstrapWorkspace() error = %v", err)
	}

	observer := shell.NewObserver(client).WithStateDir(runtimeDir).WithSessionName(sessionName).WithStartDir(repoRoot).WithLaunchProfiles(launchProfiles)
	ownedPane, cleanup, err := observer.CreateOwnedExecutionPane(ctx, repoRoot)
	if err != nil {
		t.Fatalf("CreateOwnedExecutionPane() error = %v", err)
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup(context.Background())
		}
	}()

	waitForObservedPrompt(t, ctx, observer, ownedPane.PaneID)

	result, err := observer.RunTrackedCommand(ctx, ownedPane.PaneID, "find . -maxdepth 2 -type f | sort", 10*time.Second)
	if err != nil {
		t.Fatalf("RunTrackedCommand(find) error = %v", err)
	}
	if !strings.Contains(result.Captured, "./AGENTS.md") {
		t.Fatalf("expected owned execution find output to include ./AGENTS.md, got %q", result.Captured)
	}
	if strings.Contains(result.Captured, "runtime/commands/") {
		t.Fatalf("expected owned execution result to hide transport wrapper chatter, got %q", result.Captured)
	}

	_ = workspace
}

func TestRunTrackedInteractiveCommand(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socketName := fmt.Sprintf("shuttle-track-interactive-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-track-interactive-%d", time.Now().UnixNano())

	client, err := tmux.NewClient(socketName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Cleanup(func() {
		_ = client.KillSession(context.Background(), sessionName)
	})

	workspace, _, err := tmux.BootstrapWorkspace(ctx, client, tmux.BootstrapOptions{
		SessionName:       sessionName,
		StartDir:          ".",
		BottomPanePercent: 30,
		HistoryFile:       filepath.Join(t.TempDir(), "shell_history"),
	})
	if err != nil {
		t.Fatalf("BootstrapWorkspace() error = %v", err)
	}

	observer := shell.NewObserver(client)
	results := make(chan shell.TrackedExecution, 1)
	errs := make(chan error, 1)
	go func() {
		result, runErr := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, `bash -lc 'read -n 1 -s -r -p "Press any key to continue..." _; echo ready'`, 10*time.Second)
		if runErr != nil {
			errs <- runErr
			return
		}
		results <- result
	}()

	time.Sleep(500 * time.Millisecond)
	if err := client.SendKeys(ctx, workspace.TopPane.ID, "x", true); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}

	select {
	case runErr := <-errs:
		t.Fatalf("RunTrackedCommand() error = %v", runErr)
	case result := <-results:
		if result.ExitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", result.ExitCode)
		}
		if !strings.Contains(result.Captured, "ready") {
			t.Fatalf("expected captured output to contain ready, got %q", result.Captured)
		}
		if strings.Contains(result.Captured, "command not found: cho") {
			t.Fatalf("expected wrapper not to leak truncated sentinel command, got %q", result.Captured)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for interactive tracked command")
	}
}

func TestStartTrackedCommandDetectsAlternateScreen(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socketName := fmt.Sprintf("shuttle-track-alt-screen-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-track-alt-screen-%d", time.Now().UnixNano())

	client, err := tmux.NewClient(socketName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Cleanup(func() {
		_ = client.KillSession(context.Background(), sessionName)
	})

	workspace, _, err := tmux.BootstrapWorkspace(ctx, client, tmux.BootstrapOptions{
		SessionName:       sessionName,
		StartDir:          ".",
		BottomPanePercent: 30,
		HistoryFile:       filepath.Join(t.TempDir(), "shell_history"),
	})
	if err != nil {
		t.Fatalf("BootstrapWorkspace() error = %v", err)
	}

	observer := shell.NewObserver(client)
	monitor, err := observer.StartTrackedCommand(ctx, workspace.TopPane.ID, `bash -lc 'printf "\033[?1049h"; printf "fullscreen\n"; sleep 1; printf "\033[?1049l"; printf "done\n"'`, 2*time.Second)
	if err != nil {
		t.Fatalf("StartTrackedCommand() error = %v", err)
	}

	sawInteractive := false
	for snapshot := range monitor.Updates() {
		if snapshot.State == shell.MonitorStateInteractiveFullscreen {
			sawInteractive = true
			break
		}
	}

	result, err := monitor.Wait()
	if err != nil {
		t.Fatalf("monitor.Wait() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if !sawInteractive {
		t.Fatalf("expected alternate-screen command to publish interactive fullscreen state, got %#v", result)
	}
}

func TestRunTrackedCommandUsesStartTimeoutNotCompletionTimeout(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socketName := fmt.Sprintf("shuttle-track-start-timeout-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-track-start-timeout-%d", time.Now().UnixNano())

	client, err := tmux.NewClient(socketName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Cleanup(func() {
		_ = client.KillSession(context.Background(), sessionName)
	})

	workspace, _, err := tmux.BootstrapWorkspace(ctx, client, tmux.BootstrapOptions{
		SessionName:       sessionName,
		StartDir:          ".",
		BottomPanePercent: 30,
		HistoryFile:       filepath.Join(t.TempDir(), "shell_history"),
	})
	if err != nil {
		t.Fatalf("BootstrapWorkspace() error = %v", err)
	}

	observer := shell.NewObserver(client)
	result, err := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, "sleep 1; printf 'ready\\n'", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("RunTrackedCommand() error = %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}

	if !strings.Contains(result.Captured, "ready") {
		t.Fatalf("expected captured output to contain ready, got %q", result.Captured)
	}
}

func TestRunTrackedCommandUsesLocalManagedTransport(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socketName := fmt.Sprintf("shuttle-track-local-managed-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-track-local-managed-%d", time.Now().UnixNano())

	client, err := tmux.NewClient(socketName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Cleanup(func() {
		_ = client.KillSession(context.Background(), sessionName)
	})

	workspace, _, err := tmux.BootstrapWorkspace(ctx, client, tmux.BootstrapOptions{
		SessionName:       sessionName,
		StartDir:          ".",
		BottomPanePercent: 30,
		HistoryFile:       filepath.Join(t.TempDir(), "shell_history"),
	})
	if err != nil {
		t.Fatalf("BootstrapWorkspace() error = %v", err)
	}

	stateDir := t.TempDir()
	observer := shell.NewObserver(client).WithStateDir(stateDir).WithPromptHint(shell.GuessLocalContext("."))
	waitForObservedPrompt(t, ctx, observer, workspace.TopPane.ID)
	result, err := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, "printf 'alpha\\n'", 2*time.Second)
	if err != nil {
		t.Fatalf("RunTrackedCommand() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}

	captured, err := client.CapturePane(ctx, workspace.TopPane.ID, -80)
	if err != nil {
		t.Fatalf("CapturePane() error = %v", err)
	}
	normalizedCapture := strings.ReplaceAll(captured, "\n", "")
	if !strings.Contains(normalizedCapture, "/commands/") {
		t.Fatalf("expected local managed transport path in shell capture, got %q", captured)
	}
	if !strings.Contains(normalizedCapture, ". '") || !strings.Contains(normalizedCapture, "/commands/") {
		t.Fatalf("expected tracked command to use sourced managed transport, got %q", captured)
	}
}

func TestRunTrackedCommandPreservesCaptureAfterDirectoryChange(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socketName := fmt.Sprintf("shuttle-track-cd-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-track-cd-%d", time.Now().UnixNano())

	client, err := tmux.NewClient(socketName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Cleanup(func() {
		_ = client.KillSession(context.Background(), sessionName)
	})

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("filepath.Abs(..) error = %v", err)
	}

	workspace, _, err := tmux.BootstrapWorkspace(ctx, client, tmux.BootstrapOptions{
		SessionName:       sessionName,
		StartDir:          repoRoot,
		BottomPanePercent: 30,
		HistoryFile:       filepath.Join(t.TempDir(), "shell_history"),
	})
	if err != nil {
		t.Fatalf("BootstrapWorkspace() error = %v", err)
	}

	stateDir := t.TempDir()
	observer := shell.NewObserver(client).WithStateDir(stateDir).WithPromptHint(shell.GuessLocalContext(repoRoot))
	waitForObservedPrompt(t, ctx, observer, workspace.TopPane.ID)

	result, err := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, "cd completed", 2*time.Second)
	if err != nil {
		t.Fatalf("RunTrackedCommand(cd completed) error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected cd completed exit code 0, got %d with capture %q", result.ExitCode, result.Captured)
	}

	result, err = observer.RunTrackedCommand(ctx, workspace.TopPane.ID, "pwd", 2*time.Second)
	if err != nil {
		t.Fatalf("RunTrackedCommand(pwd) error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected pwd exit code 0, got %d with capture %q", result.ExitCode, result.Captured)
	}

	want := filepath.Join(repoRoot, "completed")
	if result.Captured != want {
		t.Fatalf("expected pwd capture %q after cd, got %q", want, result.Captured)
	}
	for _, fragment := range []string{"status\"", "__SHUTTLE_", ". '/", " %"} {
		if strings.Contains(result.Captured, fragment) {
			t.Fatalf("expected pwd capture to exclude shuttle/prompt fragments %q, got %q", fragment, result.Captured)
		}
	}
}

func TestRunTrackedCommandHandlesFastHighVolumeOutput(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socketName := fmt.Sprintf("shuttle-track-high-volume-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-track-high-volume-%d", time.Now().UnixNano())

	client, err := tmux.NewClient(socketName)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Cleanup(func() {
		_ = client.KillSession(context.Background(), sessionName)
	})

	workspace, _, err := tmux.BootstrapWorkspace(ctx, client, tmux.BootstrapOptions{
		SessionName:       sessionName,
		StartDir:          ".",
		BottomPanePercent: 30,
		HistoryFile:       filepath.Join(t.TempDir(), "shell_history"),
	})
	if err != nil {
		t.Fatalf("BootstrapWorkspace() error = %v", err)
	}

	observer := shell.NewObserver(client)
	result, err := observer.RunTrackedCommand(ctx, workspace.TopPane.ID, "seq 1 5000; printf 'ready\\n'", 2*time.Second)
	if err != nil {
		t.Fatalf("RunTrackedCommand() error = %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}

	if !strings.Contains(result.Captured, "ready") {
		t.Fatalf("expected captured output to contain ready, got %q", result.Captured)
	}
}

package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiterm/internal/protocol"
	"aiterm/internal/tmux"
)

func TestSanitizeCapturedBody(t *testing.T) {
	body := "prompt% echo __SHUTTLE_B__:cmd-1\nprompt% printf 'alpha\\n'; false\nalpha\nprompt% echo __SHUTTLE_E__:cmd-1:1\nabc123:$?"

	got := sanitizeCapturedBody(body)
	want := "prompt% printf 'alpha\\n'; false\nalpha"

	if got != want {
		t.Fatalf("sanitizeCapturedBody() = %q, want %q", got, want)
	}
}

func TestSanitizeCapturedBodyStripsSemanticBootstrapNoise(t *testing.T) {
	body := "localuser@workstation ~/workspace/project % [ -n \"$SHUTTLE_SEMANTIC_SHELL_V1\" ] || . '/run/user/1000/shuttle/shell-integration/zsh-pane0.sh' >/dev/null 2>&1\nlocaluser@workstation ~/workspace/project % .\n '/run/user/1000/shuttle/commands/cmd-1.sh'\n1\n2\n^C"

	got := sanitizeCapturedBody(body)
	want := "1\n2\n^C"

	if got != want {
		t.Fatalf("sanitizeCapturedBody() = %q, want %q", got, want)
	}
}

func TestSanitizeCapturedBodyStripsWrappedShuttleProtocolNoise(t *testing.T) {
	body := `.sh'"'"' >/dev/null 2>&1')"; __shuttle_status=$?; printf '%s%s\n' '__SHUTTLE_E__
:abc123:' "$__shuttle_status"
localuser@workstation ~/workspace/project % bas
h
ull || id -un 2>/dev/null)"' 'printf '"'"'__SHUTTLE_CTX_HOST__=%s\n'"'"' "$(hostname 2>/dev/null || uname -n 2>/dev/null)"'
__SHUTTLE_CTX_EXIT__=0
__SHUTTLE_CTX_USER__=localuser
1
2
^C`

	got := sanitizeCapturedBody(body)
	want := "localuser@workstation ~/workspace/project % bas\nh\n1\n2\n^C"

	if got != want {
		t.Fatalf("sanitizeCapturedBody() = %q, want %q", got, want)
	}
}

func TestSanitizeCapturedBodyPreservesTrackedCommandScriptErrors(t *testing.T) {
	body := ". '/run/user/1000/shuttle/commands/cmd-1.sh'\n__SHUTTLE_B__:cmd-1\n/run/user/1000/shuttle/commands/cmd-1.sh:2: command not found: apply_patch\nlocaluser@workstation ~/workspace/project %"

	got := sanitizeCapturedBody(body)
	if !strings.Contains(got, "command not found: apply_patch") {
		t.Fatalf("expected tracked command error to survive sanitization, got %q", got)
	}
}

func TestSanitizeCapturedBodyPreservesRemotePatchPayloadMarkers(t *testing.T) {
	body := strings.Join([]string{
		"__SHUTTLE_B__:cmd-1",
		"__SHUTTLE_REMOTE_READ__ exists 420",
		"__SHUTTLE_REMOTE_DATA_BEGIN__",
		"aGVsbG8K",
		"__SHUTTLE_REMOTE_DATA_END__",
		"__SHUTTLE_E__:cmd-1:0",
	}, "\n")

	got := sanitizeCapturedBody(body)
	want := strings.Join([]string{
		"__SHUTTLE_REMOTE_READ__ exists 420",
		"__SHUTTLE_REMOTE_DATA_BEGIN__",
		"aGVsbG8K",
		"__SHUTTLE_REMOTE_DATA_END__",
	}, "\n")

	if got != want {
		t.Fatalf("sanitizeCapturedBody() = %q, want %q", got, want)
	}
}

func TestStripEchoedSingleLineCommand(t *testing.T) {
	body := "localuser@devbox % ls\nfile-a\nfile-b"

	got := stripEchoedCommand(body, "ls")
	want := "file-a\nfile-b"

	if got != want {
		t.Fatalf("stripEchoedCommand() = %q, want %q", got, want)
	}
}

func TestStripEchoedMultiLineQuotedCommand(t *testing.T) {
	body := "localuser@devbox % bash -lc '\nquote> set -e\nquote> printf \"## PWD\\n\"; pwd\nquote> '\n## PWD\n/workspace/project"
	command := "bash -lc '\nset -e\nprintf \"## PWD\\n\"; pwd\n'"

	got := stripEchoedCommand(body, command)
	want := "## PWD\n/workspace/project"

	if got != want {
		t.Fatalf("stripEchoedCommand() = %q, want %q", got, want)
	}
}

func TestStripEchoedCommandWithPromptLineBeforeTransportCommand(t *testing.T) {
	body := "localuser@workstation ~/workspace/project git:(main) %\n. '/workspace/project/.shuttle/commands/cmd-1.sh'\n1\n2\n3"
	command := ". '/workspace/project/.shuttle/commands/cmd-1.sh'"

	got := stripEchoedCommand(body, command)
	want := "1\n2\n3"

	if got != want {
		t.Fatalf("stripEchoedCommand() = %q, want %q", got, want)
	}
}

func TestStripEchoedWrappedTransportCommand(t *testing.T) {
	body := "localuser@workstation ~/workspace/project git:(main) %\n. \n '/workspace/project/.shuttle/commands/cmd-1.sh'\n1\n2\n3"
	command := ". '/workspace/project/.shuttle/commands/cmd-1.sh'"

	got := stripEchoedCommand(body, command)
	want := "1\n2\n3"

	if got != want {
		t.Fatalf("stripEchoedCommand() = %q, want %q", got, want)
	}
}

func TestIsContextTransitionCommand(t *testing.T) {
	cases := map[string]bool{
		"ssh prod":                   true,
		"telnet 10.0.0.5":            true,
		"sudo -i":                    true,
		"bash":                       true,
		"zsh -i":                     true,
		"docker exec -it app sh":     true,
		"kubectl exec -it pod -- sh": true,
		"exit":                       true,
		"bash -lc 'echo hi'":         false,
		"ls -lah":                    false,
		"git status":                 false,
		"sudo ls":                    false,
	}

	for command, want := range cases {
		if got := isContextTransitionCommand(command); got != want {
			t.Fatalf("isContextTransitionCommand(%q) = %v, want %v", command, got, want)
		}
	}
}

func TestCommandTimeout(t *testing.T) {
	if got := CommandTimeout("ssh prod"); got != ContextTransitionCommandTimeout {
		t.Fatalf("CommandTimeout(ssh) = %v, want %v", got, ContextTransitionCommandTimeout)
	}

	if got := CommandTimeout("ls -lah"); got != DefaultCommandTimeout {
		t.Fatalf("CommandTimeout(ls -lah) = %v, want %v", got, DefaultCommandTimeout)
	}
}

func TestClassifyActiveMonitorStateTreatsInteractiveCommandAsAwaitingInput(t *testing.T) {
	if got := classifyActiveMonitorState("sudo ls", ObservedShellState{Tail: "[sudo] password for localuser:", CurrentPaneCommand: "sudo"}); got != MonitorStateAwaitingInput {
		t.Fatalf("classifyActiveMonitorState(sudo ls) = %s, want %s", got, MonitorStateAwaitingInput)
	}
}

func TestClassifyActiveMonitorStateTreatsAlternateScreenAsInteractiveFullscreen(t *testing.T) {
	if got := classifyActiveMonitorState("wrapped-btop", ObservedShellState{AlternateOn: true, CurrentPaneCommand: "btop"}); got != MonitorStateInteractiveFullscreen {
		t.Fatalf("classifyActiveMonitorState(alternate screen) = %s, want %s", got, MonitorStateInteractiveFullscreen)
	}
}

func TestClassifyActiveMonitorStateTreatsFullscreenForegroundCommandAsInteractiveFullscreen(t *testing.T) {
	if got := classifyActiveMonitorState("wrapped-alias", ObservedShellState{CurrentPaneCommand: "nano"}); got != MonitorStateInteractiveFullscreen {
		t.Fatalf("classifyActiveMonitorState(foreground nano) = %s, want %s", got, MonitorStateInteractiveFullscreen)
	}
}

func TestClassifyActiveMonitorStateTreatsAwaitingForegroundCommandAsAwaitingInput(t *testing.T) {
	if got := classifyActiveMonitorState("wrapped-alias", ObservedShellState{CurrentPaneCommand: "sudo"}); got != MonitorStateAwaitingInput {
		t.Fatalf("classifyActiveMonitorState(foreground sudo) = %s, want %s", got, MonitorStateAwaitingInput)
	}
}

func TestAllowPromptReturnInferenceDisablesInteractiveCommands(t *testing.T) {
	if allowPromptReturnInference("btop", ObservedShellState{CurrentPaneCommand: "btop"}) {
		t.Fatal("expected fullscreen interactive command to disable prompt-return inference")
	}
	if allowPromptReturnInference("wrapped-btop", ObservedShellState{AlternateOn: true, CurrentPaneCommand: "btop"}) {
		t.Fatal("expected alternate-screen command to disable prompt-return inference")
	}
	if !allowPromptReturnInference("bash -lc 'sleep 5; echo ready'", ObservedShellState{CurrentPaneCommand: "zsh", Location: ShellLocation{Kind: ShellLocationLocal}}) {
		t.Fatal("expected ordinary shell command to allow prompt-return inference")
	}
}

func TestAllowPromptReturnInferenceDisablesNonShellPaneCommands(t *testing.T) {
	if allowPromptReturnInference("bash -lc 'sleep 5; echo ready'", ObservedShellState{CurrentPaneCommand: "nano", Location: ShellLocation{Kind: ShellLocationLocal}}) {
		t.Fatal("expected non-shell foreground command to disable prompt-return inference")
	}
}

func TestAllowPromptReturnInferenceAllowsRemoteTransportPaneCommands(t *testing.T) {
	if !allowPromptReturnInference("bash -lc 'sleep 5; echo ready'", ObservedShellState{CurrentPaneCommand: "ssh", Location: ShellLocation{Kind: ShellLocationRemote}}) {
		t.Fatal("expected ssh pane command to allow prompt-return inference for remote shell reconciliation")
	}
}

func TestShouldIgnoreLocalSemanticStateForRemotePromptContext(t *testing.T) {
	if !shouldIgnoreLocalSemanticState("", "zsh", PromptContext{Remote: true}, shellTransitionNone) {
		t.Fatal("expected remote prompt context to suppress local semantic state")
	}
}

func TestShouldIgnoreLocalSemanticStateForSSHPaneCommand(t *testing.T) {
	if !shouldIgnoreLocalSemanticState("", "ssh", PromptContext{}, shellTransitionNone) {
		t.Fatal("expected ssh pane command to suppress local semantic state")
	}
}

func TestShouldIgnoreLocalSemanticStateForRememberedNestedShell(t *testing.T) {
	if !shouldIgnoreLocalSemanticState("", "bash", PromptContext{}, shellTransitionLocal) {
		t.Fatal("expected remembered nested shell to suppress local semantic state")
	}
}

func TestCaptureSemanticShellStatePrefersOSCCaptureOverStateFile(t *testing.T) {
	dir := t.TempDir()
	client := &fakeSemanticPaneClient{
		pane:    tmux.Pane{ID: "%0", CurrentCommand: "zsh", TTY: "/dev/pts/12"},
		capture: shellTestProjectPrompt(t),
		escaped: "\x1b]133;C\x1b\\\x1b]7;file://workstation/workspace/project\x1b\\",
	}
	statePath := semanticStatePath(dir, client.pane.TTY)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte("{\"event\":\"prompt\",\"exit\":0,\"cwd\":\"/tmp/fallback\",\"shell\":\"zsh\"}\n"), 0o644); err != nil {
		t.Fatalf("write state file: %v", err)
	}

	observer := (&Observer{client: client}).WithStateDir(dir)
	state, source, ok := observer.captureSemanticShellState(context.Background(), "%0", client.pane.TTY, "", client.pane.CurrentCommand, PromptContext{})
	if !ok {
		t.Fatal("expected semantic shell state")
	}
	if source != semanticSourceOSCCapture {
		t.Fatalf("expected osc capture source, got %q", source)
	}
	if state.Event != semanticEventCommand {
		t.Fatalf("expected command event, got %#v", state)
	}
	if state.Directory != "/workspace/project" {
		t.Fatalf("expected osc capture cwd, got %#v", state)
	}
}

func TestCaptureSemanticShellStatePrefersStreamOverOSCCaptureAndStateFile(t *testing.T) {
	dir := t.TempDir()
	client := &fakeSemanticPaneClient{
		pane:    tmux.Pane{ID: "%0", CurrentCommand: "zsh", TTY: "/dev/pts/14"},
		escaped: "\x1b]133;C\x1b\\\x1b]7;file://workstation/tmp/capture-fallback\x1b\\",
		stream:  "\x1b]133;B\x1b\\\x1b]133;C\x1b\\\x1b]7;file://localhost/tmp/stream-primary\x1b\\\x1b]133;D;7\x1b\\\x1b]133;A\x1b\\",
	}
	statePath := semanticStatePath(dir, client.pane.TTY)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte("{\"event\":\"prompt\",\"exit\":0,\"cwd\":\"/tmp/state-fallback\",\"shell\":\"zsh\"}\n"), 0o644); err != nil {
		t.Fatalf("write state file: %v", err)
	}

	observer := (&Observer{client: client}).WithStateDir(dir)
	state, source, ok := observer.captureSemanticShellState(context.Background(), "%0", client.pane.TTY, "", client.pane.CurrentCommand, PromptContext{})
	if !ok {
		t.Fatal("expected semantic shell state")
	}
	if source != semanticSourceStream {
		t.Fatalf("expected stream source, got %q", source)
	}
	if state.Event != semanticEventPrompt {
		t.Fatalf("expected prompt event, got %#v", state)
	}
	if state.Directory != "/tmp/stream-primary" {
		t.Fatalf("expected stream cwd, got %#v", state)
	}
	if state.ExitCode == nil || *state.ExitCode != 7 {
		t.Fatalf("expected stream exit code 7, got %#v", state.ExitCode)
	}
	if len(client.piped) != 1 {
		t.Fatalf("expected one pipe-pane start, got %#v", client.piped)
	}

	state, source, ok = observer.captureSemanticShellState(context.Background(), "%0", client.pane.TTY, "", client.pane.CurrentCommand, PromptContext{})
	if !ok {
		t.Fatal("expected semantic shell state on repeated capture")
	}
	if source != semanticSourceStream {
		t.Fatalf("expected repeated stream source, got %q", source)
	}
	if len(client.piped) != 1 {
		t.Fatalf("expected stream collector to reuse existing pipe-pane, got %#v", client.piped)
	}
}

func TestStartTrackedCommandEnsuresLocalShellIntegrationBeforeLaunching(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane:    tmux.Pane{ID: "%0", CurrentCommand: "bash", TTY: "/dev/pts/22"},
		capture: shellTestProjectPrompt(t),
	}
	observer := (&Observer{client: client}).WithStateDir(t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	monitor, err := observer.StartTrackedCommand(ctx, "%0", "printf 'hi\\n'", 250*time.Millisecond)
	if err != nil {
		t.Fatalf("StartTrackedCommand() error = %v", err)
	}
	cancel()
	_, _ = monitor.Wait()

	if len(client.sent) < 2 {
		t.Fatalf("expected semantic bootstrap and tracked command send, got %#v", client.sent)
	}
	if !strings.Contains(client.sent[0], "SHUTTLE_SEMANTIC_SHELL_V1_PID") {
		t.Fatalf("expected first send to be semantic bootstrap, got %q", client.sent[0])
	}
	if strings.Contains(client.sent[1], "SHUTTLE_SEMANTIC_SHELL_V1_PID") {
		t.Fatalf("expected second send to be tracked command transport, got %#v", client.sent)
	}
}

func TestAttachForegroundCommandAttachesToManualForegroundProcess(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane: tmux.Pane{ID: "%0", CurrentCommand: "sleep", TTY: "/dev/pts/23"},
		panes: []tmux.Pane{
			{ID: "%0", CurrentCommand: "sleep", TTY: "/dev/pts/23"},
			{ID: "%0", CurrentCommand: "sleep", TTY: "/dev/pts/23"},
			{ID: "%0", CurrentCommand: "zsh", TTY: "/dev/pts/23"},
		},
		captures: []string{
			"sleep 30\nworking",
			"sleep 30\nworking",
			shellTestProjectPrompt(t),
		},
	}
	observer := (&Observer{client: client}).WithStateDir(t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	monitor, err := observer.AttachForegroundCommand(ctx, "%0")
	if err != nil {
		t.Fatalf("AttachForegroundCommand() error = %v", err)
	}
	if monitor == nil {
		t.Fatal("expected active foreground monitor")
	}
	result, err := monitor.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if result.State != MonitorStateCompleted {
		t.Fatalf("expected completed attached command, got %#v", result)
	}
	if result.Command != "sleep" {
		t.Fatalf("expected foreground command label sleep, got %#v", result)
	}
	if result.Captured != "working" {
		t.Fatalf("expected attached foreground output delta, got %q", result.Captured)
	}
}

func TestAttachForegroundCommandQuietProcessDoesNotReturnPaneScrollback(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane: tmux.Pane{ID: "%0", CurrentCommand: "sleep", TTY: "/dev/pts/23"},
		panes: []tmux.Pane{
			{ID: "%0", CurrentCommand: "sleep", TTY: "/dev/pts/23"},
			{ID: "%0", CurrentCommand: "zsh", TTY: "/dev/pts/23"},
		},
		captures: []string{
			"ls\nREADME.md\nsleep 30",
			shellTestProjectPrompt(t),
		},
	}
	observer := (&Observer{client: client}).WithStateDir(t.TempDir())

	monitor, err := observer.AttachForegroundCommand(context.Background(), "%0")
	if err != nil {
		t.Fatalf("AttachForegroundCommand() error = %v", err)
	}
	if monitor == nil {
		t.Fatal("expected active foreground monitor")
	}
	result, err := monitor.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if result.Captured != "" {
		t.Fatalf("expected quiet foreground command not to replay pane scrollback, got %q", result.Captured)
	}
}

func TestAttachForegroundCommandPromptInferenceFocusesOnLatestPromptWindow(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane: tmux.Pane{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/29"},
		panes: []tmux.Pane{
			{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/29"},
			{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/29"},
			{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/29"},
		},
		captures: []string{
			"root@web01:/srv/app# cat file.txt\nalpha\nroot@web01:/srv/app# rm file.txt",
			"root@web01:/srv/app# cat file.txt\nalpha\nroot@web01:/srv/app# rm file.txt",
			"root@web01:/srv/app# cat file.txt\nalpha\nroot@web01:/srv/app# rm file.txt\nroot@web01:/srv/app#",
		},
	}
	observer := (&Observer{client: client}).WithStateDir(t.TempDir())

	monitor, err := observer.AttachForegroundCommand(context.Background(), "%0")
	if err != nil {
		t.Fatalf("AttachForegroundCommand() error = %v", err)
	}
	if monitor == nil {
		t.Fatal("expected active foreground monitor")
	}
	result, err := monitor.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if result.Captured != "" {
		t.Fatalf("expected quiet prompt-inference command not to replay prior output, got %q", result.Captured)
	}
}

func TestAttachForegroundCommandPromptInferencePreservesCurrentCommandOutputOnly(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane: tmux.Pane{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/30"},
		panes: []tmux.Pane{
			{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/30"},
			{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/30"},
			{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/30"},
		},
		captures: []string{
			"root@web01:/srv/app# cat old.txt\nold\nroot@web01:/srv/app# cat new.txt",
			"root@web01:/srv/app# cat old.txt\nold\nroot@web01:/srv/app# cat new.txt\nnew",
			"root@web01:/srv/app# cat old.txt\nold\nroot@web01:/srv/app# cat new.txt\nnew\nroot@web01:/srv/app#",
		},
	}
	observer := (&Observer{client: client}).WithStateDir(t.TempDir())

	monitor, err := observer.AttachForegroundCommand(context.Background(), "%0")
	if err != nil {
		t.Fatalf("AttachForegroundCommand() error = %v", err)
	}
	if monitor == nil {
		t.Fatal("expected active foreground monitor")
	}
	result, err := monitor.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if result.Captured != "new" {
		t.Fatalf("expected prompt-inference command to keep only current output, got %q", result.Captured)
	}
}

func TestAttachForegroundCommandPromptInferenceUsesRemoteForegroundCommandState(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane: tmux.Pane{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/32"},
		panes: []tmux.Pane{
			{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/32"},
			{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/32"},
			{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/32"},
		},
		captures: []string{
			"openclaw@openclaw:~$ sleep 15",
			"openclaw@openclaw:~$ sleep 15",
			"openclaw@openclaw:~$",
		},
	}
	observer := (&Observer{client: client}).WithStateDir(t.TempDir())

	monitor, err := observer.AttachForegroundCommand(context.Background(), "%0")
	if err != nil {
		t.Fatalf("AttachForegroundCommand() error = %v", err)
	}
	if monitor == nil {
		t.Fatal("expected active foreground monitor")
	}

	snapshot := monitor.Snapshot()
	if snapshot.Command != "sleep 15" {
		t.Fatalf("expected prompt-inference command label sleep 15, got %#v", snapshot)
	}
	if snapshot.State != MonitorStateRunning {
		t.Fatalf("expected prompt-inference command to start running, got %#v", snapshot)
	}

	result, err := monitor.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if result.State != MonitorStateCompleted {
		t.Fatalf("expected prompt-inference command to complete, got %#v", result)
	}
	if result.Command != "sleep 15" {
		t.Fatalf("expected completed command label sleep 15, got %#v", result)
	}
}

func TestAttachForegroundCommandPromptInferenceSettlesSSHWhenRemoteCommandStarts(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane: tmux.Pane{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/33"},
		panes: []tmux.Pane{
			{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/33"},
			{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/33"},
		},
		captures: []string{
			"openclaw@openclaw's password:",
			"openclaw@openclaw:~$ sleep 15",
		},
	}
	observer := (&Observer{client: client}).WithStateDir(t.TempDir())

	monitor, err := observer.AttachForegroundCommand(context.Background(), "%0")
	if err != nil {
		t.Fatalf("AttachForegroundCommand() error = %v", err)
	}
	if monitor == nil {
		t.Fatal("expected active foreground monitor")
	}
	if snapshot := monitor.Snapshot(); snapshot.State != MonitorStateAwaitingInput {
		t.Fatalf("expected password prompt to start awaiting input, got %#v", snapshot)
	}

	result, err := monitor.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if result.State != MonitorStateCompleted {
		t.Fatalf("expected ssh transport to settle once remote command starts, got %#v", result)
	}
	if result.Command != "ssh" {
		t.Fatalf("expected settled transport command ssh, got %#v", result)
	}
	if result.Cause != CompletionCauseContextTransition {
		t.Fatalf("expected settled transport command to report context transition cause, got %#v", result)
	}
	if result.ShellContext.Host != "openclaw" {
		t.Fatalf("expected remote shell context to be preserved, got %#v", result)
	}
}

func TestCaptureShellContextIgnoresHistoricalPromptWhileForegroundCommandActive(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane:    tmux.Pane{ID: "%0", CurrentCommand: "sleep", TTY: "/dev/pts/26"},
		capture: shellTestProjectPrompt(t) + "\n. '/run/user/1000/shuttle/shell-integration/zsh-pane0.sh'\nsleep 20",
	}
	observer := (&Observer{client: client}).WithStateDir(t.TempDir())

	context, err := observer.CaptureShellContext(context.Background(), "%0")
	if err != nil {
		t.Fatalf("CaptureShellContext() error = %v", err)
	}
	if context.PromptLine() != "" {
		t.Fatalf("expected stale prompt scrollback to be ignored, got %#v", context)
	}
}

func TestCaptureShellContextReturnsTrailingPrompt(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane:    tmux.Pane{ID: "%0", CurrentCommand: "zsh", TTY: "/dev/pts/27"},
		capture: "sleep 20\n" + shellTestProjectPrompt(t),
	}
	observer := (&Observer{client: client}).WithStateDir(t.TempDir())

	context, err := observer.CaptureShellContext(context.Background(), "%0")
	if err != nil {
		t.Fatalf("CaptureShellContext() error = %v", err)
	}
	if got := context.PromptLine(); got != shellTestProjectPrompt(t) {
		t.Fatalf("expected trailing prompt context, got %#v", context)
	}
}

func TestAttachForegroundCommandIgnoresHistoricalPromptScrollback(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane: tmux.Pane{ID: "%0", CurrentCommand: "sleep", TTY: "/dev/pts/28"},
		panes: []tmux.Pane{
			{ID: "%0", CurrentCommand: "sleep", TTY: "/dev/pts/28"},
			{ID: "%0", CurrentCommand: "sleep", TTY: "/dev/pts/28"},
			{ID: "%0", CurrentCommand: "zsh", TTY: "/dev/pts/28"},
		},
		captures: []string{
			shellTestProjectPrompt(t) + "\n. '/run/user/1000/shuttle/shell-integration/zsh-pane0.sh'\nsleep 20",
			shellTestProjectPrompt(t) + "\n. '/run/user/1000/shuttle/shell-integration/zsh-pane0.sh'\nsleep 20",
			shellTestProjectPrompt(t),
		},
	}
	observer := (&Observer{client: client}).WithStateDir(t.TempDir())

	monitor, err := observer.AttachForegroundCommand(context.Background(), "%0")
	if err != nil {
		t.Fatalf("AttachForegroundCommand() error = %v", err)
	}
	if monitor == nil {
		t.Fatal("expected active foreground monitor")
	}
	result, err := monitor.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if result.State != MonitorStateCompleted {
		t.Fatalf("expected completed attached command, got %#v", result)
	}
	if result.Captured != "" {
		t.Fatalf("expected historical prompt scrollback to be ignored, got %q", result.Captured)
	}
}

func TestAttachForegroundCommandSkipsIdleRemotePrompt(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane:    tmux.Pane{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/24"},
		capture: "root@web01:/srv/app#",
	}
	observer := (&Observer{client: client}).WithStateDir(t.TempDir())

	monitor, err := observer.AttachForegroundCommand(context.Background(), "%0")
	if err != nil {
		t.Fatalf("AttachForegroundCommand() error = %v", err)
	}
	if monitor != nil {
		t.Fatalf("expected idle remote prompt not to attach, got %#v", monitor.Snapshot())
	}
}

func TestAttachForegroundCommandSkipsIdleRemotePromptWithoutDirectory(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane:    tmux.Pane{ID: "%0", CurrentCommand: "ssh", TTY: "/dev/pts/24"},
		capture: "root@web01#",
	}
	observer := (&Observer{client: client}).WithStateDir(t.TempDir())

	monitor, err := observer.AttachForegroundCommand(context.Background(), "%0")
	if err != nil {
		t.Fatalf("AttachForegroundCommand() error = %v", err)
	}
	if monitor != nil {
		t.Fatalf("expected idle remote prompt without cwd not to attach, got %#v", monitor.Snapshot())
	}
}

func TestAttachForegroundCommandSkipsShellPaneWhenPromptParseFails(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pane:    tmux.Pane{ID: "%0", CurrentCommand: "zsh", TTY: "/dev/pts/24"},
		capture: "banner line without a parseable prompt yet",
	}
	observer := (&Observer{client: client}).WithStateDir(t.TempDir())

	monitor, err := observer.AttachForegroundCommand(context.Background(), "%0")
	if err != nil {
		t.Fatalf("AttachForegroundCommand() error = %v", err)
	}
	if monitor != nil {
		t.Fatalf("expected shell pane without prompt evidence not to attach, got %#v", monitor.Snapshot())
	}
}

func TestRunTrackedMonitorCompletesOnPromptReturnEvenWithNonPromptSemanticState(t *testing.T) {
	markers := protocol.NewMarkers()
	command := "apply_patch <<'PATCH'\ndiff --git a/hello.txt b/hello.txt\n--- a/hello.txt\n+++ b/hello.txt\n@@ -1 +1 @@\n-hello\n+hello world\nPATCH"
	client := &fakeSemanticPaneClient{
		pane: tmux.Pane{ID: "%0", CurrentCommand: "zsh", TTY: "/dev/pts/31"},
		capture: strings.Join([]string{
			shellTestProjectPrompt(t),
			markers.BeginLine,
			"/run/user/1000/shuttle/commands/cmd-1.sh:2: command not found: apply_patch",
			shellTestProjectPrompt(t),
		}, "\n"),
		escaped: "\x1b]133;C\x1b\\\x1b]7;file://localhost/workspace/project\x1b\\",
	}
	observer := (&Observer{client: client}).WithStateDir(t.TempDir())
	monitor := newTrackedCommandMonitor(markers.CommandID, command)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go observer.runTrackedMonitor(
		ctx,
		monitor,
		"%0",
		command,
		". '/run/user/1000/shuttle/commands/cmd-1.sh'",
		250*time.Millisecond,
		shellTestProjectPrompt(t),
		shellTestProjectPrompt(t),
		markers,
		func() {},
	)

	result, err := monitor.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if result.State != MonitorStateFailed || result.ExitCode != 127 {
		t.Fatalf("expected prompt-return failure result, got %#v", result)
	}
	if !strings.Contains(result.Captured, "command not found: apply_patch") {
		t.Fatalf("expected captured shell failure, got %q", result.Captured)
	}
}

func TestCaptureRecentOutputRecoversFromRespawnedTopPane(t *testing.T) {
	client := &fakeSemanticPaneClient{
		captureErrByTarget: map[string]error{
			"%0": fmt.Errorf("tmux capture-pane -p -t %%0 -S -20: exit status 1: can't find pane: %%0"),
		},
		capturesByTarget: map[string]string{
			"%2": "new pane output",
		},
		panesByTarget: map[string]tmux.Pane{
			"%2": {ID: "%2", CurrentCommand: "zsh", TTY: "/dev/pts/30", Top: 0, Left: 0},
		},
		listPanes: []tmux.Pane{
			{ID: "%2", CurrentCommand: "zsh", TTY: "/dev/pts/30", Top: 0, Left: 0},
			{ID: "%3", CurrentCommand: "shuttle", TTY: "/dev/pts/31", Top: 20, Left: 0},
		},
	}
	observer := (&Observer{client: client}).WithSessionName("shuttle")

	got, err := observer.CaptureRecentOutput(context.Background(), "%0", 20)
	if err != nil {
		t.Fatalf("CaptureRecentOutput() error = %v", err)
	}
	if got != "new pane output" {
		t.Fatalf("CaptureRecentOutput() = %q, want %q", got, "new pane output")
	}
	if alias := observer.paneAlias("%0"); alias != "%2" {
		t.Fatalf("expected respawned pane alias %%2, got %q", alias)
	}
}

func TestCaptureRecentOutputDisplayPreservesANSI(t *testing.T) {
	client := &fakeSemanticPaneClient{
		capture: "red.txt",
		escaped: "\x1b[31mred.txt\x1b[0m",
	}
	observer := &Observer{client: client}

	display, err := observer.CaptureRecentOutputDisplay(context.Background(), "%0", 20)
	if err != nil {
		t.Fatalf("CaptureRecentOutputDisplay() error = %v", err)
	}
	if !strings.Contains(display, "\x1b[31m") {
		t.Fatalf("expected ANSI color in display capture, got %q", display)
	}

	plain, err := observer.CaptureRecentOutput(context.Background(), "%0", 20)
	if err != nil {
		t.Fatalf("CaptureRecentOutput() error = %v", err)
	}
	if plain != "red.txt" {
		t.Fatalf("expected stripped plain output, got %q", plain)
	}
}

func TestCaptureRecentOutputRecreatesMissingSession(t *testing.T) {
	client := &fakeSemanticPaneClient{
		captureErrByTarget: map[string]error{
			"%0": fmt.Errorf("tmux capture-pane -p -t %%0 -S -20: exit status 1: no server running on /tmp/tmux-1000/default"),
		},
		paneErrByTarget: map[string]error{
			"%0": fmt.Errorf("tmux list-panes -t %%0: exit status 1: can't find pane: %%0"),
		},
		listPanesErr: fmt.Errorf("tmux list-panes -t shuttle: exit status 1: can't find session: shuttle"),
	}
	observer := (&Observer{
		client:      client,
		sessionName: "shuttle",
		sessionEnsurer: func(context.Context) error {
			client.captureErrByTarget = map[string]error{}
			client.paneErrByTarget = map[string]error{
				"%0": fmt.Errorf("tmux list-panes -t %%0: exit status 1: can't find pane: %%0"),
			}
			client.listPanesErr = nil
			client.listPanes = []tmux.Pane{
				{ID: "%4", CurrentCommand: "zsh", TTY: "/dev/pts/44", Top: 0, Left: 0},
			}
			client.panesByTarget = map[string]tmux.Pane{
				"%4": {ID: "%4", CurrentCommand: "zsh", TTY: "/dev/pts/44", Top: 0, Left: 0},
			}
			client.capturesByTarget = map[string]string{
				"%4": "recreated session output",
			}
			return nil
		},
	}).WithSessionName("shuttle").WithStartDir(t.TempDir())

	got, err := observer.CaptureRecentOutput(context.Background(), "%0", 20)
	if err != nil {
		t.Fatalf("CaptureRecentOutput() error = %v", err)
	}
	if got != "recreated session output" {
		t.Fatalf("CaptureRecentOutput() = %q, want %q", got, "recreated session output")
	}
	if alias := observer.paneAlias("%0"); alias != "%4" {
		t.Fatalf("expected recovered pane alias %%4, got %q", alias)
	}
}

func TestPipePaneOutputRecreatesMissingSession(t *testing.T) {
	client := &fakeSemanticPaneClient{
		pipeErrByTarget: map[string]error{
			"%0": fmt.Errorf("tmux pipe-pane -O -t %%0: exit status 1: no server running on /tmp/tmux-1000/default"),
		},
		paneErrByTarget: map[string]error{
			"%0": fmt.Errorf("tmux list-panes -t %%0: exit status 1: can't find pane: %%0"),
		},
		listPanesErr: fmt.Errorf("tmux list-panes -t shuttle: exit status 1: can't find session: shuttle"),
	}
	observer := (&Observer{
		client:      client,
		sessionName: "shuttle",
		sessionEnsurer: func(context.Context) error {
			client.pipeErrByTarget = map[string]error{}
			client.paneErrByTarget = map[string]error{
				"%0": fmt.Errorf("tmux list-panes -t %%0: exit status 1: can't find pane: %%0"),
			}
			client.listPanesErr = nil
			client.listPanes = []tmux.Pane{
				{ID: "%5", CurrentCommand: "zsh", TTY: "/dev/pts/45", Top: 0, Left: 0},
			}
			client.panesByTarget = map[string]tmux.Pane{
				"%5": {ID: "%5", CurrentCommand: "zsh", TTY: "/dev/pts/45", Top: 0, Left: 0},
			}
			return nil
		},
	}).WithSessionName("shuttle").WithStartDir(t.TempDir())

	if err := observer.PipePaneOutput(context.Background(), "%0", "cat > /tmp/demo"); err != nil {
		t.Fatalf("PipePaneOutput() error = %v", err)
	}
	if len(client.pipeTargets) != 1 || client.pipeTargets[0] != "%5" {
		t.Fatalf("expected pipe-pane recovery to target %%5, got %#v", client.pipeTargets)
	}
	if alias := observer.paneAlias("%0"); alias != "%5" {
		t.Fatalf("expected recovered pane alias %%5, got %q", alias)
	}
}

func TestPaneCommandIsShell(t *testing.T) {
	if !paneCommandIsShell("zsh") {
		t.Fatal("expected zsh to be treated as shell")
	}
	if paneCommandIsShell("nano") {
		t.Fatal("expected nano not to be treated as shell")
	}
}

func TestParseShellContextProbeOutput(t *testing.T) {
	body := "__SHUTTLE_CTX_EXIT__=0\n__SHUTTLE_CTX_USER__=root\n__SHUTTLE_CTX_HOST__=web01\n__SHUTTLE_CTX_UNAME__=Linux 6.8\n__SHUTTLE_CTX_PWD__=/srv/app"

	clean, context, exitCode := parseShellContextProbeOutput(body, PromptContext{})
	if clean != "" {
		t.Fatalf("expected empty clean output, got %q", clean)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if context.User != "root" || context.Host != "web01" || context.Directory != "/srv/app" {
		t.Fatalf("unexpected prompt context %#v", context)
	}
	if !context.Root {
		t.Fatalf("expected root prompt context %#v", context)
	}
	if got := context.PromptLine(); got != "root@web01 /srv/app #" {
		t.Fatalf("expected prompt line to be rebuilt from probe context, got %q", got)
	}
}

func TestPromptReturnedAfterTransitionIgnoresPureCommandEcho(t *testing.T) {
	before := "localuser@workstation ~/repo %"
	baseline := PromptContext{
		User:         "localuser",
		Host:         "workstation",
		Directory:    "~/repo",
		PromptSymbol: "%",
		RawLine:      before,
	}
	candidate := baseline
	captured := before + "\nssh openclaw@openclaw"

	if promptReturnedAfterTransition(before, baseline, candidate, captured, "") {
		t.Fatal("expected pure echoed transition command not to count as prompt return")
	}
}

func TestPromptReturnedAfterTransitionAcceptsPromptChange(t *testing.T) {
	before := "localuser@workstation ~/repo %"
	baseline := PromptContext{
		User:         "localuser",
		Host:         "workstation",
		Directory:    "~/repo",
		PromptSymbol: "%",
		RawLine:      before,
	}
	candidate := PromptContext{
		User:         "openclaw",
		Host:         "openclaw",
		Directory:    "~",
		PromptSymbol: "$",
		Remote:       true,
		RawLine:      "openclaw@openclaw ~ $",
	}

	if !promptReturnedAfterTransition(before, baseline, candidate, before+"\nLast login\nopenclaw@openclaw ~ $", "") {
		t.Fatal("expected changed prompt to count as settled transition")
	}
}

func TestPromptReturnedAfterTransitionIgnoresAwaitingInputTail(t *testing.T) {
	before := "openclaw@openclaw ~ $"
	baseline := PromptContext{
		User:         "openclaw",
		Host:         "openclaw",
		Directory:    "~",
		PromptSymbol: "$",
		Remote:       true,
		RawLine:      before,
	}
	candidate := baseline
	captured := before + "\nssh openclaw@openclaw\nopenclaw@openclaw's password:"
	delta := "openclaw@openclaw's password:"

	if promptReturnedAfterTransition(before, baseline, candidate, captured, delta) {
		t.Fatal("expected password prompt tail not to count as settled transition")
	}
}

func TestShouldFallbackSettleExitTransitionOnRespawnedPane(t *testing.T) {
	observation := transitionObservation{}
	livePane := tmux.Pane{ID: "%1", CurrentCommand: "zsh"}

	if !shouldFallbackSettleExitTransition("exit", "%0", livePane, observation) {
		t.Fatal("expected respawned pane to settle exit transition")
	}
}

func TestShouldFallbackSettleExitTransitionOnDisconnectTail(t *testing.T) {
	observation := transitionObservation{Delta: "logout\nConnection to openclaw closed.\n➜ shuttle git:(uitweaks)"}
	livePane := tmux.Pane{ID: "%1", CurrentCommand: "zsh"}

	if !shouldFallbackSettleExitTransition("exit", "%1", livePane, observation) {
		t.Fatal("expected disconnect tail to settle exit transition")
	}
}

func TestShouldFallbackSettleExitTransitionIgnoresPlainEcho(t *testing.T) {
	observation := transitionObservation{}
	livePane := tmux.Pane{ID: "%1", CurrentCommand: "zsh"}

	if shouldFallbackSettleExitTransition("exit", "%1", livePane, observation) {
		t.Fatal("expected plain echoed exit not to settle transition")
	}
}

func TestPromptReturnedAfterTransitionAcceptsSamePromptWithNonInteractiveOutput(t *testing.T) {
	before := "openclaw@openclaw ~ $"
	baseline := PromptContext{
		User:         "openclaw",
		Host:         "openclaw",
		Directory:    "~",
		PromptSymbol: "$",
		Remote:       true,
		RawLine:      before,
	}
	candidate := baseline
	captured := before + "\nLast login: Fri Mar 27 23:33:33 2026 from 192.168.0.80\nopenclaw@openclaw ~ $"
	delta := "Last login: Fri Mar 27 23:33:33 2026 from 192.168.0.80"

	if !promptReturnedAfterTransition(before, baseline, candidate, captured, delta) {
		t.Fatal("expected same prompt plus non-interactive login output to count as settled transition")
	}
}

func TestTrackedCommandLikelyStarted(t *testing.T) {
	before := "localuser@devbox %"
	after := "localuser@devbox % printf '__SHUTTLE_B__'\nalpha"

	if !trackedCommandLikelyStarted(before, after) {
		t.Fatal("expected changed pane capture to infer command start")
	}
}

func TestInferTrackedCommandResultFromEndMarker(t *testing.T) {
	markers := protocol.Markers{
		CommandID: "cmd-1",
		BeginLine: "__SHUTTLE_B__:cmd-1",
		EndPrefix: "__SHUTTLE_E__:cmd-1:",
	}

	before := "localuser@devbox %"
	after := "localuser@devbox % rg -n -H -e foo ~\nalpha\nbeta\n__SHUTTLE_E__:cmd-1:0\nlocaluser@devbox %"

	result, complete, err := inferTrackedCommandResultFromEndMarker(after, before, "rg -n -H -e foo ~", markers)
	if err != nil {
		t.Fatalf("inferTrackedCommandResultFromEndMarker() error = %v", err)
	}
	if !complete {
		t.Fatal("expected inferred result to complete")
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if result.Body != "alpha\nbeta\nlocaluser@devbox %" {
		t.Fatalf("unexpected inferred body %q", result.Body)
	}
}

func TestInferTrackedCommandResultFromEndMarkerIgnoresWrappedMarkerLookalike(t *testing.T) {
	markers := protocol.Markers{
		CommandID: "cmd-1",
		BeginLine: "__SHUTTLE_B__:cmd-1",
		EndPrefix: "__SHUTTLE_E__:cmd-1:",
	}

	before := "openclaw@openclaw ~ $"
	after := strings.Join([]string{
		"openclaw@openclaw ~ $ printf '%s%s\\n' '__SHUTTLE_E__:cmd-1:' \"$__shuttle_status\"",
		"chunk appended",
		"__SHUTTLE_E__:cmd-1:0",
		"openclaw@openclaw ~ $",
	}, "\n")

	result, complete, err := inferTrackedCommandResultFromEndMarker(after, before, "printf '%s' 'abc' >> '/tmp/file'", markers)
	if err != nil {
		t.Fatalf("inferTrackedCommandResultFromEndMarker() error = %v", err)
	}
	if !complete {
		t.Fatal("expected inferred result to complete")
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Body, "chunk appended") {
		t.Fatalf("expected chunk append output in body, got %q", result.Body)
	}
}

package shell

import (
	"testing"

	"aiterm/internal/protocol"
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
	body := "jsmith@linuxdesktop ~/source/repos/aiterm % [ -n \"$SHUTTLE_SEMANTIC_SHELL_V1\" ] || . '/home/jsmith/source/repos/aiterm/.shuttle/shell-integration/zsh-pane0.sh' >/dev/null 2>&1\njsmith@linuxdesktop ~/source/repos/aiterm % .\n '/home/jsmith/source/repos/aiterm/.shuttle/commands/cmd-1.sh'\n1\n2\n^C"

	got := sanitizeCapturedBody(body)
	want := "1\n2\n^C"

	if got != want {
		t.Fatalf("sanitizeCapturedBody() = %q, want %q", got, want)
	}
}

func TestStripEchoedSingleLineCommand(t *testing.T) {
	body := "jsmith@host % ls\nfile-a\nfile-b"

	got := stripEchoedCommand(body, "ls")
	want := "file-a\nfile-b"

	if got != want {
		t.Fatalf("stripEchoedCommand() = %q, want %q", got, want)
	}
}

func TestStripEchoedMultiLineQuotedCommand(t *testing.T) {
	body := "jsmith@host % bash -lc '\nquote> set -e\nquote> printf \"## PWD\\n\"; pwd\nquote> '\n## PWD\n/home/jsmith/source/repos/aiterm"
	command := "bash -lc '\nset -e\nprintf \"## PWD\\n\"; pwd\n'"

	got := stripEchoedCommand(body, command)
	want := "## PWD\n/home/jsmith/source/repos/aiterm"

	if got != want {
		t.Fatalf("stripEchoedCommand() = %q, want %q", got, want)
	}
}

func TestStripEchoedCommandWithPromptLineBeforeTransportCommand(t *testing.T) {
	body := "jsmith@linuxdesktop ~/source/repos/aiterm git:(main) %\n. '/home/jsmith/source/repos/aiterm/.shuttle/commands/cmd-1.sh'\n1\n2\n3"
	command := ". '/home/jsmith/source/repos/aiterm/.shuttle/commands/cmd-1.sh'"

	got := stripEchoedCommand(body, command)
	want := "1\n2\n3"

	if got != want {
		t.Fatalf("stripEchoedCommand() = %q, want %q", got, want)
	}
}

func TestStripEchoedWrappedTransportCommand(t *testing.T) {
	body := "jsmith@linuxdesktop ~/source/repos/aiterm git:(main) %\n. \n '/home/jsmith/source/repos/aiterm/.shuttle/commands/cmd-1.sh'\n1\n2\n3"
	command := ". '/home/jsmith/source/repos/aiterm/.shuttle/commands/cmd-1.sh'"

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
		"docker exec -it app sh":     true,
		"kubectl exec -it pod -- sh": true,
		"exit":                       true,
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
	if got := classifyActiveMonitorState("sudo ls", "[sudo] password for jsmith:", false, "sudo"); got != MonitorStateAwaitingInput {
		t.Fatalf("classifyActiveMonitorState(sudo ls) = %s, want %s", got, MonitorStateAwaitingInput)
	}
}

func TestClassifyActiveMonitorStateTreatsAlternateScreenAsInteractiveFullscreen(t *testing.T) {
	if got := classifyActiveMonitorState("wrapped-btop", "", true, "btop"); got != MonitorStateInteractiveFullscreen {
		t.Fatalf("classifyActiveMonitorState(alternate screen) = %s, want %s", got, MonitorStateInteractiveFullscreen)
	}
}

func TestClassifyActiveMonitorStateTreatsFullscreenForegroundCommandAsInteractiveFullscreen(t *testing.T) {
	if got := classifyActiveMonitorState("wrapped-alias", "", false, "nano"); got != MonitorStateInteractiveFullscreen {
		t.Fatalf("classifyActiveMonitorState(foreground nano) = %s, want %s", got, MonitorStateInteractiveFullscreen)
	}
}

func TestClassifyActiveMonitorStateTreatsAwaitingForegroundCommandAsAwaitingInput(t *testing.T) {
	if got := classifyActiveMonitorState("wrapped-alias", "", false, "sudo"); got != MonitorStateAwaitingInput {
		t.Fatalf("classifyActiveMonitorState(foreground sudo) = %s, want %s", got, MonitorStateAwaitingInput)
	}
}

func TestAllowPromptReturnInferenceDisablesInteractiveCommands(t *testing.T) {
	if allowPromptReturnInference("btop", false, "btop") {
		t.Fatal("expected fullscreen interactive command to disable prompt-return inference")
	}
	if allowPromptReturnInference("wrapped-btop", true, "btop") {
		t.Fatal("expected alternate-screen command to disable prompt-return inference")
	}
	if !allowPromptReturnInference("bash -lc 'sleep 5; echo ready'", false, "zsh") {
		t.Fatal("expected ordinary shell command to allow prompt-return inference")
	}
}

func TestAllowPromptReturnInferenceDisablesNonShellPaneCommands(t *testing.T) {
	if allowPromptReturnInference("bash -lc 'sleep 5; echo ready'", false, "nano") {
		t.Fatal("expected non-shell foreground command to disable prompt-return inference")
	}
}

func TestAllowPromptReturnInferenceAllowsRemoteTransportPaneCommands(t *testing.T) {
	if !allowPromptReturnInference("bash -lc 'sleep 5; echo ready'", false, "ssh") {
		t.Fatal("expected ssh pane command to allow prompt-return inference for remote shell reconciliation")
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
}

func TestTrackedCommandLikelyStarted(t *testing.T) {
	before := "jsmith@host %"
	after := "jsmith@host % printf '__SHUTTLE_B__'\nalpha"

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

	before := "jsmith@host %"
	after := "jsmith@host % rg -n -H -e foo ~\nalpha\nbeta\n__SHUTTLE_E__:cmd-1:0\njsmith@host %"

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
	if result.Body != "alpha\nbeta\njsmith@host %" {
		t.Fatalf("unexpected inferred body %q", result.Body)
	}
}

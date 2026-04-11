package shell

import "testing"

func TestTailSuggestsPromptReturnForTrailingPrompt(t *testing.T) {
	current := shellTestLocalPromptContext(t)

	tail := "some tool exited unexpectedly\n" + current.PromptLine()
	if !TailSuggestsPromptReturn(tail, current) {
		t.Fatal("expected trailing prompt to be recognized")
	}
}

func TestTailSuggestsPromptReturnIgnoresEarlierPromptHistory(t *testing.T) {
	current := shellTestLocalPromptContext(t)

	tail := current.PromptLine() + "\n. '/tmp/cmd.sh'\n1\n2\n3"
	if TailSuggestsPromptReturn(tail, current) {
		t.Fatal("expected earlier prompt history to be ignored")
	}
}

func TestTailSuggestsPromptReturnWithParsedPromptContext(t *testing.T) {
	tail := "line 1\nroot@pve:~#"
	if !TailSuggestsPromptReturn(tail, PromptContext{}) {
		t.Fatal("expected parsed prompt context to be recognized")
	}
}

func TestInferPromptReturnResultUsesFailureHeuristicForCommandNotFound(t *testing.T) {
	exitCode, state, confidence, inferred := inferPromptReturnResult(PromptContext{}, "command not found: apply_patch", nil)
	if exitCode != 127 || state != MonitorStateFailed {
		t.Fatalf("expected command-not-found to infer failed exit 127, got exit=%d state=%s", exitCode, state)
	}
	if confidence != ConfidenceMedium || !inferred {
		t.Fatalf("expected inferred medium-confidence failure, got confidence=%s inferred=%v", confidence, inferred)
	}
}

func TestInferPromptReturnResultPrefersPromptExitCode(t *testing.T) {
	exitCodeValue := 17
	exitCode, state, confidence, inferred := inferPromptReturnResult(PromptContext{LastExitCode: &exitCodeValue}, "done", nil)
	if exitCode != 17 || state != MonitorStateFailed {
		t.Fatalf("expected prompt exit code to win, got exit=%d state=%s", exitCode, state)
	}
	if confidence != ConfidenceStrong || inferred {
		t.Fatalf("expected strong non-inferred result, got confidence=%s inferred=%v", confidence, inferred)
	}
}

func TestHandoffPromptReturnReasonPrefersSemanticExitWithoutPromptContext(t *testing.T) {
	exitCode := 17
	reason := HandoffPromptReturnReason(ObservedShellState{
		CurrentPaneCommand: "ssh",
		SemanticState:      semanticShellState{ExitCode: &exitCode},
	}, "partial output", nil)
	if reason != "semantic_exit" {
		t.Fatalf("expected semantic_exit reason, got %q", reason)
	}
}

func TestHandoffPromptReturnReasonUsesFallbackPromptTailForRemoteTransport(t *testing.T) {
	fallback := PromptContext{
		User:         "jsmith",
		Host:         "linuxdesktop",
		Directory:    "~/source/repos/aiterm",
		PromptSymbol: "%",
		RawLine:      "jsmith@linuxdesktop ~/source/repos/aiterm %",
	}
	tail := "logout\nConnection to openclaw closed.\n" + fallback.PromptLine()
	reason := HandoffPromptReturnReason(ObservedShellState{CurrentPaneCommand: "ssh"}, tail, &fallback)
	if reason != "fallback_prompt_tail" {
		t.Fatalf("expected fallback_prompt_tail reason, got %q", reason)
	}
}

func TestHandoffPromptReturnReasonDoesNotUseFallbackPromptTailForAwaitingInput(t *testing.T) {
	fallback := PromptContext{
		User:         "jsmith",
		Host:         "linuxdesktop",
		Directory:    "~/source/repos/aiterm",
		PromptSymbol: "%",
		RawLine:      "jsmith@linuxdesktop ~/source/repos/aiterm %",
	}
	tail := "openclaw@openclaw's password:\n" + fallback.PromptLine()
	reason := HandoffPromptReturnReason(ObservedShellState{CurrentPaneCommand: "ssh"}, tail, &fallback)
	if reason != "" {
		t.Fatalf("expected awaiting-input tail not to reconcile, got %q", reason)
	}
}

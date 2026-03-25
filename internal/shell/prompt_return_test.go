package shell

import "testing"

func TestTailSuggestsPromptReturnForTrailingPrompt(t *testing.T) {
	current := PromptContext{
		User:         "jsmith",
		Host:         "linuxdesktop",
		Directory:    "~/source/repos/aiterm",
		GitBranch:    "main",
		PromptSymbol: "%",
	}

	tail := "some tool exited unexpectedly\njsmith@linuxdesktop ~/source/repos/aiterm git:(main) %"
	if !TailSuggestsPromptReturn(tail, current) {
		t.Fatal("expected trailing prompt to be recognized")
	}
}

func TestTailSuggestsPromptReturnIgnoresEarlierPromptHistory(t *testing.T) {
	current := PromptContext{
		User:         "jsmith",
		Host:         "linuxdesktop",
		Directory:    "~/source/repos/aiterm",
		GitBranch:    "main",
		PromptSymbol: "%",
	}

	tail := "jsmith@linuxdesktop ~/source/repos/aiterm git:(main) %\n. '/tmp/cmd.sh'\n1\n2\n3"
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

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

package shell

import "testing"

func TestTailSuggestsAwaitingInputDetectsPasswordPrompt(t *testing.T) {
	tail := "sudo ls\n[sudo] password for jsmith:"
	if !TailSuggestsAwaitingInput(tail) {
		t.Fatal("expected password prompt to be detected")
	}
}

func TestTailSuggestsAwaitingInputDetectsPressAnyKey(t *testing.T) {
	tail := "Press any key to continue..."
	if !TailSuggestsAwaitingInput(tail) {
		t.Fatal("expected press-any-key prompt to be detected")
	}
}

func TestTailSuggestsAwaitingInputDetectsTruncatedQuotedPressPrompt(t *testing.T) {
	tail := `"Press`
	if !TailSuggestsAwaitingInput(tail) {
		t.Fatal("expected truncated quoted press prompt to be detected")
	}
}

func TestTailSuggestsAwaitingInputDetectsSelectPrompt(t *testing.T) {
	tail := "=== TUI Applications ===\n[Q] Quit\nSelect:"
	if !TailSuggestsAwaitingInput(tail) {
		t.Fatal("expected select prompt to be detected")
	}
}

func TestTailSuggestsAwaitingInputDetectsChoosePrompt(t *testing.T) {
	tail := "Choose:"
	if !TailSuggestsAwaitingInput(tail) {
		t.Fatal("expected choose prompt to be detected")
	}
}

func TestTailSuggestsAwaitingInputIgnoresNormalOutput(t *testing.T) {
	tail := "1\n2\n3\n4"
	if TailSuggestsAwaitingInput(tail) {
		t.Fatal("expected normal output not to be treated as awaiting input")
	}
}

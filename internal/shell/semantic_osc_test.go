package shell

import "testing"

func TestParseSemanticShellStateFromOSCCapture(t *testing.T) {
	raw := "\x1b]133;B\x07run\n\x1b]7;file://linuxdesktop/home/jsmith/source/repos/aiterm\x07\x1b]133;D;130\x07\x1b]133;A\x07"
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
	if state.Directory != "/home/jsmith/source/repos/aiterm" {
		t.Fatalf("unexpected directory %#v", state)
	}
}

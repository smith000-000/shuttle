package protocol

import (
	"strings"
	"testing"
)

func TestNewMarkersProducesDistinctIDs(t *testing.T) {
	first := NewMarkers()
	second := NewMarkers()

	if first.CommandID == second.CommandID {
		t.Fatalf("expected unique command ids, got %q", first.CommandID)
	}
}

func TestWrapCommandIncludesMarkers(t *testing.T) {
	markers := Markers{
		CommandID: "cmd-123",
		BeginLine: "__SHUTTLE_B__:cmd-123",
		EndPrefix: "__SHUTTLE_E__:cmd-123:",
	}

	wrapped := WrapCommand("echo hello", markers)

	if wrapped == "" {
		t.Fatal("expected wrapped command")
	}

	if !strings.Contains(wrapped, markers.BeginLine) {
		t.Fatalf("wrapped command missing begin marker: %q", wrapped)
	}

	if !strings.Contains(wrapped, markers.EndPrefix) {
		t.Fatalf("wrapped command missing end marker prefix: %q", wrapped)
	}

	if strings.Contains(wrapped, "\n") {
		t.Fatalf("wrapped command should be a single shell line: %q", wrapped)
	}

	if !strings.Contains(wrapped, `eval "$(printf '%s\n'`) {
		t.Fatalf("wrapped command should reconstruct the original command via printf/eval: %q", wrapped)
	}
}

func TestParseCommandResult(t *testing.T) {
	markers := Markers{
		CommandID: "cmd-123",
		BeginLine: "__SHUTTLE_B__:cmd-123",
		EndPrefix: "__SHUTTLE_E__:cmd-123:",
	}

	captured := "prompt$ echo __SHUTTLE_B__:cmd-123\n__SHUTTLE_B__:cmd-123\nhello\nworld\n__SHUTTLE_E__:cmd-123:7\nprompt$"
	result, complete, err := ParseCommandResult(captured, markers)
	if err != nil {
		t.Fatalf("ParseCommandResult() error = %v", err)
	}

	if !complete {
		t.Fatal("expected complete result")
	}

	if result.ExitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", result.ExitCode)
	}

	if result.Body != "hello\nworld" {
		t.Fatalf("unexpected body: %q", result.Body)
	}
}

func TestParseCommandResultIncomplete(t *testing.T) {
	markers := Markers{
		CommandID: "cmd-123",
		BeginLine: "__SHUTTLE_B__:cmd-123",
		EndPrefix: "__SHUTTLE_E__:cmd-123:",
	}

	_, complete, err := ParseCommandResult("__SHUTTLE_B__:cmd-123\nhello", markers)
	if err != nil {
		t.Fatalf("ParseCommandResult() error = %v", err)
	}

	if complete {
		t.Fatal("expected incomplete result")
	}
}

func TestParseCommandResultIncompletePartialEndMarker(t *testing.T) {
	markers := Markers{
		CommandID: "cmd-123",
		BeginLine: "__SHUTTLE_B__:cmd-123",
		EndPrefix: "__SHUTTLE_E__:cmd-123:",
	}

	_, complete, err := ParseCommandResult("__SHUTTLE_B__:cmd-123\nhello\n__SHUTTLE_E__:cmd-123:", markers)
	if err != nil {
		t.Fatalf("ParseCommandResult() error = %v", err)
	}

	if complete {
		t.Fatal("expected incomplete result for partial end marker")
	}
}

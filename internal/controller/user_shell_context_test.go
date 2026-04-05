package controller

import (
	"testing"

	"aiterm/internal/shell"
)

func TestApplyObservedShellStateCarriesForwardRemoteDirectoryAsLowConfidence(t *testing.T) {
	controller := New(nil, nil, nil, SessionContext{
		CurrentShellLocation: &shell.ShellLocation{
			Kind:                shell.ShellLocationRemote,
			User:                "openclaw",
			Host:                "openclaw",
			Directory:           "/srv/app",
			DirectorySource:     shell.ShellDirectorySourceProbe,
			DirectoryConfidence: shell.ConfidenceStrong,
		},
	})

	controller.applyObservedShellStateLocked(&shell.ObservedShellState{
		Location: shell.ShellLocation{
			Kind: shell.ShellLocationRemote,
			User: "openclaw",
			Host: "openclaw",
		},
	})

	location := controller.session.CurrentShellLocation
	if location == nil {
		t.Fatal("expected current shell location")
	}
	if location.Directory != "/srv/app" {
		t.Fatalf("expected carried directory, got %#v", location)
	}
	if location.DirectorySource != shell.ShellDirectorySourceCarriedForward || location.DirectoryConfidence != shell.ConfidenceLow {
		t.Fatalf("expected low-confidence carried directory metadata, got %#v", location)
	}
	if controller.session.WorkingDirectory != "/srv/app" {
		t.Fatalf("expected working directory to track carried remote directory, got %q", controller.session.WorkingDirectory)
	}
}

func TestApplyObservedShellStatePreservesProbeAuthoritativeRemoteDirectory(t *testing.T) {
	controller := New(nil, nil, nil, SessionContext{})

	controller.applyObservedShellStateLocked(&shell.ObservedShellState{
		HasPromptContext: true,
		PromptContext: shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "~",
			PromptSymbol: "$",
			RawLine:      "openclaw@openclaw ~ $",
			Remote:       true,
		},
		Location: shell.ShellLocation{
			Kind:                shell.ShellLocationRemote,
			User:                "openclaw",
			Host:                "openclaw",
			Directory:           "/srv/app",
			DirectorySource:     shell.ShellDirectorySourceProbe,
			DirectoryConfidence: shell.ConfidenceStrong,
		},
	})

	location := controller.session.CurrentShellLocation
	if location == nil {
		t.Fatal("expected current shell location")
	}
	if location.Directory != "/srv/app" || location.DirectorySource != shell.ShellDirectorySourceProbe || location.DirectoryConfidence != shell.ConfidenceStrong {
		t.Fatalf("expected authoritative remote directory metadata, got %#v", location)
	}
	if controller.session.WorkingDirectory != "/srv/app" {
		t.Fatalf("expected authoritative working directory, got %q", controller.session.WorkingDirectory)
	}
	if controller.session.CurrentShell == nil || controller.session.CurrentShell.Directory != "~" {
		t.Fatalf("expected prompt context to remain prompt-shaped, got %#v", controller.session.CurrentShell)
	}
}

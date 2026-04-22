package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiterm/internal/controller"
	"aiterm/internal/shell"
	"aiterm/internal/tuifeatures"
)

func TestDisabledShellCompletionSuppressesShellCandidates(t *testing.T) {
	pathDir := t.TempDir()
	t.Setenv("PATH", pathDir)

	gitPath := filepath.Join(pathDir, "git")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(git) error = %v", err)
	}

	model := NewModel(fakeWorkspace(), nil).WithDisabledFeatures(tuifeatures.Set{
		tuifeatures.ShellCompletion: {},
	})
	model.setInput("gi")

	if model.completion != nil {
		t.Fatalf("expected shell completion to be disabled, got %#v", model.completion)
	}
}

func TestDisabledFooterHintsRendersEmptyFooter(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil).WithDisabledFeatures(tuifeatures.Set{
		tuifeatures.FooterHints: {},
	})

	if got := model.renderFooter(80); got != "" {
		t.Fatalf("expected empty footer, got %q", got)
	}
}

func TestDisabledStatusLineRendersEmptyStatus(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithDisabledFeatures(tuifeatures.Set{
		tuifeatures.StatusLine: {},
	})
	model.shellContext = shell.PromptContext{Directory: "/tmp", User: "tester", Host: "local"}

	if got := model.renderStatusLine(80); got != "" {
		t.Fatalf("expected empty status line, got %q", got)
	}
}

func TestDisabledShellContextRemovesLeftStatusSegment(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithDisabledFeatures(tuifeatures.Set{
		tuifeatures.ShellContext: {},
	})
	model.shellContext = shell.PromptContext{Directory: "/tmp", User: "tester", Host: "local"}

	if got := model.renderStatusLine(80); strings.Contains(got, "/tmp") || strings.Contains(got, "tester@local") {
		t.Fatalf("expected shell context to be omitted, got %q", got)
	}
}

func TestDisabledModelStatusSuppressesModelLabel(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithDisabledFeatures(tuifeatures.Set{
		tuifeatures.ModelStatus: {},
	})
	model.activeProvider.Model = "gpt-test"
	model.syncDerivedState()

	if got := model.renderModelStatus(model.statusSnapshotForRender()); got != "" {
		t.Fatalf("expected model status to be empty, got %q", got)
	}
}

func TestDisabledTranscriptChromeFallsBackToPlainResultRendering(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil).WithDisabledFeatures(tuifeatures.Set{
		tuifeatures.TranscriptChrome: {},
	})

	lines := model.renderEntryLines(0, Entry{
		Title:   "result",
		Command: "git status",
		Body:    "clean",
	}, 80)
	if len(lines) == 0 {
		t.Fatal("expected transcript lines")
	}
	if strings.Contains(lines[0].text, "┌") || strings.Contains(lines[0].text, "│") {
		t.Fatalf("expected plain rendering without result chrome, got %q", lines[0].text)
	}
}

func TestDisabledTranscriptUsesPlaceholderLine(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil).WithDisabledFeatures(tuifeatures.Set{
		tuifeatures.Transcript: {},
	})
	model.width = 80
	model.height = 24

	if got := model.currentTranscriptHeight(); got != 1 {
		t.Fatalf("expected transcript height 1 when disabled, got %d", got)
	}

	rendered := model.renderTranscript(model.currentTranscriptWidth(), model.currentTranscriptHeight())
	if !strings.Contains(rendered, "transcript hidden for perf test") {
		t.Fatalf("expected transcript placeholder, got %q", rendered)
	}
}

func TestDisabledPollingFlagsSuppressDiagnosticPolling(t *testing.T) {
	ctrl := &fakeController{
		activeExecution: &controller.CommandExecution{
			ID:      "exec-1",
			Command: "sleep 10",
			State:   controller.CommandExecutionRunning,
		},
	}
	model := NewModel(fakeWorkspace(), ctrl).WithDisabledFeatures(tuifeatures.Set{
		tuifeatures.BusyTick:            {},
		tuifeatures.ExecutionPolling:    {},
		tuifeatures.ShellContextPolling: {},
		tuifeatures.ShellTail:           {},
	})
	model.busy = true
	model.busyStartedAt = time.Now().Add(-2 * time.Second)
	model.activeExecution = ctrl.activeExecution
	model.showShellTail = true

	if got := model.tickBusyCmd(); got != nil {
		t.Fatal("expected busy tick to be disabled")
	}
	if got := model.pollActiveExecutionCmd(); got != nil {
		t.Fatal("expected execution polling to be disabled")
	}
	if got := model.tickShellContextCmd(); got != nil {
		t.Fatal("expected shell-context polling to be disabled")
	}
	if got := model.pollShellTailCmd(); got != nil {
		t.Fatal("expected shell-tail polling to be disabled")
	}
}

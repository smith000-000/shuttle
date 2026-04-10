package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiterm/internal/shell"
)

func TestLocalControllerSubmitShellCommand(t *testing.T) {
	controller := New(nil, &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-1",
			Command:   "ls",
			ExitCode:  0,
			Captured:  "file.txt",
		},
	}, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitShellCommand(context.Background(), "ls")
	if err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].Kind != EventCommandStart || events[1].Kind != EventCommandResult {
		t.Fatalf("unexpected event kinds: %#v", events)
	}

	startPayload, ok := events[0].Payload.(CommandStartPayload)
	if !ok {
		t.Fatalf("expected command start payload, got %#v", events[0].Payload)
	}
	if startPayload.Execution.Origin != CommandOriginUserShell || startPayload.Execution.State != CommandExecutionRunning {
		t.Fatalf("unexpected execution payload: %#v", startPayload.Execution)
	}

	resultPayload, ok := events[1].Payload.(CommandResultSummary)
	if !ok {
		t.Fatalf("expected command result payload, got %#v", events[1].Payload)
	}
	if resultPayload.Origin != CommandOriginUserShell {
		t.Fatalf("expected user-shell origin, got %q", resultPayload.Origin)
	}
}

func TestLocalControllerSubmitShellCommandUsesResolvedTrackedShellPane(t *testing.T) {
	runner := &stubRunner{
		result:         shell.TrackedExecution{CommandID: "cmd-1", Command: "ls", ExitCode: 0},
		resolvedPaneID: "%5",
	}
	controller := New(nil, runner, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	if _, err := controller.SubmitShellCommand(context.Background(), "ls"); err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}
	if len(runner.paneIDs) != 1 || runner.paneIDs[0] != "%5" {
		t.Fatalf("expected tracked command to use resolved pane %%5, got %#v", runner.paneIDs)
	}
	target := controller.TrackedShellTarget()
	if target.PaneID != "%5" {
		t.Fatalf("expected tracked shell pane %%5, got %#v", target)
	}
}

func TestLocalControllerTakeControlTargetUsesOwnedInteractivePane(t *testing.T) {
	controller := New(nil, nil, nil, SessionContext{
		SessionName:  "shuttle-test",
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
	})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:        "cmd-1",
		Command:   "sudo apt update",
		Origin:    CommandOriginAgentProposal,
		State:     CommandExecutionAwaitingInput,
		StartedAt: time.Now(),
		TrackedShell: TrackedShellTarget{
			SessionName: "shuttle-test",
			PaneID:      "%9",
		},
	})

	target := controller.TakeControlTarget()
	if target.PaneID != "%9" {
		t.Fatalf("expected take-control target pane %%9, got %#v", target)
	}
	if controller.TrackedShellTarget().PaneID != "%0" {
		t.Fatalf("expected persistent tracked shell pane to remain %%0, got %#v", controller.TrackedShellTarget())
	}
}

func TestLocalControllerTakeControlTargetUsesOwnedRunningPane(t *testing.T) {
	controller := New(nil, nil, nil, SessionContext{
		SessionName:  "shuttle-test",
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
	})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:        "cmd-1",
		Command:   "for i in $(seq 1 15); do echo $i; sleep 1; done",
		Origin:    CommandOriginAgentProposal,
		State:     CommandExecutionRunning,
		StartedAt: time.Now(),
		TrackedShell: TrackedShellTarget{
			SessionName: "shuttle-test",
			PaneID:      "%9",
		},
	})

	target := controller.TakeControlTarget()
	if target.PaneID != "%9" {
		t.Fatalf("expected take-control target pane %%9, got %#v", target)
	}
	if controller.TrackedShellTarget().PaneID != "%0" {
		t.Fatalf("expected persistent tracked shell pane to remain %%0, got %#v", controller.TrackedShellTarget())
	}
}

func TestLocalControllerSubmitShellCommandReturnsTrackedShellChangeNotice(t *testing.T) {
	runner := &stubRunner{
		result:         shell.TrackedExecution{CommandID: "cmd-1", Command: "ls", ExitCode: 0},
		resolvedPaneID: "%5",
	}
	controller := New(nil, runner, nil, SessionContext{SessionName: "shuttle-test", TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"}})

	events, err := controller.SubmitShellCommand(context.Background(), "ls")
	if err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected tracked-shell notice plus start/result, got %#v", events)
	}
	if events[0].Kind != EventSystemNotice || events[1].Kind != EventCommandStart || events[2].Kind != EventCommandResult {
		t.Fatalf("unexpected event sequence: %#v", events)
	}
	notice, ok := events[0].Payload.(TextPayload)
	if !ok || !strings.Contains(notice.Text, "Tracked shell pane changed from %0 to %5.") {
		t.Fatalf("expected tracked-shell change notice, got %#v", events[0].Payload)
	}
}

func TestNewNormalizesTrackedShellTargetFromSessionContext(t *testing.T) {
	controller := New(nil, nil, nil, SessionContext{
		SessionName: " shuttle-test ",
		TrackedShell: TrackedShellTarget{
			PaneID: " %0 ",
		},
	})

	target := controller.TrackedShellTarget()
	if target.SessionName != "shuttle-test" || target.PaneID != "%0" {
		t.Fatalf("expected normalized tracked shell target, got %#v", target)
	}
}

func TestLocalControllerSubmitShellCommandCanceledReturnsResultEvent(t *testing.T) {
	controller := New(nil, &stubRunner{
		result: shell.TrackedExecution{
			CommandID:  "cmd-1",
			Command:    "sleep 60",
			State:      shell.MonitorStateCanceled,
			Cause:      shell.CompletionCausePromptReturn,
			Confidence: shell.ConfidenceMedium,
			ExitCode:   shell.InterruptedExitCode,
			Captured:   "^C\nlocaluser@host % ",
		},
	}, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitShellCommand(context.Background(), "sleep 60")
	if err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Kind != EventCommandStart || events[1].Kind != EventCommandResult {
		t.Fatalf("unexpected event kinds: %#v", events)
	}

	resultPayload, ok := events[1].Payload.(CommandResultSummary)
	if !ok {
		t.Fatalf("expected command result payload, got %#v", events[1].Payload)
	}
	if resultPayload.State != CommandExecutionCanceled {
		t.Fatalf("expected canceled result state, got %q", resultPayload.State)
	}
	if resultPayload.Cause != shell.CompletionCausePromptReturn {
		t.Fatalf("expected prompt-return cause, got %q", resultPayload.Cause)
	}
	if resultPayload.Confidence != shell.ConfidenceMedium {
		t.Fatalf("expected medium confidence, got %q", resultPayload.Confidence)
	}
	if resultPayload.ExitCode != shell.InterruptedExitCode {
		t.Fatalf("expected interrupted exit code, got %d", resultPayload.ExitCode)
	}
	if controller.ActiveExecution() != nil {
		t.Fatal("expected active execution to clear after canceled result")
	}
	if controller.task.LastCommandResult == nil || controller.task.LastCommandResult.State != CommandExecutionCanceled {
		t.Fatalf("expected canceled last command result, got %#v", controller.task.LastCommandResult)
	}
}

func TestLocalControllerResumeAfterTakeControlReconcilesUserShellPromptReturn(t *testing.T) {
	exitCode := shell.InterruptedExitCode
	reader := &stubContextReader{
		snapshot: "^C\nlocaluser@workstation ~/workspace/project %",
		context: shell.PromptContext{
			User:         "localuser",
			Host:         "workstation",
			Directory:    "/workspace/project",
			PromptSymbol: "%",
			RawLine:      "localuser@workstation ~/workspace/project %",
			LastExitCode: &exitCode,
		},
	}
	controller := New(nil, nil, reader, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:        "cmd-1",
		Command:   "bash -lc 'for i in {1..30}; do echo \"$i\"; sleep 1; done'",
		Origin:    CommandOriginUserShell,
		State:     CommandExecutionRunning,
		StartedAt: time.Now().Add(-30 * time.Second),
	})

	events, err := controller.ResumeAfterTakeControl(context.Background())
	if err != nil {
		t.Fatalf("ResumeAfterTakeControl() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventCommandResult {
		t.Fatalf("expected command result only, got %#v", events)
	}
	result, ok := events[0].Payload.(CommandResultSummary)
	if !ok {
		t.Fatalf("expected command result payload, got %#v", events[0].Payload)
	}
	if result.State != CommandExecutionCanceled || result.ExitCode != shell.InterruptedExitCode {
		t.Fatalf("unexpected reconcile result %#v", result)
	}
	if controller.ActiveExecution() != nil {
		t.Fatal("expected active execution to clear after handoff reconcile")
	}
}

func TestLocalControllerResumeAfterTakeControlReconcilesOwnedInteractivePane(t *testing.T) {
	exitCode := 0
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Handled the interactive step.",
		},
	}
	reader := &stubContextReader{
		snapshot: "done\nlocaluser@workstation /tmp/project %",
		context: shell.PromptContext{
			User:         "localuser",
			Host:         "workstation",
			Directory:    "/tmp/project",
			PromptSymbol: "%",
			RawLine:      "localuser@workstation /tmp/project %",
			LastExitCode: &exitCode,
		},
	}
	controller := New(agent, nil, reader, SessionContext{
		SessionName:  "shuttle-test",
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
	})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:        "cmd-1",
		Command:   "sudo apt update",
		Origin:    CommandOriginAgentProposal,
		State:     CommandExecutionAwaitingInput,
		StartedAt: time.Now().Add(-30 * time.Second),
		TrackedShell: TrackedShellTarget{
			SessionName: "shuttle-test",
			PaneID:      "%9",
		},
	})

	events, err := controller.ResumeAfterTakeControl(context.Background())
	if err != nil {
		t.Fatalf("ResumeAfterTakeControl() error = %v", err)
	}
	if len(events) != 3 || events[0].Kind != EventSystemNotice || events[1].Kind != EventCommandResult || events[2].Kind != EventAgentMessage {
		t.Fatalf("expected reconcile notice, command result, and agent follow-up, got %#v", events)
	}
	if controller.ActiveExecution() != nil {
		t.Fatal("expected active execution to clear after owned-pane reconcile")
	}
	if controller.task.LastCommandResult == nil || controller.task.LastCommandResult.State != CommandExecutionCompleted {
		t.Fatalf("expected completed last command result, got %#v", controller.task.LastCommandResult)
	}
	if !strings.Contains(agent.lastInput.Prompt, resumeAfterTakeControlPrompt) {
		t.Fatalf("expected resume-after-take-control prompt, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, stateAuthorityPromptSuffix) {
		t.Fatalf("expected state-authority guidance, got %q", agent.lastInput.Prompt)
	}
}

func TestLocalControllerResumeAfterTakeControlInfersCanceledWhenPromptReturnedWithoutExitCode(t *testing.T) {
	reader := &stubContextReader{
		snapshot: "^C\nlocaluser@workstation ~/workspace/project %",
		context: shell.PromptContext{
			User:         "localuser",
			Host:         "workstation",
			Directory:    "/workspace/project",
			PromptSymbol: "%",
			RawLine:      "localuser@workstation ~/workspace/project %",
		},
	}
	controller := New(nil, nil, reader, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 10",
		Origin:    CommandOriginUserShell,
		State:     CommandExecutionRunning,
		StartedAt: time.Now().Add(-10 * time.Second),
	})

	events, err := controller.ResumeAfterTakeControl(context.Background())
	if err != nil {
		t.Fatalf("ResumeAfterTakeControl() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventCommandResult {
		t.Fatalf("expected command result only, got %#v", events)
	}
	result, ok := events[0].Payload.(CommandResultSummary)
	if !ok {
		t.Fatalf("expected command result payload, got %#v", events[0].Payload)
	}
	if result.State != CommandExecutionCanceled {
		t.Fatalf("expected canceled reconcile result, got %#v", result)
	}
	if result.ExitCode != shell.InterruptedExitCode {
		t.Fatalf("expected interrupted exit code, got %#v", result)
	}
	if result.Confidence != shell.ConfidenceMedium {
		t.Fatalf("expected medium confidence, got %#v", result)
	}
	if controller.ActiveExecution() != nil {
		t.Fatal("expected active execution to clear after inferred handoff reconcile")
	}
}

func TestLocalControllerResumeAfterTakeControlReconcilesCtrlCWithUnparseableLocalPrompt(t *testing.T) {
	reader := &stubContextReader{
		snapshot: "^C\n➜ shuttle git:(uitweaks) ✗",
		observed: shell.ObservedShellState{
			CurrentPaneCommand: "zsh",
		},
	}
	controller := New(nil, nil, reader, SessionContext{
		TrackedShell: TrackedShellTarget{PaneID: "%0"},
		CurrentShell: &shell.PromptContext{
			User:         "localuser",
			Host:         "workstation",
			Directory:    "/workspace/project",
			GitBranch:    "uitweaks",
			PromptSymbol: "%",
			RawLine:      "localuser@workstation /workspace/project git:(uitweaks) %",
		},
	})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 15",
		Origin:    CommandOriginUserShell,
		State:     CommandExecutionRunning,
		StartedAt: time.Now().Add(-10 * time.Second),
	})

	events, err := controller.ResumeAfterTakeControl(context.Background())
	if err != nil {
		t.Fatalf("ResumeAfterTakeControl() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventCommandResult {
		t.Fatalf("expected command result only, got %#v", events)
	}
	result, ok := events[0].Payload.(CommandResultSummary)
	if !ok {
		t.Fatalf("expected command result payload, got %#v", events[0].Payload)
	}
	if result.State != CommandExecutionCanceled || result.ExitCode != shell.InterruptedExitCode {
		t.Fatalf("expected canceled handoff reconcile, got %#v", result)
	}
	if controller.ActiveExecution() != nil {
		t.Fatal("expected active execution to clear after local ctrl-c reconcile")
	}
}

func TestLocalControllerResumeAfterTakeControlReconcilesCtrlCInRemoteShellWrapper(t *testing.T) {
	reader := &stubContextReader{
		snapshot: "^C\nopenclaw@openclaw [~] custom-prompt",
		observed: shell.ObservedShellState{
			CurrentPaneCommand: "ssh",
			Location: shell.ShellLocation{
				Kind:      shell.ShellLocationRemote,
				User:      "openclaw",
				Host:      "openclaw",
				Directory: "/home/openclaw",
			},
		},
	}
	controller := New(nil, nil, reader, SessionContext{
		TrackedShell: TrackedShellTarget{PaneID: "%0"},
		CurrentShell: &shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "/home/openclaw",
			PromptSymbol: "$",
			Remote:       true,
			RawLine:      "openclaw@openclaw /home/openclaw $",
		},
		CurrentShellLocation: &shell.ShellLocation{
			Kind:      shell.ShellLocationRemote,
			User:      "openclaw",
			Host:      "openclaw",
			Directory: "/home/openclaw",
		},
	})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:        "cmd-1",
		Command:   "tail -f AGENTS.md",
		Origin:    CommandOriginUserShell,
		State:     CommandExecutionRunning,
		StartedAt: time.Now().Add(-10 * time.Second),
	})

	events, err := controller.ResumeAfterTakeControl(context.Background())
	if err != nil {
		t.Fatalf("ResumeAfterTakeControl() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventCommandResult {
		t.Fatalf("expected command result only, got %#v", events)
	}
	result, ok := events[0].Payload.(CommandResultSummary)
	if !ok {
		t.Fatalf("expected command result payload, got %#v", events[0].Payload)
	}
	if result.State != CommandExecutionCanceled || result.ExitCode != shell.InterruptedExitCode {
		t.Fatalf("expected canceled remote handoff reconcile, got %#v", result)
	}
	if controller.ActiveExecution() != nil {
		t.Fatal("expected active execution to clear after remote ctrl-c reconcile")
	}
}

func TestLocalControllerResumeAfterTakeControlDoesNotReconcileWithoutCurrentPrompt(t *testing.T) {
	reader := &stubContextReader{
		snapshot: "localuser@workstation ~/workspace/project %\n. '/run/user/1000/shuttle/shell-integration/zsh-pane0.sh'\nsleep 20",
		contexts: []shell.PromptContext{{}},
	}
	controller := New(nil, nil, reader, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:            "cmd-1",
		Command:       "sleep 20",
		Origin:        CommandOriginUserShell,
		OwnershipMode: CommandOwnershipSharedObserver,
		State:         CommandExecutionRunning,
		StartedAt:     time.Now().Add(-10 * time.Second),
	})

	events, err := controller.ResumeAfterTakeControl(context.Background())
	if err != nil {
		t.Fatalf("ResumeAfterTakeControl() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no reconcile events without a current prompt, got %#v", events)
	}
	if active := controller.ActiveExecution(); active == nil || active.ID != "cmd-1" {
		t.Fatalf("expected active execution to remain running, got %#v", active)
	}
}

func TestLocalControllerResumeAfterTakeControlDoesNotReenterAgentWhileTrackedExecutionStillActive(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "This should not be called.",
		},
	}
	reader := &stubContextReader{
		snapshot: "still scanning",
		contexts: []shell.PromptContext{{}},
	}
	controller := New(agent, nil, reader, SessionContext{
		SessionName:  "shuttle-test",
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
	})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:        "cmd-1",
		Command:   "find . -type f -name '*.md'",
		Origin:    CommandOriginAgentProposal,
		State:     CommandExecutionRunning,
		StartedAt: time.Now().Add(-10 * time.Second),
		TrackedShell: TrackedShellTarget{
			SessionName: "shuttle-test",
			PaneID:      "%0",
		},
	})

	events, err := controller.ResumeAfterTakeControl(context.Background())
	if err != nil {
		t.Fatalf("ResumeAfterTakeControl() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no resume events while tracked execution is still active, got %#v", events)
	}
	if agent.lastInput.Prompt != "" {
		t.Fatalf("expected no agent re-entry while command is still active, got %#v", agent.lastInput)
	}
	if active := controller.ActiveExecution(); active == nil || active.ID != "cmd-1" {
		t.Fatalf("expected active execution to remain running, got %#v", active)
	}
}

func TestLocalControllerResumeAfterTakeControlPrefersAttachedExecutionTail(t *testing.T) {
	exitCode := 0
	reader := &stubContextReader{
		snapshot: "localuser@workstation ~/workspace/other-project %\n. '/run/user/1000/shuttle/commands/noisy.sh'",
		context: shell.PromptContext{
			User:         "localuser",
			Host:         "workstation",
			Directory:    "~/workspace/other-project",
			PromptSymbol: "%",
			RawLine:      "localuser@workstation ~/workspace/other-project %",
			LastExitCode: &exitCode,
		},
	}
	controller := New(nil, nil, reader, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:               "cmd-1",
		Command:          "sleep",
		Origin:           CommandOriginUserShell,
		OwnershipMode:    CommandOwnershipSharedObserver,
		State:            CommandExecutionRunning,
		StartedAt:        time.Now().Add(-10 * time.Second),
		LatestOutputTail: "",
	})
	controller.mu.Lock()
	current := controller.executionLocked("cmd-1")
	current.LatestOutputTail = ""
	controller.mu.Unlock()

	// Simulate the attached foreground monitor having already observed the real command window.
	controller.mu.Lock()
	controller.executionLocked("cmd-1").LatestOutputTail = "tick 1\ntick 2"
	controller.mu.Unlock()

	events, err := controller.ResumeAfterTakeControl(context.Background())
	if err != nil {
		t.Fatalf("ResumeAfterTakeControl() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventCommandResult {
		t.Fatalf("expected command result only, got %#v", events)
	}
	result, ok := events[0].Payload.(CommandResultSummary)
	if !ok {
		t.Fatalf("expected command result payload, got %#v", events[0].Payload)
	}
	if result.Summary != "tick 1\ntick 2" {
		t.Fatalf("expected attached execution tail, got %q", result.Summary)
	}
}

func TestLocalControllerResumeAfterTakeControlReturnsTrackedShellChangeNotice(t *testing.T) {
	exitCode := shell.InterruptedExitCode
	reader := &stubContextReader{
		snapshot:       "^C\nlocaluser@workstation ~/workspace/project %",
		resolvedPaneID: "%5",
		context: shell.PromptContext{
			User:         "localuser",
			Host:         "workstation",
			Directory:    "/workspace/project",
			PromptSymbol: "%",
			RawLine:      "localuser@workstation ~/workspace/project %",
			LastExitCode: &exitCode,
		},
	}
	controller := New(nil, nil, reader, SessionContext{SessionName: "shuttle-test", TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"}})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 10",
		Origin:    CommandOriginUserShell,
		State:     CommandExecutionRunning,
		StartedAt: time.Now().Add(-10 * time.Second),
	})

	events, err := controller.ResumeAfterTakeControl(context.Background())
	if err != nil {
		t.Fatalf("ResumeAfterTakeControl() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected tracked-shell notice plus result, got %#v", events)
	}
	if events[0].Kind != EventSystemNotice || events[1].Kind != EventCommandResult {
		t.Fatalf("unexpected event sequence: %#v", events)
	}
	firstNotice, ok := events[0].Payload.(TextPayload)
	if !ok || !strings.Contains(firstNotice.Text, "Tracked shell pane changed from %0 to %5.") {
		t.Fatalf("expected tracked-shell change notice, got %#v", events[0].Payload)
	}
}

func TestLocalControllerSubmitShellCommandLostReturnsResultEvent(t *testing.T) {
	controller := New(nil, &stubRunner{
		result: shell.TrackedExecution{
			CommandID:  "cmd-1",
			Command:    "rg -n foo ~",
			State:      shell.MonitorStateLost,
			Cause:      shell.CompletionCauseUnknown,
			Confidence: shell.ConfidenceLow,
			Captured:   "partial output",
		},
		err: context.DeadlineExceeded,
	}, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitShellCommand(context.Background(), "rg -n foo ~")
	if err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Kind != EventCommandStart || events[1].Kind != EventCommandResult {
		t.Fatalf("unexpected event kinds: %#v", events)
	}

	resultPayload, ok := events[1].Payload.(CommandResultSummary)
	if !ok {
		t.Fatalf("expected command result payload, got %#v", events[1].Payload)
	}
	if resultPayload.State != CommandExecutionLost {
		t.Fatalf("expected lost result state, got %q", resultPayload.State)
	}
	if resultPayload.Confidence != shell.ConfidenceLow {
		t.Fatalf("expected low confidence, got %q", resultPayload.Confidence)
	}
	if resultPayload.Summary != "partial output" {
		t.Fatalf("expected partial output summary, got %q", resultPayload.Summary)
	}
	if controller.ActiveExecution() != nil {
		t.Fatal("expected active execution to clear after lost result")
	}
	if controller.task.LastCommandResult == nil || controller.task.LastCommandResult.State != CommandExecutionLost {
		t.Fatalf("expected lost last command result, got %#v", controller.task.LastCommandResult)
	}
}

func TestLocalControllerAgentOwnedLostTriggersRecoveryInference(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Tracking is low-confidence. Use F2 to inspect the shell or review the recovery snapshot.",
		},
	}
	controller := New(agent, &stubRunner{
		result: shell.TrackedExecution{
			CommandID:  "cmd-2",
			Command:    "rg -n foo ~",
			State:      shell.MonitorStateLost,
			Cause:      shell.CompletionCauseUnknown,
			Confidence: shell.ConfidenceLow,
			Captured:   "partial output",
		},
		err: context.DeadlineExceeded,
	}, &stubContextReader{
		output: "recovery line 1\nrecovery line 2",
	}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitProposedShellCommand(context.Background(), "rg -n foo ~")
	if err != nil {
		t.Fatalf("SubmitProposedShellCommand() error = %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Kind != EventCommandStart || events[1].Kind != EventCommandResult || events[2].Kind != EventAgentMessage {
		t.Fatalf("unexpected event kinds: %#v", events)
	}

	if !strings.Contains(agent.lastInput.Prompt, lostTrackingCheckInPrompt) {
		t.Fatalf("expected lost recovery prompt, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, stateAuthorityPromptSuffix) {
		t.Fatalf("expected state-authority guidance, got %q", agent.lastInput.Prompt)
	}
	if agent.lastInput.Task.CurrentExecution == nil || agent.lastInput.Task.CurrentExecution.State != CommandExecutionLost {
		t.Fatalf("expected lost current execution in agent input, got %#v", agent.lastInput.Task.CurrentExecution)
	}
	if agent.lastInput.Task.RecoverySnapshot != "recovery line 1\nrecovery line 2" {
		t.Fatalf("expected recovery snapshot, got %q", agent.lastInput.Task.RecoverySnapshot)
	}
}

func TestLocalControllerSubmitProposedShellCommandTracksAgentOrigin(t *testing.T) {
	controller := New(nil, &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-2",
			Command:   "ls -lah",
			ExitCode:  0,
			Captured:  "file.txt",
		},
	}, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitProposedShellCommand(context.Background(), "ls -lah")
	if err != nil {
		t.Fatalf("SubmitProposedShellCommand() error = %v", err)
	}

	startPayload, ok := events[0].Payload.(CommandStartPayload)
	if !ok {
		t.Fatalf("expected command start payload, got %#v", events[0].Payload)
	}
	if startPayload.Execution.Origin != CommandOriginAgentProposal {
		t.Fatalf("expected proposal origin, got %q", startPayload.Execution.Origin)
	}

	resultPayload, ok := events[1].Payload.(CommandResultSummary)
	if !ok {
		t.Fatalf("expected command result payload, got %#v", events[1].Payload)
	}
	if resultPayload.Origin != CommandOriginAgentProposal {
		t.Fatalf("expected proposal origin in result, got %q", resultPayload.Origin)
	}
}

func TestLocalControllerSubmitProposedShellCommandUsesOwnedExecutionPane(t *testing.T) {
	runner := &ownedExecutionRunner{
		stubRunner: stubRunner{
			result: shell.TrackedExecution{
				CommandID: "cmd-2",
				Command:   "ls -lah",
				ExitCode:  0,
				Captured:  "file.txt",
			},
		},
		ownedPane: shell.OwnedExecutionPane{
			SessionName: "shuttle-test",
			PaneID:      "%9",
			WindowID:    "@3",
		},
	}
	controller := New(nil, runner, nil, SessionContext{
		SessionName:      "shuttle-test",
		TrackedShell:     TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
		WorkingDirectory: "/tmp/project",
		CurrentShell: &shell.PromptContext{
			User:         "localuser",
			Host:         "workstation",
			Directory:    "/tmp/project",
			PromptSymbol: "%",
			RawLine:      "localuser@workstation /tmp/project %",
		},
	})

	events, err := controller.SubmitProposedShellCommand(context.Background(), "ls -lah")
	if err != nil {
		t.Fatalf("SubmitProposedShellCommand() error = %v", err)
	}

	if runner.startDir != "/tmp/project" {
		t.Fatalf("expected owned execution to inherit user-shell cwd, got %q", runner.startDir)
	}
	if len(runner.paneIDs) != 1 || runner.paneIDs[0] != "%9" {
		t.Fatalf("expected owned execution pane %%9, got %#v", runner.paneIDs)
	}
	if runner.cleanupCalls != 1 {
		t.Fatalf("expected owned execution cleanup to run once, got %d", runner.cleanupCalls)
	}
	if controller.TrackedShellTarget().PaneID != "%0" {
		t.Fatalf("expected persistent tracked shell pane to remain %%0, got %#v", controller.TrackedShellTarget())
	}
	if len(events) != 3 || events[0].Kind != EventSystemNotice || events[1].Kind != EventCommandStart || events[2].Kind != EventCommandResult {
		t.Fatalf("expected owned-execution notice plus start/result, got %#v", events)
	}
	notice, ok := events[0].Payload.(TextPayload)
	if !ok || strings.Contains(notice.Text, "%9") || !strings.Contains(notice.Text, "owned execution pane") {
		t.Fatalf("expected generic owned execution notice, got %#v", events[0].Payload)
	}
	startPayload, ok := events[1].Payload.(CommandStartPayload)
	if !ok {
		t.Fatalf("expected command start payload, got %#v", events[1].Payload)
	}
	if startPayload.Execution.TrackedShell.PaneID != "%9" {
		t.Fatalf("expected execution target pane %%9, got %#v", startPayload.Execution.TrackedShell)
	}
}

func TestLocalControllerBlocksInternalTmuxPaneProposal(t *testing.T) {
	controller := New(nil, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 15",
		Origin:    CommandOriginAgentProposal,
		State:     CommandExecutionRunning,
		StartedAt: time.Now(),
		TrackedShell: TrackedShellTarget{
			SessionName: "shuttle-test",
			PaneID:      "%9",
		},
	})

	events, err := controller.SubmitProposedShellCommand(context.Background(), "tmux capture-pane -pt %9 -S -80")
	if err != nil {
		t.Fatalf("SubmitProposedShellCommand() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventSystemNotice {
		t.Fatalf("expected internal tmux guard notice, got %#v", events)
	}
	notice, ok := events[0].Payload.(TextPayload)
	if !ok || !strings.Contains(notice.Text, "Shuttle-managed tmux pane IDs") {
		t.Fatalf("expected guard notice payload, got %#v", events[0].Payload)
	}
	if controller.ActiveExecution() == nil || controller.ActiveExecution().Command != "sleep 15" {
		t.Fatalf("expected active execution to remain unchanged, got %#v", controller.ActiveExecution())
	}
}

func TestLocalControllerSubmitProposedShellCommandUsesTrackedShellWhenUserShellRemote(t *testing.T) {
	runner := &ownedExecutionRunner{
		stubRunner: stubRunner{
			result: shell.TrackedExecution{
				CommandID: "cmd-2",
				Command:   "touch openclawfoo.txt",
				ExitCode:  0,
			},
		},
		ownedPane: shell.OwnedExecutionPane{
			SessionName: "shuttle-test",
			PaneID:      "%9",
			WindowID:    "@3",
		},
	}
	controller := New(nil, runner, nil, SessionContext{
		SessionName:      "shuttle-test",
		TrackedShell:     TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
		WorkingDirectory: "/workspace/home",
		CurrentShell: &shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "/home/openclaw",
			PromptSymbol: "$",
			RawLine:      "openclaw@openclaw /home/openclaw $",
			Remote:       true,
		},
	})

	events, err := controller.SubmitProposedShellCommand(context.Background(), "touch openclawfoo.txt")
	if err != nil {
		t.Fatalf("SubmitProposedShellCommand() error = %v", err)
	}

	if runner.startDir != "" {
		t.Fatalf("expected no owned execution pane for remote shell, got start dir %q", runner.startDir)
	}
	if len(runner.paneIDs) != 1 || runner.paneIDs[0] != "%0" {
		t.Fatalf("expected tracked shell pane %%0, got %#v", runner.paneIDs)
	}
	if len(events) != 2 || events[0].Kind != EventCommandStart || events[1].Kind != EventCommandResult {
		t.Fatalf("expected direct tracked-shell execution start/result, got %#v", events)
	}
}

func TestLocalControllerSubmitProposedShellCommandRepairsPatchableRemoteEdit(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Proposal: &Proposal{
				Kind:        ProposalPatch,
				Patch:       diffNewFileFixture("foo.txt", "hello\n"),
				PatchTarget: PatchTargetRemoteShell,
				Description: "Apply a remote patch instead.",
			},
		},
	}
	controller := New(agent, &stubRunner{}, &stubContextReader{
		context: shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "/home/openclaw",
			PromptSymbol: "$",
			RawLine:      "openclaw@openclaw /home/openclaw $",
			Remote:       true,
		},
	}, SessionContext{
		SessionName:      "shuttle-test",
		TrackedShell:     TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
		WorkingDirectory: "/workspace/project",
	})

	events, err := controller.SubmitProposedShellCommand(context.Background(), "sed -i 's/foo/bar/' /home/openclaw/foo.txt && cat /home/openclaw/foo.txt")
	if err != nil {
		t.Fatalf("SubmitProposedShellCommand() error = %v", err)
	}

	if len(events) != 1 || events[0].Kind != EventProposal {
		t.Fatalf("expected repaired proposal event, got %#v", events)
	}
	payload, _ := events[0].Payload.(ProposalPayload)
	if payload.Kind != ProposalPatch || payload.PatchTarget != PatchTargetRemoteShell {
		t.Fatalf("expected remote patch proposal, got %#v", payload)
	}
	if !strings.Contains(agent.lastInput.Prompt, "Do not emit another shell mutation command.") {
		t.Fatalf("expected repair prompt, got %q", agent.lastInput.Prompt)
	}
}

func TestLocalControllerSubmitProposedShellCommandConvertsPatchableRemoteEditToApprovalWhenRepairFails(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "I still think a shell command is fine here.",
			Proposal: &Proposal{
				Kind:    ProposalCommand,
				Command: "sed -i 's/foo/bar/' /home/openclaw/foo.txt",
			},
		},
	}
	controller := New(agent, &stubRunner{}, &stubContextReader{
		context: shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "/home/openclaw",
			PromptSymbol: "$",
			RawLine:      "openclaw@openclaw /home/openclaw $",
			Remote:       true,
		},
	}, SessionContext{
		SessionName:      "shuttle-test",
		TrackedShell:     TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
		WorkingDirectory: "/workspace/project",
	})

	events, err := controller.SubmitProposedShellCommand(context.Background(), "sed -i 's/foo/bar/' /home/openclaw/foo.txt")
	if err != nil {
		t.Fatalf("SubmitProposedShellCommand() error = %v", err)
	}

	if len(events) != 2 || events[0].Kind != EventApproval || events[1].Kind != EventSystemNotice {
		t.Fatalf("expected approval plus notice, got %#v", events)
	}
	if controller.task.PendingApproval == nil || controller.task.PendingApproval.Kind != ApprovalCommand {
		t.Fatalf("expected pending approval, got %#v", controller.task.PendingApproval)
	}
	if !strings.Contains(controller.task.PendingApproval.Summary, "prefer native patches") {
		t.Fatalf("expected approval summary to mention patches, got %#v", controller.task.PendingApproval)
	}
}

func TestLocalControllerOwnedExecutionDoesNotOverwriteUserShellContext(t *testing.T) {
	runner := &ownedExecutionRunner{
		stubRunner: stubRunner{
			result: shell.TrackedExecution{
				CommandID: "cmd-2",
				Command:   "pwd",
				ExitCode:  0,
				Captured:  "/tmp/owned",
				ShellContext: shell.PromptContext{
					User:         "localuser",
					Host:         "workstation",
					Directory:    "/tmp/owned",
					PromptSymbol: "%",
					RawLine:      "localuser@workstation /tmp/owned %",
				},
			},
		},
		ownedPane: shell.OwnedExecutionPane{
			SessionName: "shuttle-test",
			PaneID:      "%9",
			WindowID:    "@3",
		},
	}
	controller := New(nil, runner, nil, SessionContext{
		SessionName:      "shuttle-test",
		TrackedShell:     TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
		WorkingDirectory: "/workspace/project",
		CurrentShell: &shell.PromptContext{
			User:         "localuser",
			Host:         "workstation",
			Directory:    "/workspace/project",
			PromptSymbol: "%",
			RawLine:      "localuser@workstation ~/workspace/project %",
		},
	})

	if _, err := controller.SubmitProposedShellCommand(context.Background(), "pwd"); err != nil {
		t.Fatalf("SubmitProposedShellCommand() error = %v", err)
	}

	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.session.WorkingDirectory != "/workspace/project" {
		t.Fatalf("expected user-shell cwd to remain unchanged, got %q", controller.session.WorkingDirectory)
	}
	if controller.session.CurrentShell == nil || controller.session.CurrentShell.Directory != "/workspace/project" {
		t.Fatalf("expected user-shell prompt context to remain unchanged, got %#v", controller.session.CurrentShell)
	}
	if controller.task.LastCommandResult == nil || controller.task.LastCommandResult.Summary != "/tmp/owned" {
		t.Fatalf("expected owned execution result summary to be preserved, got %#v", controller.task.LastCommandResult)
	}
}

func TestLocalControllerDirectShellCommandRefreshesTrackedUserShellContext(t *testing.T) {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	goLearnDirectory := filepath.Join(homeDirectory, "workspace", "other-project")

	runner := &ownedExecutionRunner{
		stubRunner: stubRunner{
			result: shell.TrackedExecution{
				CommandID: "cmd-1",
				ExitCode:  0,
			},
		},
		ownedPane: shell.OwnedExecutionPane{
			SessionName: "shuttle-test",
			PaneID:      "%9",
			WindowID:    "@3",
		},
	}
	reader := &stubContextReader{
		contexts: []shell.PromptContext{
			{
				User:         "localuser",
				Host:         "workstation",
				Directory:    "~/workspace",
				PromptSymbol: "%",
				RawLine:      "localuser@workstation ~/workspace %",
			},
			{
				User:         "localuser",
				Host:         "workstation",
				Directory:    "~/workspace/other-project",
				PromptSymbol: "%",
				RawLine:      "localuser@workstation ~/workspace/other-project %",
			},
			{
				User:         "localuser",
				Host:         "workstation",
				Directory:    "~/workspace/other-project",
				PromptSymbol: "%",
				RawLine:      "localuser@workstation ~/workspace/other-project %",
			},
		},
	}
	controller := New(nil, runner, reader, SessionContext{
		SessionName:      "shuttle-test",
		TrackedShell:     TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
		WorkingDirectory: filepath.Join(homeDirectory, "source/repos"),
		CurrentShell: &shell.PromptContext{
			User:         "localuser",
			Host:         "workstation",
			Directory:    "~/workspace",
			PromptSymbol: "%",
			RawLine:      "localuser@workstation ~/workspace %",
		},
	})

	if _, err := controller.SubmitShellCommand(context.Background(), "cd go_learn"); err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}

	controller.mu.Lock()
	if controller.session.WorkingDirectory != goLearnDirectory {
		controller.mu.Unlock()
		t.Fatalf("expected refreshed working directory %q, got %q", goLearnDirectory, controller.session.WorkingDirectory)
	}
	if controller.session.CurrentShell == nil || controller.session.CurrentShell.Directory != "~/workspace/other-project" {
		controller.mu.Unlock()
		t.Fatalf("expected refreshed prompt context, got %#v", controller.session.CurrentShell)
	}
	controller.mu.Unlock()

	if _, err := controller.SubmitProposedShellCommand(context.Background(), "tail -n 10 README.md"); err != nil {
		t.Fatalf("SubmitProposedShellCommand() error = %v", err)
	}

	if runner.startDir != goLearnDirectory {
		t.Fatalf("expected owned execution start dir %q, got %q", goLearnDirectory, runner.startDir)
	}
}

func TestLocalControllerActiveExecutionVisibleWhileCommandRuns(t *testing.T) {
	runner := &blockingRunner{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
		result: shell.TrackedExecution{
			CommandID: "cmd-blocking",
			Command:   "sleep 5",
			ExitCode:  0,
			Captured:  "done",
		},
	}
	controller := New(nil, runner, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = controller.SubmitShellCommand(context.Background(), "sleep 5")
	}()

	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runner to start")
	}

	active := controller.ActiveExecution()
	if active == nil {
		t.Fatal("expected active execution while command is running")
	}
	if active.Command != "sleep 5" || active.State != CommandExecutionRunning {
		t.Fatalf("unexpected active execution: %#v", active)
	}

	close(runner.release)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runner to finish")
	}

	if controller.ActiveExecution() != nil {
		t.Fatal("expected active execution to clear after completion")
	}
}

func TestLocalControllerSubmitProposedShellCommandUsesTrackedShellWhenFreshPromptIsRemote(t *testing.T) {
	runner := &ownedExecutionRunner{
		ownedPane: shell.OwnedExecutionPane{SessionName: "shuttle-test", PaneID: "%9"},
	}
	runner.result = shell.TrackedExecution{
		CommandID: "cmd-1",
		Command:   "find . -name implementation-plan.md",
		ExitCode:  0,
		Captured:  "implementation-plan.md",
	}
	reader := &stubContextReader{
		context: shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "~",
			PromptSymbol: "$",
			RawLine:      "openclaw@openclaw ~ $",
			Remote:       true,
		},
	}
	controller := New(nil, runner, reader, SessionContext{
		SessionName:  "shuttle-test",
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
		CurrentShell: &shell.PromptContext{
			User:         "jsmith",
			Host:         "linuxdesktop",
			Directory:    "~/source/repos/aiterm",
			PromptSymbol: "%",
			RawLine:      "jsmith@linuxdesktop ~/source/repos/aiterm %",
			Remote:       false,
		},
	})

	if _, err := controller.SubmitProposedShellCommand(context.Background(), "find . -name implementation-plan.md"); err != nil {
		t.Fatalf("SubmitProposedShellCommand() error = %v", err)
	}
	if runner.startDir != "" {
		t.Fatalf("expected no owned execution pane for fresh remote prompt, got start dir %q", runner.startDir)
	}
	if len(runner.paneIDs) != 1 || runner.paneIDs[0] != "%0" {
		t.Fatalf("expected command to run in tracked shell pane %%0, got %#v", runner.paneIDs)
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.session.CurrentShell == nil || !controller.session.CurrentShell.Remote || controller.session.CurrentShell.Host != "openclaw" {
		t.Fatalf("expected session current shell to refresh to remote prompt, got %#v", controller.session.CurrentShell)
	}
}

func TestSubmitProposedShellCommandRepairsRemoteLocalPathProposal(t *testing.T) {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}

	agent := &stubAgent{
		response: AgentResponse{
			Message: "Use a remote-relative inspection command instead.",
			Proposal: &Proposal{
				Kind:        ProposalCommand,
				Command:     "find ~ -name foo.txt -type f -print",
				Description: "Find the file on the remote shell.",
			},
		},
	}
	controller := New(agent, nil, nil, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		LocalHomeDirectory: filepath.Join(homeDirectory, "..", "stale-home"),
		LocalWorkspaceRoot: filepath.Join(homeDirectory, "source", "repos", "aiterm"),
		CurrentShell: &shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "/home/openclaw",
			PromptSymbol: "$",
			RawLine:      "openclaw@openclaw ~ $",
			Remote:       true,
		},
	})

	events, err := controller.SubmitProposedShellCommand(context.Background(), fmt.Sprintf("find %s -name foo.txt -type f -print", homeDirectory))
	if err != nil {
		t.Fatalf("SubmitProposedShellCommand() error = %v", err)
	}
	if !strings.Contains(agent.lastInput.Prompt, "referenced local machine paths") {
		t.Fatalf("expected remote local-path repair prompt, got %q", agent.lastInput.Prompt)
	}
	last := events[len(events)-1]
	if last.Kind != EventProposal {
		t.Fatalf("expected repaired proposal event, got %#v", events)
	}
	payload := last.Payload.(ProposalPayload)
	if payload.Command != "find ~ -name foo.txt -type f -print" {
		t.Fatalf("expected remote-safe replacement command, got %#v", payload)
	}
}

func TestLocalControllerAbandonActiveExecutionClearsState(t *testing.T) {
	controller := New(nil, nil, &stubContextReader{output: "tail line"}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 60",
		Origin:    CommandOriginUserShell,
		State:     CommandExecutionRunning,
		StartedAt: time.Now(),
	})

	execution := controller.AbandonActiveExecution("user interrupted from handoff")
	if execution == nil {
		t.Fatal("expected execution snapshot")
	}
	if execution.State != CommandExecutionCanceled {
		t.Fatalf("expected canceled state, got %q", execution.State)
	}
	if execution.Error != "user interrupted from handoff" {
		t.Fatalf("expected abandon reason, got %q", execution.Error)
	}
	if execution.LatestOutputTail != "tail line" {
		t.Fatalf("expected captured tail, got %q", execution.LatestOutputTail)
	}
	if controller.ActiveExecution() != nil {
		t.Fatal("expected active execution to clear")
	}
	if len(controller.task.PriorTranscript) == 0 {
		t.Fatal("expected abandon to append a transcript notice")
	}
	last := controller.task.PriorTranscript[len(controller.task.PriorTranscript)-1]
	if last.Kind != EventSystemNotice {
		t.Fatalf("expected system notice after abandon, got %#v", last)
	}
	notice, ok := last.Payload.(TextPayload)
	if !ok || !strings.Contains(notice.Text, "Abandoned active execution: sleep 60") {
		t.Fatalf("expected abandon notice payload, got %#v", last.Payload)
	}
}

func TestLocalControllerIgnoresLateResultAfterAbandon(t *testing.T) {
	runner := &blockingRunner{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
		result: shell.TrackedExecution{
			CommandID: "cmd-blocking",
			Command:   "sleep 60",
			ExitCode:  0,
			Captured:  "done",
		},
	}
	controller := New(nil, runner, &stubContextReader{output: "^C"}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	resultCh := make(chan struct {
		events []TranscriptEvent
		err    error
	}, 1)
	go func() {
		events, err := controller.SubmitShellCommand(context.Background(), "sleep 60")
		resultCh <- struct {
			events []TranscriptEvent
			err    error
		}{events: events, err: err}
	}()

	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runner to start")
	}

	controller.AbandonActiveExecution("user interrupted from handoff")
	close(runner.release)

	select {
	case result := <-resultCh:
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("expected late result to be treated as canceled, got err=%v events=%#v", result.err, result.events)
		}
		if len(result.events) != 0 {
			t.Fatalf("expected no late events after abandon, got %#v", result.events)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for submit shell command to finish")
	}
}

func TestLocalControllerCheckActiveExecutionUsesAgentContext(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Still waiting on the active command.",
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:               "cmd-agent",
		Command:          "sleep 60",
		Origin:           CommandOriginAgentProposal,
		State:            CommandExecutionRunning,
		StartedAt:        time.Now().Add(-15 * time.Second),
		LatestOutputTail: "line 1\nline 2\nline 3",
	})

	events, err := controller.CheckActiveExecution(context.Background())
	if err != nil {
		t.Fatalf("CheckActiveExecution() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventAgentMessage {
		t.Fatalf("unexpected check-in events: %#v", events)
	}
	if !strings.Contains(agent.lastInput.Prompt, activeExecutionCheckInPrompt) {
		t.Fatalf("expected running check-in prompt, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, stateAuthorityPromptSuffix) {
		t.Fatalf("expected state-authority guidance, got %q", agent.lastInput.Prompt)
	}
	if agent.lastInput.Task.CurrentExecution == nil || agent.lastInput.Task.CurrentExecution.State != CommandExecutionBackgroundMonitor {
		t.Fatalf("expected background monitoring state, got %#v", agent.lastInput.Task.CurrentExecution)
	}
	if agent.lastInput.Session.RecentShellOutput != "line 1\nline 2\nline 3" {
		t.Fatalf("expected recent shell output in agent input, got %q", agent.lastInput.Session.RecentShellOutput)
	}
}

func TestLocalControllerPeekShellTailUsesActiveExecutionPane(t *testing.T) {
	reader := &stubContextReader{output: "running"}
	controller := New(nil, nil, reader, SessionContext{
		SessionName:  "shuttle-test",
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
	})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:           "cmd-1",
		Command:      "sleep 20",
		Origin:       CommandOriginAgentProposal,
		State:        CommandExecutionRunning,
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%9"},
	})

	tail, err := controller.PeekShellTail(context.Background(), 20)
	if err != nil {
		t.Fatalf("PeekShellTail() error = %v", err)
	}
	if tail != "running" {
		t.Fatalf("expected tail output, got %q", tail)
	}
	if len(reader.paneIDs) == 0 || reader.paneIDs[len(reader.paneIDs)-1] != "%9" {
		t.Fatalf("expected active execution pane %%9, got %#v", reader.paneIDs)
	}
}

func TestLocalControllerCheckActiveExecutionPreservesAwaitingInputState(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "The command is waiting for input.",
		},
	}
	controller := New(agent, nil, &stubContextReader{snapshot: "snapshot lines"}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:               "cmd-agent",
		Command:          "python3 -c \"input('name: ')\"",
		Origin:           CommandOriginAgentProposal,
		State:            CommandExecutionAwaitingInput,
		StartedAt:        time.Now().Add(-15 * time.Second),
		LatestOutputTail: "name:",
	})

	_, err := controller.CheckActiveExecution(context.Background())
	if err != nil {
		t.Fatalf("CheckActiveExecution() error = %v", err)
	}
	if !strings.Contains(agent.lastInput.Prompt, awaitingInputCheckInPrompt) {
		t.Fatalf("expected awaiting-input prompt, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, stateAuthorityPromptSuffix) {
		t.Fatalf("expected state-authority guidance, got %q", agent.lastInput.Prompt)
	}
	if agent.lastInput.Task.CurrentExecution == nil || agent.lastInput.Task.CurrentExecution.State != CommandExecutionAwaitingInput {
		t.Fatalf("expected awaiting_input state to be preserved, got %#v", agent.lastInput.Task.CurrentExecution)
	}
	if agent.lastInput.Task.RecoverySnapshot != "snapshot lines" {
		t.Fatalf("expected recovery snapshot, got %q", agent.lastInput.Task.RecoverySnapshot)
	}
}

func TestLocalControllerCheckActiveExecutionPreservesInteractiveFullscreenState(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "The command is occupying a fullscreen terminal app.",
		},
	}
	controller := New(agent, nil, &stubContextReader{snapshot: "fullscreen snapshot"}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:               "cmd-agent",
		Command:          "wrapped-btop",
		Origin:           CommandOriginAgentProposal,
		State:            CommandExecutionInteractiveFullscreen,
		StartedAt:        time.Now().Add(-15 * time.Second),
		LatestOutputTail: "",
	})

	_, err := controller.CheckActiveExecution(context.Background())
	if err != nil {
		t.Fatalf("CheckActiveExecution() error = %v", err)
	}
	if !strings.Contains(agent.lastInput.Prompt, fullscreenCheckInPrompt) {
		t.Fatalf("expected fullscreen prompt, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, stateAuthorityPromptSuffix) {
		t.Fatalf("expected state-authority guidance, got %q", agent.lastInput.Prompt)
	}
	if agent.lastInput.Task.CurrentExecution == nil || agent.lastInput.Task.CurrentExecution.State != CommandExecutionInteractiveFullscreen {
		t.Fatalf("expected interactive_fullscreen state to be preserved, got %#v", agent.lastInput.Task.CurrentExecution)
	}
	if agent.lastInput.Task.RecoverySnapshot != "fullscreen snapshot" {
		t.Fatalf("expected fullscreen recovery snapshot, got %q", agent.lastInput.Task.RecoverySnapshot)
	}
}

func TestLocalControllerCheckActiveExecutionUsesLostPrompt(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Tracking confidence is low.",
		},
	}
	controller := New(agent, nil, &stubContextReader{snapshot: "recovery lines"}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:               "cmd-agent",
		Command:          "unknown",
		Origin:           CommandOriginAgentProposal,
		State:            CommandExecutionLost,
		StartedAt:        time.Now().Add(-15 * time.Second),
		LatestOutputTail: "weird output",
	})

	_, err := controller.CheckActiveExecution(context.Background())
	if err != nil {
		t.Fatalf("CheckActiveExecution() error = %v", err)
	}
	if !strings.Contains(agent.lastInput.Prompt, lostTrackingCheckInPrompt) {
		t.Fatalf("expected lost prompt, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, stateAuthorityPromptSuffix) {
		t.Fatalf("expected state-authority guidance, got %q", agent.lastInput.Prompt)
	}
	if agent.lastInput.Task.RecoverySnapshot != "recovery lines" {
		t.Fatalf("expected recovery snapshot, got %q", agent.lastInput.Task.RecoverySnapshot)
	}
}

func TestLocalControllerMonitorUpdatesActiveExecutionTail(t *testing.T) {
	monitor := newManualMonitor()
	runner := &monitoringRunner{
		monitor: monitor,
		started: make(chan struct{}, 1),
	}
	controller := New(nil, runner, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = controller.SubmitShellCommand(context.Background(), "sleep 60")
	}()

	runner.waitForStart(t)
	monitor.publish(shell.MonitorSnapshot{
		CommandID:         "cmd-monitor",
		Command:           "sleep 60",
		State:             shell.MonitorStateRunning,
		StartedAt:         time.Now(),
		LatestOutputTail:  "still running",
		ForegroundCommand: "sleep",
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		active := controller.ActiveExecution()
		if active != nil && active.LatestOutputTail == "still running" && active.State == CommandExecutionRunning && active.ForegroundCommand == "sleep" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected active execution to receive monitor tail, got %#v", active)
		}
		time.Sleep(10 * time.Millisecond)
	}

	controller.mu.Lock()
	if controller.task.PrimaryExecutionID == "" || len(controller.task.ExecutionRegistry) != 1 {
		controller.mu.Unlock()
		t.Fatalf("expected one active execution in registry, got primary=%q registry=%#v", controller.task.PrimaryExecutionID, controller.task.ExecutionRegistry)
	}
	registryExecution := controller.task.ExecutionRegistry[0]
	controller.mu.Unlock()
	if registryExecution.Command != "sleep 60" || registryExecution.TrackedShell.PaneID != "%0" || registryExecution.OwnershipMode != CommandOwnershipExclusive {
		t.Fatalf("unexpected registry execution %#v", registryExecution)
	}

	monitor.finish(shell.TrackedExecution{
		CommandID: "cmd-monitor",
		Command:   "sleep 60",
		ExitCode:  0,
		Captured:  "done",
	}, nil)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for monitored command to finish")
	}
}

func TestLocalControllerAttachForegroundExecutionReturnsNoticeAndStart(t *testing.T) {
	monitor := newManualMonitor()
	now := time.Now().Add(-5 * time.Second)
	monitor.snapshot = shell.MonitorSnapshot{
		CommandID:         "cmd-monitor",
		Command:           "sleep 30",
		State:             shell.MonitorStateRunning,
		StartedAt:         now,
		ForegroundCommand: "sleep",
	}
	runner := &monitoringRunner{attachMonitor: monitor}
	controller := New(nil, runner, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, attached, err := controller.attachForegroundExecution(context.Background())
	if err != nil {
		t.Fatalf("attachForegroundExecution() error = %v", err)
	}
	if !attached {
		t.Fatal("expected foreground command to attach")
	}
	if len(events) != 2 || events[0].Kind != EventSystemNotice || events[1].Kind != EventCommandStart {
		t.Fatalf("expected attach notice and command start, got %#v", events)
	}
	notice, ok := events[0].Payload.(TextPayload)
	if !ok || !strings.Contains(notice.Text, "Attached to existing foreground command in the tracked shell: sleep 30") {
		t.Fatalf("expected foreground attach notice, got %#v", events[0].Payload)
	}
	start, ok := events[1].Payload.(CommandStartPayload)
	if !ok {
		t.Fatalf("expected command start payload, got %#v", events[1].Payload)
	}
	if start.Execution.OwnershipMode != CommandOwnershipSharedObserver {
		t.Fatalf("expected shared observer ownership, got %#v", start.Execution)
	}
	if start.Execution.State != CommandExecutionRunning || start.Execution.ForegroundCommand != "sleep" {
		t.Fatalf("unexpected attached execution snapshot %#v", start.Execution)
	}
}

func TestLocalControllerRefreshActiveExecutionAttachesForegroundCommand(t *testing.T) {
	monitor := newManualMonitor()
	now := time.Now().Add(-5 * time.Second)
	monitor.snapshot = shell.MonitorSnapshot{
		CommandID:         "cmd-monitor",
		Command:           "sleep 30",
		State:             shell.MonitorStateRunning,
		StartedAt:         now,
		ForegroundCommand: "sleep",
	}
	runner := &monitoringRunner{attachMonitor: monitor}
	controller := New(nil, runner, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, active, err := controller.RefreshActiveExecution(context.Background())
	if err != nil {
		t.Fatalf("RefreshActiveExecution() error = %v", err)
	}
	if len(events) != 2 || events[0].Kind != EventSystemNotice || events[1].Kind != EventCommandStart {
		t.Fatalf("expected attach notice and command start, got %#v", events)
	}
	if active == nil || active.Command != "sleep 30" || active.State != CommandExecutionRunning {
		t.Fatalf("expected refreshed active execution, got %#v", active)
	}

	monitor.finish(shell.TrackedExecution{
		CommandID: "cmd-monitor",
		Command:   "sleep 30",
		State:     shell.MonitorStateCompleted,
		ExitCode:  0,
		Captured:  "done",
	}, nil)
}

func TestLocalControllerRefreshActiveExecutionDoesNotReattachWhileForegroundExecutionActive(t *testing.T) {
	monitor := newManualMonitor()
	now := time.Now().Add(-5 * time.Second)
	monitor.snapshot = shell.MonitorSnapshot{
		CommandID:         "cmd-monitor",
		Command:           "sleep 30",
		State:             shell.MonitorStateRunning,
		StartedAt:         now,
		ForegroundCommand: "sleep",
	}
	runner := &monitoringRunner{attachMonitor: monitor}
	controller := New(nil, runner, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	_, active, err := controller.RefreshActiveExecution(context.Background())
	if err != nil {
		t.Fatalf("first RefreshActiveExecution() error = %v", err)
	}
	if active == nil {
		t.Fatal("expected attached active execution on first refresh")
	}
	firstID := active.ID

	events, active, err := controller.RefreshActiveExecution(context.Background())
	if err != nil {
		t.Fatalf("second RefreshActiveExecution() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no new events on second refresh, got %#v", events)
	}
	if active == nil || active.ID != firstID {
		t.Fatalf("expected same attached execution on second refresh, got %#v", active)
	}
	if runner.attachCalls != 1 {
		t.Fatalf("expected one foreground attach call, got %d", runner.attachCalls)
	}

	monitor.finish(shell.TrackedExecution{
		CommandID: "cmd-monitor",
		Command:   "sleep 30",
		State:     shell.MonitorStateCompleted,
		ExitCode:  0,
		Captured:  "done",
	}, nil)
}

func TestLocalControllerAttachForegroundExecutionDetachesMonitorLifetimeFromRequestContext(t *testing.T) {
	monitor := newManualMonitor()
	now := time.Now().Add(-5 * time.Second)
	monitor.snapshot = shell.MonitorSnapshot{
		CommandID:         "cmd-monitor",
		Command:           "sleep 30",
		State:             shell.MonitorStateRunning,
		StartedAt:         now,
		ForegroundCommand: "sleep",
	}
	var attachedCtx context.Context
	runner := &monitoringRunner{
		attachFunc: func(ctx context.Context, _ string) (shell.CommandMonitor, error) {
			attachedCtx = ctx
			return monitor, nil
		},
	}
	controller := New(nil, runner, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	requestCtx, cancel := context.WithCancel(context.Background())
	_, active, err := controller.RefreshActiveExecution(requestCtx)
	if err != nil {
		t.Fatalf("RefreshActiveExecution() error = %v", err)
	}
	if active == nil {
		t.Fatal("expected attached active execution")
	}
	if attachedCtx == nil {
		t.Fatal("expected attach runner to receive a context")
	}

	cancel()
	time.Sleep(25 * time.Millisecond)

	if err := attachedCtx.Err(); err != nil {
		t.Fatalf("expected attached monitor context to survive request cancellation, got %v", err)
	}
	if controller.ActiveExecution() == nil {
		t.Fatal("expected active execution to remain after request cancellation")
	}

	monitor.finish(shell.TrackedExecution{
		CommandID: "cmd-monitor",
		Command:   "sleep 30",
		State:     shell.MonitorStateCompleted,
		ExitCode:  0,
		Captured:  "done",
	}, nil)
}

func TestLocalControllerResumeAfterTakeControlAttachesForegroundExecutionAndCompletes(t *testing.T) {
	monitor := newManualMonitor()
	now := time.Now().Add(-5 * time.Second)
	monitor.snapshot = shell.MonitorSnapshot{
		CommandID:         "cmd-monitor",
		Command:           "sleep 30",
		State:             shell.MonitorStateRunning,
		StartedAt:         now,
		ForegroundCommand: "sleep",
	}
	runner := &monitoringRunner{attachMonitor: monitor}
	controller := New(nil, runner, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.ResumeAfterTakeControl(context.Background())
	if err != nil {
		t.Fatalf("ResumeAfterTakeControl() error = %v", err)
	}
	if len(events) != 2 || events[0].Kind != EventSystemNotice || events[1].Kind != EventCommandStart {
		t.Fatalf("expected attach notice and command start, got %#v", events)
	}
	active := controller.ActiveExecution()
	if active == nil || active.State != CommandExecutionRunning || active.Command != "sleep 30" {
		t.Fatalf("expected attached foreground execution to be active, got %#v", active)
	}

	monitor.finish(shell.TrackedExecution{
		CommandID: "cmd-monitor",
		Command:   "sleep 30",
		State:     shell.MonitorStateCompleted,
		ExitCode:  0,
		Captured:  "done",
	}, nil)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if controller.ActiveExecution() == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected attached foreground execution to clear, got %#v", controller.ActiveExecution())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if controller.task.LastCommandResult == nil {
		t.Fatal("expected attached foreground completion summary")
	}
	if controller.task.LastCommandResult.State != CommandExecutionCompleted || controller.task.LastCommandResult.Summary != "done" {
		t.Fatalf("unexpected attached foreground result %#v", controller.task.LastCommandResult)
	}
}

func TestLocalControllerAttachedForegroundLateCompletionIgnoredAfterAbandon(t *testing.T) {
	monitor := newManualMonitor()
	now := time.Now().Add(-5 * time.Second)
	monitor.snapshot = shell.MonitorSnapshot{
		CommandID:         "cmd-monitor",
		Command:           "sleep 30",
		State:             shell.MonitorStateRunning,
		StartedAt:         now,
		ForegroundCommand: "sleep",
	}
	runner := &monitoringRunner{attachMonitor: monitor}
	controller := New(nil, runner, &stubContextReader{output: "^C"}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	_, attached, err := controller.attachForegroundExecution(context.Background())
	if err != nil {
		t.Fatalf("attachForegroundExecution() error = %v", err)
	}
	if !attached {
		t.Fatal("expected foreground command to attach")
	}

	execution := controller.AbandonActiveExecution("user interrupted from handoff")
	if execution == nil {
		t.Fatal("expected abandoned execution snapshot")
	}

	monitor.finish(shell.TrackedExecution{
		CommandID: "cmd-monitor",
		Command:   "sleep 30",
		State:     shell.MonitorStateCompleted,
		ExitCode:  0,
		Captured:  "done",
	}, nil)

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if controller.task.LastCommandResult != nil {
			t.Fatalf("expected late attached completion to be ignored, got %#v", controller.task.LastCommandResult)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if controller.ActiveExecution() != nil {
		t.Fatalf("expected no active execution after abandon, got %#v", controller.ActiveExecution())
	}
}

func TestLocalControllerRejectsSecondShellCommandWhileExecutionIsActive(t *testing.T) {
	runner := &blockingRunner{
		started: make(chan struct{}, 2),
		release: make(chan struct{}),
		result:  shell.TrackedExecution{CommandID: "cmd-1", Command: "sleep 30", ExitCode: 0},
	}
	controller := New(nil, runner, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	firstDone := make(chan []TranscriptEvent, 1)
	go func() {
		events, _ := controller.SubmitShellCommand(context.Background(), "sleep 30")
		firstDone <- events
	}()
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first command to start")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		controller.mu.Lock()
		registry := append([]CommandExecution(nil), controller.task.ExecutionRegistry...)
		primary := controller.task.PrimaryExecutionID
		controller.mu.Unlock()
		if len(registry) == 1 && primary != "" {
			if active := controller.ActiveExecution(); active == nil || active.Command != "sleep 30" {
				t.Fatalf("expected first command to remain the primary active execution, got %#v", active)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected one active execution in registry, got %#v", registry)
		}
		time.Sleep(10 * time.Millisecond)
	}

	events, err := controller.SubmitShellCommand(context.Background(), "sleep 60")
	if err != nil {
		t.Fatalf("SubmitShellCommand(second) error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventError {
		t.Fatalf("expected single error event for overlapping command, got %#v", events)
	}
	payload, ok := events[0].Payload.(TextPayload)
	if !ok || !strings.Contains(payload.Text, `"sleep 30"`) {
		t.Fatalf("expected overlapping-command error to mention the active command, got %#v", events[0].Payload)
	}

	select {
	case <-runner.started:
		t.Fatal("expected second command to be rejected before runner execution")
	case <-time.After(100 * time.Millisecond):
	}

	close(runner.release)

	select {
	case events := <-firstDone:
		if len(events) != 2 || events[1].Kind != EventCommandResult {
			t.Fatalf("unexpected first command events %#v", events)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first command to finish")
	}

	controller.mu.Lock()
	defer controller.mu.Unlock()
	if len(controller.task.ExecutionRegistry) != 0 || controller.task.CurrentExecution != nil || controller.task.PrimaryExecutionID != "" {
		t.Fatalf("expected execution registry to be empty after serial completion, got primary=%q current=%#v registry=%#v", controller.task.PrimaryExecutionID, controller.task.CurrentExecution, controller.task.ExecutionRegistry)
	}
}

func TestLocalControllerMonitorMapsAwaitingInputState(t *testing.T) {
	monitor := newManualMonitor()
	runner := &monitoringRunner{
		monitor: monitor,
		started: make(chan struct{}, 1),
	}
	controller := New(nil, runner, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = controller.SubmitShellCommand(context.Background(), "python3 -c \"input('name: ')\"")
	}()

	runner.waitForStart(t)
	monitor.publish(shell.MonitorSnapshot{
		CommandID:        "cmd-monitor",
		Command:          "python3 -c \"input('name: ')\"",
		State:            shell.MonitorStateAwaitingInput,
		StartedAt:        time.Now(),
		LatestOutputTail: "name:",
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		active := controller.ActiveExecution()
		if active != nil && active.State == CommandExecutionAwaitingInput && active.LatestOutputTail == "name:" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected active execution to enter awaiting_input, got %#v", active)
		}
		time.Sleep(10 * time.Millisecond)
	}

	monitor.finish(shell.TrackedExecution{
		CommandID: "cmd-monitor",
		Command:   "python3 -c \"input('name: ')\"",
		ExitCode:  shell.InterruptedExitCode,
		Captured:  "name:\n",
	}, context.Canceled)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for monitored command to finish")
	}
}

func TestLocalControllerMonitorMapsInteractiveFullscreenState(t *testing.T) {
	monitor := newManualMonitor()
	runner := &monitoringRunner{
		monitor: monitor,
		started: make(chan struct{}, 1),
	}
	controller := New(nil, runner, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = controller.SubmitShellCommand(context.Background(), "wrapped-btop")
	}()

	runner.waitForStart(t)
	monitor.publish(shell.MonitorSnapshot{
		CommandID:        "cmd-monitor",
		Command:          "wrapped-btop",
		State:            shell.MonitorStateInteractiveFullscreen,
		StartedAt:        time.Now(),
		LatestOutputTail: "",
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		active := controller.ActiveExecution()
		if active != nil && active.State == CommandExecutionInteractiveFullscreen {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected active execution to enter interactive_fullscreen, got %#v", active)
		}
		time.Sleep(10 * time.Millisecond)
	}

	monitor.finish(shell.TrackedExecution{
		CommandID: "cmd-monitor",
		Command:   "wrapped-btop",
		ExitCode:  shell.InterruptedExitCode,
		Captured:  "",
	}, context.Canceled)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for monitored command to finish")
	}
}

func TestLocalControllerCheckActiveExecutionSkipsUserShellCommands(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "unexpected",
		},
	}
	controller := New(agent, nil, &stubContextReader{output: "ignored"}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	setPrimaryExecutionForTest(controller, CommandExecution{
		ID:        "cmd-user",
		Command:   "sleep 60",
		Origin:    CommandOriginUserShell,
		State:     CommandExecutionRunning,
		StartedAt: time.Now().Add(-15 * time.Second),
	})

	events, err := controller.CheckActiveExecution(context.Background())
	if err != nil {
		t.Fatalf("CheckActiveExecution() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no events, got %#v", events)
	}
	if agent.lastInput.Prompt != "" {
		t.Fatalf("expected agent not to be called, got input %#v", agent.lastInput)
	}
}

func TestLocalControllerSubmitShellCommandPreservesMonitoredTailWhenCompletionCaptureEmpty(t *testing.T) {
	monitor := newManualMonitor()
	runner := &monitoringRunner{monitor: monitor, started: make(chan struct{}, 1)}
	controller := New(nil, runner, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	done := make(chan struct{})
	var (
		events []TranscriptEvent
		err    error
	)
	go func() {
		events, err = controller.SubmitShellCommand(context.Background(), "find . -name '*.md'")
		close(done)
	}()

	runner.waitForStart(t)
	monitor.publish(shell.MonitorSnapshot{
		State:            shell.MonitorStateRunning,
		LatestOutputTail: "docs/a.md\ndocs/b.md\n... (2004 more lines)",
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		active := controller.ActiveExecution()
		if active != nil && strings.Contains(active.LatestOutputTail, "docs/a.md") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected active execution tail to update before completion, got %#v", active)
		}
		time.Sleep(10 * time.Millisecond)
	}
	monitor.finish(shell.TrackedExecution{
		CommandID: "cmd-1",
		Command:   "find . -name '*.md'",
		ExitCode:  0,
		Captured:  "",
	}, nil)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for monitored command to finish")
	}
	if err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}
	if len(events) != 2 || events[1].Kind != EventCommandResult {
		t.Fatalf("expected start/result events, got %#v", events)
	}
	payload, ok := events[1].Payload.(CommandResultSummary)
	if !ok {
		t.Fatalf("expected command result payload, got %#v", events[1].Payload)
	}
	if !strings.Contains(payload.Summary, "docs/a.md") {
		t.Fatalf("expected result summary to preserve monitored tail, got %q", payload.Summary)
	}
	if controller.task.LastCommandResult == nil || !strings.Contains(controller.task.LastCommandResult.Summary, "docs/b.md") {
		t.Fatalf("expected last command result to preserve monitored tail, got %#v", controller.task.LastCommandResult)
	}
}

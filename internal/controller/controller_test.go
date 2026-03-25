package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiterm/internal/shell"
)

func TestLocalControllerSubmitAgentPrompt(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "hello",
			Proposal: &Proposal{
				Kind:    ProposalCommand,
				Command: "ls -lah",
			},
		},
	}
	controller := New(agent, nil, &stubContextReader{
		output: "recent shell output",
	}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitAgentPrompt(context.Background(), "list files")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	if events[0].Kind != EventUserMessage || events[1].Kind != EventAgentMessage || events[2].Kind != EventProposal {
		t.Fatalf("unexpected event sequence: %#v", events)
	}

	if agent.lastInput.Session.RecentShellOutput != "recent shell output" {
		t.Fatalf("expected recent shell output in agent input, got %q", agent.lastInput.Session.RecentShellOutput)
	}
}

func TestLocalControllerSubmitAgentPromptIncludesRecentManualShellContext(t *testing.T) {
	historyFile := t.TempDir() + "/shell_history"
	if err := os.WriteFile(historyFile, []byte(strings.Join([]string{
		": 1710000000:0;ls",
		"mv foo.md foo_new.md",
		"touch chicken.mmd",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	agent := &stubAgent{
		response: AgentResponse{
			Message: "ready",
		},
	}
	controller := New(agent, nil, &stubContextReader{
		output: "recent shell output",
		context: shell.PromptContext{
			User:         "jsmith",
			Host:         "linuxdesktop",
			Directory:    "/home/jsmith/source/repos/aiterm",
			PromptSymbol: "%",
			RawLine:      "jsmith@linuxdesktop ~/source/repos/aiterm %",
		},
	}, SessionContext{
		UserShellHistoryFile: historyFile,
	})

	if _, err := controller.SubmitAgentPrompt(context.Background(), "what changed?"); err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	if got := strings.Join(agent.lastInput.Session.RecentManualCommands, "\n"); !strings.Contains(got, "mv foo.md foo_new.md") || !strings.Contains(got, "touch chicken.mmd") {
		t.Fatalf("expected recent manual commands in agent input, got %#v", agent.lastInput.Session.RecentManualCommands)
	}
	if got := strings.Join(agent.lastInput.Session.RecentManualActions, "\n"); !strings.Contains(got, "renamed foo.md -> foo_new.md") || !strings.Contains(got, "touched chicken.mmd") {
		t.Fatalf("expected recent manual actions in agent input, got %#v", agent.lastInput.Session.RecentManualActions)
	}
}

func TestLocalControllerSubmitAgentPromptCreatesActivePlan(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Plan: &Plan{
				Summary: "Inspect and repair the workspace.",
				Steps: []string{
					"Review the current files.",
					"Apply the next patch.",
					"Run tests.",
				},
			},
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitAgentPrompt(context.Background(), "make a plan")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	planEvent, ok := events[1].Payload.(PlanPayload)
	if !ok {
		t.Fatalf("expected plan payload, got %#v", events[1].Payload)
	}

	if len(planEvent.Steps) != 3 || planEvent.Steps[0].Status != PlanStepInProgress || planEvent.Steps[1].Status != PlanStepPending {
		t.Fatalf("unexpected active plan state: %#v", planEvent.Steps)
	}
}

func TestLocalControllerSubmitAgentPromptIgnoresAnswerProposal(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "The selection is complete.",
			Proposal: &Proposal{
				Kind:        ProposalAnswer,
				Description: "No further action is needed.",
			},
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitAgentPrompt(context.Background(), "continue")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected user and agent events only, got %#v", events)
	}
	if events[0].Kind != EventUserMessage || events[1].Kind != EventAgentMessage {
		t.Fatalf("unexpected event sequence: %#v", events)
	}
}

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
			Captured:   "^C\njsmith@host % ",
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
		snapshot: "^C\njsmith@linuxdesktop ~/source/repos/aiterm %",
		context: shell.PromptContext{
			User:         "jsmith",
			Host:         "linuxdesktop",
			Directory:    "/home/jsmith/source/repos/aiterm",
			PromptSymbol: "%",
			RawLine:      "jsmith@linuxdesktop ~/source/repos/aiterm %",
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

func TestLocalControllerResumeAfterTakeControlInfersCanceledWhenPromptReturnedWithoutExitCode(t *testing.T) {
	reader := &stubContextReader{
		snapshot: "^C\njsmith@linuxdesktop ~/source/repos/aiterm %",
		context: shell.PromptContext{
			User:         "jsmith",
			Host:         "linuxdesktop",
			Directory:    "/home/jsmith/source/repos/aiterm",
			PromptSymbol: "%",
			RawLine:      "jsmith@linuxdesktop ~/source/repos/aiterm %",
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

func TestLocalControllerResumeAfterTakeControlDoesNotReconcileWithoutCurrentPrompt(t *testing.T) {
	reader := &stubContextReader{
		snapshot: "jsmith@linuxdesktop ~/source/repos/aiterm %\n. '/run/user/1000/shuttle/shell-integration/zsh-pane0.sh'\nsleep 20",
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

func TestLocalControllerResumeAfterTakeControlPrefersAttachedExecutionTail(t *testing.T) {
	exitCode := 0
	reader := &stubContextReader{
		snapshot: "jsmith@linuxdesktop ~/source/repos/go_learn %\n. '/run/user/1000/shuttle/commands/noisy.sh'",
		context: shell.PromptContext{
			User:         "jsmith",
			Host:         "linuxdesktop",
			Directory:    "~/source/repos/go_learn",
			PromptSymbol: "%",
			RawLine:      "jsmith@linuxdesktop ~/source/repos/go_learn %",
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
		snapshot:       "^C\njsmith@linuxdesktop ~/source/repos/aiterm %",
		resolvedPaneID: "%5",
		context: shell.PromptContext{
			User:         "jsmith",
			Host:         "linuxdesktop",
			Directory:    "/home/jsmith/source/repos/aiterm",
			PromptSymbol: "%",
			RawLine:      "jsmith@linuxdesktop ~/source/repos/aiterm %",
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

	if agent.lastInput.Prompt != lostTrackingCheckInPrompt {
		t.Fatalf("expected lost recovery prompt, got %q", agent.lastInput.Prompt)
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
			User:         "jsmith",
			Host:         "linuxdesktop",
			Directory:    "/tmp/project",
			PromptSymbol: "%",
			RawLine:      "jsmith@linuxdesktop /tmp/project %",
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
	startPayload, ok := events[1].Payload.(CommandStartPayload)
	if !ok {
		t.Fatalf("expected command start payload, got %#v", events[1].Payload)
	}
	if startPayload.Execution.TrackedShell.PaneID != "%9" {
		t.Fatalf("expected execution target pane %%9, got %#v", startPayload.Execution.TrackedShell)
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
					User:         "jsmith",
					Host:         "linuxdesktop",
					Directory:    "/tmp/owned",
					PromptSymbol: "%",
					RawLine:      "jsmith@linuxdesktop /tmp/owned %",
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
		WorkingDirectory: "/home/jsmith/source/repos/aiterm",
		CurrentShell: &shell.PromptContext{
			User:         "jsmith",
			Host:         "linuxdesktop",
			Directory:    "/home/jsmith/source/repos/aiterm",
			PromptSymbol: "%",
			RawLine:      "jsmith@linuxdesktop ~/source/repos/aiterm %",
		},
	})

	if _, err := controller.SubmitProposedShellCommand(context.Background(), "pwd"); err != nil {
		t.Fatalf("SubmitProposedShellCommand() error = %v", err)
	}

	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.session.WorkingDirectory != "/home/jsmith/source/repos/aiterm" {
		t.Fatalf("expected user-shell cwd to remain unchanged, got %q", controller.session.WorkingDirectory)
	}
	if controller.session.CurrentShell == nil || controller.session.CurrentShell.Directory != "/home/jsmith/source/repos/aiterm" {
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
	goLearnDirectory := filepath.Join(homeDirectory, "source/repos/go_learn")

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
				User:         "jsmith",
				Host:         "linuxdesktop",
				Directory:    "~/source/repos",
				PromptSymbol: "%",
				RawLine:      "jsmith@linuxdesktop ~/source/repos %",
			},
			{
				User:         "jsmith",
				Host:         "linuxdesktop",
				Directory:    "~/source/repos/go_learn",
				PromptSymbol: "%",
				RawLine:      "jsmith@linuxdesktop ~/source/repos/go_learn %",
			},
			{
				User:         "jsmith",
				Host:         "linuxdesktop",
				Directory:    "~/source/repos/go_learn",
				PromptSymbol: "%",
				RawLine:      "jsmith@linuxdesktop ~/source/repos/go_learn %",
			},
		},
	}
	controller := New(nil, runner, reader, SessionContext{
		SessionName:      "shuttle-test",
		TrackedShell:     TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
		WorkingDirectory: filepath.Join(homeDirectory, "source/repos"),
		CurrentShell: &shell.PromptContext{
			User:         "jsmith",
			Host:         "linuxdesktop",
			Directory:    "~/source/repos",
			PromptSymbol: "%",
			RawLine:      "jsmith@linuxdesktop ~/source/repos %",
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
	if controller.session.CurrentShell == nil || controller.session.CurrentShell.Directory != "~/source/repos/go_learn" {
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
	if agent.lastInput.Prompt != activeExecutionCheckInPrompt {
		t.Fatalf("expected running check-in prompt, got %q", agent.lastInput.Prompt)
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
	if agent.lastInput.Prompt != awaitingInputCheckInPrompt {
		t.Fatalf("expected awaiting-input prompt, got %q", agent.lastInput.Prompt)
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
	if agent.lastInput.Prompt != fullscreenCheckInPrompt {
		t.Fatalf("expected fullscreen prompt, got %q", agent.lastInput.Prompt)
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
	if agent.lastInput.Prompt != lostTrackingCheckInPrompt {
		t.Fatalf("expected lost prompt, got %q", agent.lastInput.Prompt)
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

func TestLocalControllerSubmitProposalRefinementBuildsAgentPrompt(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Revised proposal ready.",
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitProposalRefinement(context.Background(), ProposalPayload{
		Kind:        ProposalCommand,
		Command:     "sleep 5",
		Description: "Run a short sleep.",
	}, "Make it one second.")
	if err != nil {
		t.Fatalf("SubmitProposalRefinement() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected user + agent events, got %d", len(events))
	}
	if events[0].Kind != EventUserMessage {
		t.Fatalf("expected visible user note, got %#v", events[0])
	}
	if !strings.Contains(agent.lastInput.Prompt, "Original command: sleep 5") {
		t.Fatalf("expected proposal context in agent prompt, got %q", agent.lastInput.Prompt)
	}
	if events[0].Payload.(TextPayload).Text != "Make it one second." {
		t.Fatalf("expected visible refinement note, got %#v", events[0].Payload)
	}
}

func TestLocalControllerApproveRunsCommand(t *testing.T) {
	runner := &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-approve",
			Command:   "rm -rf tmp",
			ExitCode:  0,
			Captured:  "",
		},
	}
	controller := New(&stubAgent{
		response: AgentResponse{
			Approval: &ApprovalRequest{
				ID:      "approval-1",
				Kind:    ApprovalCommand,
				Command: "rm -rf tmp",
			},
		},
	}, runner, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitAgentPrompt(context.Background(), "remove tmp")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected agent events")
	}

	approveEvents, err := controller.DecideApproval(context.Background(), "approval-1", DecisionApprove, "")
	if err != nil {
		t.Fatalf("DecideApproval() error = %v", err)
	}

	if len(approveEvents) != 2 {
		t.Fatalf("expected 2 approval events, got %d", len(approveEvents))
	}

	if runner.commands[0] != "rm -rf tmp" {
		t.Fatalf("expected approved command to run, got %q", runner.commands[0])
	}
}

func TestLocalControllerProposalCommandFillsApprovalCommand(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Approve this test command.",
			Proposal: &Proposal{
				Kind:        ProposalCommand,
				Command:     "bash -lc 'for i in {1..20}; do echo \"$i\"; sleep 1; done'",
				Description: "Streaming loop.",
			},
			Approval: &ApprovalRequest{
				ID:      "approval-1",
				Kind:    ApprovalCommand,
				Title:   "Approve test command",
				Summary: "Run the streaming loop.",
				Risk:    RiskLow,
			},
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitAgentPrompt(context.Background(), "propose a streaming loop and ask for approval")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	var approval ApprovalRequest
	foundApproval := false
	for _, event := range events {
		if event.Kind != EventApproval {
			continue
		}
		payload, ok := event.Payload.(ApprovalRequest)
		if !ok {
			t.Fatalf("expected approval payload, got %#v", event.Payload)
		}
		approval = payload
		foundApproval = true
	}
	if !foundApproval {
		t.Fatal("expected approval event")
	}
	if approval.Command != "bash -lc 'for i in {1..20}; do echo \"$i\"; sleep 1; done'" {
		t.Fatalf("expected approval to inherit proposal command, got %q", approval.Command)
	}
}

func TestLocalControllerApproveWithoutCommandReturnsError(t *testing.T) {
	controller := New(nil, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	controller.task.PendingApproval = &ApprovalRequest{
		ID:      "approval-1",
		Kind:    ApprovalCommand,
		Title:   "Broken approval",
		Summary: "Missing command",
		Risk:    RiskLow,
	}

	events, err := controller.DecideApproval(context.Background(), "approval-1", DecisionApprove, "")
	if err != nil {
		t.Fatalf("DecideApproval() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventError {
		t.Fatalf("expected single error event, got %#v", events)
	}
}

type stubAgent struct {
	response  AgentResponse
	err       error
	lastInput AgentInput
}

func (s *stubAgent) Respond(_ context.Context, input AgentInput) (AgentResponse, error) {
	s.lastInput = input
	return s.response, s.err
}

func setPrimaryExecutionForTest(controller *LocalController, execution CommandExecution) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.registerExecutionLocked(execution)
}

type stubRunner struct {
	result         shell.TrackedExecution
	err            error
	commands       []string
	paneIDs        []string
	resolvedPaneID string
}

func (s *stubRunner) RunTrackedCommand(_ context.Context, paneID string, command string, _ time.Duration) (shell.TrackedExecution, error) {
	s.commands = append(s.commands, command)
	s.paneIDs = append(s.paneIDs, paneID)
	if s.result.Command == "" {
		s.result.Command = command
	}
	if s.err != nil {
		return s.result, s.err
	}
	return s.result, nil
}

func (s *stubRunner) ResolveTrackedPane(_ context.Context, paneID string) (string, error) {
	if strings.TrimSpace(s.resolvedPaneID) != "" {
		return s.resolvedPaneID, nil
	}
	return paneID, nil
}

type ownedExecutionRunner struct {
	stubRunner
	ownedPane    shell.OwnedExecutionPane
	startDir     string
	cleanupCalls int
}

func (o *ownedExecutionRunner) CreateOwnedExecutionPane(_ context.Context, startDir string) (shell.OwnedExecutionPane, func(context.Context) error, error) {
	o.startDir = startDir
	cleanup := func(context.Context) error {
		o.cleanupCalls++
		return nil
	}
	return o.ownedPane, cleanup, nil
}

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
	result  shell.TrackedExecution
}

func (b *blockingRunner) RunTrackedCommand(_ context.Context, _ string, _ string, _ time.Duration) (shell.TrackedExecution, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-b.release
	return b.result, nil
}

type monitoringRunner struct {
	monitor       *manualMonitor
	attachMonitor *manualMonitor
	attachFunc    func(context.Context, string) (shell.CommandMonitor, error)
	attachCalls   int
	commands      []string
	started       chan struct{}
}

func (m *monitoringRunner) RunTrackedCommand(_ context.Context, _ string, _ string, _ time.Duration) (shell.TrackedExecution, error) {
	return shell.TrackedExecution{}, errors.New("unexpected fallback RunTrackedCommand call")
}

func (m *monitoringRunner) StartTrackedCommand(_ context.Context, _ string, command string, _ time.Duration) (shell.CommandMonitor, error) {
	m.commands = append(m.commands, command)
	if m.started != nil {
		select {
		case m.started <- struct{}{}:
		default:
		}
	}
	return m.monitor, nil
}

func (m *monitoringRunner) AttachForegroundCommand(ctx context.Context, paneID string) (shell.CommandMonitor, error) {
	m.attachCalls++
	if m.attachFunc != nil {
		return m.attachFunc(ctx, paneID)
	}
	if m.attachMonitor == nil {
		return nil, nil
	}
	return m.attachMonitor, nil
}

func (m *monitoringRunner) waitForStart(t *testing.T) {
	t.Helper()
	if m.started == nil {
		m.started = make(chan struct{}, 1)
	}
	select {
	case <-m.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for monitor runner to start")
	}
}

type manualMonitor struct {
	snapshot shell.MonitorSnapshot
	updates  chan shell.MonitorSnapshot
	done     chan struct{}
	result   shell.TrackedExecution
	err      error
}

func newManualMonitor() *manualMonitor {
	return &manualMonitor{
		updates: make(chan shell.MonitorSnapshot, 16),
		done:    make(chan struct{}),
	}
}

func (m *manualMonitor) Snapshot() shell.MonitorSnapshot {
	return m.snapshot
}

func (m *manualMonitor) Updates() <-chan shell.MonitorSnapshot {
	return m.updates
}

func (m *manualMonitor) Wait() (shell.TrackedExecution, error) {
	<-m.done
	return m.result, m.err
}

func (m *manualMonitor) publish(snapshot shell.MonitorSnapshot) {
	m.snapshot = snapshot
	m.updates <- snapshot
}

func (m *manualMonitor) finish(result shell.TrackedExecution, err error) {
	m.result = result
	m.err = err
	close(m.done)
	close(m.updates)
}

func TestLocalControllerRunnerError(t *testing.T) {
	controller := New(nil, &stubRunner{err: errors.New("boom")}, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	events, err := controller.SubmitShellCommand(context.Background(), "ls")
	if err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}
	if events[len(events)-1].Kind != EventError {
		t.Fatalf("expected trailing error event, got %s", events[len(events)-1].Kind)
	}
}

func TestLocalControllerRefineClearsApproval(t *testing.T) {
	controller := New(&stubAgent{
		response: AgentResponse{
			Approval: &ApprovalRequest{
				ID:      "approval-1",
				Kind:    ApprovalCommand,
				Command: "rm -rf tmp",
			},
		},
	}, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	if _, err := controller.SubmitAgentPrompt(context.Background(), "remove tmp"); err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}

	events, err := controller.DecideApproval(context.Background(), "approval-1", DecisionRefine, "Refine this proposed command")
	if err != nil {
		t.Fatalf("DecideApproval() error = %v", err)
	}

	if len(events) != 1 || events[0].Kind != EventSystemNotice {
		t.Fatalf("expected single system notice, got %#v", events)
	}
}

func TestLocalControllerSubmitRefinementIncludesApprovalContext(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Refinement noted.",
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	approval := ApprovalRequest{
		ID:      "approval-1",
		Kind:    ApprovalCommand,
		Title:   "Destructive command approval",
		Command: "rm -rf tmp",
		Risk:    RiskHigh,
	}

	events, err := controller.SubmitRefinement(context.Background(), approval, "Use a safer option.")
	if err != nil {
		t.Fatalf("SubmitRefinement() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 refinement events, got %d", len(events))
	}

	if agent.lastInput.Task.PendingApproval == nil || agent.lastInput.Task.PendingApproval.Command != "rm -rf tmp" {
		t.Fatalf("expected pending approval in agent input, got %#v", agent.lastInput.Task.PendingApproval)
	}

	if agent.lastInput.Prompt != "Use a safer option." {
		t.Fatalf("expected note prompt, got %q", agent.lastInput.Prompt)
	}
}

func TestLocalControllerContinueAfterCommandUsesLastResultWithoutUserEvent(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "I reviewed the command result.",
		},
	}
	controller := New(agent, &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-1",
			Command:   "ls",
			ExitCode:  0,
			Captured:  "file.txt",
		},
	}, &stubContextReader{
		output: "file.txt",
	}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	if _, err := controller.SubmitShellCommand(context.Background(), "ls"); err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}

	events, err := controller.ContinueAfterCommand(context.Background())
	if err != nil {
		t.Fatalf("ContinueAfterCommand() error = %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected only agent events, got %#v", events)
	}

	if events[0].Kind != EventAgentMessage {
		t.Fatalf("expected agent message, got %#v", events)
	}

	if agent.lastInput.Prompt != buildAutoContinuePrompt(controller.task) {
		t.Fatalf("expected auto-continue prompt, got %q", agent.lastInput.Prompt)
	}

	if agent.lastInput.Task.LastCommandResult == nil || agent.lastInput.Task.LastCommandResult.Command != "ls" {
		t.Fatalf("expected last command result in agent input, got %#v", agent.lastInput.Task.LastCommandResult)
	}
}

func TestLocalControllerContinueAfterCommandPrefersSerialFollowUpPrompt(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Step 1 is complete.",
		},
	}
	controller := New(agent, &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-1",
			Command:   "find . -maxdepth 1 -name '*.md'",
			ExitCode:  0,
			Captured:  "a.md\nb.md",
		},
	}, &stubContextReader{
		output: "a.md\nb.md",
	}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	controller.task.PriorTranscript = append(controller.task.PriorTranscript, TranscriptEvent{
		Kind:    EventUserMessage,
		Payload: TextPayload{Text: "list all the markdown files in this directory. Then when you see the list, give me a tail of the last 20 lines of the shortest one. I want to do this in serial commands, don't lump them together."},
	})

	if _, err := controller.SubmitShellCommand(context.Background(), "find . -maxdepth 1 -name '*.md'"); err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}

	if _, err := controller.ContinueAfterCommand(context.Background()); err != nil {
		t.Fatalf("ContinueAfterCommand() error = %v", err)
	}

	if !strings.Contains(agent.lastInput.Prompt, "propose exactly one next command now") {
		t.Fatalf("expected serial continuation prompt, got %q", agent.lastInput.Prompt)
	}
}

func TestLocalControllerContinueAfterCommandAdvancesActivePlan(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Continuing the plan.",
			Plan: &Plan{
				Summary: "Stale replacement plan.",
				Steps: []string{
					"Start over from the beginning.",
					"Redo the same shell work.",
				},
			},
		},
	}
	controller := New(agent, &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-1",
			Command:   "ls",
			ExitCode:  0,
			Captured:  "file.txt",
		},
	}, &stubContextReader{
		output: "file.txt",
	}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	if _, err := controller.SubmitAgentPrompt(context.Background(), "make a plan"); err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}
	controller.task.ActivePlan = &ActivePlan{
		Summary: "Inspect and repair the workspace.",
		Steps: []PlanStep{
			{Text: "Review the current files.", Status: PlanStepInProgress},
			{Text: "Apply the next patch.", Status: PlanStepPending},
		},
	}

	if _, err := controller.SubmitShellCommand(context.Background(), "ls"); err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}

	events, err := controller.ContinueAfterCommand(context.Background())
	if err != nil {
		t.Fatalf("ContinueAfterCommand() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected plan progress event plus continuation without replacement plan, got %#v", events)
	}

	planEvent, ok := events[0].Payload.(PlanPayload)
	if !ok {
		t.Fatalf("expected leading plan payload, got %#v", events[0].Payload)
	}
	if planEvent.Steps[0].Status != PlanStepDone || planEvent.Steps[1].Status != PlanStepInProgress {
		t.Fatalf("expected plan advancement, got %#v", planEvent.Steps)
	}
	if controller.task.ActivePlan == nil || controller.task.ActivePlan.Summary != "Inspect and repair the workspace." {
		t.Fatalf("expected existing active plan to be preserved, got %#v", controller.task.ActivePlan)
	}
}

func TestLocalControllerContinueActivePlanUsesActivePlanContext(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Continuing the active plan.",
			Plan: &Plan{
				Summary: "A replacement plan that should be ignored.",
				Steps: []string{
					"Start over.",
				},
			},
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	controller.task.ActivePlan = &ActivePlan{
		Summary: "Inspect and repair the workspace.",
		Steps: []PlanStep{
			{Text: "Review the current files.", Status: PlanStepInProgress},
			{Text: "Apply the next patch.", Status: PlanStepPending},
		},
	}

	events, err := controller.ContinueActivePlan(context.Background())
	if err != nil {
		t.Fatalf("ContinueActivePlan() error = %v", err)
	}

	if len(events) != 1 || events[0].Kind != EventAgentMessage {
		t.Fatalf("expected agent continuation event, got %#v", events)
	}
	if agent.lastInput.Task.ActivePlan == nil || agent.lastInput.Task.ActivePlan.Steps[0].Status != PlanStepInProgress {
		t.Fatalf("expected active plan in agent input, got %#v", agent.lastInput.Task.ActivePlan)
	}
	if agent.lastInput.Prompt != continuePlanPrompt {
		t.Fatalf("expected continue-plan prompt, got %q", agent.lastInput.Prompt)
	}
	if controller.task.ActivePlan == nil || controller.task.ActivePlan.Summary != "Inspect and repair the workspace." {
		t.Fatalf("expected existing active plan to survive continuation, got %#v", controller.task.ActivePlan)
	}
}

func TestLocalControllerContinueAfterCommandClearsPlanWhenAgentDeclaresCompletion(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "The active plan is complete: Markdown files were listed and each last line was printed.",
		},
	}
	controller := New(agent, &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-1",
			Command:   "find . -type f -name '*.md'",
			ExitCode:  0,
			Captured:  "./a.md: tail",
		},
	}, &stubContextReader{
		output: "./a.md: tail",
	}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	controller.task.ActivePlan = &ActivePlan{
		Summary: "List every Markdown file and display the last line of each.",
		Steps: []PlanStep{
			{Text: "Find all Markdown files.", Status: PlanStepDone},
			{Text: "Read the last line of each file.", Status: PlanStepInProgress},
			{Text: "Print results clearly.", Status: PlanStepPending},
		},
	}

	if _, err := controller.SubmitShellCommand(context.Background(), "find . -type f -name '*.md'"); err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}

	events, err := controller.ContinueAfterCommand(context.Background())
	if err != nil {
		t.Fatalf("ContinueAfterCommand() error = %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected progress event, completed-plan event, and agent message, got %#v", events)
	}
	var completedPlan PlanPayload
	foundCompletedPlan := false
	for _, event := range events {
		payload, ok := event.Payload.(PlanPayload)
		if !ok {
			continue
		}
		completedPlan = payload
		foundCompletedPlan = true
	}
	if !foundCompletedPlan {
		t.Fatalf("expected completed plan payload in %#v", events)
	}
	for _, step := range completedPlan.Steps {
		if step.Status != PlanStepDone {
			t.Fatalf("expected completed plan event, got %#v", completedPlan)
		}
	}
	if controller.task.ActivePlan != nil {
		t.Fatalf("expected active plan to clear after completion, got %#v", controller.task.ActivePlan)
	}
}

func TestBuildActivePlanStripsModelStatusPrefixesFromStepText(t *testing.T) {
	plan := buildActivePlan(Plan{
		Summary: "Serial shell workflow",
		Steps: []string{
			"[done] List all Markdown files.",
			"[in_progress] Review the file list output.",
			"pending: Select one Markdown file at random.",
		},
	})

	if plan.Steps[0].Text != "List all Markdown files." {
		t.Fatalf("expected normalized first step text, got %#v", plan.Steps[0])
	}
	if plan.Steps[1].Text != "Review the file list output." {
		t.Fatalf("expected normalized second step text, got %#v", plan.Steps[1])
	}
	if plan.Steps[2].Text != "Select one Markdown file at random." {
		t.Fatalf("expected normalized third step text, got %#v", plan.Steps[2])
	}
}

type stubContextReader struct {
	output         string
	snapshot       string
	context        shell.PromptContext
	contexts       []shell.PromptContext
	err            error
	resolvedPaneID string
	paneIDs        []string
	contextCalls   int
}

func (s *stubContextReader) CaptureRecentOutput(_ context.Context, paneID string, _ int) (string, error) {
	s.paneIDs = append(s.paneIDs, paneID)
	if s.err != nil {
		return "", s.err
	}
	if s.snapshot != "" {
		return s.snapshot, nil
	}

	return s.output, nil
}

func (s *stubContextReader) CaptureShellContext(context.Context, string) (shell.PromptContext, error) {
	if s.err != nil {
		return shell.PromptContext{}, s.err
	}
	if len(s.contexts) > 0 {
		index := s.contextCalls
		if index >= len(s.contexts) {
			index = len(s.contexts) - 1
		}
		s.contextCalls++
		return s.contexts[index], nil
	}
	if s.context.PromptLine() != "" || s.context.LastExitCode != nil {
		return s.context, nil
	}

	return shell.PromptContext{
		User:      "jsmith",
		Host:      "linuxdesktop",
		Directory: "/home/jsmith/source/repos/aiterm",
	}, nil
}

func (s *stubContextReader) ResolveTrackedPane(_ context.Context, paneID string) (string, error) {
	if strings.TrimSpace(s.resolvedPaneID) != "" {
		return s.resolvedPaneID, nil
	}
	return paneID, nil
}

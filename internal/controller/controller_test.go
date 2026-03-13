package controller

import (
	"context"
	"errors"
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
	}, SessionContext{TopPaneID: "%0"})

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
	controller := New(agent, nil, nil, SessionContext{TopPaneID: "%0"})

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

func TestLocalControllerSubmitShellCommand(t *testing.T) {
	controller := New(nil, &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-1",
			Command:   "ls",
			ExitCode:  0,
			Captured:  "file.txt",
		},
	}, nil, SessionContext{TopPaneID: "%0"})

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
	}, nil, SessionContext{TopPaneID: "%0"})

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

func TestLocalControllerSubmitProposedShellCommandTracksAgentOrigin(t *testing.T) {
	controller := New(nil, &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-2",
			Command:   "ls -lah",
			ExitCode:  0,
			Captured:  "file.txt",
		},
	}, nil, SessionContext{TopPaneID: "%0"})

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
	controller := New(nil, runner, nil, SessionContext{TopPaneID: "%0"})

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
	controller := New(nil, nil, &stubContextReader{output: "tail line"}, SessionContext{TopPaneID: "%0"})
	controller.task.CurrentExecution = &CommandExecution{
		ID:        "cmd-1",
		Command:   "sleep 60",
		Origin:    CommandOriginUserShell,
		State:     CommandExecutionRunning,
		StartedAt: time.Now(),
	}

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
	controller := New(nil, runner, &stubContextReader{output: "^C"}, SessionContext{TopPaneID: "%0"})

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
	controller := New(agent, nil, nil, SessionContext{TopPaneID: "%0"})
	controller.task.CurrentExecution = &CommandExecution{
		ID:               "cmd-agent",
		Command:          "sleep 60",
		Origin:           CommandOriginAgentProposal,
		State:            CommandExecutionRunning,
		StartedAt:        time.Now().Add(-15 * time.Second),
		LatestOutputTail: "line 1\nline 2\nline 3",
	}

	events, err := controller.CheckActiveExecution(context.Background())
	if err != nil {
		t.Fatalf("CheckActiveExecution() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventAgentMessage {
		t.Fatalf("unexpected check-in events: %#v", events)
	}
	if agent.lastInput.Prompt != activeExecutionCheckInPrompt {
		t.Fatalf("expected check-in prompt, got %q", agent.lastInput.Prompt)
	}
	if agent.lastInput.Task.CurrentExecution == nil || agent.lastInput.Task.CurrentExecution.State != CommandExecutionBackgroundMonitor {
		t.Fatalf("expected background monitoring state, got %#v", agent.lastInput.Task.CurrentExecution)
	}
	if agent.lastInput.Session.RecentShellOutput != "line 1\nline 2\nline 3" {
		t.Fatalf("expected recent shell output in agent input, got %q", agent.lastInput.Session.RecentShellOutput)
	}
}

func TestLocalControllerCheckActiveExecutionPreservesAwaitingInputState(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "The command is waiting for input.",
		},
	}
	controller := New(agent, nil, nil, SessionContext{TopPaneID: "%0"})
	controller.task.CurrentExecution = &CommandExecution{
		ID:               "cmd-agent",
		Command:          "python3 -c \"input('name: ')\"",
		Origin:           CommandOriginAgentProposal,
		State:            CommandExecutionAwaitingInput,
		StartedAt:        time.Now().Add(-15 * time.Second),
		LatestOutputTail: "name:",
	}

	_, err := controller.CheckActiveExecution(context.Background())
	if err != nil {
		t.Fatalf("CheckActiveExecution() error = %v", err)
	}
	if agent.lastInput.Task.CurrentExecution == nil || agent.lastInput.Task.CurrentExecution.State != CommandExecutionAwaitingInput {
		t.Fatalf("expected awaiting_input state to be preserved, got %#v", agent.lastInput.Task.CurrentExecution)
	}
}

func TestLocalControllerMonitorUpdatesActiveExecutionTail(t *testing.T) {
	monitor := newManualMonitor()
	runner := &monitoringRunner{
		monitor: monitor,
		started: make(chan struct{}, 1),
	}
	controller := New(nil, runner, nil, SessionContext{TopPaneID: "%0"})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = controller.SubmitShellCommand(context.Background(), "sleep 60")
	}()

	runner.waitForStart(t)
	monitor.publish(shell.MonitorSnapshot{
		CommandID:        "cmd-monitor",
		Command:          "sleep 60",
		State:            shell.MonitorStateRunning,
		StartedAt:        time.Now(),
		LatestOutputTail: "still running",
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		active := controller.ActiveExecution()
		if active != nil && active.LatestOutputTail == "still running" && active.State == CommandExecutionRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected active execution to receive monitor tail, got %#v", active)
		}
		time.Sleep(10 * time.Millisecond)
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

func TestLocalControllerMonitorMapsAwaitingInputState(t *testing.T) {
	monitor := newManualMonitor()
	runner := &monitoringRunner{
		monitor: monitor,
		started: make(chan struct{}, 1),
	}
	controller := New(nil, runner, nil, SessionContext{TopPaneID: "%0"})

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

func TestLocalControllerCheckActiveExecutionSkipsUserShellCommands(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "unexpected",
		},
	}
	controller := New(agent, nil, &stubContextReader{output: "ignored"}, SessionContext{TopPaneID: "%0"})
	controller.task.CurrentExecution = &CommandExecution{
		ID:        "cmd-user",
		Command:   "sleep 60",
		Origin:    CommandOriginUserShell,
		State:     CommandExecutionRunning,
		StartedAt: time.Now().Add(-15 * time.Second),
	}

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
	controller := New(agent, nil, nil, SessionContext{TopPaneID: "%0"})

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
	}, runner, nil, SessionContext{TopPaneID: "%0"})

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
	controller := New(agent, nil, nil, SessionContext{TopPaneID: "%0"})

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
	controller := New(nil, nil, nil, SessionContext{TopPaneID: "%0"})
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

type stubRunner struct {
	result   shell.TrackedExecution
	err      error
	commands []string
}

func (s *stubRunner) RunTrackedCommand(_ context.Context, _ string, command string, _ time.Duration) (shell.TrackedExecution, error) {
	if s.err != nil {
		return shell.TrackedExecution{}, s.err
	}

	s.commands = append(s.commands, command)
	if s.result.Command == "" {
		s.result.Command = command
	}
	return s.result, nil
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
	monitor  *manualMonitor
	commands []string
	started  chan struct{}
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
	controller := New(nil, &stubRunner{err: errors.New("boom")}, nil, SessionContext{TopPaneID: "%0"})
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
	}, nil, nil, SessionContext{TopPaneID: "%0"})

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
	controller := New(agent, nil, nil, SessionContext{TopPaneID: "%0"})

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
	}, SessionContext{TopPaneID: "%0"})

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

	if agent.lastInput.Prompt != autoContinuePrompt {
		t.Fatalf("expected auto-continue prompt, got %q", agent.lastInput.Prompt)
	}

	if agent.lastInput.Task.LastCommandResult == nil || agent.lastInput.Task.LastCommandResult.Command != "ls" {
		t.Fatalf("expected last command result in agent input, got %#v", agent.lastInput.Task.LastCommandResult)
	}
}

func TestLocalControllerContinueAfterCommandAdvancesActivePlan(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Continuing the plan.",
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
	}, SessionContext{TopPaneID: "%0"})

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
		t.Fatalf("expected plan progress event plus continuation, got %#v", events)
	}

	planEvent, ok := events[0].Payload.(PlanPayload)
	if !ok {
		t.Fatalf("expected leading plan payload, got %#v", events[0].Payload)
	}
	if planEvent.Steps[0].Status != PlanStepDone || planEvent.Steps[1].Status != PlanStepInProgress {
		t.Fatalf("expected plan advancement, got %#v", planEvent.Steps)
	}
}

func TestLocalControllerContinueActivePlanUsesActivePlanContext(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Continuing the active plan.",
		},
	}
	controller := New(agent, nil, nil, SessionContext{TopPaneID: "%0"})
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
}

type stubContextReader struct {
	output string
	err    error
}

func (s *stubContextReader) CaptureRecentOutput(context.Context, string, int) (string, error) {
	if s.err != nil {
		return "", s.err
	}

	return s.output, nil
}

func (s *stubContextReader) CaptureShellContext(context.Context, string) (shell.PromptContext, error) {
	if s.err != nil {
		return shell.PromptContext{}, s.err
	}

	return shell.PromptContext{
		User:      "jsmith",
		Host:      "linuxdesktop",
		Directory: "/home/jsmith/source/repos/aiterm",
	}, nil
}

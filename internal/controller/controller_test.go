package controller

import (
	"context"
	"errors"
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

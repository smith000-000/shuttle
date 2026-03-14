package provider

import (
	"context"
	"strings"
	"testing"

	"aiterm/internal/controller"
)

func TestMockAgentReturnsCommandProposalForListFiles(t *testing.T) {
	agent := NewMockAgent()

	response, err := agent.Respond(context.Background(), controller.AgentInput{
		Prompt: "list files",
	})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if response.Proposal == nil {
		t.Fatal("expected proposal")
	}

	if response.Proposal.Kind != controller.ProposalCommand {
		t.Fatalf("expected command proposal, got %s", response.Proposal.Kind)
	}

	if response.Proposal.Command != "ls -lah" {
		t.Fatalf("expected ls -lah, got %q", response.Proposal.Command)
	}
}

func TestMockAgentReturnsApprovalForDestructivePrompt(t *testing.T) {
	agent := NewMockAgent()

	response, err := agent.Respond(context.Background(), controller.AgentInput{
		Prompt: "rm -rf tmp",
	})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if response.Approval == nil {
		t.Fatal("expected approval request")
	}

	if response.Approval.Risk != controller.RiskHigh {
		t.Fatalf("expected high risk, got %s", response.Approval.Risk)
	}
}

func TestMockAgentReturnsPlan(t *testing.T) {
	agent := NewMockAgent()

	response, err := agent.Respond(context.Background(), controller.AgentInput{
		Prompt: "show plan",
	})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if response.Plan == nil {
		t.Fatal("expected plan")
	}

	if len(response.Plan.Steps) != 3 {
		t.Fatalf("expected 3 plan steps, got %d", len(response.Plan.Steps))
	}
}

func TestMockAgentSummarizesRecentContext(t *testing.T) {
	agent := NewMockAgent()

	response, err := agent.Respond(context.Background(), controller.AgentInput{
		Prompt: "what happened",
		Session: controller.SessionContext{
			RecentShellOutput: "line one\nline two",
		},
		Task: controller.TaskContext{
			LastCommandResult: &controller.CommandResultSummary{
				Command:  "ls",
				ExitCode: 0,
			},
		},
	})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if response.Message == "" {
		t.Fatal("expected context summary message")
	}

	if !strings.Contains(response.Message, "Last command `ls` exited with code 0.") {
		t.Fatalf("expected command summary in response, got %q", response.Message)
	}

	if !strings.Contains(response.Message, "line two") {
		t.Fatalf("expected shell output summary in response, got %q", response.Message)
	}
}

func TestMockAgentHandlesRefinementWithPendingApproval(t *testing.T) {
	agent := NewMockAgent()

	response, err := agent.Respond(context.Background(), controller.AgentInput{
		Prompt: "Use a safer option.",
		Task: controller.TaskContext{
			PendingApproval: &controller.ApprovalRequest{
				ID:      "approval-1",
				Kind:    controller.ApprovalCommand,
				Title:   "Destructive command approval",
				Summary: "rm -rf tmp",
				Command: "rm -rf tmp",
				Risk:    controller.RiskHigh,
			},
		},
	})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if response.Approval == nil {
		t.Fatal("expected refined approval response")
	}

	if response.Approval.Command != "rm -rf tmp" {
		t.Fatalf("expected original command to be preserved, got %q", response.Approval.Command)
	}

	if response.Approval.Summary != "Use a safer option." {
		t.Fatalf("expected refinement note in summary, got %q", response.Approval.Summary)
	}
}

func TestMockAgentPrioritizesRecoveryForAwaitingInput(t *testing.T) {
	agent := NewMockAgent()

	response, err := agent.Respond(context.Background(), controller.AgentInput{
		Prompt: "what should I do now?",
		Task: controller.TaskContext{
			CurrentExecution: &controller.CommandExecution{
				ID:               "cmd-1",
				Command:          `bash -lc 'read -n 1 -s -r -p "Press any key" _'`,
				State:            controller.CommandExecutionAwaitingInput,
				LatestOutputTail: "Press any key",
			},
			RecoverySnapshot: "Press any key\n",
		},
	})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if response.Message == "" {
		t.Fatal("expected recovery guidance message")
	}
	if !strings.Contains(strings.ToLower(response.Message), "press f2") {
		t.Fatalf("expected take-control guidance, got %q", response.Message)
	}
	if response.Proposal != nil || response.Approval != nil || response.Plan != nil {
		t.Fatalf("expected recovery-only response, got %#v", response)
	}
}

func TestMockAgentPrioritizesRecoveryForFullscreen(t *testing.T) {
	agent := NewMockAgent()

	response, err := agent.Respond(context.Background(), controller.AgentInput{
		Prompt: "help",
		Task: controller.TaskContext{
			CurrentExecution: &controller.CommandExecution{
				ID:               "cmd-1",
				Command:          "nano ui-scratchpad.md",
				State:            controller.CommandExecutionInteractiveFullscreen,
				LatestOutputTail: "",
			},
			RecoverySnapshot: "GNU nano 7.2\n",
		},
	})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if !strings.Contains(strings.ToLower(response.Message), "fullscreen") {
		t.Fatalf("expected fullscreen guidance, got %q", response.Message)
	}
	if !strings.Contains(strings.ToLower(response.Message), "press f2") {
		t.Fatalf("expected take-control guidance, got %q", response.Message)
	}
}

func TestMockAgentCanProposeKeysForAwaitingInput(t *testing.T) {
	agent := NewMockAgent()

	response, err := agent.Respond(context.Background(), controller.AgentInput{
		Prompt: "go ahead and send a keystroke, press enter",
		Task: controller.TaskContext{
			CurrentExecution: &controller.CommandExecution{
				ID:      "cmd-1",
				Command: `bash -lc 'read -n 1 -s -r -p "Press any key" _'`,
				State:   controller.CommandExecutionAwaitingInput,
			},
		},
	})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if response.Proposal == nil {
		t.Fatal("expected keys proposal")
	}
	if response.Proposal.Kind != controller.ProposalKeys {
		t.Fatalf("expected keys proposal, got %s", response.Proposal.Kind)
	}
	if response.Proposal.Keys != "\n" {
		t.Fatalf("expected enter key proposal, got %#v", response.Proposal.Keys)
	}
}

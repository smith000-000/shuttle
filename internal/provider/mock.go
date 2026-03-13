package provider

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"aiterm/internal/controller"
)

type MockAgent struct {
	counter atomic.Uint64
}

func NewMockAgent() *MockAgent {
	return &MockAgent{}
}

func (m *MockAgent) Respond(_ context.Context, input controller.AgentInput) (controller.AgentResponse, error) {
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return controller.AgentResponse{
			Message: "Please enter a task or question.",
		}, nil
	}

	lower := strings.ToLower(prompt)

	switch {
	case containsAny(lower, "what happened", "summarize result", "what changed"):
		return controller.AgentResponse{
			Message: summarizeRecentContext(input),
		}, nil
	case containsAny(lower, "agent-started shell command is still running", "give a brief status update based on the latest shell output"):
		return controller.AgentResponse{
			Message: summarizeActiveExecution(input),
		}, nil
	case containsAny(lower, "previously approved or proposed command has completed", "continue the task using the latest shell output"):
		return continueActivePlanResponse(input), nil
	case containsAny(lower, "continue the active plan from the current step"):
		return continueActivePlanResponse(input), nil
	case input.Task.PendingApproval != nil:
		return refineApprovalResponse(m, prompt, *input.Task.PendingApproval), nil
	case containsAny(lower, "delete", "remove", "rm ", "drop ", "destroy"):
		return controller.AgentResponse{
			Message: "This action looks destructive and requires approval before execution.",
			Approval: &controller.ApprovalRequest{
				ID:      m.nextID("approval"),
				Kind:    controller.ApprovalCommand,
				Title:   "Destructive command approval",
				Summary: prompt,
				Command: prompt,
				Risk:    controller.RiskHigh,
			},
		}, nil
	case containsAny(lower, "list files", "show files", "directory listing", "ls"):
		return controller.AgentResponse{
			Message: "I can inspect the current directory contents.",
			Proposal: &controller.Proposal{
				Kind:        controller.ProposalCommand,
				Command:     "ls -lah",
				Description: "List files with permissions and sizes.",
			},
		}, nil
	case containsAny(lower, "show plan", "make a plan", "plan"):
		return controller.AgentResponse{
			Message: "Here is a short plan.",
			Plan: &controller.Plan{
				Summary: "Inspect the current state, choose the next shell action, and verify the result.",
				Steps: []string{
					"Review the recent shell output and current directory.",
					"Run the next shell command needed to make progress.",
					"Check the result and decide whether another step is required.",
				},
			},
		}, nil
	default:
		return controller.AgentResponse{
			Message: fmt.Sprintf("Mock agent received: %s", prompt),
			Proposal: &controller.Proposal{
				Kind:        controller.ProposalAnswer,
				Description: "No shell action proposed for this prompt yet.",
			},
		}, nil
	}
}

func refineApprovalResponse(m *MockAgent, note string, approval controller.ApprovalRequest) controller.AgentResponse {
	message := "Refinement noted. This action still requires approval before execution."
	if trimmed := strings.TrimSpace(note); trimmed != "" {
		message = fmt.Sprintf("Refinement noted: %s", trimmed)
	}

	title := approval.Title
	if title == "" {
		title = "Refined approval"
	}

	summary := approval.Summary
	if strings.TrimSpace(note) != "" {
		summary = note
	}

	return controller.AgentResponse{
		Message: message,
		Approval: &controller.ApprovalRequest{
			ID:      m.nextID("approval"),
			Kind:    approval.Kind,
			Title:   title,
			Summary: summary,
			Command: approval.Command,
			Patch:   approval.Patch,
			Risk:    approval.Risk,
		},
	}
}

func summarizeRecentContext(input controller.AgentInput) string {
	parts := make([]string, 0, 2)

	if input.Task.LastCommandResult != nil {
		if input.Task.LastCommandResult.State == controller.CommandExecutionCanceled {
			parts = append(parts, fmt.Sprintf(
				"Last command `%s` was canceled.",
				input.Task.LastCommandResult.Command,
			))
		} else {
			parts = append(parts, fmt.Sprintf(
				"Last command `%s` exited with code %d.",
				input.Task.LastCommandResult.Command,
				input.Task.LastCommandResult.ExitCode,
			))
		}
		if input.Task.LastCommandResult.Cause != "" || input.Task.LastCommandResult.Confidence != "" {
			meta := []string{}
			if input.Task.LastCommandResult.Cause != "" {
				meta = append(meta, "cause="+string(input.Task.LastCommandResult.Cause))
			}
			if input.Task.LastCommandResult.Confidence != "" {
				meta = append(meta, "confidence="+string(input.Task.LastCommandResult.Confidence))
			}
			parts = append(parts, "Result metadata: "+strings.Join(meta, ", "))
		}
	}

	if trimmed := strings.TrimSpace(input.Session.RecentShellOutput); trimmed != "" {
		parts = append(parts, "Recent shell output:\n"+compactShellOutput(trimmed, 2, 2, 400))
	}

	if len(parts) == 0 {
		return "I do not have recent shell context yet."
	}

	return strings.Join(parts, "\n\n")
}

func summarizeActiveExecution(input controller.AgentInput) string {
	current := input.Task.CurrentExecution
	if current == nil {
		return "I no longer see an active command."
	}

	lines := []string{
		fmt.Sprintf("Active command `%s` is in state `%s`.", current.Command, current.State),
	}

	if trimmed := strings.TrimSpace(current.LatestOutputTail); trimmed != "" {
		lines = append(lines, "Latest shell output:\n"+compactShellOutput(trimmed, 2, 2, 400))
	}

	return strings.Join(lines, "\n\n")
}

func continueActivePlanResponse(input controller.AgentInput) controller.AgentResponse {
	if input.Task.ActivePlan == nil {
		return controller.AgentResponse{
			Message: summarizeRecentContext(input),
		}
	}

	next := nextPlanStep(*input.Task.ActivePlan)
	if next == "" {
		return controller.AgentResponse{
			Message: "The active plan appears complete.",
		}
	}

	lower := strings.ToLower(next)
	switch {
	case containsAny(lower, "inspect", "review", "list", "directory", "files", "status"):
		return controller.AgentResponse{
			Message: "Continuing the active plan with the next inspection step.",
			Proposal: &controller.Proposal{
				Kind:        controller.ProposalCommand,
				Command:     "ls -lah",
				Description: next,
			},
		}
	default:
		return controller.AgentResponse{
			Message: "Next active plan step: " + next,
		}
	}
}

func nextPlanStep(plan controller.ActivePlan) string {
	for _, step := range plan.Steps {
		if step.Status == controller.PlanStepInProgress || step.Status == controller.PlanStepPending {
			return strings.TrimSpace(step.Text)
		}
	}

	return ""
}

func (m *MockAgent) nextID(prefix string) string {
	value := m.counter.Add(1)
	return fmt.Sprintf("%s-%d", prefix, value)
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}

	return false
}

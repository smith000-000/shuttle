package tui

import (
	"aiterm/internal/controller"
	"aiterm/internal/shell"
	"aiterm/internal/tmux"
	"context"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fakeWorkspace() tmux.Workspace {
	return tmux.Workspace{
		SessionName: "shuttle-test",
		TopPane: tmux.Pane{
			ID: "%0",
		},
		BottomPane: tmux.Pane{
			ID: "%1",
		},
	}
}

func transcriptLineIndexForEntry(model Model, entryIndex int) int {
	lines := model.transcriptWindowDisplay(model.transcriptDisplayLines(model.currentTranscriptWidth()), model.currentTranscriptHeight())
	for index, line := range lines {
		if line.entryIndex == entryIndex {
			return index
		}
	}
	return 0
}

func actionCardButtonPoint(t *testing.T, model Model, buttonIndex int) (int, int) {
	t.Helper()
	spec := model.currentActionCardSpec()
	if spec == nil {
		t.Fatal("expected action card spec")
	}
	if buttonIndex < 0 || buttonIndex >= len(spec.buttons) {
		t.Fatalf("invalid button index %d for %#v", buttonIndex, spec.buttons)
	}
	startY, ok := model.actionCardStartY()
	if !ok {
		t.Fatal("expected action card start position")
	}

	contentWidth := model.contentWidthFor(model.currentTranscriptWidth(), model.styles.actionCard)
	bodyLines := actionCardBodyLines(spec.body, contentWidth)
	buttonLines := layoutActionCardButtons(spec.buttons, contentWidth)
	targetAction := spec.buttons[buttonIndex].action
	for lineIndex, line := range buttonLines {
		for _, hit := range line.hits {
			if hit.action != targetAction {
				continue
			}
			x := model.styles.actionCard.GetBorderLeftSize() + model.styles.actionCard.GetPaddingLeft() + hit.start + 1
			y := startY + model.styles.actionCard.GetBorderTopSize() + 1 + len(bodyLines) + lineIndex
			return x, y
		}
	}
	t.Fatalf("no hit target for button %d (%q)", buttonIndex, spec.buttons[buttonIndex].label)
	return 0, 0
}

func makeTranscriptEntries(count int) []Entry {
	entries := make([]Entry, 0, count)
	for index := 0; index < count; index++ {
		entries = append(entries, Entry{
			Title: "result",
			Body:  fmt.Sprintf("line %d", index),
		})
	}

	return entries
}

func makeMultilineBody(count int) string {
	lines := make([]string, 0, count)
	for index := 0; index < count; index++ {
		lines = append(lines, fmt.Sprintf("line %d", index))
	}

	return strings.Join(lines, "\n")
}

type fakeController struct {
	agentEvents              []controller.TranscriptEvent
	continueEvents           []controller.TranscriptEvent
	resumeEventsQueue        [][]controller.TranscriptEvent
	continueAfterPatchEvents []controller.TranscriptEvent
	checkInEvents            []controller.TranscriptEvent
	shellEvents              []controller.TranscriptEvent
	patchEvents              []controller.TranscriptEvent
	inspectContextEvents     []controller.TranscriptEvent
	newTaskEvents            []controller.TranscriptEvent
	compactTaskEvents        []controller.TranscriptEvent
	shellCommands            []string
	appliedPatches           []string
	appliedPatchTargets      []controller.PatchTarget
	inspectContextCalls      int
	decisionEvents           []controller.TranscriptEvent
	decisions                []approvalDecisionCall
	refinements              []refinementCall
	continueCalls            int
	continueAfterPatchCalls  int
	checkInCalls             int
	newTaskCalls             int
	compactTaskCalls         int
	activeExecution          *controller.CommandExecution
	refreshEvents            []controller.TranscriptEvent
	abandonReason            string
	peekShellTail            string
	sessionName              string
	trackedPaneID            string
	contextUsage             controller.ContextWindowUsage
	approvalMode             controller.ApprovalMode
	refreshedShellContext    *shell.PromptContext
	refreshShellContextErr   error
	refreshShellContextCalls int
}

func (f *fakeController) SubmitAgentPrompt(_ context.Context, _ string) ([]controller.TranscriptEvent, error) {
	return append([]controller.TranscriptEvent(nil), f.agentEvents...), nil
}

func (f *fakeController) SubmitRefinement(_ context.Context, approval controller.ApprovalRequest, note string) ([]controller.TranscriptEvent, error) {
	f.refinements = append(f.refinements, refinementCall{
		approval: approval,
		note:     note,
	})
	return append([]controller.TranscriptEvent(nil), f.agentEvents...), nil
}

func (f *fakeController) SubmitProposalRefinement(_ context.Context, proposal controller.ProposalPayload, note string) ([]controller.TranscriptEvent, error) {
	f.refinements = append(f.refinements, refinementCall{
		proposal: proposal,
		note:     note,
	})
	return append([]controller.TranscriptEvent(nil), f.agentEvents...), nil
}

func (f *fakeController) ContinueAfterCommand(_ context.Context) ([]controller.TranscriptEvent, error) {
	f.continueCalls++
	if len(f.continueEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Reviewed the last command result."},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.continueEvents...), nil
}

func (f *fakeController) ContinueAfterPatchApply(_ context.Context) ([]controller.TranscriptEvent, error) {
	f.continueAfterPatchCalls++
	if len(f.continueAfterPatchEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Reviewed the applied patch."},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.continueAfterPatchEvents...), nil
}

func (f *fakeController) ResumeAfterTakeControl(_ context.Context) ([]controller.TranscriptEvent, error) {
	f.continueCalls++
	if len(f.resumeEventsQueue) > 0 {
		events := append([]controller.TranscriptEvent(nil), f.resumeEventsQueue[0]...)
		f.resumeEventsQueue = f.resumeEventsQueue[1:]
		return events, nil
	}
	if len(f.continueEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Resuming after take control."},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.continueEvents...), nil
}

func (f *fakeController) ContinueActivePlan(_ context.Context) ([]controller.TranscriptEvent, error) {
	f.continueCalls++
	if len(f.continueEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Continuing the active plan."},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.continueEvents...), nil
}

func (f *fakeController) CheckActiveExecution(_ context.Context) ([]controller.TranscriptEvent, error) {
	f.checkInCalls++
	if len(f.checkInEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind:    controller.EventAgentMessage,
				Payload: controller.TextPayload{Text: "Still monitoring the active command."},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.checkInEvents...), nil
}

func (f *fakeController) SubmitShellCommand(_ context.Context, command string) ([]controller.TranscriptEvent, error) {
	f.shellCommands = append(f.shellCommands, command)
	if len(f.shellEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind: controller.EventCommandStart,
				Payload: controller.CommandStartPayload{
					Command: command,
					Execution: controller.CommandExecution{
						ID:        "cmd-1",
						Command:   command,
						Origin:    controller.CommandOriginUserShell,
						State:     controller.CommandExecutionRunning,
						StartedAt: time.Now(),
					},
				},
			},
			{
				Kind: controller.EventCommandResult,
				Payload: controller.CommandResultSummary{
					ExecutionID: "cmd-1",
					Command:     command,
					Origin:      controller.CommandOriginUserShell,
					ExitCode:    0,
					Summary:     command,
				},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.shellEvents...), nil
}

func (f *fakeController) SubmitProposedShellCommand(_ context.Context, command string) ([]controller.TranscriptEvent, error) {
	return f.SubmitShellCommand(context.Background(), command)
}

func (f *fakeController) ApplyProposedPatch(_ context.Context, patch string, target controller.PatchTarget) ([]controller.TranscriptEvent, error) {
	f.appliedPatches = append(f.appliedPatches, patch)
	f.appliedPatchTargets = append(f.appliedPatchTargets, target)
	if len(f.patchEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind: controller.EventPatchApplyResult,
				Payload: controller.PatchApplySummary{
					Applied: true,
					Target:  target,
					Updated: 1,
					Files: []controller.PatchApplyFile{
						{Operation: "update", NewPath: "README.md"},
					},
				},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.patchEvents...), nil
}

func (f *fakeController) DecideApproval(_ context.Context, approvalID string, decision controller.ApprovalDecision, refineText string) ([]controller.TranscriptEvent, error) {
	f.decisions = append(f.decisions, approvalDecisionCall{
		approvalID: approvalID,
		decision:   decision,
		refineText: refineText,
	})
	if len(f.decisionEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind:    controller.EventSystemNotice,
				Payload: controller.TextPayload{Text: "approval handled"},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.decisionEvents...), nil
}

func (f *fakeController) StartNewTask(_ context.Context) ([]controller.TranscriptEvent, error) {
	f.newTaskCalls++
	if len(f.newTaskEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind:    controller.EventSystemNotice,
				Payload: controller.TextPayload{Text: "Started a fresh task context. Shell continuity and provider settings were preserved."},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.newTaskEvents...), nil
}

func (f *fakeController) CompactTask(_ context.Context) ([]controller.TranscriptEvent, error) {
	f.compactTaskCalls++
	if len(f.compactTaskEvents) == 0 {
		return []controller.TranscriptEvent{
			{
				Kind:    controller.EventSystemNotice,
				Payload: controller.TextPayload{Text: "Compacted task context into a summary and kept 8 recent transcript event(s)."},
			},
		}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.compactTaskEvents...), nil
}

func (f *fakeController) InspectProposedContext(_ context.Context) ([]controller.TranscriptEvent, error) {
	f.inspectContextCalls++
	if len(f.inspectContextEvents) == 0 {
		return []controller.TranscriptEvent{{
			Kind: controller.EventCommandResult,
			Payload: controller.CommandResultSummary{
				Command:  "inspect current shell context",
				State:    controller.CommandExecutionCompleted,
				ExitCode: 0,
				Summary:  "user_host=localuser@workstation\ncwd=/workspace/project\nremote=false",
			},
		}}, nil
	}
	return append([]controller.TranscriptEvent(nil), f.inspectContextEvents...), nil
}

func (f *fakeController) SetApprovalMode(_ context.Context, mode controller.ApprovalMode) ([]controller.TranscriptEvent, error) {
	f.approvalMode = mode
	return []controller.TranscriptEvent{{
		Kind:    controller.EventSystemNotice,
		Payload: controller.TextPayload{Text: controller.ApprovalModeDescription(mode)},
	}}, nil
}

func (f *fakeController) RefreshShellContext(_ context.Context) (*shell.PromptContext, error) {
	f.refreshShellContextCalls++
	if f.refreshShellContextErr != nil {
		return nil, f.refreshShellContextErr
	}
	if f.refreshedShellContext != nil {
		contextCopy := *f.refreshedShellContext
		return &contextCopy, nil
	}
	return &shell.PromptContext{
		User:      "localuser",
		Host:      "workstation",
		Directory: "/workspace/project",
	}, nil
}

func (f *fakeController) PeekShellTail(_ context.Context, _ int) (string, error) {
	if f.peekShellTail != "" {
		return f.peekShellTail, nil
	}
	return "waiting for input", nil
}

func (f *fakeController) EstimateContextUsage(_ string) controller.ContextWindowUsage {
	return f.contextUsage
}

func (f *fakeController) ApprovalMode() controller.ApprovalMode {
	if f.approvalMode == "" {
		return controller.ApprovalModeConfirm
	}
	return f.approvalMode
}

func (f *fakeController) ActiveExecution() *controller.CommandExecution {
	if f.activeExecution == nil {
		return nil
	}
	execution := *f.activeExecution
	return &execution
}

func (f *fakeController) RefreshActiveExecution(_ context.Context) ([]controller.TranscriptEvent, *controller.CommandExecution, error) {
	var execution *controller.CommandExecution
	if f.activeExecution != nil {
		copy := *f.activeExecution
		execution = &copy
	}
	return append([]controller.TranscriptEvent(nil), f.refreshEvents...), execution, nil
}

func (f *fakeController) AbandonActiveExecution(reason string) *controller.CommandExecution {
	f.abandonReason = reason
	if f.activeExecution == nil {
		return nil
	}
	execution := *f.activeExecution
	f.activeExecution = nil
	return &execution
}

func (f *fakeController) TrackedShellTarget() controller.TrackedShellTarget {
	sessionName := strings.TrimSpace(f.sessionName)
	if sessionName == "" {
		sessionName = "shuttle-test"
	}
	if strings.TrimSpace(f.trackedPaneID) != "" {
		return controller.TrackedShellTarget{SessionName: sessionName, PaneID: f.trackedPaneID}
	}
	return controller.TrackedShellTarget{SessionName: sessionName, PaneID: "%0"}
}

func (f *fakeController) TakeControlTarget() controller.TrackedShellTarget {
	if f.activeExecution != nil && strings.TrimSpace(f.activeExecution.TrackedShell.PaneID) != "" {
		switch f.activeExecution.State {
		case controller.CommandExecutionAwaitingInput, controller.CommandExecutionInteractiveFullscreen, controller.CommandExecutionHandoffActive:
			sessionName := strings.TrimSpace(f.activeExecution.TrackedShell.SessionName)
			if sessionName == "" {
				sessionName = f.TrackedShellTarget().SessionName
			}
			return controller.TrackedShellTarget{
				SessionName: sessionName,
				PaneID:      strings.TrimSpace(f.activeExecution.TrackedShell.PaneID),
			}
		}
	}
	return f.TrackedShellTarget()
}

type approvalDecisionCall struct {
	approvalID string
	decision   controller.ApprovalDecision
	refineText string
}

type refinementCall struct {
	approval controller.ApprovalRequest
	proposal controller.ProposalPayload
	note     string
}

func controllerEventsFromCmd(t *testing.T, cmd tea.Cmd) controllerEventsMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}

	msg := cmd()
	switch typed := msg.(type) {
	case controllerEventsMsg:
		return typed
	case tea.BatchMsg:
		for _, candidate := range typed {
			if candidate == nil {
				continue
			}
			nested := candidate()
			if eventMsg, ok := nested.(controllerEventsMsg); ok {
				return eventMsg
			}
		}
		t.Fatalf("expected controllerEventsMsg in batch, got %#v", typed)
	default:
		t.Fatalf("expected controllerEventsMsg or tea.BatchMsg, got %T", msg)
	}

	return controllerEventsMsg{}
}

func cmdContainsMessageType(t *testing.T, cmd tea.Cmd, expected tea.Msg) bool {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}

	expectedType := reflect.TypeOf(expected)
	msg := cmd()
	if reflect.TypeOf(msg) == expectedType {
		return true
	}

	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return false
	}
	for _, candidate := range batch {
		if candidate == nil {
			continue
		}
		if reflect.TypeOf(candidate()) == expectedType {
			return true
		}
	}
	return false
}

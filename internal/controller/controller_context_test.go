package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"aiterm/internal/agentruntime"
)

type stubRuntime struct {
	outcome agentruntime.Outcome
	err     error
	reqs    []agentruntime.Request
}

func (s *stubRuntime) Handle(_ context.Context, _ agentruntime.Host, req agentruntime.Request) (agentruntime.Outcome, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return agentruntime.Outcome{}, s.err
	}
	return s.outcome, nil
}

type stubRuntimeHost struct{}

func (stubRuntimeHost) Respond(context.Context, agentruntime.Request) (agentruntime.Outcome, error) {
	return agentruntime.Outcome{}, nil
}

func (stubRuntimeHost) InspectContext(context.Context, agentruntime.Request) error { return nil }

func (stubRuntimeHost) SynthesizeStructuredEdit(_ context.Context, outcome agentruntime.Outcome) (agentruntime.Outcome, error) {
	return outcome, nil
}

func (stubRuntimeHost) ValidatePatch(context.Context, string, string) error { return nil }

func TestLocalControllerStartNewTaskResetsTaskStateButPreservesSession(t *testing.T) {
	stateDir := t.TempDir()
	controller := New(nil, nil, nil, SessionContext{
		SessionName:      "shuttle-test",
		TrackedShell:     TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
		WorkingDirectory: "/workspace",
		ApprovalMode:     ApprovalModeAuto,
		StateDir:         stateDir,
	})
	oldTaskID := controller.task.TaskID
	if err := agentruntime.SaveStoredCodexAppServerThreadBinding(stateDir, "shuttle-test", oldTaskID, "thread-old"); err != nil {
		t.Fatalf("SaveStoredCodexAppServerThreadBinding() error = %v", err)
	}
	controller.task.CompactedSummary = "old summary"
	controller.task.PriorTranscript = []TranscriptEvent{{Kind: EventUserMessage, Payload: TextPayload{Text: "old task"}}}
	controller.task.LastCommandResult = &CommandResultSummary{Command: "ls -lah"}
	controller.task.ActivePlan = &ActivePlan{
		Summary: "Old plan",
		Steps:   []PlanStep{{Text: "Old step", Status: PlanStepInProgress}},
	}

	events, err := controller.StartNewTask(context.Background())
	if err != nil {
		t.Fatalf("StartNewTask() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventSystemNotice {
		t.Fatalf("expected one system notice event, got %#v", events)
	}
	if controller.task.TaskID == "" || controller.task.TaskID == "task-1" {
		t.Fatalf("expected a fresh task id, got %q", controller.task.TaskID)
	}
	if controller.task.CompactedSummary != "" || controller.task.LastCommandResult != nil || controller.task.ActivePlan != nil {
		t.Fatalf("expected task state to reset, got %#v", controller.task)
	}
	if len(controller.task.PriorTranscript) != 1 || controller.task.PriorTranscript[0].Kind != EventSystemNotice {
		t.Fatalf("expected new-task notice to seed the fresh transcript, got %#v", controller.task.PriorTranscript)
	}
	if controller.session.WorkingDirectory != "/workspace" || controller.session.TrackedShell.PaneID != "%0" || controller.session.ApprovalMode != ApprovalModeAuto {
		t.Fatalf("expected session continuity to be preserved, got %#v", controller.session)
	}
	if controller.session.LocalWorkingDirectory == "" {
		t.Fatalf("expected local working directory probe to be populated, got %#v", controller.session)
	}
	threadID, ok, err := agentruntime.LoadStoredCodexAppServerThreadBinding(stateDir, "shuttle-test", oldTaskID)
	if err != nil {
		t.Fatalf("LoadStoredCodexAppServerThreadBinding() error = %v", err)
	}
	if ok || threadID != "" {
		t.Fatalf("expected previous task binding to be cleared, got ok=%v thread=%q", ok, threadID)
	}
}

func TestLocalControllerStartNewTaskBlocksPendingApproval(t *testing.T) {
	controller := New(nil, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	controller.task.PendingApproval = &ApprovalRequest{
		ID:    "approval-1",
		Title: "Dangerous change",
	}

	events, err := controller.StartNewTask(context.Background())
	if err != nil {
		t.Fatalf("StartNewTask() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventError {
		t.Fatalf("expected one error event, got %#v", events)
	}
	payload, _ := events[0].Payload.(TextPayload)
	if !strings.Contains(payload.Text, "pending") {
		t.Fatalf("expected guardrail message, got %q", payload.Text)
	}
	if controller.task.PendingApproval == nil {
		t.Fatal("expected pending approval to remain unchanged")
	}
}

func TestLocalControllerCompactTaskStoresSummaryAndTrimsTranscript(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "User wants to finish the workspace update. The last shell step succeeded. Next, run the targeted tests.",
		},
	}
	controller := New(agent, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	for index := 0; index < 12; index++ {
		controller.task.PriorTranscript = append(controller.task.PriorTranscript, TranscriptEvent{
			Kind:    EventUserMessage,
			Payload: TextPayload{Text: fmt.Sprintf("event-%02d", index)},
		})
	}
	controller.task.RecoverySnapshot = "snapshot lines"

	events, err := controller.CompactTask(context.Background())
	if err != nil {
		t.Fatalf("CompactTask() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventSystemNotice {
		t.Fatalf("expected one compaction notice event, got %#v", events)
	}
	if !strings.Contains(agent.lastInput.Prompt, "Summarize the current Shuttle task") {
		t.Fatalf("expected compaction prompt, got %q", agent.lastInput.Prompt)
	}
	if controller.task.CompactedSummary == "" {
		t.Fatal("expected compacted summary to be stored")
	}
	if controller.task.RecoverySnapshot != "" {
		t.Fatalf("expected recovery snapshot to clear after compaction, got %q", controller.task.RecoverySnapshot)
	}
	if len(controller.task.PriorTranscript) != compactTaskTranscriptTail+1 {
		t.Fatalf("expected trimmed transcript plus compaction notice, got %d entries", len(controller.task.PriorTranscript))
	}
	firstPayload, _ := controller.task.PriorTranscript[0].Payload.(TextPayload)
	if firstPayload.Text != "event-04" {
		t.Fatalf("expected transcript tail to start at event-04, got %#v", controller.task.PriorTranscript[0])
	}
	last := controller.task.PriorTranscript[len(controller.task.PriorTranscript)-1]
	if last.Kind != EventSystemNotice {
		t.Fatalf("expected final compaction notice in transcript, got %#v", last)
	}
}

func TestLocalControllerCompactTaskAcceptsNativeCompactionAndClearsSummary(t *testing.T) {
	controller := New(nil, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	runtime := &stubRuntime{outcome: agentruntime.Outcome{NativeCompaction: true}}
	controller.SetRuntime(runtime)
	controller.SetRuntimeHost(stubRuntimeHost{})
	for index := 0; index < 12; index++ {
		controller.task.PriorTranscript = append(controller.task.PriorTranscript, TranscriptEvent{
			Kind:    EventUserMessage,
			Payload: TextPayload{Text: fmt.Sprintf("event-%02d", index)},
		})
	}
	controller.task.RecoverySnapshot = "snapshot lines"
	controller.task.CompactedSummary = "old summary"

	events, err := controller.CompactTask(context.Background())
	if err != nil {
		t.Fatalf("CompactTask() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventSystemNotice {
		t.Fatalf("expected one compaction notice event, got %#v", events)
	}
	if len(runtime.reqs) != 1 || runtime.reqs[0].Kind != agentruntime.RequestCompactTask {
		t.Fatalf("expected native compact runtime request, got %#v", runtime.reqs)
	}
	if controller.task.CompactedSummary != "" {
		t.Fatalf("expected native compaction to clear stored summary, got %q", controller.task.CompactedSummary)
	}
	if controller.task.RecoverySnapshot != "" {
		t.Fatalf("expected recovery snapshot to clear after native compaction, got %q", controller.task.RecoverySnapshot)
	}
	if len(controller.task.PriorTranscript) != compactTaskTranscriptTail+1 {
		t.Fatalf("expected trimmed transcript plus compaction notice, got %d entries", len(controller.task.PriorTranscript))
	}
	last := controller.task.PriorTranscript[len(controller.task.PriorTranscript)-1]
	payload, _ := last.Payload.(TextPayload)
	if last.Kind != EventSystemNotice || !strings.Contains(payload.Text, "runtime thread") {
		t.Fatalf("expected native compaction notice, got %#v", last)
	}
}

func TestEstimateContextUsageShrinksAfterCompaction(t *testing.T) {
	controller := New(nil, nil, nil, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	for index := 0; index < 24; index++ {
		controller.task.PriorTranscript = append(controller.task.PriorTranscript, TranscriptEvent{
			Kind:    EventUserMessage,
			Payload: TextPayload{Text: strings.Repeat("history item ", 12) + fmt.Sprintf("%d", index)},
		})
	}

	before := controller.EstimateContextUsage("continue")
	controller.task.CompactedSummary = "Short summary."
	after := controller.EstimateContextUsage("continue")

	if before.ApproxPromptTokens <= after.ApproxPromptTokens {
		t.Fatalf("expected compacted usage to shrink, got before=%d after=%d", before.ApproxPromptTokens, after.ApproxPromptTokens)
	}
}

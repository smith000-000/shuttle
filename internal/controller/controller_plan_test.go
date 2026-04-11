package controller

import (
	"context"
	"strings"
	"testing"

	"aiterm/internal/shell"
)

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

	if !strings.Contains(agent.lastInput.Prompt, buildAutoContinuePrompt(controller.task)) {
		t.Fatalf("expected auto-continue prompt, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, stateAuthorityPromptSuffix) {
		t.Fatalf("expected state-authority guidance, got %q", agent.lastInput.Prompt)
	}

	if agent.lastInput.Task.LastCommandResult == nil || agent.lastInput.Task.LastCommandResult.Command != "ls" {
		t.Fatalf("expected last command result in agent input, got %#v", agent.lastInput.Task.LastCommandResult)
	}
}

func TestLocalControllerContinueAfterCommandAppendsPlanStatusCheckPrompt(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Continuing the active plan.",
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
	if _, err := controller.ContinueAfterCommand(context.Background()); err != nil {
		t.Fatalf("ContinueAfterCommand() error = %v", err)
	}

	if !strings.Contains(agent.lastInput.Prompt, activePlanStatusCheckPromptSuffix) {
		t.Fatalf("expected active plan status-check guidance, got %q", agent.lastInput.Prompt)
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

func TestLocalControllerContinueAfterCommandDoesNotInferSerialFromOrdinaryPrompt(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "The requested file edit is complete.",
		},
	}
	controller := New(agent, &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-1",
			Command:   "python update.py",
			ExitCode:  0,
			Captured:  "updated hello.txt",
		},
	}, &stubContextReader{
		output: "updated hello.txt",
	}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	controller.task.PriorTranscript = append(controller.task.PriorTranscript, TranscriptEvent{
		Kind:    EventUserMessage,
		Payload: TextPayload{Text: "ok now add a bubble sort to that file and comment out the hello world. Doesn't matter the language."},
	})

	if _, err := controller.SubmitShellCommand(context.Background(), "python update.py"); err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}

	if _, err := controller.ContinueAfterCommand(context.Background()); err != nil {
		t.Fatalf("ContinueAfterCommand() error = %v", err)
	}

	if strings.Contains(agent.lastInput.Prompt, "propose exactly one next command now") {
		t.Fatalf("did not expect serial continuation prompt, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, "Do not propose extra verification") {
		t.Fatalf("expected stop-biased continuation prompt, got %q", agent.lastInput.Prompt)
	}
}

func TestLocalControllerContinueAfterCommandRequiresNextActionAfterInspectionFailure(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "The inspection confirms the bug.",
		},
	}
	controller := New(agent, &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-1",
			Command:   "sed -n '1,220p' hello.py",
			ExitCode:  0,
			Captured:  "def bubble_sort(arr):\n...\nsorted_nums = selection_sort(nums)\n",
		},
	}, &stubContextReader{
		output: "def bubble_sort(arr):\n...\nsorted_nums = selection_sort(nums)\n",
	}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	controller.task.PriorTranscript = append(controller.task.PriorTranscript,
		TranscriptEvent{
			Kind:    EventUserMessage,
			Payload: TextPayload{Text: "update hello.py to change the sort type from whatever is in there now into a different sort algorithm. Show the results. Then change it back to the original sort and run it again."},
		},
		TranscriptEvent{
			Kind:    EventAgentMessage,
			Payload: TextPayload{Text: "python hello.py failed with NameError: name 'selection_sort' is not defined, so the algorithm swap is incomplete and still needs fixing."},
		},
	)

	if _, err := controller.SubmitShellCommand(context.Background(), "sed -n '1,220p' hello.py"); err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}

	if _, err := controller.ContinueAfterCommand(context.Background()); err != nil {
		t.Fatalf("ContinueAfterCommand() error = %v", err)
	}

	if !strings.Contains(agent.lastInput.Prompt, autoContinuePromptUnresolvedInspectionSuffix) {
		t.Fatalf("expected unresolved-inspection guidance, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, autoContinuePromptChecklistSuffix) {
		t.Fatalf("expected checklist guidance for ordered workflow, got %q", agent.lastInput.Prompt)
	}
}

func TestBuildAutoContinuePromptForcesNextActionAfterFailedInvestigativeInspection(t *testing.T) {
	task := TaskContext{
		PriorTranscript: []TranscriptEvent{
			{Kind: EventUserMessage, Payload: TextPayload{Text: "can you figure out how and where orcaslicer was installed?"}},
		},
		LastCommandResult: &CommandResultSummary{
			Command: "which orcaslicer",
			State:   CommandExecutionFailed,
			Summary: "orcaslicer not found",
		},
	}

	prompt := buildAutoContinuePrompt(task)
	if !strings.Contains(prompt, autoContinuePromptChecklistSuffix) {
		t.Fatalf("expected checklist continuation guidance, got %q", prompt)
	}
	if !strings.Contains(prompt, autoContinuePromptUnresolvedInspectionSuffix) {
		t.Fatalf("expected unresolved investigative guidance, got %q", prompt)
	}
}

func TestBuildAutoContinuePromptForcesNextActionAfterFailedInvestigativeDebugInspection(t *testing.T) {
	task := TaskContext{
		PriorTranscript: []TranscriptEvent{
			{Kind: EventUserMessage, Payload: TextPayload{Text: "can you figure out why nginx is listening on this port?"}},
		},
		LastCommandResult: &CommandResultSummary{
			Command: "ss -ltnp | grep :8080",
			State:   CommandExecutionFailed,
			Summary: "no matches found",
		},
	}

	prompt := buildAutoContinuePrompt(task)
	if !strings.Contains(prompt, autoContinuePromptChecklistSuffix) {
		t.Fatalf("expected checklist continuation guidance, got %q", prompt)
	}
	if !strings.Contains(prompt, autoContinuePromptUnresolvedInspectionSuffix) {
		t.Fatalf("expected unresolved investigative guidance, got %q", prompt)
	}
}

func TestBuildAutoContinuePromptForcesNextActionAfterInconclusiveInvestigativeInspection(t *testing.T) {
	task := TaskContext{
		PriorTranscript: []TranscriptEvent{
			{Kind: EventUserMessage, Payload: TextPayload{Text: "determine which file is loading this environment variable"}},
		},
		LastCommandResult: &CommandResultSummary{
			Command: "rg FOO_ENABLED /etc ~/.config",
			State:   CommandExecutionCompleted,
			Summary: "no results",
		},
	}

	prompt := buildAutoContinuePrompt(task)
	if !strings.Contains(prompt, autoContinuePromptChecklistSuffix) {
		t.Fatalf("expected checklist continuation guidance, got %q", prompt)
	}
	if !strings.Contains(prompt, autoContinuePromptUnresolvedInspectionSuffix) {
		t.Fatalf("expected unresolved investigative guidance, got %q", prompt)
	}
}

func TestBuildAutoContinuePromptForcesPatchRebaseAfterInspection(t *testing.T) {
	task := TaskContext{
		LastCommandResult: &CommandResultSummary{
			Command: "nl -ba /home/openclaw/foo.txt | sed -n '1,20p'",
			State:   CommandExecutionCompleted,
			Summary: "5  alpha\n6  INSERT BELOW HERE\n7  omega",
		},
		LastPatchApplyResult: &PatchApplySummary{
			Applied: false,
			Target:  PatchTargetRemoteShell,
			Error:   "apply foo.txt: conflict: fragment line does not match src line",
		},
	}

	prompt := buildAutoContinuePrompt(task)
	if !strings.Contains(prompt, autoContinuePromptPatchRebaseSuffix) {
		t.Fatalf("expected patch rebase guidance, got %q", prompt)
	}
}

func TestLocalControllerContinueAfterCommandAdvancesActivePlan(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message:      "Continuing the plan.",
			PlanStatuses: []PlanStepStatus{PlanStepDone, PlanStepInProgress},
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
		t.Fatalf("expected agent message plus plan status update, got %#v", events)
	}
	if events[0].Kind != EventAgentMessage {
		t.Fatalf("expected agent message first, got %#v", events)
	}
	planEvent, ok := events[1].Payload.(PlanPayload)
	if !ok {
		t.Fatalf("expected trailing plan payload, got %#v", events[1].Payload)
	}
	if planEvent.Steps[0].Status != PlanStepDone || planEvent.Steps[1].Status != PlanStepInProgress {
		t.Fatalf("expected plan advancement, got %#v", planEvent.Steps)
	}
	if controller.task.ActivePlan == nil || controller.task.ActivePlan.Summary != "Inspect and repair the workspace." {
		t.Fatalf("expected existing active plan to be preserved, got %#v", controller.task.ActivePlan)
	}
}

func TestLocalControllerContinueAfterCommandIgnoresMismatchedPlanStatusUpdate(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message:      "Continuing the plan.",
			PlanStatuses: []PlanStepStatus{PlanStepDone},
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

	if len(events) != 1 || events[0].Kind != EventAgentMessage {
		t.Fatalf("expected only agent message when plan status update mismatches, got %#v", events)
	}
	if controller.task.ActivePlan == nil || controller.task.ActivePlan.Steps[0].Status != PlanStepInProgress {
		t.Fatalf("expected active plan to remain unchanged, got %#v", controller.task.ActivePlan)
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
	if !strings.Contains(agent.lastInput.Prompt, continuePlanPrompt) {
		t.Fatalf("expected continue-plan prompt, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, stateAuthorityPromptSuffix) {
		t.Fatalf("expected state-authority guidance, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, activePlanStatusCheckPromptSuffix) {
		t.Fatalf("expected active plan status-check guidance, got %q", agent.lastInput.Prompt)
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

	var completedPlan PlanPayload
	foundCompletedPlan := false
	foundAgentMessage := false
	for _, event := range events {
		if event.Kind == EventAgentMessage {
			foundAgentMessage = true
		}
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
	if !foundAgentMessage {
		t.Fatalf("expected agent message in %#v", events)
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

func TestLocalControllerContinueAfterCommandClearsPlanWhenAgentDeclaresChecklistCompletion(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "The checklist is complete: loop.txt now contains beta.",
		},
	}
	controller := New(agent, &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-1",
			Command:   "cat loop.txt",
			ExitCode:  0,
			Captured:  "beta",
		},
	}, &stubContextReader{
		output: "beta",
	}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	controller.task.ActivePlan = &ActivePlan{
		Summary: "Create loop.txt, confirm alpha, replace it with beta, and finish.",
		Steps: []PlanStep{
			{Text: "Write alpha into loop.txt.", Status: PlanStepDone},
			{Text: "Replace loop.txt with beta.", Status: PlanStepDone},
			{Text: "Report completion.", Status: PlanStepInProgress},
		},
	}

	if _, err := controller.SubmitShellCommand(context.Background(), "cat loop.txt"); err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}

	events, err := controller.ContinueAfterCommand(context.Background())
	if err != nil {
		t.Fatalf("ContinueAfterCommand() error = %v", err)
	}

	var completedPlan PlanPayload
	foundCompletedPlan := false
	foundAgentMessage := false
	for _, event := range events {
		if event.Kind == EventAgentMessage {
			foundAgentMessage = true
		}
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
	if !foundAgentMessage {
		t.Fatalf("expected agent message in %#v", events)
	}
	for _, step := range completedPlan.Steps {
		if step.Status != PlanStepDone {
			t.Fatalf("expected completed plan event, got %#v", completedPlan)
		}
	}
	if controller.task.ActivePlan != nil {
		t.Fatalf("expected active plan to clear after checklist completion, got %#v", controller.task.ActivePlan)
	}
}

func TestLocalControllerContinueAfterCommandClearsInformationalFinalStepOnMessageOnlyResponse(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Both counter loops finished successfully.",
		},
	}
	controller := New(agent, &stubRunner{
		result: shell.TrackedExecution{
			CommandID: "cmd-1",
			Command:   "for i in $(seq 1 15); do echo $i; sleep 1; done",
			ExitCode:  0,
			Captured:  "15",
		},
	}, &stubContextReader{
		output: "15",
	}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})
	controller.task.ActivePlan = &ActivePlan{
		Summary: "Run the counter loop twice and report the result.",
		Steps: []PlanStep{
			{Text: "Run the first counter loop.", Status: PlanStepDone},
			{Text: "Report the result.", Status: PlanStepInProgress},
		},
	}

	if _, err := controller.SubmitShellCommand(context.Background(), "for i in $(seq 1 15); do echo $i; sleep 1; done"); err != nil {
		t.Fatalf("SubmitShellCommand() error = %v", err)
	}

	events, err := controller.ContinueAfterCommand(context.Background())
	if err != nil {
		t.Fatalf("ContinueAfterCommand() error = %v", err)
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
		t.Fatalf("expected active plan to clear after informational final step, got %#v", controller.task.ActivePlan)
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
	if plan.Steps[0].Status != PlanStepDone {
		t.Fatalf("expected done status on first step, got %#v", plan.Steps[0])
	}
	if plan.Steps[1].Text != "Review the file list output." {
		t.Fatalf("expected normalized second step text, got %#v", plan.Steps[1])
	}
	if plan.Steps[1].Status != PlanStepInProgress {
		t.Fatalf("expected in-progress status on second step, got %#v", plan.Steps[1])
	}
	if plan.Steps[2].Text != "Select one Markdown file at random." {
		t.Fatalf("expected normalized third step text, got %#v", plan.Steps[2])
	}
	if plan.Steps[2].Status != PlanStepPending {
		t.Fatalf("expected pending status on third step, got %#v", plan.Steps[2])
	}
}

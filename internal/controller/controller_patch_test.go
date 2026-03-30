package controller

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"aiterm/internal/patchapply"
	"aiterm/internal/shell"
)

func TestLocalControllerApplyProposedPatch(t *testing.T) {
	applier := &stubPatchApplier{
		result: patchapply.Result{
			WorkspaceRoot: "/repo",
			Validation:    "native",
			Updated:       1,
			Files: []patchapply.FileChange{
				{Operation: patchapply.OperationUpdate, NewPath: "README.md"},
			},
		},
	}
	controller := New(nil, nil, nil, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		LocalWorkspaceRoot: "/repo",
	})
	controller.patches = applier
	controller.patchInitErr = nil

	events, err := controller.ApplyProposedPatch(context.Background(), diffFixture("README.md", "before\n", "after\n"), PatchTargetLocalWorkspace)
	if err != nil {
		t.Fatalf("ApplyProposedPatch() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventPatchApplyResult {
		t.Fatalf("expected patch apply result event, got %#v", events)
	}

	payload, ok := events[0].Payload.(PatchApplySummary)
	if !ok {
		t.Fatalf("expected patch apply payload, got %#v", events[0].Payload)
	}
	if !payload.Applied || payload.Updated != 1 || len(payload.Files) != 1 {
		t.Fatalf("unexpected patch apply payload %#v", payload)
	}
	if controller.task.LastPatchApplyResult == nil || !controller.task.LastPatchApplyResult.Applied {
		t.Fatalf("expected controller task to store patch result, got %#v", controller.task.LastPatchApplyResult)
	}
}

func TestLocalControllerApplyProposedPatchFailureEmitsResultAndError(t *testing.T) {
	applier := &stubPatchApplier{err: errors.New("conflict: README.md")}
	controller := New(nil, nil, nil, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		LocalWorkspaceRoot: "/repo",
	})
	controller.patches = applier
	controller.patchInitErr = nil

	events, err := controller.ApplyProposedPatch(context.Background(), diffFixture("README.md", "before\n", "after\n"), PatchTargetLocalWorkspace)
	if err != nil {
		t.Fatalf("ApplyProposedPatch() error = %v", err)
	}
	if len(events) != 2 || events[0].Kind != EventPatchApplyResult || events[1].Kind != EventError {
		t.Fatalf("expected patch result + error, got %#v", events)
	}

	payload, _ := events[0].Payload.(PatchApplySummary)
	if payload.Applied || payload.Error == "" {
		t.Fatalf("expected failed patch result, got %#v", payload)
	}
}

func TestLocalControllerApprovePatchRunsPatchPath(t *testing.T) {
	applier := &stubPatchApplier{
		result: patchapply.Result{
			WorkspaceRoot: "/repo",
			Validation:    "native",
			Created:       1,
			Files: []patchapply.FileChange{
				{Operation: patchapply.OperationCreate, NewPath: "new.txt"},
			},
		},
	}
	controller := New(nil, nil, nil, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		LocalWorkspaceRoot: "/repo",
	})
	controller.patches = applier
	controller.patchInitErr = nil
	controller.task.PendingApproval = &ApprovalRequest{
		ID:    "approval-1",
		Kind:  ApprovalPatch,
		Title: "Apply patch",
		Patch: diffNewFileFixture("new.txt", "hello\n"),
	}

	events, err := controller.DecideApproval(context.Background(), "approval-1", DecisionApprove, "")
	if err != nil {
		t.Fatalf("DecideApproval() error = %v", err)
	}
	if len(applier.patches) != 1 {
		t.Fatalf("expected patch applier call, got %#v", applier.patches)
	}
	if len(events) != 1 || events[0].Kind != EventPatchApplyResult {
		t.Fatalf("expected patch apply result, got %#v", events)
	}
}

func TestLocalControllerContinueAfterPatchApply(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "Patch applied. Next I can run tests.",
		},
	}
	controller := New(agent, nil, nil, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		LocalWorkspaceRoot: "/repo",
	})
	controller.task.LastPatchApplyResult = &PatchApplySummary{
		WorkspaceRoot: "/repo",
		Applied:       true,
		Target:        PatchTargetLocalWorkspace,
		Updated:       1,
	}

	events, err := controller.ContinueAfterPatchApply(context.Background())
	if err != nil {
		t.Fatalf("ContinueAfterPatchApply() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventAgentMessage {
		t.Fatalf("expected agent message, got %#v", events)
	}
	if agent.lastInput.Prompt != continueAfterPatchApplyPrompt(PatchTargetLocalWorkspace, "") {
		t.Fatalf("expected patch continuation prompt, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, "Do not propose extra verification or follow-up edits") {
		t.Fatalf("expected stop-biased patch continuation prompt, got %q", agent.lastInput.Prompt)
	}
}

func TestLocalControllerContinueAfterFailedPatchApply(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "The patch failed to parse. I can retry with one corrected unified diff.",
		},
	}
	controller := New(agent, nil, nil, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		LocalWorkspaceRoot: "/repo",
	})
	controller.task.ActivePlan = &ActivePlan{
		Summary: "Repair hello.py.",
		Steps: []PlanStep{
			{Text: "Apply a patch.", Status: PlanStepInProgress},
			{Text: "Run the script.", Status: PlanStepPending},
		},
	}
	controller.task.LastPatchApplyResult = &PatchApplySummary{
		WorkspaceRoot: "/repo",
		Applied:       false,
		Target:        PatchTargetLocalWorkspace,
		Error:         "parse patch: gitdiff: invalid line operation",
	}

	events, err := controller.ContinueAfterPatchApply(context.Background())
	if err != nil {
		t.Fatalf("ContinueAfterPatchApply() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventAgentMessage {
		t.Fatalf("expected agent message, got %#v", events)
	}
	if agent.lastInput.Prompt != continueAfterPatchFailurePrompt(PatchTargetLocalWorkspace, "", "parse patch: gitdiff: invalid line operation", false) {
		t.Fatalf("expected failed patch continuation prompt, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, "Do not emit a shell command that invokes apply_patch") {
		t.Fatalf("expected shell patch tool prohibition, got %q", agent.lastInput.Prompt)
	}
	if controller.task.ActivePlan == nil || controller.task.ActivePlan.Steps[0].Status != PlanStepInProgress {
		t.Fatalf("expected active plan to remain unchanged after failed patch, got %#v", controller.task.ActivePlan)
	}
}

func TestLocalControllerContinueAfterRemotePatchConflictRequestsReinspection(t *testing.T) {
	agent := &stubAgent{
		response: AgentResponse{
			Message: "I should inspect the file before retrying the patch.",
			Proposal: &Proposal{
				Kind:        ProposalCommand,
				Command:     "sed -n '1,120p' foo.txt",
				Description: "Inspect the file before retrying.",
			},
		},
	}
	controller := New(agent, nil, nil, SessionContext{
		TrackedShell: TrackedShellTarget{PaneID: "%0"},
	})
	controller.task.LastPatchApplyResult = &PatchApplySummary{
		Applied:     false,
		Target:      PatchTargetRemoteShell,
		TargetLabel: "openclaw@openclaw ~ $",
		Error:       "apply foo.txt: conflict: fragment line does not match src line",
	}

	events, err := controller.ContinueAfterPatchApply(context.Background())
	if err != nil {
		t.Fatalf("ContinueAfterPatchApply() error = %v", err)
	}
	if len(events) != 2 || events[1].Kind != EventProposal {
		t.Fatalf("expected message plus inspection proposal, got %#v", events)
	}
	if !strings.Contains(agent.lastInput.Prompt, "Do not emit another patch yet.") {
		t.Fatalf("expected reinspection guidance, got %q", agent.lastInput.Prompt)
	}
	if !strings.Contains(agent.lastInput.Prompt, "propose exactly one safe read-only shell command") {
		t.Fatalf("expected read-only command guidance, got %q", agent.lastInput.Prompt)
	}
}

func TestParseRemoteReadPayloadConcatenatesWrappedBase64Lines(t *testing.T) {
	payload, err := parseRemoteReadPayload(strings.Join([]string{
		remoteReadMarker + " exists 420",
		remoteReadDataBegin,
		"aGVsbG8g",
		"cmVtb3Rl",
		"Cg==",
		remoteReadDataEnd,
	}, "\n"))
	if err != nil {
		t.Fatalf("parseRemoteReadPayload() error = %v", err)
	}
	if !payload.Exists || payload.Mode != 420 {
		t.Fatalf("unexpected payload metadata %#v", payload)
	}
	if payload.Data != "aGVsbG8gcmVtb3RlCg==" {
		t.Fatalf("unexpected concatenated data %q", payload.Data)
	}
}

func TestParseRemotePatchFilesRejectsTruncatedFragment(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/foo.txt b/foo.txt",
		"--- a/foo.txt",
		"+++ b/foo.txt",
		"@@ -1 +1,4 @@",
		" hello",
		"+there was a young coder from leeds",
	}, "\n")

	if _, err := parseRemotePatchFiles(patch); err == nil || (!strings.Contains(err.Error(), "validate fragment") && !strings.Contains(err.Error(), "fragment header miscounts lines")) {
		t.Fatalf("expected truncated fragment validation error, got %v", err)
	}
}

func TestLocalControllerApplyRemotePatchWithPythonCapability(t *testing.T) {
	exists := false
	written := base64.StdEncoding.EncodeToString([]byte("hello remote\n"))
	runner := &stubRunner{
		run: func(_ context.Context, _ string, command string, _ time.Duration) (shell.TrackedExecution, error) {
			if handled, result, err := handleRemotePayloadStagingCommand(command); handled {
				return result, err
			}
			switch {
			case strings.Contains(command, "printf 'git=%s"):
				return shell.TrackedExecution{Captured: "git=0\npython3=1\nbase64=1\nmktemp=1\nshell=bash\nsystem=Linux x86_64\nos_release=ubuntu 24.04\n"}, nil
			case strings.Contains(command, "tempfile.mkstemp(prefix='.shuttle-patch-'"):
				exists = true
				return shell.TrackedExecution{Captured: ""}, nil
			case strings.Contains(command, "path = \"hello.txt\""):
				if exists {
					return shell.TrackedExecution{Captured: remoteReadMarker + " exists 420\n" + remoteReadDataBegin + "\n" + written + "\n" + remoteReadDataEnd + "\n"}, nil
				}
				return shell.TrackedExecution{Captured: remoteReadMarker + " missing\n"}, nil
			default:
				return shell.TrackedExecution{}, fmt.Errorf("unexpected remote patch command: %s", command)
			}
		},
	}
	reader := &stubContextReader{
		context: shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "~",
			PromptSymbol: "$",
			Remote:       true,
		},
	}
	controller := New(nil, runner, reader, SessionContext{
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%9"},
		StateDir:     t.TempDir(),
	})

	events, err := controller.ApplyProposedPatch(context.Background(), diffNewFileFixture("hello.txt", "hello remote\n"), PatchTargetRemoteShell)
	if err != nil {
		t.Fatalf("ApplyProposedPatch() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventPatchApplyResult {
		t.Fatalf("expected patch apply result event, got %#v", events)
	}

	payload, ok := events[0].Payload.(PatchApplySummary)
	if !ok {
		t.Fatalf("expected patch apply payload, got %#v", events[0].Payload)
	}
	if !payload.Applied || payload.Target != PatchTargetRemoteShell || payload.Created != 1 {
		t.Fatalf("unexpected remote patch payload %#v", payload)
	}
	if payload.Transport != PatchTransportPython {
		t.Fatalf("expected python transport, got %#v", payload)
	}
	if payload.CapabilitySource != "probed" {
		t.Fatalf("expected probed capability source, got %#v", payload)
	}
	if payload.TargetLabel == "" {
		t.Fatalf("expected remote target label, got %#v", payload)
	}
	if controller.task.LastPatchApplyResult == nil || controller.task.LastPatchApplyResult.Target != PatchTargetRemoteShell {
		t.Fatalf("expected controller task to store remote patch result, got %#v", controller.task.LastPatchApplyResult)
	}
}

func TestLocalControllerApplyRemotePatchFailsWhenCurrentShellNotRemote(t *testing.T) {
	controller := New(nil, &stubRunner{}, &stubContextReader{
		context: shell.PromptContext{
			User:         "localuser",
			Host:         "workstation",
			Directory:    "~/repo",
			PromptSymbol: "%",
		},
	}, SessionContext{
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%0"},
	})

	events, err := controller.ApplyProposedPatch(context.Background(), diffNewFileFixture("hello.txt", "hello\n"), PatchTargetRemoteShell)
	if err != nil {
		t.Fatalf("ApplyProposedPatch() error = %v", err)
	}
	if len(events) != 2 || events[0].Kind != EventPatchApplyResult || events[1].Kind != EventError {
		t.Fatalf("expected patch result + error, got %#v", events)
	}

	payload, _ := events[0].Payload.(PatchApplySummary)
	if payload.Applied || payload.Target != PatchTargetRemoteShell {
		t.Fatalf("expected failed remote patch result, got %#v", payload)
	}
	if !strings.Contains(payload.Error, "ambiguous or not currently active") {
		t.Fatalf("expected remote ambiguity error, got %#v", payload)
	}
}

func TestLocalControllerApplyRemotePatchFallsBackToShellTransport(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("before\n"))
	updated := base64.StdEncoding.EncodeToString([]byte("after\n"))
	current := encoded
	runner := &stubRunner{
		run: func(_ context.Context, _ string, command string, _ time.Duration) (shell.TrackedExecution, error) {
			if handled, result, err := handleRemotePayloadStagingCommand(command); handled {
				return result, err
			}
			switch {
			case strings.Contains(command, "printf 'git=%s"):
				return shell.TrackedExecution{Captured: "git=0\npython3=0\nbase64=1\nmktemp=1\nshell=bash\nsystem=Linux x86_64\nos_release=ubuntu 24.04\n"}, nil
			case strings.Contains(command, "base64 < 'README.md'"):
				return shell.TrackedExecution{Captured: remoteReadMarker + " exists 420\n" + remoteReadDataBegin + "\n" + current + "\n" + remoteReadDataEnd + "\n"}, nil
			case strings.Contains(command, "base64 -d \"$payload\" > \"$tmp\""):
				current = updated
				return shell.TrackedExecution{Captured: ""}, nil
			default:
				return shell.TrackedExecution{}, fmt.Errorf("unexpected shell fallback command: %s", command)
			}
		},
	}
	reader := &stubContextReader{
		context: shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "~",
			PromptSymbol: "$",
			Remote:       true,
		},
	}
	controller := New(nil, runner, reader, SessionContext{
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%9"},
		StateDir:     t.TempDir(),
	})
	patch := strings.Join([]string{
		"diff --git a/README.md b/README.md",
		"--- a/README.md",
		"+++ b/README.md",
		"@@ -1 +1 @@",
		"-before",
		"+after",
	}, "\n")

	events, err := controller.ApplyProposedPatch(context.Background(), patch, PatchTargetRemoteShell)
	if err != nil {
		t.Fatalf("ApplyProposedPatch() error = %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventPatchApplyResult {
		t.Fatalf("expected patch apply result event, got %#v", events)
	}

	payload, _ := events[0].Payload.(PatchApplySummary)
	if !payload.Applied || payload.Target != PatchTargetRemoteShell || payload.Updated != 1 {
		t.Fatalf("unexpected shell fallback patch payload %#v", payload)
	}
	if payload.Transport != PatchTransportShell {
		t.Fatalf("expected shell transport, got %#v", payload)
	}
}

func TestLocalControllerApplyRemotePatchResetsPatchRepairCountOnSuccess(t *testing.T) {
	exists := false
	written := base64.StdEncoding.EncodeToString([]byte("hello remote\n"))
	runner := &stubRunner{
		run: func(_ context.Context, _ string, command string, _ time.Duration) (shell.TrackedExecution, error) {
			if handled, result, err := handleRemotePayloadStagingCommand(command); handled {
				return result, err
			}
			switch {
			case strings.Contains(command, "printf 'git=%s"):
				return shell.TrackedExecution{Captured: "git=0\npython3=1\nbase64=1\nmktemp=1\nshell=bash\nsystem=Linux x86_64\nos_release=ubuntu 24.04\n"}, nil
			case strings.Contains(command, "tempfile.mkstemp(prefix='.shuttle-patch-'"):
				exists = true
				return shell.TrackedExecution{Captured: ""}, nil
			case strings.Contains(command, "path = \"hello.txt\""):
				if exists {
					return shell.TrackedExecution{Captured: remoteReadMarker + " exists 420\n" + remoteReadDataBegin + "\n" + written + "\n" + remoteReadDataEnd + "\n"}, nil
				}
				return shell.TrackedExecution{Captured: remoteReadMarker + " missing\n"}, nil
			default:
				return shell.TrackedExecution{}, fmt.Errorf("unexpected remote patch command: %s", command)
			}
		},
	}
	controller := New(nil, runner, &stubContextReader{
		context: shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "~",
			PromptSymbol: "$",
			Remote:       true,
		},
	}, SessionContext{
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%9"},
		StateDir:     t.TempDir(),
	})
	controller.task.PatchRepairCount = 1

	if _, err := controller.ApplyProposedPatch(context.Background(), diffNewFileFixture("hello.txt", "hello remote\n"), PatchTargetRemoteShell); err != nil {
		t.Fatalf("ApplyProposedPatch() error = %v", err)
	}
	if controller.task.PatchRepairCount != 0 {
		t.Fatalf("expected remote patch success to reset repair count, got %d", controller.task.PatchRepairCount)
	}
}

func TestLocalControllerApplyRemotePatchUsesGitTransportWhenAvailable(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("before\n"))
	updated := base64.StdEncoding.EncodeToString([]byte("after\n"))
	current := encoded
	runner := &stubRunner{
		run: func(_ context.Context, _ string, command string, _ time.Duration) (shell.TrackedExecution, error) {
			if handled, result, err := handleRemotePayloadStagingCommand(command); handled {
				return result, err
			}
			switch {
			case strings.Contains(command, "printf 'git=%s"):
				return shell.TrackedExecution{Captured: "git=1\npython3=1\nbase64=1\nmktemp=1\nshell=bash\nsystem=Linux x86_64\nos_release=ubuntu 24.04\n"}, nil
			case strings.Contains(command, "path = \"README.md\""):
				return shell.TrackedExecution{Captured: remoteReadMarker + " exists 420\n" + remoteReadDataBegin + "\n" + current + "\n" + remoteReadDataEnd + "\n"}, nil
			case strings.Contains(command, "git rev-parse --is-inside-work-tree"):
				return shell.TrackedExecution{ExitCode: 0}, nil
			case strings.Contains(command, "git apply --check --verbose "):
				return shell.TrackedExecution{ExitCode: 0}, nil
			case strings.Contains(command, "git apply --whitespace=nowarn "):
				current = updated
				return shell.TrackedExecution{ExitCode: 0}, nil
			default:
				return shell.TrackedExecution{}, fmt.Errorf("unexpected remote patch command: %s", command)
			}
		},
	}
	controller := New(nil, runner, &stubContextReader{
		context: shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "~",
			PromptSymbol: "$",
			Remote:       true,
		},
	}, SessionContext{
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%9"},
		StateDir:     t.TempDir(),
	})
	patch := strings.Join([]string{
		"diff --git a/README.md b/README.md",
		"--- a/README.md",
		"+++ b/README.md",
		"@@ -1 +1 @@",
		"-before",
		"+after",
	}, "\n")

	events, err := controller.ApplyProposedPatch(context.Background(), patch, PatchTargetRemoteShell)
	if err != nil {
		t.Fatalf("ApplyProposedPatch() error = %v", err)
	}
	payload, _ := events[0].Payload.(PatchApplySummary)
	if payload.Transport != PatchTransportGit {
		t.Fatalf("expected git transport, got %#v", payload)
	}
}

func TestLocalControllerApplyRemotePatchUsesCachedCapabilities(t *testing.T) {
	stateDir := t.TempDir()
	exists := false
	written := base64.StdEncoding.EncodeToString([]byte("hello remote\n"))
	runner := &stubRunner{
		run: func(_ context.Context, _ string, command string, _ time.Duration) (shell.TrackedExecution, error) {
			if handled, result, err := handleRemotePayloadStagingCommand(command); handled {
				return result, err
			}
			switch {
			case strings.Contains(command, "git rev-parse --is-inside-work-tree"):
				return shell.TrackedExecution{ExitCode: 1}, nil
			case strings.Contains(command, "tempfile.mkstemp"):
				exists = true
				return shell.TrackedExecution{Captured: ""}, nil
			case strings.Contains(command, "path = \"hello.txt\""):
				if exists {
					return shell.TrackedExecution{Captured: remoteReadMarker + " exists 420\n" + remoteReadDataBegin + "\n" + written + "\n" + remoteReadDataEnd + "\n"}, nil
				}
				return shell.TrackedExecution{Captured: remoteReadMarker + " missing\n"}, nil
			default:
				return shell.TrackedExecution{}, fmt.Errorf("unexpected command with cached capabilities: %s", command)
			}
		},
	}
	controller := New(nil, runner, &stubContextReader{
		context: shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "~",
			PromptSymbol: "$",
			Remote:       true,
		},
	}, SessionContext{
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%9"},
		StateDir:     stateDir,
	})
	controller.remoteCaps.saveRecord(remoteCapabilityRecord{
		Key:                "openclaw@openclaw",
		User:               "openclaw",
		Host:               "openclaw",
		TargetKind:         "remote_shell",
		Git:                true,
		Python3:            true,
		Base64:             true,
		Mktemp:             true,
		LastProbeSucceeded: true,
		LastValidated:      time.Now().UTC(),
	})

	events, err := controller.ApplyProposedPatch(context.Background(), diffNewFileFixture("hello.txt", "hello remote\n"), PatchTargetRemoteShell)
	if err != nil {
		t.Fatalf("ApplyProposedPatch() error = %v", err)
	}
	payload, _ := events[0].Payload.(PatchApplySummary)
	if payload.CapabilitySource != "cached" {
		t.Fatalf("expected cached capability source, got %#v", payload)
	}
	for _, command := range runner.commands {
		if strings.Contains(command, "printf 'git=%s") {
			t.Fatalf("did not expect a capability probe command, got %#v", runner.commands)
		}
	}
}

func TestLocalControllerApplyRemotePatchReprobesNegativeCachedCapabilities(t *testing.T) {
	exists := false
	written := base64.StdEncoding.EncodeToString([]byte("hello remote\n"))
	runner := &stubRunner{
		run: func(_ context.Context, _ string, command string, _ time.Duration) (shell.TrackedExecution, error) {
			if handled, result, err := handleRemotePayloadStagingCommand(command); handled {
				return result, err
			}
			switch {
			case strings.Contains(command, "printf 'git=%s"):
				return shell.TrackedExecution{Captured: "git=0\npython3=1\nbase64=1\nmktemp=1\nshell=bash\nsystem=Linux x86_64\nos_release=ubuntu 24.04\n"}, nil
			case strings.Contains(command, "tempfile.mkstemp(prefix='.shuttle-patch-'"):
				exists = true
				return shell.TrackedExecution{Captured: ""}, nil
			case strings.Contains(command, "path = \"hello.txt\""):
				if exists {
					return shell.TrackedExecution{Captured: remoteReadMarker + " exists 420\n" + remoteReadDataBegin + "\n" + written + "\n" + remoteReadDataEnd + "\n"}, nil
				}
				return shell.TrackedExecution{Captured: remoteReadMarker + " missing\n"}, nil
			default:
				return shell.TrackedExecution{}, fmt.Errorf("unexpected re-probe command: %s", command)
			}
		},
	}
	controller := New(nil, runner, &stubContextReader{
		context: shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "~",
			PromptSymbol: "$",
			Remote:       true,
		},
	}, SessionContext{
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%9"},
		StateDir:     t.TempDir(),
	})
	controller.remoteCaps.saveRecord(remoteCapabilityRecord{
		Key:                "openclaw@openclaw",
		User:               "openclaw",
		Host:               "openclaw",
		TargetKind:         "remote_shell",
		Git:                false,
		Python3:            false,
		Base64:             true,
		Mktemp:             true,
		LastProbeSucceeded: true,
		LastValidated:      time.Now().UTC(),
	})

	events, err := controller.ApplyProposedPatch(context.Background(), diffNewFileFixture("hello.txt", "hello remote\n"), PatchTargetRemoteShell)
	if err != nil {
		t.Fatalf("ApplyProposedPatch() error = %v", err)
	}
	payload, _ := events[0].Payload.(PatchApplySummary)
	if !payload.Applied || payload.Transport != PatchTransportPython {
		t.Fatalf("expected re-probed python transport, got %#v", payload)
	}
	if payload.CapabilitySource != "reprobed" {
		t.Fatalf("expected reprobed capability source, got %#v", payload)
	}
	if countCommandsContaining(runner.commands, "printf 'git=%s") != 1 {
		t.Fatalf("expected exactly one capability re-probe, got %#v", runner.commands)
	}
}

func TestLocalControllerApplyRemotePatchFailsModeVerification(t *testing.T) {
	exists := false
	written := base64.StdEncoding.EncodeToString([]byte("hello remote\n"))
	runner := &stubRunner{
		run: func(_ context.Context, _ string, command string, _ time.Duration) (shell.TrackedExecution, error) {
			if handled, result, err := handleRemotePayloadStagingCommand(command); handled {
				return result, err
			}
			switch {
			case strings.Contains(command, "printf 'git=%s"):
				return shell.TrackedExecution{Captured: "git=0\npython3=1\nbase64=1\nmktemp=1\nshell=bash\nsystem=Linux x86_64\nos_release=ubuntu 24.04\n"}, nil
			case strings.Contains(command, "tempfile.mkstemp(prefix='.shuttle-patch-'"):
				exists = true
				return shell.TrackedExecution{Captured: ""}, nil
			case strings.Contains(command, "path = \"hello.txt\""):
				if exists {
					return shell.TrackedExecution{Captured: remoteReadMarker + " exists 384\n" + remoteReadDataBegin + "\n" + written + "\n" + remoteReadDataEnd + "\n"}, nil
				}
				return shell.TrackedExecution{Captured: remoteReadMarker + " missing\n"}, nil
			default:
				return shell.TrackedExecution{}, fmt.Errorf("unexpected mode verification command: %s", command)
			}
		},
	}
	controller := New(nil, runner, &stubContextReader{
		context: shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "~",
			PromptSymbol: "$",
			Remote:       true,
		},
	}, SessionContext{
		TrackedShell: TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%9"},
		StateDir:     t.TempDir(),
	})

	events, err := controller.ApplyProposedPatch(context.Background(), diffNewFileFixture("hello.txt", "hello remote\n"), PatchTargetRemoteShell)
	if err != nil {
		t.Fatalf("ApplyProposedPatch() error = %v", err)
	}
	if len(events) != 2 || events[0].Kind != EventPatchApplyResult || events[1].Kind != EventError {
		t.Fatalf("expected failed patch result + error, got %#v", events)
	}
	payload, _ := events[0].Payload.(PatchApplySummary)
	if payload.Applied || !strings.Contains(payload.Error, "file mode") {
		t.Fatalf("expected mode verification failure, got %#v", payload)
	}
}

func handleRemotePayloadStagingCommand(command string) (bool, shell.TrackedExecution, error) {
	switch {
	case strings.Contains(command, "tempfile.mkstemp(prefix='.shuttle-remote-'"):
		suffix := ".tmp"
		if strings.Contains(command, "suffix=\".diff.b64\"") {
			suffix = ".diff.b64"
		} else if strings.Contains(command, "suffix=\".diff\"") {
			suffix = ".diff"
		} else if strings.Contains(command, "suffix=\".b64\"") {
			suffix = ".b64"
		}
		return true, shell.TrackedExecution{Captured: "/tmp/shuttle-remote" + suffix + "\n"}, nil
	case strings.HasPrefix(command, "mktemp "):
		suffix := ".tmp"
		if strings.Contains(command, ".b64") {
			suffix = ".b64"
		} else if strings.Contains(command, ".diff") {
			suffix = ".diff"
		}
		return true, shell.TrackedExecution{Captured: "/tmp/shuttle-remote" + suffix + "\n"}, nil
	case strings.HasPrefix(command, ": > "),
		strings.Contains(command, " >> "),
		strings.Contains(command, "verify staged payload"),
		strings.HasPrefix(command, "test \"$(wc -c < "),
		strings.HasPrefix(command, "rm -f "):
		return true, shell.TrackedExecution{}, nil
	default:
		return false, shell.TrackedExecution{}, nil
	}
}

func countCommandsContaining(commands []string, needle string) int {
	count := 0
	for _, command := range commands {
		if strings.Contains(command, needle) {
			count++
		}
	}
	return count
}

func diffFixture(path string, oldBody string, newBody string) string {
	return "--- a/" + path + "\n" +
		"+++ b/" + path + "\n" +
		"@@ -1 +1 @@\n" +
		"-" + trimTrailingNewline(oldBody) + "\n" +
		"+" + trimTrailingNewline(newBody) + "\n"
}

func diffNewFileFixture(path string, body string) string {
	return "diff --git a/" + path + " b/" + path + "\n" +
		"new file mode 100644\n" +
		"--- /dev/null\n" +
		"+++ b/" + path + "\n" +
		"@@ -0,0 +1 @@\n" +
		"+" + trimTrailingNewline(body) + "\n"
}

func trimTrailingNewline(value string) string {
	for len(value) > 0 && value[len(value)-1] == '\n' {
		value = value[:len(value)-1]
	}
	return value
}

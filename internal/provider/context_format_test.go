package provider

import (
	"strings"
	"testing"
	"time"

	"aiterm/internal/controller"
	"aiterm/internal/shell"
)

func TestCompactShellOutputCompactsHeadAndTail(t *testing.T) {
	lines := make([]string, 0, 12)
	for index := 1; index <= 12; index++ {
		lines = append(lines, "line "+string(rune('A'+index-1)))
	}

	got := compactShellOutput(strings.Join(lines, "\n"), 2, 2, 200)

	if !strings.Contains(got, "line A\nline B") {
		t.Fatalf("expected head lines to be preserved, got %q", got)
	}
	if !strings.Contains(got, "...(8 more lines omitted)...") {
		t.Fatalf("expected omission marker, got %q", got)
	}
	if !strings.Contains(got, "line K\nline L") {
		t.Fatalf("expected tail lines to be preserved, got %q", got)
	}
}

func TestBuildTurnContextDeduplicatesRepeatedShellOutput(t *testing.T) {
	lines := make([]string, 0, 20)
	for index := 1; index <= 20; index++ {
		lines = append(lines, "line "+string(rune('a'+(index-1)%26)))
	}
	shared := strings.Join(lines, "\n")

	context := buildTurnContext(controller.AgentInput{
		Prompt: "summarize the last command",
		Session: controller.SessionContext{
			RecentShellOutput: shared,
			TrackedShell: controller.TrackedShellTarget{
				SessionName: "shuttle-test",
				PaneID:      "%9",
			},
		},
		Task: controller.TaskContext{
			LastCommandResult: &controller.CommandResultSummary{
				Command:        "rg -n foo ~",
				State:          controller.CommandExecutionCompleted,
				Cause:          "end_marker",
				Confidence:     "strong",
				SemanticShell:  true,
				SemanticSource: "osc_capture",
				ExitCode:       0,
				Summary:        shared,
			},
		},
	})

	if !strings.Contains(context, "Recent shell output:\n") {
		t.Fatalf("expected recent shell output section, got %q", context)
	}
	if !strings.Contains(context, "tracked_session=shuttle-test") || !strings.Contains(context, "tracked_pane=%9") {
		t.Fatalf("expected tracked shell metadata, got %q", context)
	}
	if !strings.Contains(context, "Last command result:\n") {
		t.Fatalf("expected last command result section, got %q", context)
	}
	if !strings.Contains(context, "cause=end_marker") || !strings.Contains(context, "confidence=strong") {
		t.Fatalf("expected cause/confidence metadata, got %q", context)
	}
	if !strings.Contains(context, "semantic_shell=true") || !strings.Contains(context, "semantic_source=osc_capture") {
		t.Fatalf("expected semantic metadata, got %q", context)
	}
	if strings.Contains(context, "\nsummary=") {
		t.Fatalf("expected duplicate summary to be omitted, got %q", context)
	}
}

func TestBuildTurnContextIncludesRecoverySnapshot(t *testing.T) {
	context := buildTurnContext(controller.AgentInput{
		Prompt: "figure out what happened",
		Task: controller.TaskContext{
			CurrentExecution: &controller.CommandExecution{
				ID:      "cmd-1",
				Command: "nano goo.txt",
				State:   controller.CommandExecutionInteractiveFullscreen,
			},
			RecoverySnapshot: "line 1\nline 2\nline 3",
		},
	})

	if !strings.Contains(context, "Recovery terminal snapshot:\nline 1\nline 2\nline 3") {
		t.Fatalf("expected recovery snapshot section, got %q", context)
	}
}

func TestBuildTurnContextIncludesExecutionMetadata(t *testing.T) {
	before := shell.PromptContext{
		User:         "localuser",
		Host:         "workstation",
		Directory:    "~/repo",
		PromptSymbol: "%",
	}
	after := shell.PromptContext{
		User:         "openclaw",
		Host:         "openclaw",
		Directory:    "~",
		PromptSymbol: "$",
		Remote:       true,
	}
	context := buildTurnContext(controller.AgentInput{
		Prompt: "what is going on",
		Task: controller.TaskContext{
			PrimaryExecutionID: "cmd-1",
			ExecutionRegistry: []controller.CommandExecution{
				{ID: "cmd-1", Command: "ssh openclaw@openclaw"},
				{ID: "cmd-2", Command: "tail -f /var/log/syslog"},
			},
			CurrentExecution: &controller.CommandExecution{
				ID:                 "cmd-1",
				Command:            "ssh openclaw@openclaw",
				State:              controller.CommandExecutionAwaitingInput,
				TrackedShell:       controller.TrackedShellTarget{SessionName: "shuttle-test", PaneID: "%9"},
				ForegroundCommand:  "ssh",
				SemanticShell:      true,
				SemanticSource:     "state_file",
				StartedAt:          time.Now().Add(-12 * time.Second),
				ShellContextBefore: &before,
				ShellContextAfter:  &after,
			},
		},
	})

	if !strings.Contains(context, "foreground_command=ssh") {
		t.Fatalf("expected foreground command metadata, got %q", context)
	}
	if !strings.Contains(context, "Execution registry:\nprimary_execution=cmd-1\nactive_execution_count=2") {
		t.Fatalf("expected execution registry metadata, got %q", context)
	}
	if !strings.Contains(context, "semantic_shell=true") || !strings.Contains(context, "semantic_source=state_file") {
		t.Fatalf("expected semantic shell metadata, got %q", context)
	}
	if !strings.Contains(context, "elapsed_seconds=") {
		t.Fatalf("expected elapsed_seconds metadata, got %q", context)
	}
	if !strings.Contains(context, "execution_session=shuttle-test") || !strings.Contains(context, "execution_pane=%9") {
		t.Fatalf("expected execution target metadata, got %q", context)
	}
	if !strings.Contains(context, "prompt_before=localuser@workstation ~/repo %") {
		t.Fatalf("expected prompt_before metadata, got %q", context)
	}
	if !strings.Contains(context, "prompt_after=openclaw@openclaw ~ $") {
		t.Fatalf("expected prompt_after metadata, got %q", context)
	}
}

func TestBuildTurnContextIncludesRecentManualShellContext(t *testing.T) {
	context := buildTurnContext(controller.AgentInput{
		Prompt: "what changed?",
		Session: controller.SessionContext{
			TrackedShell: controller.TrackedShellTarget{
				SessionName: "shuttle-test",
				PaneID:      "%0",
			},
			RecentManualCommands: []string{
				"mv foo.md foo_new.md",
				"touch chicken.mmd",
			},
			RecentManualActions: []string{
				"renamed foo.md -> foo_new.md",
				"touched chicken.mmd",
			},
		},
	})

	if !strings.Contains(context, "Recent manual shell commands:\nmv foo.md foo_new.md\ntouch chicken.mmd") {
		t.Fatalf("expected recent manual command section, got %q", context)
	}
	if !strings.Contains(context, "Recent manual shell actions:\nrenamed foo.md -> foo_new.md\ntouched chicken.mmd") {
		t.Fatalf("expected recent manual action section, got %q", context)
	}
}

func TestBuildTurnContextIncludesWorkspaceRootAndPatchResult(t *testing.T) {
	context := buildTurnContext(controller.AgentInput{
		Prompt: "what changed?",
		Session: controller.SessionContext{
			WorkingDirectory:   "/tmp/remote",
			LocalWorkspaceRoot: "/workspace/project",
			ApprovalMode:       controller.ApprovalModeAuto,
			CurrentShell: &shell.PromptContext{
				User:         "openclaw",
				Host:         "openclaw",
				Directory:    "/tmp/remote",
				PromptSymbol: "$",
				RawLine:      "openclaw@openclaw /tmp/remote $",
				Remote:       true,
			},
			RemoteCapabilities: &controller.RemoteCapabilitySummary{
				Identity:                "openclaw@openclaw",
				Source:                  "cached",
				LastSuccessfulTransport: controller.PatchTransportPython,
				Git:                     true,
				Python3:                 true,
				Base64:                  true,
				Mktemp:                  true,
			},
		},
		Task: controller.TaskContext{
			LastPatchApplyResult: &controller.PatchApplySummary{
				WorkspaceRoot:    "/workspace/project",
				Validation:       "native+git_apply_check",
				Applied:          true,
				Transport:        controller.PatchTransportGit,
				CapabilitySource: "cached",
				Created:          1,
				Updated:          2,
				Files: []controller.PatchApplyFile{
					{Operation: "create", NewPath: "new.txt"},
					{Operation: "update", NewPath: "README.md"},
				},
			},
		},
	})

	if !strings.Contains(context, "local_workspace_root=/workspace/project") {
		t.Fatalf("expected local workspace root label, got %q", context)
	}
	if !strings.Contains(context, "approval_mode=auto") {
		t.Fatalf("expected approval mode, got %q", context)
	}
	if !strings.Contains(context, "shell_location=remote") {
		t.Fatalf("expected shell location, got %q", context)
	}
	if !strings.Contains(context, "cwd_source=prompt") || !strings.Contains(context, "cwd_confidence=medium") || !strings.Contains(context, "cwd_authoritative=false") {
		t.Fatalf("expected cwd authority metadata, got %q", context)
	}
	if !strings.Contains(context, "remote_patch_root=/tmp/remote") {
		t.Fatalf("expected remote patch root, got %q", context)
	}
	if !strings.Contains(context, "remote_cwd=/tmp/remote") {
		t.Fatalf("expected remote cwd, got %q", context)
	}
	if strings.Contains(context, "\ncwd=/tmp/remote") {
		t.Fatalf("did not expect ambiguous cwd label for remote shell, got %q", context)
	}
	if !strings.Contains(context, "local_workspace_note=local workspace paths are not remote shell paths") {
		t.Fatalf("expected explicit local workspace note, got %q", context)
	}
	if !strings.Contains(context, "remote_capabilities=git,python3,base64,mktemp") {
		t.Fatalf("expected remote capability summary, got %q", context)
	}
	if !strings.Contains(context, "remote_last_patch_transport=python3") {
		t.Fatalf("expected last successful transport, got %q", context)
	}
	if !strings.Contains(context, "Last patch apply result:\n") {
		t.Fatalf("expected last patch apply section, got %q", context)
	}
	if !strings.Contains(context, "validation=native+git_apply_check") {
		t.Fatalf("expected patch validation, got %q", context)
	}
	if !strings.Contains(context, "transport=git") || !strings.Contains(context, "capability_source=cached") {
		t.Fatalf("expected patch transport metadata, got %q", context)
	}
}

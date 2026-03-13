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
		},
		Task: controller.TaskContext{
			LastCommandResult: &controller.CommandResultSummary{
				Command:    "rg -n foo ~",
				State:      controller.CommandExecutionCompleted,
				Cause:      "end_marker",
				Confidence: "strong",
				ExitCode:   0,
				Summary:    shared,
			},
		},
	})

	if !strings.Contains(context, "Recent shell output:\n") {
		t.Fatalf("expected recent shell output section, got %q", context)
	}
	if !strings.Contains(context, "Last command result:\n") {
		t.Fatalf("expected last command result section, got %q", context)
	}
	if !strings.Contains(context, "cause=end_marker") || !strings.Contains(context, "confidence=strong") {
		t.Fatalf("expected cause/confidence metadata, got %q", context)
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
		User:         "jsmith",
		Host:         "linuxdesktop",
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
			CurrentExecution: &controller.CommandExecution{
				ID:                 "cmd-1",
				Command:            "ssh openclaw@openclaw",
				State:              controller.CommandExecutionAwaitingInput,
				ForegroundCommand:  "ssh",
				StartedAt:          time.Now().Add(-12 * time.Second),
				ShellContextBefore: &before,
				ShellContextAfter:  &after,
			},
		},
	})

	if !strings.Contains(context, "foreground_command=ssh") {
		t.Fatalf("expected foreground command metadata, got %q", context)
	}
	if !strings.Contains(context, "elapsed_seconds=") {
		t.Fatalf("expected elapsed_seconds metadata, got %q", context)
	}
	if !strings.Contains(context, "prompt_before=jsmith@linuxdesktop ~/repo %") {
		t.Fatalf("expected prompt_before metadata, got %q", context)
	}
	if !strings.Contains(context, "prompt_after=openclaw@openclaw ~ $") {
		t.Fatalf("expected prompt_after metadata, got %q", context)
	}
}

package controller

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"aiterm/internal/shell"
)

func benchmarkTranscriptEvents(count int) []TranscriptEvent {
	events := make([]TranscriptEvent, 0, count)
	for index := 0; index < count; index++ {
		events = append(events, TranscriptEvent{
			ID:        fmt.Sprintf("event-%03d", index),
			Kind:      EventAgentMessage,
			Timestamp: time.Unix(int64(index), 0),
			Payload: TextPayload{
				Text: fmt.Sprintf("Agent update %03d: investigated shell state, compared outputs, and continued the task.", index),
			},
		})
	}
	return events
}

func benchmarkAgentInput() AgentInput {
	recentShellLines := make([]string, 0, 80)
	for index := 0; index < 80; index++ {
		recentShellLines = append(recentShellLines, fmt.Sprintf("shell output line %03d: /Users/jsmith/source/shuttle synthetic payload for context estimate benchmarks", index))
	}
	recoveryLines := make([]string, 0, 120)
	for index := 0; index < 120; index++ {
		recoveryLines = append(recoveryLines, fmt.Sprintf("recovery snapshot line %03d with prompt, cwd, and output hints", index))
	}
	completedAt := time.Now().Add(-2 * time.Second)

	return AgentInput{
		Session: SessionContext{
			SessionName:           "shuttle-bench",
			WorkingDirectory:      "/Users/jsmith/source/shuttle",
			LocalWorkingDirectory: "/Users/jsmith/source/shuttle",
			LocalHomeDirectory:    "/Users/jsmith",
			LocalUsername:         "jsmith",
			LocalHostname:         "workstation",
			LocalWorkspaceRoot:    "/Users/jsmith/source/shuttle",
			RecentShellOutput:     strings.Join(recentShellLines, "\n"),
			RecentManualCommands: []string{
				"git status",
				"go test ./internal/tui -count=1",
				"rg -n \"renderStatusLine\" internal/tui",
			},
			RecentManualActions: []string{
				"Opened the TUI and typed in the composer.",
				"Compared response times on macOS and Linux.",
			},
			CurrentShell: &shell.PromptContext{
				User:      "jsmith",
				Host:      "workstation",
				Directory: "/Users/jsmith/source/shuttle",
				RawLine:   "jsmith@workstation /Users/jsmith/source/shuttle %",
			},
		},
		Task: TaskContext{
			TaskID:            "task-bench",
			CompactedSummary:  "Investigating a severe composer responsiveness regression in Shuttle on macOS.",
			PriorTranscript:   benchmarkTranscriptEvents(120),
			RecoverySnapshot:  strings.Join(recoveryLines, "\n"),
			LastCommandResult: &CommandResultSummary{Command: "go test ./internal/tui -count=1", Summary: "Tests passed with synthetic benchmark fixtures and no compile errors.", State: CommandExecutionCompleted, ExitCode: 0},
			ActivePlan: &ActivePlan{
				Summary: "Find the hot path behind per-keystroke lag.",
				Steps: []PlanStep{
					{Text: "Measure shell completion cost", Status: PlanStepDone},
					{Text: "Measure render and status-line cost", Status: PlanStepInProgress},
					{Text: "Implement the winning fix", Status: PlanStepPending},
				},
			},
			CurrentExecution: &CommandExecution{
				ID:               "exec-bench",
				Command:          "go test ./internal/tui -count=1",
				State:            CommandExecutionCompleted,
				Origin:           CommandOriginUserShell,
				StartedAt:        time.Now().Add(-3 * time.Second),
				CompletedAt:      &completedAt,
				LatestOutputTail: strings.Join(recentShellLines[:20], "\n"),
			},
		},
		Prompt: "please explain why typing in the composer is slow",
	}
}

func BenchmarkEstimateContextUsage(b *testing.B) {
	input := benchmarkAgentInput()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = estimateContextUsage(input)
	}
}

package shell

import "testing"

func TestEvaluateSemanticPromptReturnUsesFallbackTail(t *testing.T) {
	exitCode := 0
	evaluation, complete := evaluateSemanticPromptReturn(promptReturnInputs{
		Command: "sleep",
		Observed: ObservedShellState{
			HasSemanticState: true,
			SemanticState: semanticShellState{
				Event:    semanticEventPrompt,
				ExitCode: &exitCode,
			},
			SemanticSource: "osc_stream",
		},
		Snapshot: MonitorSnapshot{
			ShellContext: PromptContext{
				User:         "root",
				Host:         "web01",
				Directory:    "/srv/app",
				PromptSymbol: "#",
				RawLine:      "root@web01 /srv/app #",
			},
			LatestOutputTail: "password accepted",
		},
		RawBody:        "",
		BodyCleaner:    func(body string, promptContext PromptContext) string { return body },
		FallbackBody:   func(snapshot MonitorSnapshot) string { return snapshot.LatestOutputTail },
		SemanticSource: "osc_stream",
	})
	if !complete {
		t.Fatal("expected semantic prompt return to complete")
	}
	if evaluation.State != MonitorStateCompleted {
		t.Fatalf("expected completed state, got %q", evaluation.State)
	}
	if evaluation.Result.Captured != "password accepted" {
		t.Fatalf("expected fallback output tail, got %q", evaluation.Result.Captured)
	}
	if !evaluation.Result.SemanticShell || evaluation.Result.SemanticSource != "osc_stream" {
		t.Fatalf("expected semantic metadata, got %#v", evaluation.Result)
	}
}

func TestEvaluatePromptReturnInferenceUsesPromptExitCodeAndFallbackTail(t *testing.T) {
	exitCode := InterruptedExitCode
	evaluation, complete := evaluatePromptReturnInference(promptReturnInputs{
		Command: "sleep 20",
		Observed: ObservedShellState{
			PromptContext: PromptContext{
				User:         "localuser",
				Host:         "workstation",
				Directory:    "/workspace/project",
				PromptSymbol: "%",
				RawLine:      "localuser@workstation /workspace/project %",
				LastExitCode: &exitCode,
			},
		},
		Snapshot: MonitorSnapshot{
			ShellContext: PromptContext{
				User:         "localuser",
				Host:         "workstation",
				Directory:    "/workspace/project",
				PromptSymbol: "%",
				RawLine:      "localuser@workstation /workspace/project %",
				LastExitCode: &exitCode,
			},
			LatestOutputTail: "^C",
		},
		RawBody:      "",
		BodyCleaner:  func(body string, promptContext PromptContext) string { return body },
		FallbackBody: func(snapshot MonitorSnapshot) string { return snapshot.LatestOutputTail },
	})
	if !complete {
		t.Fatal("expected prompt return inference to complete")
	}
	if evaluation.State != MonitorStateCanceled {
		t.Fatalf("expected canceled state, got %q", evaluation.State)
	}
	if evaluation.Result.ExitCode != InterruptedExitCode {
		t.Fatalf("expected interrupted exit code, got %d", evaluation.Result.ExitCode)
	}
	if evaluation.Result.Captured != "^C" {
		t.Fatalf("expected fallback output tail, got %q", evaluation.Result.Captured)
	}
	if evaluation.Result.Confidence != ConfidenceStrong {
		t.Fatalf("expected strong confidence from prompt exit code, got %q", evaluation.Result.Confidence)
	}
}

func TestEvaluatePromptReturnInferenceWaitsForBodyOrExitEvidence(t *testing.T) {
	_, complete := evaluatePromptReturnInference(promptReturnInputs{
		Command: "sleep 20",
		Observed: ObservedShellState{
			PromptContext: PromptContext{
				User:         "localuser",
				Host:         "workstation",
				Directory:    "/workspace/project",
				PromptSymbol: "%",
				RawLine:      "localuser@workstation /workspace/project %",
			},
		},
		Snapshot: MonitorSnapshot{
			ShellContext: PromptContext{
				User:         "localuser",
				Host:         "workstation",
				Directory:    "/workspace/project",
				PromptSymbol: "%",
				RawLine:      "localuser@workstation /workspace/project %",
			},
		},
		RawBody:     "",
		BodyCleaner: func(body string, promptContext PromptContext) string { return body },
	})
	if complete {
		t.Fatal("expected empty prompt return without exit evidence to wait for another observation")
	}
}

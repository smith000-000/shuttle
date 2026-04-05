package shell

import "strings"

type promptReturnEvaluation struct {
	Result TrackedExecution
	State  MonitorState
}

type promptReturnInputs struct {
	CommandID      string
	Command        string
	Observed       ObservedShellState
	Snapshot       MonitorSnapshot
	PromptHint     PromptContext
	RawBody        string
	BodyCleaner    func(string, PromptContext) string
	FallbackBody   func(MonitorSnapshot) string
	AllowEmptyBody bool
	SemanticSource string
}

func evaluateSemanticPromptReturn(input promptReturnInputs) (promptReturnEvaluation, bool) {
	if !input.Observed.HasSemanticState || input.Observed.SemanticState.Event != semanticEventPrompt {
		return promptReturnEvaluation{}, false
	}

	promptContext := promptReturnContext(input)
	cleanBody := cleanPromptReturnBody(input.RawBody, promptContext, input.BodyCleaner)
	if cleanBody == "" && input.FallbackBody != nil {
		cleanBody = strings.TrimSpace(input.FallbackBody(input.Snapshot))
	}

	exitCode := 0
	if input.Observed.SemanticState.ExitCode != nil {
		exitCode = *input.Observed.SemanticState.ExitCode
	}

	state := monitorStateFromExitCode(exitCode)
	return promptReturnEvaluation{
		Result: TrackedExecution{
			CommandID:      input.CommandID,
			Command:        input.Command,
			Cause:          CompletionCausePromptReturn,
			Confidence:     ConfidenceStrong,
			SemanticShell:  true,
			SemanticSource: input.SemanticSource,
			ExitCode:       exitCode,
			Captured:       cleanBody,
			ShellContext:   promptContext,
		},
		State: state,
	}, true
}

func evaluatePromptReturnInference(input promptReturnInputs) (promptReturnEvaluation, bool) {
	promptContext := promptReturnContext(input)
	cleanBody := cleanPromptReturnBody(input.RawBody, promptContext, input.BodyCleaner)
	if cleanBody == "" && input.FallbackBody != nil {
		cleanBody = strings.TrimSpace(input.FallbackBody(input.Snapshot))
	}
	if !input.AllowEmptyBody && cleanBody == "" && promptContext.LastExitCode == nil && input.Observed.SemanticState.ExitCode == nil {
		return promptReturnEvaluation{}, false
	}

	exitCode, state, confidence, inferred := inferPromptReturnResult(promptContext, cleanBody, input.Observed.SemanticState.ExitCode)
	semanticShell := !inferred && (promptContext.LastExitCode != nil || input.Observed.SemanticState.ExitCode != nil)
	semanticSource := ""
	if semanticShell {
		semanticSource = input.SemanticSource
	}

	return promptReturnEvaluation{
		Result: TrackedExecution{
			CommandID:      input.CommandID,
			Command:        input.Command,
			Cause:          CompletionCausePromptReturn,
			Confidence:     confidence,
			SemanticShell:  semanticShell,
			SemanticSource: semanticSource,
			ExitCode:       exitCode,
			Captured:       cleanBody,
			ShellContext:   promptContext,
		},
		State: state,
	}, true
}

func promptReturnContext(input promptReturnInputs) PromptContext {
	promptContext := input.Snapshot.ShellContext
	if promptContext.PromptLine() == "" {
		promptContext = input.Observed.PromptContext
	}
	if promptContext.PromptLine() == "" && input.Observed.HasSemanticState {
		promptContext = synthesizePromptContext(input.PromptHint, input.Observed.SemanticState)
	}
	return promptContext
}

func cleanPromptReturnBody(body string, promptContext PromptContext, cleaner func(string, PromptContext) string) string {
	if cleaner == nil {
		return strings.TrimSpace(body)
	}
	return strings.TrimSpace(cleaner(body, promptContext))
}

func monitorStateFromExitCode(exitCode int) MonitorState {
	switch exitCode {
	case InterruptedExitCode:
		return MonitorStateCanceled
	case 0:
		return MonitorStateCompleted
	default:
		return MonitorStateFailed
	}
}

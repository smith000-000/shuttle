package shell

import "strings"

func TailSuggestsPromptReturn(tail string, current PromptContext) bool {
	if strings.TrimSpace(tail) == "" {
		return false
	}

	lines := lastNonEmptyLines(tail, 2)
	if len(lines) == 0 {
		return false
	}

	trailing := strings.Join(lines, "\n")
	if context, ok := ParsePromptContextFromCapture(trailing); ok {
		last := strings.TrimSpace(lines[len(lines)-1])
		if strings.TrimSpace(context.RawLine) == last {
			return true
		}
	}

	line := strings.TrimSpace(lines[len(lines)-1])
	return promptLooksLikeCurrentShell(line, current)
}

func HandoffPromptReturnReason(observed ObservedShellState, tail string, fallbackPrompt *PromptContext) string {
	if observed.HasPromptContext && observed.PromptContext.PromptLine() != "" {
		return "prompt_context"
	}
	if TailSuggestsAwaitingInput(tail) {
		return ""
	}
	if !paneCommandAllowsPromptInference(observed.CurrentPaneCommand) {
		return ""
	}
	if observed.SemanticState.ExitCode != nil {
		return "semantic_exit"
	}
	if strings.Contains(tail, "^C") {
		return "interrupt_tail"
	}
	if fallbackPrompt != nil && fallbackPrompt.PromptLine() != "" && TailSuggestsPromptReturn(tail, *fallbackPrompt) {
		return "fallback_prompt_tail"
	}
	return ""
}

func captureHasCurrentPromptContext(captured string, current PromptContext) bool {
	if current.PromptLine() == "" {
		return false
	}
	return TailSuggestsPromptReturn(captured, current)
}

func lastNonEmptyLines(tail string, limit int) []string {
	if limit <= 0 {
		return nil
	}

	lines := strings.Split(strings.ReplaceAll(tail, "\r\n", "\n"), "\n")
	result := make([]string, 0, limit)
	for index := len(lines) - 1; index >= 0 && len(result) < limit; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		result = append(result, line)
	}

	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}

	return result
}

func promptLooksLikeCurrentShell(line string, current PromptContext) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}

	promptSymbol := current.PromptSymbol
	if promptSymbol == "" {
		switch last := trimmed[len(trimmed)-1]; last {
		case '$', '#', '%', '>':
			promptSymbol = string(last)
		default:
			return false
		}
	}
	if !strings.HasSuffix(trimmed, promptSymbol) {
		return false
	}

	score := 0
	if current.User != "" && current.Host != "" && strings.Contains(trimmed, current.User+"@"+current.Host) {
		score++
	}
	if current.Directory != "" && strings.Contains(trimmed, current.Directory) {
		score++
	}
	if current.GitBranch != "" && strings.Contains(trimmed, "git:("+current.GitBranch+")") {
		score++
	}

	if score > 0 {
		return true
	}

	if strings.Contains(trimmed, "@") && (strings.Contains(trimmed, "~/") || strings.Contains(trimmed, "/")) {
		return true
	}
	if strings.HasPrefix(trimmed, "~") || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, ".") {
		return true
	}

	return false
}

func inferPromptReturnResult(promptContext PromptContext, tail string, semanticExitCode *int) (int, MonitorState, SignalConfidence, bool) {
	inferred := false
	confidence := ConfidenceStrong
	exitCode := 0

	switch {
	case promptContext.LastExitCode != nil:
		exitCode = *promptContext.LastExitCode
	case semanticExitCode != nil:
		exitCode = *semanticExitCode
	case strings.Contains(tail, "^C"):
		exitCode = InterruptedExitCode
		confidence = ConfidenceMedium
		inferred = true
	case strings.Contains(tail, "command not found") || strings.Contains(tail, "No such file or directory"):
		exitCode = 127
		confidence = ConfidenceMedium
		inferred = true
	case strings.Contains(tail, "Permission denied"):
		exitCode = 126
		confidence = ConfidenceMedium
		inferred = true
	default:
		exitCode = 0
		confidence = ConfidenceLow
		inferred = true
	}

	switch exitCode {
	case InterruptedExitCode:
		return exitCode, MonitorStateCanceled, confidence, inferred
	case 0:
		return exitCode, MonitorStateCompleted, confidence, inferred
	default:
		return exitCode, MonitorStateFailed, confidence, inferred
	}
}

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

package provider

import (
	"strconv"
	"strings"
)

func compactShellOutput(value string, headLines int, tailLines int, maxChars int) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	if trimmed == "" {
		return ""
	}

	lines := strings.Split(trimmed, "\n")
	if headLines < 0 {
		headLines = 0
	}
	if tailLines < 0 {
		tailLines = 0
	}

	if len(lines) > headLines+tailLines && (headLines > 0 || tailLines > 0) {
		compacted := make([]string, 0, headLines+tailLines+1)
		if headLines > 0 {
			compacted = append(compacted, lines[:headLines]...)
		}
		omitted := len(lines) - headLines - tailLines
		if omitted > 0 {
			compacted = append(compacted, "...("+strconv.Itoa(omitted)+" more lines omitted)...")
		}
		if tailLines > 0 {
			compacted = append(compacted, lines[len(lines)-tailLines:]...)
		}
		trimmed = strings.Join(compacted, "\n")
	}

	return clipText(trimmed, maxChars)
}

func normalizedContextKey(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func shouldIncludeContextSnippet(seen map[string]struct{}, value string) bool {
	key := normalizedContextKey(value)
	if key == "" {
		return false
	}
	if _, exists := seen[key]; exists {
		return false
	}
	seen[key] = struct{}{}
	return true
}

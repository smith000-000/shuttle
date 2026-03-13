package shell

import (
	"regexp"
	"strings"
)

var awaitingInputPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)password(?: for [^:]+)?:\s*$`),
	regexp.MustCompile(`(?i)passphrase(?: for [^:]+)?:\s*$`),
	regexp.MustCompile(`(?i)press any key`),
	regexp.MustCompile(`(?i)press enter`),
	regexp.MustCompile(`(?i)press return`),
	regexp.MustCompile(`(?i)continue connecting.*\(yes/no`),
	regexp.MustCompile(`(?i)\(yes/no(?:/\[[^\]]+\])?\)\??\s*$`),
	regexp.MustCompile(`(?i)\[[yYnN]/[yYnN]\]\s*$`),
	regexp.MustCompile(`(?i)enter [^:]{1,80}:\s*$`),
	regexp.MustCompile(`(?i)(choice|selection):\s*$`),
	regexp.MustCompile(`(?i)waiting for input`),
}

func TailSuggestsAwaitingInput(tail string) bool {
	lines := lastNonEmptyLines(tail, 3)
	if len(lines) == 0 {
		return false
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, pattern := range awaitingInputPatterns {
			if pattern.MatchString(trimmed) {
				return true
			}
		}
	}

	return false
}

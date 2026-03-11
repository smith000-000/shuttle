package protocol

import (
	"fmt"
	"strconv"
	"strings"
)

type CommandResult struct {
	CommandID string
	ExitCode  int
	Body      string
}

func ParseCommandResult(captured string, markers Markers) (CommandResult, bool, error) {
	lines := splitLines(captured)

	beginIndex := -1
	for index, line := range lines {
		if strings.TrimSpace(line) == markers.BeginLine {
			beginIndex = index
			break
		}
	}

	if beginIndex == -1 {
		return CommandResult{}, false, nil
	}

	endIndex := -1
	exitCode := 0
	for index := beginIndex + 1; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if strings.HasPrefix(line, markers.EndPrefix) {
			parsedExitCode, err := strconv.Atoi(strings.TrimPrefix(line, markers.EndPrefix))
			if err != nil {
				return CommandResult{}, false, fmt.Errorf("parse end marker exit code from %q: %w", line, err)
			}

			endIndex = index
			exitCode = parsedExitCode
			break
		}
	}

	if endIndex == -1 {
		return CommandResult{}, false, nil
	}

	bodyLines := lines[beginIndex+1 : endIndex]
	return CommandResult{
		CommandID: markers.CommandID,
		ExitCode:  exitCode,
		Body:      strings.TrimRight(strings.Join(bodyLines, "\n"), "\n"),
	}, true, nil
}

func splitLines(captured string) []string {
	normalized := strings.ReplaceAll(captured, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.Split(normalized, "\n")
}

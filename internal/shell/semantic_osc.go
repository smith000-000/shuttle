package shell

import "strings"

const ()

func parseSemanticShellStateFromOSCCapture(raw string) (semanticShellState, bool) {
	if strings.TrimSpace(raw) == "" {
		return semanticShellState{}, false
	}

	var state semanticShellState
	found := false
	for _, payload := range extractOSCPayloads(raw) {
		switch {
		case strings.HasPrefix(payload, "133;"):
			if !applyOSC133SemanticState(&state, strings.TrimPrefix(payload, "133;")) {
				continue
			}
			found = true
		case strings.HasPrefix(payload, "7;file://"):
			cwd := parseOSC7Directory(payload)
			if cwd != "" {
				state.Directory = cwd
				found = true
			}
		}
	}

	if !found || state.Event == semanticEventUnknown {
		return semanticShellState{}, false
	}
	return state, true
}

func applyOSC133SemanticState(state *semanticShellState, payload string) bool {
	switch {
	case payload == "A":
		exitCode := cloneExitCode(state.ExitCode)
		state.Event = semanticEventPrompt
		state.ExitCode = exitCode
		return true
	case payload == "B" || payload == "C":
		state.Event = semanticEventCommand
		state.ExitCode = nil
		return true
	case payload == "D":
		state.Event = semanticEventCommandDone
		state.ExitCode = nil
		return true
	case strings.HasPrefix(payload, "D;"):
		exitCode, ok := parseOSCExit(strings.TrimPrefix(payload, "D;"))
		state.Event = semanticEventCommandDone
		if !ok {
			state.ExitCode = nil
			return true
		}
		state.ExitCode = &exitCode
		return true
	default:
		return false
	}
}

func extractOSCPayloads(raw string) []string {
	payloads := make([]string, 0, 8)
	for index := 0; index < len(raw); index++ {
		if raw[index] != 0x1b || index+1 >= len(raw) || raw[index+1] != ']' {
			continue
		}
		start := index + 2
		for cursor := start; cursor < len(raw); cursor++ {
			if raw[cursor] == 0x07 {
				payloads = append(payloads, raw[start:cursor])
				index = cursor
				break
			}
			if raw[cursor] == 0x1b && cursor+1 < len(raw) && raw[cursor+1] == '\\' {
				payloads = append(payloads, raw[start:cursor])
				index = cursor + 1
				break
			}
		}
	}
	return payloads
}

func parseOSC133Event(payload string) (semanticShellEvent, *int, bool) {
	var state semanticShellState
	if !applyOSC133SemanticState(&state, payload) {
		return semanticEventUnknown, nil, false
	}
	return state.Event, cloneExitCode(state.ExitCode), true
}

func parseOSCExit(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	total := 0
	sign := 1
	if strings.HasPrefix(value, "-") {
		sign = -1
		value = strings.TrimPrefix(value, "-")
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
		total = total*10 + int(r-'0')
	}
	return sign * total, true
}

func parseOSC7Directory(payload string) string {
	value := strings.TrimPrefix(payload, "7;file://")
	if value == payload {
		return ""
	}
	slash := strings.IndexRune(value, '/')
	if slash < 0 {
		return ""
	}
	return value[slash:]
}

func cloneExitCode(exitCode *int) *int {
	if exitCode == nil {
		return nil
	}
	value := *exitCode
	return &value
}

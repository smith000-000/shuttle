package shell

import "strings"

const (
	semanticSourceNone       = ""
	semanticSourceState      = "state_file"
	semanticSourceOSCCapture = "osc_capture"
)

func parseSemanticShellStateFromOSCCapture(raw string) (semanticShellState, bool) {
	if strings.TrimSpace(raw) == "" {
		return semanticShellState{}, false
	}

	var state semanticShellState
	found := false
	for _, payload := range extractOSCPayloads(raw) {
		switch {
		case strings.HasPrefix(payload, "133;"):
			event, exitCode, ok := parseOSC133Event(strings.TrimPrefix(payload, "133;"))
			if !ok {
				continue
			}
			state.Event = event
			if exitCode != nil {
				state.ExitCode = exitCode
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
	switch {
	case payload == "A":
		return semanticEventPrompt, nil, true
	case payload == "B" || payload == "C":
		return semanticEventCommand, nil, true
	case payload == "D":
		return semanticEventPrompt, nil, true
	case strings.HasPrefix(payload, "D;"):
		parsed, ok := parseOSCExit(strings.TrimPrefix(payload, "D;"))
		if !ok {
			return semanticEventPrompt, nil, true
		}
		return semanticEventPrompt, &parsed, true
	default:
		return semanticEventUnknown, nil, false
	}
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

package provider

import (
	"os/exec"
	"strings"

	"aiterm/internal/agentruntime"
)

const (
	RuntimeBuiltin   = agentruntime.RuntimeBuiltin
	RuntimePi        = agentruntime.RuntimePi
	RuntimeCodexSDK  = agentruntime.RuntimeCodexSDK
	RuntimeAuto      = agentruntime.RuntimeAuto
	defaultPiCommand = "pi"
)

var runtimeLookPath = exec.LookPath

type RuntimeInstallCandidate struct {
	Name      string
	Command   string
	Runtime   string
	Installed bool
	Supported bool
}

type ResolvedRuntime struct {
	RequestedType string
	SelectedType  string
	Command       string
	AutoSelected  bool
}

func DetectRuntimeInstallCandidates() []RuntimeInstallCandidate {
	candidates := []RuntimeInstallCandidate{
		{Name: "pi", Command: defaultPiCommand, Runtime: RuntimePi, Supported: true},
		{Name: "codex sdk", Command: defaultCodexCLICommand, Runtime: RuntimeCodexSDK, Supported: true},
		{Name: "claude agent", Command: "claude", Supported: false},
		{Name: "opencode", Command: "opencode", Supported: false},
	}

	for index := range candidates {
		command := strings.TrimSpace(candidates[index].Command)
		if command == "" {
			continue
		}
		if _, err := runtimeLookPath(command); err == nil {
			candidates[index].Installed = true
		}
	}

	return candidates
}

func ResolveRuntimeSelection(requestedType string, requestedCommand string) ResolvedRuntime {
	requestedType = normalizeRuntimeType(requestedType)
	requestedCommand = strings.TrimSpace(requestedCommand)
	resolved := ResolvedRuntime{RequestedType: requestedType, SelectedType: requestedType, Command: requestedCommand}

	if requestedType == "" {
		requestedType = RuntimeBuiltin
		resolved.RequestedType = RuntimeBuiltin
		resolved.SelectedType = RuntimeBuiltin
	}

	if requestedType == RuntimeAuto {
		for _, candidate := range DetectRuntimeInstallCandidates() {
			if !candidate.Supported || !candidate.Installed {
				continue
			}
			resolved.SelectedType = candidate.Runtime
			if resolved.Command == "" {
				resolved.Command = strings.TrimSpace(candidate.Command)
			}
			resolved.AutoSelected = true
			return resolved
		}
		resolved.SelectedType = RuntimeBuiltin
		resolved.Command = ""
		resolved.AutoSelected = true
		return resolved
	}

	if resolved.Command != "" {
		return resolved
	}
	for _, candidate := range DetectRuntimeInstallCandidates() {
		if candidate.Runtime != requestedType {
			continue
		}
		resolved.Command = strings.TrimSpace(candidate.Command)
		break
	}
	return resolved
}

func normalizeRuntimeType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", RuntimeBuiltin:
		return RuntimeBuiltin
	case RuntimeAuto:
		return RuntimeAuto
	case "pi-runtime":
		return RuntimePi
	case "codex-sdk":
		return RuntimeCodexSDK
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

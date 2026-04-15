package provider

import (
	"os/exec"
	"strings"

	"aiterm/internal/agentruntime"
)

const (
	RuntimeBuiltin        = agentruntime.RuntimeBuiltin
	RuntimePi             = agentruntime.RuntimePi
	RuntimeCodexSDK       = agentruntime.RuntimeCodexSDK
	RuntimeCodexAppServer = agentruntime.RuntimeCodexAppServer
	RuntimeAuto           = agentruntime.RuntimeAuto
	defaultPiCommand      = "pi"
)

var runtimeLookPath = exec.LookPath

type RuntimeInstallCandidate struct {
	Name          string
	Command       string
	Runtime       string
	Installed     bool
	Supported     bool
	ParityRank    int
	FailureReason string
}

type ResolvedRuntime struct {
	RequestedType string
	SelectedType  string
	Command       string
	AutoSelected  bool
}

func DetectRuntimeInstallCandidates() []RuntimeInstallCandidate {
	candidates := []RuntimeInstallCandidate{
		{Name: "codex sdk", Command: defaultCodexCLICommand, Runtime: RuntimeCodexSDK, Supported: true, ParityRank: 100},
		{Name: "codex app server", Command: defaultCodexCLICommand, Runtime: RuntimeCodexAppServer, Supported: true, ParityRank: 90},
		{Name: "pi", Command: defaultPiCommand, Runtime: RuntimePi, Supported: false, ParityRank: 0},
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
		} else {
			if candidates[index].Supported {
				candidates[index].Supported = false
				candidates[index].FailureReason = err.Error()
			}
			continue
		}
		if candidates[index].Runtime == RuntimeCodexSDK || candidates[index].Runtime == RuntimeCodexAppServer {
			if err := validateCodexRuntimeCommand(command); err != nil {
				candidates[index].Supported = false
				candidates[index].FailureReason = err.Error()
			}
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
		bestRank := -1
		for _, candidate := range DetectRuntimeInstallCandidates() {
			if !candidate.Supported || !candidate.Installed {
				continue
			}
			if candidate.ParityRank < bestRank {
				continue
			}
			bestRank = candidate.ParityRank
			resolved.SelectedType = candidate.Runtime
			if resolved.Command == "" {
				resolved.Command = strings.TrimSpace(candidate.Command)
			}
		}
		if resolved.SelectedType != RuntimeAuto && resolved.SelectedType != "" {
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
	case "codex-app-server", "codex-appserver":
		return RuntimeCodexAppServer
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

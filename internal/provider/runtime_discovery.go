package provider

import "strings"

type RuntimeInstallCandidate struct {
	Name      string
	Command   string
	Runtime   string
	Installed bool
	Supported bool
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

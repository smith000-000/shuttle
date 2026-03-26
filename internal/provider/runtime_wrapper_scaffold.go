package provider

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"aiterm/internal/controller"
)

const (
	RuntimeBuiltin  = "builtin"
	RuntimePi       = "pi"
	RuntimeCodexSDK = "codex_sdk"
	RuntimeAuto     = "auto"
)

var defaultPiCommand = "pi"

type RuntimeWrapperAgent struct {
	delegate    controller.Agent
	runtimeType string
	command     string
	profile     Profile
}

func maybeWrapRuntimeAgent(delegate controller.Agent, profile Profile, runtimeType string, runtimeCommand string) (controller.Agent, error) {
	normalized := normalizeRuntimeSelection(runtimeType)
	if normalized == RuntimeBuiltin {
		return delegate, nil
	}

	command := strings.TrimSpace(runtimeCommand)
	if command == "" {
		switch normalized {
		case RuntimePi:
			command = defaultPiCommand
		case RuntimeCodexSDK:
			command = defaultCodexCLICommand
		case RuntimeAuto:
			command = firstInstalledRuntimeCommand()
			if command == "" {
				return nil, fmt.Errorf("runtime auto-discovery found no installed runtime command; install %q or %q, or set --runtime-command", defaultPiCommand, defaultCodexCLICommand)
			}
			if command == defaultPiCommand {
				normalized = RuntimePi
			}
			if command == defaultCodexCLICommand {
				normalized = RuntimeCodexSDK
			}
		default:
			return nil, fmt.Errorf("unsupported runtime %q", runtimeType)
		}
	}

	if _, err := runtimeLookPath(command); err != nil {
		return nil, fmt.Errorf("find runtime command %q: %w", command, err)
	}

	return &RuntimeWrapperAgent{
		delegate:    delegate,
		runtimeType: normalized,
		command:     command,
		profile:     profile,
	}, nil
}

func normalizeRuntimeSelection(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", RuntimeBuiltin:
		return RuntimeBuiltin
	case RuntimeAuto:
		return RuntimeAuto
	case "pi-runtime":
		return RuntimePi
	case "codex-sdk":
		return RuntimeCodexSDK
	case RuntimePi, RuntimeCodexSDK:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func (a *RuntimeWrapperAgent) Respond(ctx context.Context, input controller.AgentInput) (controller.AgentResponse, error) {
	response, err := a.delegate.Respond(ctx, input)
	if err != nil {
		return controller.AgentResponse{}, err
	}

	messagePrefix := fmt.Sprintf("[runtime=%s command=%q provider=%s model=%s] ", a.runtimeType, a.command, a.profile.Preset, strings.TrimSpace(a.profile.Model))
	response.Message = strings.TrimSpace(messagePrefix + response.Message)
	if response.ModelInfo == nil {
		response.ModelInfo = &controller.AgentModelInfo{}
	}
	response.ModelInfo.ProviderPreset = string(a.profile.Preset)
	if strings.TrimSpace(response.ModelInfo.RequestedModel) == "" {
		response.ModelInfo.RequestedModel = strings.TrimSpace(a.profile.Model)
	}

	return response, nil
}

func (a *RuntimeWrapperAgent) CheckHealth(_ context.Context) error {
	_, err := runtimeLookPath(a.command)
	return err
}

var runtimeLookPath = exec.LookPath

func firstInstalledRuntimeCommand() string {
	for _, command := range []string{defaultPiCommand, defaultCodexCLICommand} {
		if _, err := runtimeLookPath(command); err == nil {
			return command
		}
	}
	return ""
}

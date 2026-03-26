package provider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"aiterm/internal/controller"
)

type staticAgent struct {
	response controller.AgentResponse
	err      error
}

func (a staticAgent) Respond(context.Context, controller.AgentInput) (controller.AgentResponse, error) {
	return a.response, a.err
}

func TestMaybeWrapRuntimeAgentBuiltinPassThrough(t *testing.T) {
	base := staticAgent{response: controller.AgentResponse{Message: "ok"}}
	agent, err := maybeWrapRuntimeAgent(base, Profile{Preset: PresetOpenAI, Model: "gpt-5"}, RuntimeBuiltin, "")
	if err != nil {
		t.Fatalf("maybeWrapRuntimeAgent() error = %v", err)
	}
	response, err := agent.Respond(context.Background(), controller.AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if response.Message != "ok" {
		t.Fatalf("expected pass-through response, got %q", response.Message)
	}
}

func TestMaybeWrapRuntimeAgentPrefixesRuntimeContext(t *testing.T) {
	previous := runtimeLookPath
	runtimeLookPath = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}
	t.Cleanup(func() {
		runtimeLookPath = previous
	})

	base := staticAgent{response: controller.AgentResponse{Message: "base response"}}
	agent, err := maybeWrapRuntimeAgent(base, Profile{Preset: PresetOpenWebUI, Model: "qwen3"}, RuntimePi, "")
	if err != nil {
		t.Fatalf("maybeWrapRuntimeAgent() error = %v", err)
	}
	response, err := agent.Respond(context.Background(), controller.AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if !strings.Contains(response.Message, "runtime=pi") {
		t.Fatalf("expected runtime prefix, got %q", response.Message)
	}
	if !strings.Contains(response.Message, "provider=openwebui") {
		t.Fatalf("expected provider context, got %q", response.Message)
	}
}

func TestMaybeWrapRuntimeAgentRequiresInstalledCommand(t *testing.T) {
	previous := runtimeLookPath
	runtimeLookPath = func(file string) (string, error) {
		return "", errors.New("missing")
	}
	t.Cleanup(func() {
		runtimeLookPath = previous
	})

	_, err := maybeWrapRuntimeAgent(staticAgent{}, Profile{Preset: PresetOpenAI}, RuntimeCodexSDK, "")
	if err == nil {
		t.Fatal("expected missing command error")
	}
}

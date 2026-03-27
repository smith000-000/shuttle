package controller

import (
	"context"
	"testing"
)

func TestLocalControllerSubmitExternalPrompt(t *testing.T) {
	agent := &stubAgent{
		externalResponse: AgentResponse{
			Message: "external handled it",
		},
	}
	controller := New(agent, nil, &stubContextReader{
		output: "recent shell output",
	}, SessionContext{TrackedShell: TrackedShellTarget{PaneID: "%0"}})

	events, err := controller.SubmitExternalPrompt(context.Background(), "please fix the script")
	if err != nil {
		t.Fatalf("SubmitExternalPrompt() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %#v", events)
	}
	if events[0].Kind != EventUserMessage || events[1].Kind != EventAgentMessage {
		t.Fatalf("unexpected event sequence: %#v", events)
	}
	if agent.lastExternalInput.Prompt != "please fix the script" {
		t.Fatalf("expected direct external prompt, got %q", agent.lastExternalInput.Prompt)
	}
	if agent.lastExternalInput.Session.RecentShellOutput != "recent shell output" {
		t.Fatalf("expected shell context in external prompt input, got %q", agent.lastExternalInput.Session.RecentShellOutput)
	}
}

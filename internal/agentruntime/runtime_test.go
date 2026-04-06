package agentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stubHost struct {
	responses     []Outcome
	respondCalls  int
	inspectCalls  int
	validateError error
}

func (s *stubHost) Respond(_ context.Context, _ Request) (Outcome, error) {
	if s.respondCalls >= len(s.responses) {
		return Outcome{}, errors.New("unexpected respond call")
	}
	response := s.responses[s.respondCalls]
	s.respondCalls++
	return response, nil
}

func (s *stubHost) InspectContext(_ context.Context, _ Request) error {
	s.inspectCalls++
	return nil
}

func (s *stubHost) SynthesizeStructuredEdit(_ context.Context, outcome Outcome) (Outcome, error) {
	return outcome, nil
}

func (s *stubHost) ValidatePatch(_ context.Context, _ string, _ string) error {
	return s.validateError
}

func TestBuiltinRuntimeReplaysAfterInspectContext(t *testing.T) {
	host := &stubHost{
		responses: []Outcome{
			{Proposal: &Proposal{Kind: "inspect_context"}},
			{Message: "stable now"},
		},
	}

	outcome, err := NewBuiltin().Handle(context.Background(), host, Request{
		Kind:          RequestUserTurn,
		Prompt:        "help",
		InspectBudget: 2,
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if host.inspectCalls != 1 {
		t.Fatalf("expected one inspect call, got %d", host.inspectCalls)
	}
	if strings.TrimSpace(outcome.Message) != "stable now" {
		t.Fatalf("expected final message after inspect, got %#v", outcome)
	}
}

func TestBuiltinRuntimeRepairsInvalidPatchOnce(t *testing.T) {
	host := &stubHost{
		responses: []Outcome{
			{Proposal: &Proposal{Kind: "patch", Patch: "bad patch", PatchTarget: "local_workspace"}},
			{Message: "fixed", Proposal: &Proposal{Kind: "patch", Patch: "still bad", PatchTarget: "local_workspace"}},
		},
		validateError: errors.New("fragment header miscounts lines"),
	}

	outcome, err := NewBuiltin().Handle(context.Background(), host, Request{
		Kind:   RequestUserTurn,
		Prompt: "fix it",
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if host.respondCalls != 2 {
		t.Fatalf("expected repair retry, got %d calls", host.respondCalls)
	}
	if outcome.Proposal != nil {
		t.Fatalf("expected invalid repaired proposal to be dropped, got %#v", outcome.Proposal)
	}
	if !strings.Contains(outcome.Message, invalidPatchProposalNotice) {
		t.Fatalf("expected invalid patch notice, got %#v", outcome.Message)
	}
}

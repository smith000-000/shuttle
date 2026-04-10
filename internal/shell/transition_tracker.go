package shell

import "strings"

type transitionObservation struct {
	Capture   string
	Candidate PromptContext
	HasPrompt bool
	Delta     string
}

func newTransitionObservation(beforeCapture string, capture string, command string) transitionObservation {
	candidate, ok := ParsePromptContextFromCapture(capture)
	delta := capturePaneDelta(beforeCapture, capture)
	delta = sanitizeCapturedBody(delta)
	delta = stripEchoedCommand(delta, command)
	delta = stripTrailingPromptLine(delta, candidate)
	delta = strings.TrimSpace(delta)
	return transitionObservation{
		Capture:   capture,
		Candidate: candidate,
		HasPrompt: ok,
		Delta:     delta,
	}
}

type contextTransitionTracker struct {
	command         string
	beforeCapture   string
	baseline        PromptContext
	candidateSeen   bool
	candidatePrompt PromptContext
	state           contextTransitionState
}

func newContextTransitionTracker(command string, beforeCapture string, baseline PromptContext) *contextTransitionTracker {
	return &contextTransitionTracker{
		command:       command,
		beforeCapture: beforeCapture,
		baseline:      baseline,
		state:         contextTransitionSubmitted,
	}
}

type transitionTrackerDecision struct {
	State         contextTransitionState
	NeedsVerify   bool
	Settled       bool
	AwaitingInput bool
	PromptContext PromptContext
	PromptCapture string
}

func (t *contextTransitionTracker) Observe(observation transitionObservation) transitionTrackerDecision {
	if TailSuggestsAwaitingInput(observation.Delta) {
		t.state = contextTransitionAwaitingInteractive
		t.candidateSeen = false
		return transitionTrackerDecision{State: t.state, AwaitingInput: true}
	}

	if shouldSettleTopLevelExitReturn(t.command, t.baseline, observation) {
		t.state = contextTransitionSettled
		t.candidateSeen = true
		t.candidatePrompt = observation.Candidate
		return transitionTrackerDecision{
			State:         t.state,
			Settled:       true,
			PromptContext: observation.Candidate,
			PromptCapture: observation.Capture,
		}
	}

	if !observation.HasPrompt || !promptReturnedAfterTransition(t.beforeCapture, t.baseline, observation.Candidate, observation.Capture, observation.Delta) {
		return transitionTrackerDecision{State: t.state}
	}

	t.state = contextTransitionCandidatePromptSeen
	if !t.candidateSeen || !promptContextsEquivalent(t.candidatePrompt, observation.Candidate) {
		t.candidateSeen = true
		t.candidatePrompt = observation.Candidate
		return transitionTrackerDecision{State: t.state}
	}

	return transitionTrackerDecision{State: t.state, NeedsVerify: true}
}

func (t *contextTransitionTracker) ObserveVerification(observation transitionObservation) transitionTrackerDecision {
	if TailSuggestsAwaitingInput(observation.Delta) {
		t.state = contextTransitionAwaitingInteractive
		t.candidateSeen = false
		return transitionTrackerDecision{State: t.state, AwaitingInput: true}
	}

	switch {
	case observation.HasPrompt && promptContextsEquivalent(t.candidatePrompt, observation.Candidate):
		t.state = contextTransitionProbeVerifying
		return transitionTrackerDecision{
			State:         t.state,
			Settled:       true,
			PromptContext: observation.Candidate,
			PromptCapture: observation.Capture,
		}
	case observation.HasPrompt && promptReturnedAfterTransition(t.beforeCapture, t.baseline, observation.Candidate, observation.Capture, observation.Delta):
		t.candidateSeen = true
		t.candidatePrompt = observation.Candidate
		return transitionTrackerDecision{State: contextTransitionCandidatePromptSeen}
	default:
		t.candidateSeen = false
		return transitionTrackerDecision{State: contextTransitionCandidatePromptSeen}
	}
}

func shouldSettleTopLevelExitReturn(command string, baseline PromptContext, observation transitionObservation) bool {
	fields := transitionCommandFields(command)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "exit", "logout":
	default:
		return false
	}
	if baseline.PromptLine() == "" || baseline.Remote || baseline.Root {
		return false
	}
	if !observation.HasPrompt || strings.TrimSpace(observation.Delta) != "" {
		return false
	}
	return promptContextsEquivalent(baseline, observation.Candidate)
}

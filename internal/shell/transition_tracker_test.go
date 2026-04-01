package shell

import "testing"

func TestTransitionTrackerRequiresStablePromptBeforeSettling(t *testing.T) {
	before := "me@local ~/repo $\n"
	baseline, _ := ParsePromptContextFromCapture(before)
	tracker := newContextTransitionTracker("ssh prod", before, baseline)

	first := newTransitionObservation(before, "ssh prod\nme@prod ~/repo $\n", "ssh prod")
	if decision := tracker.Observe(first); decision.NeedsVerify || decision.Settled {
		t.Fatalf("first candidate should not settle transition: %+v", decision)
	}

	second := newTransitionObservation(before, "ssh prod\nme@prod ~/repo $\n", "ssh prod")
	decision := tracker.Observe(second)
	if !decision.NeedsVerify {
		t.Fatalf("second matching candidate should require verification: %+v", decision)
	}

	verify := newTransitionObservation(before, "ssh prod\nme@prod ~/repo $\n", "ssh prod")
	verifyDecision := tracker.ObserveVerification(verify)
	if !verifyDecision.Settled {
		t.Fatalf("verification should settle transition: %+v", verifyDecision)
	}
	if got := verifyDecision.PromptContext.Host; got != "prod" {
		t.Fatalf("settled prompt host = %q, want %q", got, "prod")
	}
}

func TestTransitionTrackerKeepsAwaitingInputUnsettled(t *testing.T) {
	before := "me@local ~/repo $\n"
	baseline, _ := ParsePromptContextFromCapture(before)
	tracker := newContextTransitionTracker("ssh prod", before, baseline)

	observation := newTransitionObservation(before, "ssh prod\npassword:\n", "ssh prod")
	decision := tracker.Observe(observation)
	if !decision.AwaitingInput {
		t.Fatalf("expected password prompt to remain awaiting input: %+v", decision)
	}
	if decision.Settled || decision.NeedsVerify {
		t.Fatalf("awaiting input should not settle or verify: %+v", decision)
	}
}

func TestInferShellLocationUsesTransitionEvidence(t *testing.T) {
	location := inferShellLocation(PromptContext{}, "ssh", shellTransitionNone)
	if location.Kind != ShellLocationRemote {
		t.Fatalf("location kind = %q, want %q", location.Kind, ShellLocationRemote)
	}
	if location.Confidence != ConfidenceStrong {
		t.Fatalf("location confidence = %q, want %q", location.Confidence, ConfidenceStrong)
	}
}

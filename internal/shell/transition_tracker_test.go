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

func TestPublishContextTransitionObservationPromotesAwaitingInputState(t *testing.T) {
	monitor := newTrackedCommandMonitor("", "ssh openclaw@openclaw")
	observation := transitionObservation{
		Delta: "openclaw@openclaw's password:",
	}

	publishContextTransitionObservation(monitor, observation, transitionTrackerDecision{AwaitingInput: true})

	snapshot := monitor.Snapshot()
	if snapshot.State != MonitorStateAwaitingInput {
		t.Fatalf("expected awaiting-input state, got %#v", snapshot)
	}
	if snapshot.LatestOutputTail != "openclaw@openclaw's password:" {
		t.Fatalf("expected password prompt tail, got %#v", snapshot)
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

func TestTransitionTrackerSettlesSSHInAbsoluteDirectory(t *testing.T) {
	before := "me@local ~/repo $\n"
	baseline, _ := ParsePromptContextFromCapture(before)
	tracker := newContextTransitionTracker("ssh prod", before, baseline)

	first := newTransitionObservation(before, "ssh prod\nLast login: Tue Apr 1 09:00:00 2026\nme@prod /srv/app $\n", "ssh prod")
	if decision := tracker.Observe(first); decision.NeedsVerify || decision.Settled {
		t.Fatalf("first candidate should not settle transition: %+v", decision)
	}

	second := newTransitionObservation(before, "ssh prod\nLast login: Tue Apr 1 09:00:00 2026\nme@prod /srv/app $\n", "ssh prod")
	decision := tracker.Observe(second)
	if !decision.NeedsVerify {
		t.Fatalf("second matching candidate should require verification: %+v", decision)
	}

	verify := newTransitionObservation(before, "ssh prod\nLast login: Tue Apr 1 09:00:00 2026\nme@prod /srv/app $\n", "ssh prod")
	verifyDecision := tracker.ObserveVerification(verify)
	if !verifyDecision.Settled {
		t.Fatalf("verification should settle transition: %+v", verifyDecision)
	}
	if got := verifyDecision.PromptContext.Directory; got != "/srv/app" {
		t.Fatalf("settled prompt directory = %q, want %q", got, "/srv/app")
	}
}

func TestTransitionTrackerSettlesSudoRootShellAndExitBackToUserShell(t *testing.T) {
	userPrompt := "me@local /workspace/project $\n"
	userContext, _ := ParsePromptContextFromCapture(userPrompt)

	toRoot := newContextTransitionTracker("sudo -i", userPrompt, userContext)
	rootCapture := "sudo -i\nroot@local ~ #\n"
	if decision := toRoot.Observe(newTransitionObservation(userPrompt, rootCapture, "sudo -i")); decision.NeedsVerify || decision.Settled {
		t.Fatalf("first sudo prompt should not settle yet: %+v", decision)
	}
	rootDecision := toRoot.Observe(newTransitionObservation(userPrompt, rootCapture, "sudo -i"))
	if !rootDecision.NeedsVerify {
		t.Fatalf("second sudo prompt should require verification: %+v", rootDecision)
	}
	rootVerify := toRoot.ObserveVerification(newTransitionObservation(userPrompt, rootCapture, "sudo -i"))
	if !rootVerify.Settled || rootVerify.PromptContext.User != "root" {
		t.Fatalf("sudo transition should settle to root shell: %+v", rootVerify)
	}

	rootPrompt := "root@local ~ #\n"
	rootContext, _ := ParsePromptContextFromCapture(rootPrompt)
	toUser := newContextTransitionTracker("exit", rootPrompt, rootContext)
	userCapture := "exit\nme@local /workspace/project $\n"
	if decision := toUser.Observe(newTransitionObservation(rootPrompt, userCapture, "exit")); decision.NeedsVerify || decision.Settled {
		t.Fatalf("first exit prompt should not settle yet: %+v", decision)
	}
	userDecision := toUser.Observe(newTransitionObservation(rootPrompt, userCapture, "exit"))
	if !userDecision.NeedsVerify {
		t.Fatalf("second exit prompt should require verification: %+v", userDecision)
	}
	userVerify := toUser.ObserveVerification(newTransitionObservation(rootPrompt, userCapture, "exit"))
	if !userVerify.Settled || userVerify.PromptContext.User != "me" {
		t.Fatalf("exit transition should settle back to user shell: %+v", userVerify)
	}
}

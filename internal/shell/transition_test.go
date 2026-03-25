package shell

import "testing"

func TestDetectCommandTransitionKinds(t *testing.T) {
	cases := map[string]shellTransitionKind{
		"bash":                       shellTransitionLocal,
		"env FOO=1 zsh -i":           shellTransitionLocal,
		"bash -lc 'echo hi'":         shellTransitionNone,
		"ssh prod":                   shellTransitionRemote,
		"sudo ssh prod":              shellTransitionRemote,
		"docker exec -it app sh":     shellTransitionExec,
		"kubectl exec -it pod -- sh": shellTransitionExec,
		"sudo -i":                    shellTransitionLocal,
		"logout":                     shellTransitionUnknown,
		"git status":                 shellTransitionNone,
	}

	for command, want := range cases {
		if got := detectCommandTransition(command); got != want {
			t.Fatalf("detectCommandTransition(%q) = %q, want %q", command, got, want)
		}
	}
}

func TestSettledShellTransitionClearsExitBackToLocalShell(t *testing.T) {
	got := settledShellTransition("exit", "zsh", PromptContext{Remote: false}, shellTransitionRemote)
	if got != shellTransitionNone {
		t.Fatalf("settledShellTransition(exit back to local) = %q, want none", got)
	}
}

func TestDetectShellTransitionUsesRememberedKindWhenForegroundIsAmbiguous(t *testing.T) {
	got := detectShellTransition("", "bash", PromptContext{}, shellTransitionLocal)
	if got.Kind != shellTransitionLocal {
		t.Fatalf("detectShellTransition(remembered nested shell) = %q, want %q", got.Kind, shellTransitionLocal)
	}
}

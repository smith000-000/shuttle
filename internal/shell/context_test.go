package shell

import "testing"

func TestParsePromptContextFromCaptureZshPrompt(t *testing.T) {
	captured := "output line\n" + shellTestProjectBranchPrompt(t)

	context, ok := ParsePromptContextFromCapture(captured)
	if !ok {
		t.Fatal("expected prompt context")
	}

	if context.Directory != "~/workspace/project" {
		t.Fatalf("unexpected directory: %#v", context)
	}
	if context.GitBranch != shellTestBranch {
		t.Fatalf("unexpected branch: %#v", context)
	}
	if context.PromptSymbol != "%" {
		t.Fatalf("unexpected prompt symbol: %#v", context)
	}
}

func TestParsePromptContextFromCaptureRemoteSSHStylePrompt(t *testing.T) {
	captured := "line\nroot@web01:/srv/app#"

	context, ok := ParsePromptContextFromCapture(captured)
	if !ok {
		t.Fatal("expected prompt context")
	}

	if context.User != "root" || context.Host != "web01" {
		t.Fatalf("unexpected user/host: %#v", context)
	}
	if context.Directory != "/srv/app" {
		t.Fatalf("unexpected directory: %#v", context)
	}
	if !context.Root {
		t.Fatalf("expected root context: %#v", context)
	}
}

func TestParsePromptContextFromCaptureRemotePromptWithoutDirectory(t *testing.T) {
	captured := "line\nroot@web01#"

	context, ok := ParsePromptContextFromCapture(captured)
	if !ok {
		t.Fatal("expected prompt context")
	}

	if context.User != "root" || context.Host != "web01" {
		t.Fatalf("unexpected user/host: %#v", context)
	}
	if context.Directory != "" {
		t.Fatalf("expected empty directory: %#v", context)
	}
	if context.PromptSymbol != "#" {
		t.Fatalf("unexpected prompt symbol: %#v", context)
	}
	if !context.Root {
		t.Fatalf("expected root context: %#v", context)
	}
}

func TestPromptLineFormatsBranch(t *testing.T) {
	context := PromptContext{
		User:         shellTestUser,
		Host:         shellTestHost,
		Directory:    "~/workspace/project",
		GitBranch:    shellTestBranch,
		PromptSymbol: "%",
	}

	if got := context.PromptLine(); got != "localuser@workstation ~/workspace/project git:(main) %" {
		t.Fatalf("unexpected prompt line %q", got)
	}
}

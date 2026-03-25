package shell

import "testing"

func TestParsePromptContextFromCaptureZshPrompt(t *testing.T) {
	captured := "output line\njsmith@linuxdesktop ~/source/repos/aiterm git:(main) %"

	context, ok := ParsePromptContextFromCapture(captured)
	if !ok {
		t.Fatal("expected prompt context")
	}

	if context.User != "jsmith" || context.Host != "linuxdesktop" {
		t.Fatalf("unexpected user/host: %#v", context)
	}
	if context.Directory != "~/source/repos/aiterm" {
		t.Fatalf("unexpected directory: %#v", context)
	}
	if context.GitBranch != "main" {
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
		User:         "jsmith",
		Host:         "linuxdesktop",
		Directory:    "~/source/repos/aiterm",
		GitBranch:    "main",
		PromptSymbol: "%",
	}

	if got := context.PromptLine(); got != "jsmith@linuxdesktop ~/source/repos/aiterm git:(main) %" {
		t.Fatalf("unexpected prompt line %q", got)
	}
}

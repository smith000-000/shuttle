package tmux

import (
	"reflect"
	"strings"
	"testing"
)

func TestParsePanesOutput(t *testing.T) {
	output := strings.Join([]string{
		"%0\ttop-shell\t1\tzsh\t123\tshuttle\t@1\t0\t0\t30\t200\t0\t/dev/pts/1",
		"%1\tbottom-app\t0\tshuttle\t456\tshuttle\t@1\t31\t0\t12\t200\t1\t/dev/pts/2",
	}, "\n")

	panes, err := parsePanesOutput(output)
	if err != nil {
		t.Fatalf("parsePanesOutput() error = %v", err)
	}

	if len(panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(panes))
	}

	if panes[0].ID != "%0" || !panes[0].Active {
		t.Fatalf("unexpected top pane: %#v", panes[0])
	}

	if panes[1].Top != 31 || panes[1].PID != 456 {
		t.Fatalf("unexpected bottom pane: %#v", panes[1])
	}
	if !panes[1].AlternateOn {
		t.Fatalf("expected bottom pane alternate screen to be parsed, got %#v", panes[1])
	}
	if panes[1].TTY != "/dev/pts/2" {
		t.Fatalf("expected pane tty to be parsed, got %#v", panes[1])
	}
}

func TestWorkspaceFromPanesSortsByVerticalPosition(t *testing.T) {
	panes := []Pane{
		{ID: "%1", WindowID: "@1", Top: 20, Left: 0},
		{ID: "%0", WindowID: "@1", Top: 0, Left: 0},
	}

	workspace, err := workspaceFromPanes("shuttle", panes)
	if err != nil {
		t.Fatalf("workspaceFromPanes() error = %v", err)
	}

	if workspace.TopPane.ID != "%0" {
		t.Fatalf("expected top pane %%0, got %s", workspace.TopPane.ID)
	}

	if workspace.BottomPane.ID != "%1" {
		t.Fatalf("expected bottom pane %%1, got %s", workspace.BottomPane.ID)
	}
}

func TestWorkspaceFromPanesRejectsUnexpectedPaneCount(t *testing.T) {
	_, err := workspaceFromPanes("shuttle", []Pane{{ID: "%0"}})
	if err == nil {
		t.Fatal("expected error for malformed workspace")
	}
}

func TestTopPaneFromPanesSortsByVerticalPosition(t *testing.T) {
	panes := []Pane{
		{ID: "%2", Top: 20, Left: 0},
		{ID: "%1", Top: 0, Left: 10},
		{ID: "%0", Top: 0, Left: 0},
	}

	top, err := topPaneFromPanes("shuttle", panes)
	if err != nil {
		t.Fatalf("topPaneFromPanes() error = %v", err)
	}
	if top.ID != "%0" {
		t.Fatalf("expected top pane %%0, got %s", top.ID)
	}
}

func TestEnvironmentArgsSortsKeys(t *testing.T) {
	args := environmentArgs(map[string]string{
		"ZETA":  "z",
		"ALPHA": "a",
	})

	expected := []string{"-e", "ALPHA=a", "-e", "ZETA=z"}
	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d", len(expected), len(args))
	}

	for index := range expected {
		if args[index] != expected[index] {
			t.Fatalf("expected args[%d] = %q, got %q", index, expected[index], args[index])
		}
	}
}

func TestShellHistoryEnvironment(t *testing.T) {
	env := shellHistoryEnvironment("/tmp/shuttle-history")
	if env["HISTFILE"] != "/tmp/shuttle-history" {
		t.Fatalf("expected HISTFILE to be set, got %#v", env)
	}
	if env["SHUTTLE_HISTFILE"] != "/tmp/shuttle-history" {
		t.Fatalf("expected SHUTTLE_HISTFILE to be set, got %#v", env)
	}
}

func TestResolveSocketTargetPrefersConfiguredValue(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")

	target := ResolveSocketTarget("shuttle-test")
	if target != "shuttle-test" {
		t.Fatalf("expected configured socket target, got %q", target)
	}
}

func TestResolveSocketTargetFallsBackToTMUXEnvPath(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/custom,123,0")

	target := ResolveSocketTarget("")
	if target != "/tmp/tmux-1000/custom" {
		t.Fatalf("expected TMUX-derived socket path, got %q", target)
	}
}

func TestSocketFlagArgsUsesSocketName(t *testing.T) {
	args := SocketFlagArgs("shuttle-test")
	expected := []string{"-L", "shuttle-test"}
	if !reflect.DeepEqual(args, expected) {
		t.Fatalf("expected %v, got %v", expected, args)
	}
}

func TestSocketFlagArgsUsesSocketPath(t *testing.T) {
	args := SocketFlagArgs("/tmp/tmux-1000/custom")
	expected := []string{"-S", "/tmp/tmux-1000/custom"}
	if !reflect.DeepEqual(args, expected) {
		t.Fatalf("expected %v, got %v", expected, args)
	}
}

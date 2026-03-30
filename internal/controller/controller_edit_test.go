package controller

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"aiterm/internal/shell"
)

func TestSubmitAgentTurnSynthesizesLocalStructuredEditProposal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/foo.txt", []byte("hello\nINSERT BELOW HERE\nworld\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	agent := &stubAgent{
		response: AgentResponse{
			Message: "I can make that edit.",
			Proposal: &Proposal{
				Kind:        ProposalEdit,
				PatchTarget: PatchTargetLocalWorkspace,
				Edit: &EditIntent{
					Target:     PatchTargetLocalWorkspace,
					Path:       "foo.txt",
					Operation:  EditInsertAfter,
					AnchorText: "INSERT BELOW HERE",
					NewText:    "alpha\nbeta\n",
				},
				Description: "Insert text below the marker.",
			},
		},
	}

	controller := New(agent, nil, &stubContextReader{}, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		LocalWorkspaceRoot: root,
	})

	events, err := controller.SubmitAgentPrompt(context.Background(), "edit foo.txt")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}
	last := events[len(events)-1]
	if last.Kind != EventProposal {
		t.Fatalf("expected patch proposal, got %#v", events)
	}
	payload := last.Payload.(ProposalPayload)
	if payload.Kind != ProposalPatch {
		t.Fatalf("expected synthesized patch proposal, got %#v", payload)
	}
	if payload.PatchTarget != PatchTargetLocalWorkspace {
		t.Fatalf("expected local patch target, got %#v", payload)
	}
	if !strings.Contains(payload.Patch, "diff --git a/foo.txt b/foo.txt") || !strings.Contains(payload.Patch, "+alpha") || !strings.Contains(payload.Patch, "+beta") {
		t.Fatalf("expected synthesized diff with inserted lines, got %q", payload.Patch)
	}
}

func TestSubmitAgentTurnStructuredEditAmbiguityFallsBackToInspection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/foo.txt", []byte("INSERT BELOW HERE\nkeep\nINSERT BELOW HERE\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	agent := &stubAgent{
		response: AgentResponse{
			Proposal: &Proposal{
				Kind:        ProposalEdit,
				PatchTarget: PatchTargetLocalWorkspace,
				Edit: &EditIntent{
					Target:     PatchTargetLocalWorkspace,
					Path:       "foo.txt",
					Operation:  EditInsertAfter,
					AnchorText: "INSERT BELOW HERE",
					NewText:    "alpha\nbeta\n",
				},
				Description: "Insert text below the marker.",
			},
		},
	}

	controller := New(agent, nil, &stubContextReader{}, SessionContext{
		TrackedShell:       TrackedShellTarget{PaneID: "%0"},
		LocalWorkspaceRoot: root,
	})

	events, err := controller.SubmitAgentPrompt(context.Background(), "edit foo.txt")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}
	if len(events) < 2 || events[len(events)-1].Kind != EventProposal {
		t.Fatalf("expected visible fallback proposal, got %#v", events)
	}
	if events[len(events)-2].Kind != EventAgentMessage {
		t.Fatalf("expected explanatory agent message before fallback, got %#v", events)
	}
	if !strings.Contains(events[len(events)-2].Payload.(TextPayload).Text, "could not find a unique insertion/replacement point") {
		t.Fatalf("expected ambiguity explanation, got %#v", events[len(events)-2].Payload)
	}
	payload := events[len(events)-1].Payload.(ProposalPayload)
	if payload.Kind != ProposalCommand {
		t.Fatalf("expected inspection command fallback, got %#v", payload)
	}
	if !strings.Contains(payload.Command, "nl -ba") || !strings.Contains(payload.Command, "foo.txt") {
		t.Fatalf("expected inspection command, got %#v", payload)
	}
}

func TestSubmitAgentTurnSynthesizesRemoteStructuredEditProposal(t *testing.T) {
	reader := &stubContextReader{
		context: shell.PromptContext{
			User:         "openclaw",
			Host:         "openclaw",
			Directory:    "/home/openclaw",
			PromptSymbol: "$",
			RawLine:      "openclaw@openclaw ~ $",
			Remote:       true,
		},
	}
	runner := &stubRunner{}
	runner.run = func(_ context.Context, paneID string, command string, _ time.Duration) (shell.TrackedExecution, error) {
		if paneID != "%0" {
			t.Fatalf("expected pane %%0, got %q", paneID)
		}
		switch {
		case strings.Contains(command, "printf 'git=%s\\n'"):
			return shell.TrackedExecution{Command: command, ExitCode: 0, Captured: strings.Join([]string{
				"git=0",
				"python3=1",
				"base64=1",
				"mktemp=1",
				"shell=bash",
				"system=Linux",
				"os_release=ubuntu 24.04",
			}, "\n")}, nil
		case strings.Contains(command, remoteReadMarker):
			encoded := base64.StdEncoding.EncodeToString([]byte("hello\nINSERT BELOW HERE\nworld\n"))
			return shell.TrackedExecution{Command: command, ExitCode: 0, Captured: strings.Join([]string{
				remoteReadMarker + " exists 420",
				remoteReadDataBegin,
				encoded,
				remoteReadDataEnd,
			}, "\n")}, nil
		default:
			t.Fatalf("unexpected remote command %q", command)
			return shell.TrackedExecution{}, nil
		}
	}

	agent := &stubAgent{
		response: AgentResponse{
			Proposal: &Proposal{
				Kind:        ProposalEdit,
				PatchTarget: PatchTargetRemoteShell,
				Edit: &EditIntent{
					Target:     PatchTargetRemoteShell,
					Path:       "foo.txt",
					Operation:  EditInsertAfter,
					AnchorText: "INSERT BELOW HERE",
					NewText:    "alpha\nbeta\n",
				},
				Description: "Insert text below the marker.",
			},
		},
	}

	controller := New(agent, runner, reader, SessionContext{
		TrackedShell: TrackedShellTarget{PaneID: "%0"},
	})

	events, err := controller.SubmitAgentPrompt(context.Background(), "edit foo.txt remotely")
	if err != nil {
		t.Fatalf("SubmitAgentPrompt() error = %v", err)
	}
	last := events[len(events)-1]
	if last.Kind != EventProposal {
		t.Fatalf("expected patch proposal, got %#v", events)
	}
	payload := last.Payload.(ProposalPayload)
	if payload.Kind != ProposalPatch || payload.PatchTarget != PatchTargetRemoteShell {
		t.Fatalf("expected synthesized remote patch proposal, got %#v", payload)
	}
	if !strings.Contains(payload.Patch, "+alpha") || !strings.Contains(payload.Patch, "+beta") {
		t.Fatalf("expected synthesized remote diff with inserted lines, got %q", payload.Patch)
	}
}

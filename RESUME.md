# Resume

Current branch: `semantic-shell-bootstrap`

Latest commit:
- `a89d1b9` `Harden shell pane recovery and semantic tracking`

Current state:
- stream-backed semantic shell tracking is implemented and preferred via `osc_stream`
- stream files are generation-scoped and stale generations are pruned conservatively
- local nested-shell transition detection exists for `bash`, `zsh`, `ssh`, `docker exec -it`, `kubectl exec -it`, `sudo -i`, and similar interactive takeovers
- local nested-shell bootstrap is in place and guarded by `SHUTTLE_SEMANTIC_SHELL_V1_PID`
- manual foreground attach after `F2`/terminal handoff is implemented
- tracked shell pane recovery now updates controller and TUI state instead of hiding pane aliases only inside the observer
- controller/TUI/provider state now carries an explicit `TrackedShellTarget { session, pane }` instead of treating `TopPaneID` as the only ownership handle
- agent turn context now includes explicit `tracked_session` / `tracked_pane` metadata
- destructive tmux recovery now recreates a shell-only recovery session for handoff instead of a fake two-pane workspace
- full app startup still repairs a recovered one-pane session back into the normal two-pane workspace

What was validated manually:
- local nested shell flows: `bash`, `zsh`, `exit`
- remote shell flows worked in manual validation
- `F2` handoff now consistently returns to composer/chat
- destructive `exit exit` recovery works
- after recovery, `F2` now returns cleanly because the detach binding is restored
- recovery handoff now uses a single shell pane, so one `exit` returns to composer/chat

Important implementation points:
- `internal/shell/semantic_stream.go`
  - incremental OSC reducer for `OSC 133` / `OSC 7`
- `internal/shell/transition.go`
  - transition classification for nested/remote/container shell takeover
- `internal/shell/foreground_attach.go`
  - attach to manually started foreground commands after handoff
- `internal/shell/observer.go`
  - pane/session recovery, tracked pane resolution, semantic source precedence
- `internal/controller/controller.go`
  - controller now normalizes and syncs an explicit tracked shell target instead of relying on a single overloaded top-pane field
- `internal/controller/types.go`
  - `SessionContext` now carries `TrackedShellTarget`
- `internal/provider/responses.go`
  - agent context now exposes `tracked_session` and `tracked_pane`
- `internal/tui/handoff.go`
  - handoff config now explicitly tracks the shell pane it will attach to
- `internal/tui/model.go`
  - TUI syncs tracked shell session/pane state from the controller into workspace and handoff config
- `internal/tmux/workspace.go`
  - separate normal two-pane workspace bootstrap vs shell-only recovery bootstrap

Tests:
- `go test ./...` passes

Current worktree status intentionally left uncommitted:
- `ui-scratchpad.md`

Likely next slice:
1. define first-class per-execution pane ownership rules for future parallel command tracking
2. separate tracked shell ownership from transient handoff/attachment state in transcript and trace events, not just in controller/TUI memory
3. decide whether multiple monitors can share one tracked pane or whether pane ownership must become exclusive once parallel execution lands

Suggested restart prompt after `/new`:
- "Read `RESUME.md`, `implementation-plan.md`, and `shell-execution-strategy.md`. Continue on `semantic-shell-bootstrap` from commit `a89d1b9`. The semantic stream, nested-shell bootstrap, foreground attach, pane/session recovery, and explicit tracked-shell target state are in. Focus next on per-execution pane ownership and trace/transcript observability for future parallel command tracking. Leave `ui-scratchpad.md` alone."

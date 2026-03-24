# Resume

Current branch: `semantic-shell-bootstrap`

Latest pushed commit:
- `25bad88` `Clarify shell tracking ownership and recovery`

Current state:
- stream-backed semantic shell tracking is implemented and preferred via `osc_stream`
- stream files are generation-scoped and stale generations are pruned conservatively
- local nested-shell transition detection exists for `bash`, `zsh`, `ssh`, `docker exec -it`, `kubectl exec -it`, `sudo -i`, and similar interactive takeovers
- local nested-shell bootstrap is in place and guarded by `SHUTTLE_SEMANTIC_SHELL_V1_PID`
- manual foreground attach after `F2`/terminal handoff is implemented
- tracked shell pane recovery now updates controller and TUI state instead of hiding pane aliases only inside the observer
- controller/TUI/provider state now carries an explicit `TrackedShellTarget { session, pane }` instead of treating `TopPaneID` as the only ownership handle
- agent turn context now includes explicit `tracked_session` / `tracked_pane` metadata
- controller now has a first-class execution registry with `PrimaryExecutionID`, per-execution tracked shell metadata, and ownership mode fields, but serial command submission is still enforced
- command tracking remains single-owner for now: a second tracked shell command is rejected while another execution is active
- auto-continue prompt handling now prefers exactly one next command for clearly serial/ordered shell workflows instead of defaulting to "summarize and wait"
- non-actionable `proposal_kind:"answer"` responses no longer create hidden pending-proposal UI state
- plan cards are now informational only, not approval-style action cards
- continuation turns now ignore incidental replacement plans and emit an explicit completed-plan event when the agent declares the workflow complete
- destructive tmux recovery now recreates a shell-only recovery session for handoff instead of a fake two-pane workspace
- full app startup still repairs a recovered one-pane session back into the normal two-pane workspace

What was validated manually:
- local nested shell flows: `bash`, `zsh`, `exit`
- remote shell flows worked in manual validation
- `F2` handoff now consistently returns to composer/chat
- destructive `exit exit` recovery works
- after recovery, `F2` now returns cleanly because the detach binding is restored
- recovery handoff now uses a single shell pane, so one `exit` returns to composer/chat
- serial command-loop behavior now works end-to-end:
  - one proposal at a time
  - auto-continue after command completion
  - no hidden answer-proposal modal state
  - plan card no longer hangs after the agent declares workflow completion

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
  - execution registry + serial ownership enforcement live here
  - continuation-turn plan suppression/completion logic now lives here too
- `internal/controller/types.go`
  - `SessionContext` now carries `TrackedShellTarget`
  - `TaskContext` now exposes `PrimaryExecutionID` and `ExecutionRegistry`
- `internal/provider/responses.go`
  - agent context now exposes `tracked_session` / `tracked_pane` plus active execution registry metadata
  - runtime prompt now prefers serial one-command-at-a-time continuation when the transcript makes that intent clear
- `internal/tui/handoff.go`
  - handoff config now explicitly tracks the shell pane it will attach to
- `internal/tui/model.go`
  - TUI syncs tracked shell session/pane state from the controller into workspace and handoff config
  - plan cards are informational only; `Ctrl+G` / `Ctrl+E` are secondary continue-plan shortcuts, not approval actions
- `internal/tmux/workspace.go`
  - separate normal two-pane workspace bootstrap vs shell-only recovery bootstrap

Tests:
- `go test ./...` passes

Current worktree status intentionally left uncommitted:
- `ui-scratchpad.md`

Likely next slice:
1. harden command-tracking invariants as an explicit execution lifecycle/state machine
2. make execution ownership/reconciliation transitions first-class and observable for handoff, foreground attach, pane replacement, and session recreation
3. add integration-style tests for destructive recovery and repeated handoff/attach paths before reopening any parallel-command work

Suggested restart prompt after `/new`:
- "Read `RESUME.md`, `implementation-plan.md`, and `shell-execution-strategy.md`. Continue on `semantic-shell-bootstrap` from pushed commit `25bad88` plus local worktree changes. The semantic stream, nested-shell bootstrap, foreground attach, pane/session recovery, explicit tracked-shell target state, serial execution registry, and serial auto-continue hardening are in. Focus next on execution lifecycle invariants and integration-style command-tracking recovery tests. Leave `ui-scratchpad.md` alone."

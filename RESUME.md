# Resume

## Current Branch

- Current branch: `p2-runtime-integration`

## Current Status

- P2 is the active workstream.
- Shuttle now supports authoritative runtime selection across:
  - `builtin`
  - `auto`
  - `codex_sdk`
  - `codex_app_server`
- `pi` remains explicitly rejected for authoritative use.
- Runtime selection is exposed in `F10 -> Runtime` and persists across launches unless startup flags override it.
- `codex_sdk` remains the primary CLI-backed validation bridge.
- `codex_app_server` is now beyond the minimal ephemeral bridge:
  - it keeps a long-lived `codex app-server` process alive for the Shuttle runtime session
  - it reuses in-memory native thread bindings per task across ordinary continuation turns
  - `/compact` runs on the same live native thread
- retryable stale-thread and transient app-server failures now get one same-turn self-recovery attempt on a fresh native thread
- successful self-recovery is surfaced through runtime detail as a runtime note
- interactive TUI startup no longer hard-fails if the persisted explicit Codex runtime command is missing; Shuttle opens with a visible runtime error and uses builtin for that launch only
- tracked-shell capture is back to a usable state after the runtime work:
  - cwd/prompt tracking now survives directory changes like `cd completed` again
  - transcript result blocks strip leaked Shuttle transport/plumbing fragments instead of printing shell integration chatter
  - ANSI-preserving display capture is bounded more tightly to the tracked command result path
  - `KEYS>` mode now exits when the active fullscreen or awaiting-input execution settles
- cursory manual smoke tests now show both `codex_sdk` and `codex_app_server` can at least accept ordinary prompts and return agent responses in the TUI

## What Changed In This Session

- Added session/task identity to runtime requests so non-builtin runtimes can bind native state to a Shuttle task.
- Replaced the old fixed initial task id with generated unique task ids so fresh sessions and `/new` do not accidentally reuse stale native bindings.
- Replaced the broken persisted-thread-only app-server model with a long-lived live app-server session model.
- Implemented `codex_app_server` in-memory native thread reuse across turns on the shared app-server process.
- Implemented same-turn recovery for:
  - stale in-memory thread id on the live app-server session
  - retryable transient app-server process/transport failure such as EOF or broken pipe
- Added the startup fallback path for missing persisted Codex runtime commands in interactive TUI launch.
- Fixed the runtime-switch regression that could preserve the wrong tracked shell pane after controller rebuild.
- Reworked tracked-shell capture so prompt/cwd tracking survives directory changes without leaking Shuttle shell-integration transport text into transcript results.
- Tightened final controller/TUI result assembly so trailing prompts are stripped from result summaries and ANSI display output.
- Fixed `KEYS>` lifecycle cleanup so exiting fullscreen apps like `nano` returns the composer to the underlying `AGENT` or `SHELL` mode.
- Updated runtime docs and P2 tracking to match the new behavior.
- Added a manual runtime validation script for post-break testing.

## Important Files

- Runtime core:
  - [internal/agentruntime/runtime.go](/home/jsmith/source/repos/aiterm/internal/agentruntime/runtime.go)
  - [internal/agentruntime/codex_app_server.go](/home/jsmith/source/repos/aiterm/internal/agentruntime/codex_app_server.go)
  - [internal/agentruntime/runtime_storage.go](/home/jsmith/source/repos/aiterm/internal/agentruntime/runtime_storage.go)
- Controller wiring:
  - [internal/controller/controller_agent.go](/home/jsmith/source/repos/aiterm/internal/controller/controller_agent.go)
  - [internal/controller/controller_context.go](/home/jsmith/source/repos/aiterm/internal/controller/controller_context.go)
  - [internal/controller/controller.go](/home/jsmith/source/repos/aiterm/internal/controller/controller.go)
- App/runtime configuration:
  - [internal/app/app.go](/home/jsmith/source/repos/aiterm/internal/app/app.go)
- Shell tracking / tmux capture:
  - [internal/shell/observer.go](/home/jsmith/source/repos/aiterm/internal/shell/observer.go)
  - [internal/shell/semantic.go](/home/jsmith/source/repos/aiterm/internal/shell/semantic.go)
  - [internal/tmux/client.go](/home/jsmith/source/repos/aiterm/internal/tmux/client.go)
- TUI handoff / KEYS mode:
  - [internal/tui/model.go](/home/jsmith/source/repos/aiterm/internal/tui/model.go)
  - [internal/tui/model_state.go](/home/jsmith/source/repos/aiterm/internal/tui/model_state.go)
- Tests:
  - [internal/agentruntime/runtime_test.go](/home/jsmith/source/repos/aiterm/internal/agentruntime/runtime_test.go)
  - [internal/agentruntime/runtime_storage_test.go](/home/jsmith/source/repos/aiterm/internal/agentruntime/runtime_storage_test.go)
  - [internal/controller/controller_context_test.go](/home/jsmith/source/repos/aiterm/internal/controller/controller_context_test.go)
  - [internal/shell/observer_test.go](/home/jsmith/source/repos/aiterm/internal/shell/observer_test.go)
  - [integration/tmux_observer_test.go](/home/jsmith/source/repos/aiterm/integration/tmux_observer_test.go)
  - [internal/tui/execution_test.go](/home/jsmith/source/repos/aiterm/internal/tui/execution_test.go)
- Docs:
  - [BACKLOG.md](/home/jsmith/source/repos/aiterm/BACKLOG.md)
  - [inprocess/P2.md](/home/jsmith/source/repos/aiterm/inprocess/P2.md)
  - [inprocess/README.md](/home/jsmith/source/repos/aiterm/inprocess/README.md)
  - [inprocess/agent-runtime-design.md](/home/jsmith/source/repos/aiterm/inprocess/agent-runtime-design.md)
  - [inprocess/runtime-manual-test.md](/home/jsmith/source/repos/aiterm/inprocess/runtime-manual-test.md)

## Verified

- `env GOCACHE=/tmp/aiterm-go-build GOTMPDIR=/tmp go test ./internal/agentruntime ./internal/controller ./internal/app -count=1`
- `env GOCACHE=/tmp/aiterm-go-build GOTMPDIR=/tmp go test ./internal/shell ./internal/controller ./internal/tmux -count=1`
- `env GOCACHE=/tmp/aiterm-go-build GOTMPDIR=/tmp go test ./integration/... -run 'TestRunTrackedCommandPreservesCaptureAfterDirectoryChange|TestRunTrackedCommandUsesLocalManagedTransport' -count=1`
- `env GOCACHE=/tmp/aiterm-go-build GOTMPDIR=/tmp go test ./internal/tui -run 'TestTakeControlFinished|TestAwaitingInput|TestShiftTabDismissesAutoOpenedSendKeysWithoutReasserting|TestCommandResultClearsLiveShellTailPreview|TestCommandResultClearsSendKeysModeAndPreservesComposerMode|TestActiveExecutionTransitionToRunningClearsSendKeysMode' -count=1`
- Manual smoke test: both `codex_sdk` and `codex_app_server` were able to answer ordinary prompts in the TUI after the shell/runtime fixes.

## Current Problem / Remaining Gap

- `codex_app_server` recovery is now good enough for manual validation, but not yet fully promoted:
  - stale-thread recovery works
  - one retryable fresh-process recovery works
  - broader reconnect policy is still incomplete
  - failure classification is still string-matching based
  - native compaction semantics are still only validated through the normal Shuttle turn path
  - `auto` should still prefer `codex_sdk` until manual validation proves the app-server path is ready
- We still have not done the full runtime manual script end to end.
- This branch is ready for a focused code-review pass before more runtime promotion work.

## Next Step After Break

Start the next session as a code-review pass over this branch. Focus on:

1. `codex_sdk` / `codex_app_server` runtime ownership, persistence, and startup-fallback logic.
2. The shell-tracking hardening around `internal/shell/observer.go`, `internal/tmux/client.go`, and controller final result assembly.
3. TUI handoff / `KEYS>` lifecycle cleanup.

After review, continue with the runtime manual validation script:

- [inprocess/runtime-manual-test.md](/home/jsmith/source/repos/aiterm/inprocess/runtime-manual-test.md)

Recommended order:

1. Validate `codex_sdk` baseline authoritative-runtime behavior.
2. Validate runtime persistence through `F10 -> Runtime`.
3. Validate `codex_app_server` baseline turns.
4. Validate live same-task thread reuse.
5. Validate `/new`.
6. Validate `/compact`.
7. Validate the forced-process recovery scenario from the manual test.
8. Validate a patch flow under `codex_app_server`.

## Decision Gate After Review And Manual Test

If manual testing passes cleanly:

1. Update P2 docs with any observed discrepancies from review or manual testing.
2. Decide whether the next slice is:
   - stronger app-server reconnect/failure classification, or
   - first real codex CLI / app-server end-to-end harness coverage
3. Only then reconsider whether `auto` should ever prefer `codex_app_server`.

If review or manual testing finds a defect:

1. Reproduce using the smallest step from `inprocess/runtime-manual-test.md`.
2. Add a focused runtime/controller regression test for the specific failure.
3. Fix that defect before expanding the app-server surface further.

## Suggested Restart Prompt

- "Read `RESUME.md`, `BACKLOG.md`, `inprocess/P2.md`, and `inprocess/runtime-manual-test.md`. This branch now has authoritative `codex_sdk` and long-lived `codex_app_server` integration, startup fallback for missing Codex runtime commands, shell-tracking hardening after the runtime regressions, and KEYS-mode cleanup after fullscreen apps settle. Do a focused code review first, then continue with the runtime manual test results before changing runtime preference policy."

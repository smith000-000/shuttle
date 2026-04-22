# Runtime Manual Test

Manual validation for Shuttle's authoritative runtime paths.

This script is intended to prove the current P2 runtime contract in a real session before more app-server promotion work continues.

## Scope

These checks cover:
- `codex_sdk` as the primary CLI-backed authoritative runtime
- `codex_app_server` as the native app-server authoritative runtime
- runtime selection and persistence
- request-kind continuity across normal turns
- `codex_app_server` long-lived process reuse and per-task thread reuse
- `/compact` on the active native thread
- app-server self-recovery from stale thread bindings and transient process failure

These checks do not yet prove:
- `auto` should prefer `codex_app_server`
- `pi` parity
- long soak reliability over many hours

## Prerequisites

1. A working provider configuration in `env.sh`.
2. A compatible `codex` executable in `PATH`.
3. A clean disposable workspace for testing.

Recommended env:

```bash
source ./env.sh
which codex
codex --version
```

Expected:
- `codex` resolves to a real executable
- version is `0.118.0` or newer

Use an isolated Shuttle session for the test:

```bash
export SHUTTLE_SESSION=shuttle-runtime-manual
export SHUTTLE_TMUX_SOCKET=shuttle-runtime-manual
```

## A. `codex_sdk` Baseline

Launch Shuttle:

```bash
go run ./cmd/shuttle --tui
```

In Shuttle:

1. Press `F10`, open `Runtime`, select `codex_sdk`, save/apply.
2. Submit: `Give me a 3-step plan to inspect this repo and then propose exactly one safe read-only shell command.`
3. Approve the command if one is proposed.
4. Let Shuttle continue from the command result.
5. If a proposal appears, refine it once.
6. If an approval appears, refine it once.

Expected:
- runtime status shows `codex_sdk`
- the task behaves as one authoritative runtime session, not builtin fallback
- plan, proposal refinement, approval refinement, and continue-after-command all work
- no runtime failure/error is injected for ordinary continuation turns

## A1. Missing-CLI Startup Guard

This reproduces the startup failure you hit and verifies the new behavior.

1. Persist `codex_sdk` or `codex_app_server` as the selected runtime.
2. Temporarily remove `codex` from `PATH`, or point the stored runtime command to a nonexistent path.
3. Launch Shuttle in TUI mode:

```bash
go run ./cmd/shuttle --tui
```

Expected:
- Shuttle still opens
- transcript shows a startup error explaining that the configured runtime is unavailable
- runtime detail shows the selected runtime plus effective builtin fallback for that launch
- `F10 -> Runtime` shows the runtime health as unavailable so you can fix it without being locked out of the UI

Control check:

```bash
go run ./cmd/shuttle --agent "Say hello."
```

Expected:
- the noninteractive path still fails fast instead of silently falling back

## B. Runtime Selection Persistence

1. Exit Shuttle.
2. Relaunch `go run ./cmd/shuttle --tui`.
3. Open `F10 -> Runtime`.

Expected:
- `codex_sdk` is still selected
- the stored runtime command path is preserved unless overridden on startup

## C. `codex_app_server` Baseline

1. In `F10 -> Runtime`, switch to `codex_app_server`.
2. Submit: `Create a short 3-step plan, then propose one safe read-only command to inspect the current repo state.`
3. Approve the command if needed.
4. Let Shuttle continue.
5. Run `/compact`.

Expected:
- runtime status shows `codex_app_server`
- ordinary turns, continue-after-command, and `/compact` complete without switching runtimes
- if you inspect model/runtime detail, the selected and effective runtime remain `codex_app_server`
- when the task already has a bound native app-server thread, `/compact` uses native thread compaction instead of returning a Shuttle-authored summary
- ordinary app-server turns execute tools inside the runtime thread instead of surfacing Shuttle-owned command or patch proposals for every step

## C1. `codex_app_server` Approval And Refinement Flow

While still on `codex_app_server`:

1. Submit a prompt that is likely to produce an approval, for example:
   `Propose one medium-risk workspace-changing command, but ask for approval before running it.`
2. If an approval card appears, choose `reject`.
3. Submit another prompt that should produce an approval again.
4. This time choose `refine`, add a note such as `Add a dry-run first.`, and submit the refinement.

Expected:
- approve/reject decisions resolve cleanly without switching runtimes
- refining a runtime-owned approval returns to agent mode instead of failing immediately
- the follow-up refinement turn stays on `codex_app_server`
- no silent builtin fallback occurs during approval handling

## D. Native Thread Reuse On The Live App-Server Session

While still on `codex_app_server`:

1. Submit a prompt that will take multiple turns, for example:
   `Inspect the repo, summarize one architecture constraint, then propose one read-only command, and continue after it.`
2. Approve the proposed command.
3. Submit another prompt in the same task immediately after the first continuation completes.

Expected:
- the second turn succeeds on the same selected `codex_app_server` runtime
- there is no `thread not found` failure between same-task turns
- `/compact` later in that same task still works and does not switch to a fresh thread

## E. `/new` Reset Semantics

1. While still on `codex_app_server`, run `/new`.
2. Submit a new prompt in the new task.

Expected:
- the new task starts cleanly
- the old task's native thread is not reused accidentally
- the new task can continue across multiple turns without `thread not found`

## F. Forced Stale-Thread Recovery

This path is covered by unit tests, but it is not cleanly reproducible from the normal UI without extra debug hooks because the live thread map is in memory and tied to the running app-server process.

Manual expectation:
- if the live app-server process ever loses thread state mid-session, Shuttle should retry once on a fresh native thread instead of requiring an immediate manual retry
- runtime detail should include a recovery note

Practical manual guidance:
- do not block on forcing this case from the UI today
- rely on the existing unit coverage for the stale-thread branch and focus manual effort on baseline turns, `/compact`, patch continuation, startup fallback, and transient process failure
- note that `/compact` is intentionally stricter: if the bound native thread is gone, Shuttle should now surface an explicit compaction failure instead of silently replaying the compact on a fresh thread

## G. Forced Process-Retry Recovery

This is the easiest way to simulate a transient app-server failure without changing code:

1. Stay on `codex_app_server`.
2. Submit a prompt.
3. As soon as the app-server turn is in flight, temporarily break the `codex` command path from another shell.
   Example: move the binary or point `SHUTTLE_RUNTIME_COMMAND` at a wrapper that fails once, then restore it.
4. Submit again after restoring the command path.

Expected:
- a transient app-server process failure should not force a runtime switch
- when the failure is retryable, Shuttle retries once with a fresh native thread
- runtime detail includes a runtime note indicating recovery from a transient app-server process failure
- exception: if the failure happens during a native `/compact`, Shuttle should surface an explicit compaction failure because the old runtime context was lost

## G1. Approval Resume Failure Handling

This path is specifically about a runtime-owned approval that cannot be resumed after the approval card is already on screen.

1. Stay on `codex_app_server`.
2. Submit a prompt that produces an approval card.
3. Before approving or rejecting it, intentionally break the live app-server process.
4. Then approve or reject the pending runtime approval.

Expected:
- Shuttle does not silently fall back to builtin
- Shuttle surfaces an explicit error saying the suspended approval turn was lost
- the task remains recoverable by retrying the task or switching runtimes explicitly
- a stale or broken suspended approval turn does not get silently replayed as if it were still safe to resume

## H. Patch Continuation Path

Still under `codex_app_server`:

1. Ask for a trivial local file edit that should be surfaced as a patch.
   Example:
   `Create a file named runtime_manual_test.txt containing one line: hello runtime test`
2. Approve/apply the patch.
3. Let Shuttle continue after patch apply.

Expected:
- patch proposal works
- apply works
- continue-after-patch stays on `codex_app_server`
- no builtin fallback occurs

## I. Failure Threshold

The current expected policy is:
- one automatic retry for retryable `codex_app_server` transport/process failures
- one automatic recreate when an in-memory native thread is stale on the live app-server session
- no silent fallback to builtin

Expected failure behavior when recovery does not work:
- Shuttle stops the turn with an explicit error
- the user must retry or switch runtime explicitly
- selected runtime remains visible as `codex_app_server`

## Pass Criteria

The runtime work is functionally ready for the next stage when:
- `codex_sdk` passes the baseline authoritative-runtime flow
- `codex_app_server` passes baseline turns, `/compact`, and `/new`
- live same-task native thread reuse is observable for one task
- stale-thread recovery succeeds in the same turn
- patch continuation still works under `codex_app_server`
- no silent builtin fallback is observed

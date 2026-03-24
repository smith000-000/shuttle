# Shuttle Shell Tracking Architecture

## Purpose
Describe the current shell tracking architecture on `semantic-shell-bootstrap` so contributors can reason about command monitoring, semantic shell signals, pane ownership, handoff, and recovery without reverse-engineering the implementation from `internal/shell`, `internal/controller`, and `internal/tui`.

This note is intentionally implementation-facing. It describes how the current system works, where the authoritative state lives, and what tradeoffs still exist.

---

# 1. Core Model

Shuttle tracks one primary shell target at a time.

That tracked shell target is:
- a tmux session name
- a tmux pane ID
- represented in controller state as `TrackedShellTarget`

Current design rule:
- the tracked shell target is the pane Shuttle reads from, writes to, and monitors
- the TUI is not the source of truth for that target
- the shell observer is the source of truth for low-level pane recovery
- the controller is the source of truth for product state built on top of that recovered pane identity

This is the current foundation for future parallel command tracking. Right now the product still behaves as a single tracked-shell system even though some internals are now explicit enough to split later.

---

# 2. Main Components

## 2.1 tmux Client and Workspace

Relevant files:
- `internal/tmux/client.go`
- `internal/tmux/workspace.go`

Responsibilities:
- talk to the real tmux server and socket
- create or repair the Shuttle workspace
- identify panes in a session
- bootstrap either:
  - the normal two-pane workspace
  - a shell-only recovery session

Important distinction:
- normal startup uses `BootstrapWorkspace(...)`
- destructive recovery uses `BootstrapShellSession(...)`

That distinction exists because after a broken handoff recovery there is no real embedded bottom TUI pane inside tmux. Recreating a fake two-pane tmux session caused confusing double-`exit` behavior, so recovery now prefers a one-pane shell session.

## 2.2 Shell Observer

Relevant file:
- `internal/shell/observer.go`

The observer is the low-level shell integration layer.

Responsibilities:
- capture pane output
- capture prompt context
- send commands into the tracked pane
- run tracked command monitors
- attach to a manually started foreground command
- recover from stale pane IDs
- recreate missing tmux sessions when configured with a session ensurer
- manage semantic shell source precedence

The observer knows about shell and tmux mechanics. It should not own transcript policy or TUI rendering decisions.

## 2.3 Semantic Collectors

Relevant files:
- `internal/shell/semantic_collect.go`
- `internal/shell/semantic_stream.go`
- `internal/shell/semantic.go`

Semantic shell state is consumed from multiple sources with explicit precedence:
- `osc_stream`
- `osc_capture`
- `state_file`
- heuristics

Current preferred source is `osc_stream`, backed by `tmux pipe-pane -O`.

Important detail:
- `pipe-pane -O` produces a cumulative stream
- Shuttle reduces that stream incrementally instead of rescanning the full output buffer snapshot-style

The stream collector is generation-scoped:
- each observer instance gets its own runtime file
- old generation files are ignored
- dead generations are pruned conservatively

That avoids stale semantic events after crash/restart.

## 2.4 Controller

Relevant files:
- `internal/controller/controller.go`
- `internal/controller/types.go`

The controller translates shell reality into product state.

Responsibilities:
- hold `SessionContext`
- hold `TaskContext`
- create and update `CommandExecution`
- talk to the agent/provider layer
- emit transcript events
- reconcile shell state after handoff
- synchronize the tracked shell target before reads and writes

Important state:
- `SessionContext.TrackedShell`
- `SessionContext.TopPaneID`
- `TaskContext.PrimaryExecutionID`
- `TaskContext.ExecutionRegistry`
- `TaskContext.CurrentExecution`
- `TaskContext.LastCommandResult`

Current rule:
- `TrackedShellTarget` is the explicit ownership field
- `TopPaneID` still exists as a compatibility field for older call sites and context formatting
- the controller normalizes them so they stay aligned

## 2.5 TUI and Handoff

Relevant files:
- `internal/tui/model.go`
- `internal/tui/handoff.go`

Responsibilities:
- render transcript and shell state
- start `F2` handoff into the tracked shell pane
- route `KEYS>` and local interrupts to the tracked shell pane
- mirror controller-tracked pane/session updates into local UI state

Important rule:
- the TUI does not decide which pane is authoritative
- it asks the controller for the current tracked shell target and mirrors that into handoff config

---

# 3. Command Tracking Modes

The current system supports three practical categories of shell tracking.

## 3.1 Shuttle-Started Tracked Commands

Path:
- controller submits command
- observer injects tracked transport
- monitor watches pane output and semantic state
- controller turns the final monitor result into transcript events

This is the strongest path because Shuttle knows:
- which command it launched
- when tracking started
- what command ID and sentinels belong to it

## 3.2 Manually Started Foreground Commands

Path:
- user starts a command directly in the shell pane or during handoff
- Shuttle returns from `F2`
- controller tries `AttachForegroundCommand(...)`
- observer creates a monitor from the current foreground state

This path is weaker than fully tracked transport because the command was not launched by Shuttle, but it is good enough to:
- detect that something is still running
- keep showing output
- reconcile completion back into `LastCommandResult`

## 3.3 Context-Transition Commands

Examples:
- `bash`
- `zsh`
- `ssh`
- `docker exec -it`
- `kubectl exec -it`
- `sudo -i`

These are treated specially because they are more like shell ownership transitions than normal short commands.

Current behavior:
- detect likely transition kind
- suppress inappropriate local semantic assumptions when control moved elsewhere
- bootstrap local nested shells conservatively after prompt settlement
- remain heuristic-only for remote/container cases unless later bootstrap work expands coverage

---

# 4. Semantic Shell Model

## 4.1 Why Semantic Signals Exist

Prompt scraping alone is too ambiguous for:
- exit-code accuracy
- cwd tracking
- differentiating prompt return from quiet long-running commands
- nested shell transitions

So Shuttle consumes:
- `OSC 133` prompt and command lifecycle markers
- `OSC 7` cwd markers

## 4.2 Current Semantic Lifecycle

The current reducer tracks:
- prompt start
- command line start
- command execution start
- command finish with exit code
- cwd updates

The reducer is designed for split and incremental marker delivery rather than assuming markers arrive as complete lines in one snapshot.

## 4.3 Bootstrap Scope

Current bootstrap is intentionally conservative.

Implemented:
- local shell integration for supported local shells
- local nested-shell reintegration when a child `bash` or `zsh` becomes the new prompt owner

Not yet the default:
- remote semantic bootstrap
- container semantic bootstrap
- persistent RC-file installation

The design intent is:
- semantic bootstrap should improve signal quality
- semantic bootstrap must never be required for correctness
- failure should degrade back to heuristic monitoring

---

# 5. Tracked Shell Ownership

## 5.1 Why This Became Explicit

Earlier code paths overloaded one field, `TopPaneID`, for multiple jobs:
- shell read/write target
- handoff target
- startup workspace top pane
- recovered pane alias

That was workable for a single stable session but broke down under:
- pane recreation
- session recreation
- `F2` handoff recovery
- future plans for parallel command tracking

## 5.2 Current Ownership State

The controller now carries:

```go
type TrackedShellTarget struct {
    SessionName string
    PaneID      string
}
```

Current meaning:
- `SessionName`: the tmux session that currently owns the tracked shell pane
- `PaneID`: the authoritative pane for shell observation and shell input

Current invariants:
- controller synchronizes this target before important shell reads and writes
- TUI handoff config mirrors this target
- provider context includes it as `tracked_session` and `tracked_pane`
- compatibility field `TopPaneID` remains aligned with `TrackedShellTarget.PaneID`

## 5.3 What This Does Not Mean Yet

This does not yet mean Shuttle supports arbitrary multi-pane or multi-command ownership.

Still true today:
- one tracked shell target is authoritative at a time
- one active execution is authoritative in controller state at a time
- a second tracked shell command is rejected while another execution is active

This is groundwork, not full parallelism.

---

# 6. Recovery Model

## 6.1 Pane Recovery

If a pane ID goes stale but the tmux session still exists:
- the observer lists panes in the session
- selects the top pane
- aliases the old pane ID to the replacement
- retries the operation

This covers:
- capture
- pane info
- send-keys
- semantic `pipe-pane` setup

## 6.2 Session Recovery

If tmux reports:
- `no server running`
- `can't find session`

then the observer or handoff path can call a session ensurer that:
- recreates a shell session
- restores the detach key binding
- returns a usable top pane again

Current recovery shape:
- shell-only recovery session during destructive recovery
- full two-pane layout only on normal startup

## 6.3 Handoff Recovery

`F2` handoff uses the same tracked shell identity and recovery ideas:
- resolve the current tracked pane
- recover the session if needed
- attach to the session
- rely on the detach key to return to Shuttle

This fixed a class of bugs where:
- the shell pane died
- tmux rebuilt the session
- Shuttle still tried to attach to a dead pane or wrong socket

---

# 7. Data Flow

## 7.1 Tracked Command Path

1. Controller syncs the tracked shell target.
2. Observer ensures local semantic integration if needed.
3. Observer injects tracked transport into the tracked pane.
4. Monitor reads pane output, pane metadata, and semantic state.
5. Controller converts monitor snapshots into `CommandExecution`.
6. Controller emits transcript events and updates `LastCommandResult`.
7. TUI renders the result and mirrors any updated tracked shell target.

## 7.2 Handoff and Resume Path

1. TUI takes control with `F2` using the current tracked shell target.
2. User interacts directly with the shell pane.
3. On return, controller first attempts prompt-return reconciliation for an existing active execution.
4. If no owned execution reconciles, controller tries to attach to a foreground command.
5. Updated shell context and results flow back into transcript state.

## 7.3 Agent Context Path

1. Controller gathers `SessionContext`, `TaskContext`, and recent shell output.
2. Provider formatter includes prompt, cwd, session, tracked shell target, and execution metadata.
3. Agent response comes back as structured message/plan/proposal/approval.
4. Controller decides whether that becomes a shell command, approval card, or transcript-only output.

---

# 8. Current Sharp Edges

The current architecture is much stronger than the earlier one-off fixes, but a few limits are still real.

- parallel command tracking is not implemented yet
- controller projects a single primary `CurrentExecution` even though it now keeps an internal execution registry
- serial submission is enforced; the registry is future-facing ownership scaffolding, not permission for overlap
- some compatibility code still carries `TopPaneID` alongside `TrackedShellTarget`
- remote/container semantic bootstrap is intentionally incomplete
- interactive/fullscreen classification still relies partly on tmux metadata and heuristics
- semantic signal quality still depends on shell support and tmux behavior

Contributors should avoid assuming the current tracked-shell ownership model already solves multi-execution scheduling. It only makes the ownership boundary explicit enough to build on safely.

---

# 9. Contributor Rules of Thumb

- Do not add new code that reads or writes the shell pane by assuming startup `%0` is still valid.
- Prefer controller-synced tracked-shell state over ad hoc pane IDs in UI code.
- Treat semantic bootstrap as best-effort signal improvement, not a correctness prerequisite.
- Keep shell recovery conservative. Rebind and retry; do not invent state silently.
- If a change affects handoff, verify both directions:
  - Shuttle to tmux
  - tmux back to Shuttle
- If a change affects nested or remote shells, verify prompt settlement and result reconciliation separately.

---

# 10. Related Files and Documents

Implementation:
- `internal/shell/observer.go`
- `internal/shell/foreground_attach.go`
- `internal/shell/semantic.go`
- `internal/shell/semantic_collect.go`
- `internal/shell/semantic_stream.go`
- `internal/shell/transition.go`
- `internal/controller/controller.go`
- `internal/controller/types.go`
- `internal/tui/model.go`
- `internal/tui/handoff.go`
- `internal/tmux/workspace.go`

Related docs:
- [architecture.md](architecture.md)
- [protocol-shell-observation.md](protocol-shell-observation.md)
- [shell-execution-strategy.md](shell-execution-strategy.md)
- [implementation-plan.md](implementation-plan.md)

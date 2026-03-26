# Shuttle Runtime Management Design

Current status:
- Shuttle now derives a stable workspace ID from the absolute project path
- Shuttle now defaults to a managed tmux socket under runtime state and a derived internal session name
- explicit `--socket` / `--session` overrides still exist for development and integration work
- runtime registry, reconciliation policy, and lifecycle subcommands are still pending

## Purpose
Define how Shuttle should manage tmux sockets, sessions, workspace identity, and crash recovery in a release build so users do not have to think about `--socket` and `--session` flags.

This document covers the operational layer between "launch Shuttle" and "attach to an existing two-pane workspace."

---

# 1. Problem

Historically the dev workflow used explicit tmux identifiers such as:

```bash
go run ./cmd/shuttle --socket shuttle-dev --session shuttle-dev --tui
```

The default launch path is now derived automatically, but that older explicit model still illustrates the remaining release-UX problems:
- users should not need to understand tmux server sockets
- users should not need to invent stable session names
- crashes and stale tmux state need structured cleanup and recovery
- multiple projects need predictable workspace identity without collisions

In a packaged release, socket and session naming should be internal runtime details.

---

# 2. Core Design Rule

Treat the workspace ID as the product-level identity.

Everything else is derived from it:
- tmux socket path
- tmux session name
- pane registry
- persisted session metadata

Users should think in terms of:
- "start Shuttle here"
- "resume the Shuttle workspace for this repo"
- "stop Shuttle"

They should not think in terms of:
- raw tmux socket names
- raw tmux session names

---

# 3. Runtime Identity Model

## 3.1 Workspace ID

Each Shuttle workspace should have a stable workspace ID derived from:
- normalized project path
- optional host identifier
- optional future scope dimensions such as git worktree

Recommended first implementation:

```text
workspace_id = hash(abs_project_path)
```

That keeps identity stable across normal restarts without requiring user input.

## 3.2 tmux Server Strategy

Release builds should default to one managed Shuttle tmux server, not one tmux server per launch.

Recommended default:
- one Shuttle-managed socket path
- multiple tmux sessions inside that server, one per workspace

Why:
- easier to inspect and debug
- easier to clean up stale sessions
- simpler runtime reconciliation
- fewer moving pieces than one socket per project

## 3.3 Session Naming

Session names should be derived from the workspace ID.

Recommended format:

```text
session_name = shuttle_<short-workspace-id>
```

Human-readable optional metadata may be stored in the registry, but the session name itself should stay compact and safe for tmux.

## 3.4 Socket Path

The socket path should be owned by Shuttle and stored in a runtime/state directory.

Preferred order:
1. `$XDG_RUNTIME_DIR/shuttle/tmux.sock`
2. `$TMPDIR/shuttle-$UID/tmux.sock`
3. `~/.local/state/shuttle/tmux.sock`

The product should create this path automatically if it does not exist.

---

# 4. Runtime Registry

## 4.1 Purpose

Shuttle should persist a local runtime registry so it can reconcile desired state with actual tmux state after:
- app restart
- tmux server restart
- app crash
- stale registry entries

## 4.2 Initial Storage

The first implementation can use a local JSON file or SQLite table under:

```text
~/.local/state/shuttle/
```

Recommended eventual direction:
- SQLite, because it fits session state, transcript state, and provider profiles well

## 4.3 Registry Fields

Minimum registry record:

```text
workspace_id
project_path
socket_path
session_name
top_pane_id
bottom_pane_id
layout_version
last_seen_at
status
```

Useful additional fields:
- hostname
- git root
- branch or worktree hint
- last launch version
- crash recovery marker

## 4.4 Status Values

Recommended early status set:
- `active`
- `detached`
- `stale`
- `reconciling`
- `failed`

---

# 5. Startup Reconciliation

On launch, Shuttle should reconcile registry state with real tmux state.

## 5.1 Normal Flow

1. Resolve workspace ID from the current project path.
2. Load the runtime registry.
3. Ensure the managed Shuttle tmux server exists.
4. Check whether the recorded session exists.
5. If it exists, inspect panes and layout.
6. If it is healthy, reattach and continue.
7. If it is missing or unhealthy, recreate it and update the registry.

## 5.2 Stale Registry Flow

If the registry says a workspace exists but tmux does not:
- mark the record `stale`
- recreate the session cleanly
- write the new pane IDs

## 5.3 Partial Failure Flow

If the tmux session exists but pane IDs or layout are wrong:
- attempt rediscovery
- if rediscovery fails, rebuild the layout in place if safe
- if in-place repair is unsafe, destroy only the Shuttle-managed session and recreate it

## 5.4 App Crash Flow

If Shuttle crashes but tmux is still alive:
- the next launch should reconnect to the same session
- pane IDs should be rediscovered, not blindly trusted from the registry

## 5.5 tmux Crash Flow

If the tmux server died:
- recreate the server
- recreate the Shuttle session
- preserve other persisted context where possible

---

# 6. Concurrency and Locking

Shuttle should prevent two processes from racing to manage the same workspace.

## 6.1 Workspace Lock

Each workspace should have a lock file or DB lock keyed by workspace ID.

Goals:
- prevent duplicate "start" races
- prevent conflicting pane repair logic
- ensure one authority updates the registry at a time

## 6.2 Lock Behavior

Recommended behavior:
- acquire lock before mutating runtime state
- short timeout for normal startup
- if lock holder is stale, detect and recover

---

# 7. Layout Versioning

The tmux layout should be versioned.

Why:
- future TUI or pane changes may require controlled migration
- old registry entries may point at a layout that no longer matches current assumptions

Recommended field:

```text
layout_version = 1
```

If the app expects a newer layout version than the registry/session provides:
- run migration if possible
- otherwise rebuild the Shuttle session

---

# 8. User-Facing Lifecycle Commands

Release builds should move toward product-level lifecycle commands.

Recommended initial command set:
- `shuttle start`
- `shuttle resume`
- `shuttle stop`
- `shuttle list`
- `shuttle cleanup`
- `shuttle doctor`

## 8.1 Semantics

`start`
- create or attach to the workspace for the current project

`resume`
- explicitly reconnect to the last known workspace for the current project

`stop`
- stop the Shuttle app and optionally the managed session

`list`
- show known workspaces and their status

`cleanup`
- remove stale sessions and stale registry entries

`doctor`
- inspect socket path, tmux health, registry health, and pane consistency

---

# 9. Cleanup Policy

Shuttle should clean up aggressively enough to avoid stale runtime junk, but not so aggressively that it destroys useful sessions.

Recommended rules:
- do not kill active sessions on normal restart
- mark missing sessions stale before deleting metadata
- allow explicit `cleanup` to remove stale state
- only auto-delete clearly dead records after a conservative age threshold

---

# 10. Release Defaults vs Dev Overrides

## Release Defaults

Release behavior should:
- hide raw tmux socket naming
- derive workspace identity from project path
- use the managed Shuttle tmux server automatically

## Dev Overrides

Development flags should remain:
- `--socket`
- `--session`

But they should be treated as debugging and integration-test tools, not primary product UX.

---

# 11. Recommended Execution Sequence

This work should happen after provider onboarding begins to stabilize, because both systems touch persistent local state.

Recommended order:
1. runtime registry storage
2. workspace ID derivation
3. managed tmux socket path
4. startup reconciliation logic
5. lifecycle commands
6. cleanup and doctor tools

---

# 12. Immediate Next Slice

The first implementation slice is now partially complete:

Implemented:
1. add a `workspace_id` derivation helper
2. replace default ad hoc socket/session values with derived managed defaults
3. keep `--socket` and `--session` as explicit overrides

Still next:
4. add a runtime registry record type under local persistence
5. add startup reconciliation for "session exists" vs "session missing"

That keeps the release architecture moving away from manual tmux naming while leaving lifecycle and recovery work to the next slice.

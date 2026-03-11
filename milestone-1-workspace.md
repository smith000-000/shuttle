# Milestone 1 Plan: Workspace and Pane Control

## Goal
Implement the tmux substrate that Shuttle will depend on for every later milestone.

This milestone does not attempt to solve shell observation, providers, or the full TUI. It is strictly about reliable workspace creation, pane discovery, and command injection.

## Scope
Included:
- create or attach to a named tmux session or workspace
- create a top shell pane and bottom Shuttle pane
- preserve pane IDs for later control
- apply the default layout
- inject plain shell input into the top pane

Excluded:
- sentinel protocol
- output parsing
- transcript rendering
- approvals
- provider integration

---

# 1. Deliverables

## 1.1 CLI Entry Point
- `cmd/shuttle/main.go` starts the app and calls the tmux workspace bootstrap path.

## 1.2 tmux Client Wrapper
- a small wrapper around tmux commands
- explicit methods instead of scattered shell calls
- central handling for command errors and parsing

## 1.3 Workspace Bootstrap
- create a named session if it does not exist
- attach to the existing session if it does
- create or identify the two panes
- assign the pane roles `top-shell` and `bottom-app`

## 1.4 Pane Discovery
- discover pane IDs and session metadata on startup
- verify that the stored or discovered pane IDs still exist
- fail with a clear error if the workspace is inconsistent

## 1.5 Command Injection
- send simple text commands into the top pane
- support a trailing Enter for execution
- keep injection logic isolated from later sentinel logic

---

# 2. Implementation Tasks

## Task 1. Define tmux command surface
Create a narrow tmux interface that covers only what this milestone needs:
- create session
- list panes
- split pane
- resize pane
- set pane title if useful
- send keys

Keep the response parsing explicit and testable.

## Task 2. Define workspace state model
Add a small struct for:
- session name
- window ID if needed
- top pane ID
- bottom pane ID

This should be enough to bootstrap later milestones without introducing persistence yet.

## Task 3. Implement create-or-attach flow
Behavior:
- if the Shuttle session does not exist, create it
- if it exists, inspect it and reuse it
- if it exists but is malformed, return a clear error instead of trying to self-heal in surprising ways

## Task 4. Implement pane layout logic
Behavior:
- ensure exactly two panes exist for the workspace
- assign the top pane as the shell pane
- assign the bottom pane as the app pane
- apply the default approximately 70/30 layout

## Task 5. Implement command injection smoke path
Behavior:
- provide one code path that sends a command string to the top pane
- optionally append Enter to execute
- use this for manual smoke testing before any protocol work exists

## Task 6. Add tests
Unit tests:
- tmux output parsing
- workspace state validation

Integration tests:
- create workspace in isolated tmux server
- rediscover pane IDs
- inject `echo hello` into the top pane

---

# 3. Suggested File Targets

```text
cmd/shuttle/main.go
internal/tmux/client.go
internal/tmux/workspace.go
internal/tmux/panes.go
internal/tmux/inject.go
integration/tmux_workspace_test.go
```

This is a starting point, not a rigid requirement. The important thing is keeping tmux concerns together and out of the TUI layer.

---

# 4. Acceptance Criteria

- Running Shuttle creates or attaches to a predictable tmux workspace.
- The workspace contains a top shell pane and a bottom app pane.
- Pane IDs can be discovered on a fresh start and on restart.
- The default layout is applied consistently enough to preserve the top-pane and bottom-pane mental model.
- A command injected by Shuttle appears and executes in the top pane.

---

# 5. Manual Test Procedure

1. Start Shuttle against an isolated tmux server.
2. Confirm the session is created.
3. Confirm two panes exist in the expected layout.
4. Run a controller-triggered injection of `echo hello`.
5. Verify the command appears in the top pane and executes there.
6. Restart Shuttle and verify pane rediscovery still works.

---

# 6. Failure Modes to Expect

- tmux output parsing may be brittle if it relies on human-oriented output formats.
- pane rediscovery may fail if the workspace changes outside Shuttle.
- injection may target the wrong pane if IDs are not validated carefully.

The right response is to make those failures explicit and easy to diagnose, not to add recovery logic too early.

---

# 7. Done Means

This milestone is complete when Shuttle can reliably control the workspace substrate without any hand-edited tmux setup.

If we cannot create, rediscover, and inject reliably at this stage, later milestones should stop until that is fixed.

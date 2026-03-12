# Shuttle Implementation Plan

## Purpose
Translate the product docs into an execution plan that is practical for a small team, explicit about debugging strategy, and sequenced to prove the risky parts before building polish.

## Current Status
As of March 11, 2026, the implementation state is:
- Milestone 0: complete
- Milestone 1: complete
- Milestone 2: complete
- Milestone 3: materially complete for `P0`, including transcript drill-down, scrolling, composer history, approvals, refine flow, and the compact two-pane TUI shell
- Milestone 4: complete for the mock-runtime path
- Milestone 5: in progress

Milestone 5 currently includes:
- provider profile and resolver scaffolding
- backend/auth abstraction layers
- provider factory wiring
- one real `responses_http` path for the standard OpenAI API endpoint with API-key auth
- `httptest` coverage for the OpenAI-compatible adapter

Milestone 5 still needs:
- OpenRouter verification and preset-specific tests
- onboarding and health checks
- saved provider profiles
- Codex CLI delegation path
- provider switching UI
- release-grade runtime management for socket/session lifecycle and crash recovery

## Guiding Decisions
- Build `P0` only first. That means Epics 1 through 4 in [requirements-mvp.md](requirements-mvp.md).
- Prove the shell substrate before investing in the full TUI.
- Keep the first controller implementation simple and observable rather than abstract and clever.
- Use a mock provider until the shell loop and approvals are stable.
- Treat tmux and shell integration as the primary engineering risk, not the model backend.

---

# 1. Delivery Strategy

## 1.1 What We Are Actually Trying to Prove
The product is real when Shuttle can:
1. create or attach to a two-pane tmux workspace
2. observe the top shell pane
3. inject a command into that exact pane
4. track the command start, output, end, and exit code
5. do that even after the user SSHs from the top pane into a remote shell

Everything else is downstream of that proof.

## 1.2 Scope Lock for First Pass
Included:
- workspace creation and pane discovery
- command injection into the top pane
- rolling shell observation
- sentinel parsing
- a minimal transcript-driven TUI
- Agent mode, Shell mode, and approval flow
- a mock provider, then a real provider

Deferred until the shell loop is stable:
- full persistence
- rich settings UI
- plugin and extension work
- worktree inspection features
- deeper project-aware context gathering

---

# 2. Milestones

## Milestone 0. Repo Bootstrap

### Goal
Create a Go repository skeleton and local dev workflow that is easy to run and debug.

### Deliverables
- `go.mod`
- `cmd/shuttle/main.go`
- basic config loading
- structured logging setup
- `Makefile` or equivalent task runner targets
- a tmux server naming convention for local dev, such as `shuttle-dev`

### Exit Criteria
- `go test ./...` runs cleanly
- `go run ./cmd/shuttle` starts a no-op stub
- logs can be written to a predictable local file for debugging

## Milestone 1. Workspace and Pane Control

### Goal
Own the tmux substrate reliably before building observation or agent logic.

### Deliverables
- create or attach to a Shuttle workspace
- create or discover top and bottom panes
- identify pane IDs and relevant session metadata
- set and preserve a default layout
- inject plain shell text into the top pane

### Exit Criteria
- Shuttle can create the two-pane layout repeatedly without manual cleanup
- the controller can rediscover pane IDs after restart
- a manual command injected from the controller appears in the top pane

Detailed plan: [milestone-1-workspace.md](milestone-1-workspace.md)

## Milestone 2. Observation and Sentinel Tracking

### Goal
Prove that Shuttle can observe and track controller-driven commands end to end.

### Deliverables
- rolling capture of top-pane output
- on-demand pane snapshot capture
- sentinel begin and end marker injection
- command lifecycle record with command ID and exit status
- structured event emission to logs or stdout

### Exit Criteria
- an injected `pwd` can be tracked from submission to exit code
- output is attached to the correct command record
- the flow works in a normal local shell and through a simple SSH session

## Milestone 3. Minimal TUI Shell

### Goal
Put a thin Bubble Tea interface over the working substrate without adding feature sprawl.

### Deliverables
- transcript panel
- composer
- mode indicator
- key hints
- approval card rendering
- shell event rendering

### Exit Criteria
- a user can type in Agent mode and Shell mode
- shell events render as structured transcript entries
- approval requests can be accepted, rejected, or refined

### Deferred Follow-Up
- Add a dedicated "full tmux view" action for temporarily handing the terminal over to the live tmux session when the user needs to interact with fullscreen TUIs such as `btop`, `vim`, or `less`.
- That flow should likely zoom or otherwise prioritize the top pane, then return cleanly to the Bubble Tea interface after detach without leaving the session in a bad state.
- Do not treat fullscreen top-pane TUIs as part of the normal tracked-command workflow until that escape hatch exists and is reliable.
- Add app-owned composer history navigation with Up and Down arrows, scoped separately for Agent mode and Shell mode.
- Investigate a shell-aware submission path that avoids polluting the user's normal shell history where possible, with explicit degraded behavior when the active shell or SSH target cannot guarantee that.

## Milestone 4. Agent Workflow with Mock Provider

### Goal
Prove the interaction loop without mixing in real API complexity.

### Deliverables
- controller state machine for tasks
- mock provider that returns canned plans, commands, and approval requests
- approval flow wired to actual command injection
- refine flow returning to a seeded composer

### Exit Criteria
- a user can ask for help, receive a mock plan, approve it, and see a real shell command execute
- the task loop continues until completion or cancellation

Detailed design note: [agent-runtime-design.md](agent-runtime-design.md)

## Milestone 5. Real Provider Integration

### Goal
Replace the mock provider with a real provider while preserving the same controller contract.

### Deliverables
- provider interface
- one real provider implementation
- profile-based model selection
- request and response normalization

### Current State
Implemented now:
- provider resolution and factory wiring in the app startup path
- one real OpenAI-compatible Responses adapter using API-key auth
- normalized mapping from structured provider output into Shuttle `AgentResponse`

Next:
- validate and harden the OpenRouter preset
- add provider detection and health checks
- add profile persistence
- add the Codex CLI bridge
- add runtime-management work so release builds do not expose raw tmux socket/session details

### Exit Criteria
- the same loop used in Milestone 4 works with a real provider
- provider failures are surfaced as structured errors rather than crashing the app

Framework and ACP guidance: [agent-runtime-design.md](agent-runtime-design.md)
Detailed provider decomposition: [provider-integration-design.md](provider-integration-design.md)
Execution plan: [provider-integration-plan.md](provider-integration-plan.md)
Runtime lifecycle design: [runtime-management-design.md](runtime-management-design.md)

---

# 3. Proposed Repository Layout

```text
cmd/
  shuttle/
    main.go
internal/
  app/
    app.go
  config/
    config.go
  controller/
    controller.go
    state.go
    events.go
    approvals.go
  tmux/
    client.go
    workspace.go
    panes.go
    inject.go
    capture.go
  shell/
    observer.go
    buffer.go
  protocol/
    sentinel.go
    parser.go
  tui/
    model.go
    update.go
    view.go
    transcript.go
    composer.go
  provider/
    provider.go
    mock.go
    openai.go
  logging/
    logging.go
  persistence/
    store.go
integration/
  tmux_workspace_test.go
  tmux_observer_test.go
```

## Layout Rationale
- `tmux` owns tmux command execution and pane operations only.
- `shell` owns raw observation buffers and pane-content capture.
- `protocol` owns sentinel formatting and parsing.
- `controller` owns orchestration and state transitions.
- `tui` stays presentation-focused.
- `provider` stays behind a narrow interface so it can be mocked early.

---

# 4. Package Boundaries

## 4.1 `internal/tmux`
Responsibilities:
- start or attach to a tmux session
- discover pane IDs
- resize panes
- send keys or commands into a pane
- capture pane content

Rules:
- no agent logic
- no TUI state
- no provider awareness

## 4.2 `internal/protocol`
Responsibilities:
- generate begin and end markers
- parse observed output for markers
- return structured lifecycle events

Rules:
- pure logic where possible
- heavy unit test coverage

## 4.3 `internal/shell`
Responsibilities:
- maintain a rolling output buffer
- normalize pane capture data
- publish raw observation chunks upward

Rules:
- no business decisions about approvals or tasks

## 4.4 `internal/controller`
Responsibilities:
- coordinate tmux, shell observation, protocol parsing, provider calls, and TUI-facing events
- own task state and approval state
- convert raw shell reality into structured events

Rules:
- this is the state machine layer
- keep concurrency simple until the substrate is proven

## 4.5 `internal/tui`
Responsibilities:
- render transcript and composer state
- display mode and key hints
- render approval cards
- emit user intents to the controller

Rules:
- not the source of truth for command lifecycle state

## 4.6 `internal/provider`
Responsibilities:
- expose a stable interface for prompt-in and response-out interactions
- support a mock provider early
- support a real provider later

Rules:
- provider output should normalize into controller-friendly message types

---

# 5. Debugging Strategy

## 5.1 Make Shell Events Visible
Every important transition should be loggable:
- workspace created or attached
- pane IDs discovered
- command injected
- begin marker observed
- stdout chunk received
- end marker observed
- exit code parsed
- task state changed

If a bug happens, the log should let us answer whether it is:
- tmux control failure
- observation failure
- parser failure
- controller state failure
- TUI rendering failure

## 5.2 Keep the First Controller Mostly Synchronous
Avoid clever goroutine fan-out early. Start with:
- one main controller event loop
- one observation feed
- explicit event structs

The goal is debuggability, not peak throughput.

## 5.3 Separate Unit Tests from Real Integration Tests
Unit-test heavily:
- sentinel formatting
- sentinel parsing
- controller state transitions
- approval flow state handling

Integration-test with real tmux:
- workspace creation
- pane rediscovery
- command injection
- output capture
- command lifecycle tracking

## 5.4 Use an Isolated tmux Server for Tests
Do not run tests against the user's default tmux server.

Use a dedicated server name or socket, for example:
- `tmux -L shuttle-test`

That prevents tests from touching real sessions and makes failures reproducible.

---

# 6. Developer Workflow

## 6.1 Default Commands
The exact targets can be adjusted, but the workflow should support:
- `go test ./...`
- `go run ./cmd/shuttle`
- `go test ./integration/...`

Optional task aliases:
- `make test`
- `make run`
- `make test-integration`

## 6.2 Logging
During early development, write logs to a plain local file and also allow stderr logging when running interactively.

Do not hide the logs behind a complex observability stack.

## 6.3 Manual Smoke Tests
Keep a short repeatable smoke-test script for:
1. launch Shuttle
2. verify two panes exist
3. inject `pwd`
4. inject a failing command
5. verify exit code capture
6. SSH from the top pane and repeat

---

# 7. Risk Management

## 7.1 Highest-Risk Areas
- sentinel reliability through SSH
- top-pane observation in noisy shells
- keeping command output attached to the right lifecycle record
- not overcomplicating the controller too early

## 7.2 How We Reduce Risk
- prove tmux control before TUI work
- prove protocol parsing before provider work
- use a mock provider before real API integration
- keep the first end-to-end flow narrow and testable

---

# 8. Recommended Next Step

Start with Milestone 0 and Milestone 1 only.

Do not start Bubble Tea, provider integration, persistence, or advanced keybinding work until:
- the workspace can be created reliably
- pane IDs can be rediscovered
- the controller can inject visible commands into the top pane

If those pieces are unstable, everything above them will be misleadingly expensive to debug.

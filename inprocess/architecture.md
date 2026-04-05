# Shuttle v1 Architecture

## Purpose
Define the proposed system boundaries and implementation direction separately from the core product PRD.

---

# 1. Architectural Summary

Shuttle is a local, tmux-backed two-pane system:
- the top pane is the persistent real shell session
- the bottom pane is a TUI application
- approved agent commands may also run in owned tmux execution panes within the same tmux session
- a local controller coordinates tmux integration, shell observation, command lifecycle tracking, provider communication, and persistence

The system must preserve the user-visible shell session as the continuity surface rather than replacing the terminal. It may still create owned execution panes for approved agent work when that makes command tracking more reliable.

---

# 2. Core Components

## 2.1 tmux as Infrastructure
tmux is the pane, layout, and control substrate.

Responsibilities:
- create or attach to the Shuttle workspace
- manage pane layout
- identify the top and bottom panes
- provide control hooks for injecting commands and capturing pane content

Non-responsibilities:
- terminal emulation
- product UX
- agent logic

## 2.2 Local Controller
The local controller is the orchestration layer.

Responsibilities:
- tmux integration
- shell observation
- authoritative shell-context inspection for model/runtime coordination
- sentinel parsing
- command lifecycle tracking
- target-aware patch application for local and tracked-remote filesystems
- agent loop orchestration
- persistence
- provider communication

The controller should be the single place where shell reality is translated into structured events for the TUI.

## 2.3 TUI Boundary
The TUI should remain focused on:
- rendering
- input handling
- popup and modal presentation
- approval actions
- local interaction state

The TUI should not become the source of truth for shell execution state. It renders structured state produced by the controller and sends user intents back to it.

## 2.4 Provider Abstraction
Provider communication should be abstracted behind a provider interface so model backends can be swapped without rewriting the rest of the system.

The abstraction should cover:
- provider selection
- model selection
- authentication reference handling
- request and response normalization

Detailed provider and auth decomposition lives in [provider-integration-design.md](provider-integration-design.md).

## 2.5 Persistence Layer
Persistence should store enough local state to recover useful context.

Expected responsibilities:
- session state
- task state
- transcript history
- command results
- cached remote capability inventory, including shorter-lived negative capability results so remote patch transport can re-probe stale host assumptions
- provider profile references
- runtime registry data for workspace recovery and reconciliation

## 2.6 Command Registry
The application should define an internal command registry early.

This keeps:
- keybindings decoupled from implementation logic
- app actions reusable from multiple UI entry points
- future extension points from turning into ad hoc switch statements

---

# 3. Control Flows

## 3.1 Launch Flow
1. Start or attach to a tmux workspace.
2. Discover or create the top shell pane and bottom TUI pane.
3. Initialize controller state for pane IDs and session metadata.
4. Start the TUI in the bottom pane.
5. Render initial transcript, mode state, and key hints.

Release-oriented socket/session lifecycle guidance lives in [runtime-management-design.md](runtime-management-design.md).

## 3.2 Agent Loop
1. Gather recent shell context and task state.
2. When shell identity/location certainty matters, the controller can satisfy an explicit inspect-context action from live tracked shell state before the next model turn continues.
3. Send normalized context to the selected provider.
4. If the provider emits a structured single-file edit intent, synthesize a normal unified diff from a fresh target snapshot before surfacing the action.
5. Render the response as a message, plan, diff, or approval request.
5. If approved, inject commands into the persistent shell or launch an owned execution pane, depending on controller policy.
6. Observe output and parse command lifecycle events.
7. Persist results and update the transcript.

## 3.3 Shell Mode
1. The user enters a shell command from the bottom pane.
2. The controller injects that command into the persistent user shell pane.
3. Command lifecycle tracking runs through the same observation pipeline.
4. Results are surfaced back into the transcript and inspection views.

---

# 4. Design Constraints

- Do not reimplement terminal rendering.
- Operate on the exact shell session visible to the user.
- Treat SSH as a first-class workflow.
- Keep the system local-first and do not require remote daemons.
- Tolerate noisy shell sessions and degraded conditions rather than assuming perfect prompt detection.

---

# 5. Recommended Technical Direction

Recommended implementation direction for v1:
- Language: Go
- TUI framework: Bubble Tea
- Pane and control substrate: tmux
- Persistence: SQLite
- Architecture shape: controller + TUI + tmux integration + provider abstraction + command registry

This is a recommended direction, not a hard product requirement. It aligns with the desired deployment model and with the need for a fast local binary.

---

# 6. Notes

- Detailed shell command lifecycle behavior is specified in [protocol-shell-observation.md](protocol-shell-observation.md).
- Current implementation-facing shell tracking structure, ownership, semantic-source precedence, and recovery behavior are described in [shell-tracking-architecture.md](shell-tracking-architecture.md).
- The current prioritized implementation backlog is tracked in [../BACKLOG.md](../BACKLOG.md).
- Agent runtime boundaries and integration guidance are specified in [agent-runtime-design.md](agent-runtime-design.md).
- Provider onboarding and multi-backend integration guidance are specified in [provider-integration-design.md](provider-integration-design.md).
- Runtime socket/session lifecycle and crash-recovery guidance are specified in [runtime-management-design.md](runtime-management-design.md).
- Long-running, interactive, and agent-owned shell execution redesign guidance is specified in [shell-execution-strategy.md](shell-execution-strategy.md).

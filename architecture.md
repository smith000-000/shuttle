# Shuttle v1 Architecture

## Purpose
Define the proposed system boundaries and implementation direction separately from the core product PRD.

---

# 1. Architectural Summary

Shuttle is a local, tmux-backed two-pane system:
- the top pane is the real shell session
- the bottom pane is a TUI application
- a local controller coordinates tmux integration, shell observation, command lifecycle tracking, provider communication, and persistence

The system must act on the shell session the user is already in rather than replacing the terminal or creating a shadow execution environment.

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
- sentinel parsing
- command lifecycle tracking
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
2. Send normalized context to the selected provider.
3. Render the response as a message, plan, diff, or approval request.
4. If approved, inject commands into the top pane.
5. Observe output and parse command lifecycle events.
6. Persist results and update the transcript.

## 3.3 Shell Mode
1. The user enters a shell command from the bottom pane.
2. The controller injects that command into the top pane.
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
- Detailed product acceptance criteria are specified in [requirements-mvp.md](requirements-mvp.md).
- Agent runtime boundaries and integration guidance are specified in [agent-runtime-design.md](agent-runtime-design.md).
- Provider onboarding and multi-backend integration guidance are specified in [provider-integration-design.md](provider-integration-design.md) and [provider-integration-plan.md](provider-integration-plan.md).
- Runtime socket/session lifecycle and crash-recovery guidance are specified in [runtime-management-design.md](runtime-management-design.md).
- Long-running, interactive, and agent-owned shell execution redesign guidance is specified in [shell-execution-strategy.md](shell-execution-strategy.md).

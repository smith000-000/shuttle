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
- a Shuttle-owned runtime layer can host an external coding runtime such as PI while keeping handoff decisions, state, and transcript rendering local

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

## 2.5 Runtime Layer
Preferred external-runtime selection should stay separate from provider selection.

Responsibilities:
- keep builtin Shuttle as the default conversation owner
- resolve the preferred external coding runtime for the current workspace
- persist external-work history and runtime-owned resume metadata
- let builtin Shuttle suggest and confirm explicit handoff into the external runtime
- let the user force direct external routing for the current turn via `/coder ...` while preserving the same ownership and resume model
- adapt external runtime responses back into Shuttle `AgentResponse`
- expose a generic live activity stream so the TUI can inspect external-runtime progress without depending on a specific SDK
- surface runtime diagnostics and capability constraints to the TUI
- report runtime-native capabilities such as web search without forcing external runtimes to execute through Shuttle-owned tool adapters

Current implementation note:
- builtin Shuttle remains the default agent for routine shell and task work
- PI is the first external coding runtime backend; it owns its own tool execution (`read`, `write`, `edit`, `bash`) inside the local workspace after a first-handoff trust grant, while Shuttle remains the host UI, task context, handoff control, and transcript layer
- Shuttle now has a product-owned `web_search` capability model. Builtin turns can advertise Shuttle-managed search configuration, while external runtimes can advertise native search plus optional Shuttle fallback without duplicating runtime plugin/auth configuration.

## 2.6 Persistence Layer
Persistence should store enough local state to recover useful context.

Expected responsibilities:
- session state
- task state
- transcript history
- command results
- provider profile references
- runtime registry data for workspace recovery, preferred external-runtime selection, external-work history, and PI session reconciliation

## 2.7 Command Registry
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
2. Send normalized context to builtin Shuttle by default.
3. Render the response as a message, plan, diff, approval request, handoff suggestion, or runtime-event summary.
4. If builtin suggests a handoff and the user confirms it, switch conversation ownership to the preferred external runtime.
5. If the current owner is builtin, Shuttle remains the execution authority and injects commands into the persistent shell or launches an owned execution pane depending on controller policy.
6. If the current owner is PI, PI performs its own tool loop in the local workspace and Shuttle records the resulting runtime events and assistant response.
7. Persist results and update the transcript plus external-work history state.

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
- Detailed product acceptance criteria are specified in [requirements-mvp.md](requirements-mvp.md).
- Agent runtime boundaries and integration guidance are specified in [agent-runtime-design.md](agent-runtime-design.md).
- Provider onboarding and multi-backend integration guidance are specified in [provider-integration-design.md](provider-integration-design.md) and [provider-integration-plan.md](provider-integration-plan.md).
- Runtime socket/session lifecycle and crash-recovery guidance are specified in [runtime-management-design.md](runtime-management-design.md).
- Long-running, interactive, and agent-owned shell execution redesign guidance is specified in [shell-execution-strategy.md](shell-execution-strategy.md).

# Shuttle Backlog

## Purpose
This is the only live planning file in the project root. It replaces the old scattered milestone, roadmap, and implementation-plan documents with one current backlog plus links to active reference docs and archived historical plans.

## Current State
As of April 4, 2026, Shuttle has the core local-first product loop in place:
- tmux-backed two-pane workspace with a persistent user shell plus the bottom-pane TUI
- tracked shell observation with prompt-return monitoring, semantic shell signals, remote shell location tracking, and owned execution panes
- native unified-diff proposal/apply flow for local and tracked-remote edits
- provider switching and model selection with OpenAI-compatible HTTP providers, OpenRouter support, and a first-pass Codex CLI provider path
- session-level approvals, `/new`, `/compact`, inspect-context support, and current transcript/status UI refinements
- release packaging and installer groundwork

The old implementation plans were useful to get here, but most of them now describe work that is already done. Historical plans and scratchpads live under `completed/`. Active design and operator references live under `inprocess/`.

## Active Backlog

### P0
- External agent runtime integration seam: keep Shuttle’s shell/runtime layer custom, but replace the clunky controller-side plan/act loop with a cleaner planner/tool-caller boundary above the controller. Active slice tracker: [inprocess/P0.md](inprocess/P0.md).
- Provider onboarding detection and ranking: implement first-run provider discovery, candidate ranking, and better health-check explanations so new users can land on a working provider path without manual setup.
- Runtime lifecycle hardening: move repo-local runtime artifacts out of `.shuttle/`, tighten managed socket/session recovery, and make crash/restart reconciliation release-grade.

### P1
- Security and privacy hardening: finish trace-mode separation, explicit consent for sensitive traces, stronger runtime artifact permissions/retention, and more robust semantic-state serialization.
- Shell lifecycle regression coverage: keep expanding non-interactive coverage for remote transitions and interactive recovery edge cases, and keep the manual checklist in sync with the real product behavior.
- Provider extensibility cleanup: reduce provider registration wiring, expose richer provider capabilities, and keep the current built-in backends behind the same abstraction boundary.
- Execution-pane visibility and handoff UX: add a controller-level overview of active tmux panes/executions, and use it to support a clearer tracked-command view flow such as an `F3` shortcut when a live owned execution pane exists.

### P2
- Release and install polish: package-manager distribution, remaining platform packaging cleanup, and operator-facing install/runtime docs.
- UX follow-up work: transcript and settings polish that directly supports the active backlog items above, rather than reopening broad UI exploration.

## Active Reference Docs
- [Product and operator guide](inprocess/README.md)
- [Architecture](inprocess/architecture.md)
- [Shell tracking](inprocess/shell-tracking-architecture.md)
- [Shell execution strategy](inprocess/shell-execution-strategy.md)
- [Agent runtime design](inprocess/agent-runtime-design.md)
- [Provider integration design](inprocess/provider-integration-design.md)
- [Runtime management design](inprocess/runtime-management-design.md)
- [Provider auth guide](inprocess/provider-auth-guide.md)
- [Patch apply strategy](inprocess/patch-apply-strategy.md)

## Historical Plans
These are retained for branch history and design context, but they are no longer the source of truth for current work:
- [completed/implementation-plan.md](completed/implementation-plan.md)
- [completed/provider-integration-plan.md](completed/provider-integration-plan.md)
- [completed/roadmap.md](completed/roadmap.md)
- [completed/requirements-mvp.md](completed/requirements-mvp.md)
- [completed/RESUME.md](completed/RESUME.md)

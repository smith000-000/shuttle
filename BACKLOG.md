# Shuttle Backlog

## Purpose
This is the only live planning file in the project root. It replaces the old scattered milestone, roadmap, and implementation-plan documents with one current backlog plus links to active reference docs and archived historical plans.

Epic tracking convention:
- `BACKLOG.md` is the root backlog and priority index
- `inprocess/P#.md` files are long-lived mega-epic worklists
- retired mega-epic trackers move under `completed/`

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
- Retired mega-epic: external agent runtime seam, stable `F2`/`F3` handoff semantics, runtime/controller boundary cleanup, and pane-ID exposure guards. Archived tracker: [completed/p0-agent-runtime-seam.md](completed/p0-agent-runtime-seam.md).
- Follow-up transcript rendering defect from this epic is tracked separately in GitHub rather than through the archived worklist.

### P1
- Active mega-epic tracker: [inprocess/P1.md](inprocess/P1.md).
- Security and privacy hardening: finish trace-mode separation, explicit consent for sensitive traces, stronger runtime artifact permissions/retention, operational-only app logging, and more robust semantic-state serialization.
- Shell lifecycle regression coverage: keep expanding non-interactive coverage for remote transitions and interactive recovery edge cases, and keep the manual checklist in sync with the real product behavior.
- Provider extensibility cleanup: reduce provider registration wiring, expose richer provider capabilities, and keep the current built-in backends behind the same abstraction boundary.
- Execution-pane visibility and handoff UX: add a controller-level overview of active tmux panes/executions, and use it to support a clearer tracked-command view flow such as an `F3` shortcut when a live owned execution pane exists.
- External coding runtimes: carry forward concrete `pi` and other external coding-agent integrations behind `internal/agentruntime` without turning P1 into a generic plugin system.
- Folded-forward P0 follow-ons: provider onboarding detection/ranking cleanup and runtime lifecycle hardening now ship as part of the broader P1 epic.

### P2
- Finish the current epic cycle by making external agent runtimes fully functional behind `internal/agentruntime`, including runtime selection/recovery edge cases and operator-facing clarity around active runtime ownership.
- Make the Shuttle control surface authoritative end-to-end (controller as source of truth for execution/session/task state, with TUI presentation and intent routing aligned to that authority boundary).
- Close remaining shell/runtime handoff gaps needed to treat controller-reconciled state, not ad hoc pane interpretation, as canonical behavior.

### P3
- Release and packaging hardening: package-manager distribution, cross-platform install validation, and operator-facing install/runtime docs refinement.
- Production-readiness hardening pass: reliability regressions, operational diagnostics polish, and release process tightening.

### P4
- Active mega-epic tracker: [inprocess/P4.md](inprocess/P4.md).
- Persistent memory and retrieval UX for host-scoped operational recall (for example, asking where a prior SnapRAID issue was fixed) without leaking stale or cross-host assumptions.
- Context pressure management: proactive auto-compaction and shell-output summarization before provider context limits are hit.
- Memory configuration lifecycle: retention policy controls, pruning tooling, and operator-visible memory hygiene status.
- Rules system: explicit always-on guidance/rules that the agent keeps in working context with clear precedence and auditability.
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
- [P1 mega-epic tracker](inprocess/P1.md)
- [P4 mega-epic tracker](inprocess/P4.md)

## Historical Plans
These are retained for branch history and design context, but they are no longer the source of truth for current work:
- [completed/implementation-plan.md](completed/implementation-plan.md)
- [completed/provider-integration-plan.md](completed/provider-integration-plan.md)
- [completed/roadmap.md](completed/roadmap.md)
- [completed/requirements-mvp.md](completed/requirements-mvp.md)
- [completed/RESUME.md](completed/RESUME.md)
- [completed/p0-agent-runtime-seam.md](completed/p0-agent-runtime-seam.md)

# Shuttle Backlog

## Purpose
This is the only live planning file in the project root. It replaces the old scattered milestone, roadmap, and implementation-plan documents with one current backlog plus links to active reference docs and archived historical plans.

Epic tracking convention:
- `BACKLOG.md` is the root backlog and priority index
- `inprocess/P#.md` files are long-lived mega-epic worklists
- retired mega-epic trackers move under `completed/`

## Current State
As of April 12, 2026, Shuttle has the core local-first product loop in place:
- tmux-backed two-pane workspace with a persistent user shell plus the bottom-pane TUI
- tracked shell observation with prompt-return monitoring, semantic shell signals, remote shell location tracking, and owned execution panes
- native unified-diff proposal/apply flow for local and tracked-remote edits
- provider switching and model selection with OpenAI-compatible HTTP providers, OpenRouter support, and a first-pass Codex CLI provider path
- session-level approvals, `/new`, `/compact`, inspect-context support, persisted runtime selection through `F10`, and current transcript/status UI refinements
- release packaging and installer groundwork
- the first controller-owned external runtime seam behind `internal/agentruntime`

The old implementation plans were useful to get here, but most of them now describe work that is already done. Historical plans and scratchpads live under `completed/`. Active design and operator references live under `inprocess/`.

## Active Backlog

### P0
- Retired mega-epic: external agent runtime seam, stable `F2`/`F3` handoff semantics, runtime/controller boundary cleanup, and pane-ID exposure guards. Archived tracker: [completed/p0-agent-runtime-seam.md](completed/p0-agent-runtime-seam.md).
- Follow-up transcript rendering defect from this epic is tracked separately in GitHub rather than through the archived worklist.

### P1
- Completed mega-epic: security/privacy hardening, shell lifecycle stabilization, provider/runtime seam cleanup, execution-pane visibility, and the first external runtime seam. Archived tracker: [completed/p1-hardening-and-runtime-expansion.md](completed/p1-hardening-and-runtime-expansion.md).
- Additional shell matrix expansion can continue later, but P1 is no longer the active planning document.

### P2
- Active mega-epic tracker: [inprocess/P2.md](inprocess/P2.md).
- Secondary runtime integration architecture:
  - keep Shuttle as the sole controller/state authority
  - use the CLI-backed `codex_sdk` runtime as the primary UX-validation bridge
  - carry a `codex_app_server` bridge runtime for real app-server turn handling with per-task native thread persistence and explicit reset semantics
  - extend runtime adapter behavior only through the existing `agentruntime.Host` contract
  - preserve parity across builtin and non-builtin runtimes for plans, proposals, approvals, patch handling, and recovery turns
- Cursory manual smoke testing now shows both Codex runtime paths can at least answer ordinary prompts in the TUI.
- Shell-tracking regressions introduced during the runtime work were hardened back down: prompt/cwd tracking across `cd`, transcript capture, and `KEYS>` exit behavior now have focused regression coverage.
- Continue deepening the app-server runtime from live session-bound thread reuse into stronger failure handling, reconnect policy, and native compaction behavior before `auto` prefers it.

### P3
- Planned mega-epic tracker: [inprocess/P3.md](inprocess/P3.md).
- Release hardening:
  - package-manager distribution
  - remaining platform packaging cleanup
  - operator-facing install/runtime docs
  - release validation, upgrade paths, and ship-quality hardening

### P4
- Planned mega-epic tracker: [inprocess/P4.md](inprocess/P4.md).
- Memory, auto-compaction, and rules:
  - durable task/session memory that stays product-owned and auditable
  - automatic compaction policy and recovery behavior
  - user/project/runtime rule handling and precedence
  - integration of those capabilities into the existing controller/runtime workflow without turning Shuttle into a black-box agent framework

### P5
- Planned mega-epic tracker: [inprocess/P5.md](inprocess/P5.md).
- Dual-runtime delegation and escalation:
  - keep one authoritative session runtime while allowing Shuttle-owned delegation to bounded secondary-runtime subtasks
  - define escalation triggers, delegate context packaging, reintegration semantics, and transcript visibility
  - preserve Shuttle ownership of approvals, shell execution, patch apply, and task/session persistence during delegated work
  - avoid silent fallback or hidden hybrid reasoning by making delegation explicit, auditable, and reversible

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
- [P2 mega-epic tracker](inprocess/P2.md)
- [P5 mega-epic tracker](inprocess/P5.md)

## Historical Plans
These are retained for branch history and design context, but they are no longer the source of truth for current work:
- [completed/implementation-plan.md](completed/implementation-plan.md)
- [completed/provider-integration-plan.md](completed/provider-integration-plan.md)
- [completed/roadmap.md](completed/roadmap.md)
- [completed/requirements-mvp.md](completed/requirements-mvp.md)
- [completed/RESUME.md](completed/RESUME.md)
- [completed/p0-agent-runtime-seam.md](completed/p0-agent-runtime-seam.md)
- [completed/p1-hardening-and-runtime-expansion.md](completed/p1-hardening-and-runtime-expansion.md)

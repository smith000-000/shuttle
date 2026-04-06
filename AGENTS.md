# AGENTS.md

Repository-local guidance for Codex and similar coding agents.

## Scope

These instructions apply to the entire repository.

## Product And Stack Orientation

Before making nontrivial changes, anchor yourself in the current product and architecture docs instead of inferring structure only from local code.

Primary references:
- `BACKLOG.md`: current implementation status, active branch themes, and prioritized remaining work
- `inprocess/README.md`: current user-visible behavior, supported workflows, limitations, build/test commands, and operator-facing usage
- `inprocess/architecture.md`: top-level system boundaries and responsibilities

Important subsystem references:
- `inprocess/agent-runtime-design.md`: agent/runtime interaction boundaries
- `inprocess/shell-tracking-architecture.md`: tracked shell model, prompt-return logic, ownership, and recovery behavior
- `inprocess/shell-execution-strategy.md`: execution-control policy, regression checklist, and handoff/fullscreen expectations
- `inprocess/provider-integration-design.md`: provider/auth structure and onboarding direction
- `inprocess/runtime-management-design.md`: socket/session/runtime-state lifecycle expectations

Patch and refactor references:
- `inprocess/patch-apply-strategy.md`: native patch runtime strategy and guardrails
- `completed/patch-apply-implementation-plan.md`: intended patch/apply product flow and constraints
- `completed/refactor-checklist.md`: decomposition/test-organization plan for large files

Treat these docs as part of the codebase contract. When implementation changes them materially, update them in the same branch.

## Current Tech Stack

Shuttle is a local-first Go application with:
- Go as the implementation language
- Bubble Tea for the bottom-pane TUI
- tmux as the workspace/pane/control substrate
- local shell observation and owned tmux execution panes
- OpenAI-compatible provider integrations behind the provider abstraction

Respect these boundaries:
- tmux is infrastructure, not product logic
- the controller is the source of truth for shell/task/execution state
- the TUI renders controller state and sends user intents back to it
- provider code should stay behind the provider abstraction instead of leaking backend-specific assumptions across the app

## Product Structure

The product model is a two-pane Shuttle session:
- top pane: the persistent real shell continuity surface
- bottom pane: the Shuttle TUI
- optional owned tmux execution panes: agent-approved command execution

The app is structured around:
- `internal/controller`: orchestration, agent loop, task state, execution state, patch apply, and shell/session reconciliation
- `internal/tui`: rendering, input handling, approvals/proposals, composer, and transcript UX
- `internal/shell`: prompt/context detection, semantic-shell handling, and foreground/fullscreen tracking
- `internal/provider`: provider request/response normalization and backend integrations
- `internal/patchapply`: native unified-diff parsing, validation, staging, and apply
- `integration/harness`: tmux-driven end-to-end interactive regression coverage

When adding features, fit them into these boundaries rather than creating overlapping control paths.

## Docs Policy

When a change affects behavior, user workflows, architecture, testing, or operator workflow, update the relevant docs in the same branch before opening a PR.

This includes reviewing and updating:
- `BACKLOG.md` for current implementation priorities and status
- `inprocess/README.md` for current user-visible behavior, capabilities, limitations, and workflows
- `inprocess/architecture.md` and related design docs when implementation meaningfully changes the described system
- subsystem docs such as shell/provider/runtime design notes when changes affect those areas
- test or harness docs when new test flows, scripts, or manual procedures are added
- planning docs when they are intended to track the current implementation state

Active slice tracking rules:
- keep `BACKLOG.md` as the only planning file in the project root
- track the current top-priority implementation slice in `inprocess/P0.md`
- amend `inprocess/P0.md` in place for defects, regressions, and acceptance status instead of spawning extra scratch planning files
- archive completed slice trackers under `completed/`

Do not assume existing docs are still correct after code changes.

## PR Checklist

Before pushing or creating a PR, explicitly check:
- does `inprocess/README.md` still describe the current product behavior and current limitations?
- do `BACKLOG.md`, `inprocess/architecture.md`, or subsystem design docs now need updates?
- do new commands, slash commands, prompts, flows, or test harnesses need documentation?
- are stale statements from earlier phases still present and now misleading?
- does the documented product structure still match the code paths you changed?

If the answer to any of these is yes, update the docs before creating the PR.

## Working Norms

- Keep changes behavior-preserving when doing pure refactors.
- Prefer focused follow-up branches for new feature slices instead of extending already large PRs.
- Leave the worktree clean before handing off for review unless the user explicitly asks otherwise.

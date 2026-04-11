# Patch/Apply Research

This note captures the recommended direction for Shuttle's future patch/application feature branch. It is intentionally separate from the current execution-monitor work.

## Recommendation

Shuttle should own patch approval and application natively.

Recommended stack:
- model/tool orchestration: official `openai-go` Responses API client
- patch parsing and apply engine: `github.com/bluekeyes/go-gitdiff`
- optional validation layer: `git apply --check` before final apply/commit when available

## Why This Shape

There does not appear to be a strong turnkey Go framework that cleanly handles:
- agent proposal generation
- patch approval UX
- safe workspace application
- apply result reporting

The pragmatic design is to compose:
- Shuttle's own controller/runtime flow
- an official provider SDK
- a focused diff/patch library

This keeps the user-facing behavior and guardrails inside Shuttle instead of outsourcing core product behavior to another agent runtime.

## Candidate Libraries

### `openai-go`

Use for provider/tool orchestration.

Why:
- official Go SDK
- matches the Responses API/tool loop direction already being used elsewhere in Shuttle

Role:
- generate `patch` proposals
- drive structured tool responses
- feed apply success/failure back into the agent loop

## `bluekeyes/go-gitdiff`

Best current fit for patch parsing and application.

Why:
- parses git/unified diffs
- supports patch application
- designed with `git apply`-style behavior in mind

Role:
- parse proposed diffs
- validate file targets and hunks
- apply approved diffs inside Shuttle's patch flow

## `sourcegraph/go-diff-patch`

Useful if we need git-compatible diff generation, but not the primary apply engine.

Role:
- optional helper for diff generation or formatting
- not the main recommendation for workspace application

## `gotextdiff` / `go-udiff`

Useful for text diff generation, not enough by themselves for full repo-safe patch application.

Role:
- low-level text diff helpers only

## `uber-go/gopatch`

Only relevant for Go-specific AST refactors, not general workspace patch application.

Role:
- niche future enhancement, not the base Shuttle patch pipeline

## Agent Backend Architecture

Patch/apply should be native Shuttle functionality, even if Shuttle later supports multiple coding-agent backends.

Recommended split:
- patch proposal/apply: native Shuttle
- coding-agent backend: pluggable adapter layer

Suggested backend order:
1. direct provider integration via `openai-go`
2. optional Codex CLI bridge
3. optional `pi` adapter for a more programmable long-term agent runtime

This avoids coupling Shuttle's core patch UX to a third-party agent framework.

## Suggested Feature-Branch Scope

Future branch goal:
- user approves a proposed patch
- Shuttle validates the patch
- Shuttle applies it to the workspace
- Shuttle reports success/failure as a first-class transcript result

Suggested vertical slices:
1. patch proposal schema and transcript rendering cleanup
2. patch approval action in the TUI/controller
3. patch parse + validation
4. patch apply to workspace
5. apply result fed back into the agent loop
6. follow-up commands can only assume files exist after confirmed apply

## Guardrails

Required safety rules:
- proposed patches remain inert until explicitly applied
- the agent must not claim proposed files already exist
- failed apply must produce a structured result, not silent drift
- approval policy should control whether patch application is always-ask or can be auto-approved in trusted modes

## Deferred Questions

- whether to support partial hunk apply in v1
- whether to expose a patch-inspection view before apply
- whether to run `git apply --check` as an optional extra validation step when git is present
- whether Codex CLI or `pi` should be used as an optional coding backend after native patch/apply lands

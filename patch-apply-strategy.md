# Patch Apply Strategy

## Why This Exists

Shuttle should treat patch application as a first-class runtime capability, not as a shell workaround.

The installed Codex CLI on this machine gives two useful signals:
- `codex apply` explicitly says it applies the latest diff with `git apply`
- the native binary also contains strings for an internal patch tool and preflight/apply pipeline, including:
  - `apply-patch/src/parser.rs`
  - `apply-patch/src/lib.rs`
  - `core/src/apply_patch.rs`
  - `Invalid patch`
  - `patch rejected`
  - `Preflight: patch does not fully apply`
  - `git apply failed to run`

Inference:
- Codex CLI appears to separate agent-side patch handling from the user-facing `codex apply` command
- that is the right shape for Shuttle too: native validation and apply first, optional `git` integration second

## Shuttle Contract

Patch proposals and approvals must be:
- git-style unified diffs
- relative to `LocalWorkspaceRoot`
- local workspace mutations only

Patch payloads must not be:
- shell commands that invoke `apply_patch`
- shell commands that invoke `git apply`
- shell commands that invoke `patch`
- `*** Begin Patch` payloads
- prose descriptions of edits

## Runtime Pipeline

Shuttle should use this order:
1. Parse unified diff text natively.
2. Validate patch shape and paths.
3. Run native content preflight against current workspace files.
4. Optionally run `git apply --check` when `git` is available and the workspace is a git repo.
5. Stage final file outputs in memory.
6. Commit atomically with rollback on write failure.

This means native validation is authoritative. `git apply --check` is an extra validator, not the core engine.

## Validation Rules

Reject patches that are:
- empty
- malformed unified diffs
- binary patches
- copy patches
- symlink or submodule patches
- absolute-path patches
- `..` path escapes
- mode-only patches
- duplicate source-path or target-path edits in a single patch
- self-renames
- stale hunks that do not apply to the current workspace contents

## Prompting Rules

Provider instructions should keep pushing the model toward:
- one coherent unified diff
- exact hunk headers and counts
- no prose before or after the diff
- no shell fallback for local file edits

After patch failure:
- summarize the failure briefly
- if a retry is appropriate, produce exactly one corrected patch
- never recover by proposing a shell `apply_patch` command

## UX Rules

Core UX should stay simple:
- patch proposals are `apply`, `reject`, or `ask agent`
- no patch editing in v1
- raw unified diff is the source of truth in detail view
- patch apply should not activate shell-tail or execution-monitor UI

## Current Shuttle Implementation Direction

Shuttle should keep converging on this shape:
- native patch engine in `internal/patchapply`
- controller-owned patch result events and continuation flow
- prompt/controller normalization that converts accidental shell patch-tool output back into native patch proposals when possible
- strict regression coverage for malformed diffs, stale hunks, duplicate path edits, and failed retry flows

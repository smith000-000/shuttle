# Patch Apply Implementation Plan

## Summary

Shuttle will implement patch application as a native controller/TUI flow.

V1 scope:
- git-style unified text diffs only
- explicit user apply flow
- no patch editing
- no partial hunk apply
- no binary patch support

Recommended stack:
- native patch parser/apply engine: `github.com/bluekeyes/go-gitdiff`
- optional validator: `git apply --check`

## Core Decisions

- `proposal_patch` and `approval_patch` are unified diffs, not `*** Begin Patch` payloads.
- patch application is local workspace mutation, not shell mutation
- the apply root must come from a stable local workspace path, not the live shell cwd
- patch proposals remain inert until the user explicitly applies them
- patch apply failures must be first-class transcript results, not only raw errors

## Implementation Shape

### Patch Engine

Introduce a narrow native patch package responsible for parse, validate, stage, and commit.

Validation rules:
- allow create, update, delete, and rename text diffs
- reject empty patches
- reject malformed diffs
- reject binary patches
- reject copy patches
- reject symlinks and submodules
- reject absolute paths and `..` traversal outside the workspace root

Apply behavior:
- parse and validate the full patch first
- stage final file outputs before commit
- commit in a deterministic order
- rollback already-written files if a later write fails
- never silently leave a partial success

Optional validation:
- if `git` is available and the workspace is a git repo, run `git apply --check`
- if that preflight runs and fails, block apply
- native validation remains authoritative when `git` is unavailable

### Controller And Provider

Add stable local workspace-root state separate from shell cwd.

Add first-class patch result state:
- `patch_apply_result` transcript event
- summary payload with status, workspace root, changed-file counts, and error text
- task state for the most recent patch apply result

Controller behavior:
- explicit apply path for pending patch proposals
- approval path for `ApprovalPatch`
- success clears pending proposal/approval and can continue the agent loop
- failure emits a structured failed result and does not claim workspace success

Provider context/prompt updates:
- expose local workspace root separately from shell cwd
- instruct the model to emit unified diffs
- state explicitly that local patch apply does not mutate a remote shell filesystem
- allow follow-up commands to assume file existence only after successful apply

### TUI

Core UX:
- `ProposalPatch` gets `Y apply`, `N reject`, `R ask agent`
- no `E` patch edit action in v1
- `ApprovalPatch` uses the existing approval flow with patch-specific button copy
- existing `Ctrl+O` detail view is the inspection surface for the raw unified diff
- patch apply uses busy/result state, but not shell-tail or active-execution UI

Follow-on UX:
- dedicated diff modal
- parsed file summaries in the action card
- sticky approve/reject footer in the diff view
- per-file and per-hunk navigation

## Test Plan

- patch engine unit tests for valid create/update/delete/rename diffs
- rejection tests for malformed, empty, binary, copy, symlink, submodule, and outside-root patches
- rollback test for commit failure
- controller tests for proposal apply, approval apply, result events, and auto-continue
- provider context/prompt tests for unified diff wording and local-workspace-root semantics
- TUI tests for patch proposal/approval actions, key hints, detail rendering, and result handling
- migrate existing patch fixtures from `*** Begin Patch` to unified diff

## Deferred

- execution-policy wiring beyond explicit apply
- dedicated diff popup UX
- binary patch support
- patch editing/refinement beyond the existing ask-agent flow

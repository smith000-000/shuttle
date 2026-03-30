# Patch Apply Implementation Plan

## Summary

Shuttle will implement patch application as a native controller/TUI flow.

V1 scope:
- git-style unified text diffs only
- explicit user apply flow
- no patch editing
- no partial hunk apply
- no binary patch support
- controller-synthesized unified diffs for common single-file structured text edits

Recommended stack:
- native patch parser/apply engine: `github.com/bluekeyes/go-gitdiff`
- optional local validator: `git apply --check`
- remote apply transport: controller-managed staged `git`, then staged `python3`, then verified shell fallback

## Core Decisions

- `proposal_patch` and `approval_patch` are unified diffs, not `*** Begin Patch` payloads.
- common single-file insert/replace requests may arrive as structured `proposal_kind:"edit"` intents, but Shuttle still shows and applies a normal unified diff
- patch application is target-aware: `local_workspace` or `tracked_remote_shell`
- the apply root must come from a stable verified target root, not the live shell cwd text alone
- patch proposals remain inert until the user explicitly applies them
- patch apply failures must be first-class transcript results, not only raw errors
- patch repair gets one constrained retry before Shuttle falls back to a safer next step

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

Add stable local workspace-root state separate from shell cwd, and carry explicit patch-target metadata through proposal, approval, and apply-result state.

Add first-class patch result state:
- `patch_apply_result` transcript event
- summary payload with status, workspace root, changed-file counts, and error text
- task state for the most recent patch apply result

Controller behavior:
- explicit apply path for pending patch proposals
- approval path for `ApprovalPatch`
- success clears pending proposal/approval and can continue the agent loop
- failure emits a structured failed result and does not claim workspace success
- remote apply verifies the active tracked shell target before mutation
- remote apply prefers `git` in the active remote repo, then `python3`, then a controller-managed shell transaction with backup and verification
- remote capability inventory is cached per remote target in Shuttle state, exposed back to the model as a hint, and negative results are refreshed more aggressively than positive ones
- remote payloads are staged to the tracked remote shell in bounded chunks before apply so large diffs and file contents do not depend on one giant inline shell command
- remote verification checks file mode as well as final file bytes
- patch-replaceable remote shell edit proposals are repaired back into `tracked_remote_shell` patches or downgraded to explicit approval
- a second patch-apply failure stops patch-repair looping instead of repeatedly asking for another corrected diff

Provider context/prompt updates:
- expose local workspace root separately from shell cwd
- expose the active remote patch root and cached remote capabilities when available
- instruct the model to prefer structured single-file edits for ordinary insert/replace work and reserve raw unified diffs for complex cases
- require the model to declare `local_workspace` vs `tracked_remote_shell`
- state explicitly that remote patch apply mutates the active tracked shell target, not the local workspace
- state explicitly that remote patch paths are relative to the active remote cwd
- allow follow-up commands to assume file existence only after successful apply

### TUI

Core UX:
- `ProposalPatch` gets `Y apply`, `N reject`, `R ask agent`
- structured edit proposals are synthesized into `ProposalPatch` before they become visible in the transcript or TUI
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
- provider context/prompt tests for unified diff wording, local-workspace-root semantics, and explicit patch-target declarations
- TUI tests for patch proposal/approval actions, key hints, detail rendering, and result handling
- migrate existing patch fixtures from `*** Begin Patch` to unified diff

## Deferred

- execution-policy wiring beyond explicit apply
- dedicated diff popup UX
- binary patch support
- patch editing/refinement beyond the existing ask-agent flow

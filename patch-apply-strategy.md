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
- relative to the declared patch target root
- explicit about which target they mutate:
  - `local_workspace`
  - `tracked_remote_shell`

For common single-file text edits, the model should prefer a structured edit intent over hand-authoring raw hunks. Shuttle should then synthesize the final unified diff from a fresh snapshot before normal patch validation and apply.

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
3. Resolve the declared target and block if it is ambiguous.
4. Fetch current file snapshots from that target.
5. If the model emitted a structured single-file edit, apply it in memory against the fresh snapshot and synthesize a unified diff.
6. Run native content preflight against those snapshots.
7. Optionally run `git apply --check` when the target is the local workspace, `git` is available, and the workspace is a git repo.
8. Stage final file outputs in memory.
9. For `tracked_remote_shell`, choose transport in this order: `git`, then `python3`, then verified shell fallback.
10. Stage remote payloads in bounded chunks and verify staged size before apply instead of embedding full file or diff blobs in one shell command.
11. Re-probe stale negative remote capabilities before falling all the way back to shell transport.
12. Commit atomically with rollback on write failure and verify final bytes plus mode.

This means native validation is authoritative. Remote `git` is a transport and extra validator, not the source of truth.

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
- structured single-file edits for ordinary insert/replace requests
- one coherent unified diff when a raw patch is actually needed
- exact hunk headers and counts
- no prose before or after the diff
- the correct explicit patch target
- no shell patch-tool fallback for local file edits
- remote text-file edits using `tracked_remote_shell` patches instead of raw `sed -i` or `cat > file` commands

After patch failure:
- summarize the failure briefly
- if a retry is appropriate, produce exactly one corrected patch
- keep the same target unless the context clearly proves the target was wrong
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
- target-aware routing between the local workspace and the active tracked remote shell
- remote capability inventory cached in Shuttle state
- remote commit via controller-managed staged transports, preferring `git`, then `python3`, then verified shell transport
- negative cached capability answers should expire faster than positive ones so Shuttle can recover when `git` or `python3` becomes available
- prompt/controller normalization that converts accidental shell patch-tool output back into native patch proposals when possible
- strict regression coverage for malformed diffs, stale hunks, duplicate path edits, and failed retry flows

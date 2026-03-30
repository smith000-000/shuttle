# Resume

Current branch: `uitweaks`

Latest local commit:
- `906b7ae` `Harden remote patching and shell context flow`

Current state:
- local and remote patching are both functional again
- patching is now target-aware:
  - `local_workspace`
  - `tracked_remote_shell`
- remote patch transport now prefers:
  - `git`
  - `python3`
  - verified shell fallback
- remote capability inventory is cached and can re-probe stale negative answers
- remote payload staging uses bounded chunks instead of giant inline shell blobs
- remote patch verification checks file mode as well as final bytes
- common single-file text edits no longer depend on the model authoring raw unified hunks:
  - the model can emit `proposal_kind:"edit"`
  - the controller fetches a fresh local or remote snapshot
  - Shuttle applies the edit in memory
  - Shuttle synthesizes the visible unified diff proposal
- supported structured edit operations:
  - `insert_before`
  - `insert_after`
  - `replace_exact`
  - `replace_range`
- ambiguous structured edits now fall back to a safe read-only inspection command instead of surfacing a guessed bad patch
- shell context inspection is now a first-class internal action:
  - the model can request `inspect_context`
  - Shuttle runs it silently
  - it does not require approval
  - it does not create visible transcript turns
- shell transition handling is improved for nested `ssh` / `exit` cases:
  - prompt-return is no longer accepted as eagerly
  - password-prompt/probe injection issues were reduced
- Shuttle now tears down its managed tmux session on exit even after reattaching to an existing managed runtime
- Ollama timeout handling was widened so the provider client no longer cuts off turns earlier than the TUI timeout

What was validated:
- `go test ./...` passes
- local structured edit synthesis tests pass
- remote structured edit synthesis tests pass
- provider schema/prompt parsing for `proposal_kind:"edit"` passes
- manual validation from the user:
  - local and remote patches now seem functional

Important implementation points:
- `internal/controller/controller_edit.go`
  - structured edit synthesis from fresh local/remote snapshots into unified diffs
- `internal/controller/remote_patch.go`
  - remote snapshot read, transport selection, payload staging, apply, and verification
- `internal/controller/remote_capabilities.go`
  - cached remote capability inventory and transport hints
- `internal/controller/controller_inspect.go`
  - hidden authoritative shell-context inspection
- `internal/controller/controller_agent.go`
  - internal inspect loop and structured edit synthesis before visible proposal emission
- `internal/provider/responses.go`
  - provider schema and prompt contract for:
    - `proposal_kind:"edit"`
    - `proposal_kind:"inspect_context"`
    - stricter patch payload rules
- `internal/shell/observer.go`
  - shell transition and prompt/probe reconciliation hardening

Docs status:
- `README.md` updated
- `architecture.md` updated
- `implementation-plan.md` updated
- `patch-apply-strategy.md` updated
- `patch-apply-implementation-plan.md` updated

Current worktree status:
- clean

Likely next slice:
1. clean up transcript noise from shell command execution and controller plumbing
2. keep the transcript clean while still preserving enough detail for shell-pane visibility and `Ctrl+O`
3. verify that internal plumbing markers and shell-control chatter stay out of normal transcript rows

Suggested restart prompt after `/new`:
- "Read `RESUME.md`, `README.md`, and `patch-apply-strategy.md`. Continue on `uitweaks`. Local and remote patching are functional again, including controller-synthesized structured single-file edits and hidden inspect-context refresh. Focus next on transcript noise from shell command execution and keep the normal transcript clean without breaking shell-pane visibility or `Ctrl+O` detail."

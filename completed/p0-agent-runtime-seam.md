# P0: Agent Runtime Seam

## Purpose
Retained as the completed mega-epic worklist for the agent-runtime seam work. This tracker is archived for historical context; active work has moved to `inprocess/P1.md`.

## Current Decision Summary
- Shuttle remains the sole executor for shell commands, patch application, approvals, and state mutation.
- P0 is seam-first only. It does not add real PI or Codex alternate runtimes yet.
- `BACKLOG.md` remains the only root planning file.
- This tracker previously lived in `inprocess/P0.md` before being archived under `completed/`. 
- `F2` is the persistent tracked shell handoff, including remote shells.
- `F3` is the separate owned-execution-pane handoff when a distinct local agent command pane exists.

## Slice Checklist
- [x] Add the live tracker convention and link this file from the backlog/design docs.
- [x] Introduce `internal/agentruntime` with request/outcome/runtime contracts.
- [x] Route the built-in response loop through the runtime layer.
- [x] Rewire controller turn entrypoints to use runtime handling instead of direct response orchestration.
- [x] Add a shared controller helper for applying runtime outcomes to transcript/task state.
- [x] Move runtime selection wrappers to the runtime layer instead of the provider layer.
- [x] Update agent-runtime docs and lock in regression coverage for the seam.
- [x] Move shared runtime/model contract types under `internal/agentruntime` and leave controller aliases only for compatibility.
- [x] Remove the obsolete provider-side runtime wrapper scaffold.
- [x] Split persistent-shell and owned-execution handoff semantics so the TUI can support stable shell/runtime shortcuts.
- [x] Add `F3` execution-pane handoff and keep `F2` permanently bound to the tracked shell, including remote shells.
- [x] Stop leaking Shuttle-managed tmux pane IDs into provider turn context and generic transcript notices.
- [x] Add a controller-side guard that blocks agent proposals which target Shuttle-managed tmux pane IDs directly.

## Defects / Regressions
- Fixed: context-sensitive `F2` made take-control unpredictable once owned execution panes were active.
- Fixed: `F3` handoff originally inherited the `F2` detach binding and did not behave like a true toggle.
- Fixed: provider/runtime turn context exposed `tracked_pane` and `execution_pane`, which let the agent treat ephemeral owned pane IDs like stable tools.
- Fixed: owned execution start notices exposed raw pane IDs in transcript text and reinforced the same bad model.
- Fixed: fresh user prompts could inherit a stale active checklist from an earlier task and continue the wrong plan.
- Fixed: stray mouse-report fragments from terminal scrolling could leak into the composer as literal text.
- Fixed: live user-shell executions now follow tracked-pane migration when tmux respawns the tracked shell, instead of staying bound to a dead pane ID.
- Fixed: `exit` / `logout` transitions now settle from tracked-pane respawn or disconnect-tail evidence when prompt parsing never produces a final clean trailing prompt.
- Fixed: continuation turns now require explicit agent-side checklist status reconciliation through `plan_step_statuses`; the old controller-side plan advancement helper was removed.
- Active: selected-command transcript rendering still leaks grey selection background into ANSI-preserved shell output. Only the left gutter should stay grey when browsing commands with `Alt-Up` / `Alt-Down`, but the shell body remains tinted in transcript mode.
- Next: fix transcript shell rendering so selection styling terminates before ANSI-preserved shell content and final line padding cannot reintroduce the grey background into the command body.
- Fixed: architecture review of recent shell-tracking, transition-settlement, pane-recovery, and handoff changes identified and removed duplicated controller-side handoff reconciliation, and the remaining decomposition pressure is now mostly large-file cleanup rather than ownership ambiguity.

## Acceptance Gates
- No controller turn entrypoint should orchestrate provider/model response handling inline.
- Patch repair and inspect-context recursion must still work.
- Auto-run and approval policy must remain controller-owned.
- Existing controller/provider tests for the built-in path must pass.
- `F2` must always target the persistent tracked shell.
- `F3` must target only a distinct active owned execution pane and detach on `F3`.
- Provider turn context must not expose Shuttle-managed tmux pane IDs as agent-facing inspection primitives.
- Active checklists must update only from explicit agent status reconciliation on continuation turns, not controller-side blind advancement after command or patch completion.

## Notes
- This mega-epic is retired and archived as `completed/p0-agent-runtime-seam.md`.
- The lingering transcript selection rendering bug from this epic is tracked separately in GitHub and is not a blocker for archiving the P0 worklist.
- The controller now depends on `Runtime` plus a runtime-host adapter, not on a provider/model agent field directly.
- Shared runtime/model domain types are now owned by `internal/agentruntime`, with controller re-exporting aliases for compatibility.
- The obsolete provider-side runtime wrapper scaffold was removed during cleanup.
- The current architecture seam is in place and testable; Shuttle still owns shell truth, approvals, execution, patch apply, and transcript/state mutation.
- Handoff semantics are now:
  - `F2`: persistent tracked shell, including remote shells
  - `F3`: distinct local owned execution pane, when present
- Provider context now reports execution state and shell context without exposing Shuttle-managed pane IDs to the model.
- Verification passed repeatedly with `go test ./internal/... -count=1` and `go test ./... -count=1`.
- The seam slice is otherwise functionally closed; the transcript shell-rendering regression above is tracked separately in GitHub rather than through this archived epic.

# Resume

## Current Status
- P0 is effectively landed: Shuttle now has a real `internal/agentruntime` seam, the controller depends on `Runtime` plus a runtime-host bridge, and provider/model orchestration is no longer fused into controller turn entrypoints.
- Shared runtime/model contract types now live under `internal/agentruntime`; controller re-exports aliases only for compatibility.
- The obsolete provider-side runtime wrapper scaffold is removed.
- The current built-in runtime is still the only runtime implementation, but alternate runtimes can now be added above Shuttle’s shell/runtime substrate without reopening controller orchestration.
- Active checklist reconciliation is now explicit in the runtime/provider contract:
  - agent turns with an active plan are prompted to perform a checklist status check
  - responses can now return per-step `plan_step_statuses`
  - controller-side blind advancement after command/patch completion was removed
- Fresh user prompts now supersede stale active plans unless the prompt is clearly asking to continue or resume the current checklist.

## Handoff Model
- `F2` is always the persistent tracked shell handoff.
- This includes remote shells: if the user is on SSH, `F2` remains the correct shell target.
- `F3` is the separate owned local execution-pane handoff when Shuttle is running a distinct agent command pane.
- `F3` now detaches on `F3`, so the execution-pane handoff behaves like a real toggle instead of requiring `F2` to get back.
- For long-running tracked commands, `F3` remains the operator escape hatch even if the model fails to bound or stop a listener cleanly.

## Recent Fixes
- Fixed the take-control regression where long-running owned execution panes were not reachable directly.
- Split stable shell/runtime handoff semantics:
  - `F2` = persistent tracked shell
  - `F3` = active owned execution pane
- Added a louder plan-card hint when Shuttle is waiting for `Ctrl+G` to continue from the latest command result.
- Fixed the provider/runtime contract leak where Shuttle exposed `tracked_pane` and `execution_pane` to the model.
- Fixed owned execution transcript notices so they no longer print raw pane IDs.
- Added a controller-side guard that blocks agent proposals like `tmux capture-pane -pt %3 ...` when they target Shuttle-managed pane IDs.
- Fixed checklist drift by moving status updates onto the agent turn instead of the controller guessing from command completion alone.
- Fixed the stale-plan bug where a later unrelated user request could continue an old active checklist.
- Fixed terminal mouse-report fragments such as `<64;85;43M` leaking into the composer while scrolling.
- Added provider guidance to prefer bounded forms for event-stream listeners such as `xinput test`, `tail -f`, `watch`, and similar monitor commands.

## Verified
- `go test ./internal/... -count=1`

## Current Problem
- The controller-side optimistic plan advancement bug is removed, but the UX is still not ideal.
- `Ctrl+G` remains a weak resume affordance because it does not explain enough about what Shuttle thinks it is resuming.
- The model is now told to bound listener commands, but compliance still depends on the provider following instructions.

## Next Steps
1. Replace the generic `Ctrl+G` resume path with a more explicit tracked-command resume UI that says what Shuttle is resuming and why.
2. Add a clearer active-command card affordance for `F3` takeover when a monitor/listener is still running.
3. Consider a controller-level fallback for runaway listener commands, such as a suggested `keys` interrupt proposal on check-in when a command is obviously just waiting for an external event stream.
4. After the checklist/resume UX is clean, archive `inprocess/P0.md` and start the next active slice.

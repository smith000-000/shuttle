# Resume

## Current Status
- P0 is effectively landed: Shuttle now has a real `internal/agentruntime` seam, the controller depends on `Runtime` plus a runtime-host bridge, and provider/model orchestration is no longer fused into controller turn entrypoints.
- Shared runtime/model contract types now live under `internal/agentruntime`; controller re-exports aliases only for compatibility.
- The obsolete provider-side runtime wrapper scaffold is removed.
- The current built-in runtime is still the only runtime implementation, but alternate runtimes can now be added above Shuttle’s shell/runtime substrate without reopening controller orchestration.

## Handoff Model
- `F2` is always the persistent tracked shell handoff.
- This includes remote shells: if the user is on SSH, `F2` remains the correct shell target.
- `F3` is the separate owned local execution-pane handoff when Shuttle is running a distinct agent command pane.
- `F3` now detaches on `F3`, so the execution-pane handoff behaves like a real toggle instead of requiring `F2` to get back.

## Recent Fixes
- Fixed the take-control regression where long-running owned execution panes were not reachable directly.
- Split stable shell/runtime handoff semantics:
  - `F2` = persistent tracked shell
  - `F3` = active owned execution pane
- Added a louder plan-card hint when Shuttle is waiting for `Ctrl+G` to continue from the latest command result.
- Fixed the provider/runtime contract leak where Shuttle exposed `tracked_pane` and `execution_pane` to the model.
- Fixed owned execution transcript notices so they no longer print raw pane IDs.
- Added a controller-side guard that blocks agent proposals like `tmux capture-pane -pt %3 ...` when they target Shuttle-managed pane IDs.

## Verified
- `go test ./internal/controller ./internal/provider ./internal/app ./internal/tui -count=1`
- `go test ./internal/... -count=1`

## Current Problem
- There is still a continuation-policy bug after interrupted planned commands.
- Repro shape:
  1. Start a planned multi-step command in an owned pane.
  2. Use `F3`, interrupt it with `Ctrl+C`, return, then press `Ctrl+G`.
  3. Shuttle now avoids the pane-ID leak, but the plan can still desync and choose the wrong next step.
- In practice, the agent may continue from stale plan assumptions instead of cleanly folding the interrupted command result into the active checklist state.

## Next Steps
1. Fix post-interrupt plan continuation so `Ctrl+G` after an interrupted owned-pane command advances from the actual `LastCommandResult` and updates the active plan consistently.
2. Add targeted controller/TUI regression coverage for:
   - interrupted planned command in owned pane
   - `F3` handoff + `Ctrl+C` + `Ctrl+G`
   - rerun/completion path after interruption
3. Re-run the manual local shell checklist for:
   - interrupting long-running owned commands
   - resuming planned work after interruption
   - remote shell handoff sanity with `F2`
4. After that, archive `inprocess/P0.md` and start the next active slice.

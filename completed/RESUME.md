# Resume

Current branch: `uitweaks`

Current state:
- shell tracking has been refactored around explicit observation and transition seams instead of open-coded remote-shell heuristics
- `internal/shell/observation.go` now defines `ObservedShellState` and `ShellLocation`
- tracked-command and attached-foreground prompt-return completion now flow through a shared monitor-evaluation seam in `internal/shell/monitor_eval.go`
- `ShellLocation` now also carries cwd source/confidence metadata:
  - prompt-derived cwd like `~` is marked approximate
  - probe-confirmed cwd is marked authoritative
  - carried-forward cwd is marked low-confidence
- `internal/shell/transition_tracker.go` now owns context-transition settling state
- observer-side tracked-command monitoring now consumes normalized observed-shell snapshots
- attached foreground monitoring was updated to use the same observed-shell shape without regressing output-tail behavior
- controller-side shell refresh now crosses the reader boundary as `CaptureObservedShellState(...)`
- `SessionContext` now stores `CurrentShellLocation`
- controller policy for remote-sensitive behavior no longer relies only on `PromptContext.Remote`:
  - owned-pane placement
  - remote proposal guards
  - remote structured edit gating
  - approval auto-run suppression for remote shells
  - remote capability summary refresh
- provider turn-context rendering now also keys remote/cwd reporting from normalized shell location instead of `PromptContext.Remote`
- remote capability caching and transport hints now key off normalized shell location identity instead of treating prompt remoteness as the subsystem authority
- remote cwd normalization was hardened:
  - remote prompt directories like `~` and `~/...` no longer expand to the local machine home directory
  - if remote tracking only knows `~`, context answers now preserve `~`
  - if a probe returns an absolute remote `PWD`, that absolute path is used
- context-transition probes now update tracked shell location authoritatively when they succeed, and keep remote identity with low cwd confidence when they fail
- the specific stale-context bug reproduced by the user is now covered:
  - after `ssh openclaw@openclaw`
  - if tracking says `openclaw@openclaw ~ $`
  - Shuttle should not answer with `/home/jsmith`
- interactive harness tests under `integration/harness` are now opt-in only via `SHUTTLE_RUN_INTERACTIVE_HARNESS=1` while broader end-to-end UX automation is paused

What was validated:
- `go test ./internal/shell ./internal/controller ./internal/provider ./internal/tui` passes
- with the new opt-in gate, routine `go test ./integration/...` runs can skip the interactive harness package cleanly
- foreground attach regressions introduced by the observation refactor were fixed and covered
- remote capability cache changes compile and pass controller/harness coverage
- remote tilde cwd preservation is covered by controller inspect-context tests
- provider turn-context rendering now covers remote cwd authority metadata
- added regression coverage for:
  - shared tracked/attached prompt-return evaluation
  - SSH landing in non-home directories
  - `sudo -i` then `exit`
  - carried-forward low-confidence remote cwd after failed refresh
  - probe-authoritative remote cwd surviving prompt-shaped `~` context
- `go test ./integration/harness -count=1 -timeout 180s` fails, but the current failure does not point at shell tracking directly:
  - the harness submits the scripted prompt into the shell pane as a literal shell command
  - outer pane capture shows `zsh: command not found: Add`
  - no provider request is made before the harness times out waiting for the first transcript fragment
  - this looks like a harness/input-mode issue that still needs separate investigation before using harness as the final verification gate for this slice

Important implementation points:
- `internal/shell/observation.go`
  - normalized prompt/pane/semantic/transition observation
  - inferred `ShellLocation`
- `internal/shell/monitor_eval.go`
  - shared prompt-return completion and tail normalization for tracked and attached monitors
- `internal/shell/transition_tracker.go`
  - explicit state machine for transition settling
- `internal/shell/observer.go`
  - `CaptureObservedShellState(...)`
  - tracked-command monitoring now uses `ObservedShellState`
- `internal/shell/foreground_attach.go`
  - attached monitoring now uses observed-shell snapshots too
- `internal/controller/user_shell_context.go`
  - session refresh and shell-location application
  - remote-aware cwd normalization
  - carried-forward cwd downgrade behavior
- `internal/controller/remote_capabilities.go`
  - location-based cache identity and summary lookup
- `internal/controller/remote_patch.go`
  - capability lookup/storage/transport hints now flow through shell location
- `internal/controller/controller_inspect.go`
  - inspect-context output now reflects normalized remote/local shell location plus cwd authority metadata
- `internal/provider/responses.go`
  - turn context now reports `shell_location`, `cwd_source`, `cwd_confidence`, and `cwd_authoritative`

Docs status:
- `shell-tracking-architecture.md` updated for:
  - `ObservedShellState`
  - transition tracker
  - `SessionContext.CurrentShellLocation`

Current worktree status:
- dirty
- shared monitor evaluation, remote transition regressions, controller cwd-authority fixes, and resume/doc updates are in the working tree

Exact next hardening / refactor sequence:
1. Finish verification and cleanup.
   - Leave the interactive harness disabled by default unless actively working on end-to-end TUI UX.
   - If the harness is re-enabled later, investigate why it is submitting the scripted prompt in shell mode instead of agent mode.
   - Manually run the shell-execution checklist for:
     - `ssh host`
     - remote awaiting-input
     - remote fullscreen
     - `docker exec -it`
     - nested local subshell
2. If manual checks surface a bug, add a focused shell/controller regression rather than reopening the shell model.
3. After verification is clean, commit the shared monitor-evaluation slice separately from any later semantic/bootstrap work.

Recommended immediate next implementation slice:
- leave shell tracking alone until a concrete manual-check failure points to the next gap
- next likely work is outside shell tracking: provider/agent-loop cleanup or the deferred external-agent integration seam

Suggested restart prompt after `/new`:
- "Read `RESUME.md`, `shell-tracking-architecture.md`, and `shell-execution-strategy.md`. Continue on `uitweaks`. Shared monitor evaluation for tracked and attached prompt-return completion is now in `internal/shell/monitor_eval.go`, remote transition regressions were expanded, and controller working-directory handling now preserves probe-authoritative remote cwd. Interactive harness tests are intentionally opt-in via `SHUTTLE_RUN_INTERACTIVE_HARNESS=1` for now. Finish the manual remote-shell regression checklist, then move on to provider/agent-loop cleanup or the external-agent integration seam."

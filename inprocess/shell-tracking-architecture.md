# Shuttle Shell Tracking Architecture

## Purpose
Describe the current shell tracking architecture on `main` so contributors can reason about command monitoring, semantic shell signals, pane ownership, handoff, and recovery without reverse-engineering the implementation from `internal/shell`, `internal/controller`, and `internal/tui`.

This note is intentionally implementation-facing. It describes how the current system works, where the authoritative state lives, and what tradeoffs still exist.

---

# 1. Core Model

Shuttle now has two distinct shell surfaces:
- one persistent user shell target
- zero or one active owned execution panes for agent-approved commands

The persistent user shell target is:
- a tmux session name
- a tmux pane ID
- represented in controller state as `SessionContext.TrackedShell`

Current design rule:
- the persistent tracked shell is the continuity surface for `$>`, cwd, recent manual commands, and recent manual file-affecting actions
- `F2` always targets that persistent tracked shell; `F3` targets a separate active owned execution pane when one exists
- approved agent commands no longer need to run inside that persistent shell pane
- agent-approved commands can run in detached owned tmux panes and are tracked through their own `CommandExecution.TrackedShell`
- the TUI is not the source of truth for either target
- the shell observer is the source of truth for low-level pane recovery and owned-pane provisioning
- the controller is the source of truth for product state built on top of those targets

This is the foundation for future multi-card work. The product still enforces serial command execution, but it no longer assumes “active command pane” and “persistent user shell pane” are always the same thing.

---

# 2. Main Components

## 2.1 tmux Client and Workspace

Relevant files:
- `internal/tmux/client.go`
- `internal/tmux/workspace.go`

Responsibilities:
- talk to the real tmux server and socket
- create or repair the Shuttle workspace
- identify panes in a session
- bootstrap either:
  - the normal two-pane workspace
  - a shell-only recovery session

Important distinction:
- normal startup uses `BootstrapWorkspace(...)`
- destructive recovery uses `BootstrapShellSession(...)`

That distinction exists because after a broken handoff recovery there is no real embedded bottom TUI pane inside tmux. Recreating a fake two-pane tmux session caused confusing double-`exit` behavior, so recovery now prefers a one-pane shell session.

## 2.2 Shell Observer

Relevant file:
- `internal/shell/observer.go`
- `internal/shell/observation.go`
- `internal/shell/transition_tracker.go`

The observer is the low-level shell integration layer.

Responsibilities:
- capture pane output
- preserve an ANSI-bearing display capture separately from the sanitized control capture
- capture prompt context
- send commands into the tracked pane
- run tracked command monitors
- attach to a manually started foreground command
- recover from stale pane IDs
- recreate missing tmux sessions when configured with a session ensurer
- manage semantic shell source precedence
- normalize prompt, pane, semantic, and transition evidence into a single observed-shell snapshot for monitor loops
- drive context-transition settling through an explicit transition tracker instead of open-coded per-loop local variables

The observer knows about shell and tmux mechanics. It should not own transcript policy or TUI rendering decisions.

Current transition rule:
- shell-transition commands like `ssh`, `exit`, and `sudo -i` do not settle from a single apparent prompt return
- Shuttle now requires a candidate prompt to be re-observed and then verified by a context probe before the transition is treated as settled
- if the tail still looks like `password:` or other awaiting-input text, Shuttle keeps the transition unresolved and does not inject the probe

Current implementation note:
- monitor loops now build an `ObservedShellState` snapshot that bundles prompt parse, pane metadata, semantic state, remembered transition kind, and inferred shell location
- tracked-command and attached-foreground monitors now share the same semantic/prompt completion helpers for semantic command-finish settlement, semantic prompt settlement, inferred prompt-return completion, and tail normalization; the launch-mode-specific code only owns start detection, attach gating, and capture windowing
- display-oriented transcript/output surfaces now use ANSI-preserving capture while command parsing, prompt reconciliation, and execution-state inference continue to use sanitized plain-text capture
- `ShellLocation` now also carries cwd source/confidence metadata so the controller can distinguish prompt-derived directories, probe-confirmed directories, and low-confidence carried-forward cwd
- context-transition polling now routes through a dedicated transition tracker state machine with states such as `submitted`, `candidate_prompt_seen`, `awaiting_interactive_input`, and `probe_verifying`
- this is intended as an internal cleanup seam so later remote-shell reliability work can replace scattered boolean checks without redesigning the shell product model again
- when a post-transition probe succeeds, its `PWD` becomes the authoritative tracked cwd; when it fails after a remote transition, Shuttle keeps the remote identity but downgrades cwd confidence instead of overclaiming certainty
- `exit` / `logout` transitions now have a bounded fallback settle path when prompt parsing does not conclusively finish but Shuttle can still see that the tracked shell has recovered:
  - the tracked pane respawned and the resolved pane is back in a shell
  - or the tail clearly shows a disconnect/unwind sequence such as `logout` or `Connection to ... closed.`
- that fallback exists because tmux respawn and remote transport teardown can return Shuttle to a healthy shell without always leaving a fresh parseable trailing prompt in the capture window

## 2.3 Semantic Collectors

Relevant files:
- `internal/shell/semantic_collect.go`
- `internal/shell/semantic_stream.go`
- `internal/shell/semantic.go`

Semantic shell state is consumed from multiple sources with explicit precedence:
- `osc_stream`
- `osc_capture`
- `state_file`
- heuristics

Current preferred source is `osc_stream`, backed by `tmux pipe-pane -O`.

State-file fallback remains last priority and is now intentionally stricter than before:
- only newline-terminated payloads are accepted from the file-backed fallback
- payloads must validate the expected shell/event shape
- stale `command` file-state is discarded, while older `prompt` state can still represent an idle local shell
- live `osc_stream` and `osc_capture` observations still win over the file-backed fallback

Important detail:
- `pipe-pane -O` produces a cumulative stream
- Shuttle reduces that stream incrementally instead of rescanning the full output buffer snapshot-style
- `OSC 133;C` is treated as command start and `OSC 133;D` / `OSC 133;D;<exit>` are treated as command finish; `OSC 133;A` / `OSC 133;B` remain prompt-boundary markers instead of being overloaded as the only completion signal

The stream collector is generation-scoped:
- each observer instance gets its own runtime file
- old generation files are ignored
- dead generations are pruned conservatively

That avoids stale semantic events after crash/restart.

## 2.4 Controller

Relevant files:
- `internal/controller/controller.go`
- `internal/controller/types.go`

The controller translates shell reality into product state.

Responsibilities:
- hold `SessionContext`
- hold `TaskContext`
- create and update `CommandExecution`
- talk to the agent/provider layer
- emit transcript events
- reconcile shell state after handoff
- synchronize the tracked shell target before reads and writes
- summarize recent manual user-shell commands and file-affecting actions from shell history
- provision owned execution panes for agent-approved commands when the runner supports them

Important state:
- `SessionContext.TrackedShell`
- `SessionContext.WorkingDirectory`
- `SessionContext.LocalWorkingDirectory`
- `SessionContext.LocalWorkspaceRoot`
- `SessionContext.RecentManualCommands`
- `SessionContext.RecentManualActions`
- `SessionContext.CurrentShellLocation`
- `TaskContext.PrimaryExecutionID`
- `TaskContext.ExecutionRegistry`
- `TaskContext.CurrentExecution`
- `TaskContext.LastCommandResult`

Current rule:
- `TrackedShellTarget` is the explicit ownership field
- `SessionContext` describes the persistent user shell
- `SessionContext.CurrentShellLocation` is the controller's normalized view of whether that shell is local, remote, containerized, or nested, plus how trustworthy the tracked cwd is
- `SessionContext.WorkingDirectory` is the tracked shell cwd, not the controller host cwd
- controller-owned local host probe fields such as `LocalWorkingDirectory` and `LocalHomeDirectory` describe where Shuttle itself is running on the host machine and must remain distinct from tracked-shell state
- `CommandExecution.TrackedShell` describes where the active command is actually running
- `CommandExecution.LatestOutputTail` is the sanitized control tail; `LatestDisplayTail` is the display-oriented tail that may retain ANSI styling
- owned execution results do not overwrite persistent user-shell cwd or prompt context
- controller and provider policy should key remote-sensitive decisions from `CurrentShellLocation`, not from `PromptContext.Remote`
- if the tracked shell pane ID changes during recovery or local-shell respawn, the controller must migrate any live user-shell execution that was bound to the old pane so active execution state and handoff targets continue to follow the real tracked shell

## 2.5 TUI and Handoff

Relevant files:
- `internal/tui/model.go`
- `internal/tui/handoff.go`

Responsibilities:
- render transcript and shell state
- start `F2` handoff into the controller-selected take-control pane
- route `F2` to the controller-selected take-control target
- route `KEYS>` and local interrupts to the active execution pane when one exists
- mirror controller-tracked pane/session updates into local UI state

Current `KEYS>` behavior:
- `awaiting_input` and `interactive_fullscreen` executions can use `KEYS>`
- a freshly observed `awaiting_input` prompt auto-opens `KEYS>` once as a convenience path
- `Shift-Tab` inside `KEYS>` dismisses that auto-open suggestion for the current observed prompt fingerprint instead of toggling agent/shell mode
- a successful key send also suppresses auto-reopen for that same unchanged prompt fingerprint
- if the observed waiting prompt changes materially, `KEYS>` may auto-open again
- each send requires a fresh observed execution/tail snapshot before the TUI will transmit raw keys
- manual `Enter` in `KEYS>` stays exact for "press any key" style waits, but appends `Enter` for password, confirmation, and menu-style prompts

Important rule:
- the TUI does not decide which pane is authoritative
- it asks the controller for both the persistent tracked shell target and the current take-control target, and mirrors those into local UI state
- the TUI does not invent durable active-command records anymore; active command state comes from controller `CommandStart` events and `ActiveExecution()` polling
- `F2` is a command handoff whenever the active execution pane itself is the take-control target
- owned interactive execution panes are marked in tmux as temporary Shuttle execution panes and are expected to disappear when the command completes

---

# 3. Command Tracking Modes

Current fallback ladder:
- use the controller's currently tracked execution when one exists
- on `F2` return, reconcile prompt state against the controller-selected take-control target for the active execution
- when the post-handoff prompt text is not parseable, handoff reconciliation may still settle from shell-observed wrapper state plus semantic exit metadata or explicit interrupt evidence such as `^C`, but the controller must not synthesize a fake prompt context from that evidence
- for `exit` / `logout`, reconciliation may also settle from tracked-pane respawn or disconnect-tail evidence before waiting forever on a fresh prompt parse
- if no execution reconciles, ask the controller to attach to a manually started foreground command
- if neither applies, treat the shell as having no active tracked command

Important correctness rule:
- prompt context is only accepted when Shuttle can see a current trailing prompt; shell-location updates and semantic exit evidence must remain separate from prompt-context acceptance
- prompt-looking lines buried earlier in pane scrollback do not count as proof that a quiet foreground command has completed
- this prevents false `exit=0` reconciliation for handoff cases like `sleep 20`

Important presentation rule:
- the live shell tail preview is only for in-flight commands
- once Shuttle emits a terminal `command_result` or `error`, transcript ownership takes over and the live tail preview is cleared

The current system supports four practical categories of shell tracking.

## 3.1 Persistent User Shell Context

This is the long-lived shell Shuttle treats as “your shell”.

Responsibilities:
- preserve cwd continuity
- preserve recent manual shell output
- preserve recent manual commands and file-affecting actions
- own `$>` direct commands
- own `F2` terminal handoff

This context is refreshed from:
- prompt capture
- recent pane output
- the tmux-backed shell history file configured for the session

The tracked cwd is not a single undifferentiated string anymore:
- prompt-derived cwd is useful but not treated as authoritative for remote overclaiming-sensitive flows
- probe-confirmed cwd is authoritative
- carried-forward cwd is explicitly low-confidence until a fresh prompt or probe refreshes it

That is what lets later prompts like “see the file I just renamed” work even when the actual rename happened manually in the user shell rather than inside an agent-owned command pane.

## 3.2 Shuttle-Started Tracked Commands In The User Shell

Path:
- controller submits command
- observer injects tracked transport
- monitor watches pane output and semantic state
- controller turns the final monitor result into transcript events

This is the strongest path because Shuttle knows:
- which command it launched
- when tracking started
- what command ID and sentinels belong to it

## 3.3 Owned Agent Execution Panes

Path:
- controller chooses an owned execution pane for agent proposal / approval commands
- observer provisions a detached tmux window in the current session
- tracked transport runs in that owned pane
- controller keeps the owned pane on the `CommandExecution` record
- controller cleans up the owned window when the execution completes or is abandoned

Important rule:
- owned execution results belong to `LastCommandResult` and the execution record
- they do not replace persistent user-shell cwd or prompt state
- active owned execution panes can take over `F2` temporarily, including long-running noninteractive commands, interactive prompts, and fullscreen apps

This makes agent command tracking dramatically more stable because command lifecycle no longer depends on inferring what is happening inside the shared user shell pane.

## 3.4 Manually Started Foreground Commands

Path:
- user starts a command directly in the shell pane or during handoff
- Shuttle returns from `F2`
- controller tries `AttachForegroundCommand(...)`
- observer creates a monitor from the current foreground state

This path is weaker than fully tracked transport because the command was not launched by Shuttle, but it is good enough to:
- detect that something is still running
- keep showing output
- reconcile completion back into `LastCommandResult`

Additional guardrail:
- attached foreground monitors now require a current prompt before completing from prompt-return semantics
- stale prompt scrollback is ignored the same way it is for the primary handoff-reconciliation path
- prompt-inference transport panes such as `ssh` now distinguish the outer transport from the inner remote command, so Shuttle can settle the transport once the remote prompt is reused and avoid leaving later remote commands mislabeled as stuck `awaiting_input`
- attached foreground fallback cleanup strips echoed commands according to the active transport context, so prompt-inference panes do not leave `ssh`/wrapper labels stuck onto later remote output

## 3.5 Context-Transition Commands

Examples:
- `bash`
- `zsh`
- `ssh`
- `docker exec -it`
- `kubectl exec -it`
- `sudo -i`

These are treated specially because they are more like shell ownership transitions than normal short commands.

Current behavior:
- detect likely transition kind
- suppress inappropriate local semantic assumptions when control moved elsewhere
- surface interim `awaiting_input` state from the transition tracker when prompts like passwords or host-key confirmations appear, so tracked `ssh`/`sudo -i` transitions do not look like generic running commands
- after `F2` return, reconcile active executions from observed shell state, semantic exit evidence, and explicit interrupt evidence such as `^C` even when a final prompt line is incomplete or unparseable
- bootstrap local nested shells conservatively after prompt settlement
- remain heuristic-only for remote/container cases unless later bootstrap work expands coverage

---

# 4. Semantic Shell Model

## 4.1 Why Semantic Signals Exist

Prompt scraping alone is too ambiguous for:
- exit-code accuracy
- cwd tracking
- differentiating prompt return from quiet long-running commands
- nested shell transitions

So Shuttle consumes:
- `OSC 133` prompt and command lifecycle markers
- `OSC 7` cwd markers

## 4.2 Current Semantic Lifecycle

The current reducer tracks:
- prompt start
- command line start
- command execution start
- command finish with exit code
- cwd updates

The reducer is designed for split and incremental marker delivery rather than assuming markers arrive as complete lines in one snapshot.

## 4.3 Bootstrap Scope

Current bootstrap is intentionally conservative.

Implemented:
- local shell integration for supported local shells
- local nested-shell reintegration when a child `bash` or `zsh` becomes the new prompt owner

Not yet the default:
- remote semantic bootstrap
- container semantic bootstrap
- persistent RC-file installation

The design intent is:
- semantic bootstrap should improve signal quality
- semantic bootstrap must never be required for correctness
- failure should degrade back to heuristic monitoring

---

# 5. Tracked Shell Ownership

## 5.1 Why This Became Explicit

Earlier code paths overloaded one field, `TopPaneID`, for multiple jobs:
- shell read/write target
- handoff target
- startup workspace top pane
- recovered pane alias

That was workable for a single stable session but broke down under:
- pane recreation
- session recreation
- `F2` handoff recovery
- future plans for parallel command tracking

## 5.2 Current Ownership State

The controller now carries:

```go
type TrackedShellTarget struct {
    SessionName string
    PaneID      string
}
```

Current meaning:
- `SessionName`: the tmux session that currently owns the tracked shell pane
- `PaneID`: the authoritative pane for shell observation and shell input

Current invariants:
- controller synchronizes this target before important shell reads and writes
- TUI handoff config mirrors this target
- provider context includes it as `tracked_session` and `tracked_pane`
- controller/provider/TUI state no longer carries a separate `TopPaneID` compatibility field

## 5.3 What This Does Not Mean Yet

This does not yet mean Shuttle supports arbitrary multi-pane or multi-command ownership.

Still true today:
- one tracked shell target is authoritative at a time
- one active execution is authoritative in controller state at a time
- a second tracked shell command is rejected while another execution is active

This is groundwork, not full parallelism.

---

# 6. Recovery Model

## 6.1 Pane Recovery

If a pane ID goes stale but the tmux session still exists:
- the observer lists panes in the session
- selects the top pane
- aliases the old pane ID to the replacement
- retries the operation

This covers:
- capture
- pane info
- send-keys
- semantic `pipe-pane` setup

## 6.2 Session Recovery

If tmux reports:
- `no server running`
- `can't find session`

then the observer or handoff path can call a session ensurer that:
- recreates a shell session
- restores the detach key binding
- returns a usable top pane again

Current recovery shape:
- shell-only recovery session during destructive recovery
- full two-pane layout only on normal startup

## 6.3 Handoff Recovery

`F2` handoff uses the same tracked shell identity and recovery ideas:
- resolve the current tracked pane
- recover the session if needed
- attach to the session
- rely on the detach key to return to Shuttle

This fixed a class of bugs where:
- the shell pane died
- tmux rebuilt the session
- Shuttle still tried to attach to a dead pane or wrong socket

---

# 7. Data Flow

## 7.1 Tracked Command Path

1. Controller syncs the tracked shell target.
2. Observer ensures local semantic integration if needed.
3. Observer injects tracked transport into the tracked pane.
4. Monitor reads pane output, pane metadata, and semantic state.
5. Controller converts monitor snapshots into `CommandExecution`, keeping separate control and display tails.
6. Controller emits transcript events and updates `LastCommandResult`.
7. TUI renders the display summary/tail while controller logic continues to reason over sanitized output and any updated tracked shell target.

## 7.2 Handoff and Resume Path

1. TUI takes control with `F2` using the controller-selected take-control target.
2. User interacts directly with the persistent shell pane or a temporary owned execution pane.
3. On return, controller first attempts prompt-return reconciliation for an existing active execution.
4. If no owned execution reconciles, controller tries to attach to a foreground command.
5. Updated shell context and results flow back into transcript state.

## 7.3 Agent Context Path

1. Controller gathers `SessionContext`, `TaskContext`, and recent shell output.
2. Provider formatter includes prompt, cwd, session, tracked shell target, and execution metadata.
3. Agent response comes back as structured message/plan/proposal/approval.
4. Controller decides whether that becomes a shell command, approval card, or transcript-only output.

---

# 8. Current Sharp Edges

The current architecture is much stronger than the earlier one-off fixes, but a few limits are still real.

- parallel command tracking is not implemented yet
- controller projects a single primary `CurrentExecution` even though it now keeps an internal execution registry
- serial submission is enforced; the registry is future-facing ownership scaffolding, not permission for overlap
- remote/container semantic bootstrap is intentionally incomplete
- interactive/fullscreen classification still relies partly on tmux metadata and heuristics
- semantic signal quality still depends on shell support and tmux behavior

Contributors should avoid assuming the current tracked-shell ownership model already solves multi-execution scheduling. It only makes the ownership boundary explicit enough to build on safely.

---

# 9. Contributor Rules of Thumb

- Do not add new code that reads or writes the shell pane by assuming startup `%0` is still valid.
- Prefer controller-synced tracked-shell state over ad hoc pane IDs in UI code.
- Treat semantic bootstrap as best-effort signal improvement, not a correctness prerequisite.
- Keep shell recovery conservative. Rebind and retry; do not invent state silently.
- If a change affects handoff, verify both directions:
  - Shuttle to tmux
  - tmux back to Shuttle
- If a change affects nested or remote shells, verify prompt settlement and result reconciliation separately.

---

# 10. Related Files and Documents

Implementation:
- `internal/shell/observer.go`
- `internal/shell/foreground_attach.go`
- `internal/shell/owned_execution.go`
- `internal/shell/semantic.go`
- `internal/shell/semantic_collect.go`
- `internal/shell/semantic_stream.go`
- `internal/shell/transition.go`
- `internal/controller/controller.go`
- `internal/controller/types.go`
- `internal/controller/user_shell_context.go`
- `internal/tui/model.go`
- `internal/tui/handoff.go`
- `internal/tmux/workspace.go`

Related docs:
- [architecture.md](architecture.md)
- [protocol-shell-observation.md](protocol-shell-observation.md)
- [shell-execution-strategy.md](shell-execution-strategy.md)
- [../BACKLOG.md](../BACKLOG.md)

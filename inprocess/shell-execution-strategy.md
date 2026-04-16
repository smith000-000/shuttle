# Shell Execution Strategy

## Purpose
Rethink Shuttle's shell and agent execution flow before adding more point fixes.

The current model is too synchronous:
- submit a command
- block until sentinel completion
- show either result or timeout

That works for short local commands, but it breaks down for:
- long-running commands such as `rsync`, `scp`, `docker build`, `npm install`, or large test suites
- interactive commands such as `sudo`, `ssh`, `read`, `vim`, `less`, `top`, and `btop`
- remote workflows where a session may outlive a local UI process
- agent-driven workflows that should keep checking progress instead of stalling on a hard timeout

This note captures what Warp and Wave appear to do, what Shuttle should borrow, and what should change next.

## Research Summary

### Warp
Warp's current agent docs describe a model that is more stateful than Shuttle's current implementation:
- `Full Terminal Use` says Warp's agents can interact with active terminal apps, monitor live output, and run commands in an attached terminal session.
- `Blocks as Context` treats command/output blocks as explicit context units for later agent turns.
- `Task Lists` treats longer agent work as a tracked sequence of steps with progress updates.
- `Profiles & Permissions` separates autonomy and tool permissions from the core agent loop.

What Shuttle should take from this:
- shell work should be modeled as ongoing state, not just request/response
- prior command results should be first-class reusable context objects
- plan/task progression should advance around command state, not just around user prompts

What Shuttle should not assume:
- Warp can do more because it is a full terminal product
- Shuttle should not try to emulate all of Warp's terminal control behavior inside Bubble Tea

### Wave
Wave's public docs and README suggest a different but equally relevant model:
- Wave is explicitly block-based and treats commands as isolated command blocks.
- Wave documents durable SSH sessions that survive disconnects and keep long-running remote processes alive.
- Wave's `wsh` helper creates a tighter shell-to-app bridge for remote and local state sharing.
- Wave AI today is context-aware over terminal output, widgets, files, and web content; command execution is still framed as something gated and approval-oriented.

What Shuttle should take from this:
- command lifecycle and monitoring should be explicit objects in the UI
- long-running and remote sessions need a runtime model that survives UI interruption
- local/remote shell state needs a shell-side bridge eventually, not only pane scraping forever

What Shuttle should not copy directly:
- Wave is a terminal app with richer shell integration and widget layout
- Shuttle is intentionally tmux-backed and should preserve that constraint

## Diagnosis
Shuttle's current bugs are symptoms of the same architectural mismatch:
- fixed command deadlines treat `sleep 15` and `rsync` as failures even when they are behaving normally
- interactive programs surface as errors because the system is waiting for completion instead of entering a monitored or handoff state
- the agent loop currently assumes a command either finishes quickly or fails
- shell context refresh and sentinel plumbing are carrying too much responsibility because the command model is too simple

The real problem is not just timeout values.

The real problem is that Shuttle currently models shell execution as a blocking function call, when it should model it as a job with state transitions.

That diagnosis still holds after the recent hardening work:
- the shell substrate is much stronger now
- serial agentic looping is workable
- the serial tracking model is now stable enough that the next slice should not be more shell-tracking churn
- the remaining risk is mostly transcript/UI complexity and monolithic controller/TUI code, not core command truth

## Current Status
Shuttle now has a usable first pass of the redesigned execution stack on `main`:
- first-class monitored executions for tracked shell commands
- local managed transport using sourced temp scripts instead of giant inline wrappers where possible
- active execution states including `awaiting_input`, `interactive_fullscreen`, `background_monitoring`, `canceled`, and `lost`
- `F2` take-control handoff and reconciliation back into Shuttle
- returning from `F2` while a tracked command is still active now resumes monitoring only; it does not force an immediate extra agent turn before the command has actually reconciled
- raw `KEYS>` input for active prompts and fullscreen apps, including explicit control-key tokens such as `<Ctrl+C>`
- TUI-level guardrails for `KEYS>` and agent key proposals so each send requires a fresh observed execution/tail snapshot and consumes that lease until Shuttle refreshes again
- automatic `KEYS>` entry for fresh `awaiting_input` prompts, with explicit `Shift-Tab` dismissal that suppresses re-entry until the observed prompt state changes
- successful `KEYS>` sends also suppress auto-reentry for that same unchanged prompt state, so password or menu prompts do not immediately reopen after an answer is sent
- manual `KEYS>` Enter now distinguishes likely submit prompts from exact-key prompts:
  - password / passphrase / confirmation / menu-style waits append Enter automatically
  - "press any key" style waits remain exact and do not append Enter unless the user explicitly uses `Ctrl+Y` or inserts a newline
- agent-side recovery guidance informed by a larger recovery snapshot
- first-class agent `keys` proposals so the model can ask Shuttle to send small raw key sequences instead of only narrating them
- bounded interactive/fullscreen check-ins that eventually pause automatic model retries and require an explicit `Ctrl+G` resume
- serial execution ownership enforcement plus an internal execution registry so future multi-card work has a stable base without allowing overlap yet
- serial auto-continue hardening so ordered one-command-at-a-time workflows can keep progressing without an extra user "go"
- plan cards demoted to informational state instead of approval-like control flow, with continuation turns now suppressing stale replacement plans and emitting explicit plan-complete state when needed
- proposal and approval refinement notes can now explicitly retire the active plan when the user abandons that path or chooses to run the remaining long step outside Shuttle
- quiet commands like `sleep 20` no longer reconcile as completed from stale prompt scrollback after `F2`
- transcript result entries now reflect exit status instead of always presenting a green success result
- transcript and active-execution views now preserve ANSI-colored shell output for display while keeping sanitized plain-text summaries for controller state and prompt reconciliation
- prompt-inference transport commands such as `ssh` now distinguish the outer transport from the inner remote command, surface auth waits as `awaiting_input`, and settle more reliably after `F2` return
- tracked-shell recovery now rewrites any live user-shell execution to the new pane target when tmux respawns the tracked shell, instead of leaving active execution state bound to a dead pane ID
- `exit` / `logout` transitions now have fallback settlement when Shuttle can see either a disconnect tail or a respawned tracked shell even if the prompt parser never produces one final clean trailing prompt line
- TUI shutdown now cleans up against the final recovered tracked-shell session from the live model instead of assuming the originally bootstrapped workspace session is still current after `F2` recovery

What is still not done:
- fullscreen/interactive detection still needs stronger terminal-behavior signals beyond current tmux metadata heuristics
- transcript and UI polish still need cleanup now that the shell/runtime model has changed substantially
- the agent still needs tighter guardrails around when it should propose raw keys versus when it should simply tell the user to take control
- semantic shell integration is only partially implemented; local shells now have a first-pass semantic shim, but Shuttle still needs broader raw-marker consumption and subshell/bootstrap support
- for ordinary local-shell commands, semantic `OSC 133` finish markers now settle completion before prompt/tail heuristics; transport/context transitions still require the explicit probe path because they change the shell identity rather than just ending one command
- `internal/controller/controller.go` and `internal/tui/model.go` are now large enough that further point fixes should come with a decomposition backlog:
  - controller: execution lifecycle/state machine, agent-turn normalization, plan management, and tracked-shell ownership helpers
  - TUI: composer/input routing, transcript rendering, proposal/approval state, and handoff/fullscreen control
- the recent shell hardening did require an architecture review before more behavior was added, and that review has already driven a first cleanup pass across:
  - exit/logout transition settlement
  - tracked-pane migration into active executions
  - prompt-return fallback rules across local and remote flows
  - controller/TUI ownership boundaries during `F2`/`F3` reconciliation

Recent direction change that is now the baseline on `main`:
- keep one persistent user shell pane as the continuity surface for cwd, `$>`, and recent manual shell history
- keep `F2` permanently bound to that persistent tracked shell and use `F3` only for a separate active owned execution pane when one exists
- expose that `F2`/`F3` split from a controller-owned execution overview instead of re-deriving it from raw pane IDs inside the TUI
- run approved agent shell commands in owned tmux execution panes by default
- exception: when the tracked user shell is remote, keep agent shell execution in that tracked remote shell instead of opening a local owned pane
- feed the agent structured recent manual commands/actions plus full command results, instead of forcing both concerns through one shared pane

That hybrid model is now the intended baseline. The remaining work is to simplify controller execution state around it, not to go back to “everything happens in the tracked shell pane.”

## Recommended Direction

The next major product slice should not be another command-tracking redesign.

For this branch, the better direction is:
- keep the current hybrid shell model stable
- clean up transcript/UI noise around results, plans, and handoff
- split the large controller/TUI files before layering more behavior onto them
- do an explicit architecture review of the shell-tracking and handoff stack before another recovery-oriented behavior slice
- defer multi-card / parallel execution work to a later branch

Semantic-shell expansion remains valuable, but it is no longer the immediate blocker for normal serial use. When that work resumes, it should still stay standards-based:
- `OSC 133` for prompt/command lifecycle
- `OSC 7` for cwd tracking

That lets Shuttle rely less on prompt scraping and tail heuristics for local shells. Shuttle already has a first-pass local semantic shim plus best-effort `osc_capture` / state-file consumption; the next step is to promote semantic consumption into a stronger primary source.

Keep this line explicit:
- first, adopt portable shell markers that are already documented and shared across terminals
- later, if needed, add an optional richer bootstrap/helper mode similar to Warp's subshell setup or Wave's shell bridge

Shuttle should not jump straight to a proprietary bootstrap model before implementing the standards-based marker path.

Current semantic-shell learning:
- local `bash` / `zsh` shims now emit `OSC 133` / `OSC 7`
- `OSC 133;C` is the authoritative semantic start marker and `OSC 133;D` is the authoritative semantic finish marker when those bytes survive through tmux capture
- tmux `capture-pane -e` is still useful, but only as an opportunistic snapshot source
- a live tmux spike proved `pipe-pane -O` preserves raw `OSC 133` / `OSC 7` bytes well enough to support a real semantic stream source
- the remaining blocker is not transport preservation; it is stream reduction
  - a cumulative pane-output stream must be reduced incrementally
  - a snapshot-style "keep the last marker in the whole buffer" parser will misattribute later prompt markers to earlier command events

Recommended source precedence going forward:
- `osc_stream`
- `osc_capture`
- `state_file`
- heuristics

### Subshell Bootstrap

After the standards-based local shell path is stable, Shuttle should add a separate subshell bootstrap layer for transitions such as:
- `ssh`
- `docker exec -it`
- `kubectl exec -it`
- nested `bash` / `zsh`
- `sudo -i` / `sudo -s`

This is distinct from basic `OSC 133` / `OSC 7` support:
- semantic shell integration:
  - local shell emits portable markers
  - Shuttle consumes them
- subshell bootstrap:
  - Shuttle detects a context transition
  - waits for the new shell prompt to settle
  - then injects an idempotent integration snippet into that new shell if it is safe to do so

Rules for subshell bootstrap:
- conservative by default
- only after prompt return, not mid-transition
- idempotent per shell session
- best-effort, never required for correctness
- silent fallback to heuristic monitoring if bootstrap fails
- do not assume Linux, tmux, or extra helper binaries on the remote target

This should be treated as a later capability layer, not as part of the first local semantic-shell milestone.

### Subshell Regression Checklist

Before changing subshell/bootstrap behavior, manually verify:
- `ssh host`
  - prompt returns cleanly
  - remote shell context is still detected
  - long-running remote command still reconciles after `F2 -> Ctrl+C -> F2`
- remote awaiting-input command over `ssh`
  - `awaiting_input` still works
  - `KEYS>` still works
  - agent `keys` proposal still works
- remote fullscreen app over `ssh`
  - `interactive_fullscreen` still works
  - `KEYS>` still works
  - no bogus local interrupt behavior returns
- `docker exec -it ... sh`
  - context transition still settles
  - prompt detection does not regress into `lost`
- nested local subshell such as `bash`
  - local prompt still stabilizes
  - tracked commands still complete normally

This checklist is also the manual hardening gate for shell-tracking refactors that touch prompt-return reconciliation, attached foreground monitoring, or remote transition settling, even when they are not changing bootstrap behavior directly.

Manual hardening checklist for the current baseline:
- verify remote transition settlement after `ssh`, auth waits, and remote prompt reuse
- verify tracked-pane migration and disconnect/respawn settlement after `exit` / `logout` or shell respawn
- verify awaiting-input and interactive/fullscreen recovery after `F2` return
- verify `F2` always returns to the persistent tracked shell and `F3` only appears for a distinct owned execution pane

### 1. Introduce First-Class Command Executions
Every shell command should create a tracked execution record.

Minimum fields:
- command ID
- command text
- origin: `user_shell`, `agent_proposal`, `agent_approval`, `agent_plan`
- start time
- latest output tail
- exit code, when complete
- shell context before start
- shell context after completion
- execution state

Recommended states:
- `queued`
- `running`
- `awaiting_input`
- `handoff_active`
- `background_monitoring`
- `completed`
- `failed`
- `canceled`
- `lost`

This becomes Shuttle's equivalent of a command block, even if the UI is not literally a block terminal.

### 2. Replace Hard Runtime Deadlines with Phase-Based Timeouts
Do not use one 10-second timeout for every shell command.

Instead split timeout behavior into phases:
- submission timeout
  Short. Only used to ensure tmux accepted the command.
- start-of-observation timeout
  Medium. Ensures Shuttle sees the begin sentinel or shell activity.
- completion timeout
  Usually none for normal commands.
  Completion should be driven by command state, not a universal deadline.
- watchdog timeout
  Only used when Shuttle loses track of a command completely.

Practical behavior:
- `sleep 15` should remain `running`, not fail at 10 seconds.
- `rsync` should stay visible as an active job with a live tail.
- if Shuttle stops seeing any shell activity and cannot rediscover state, that becomes a `lost` or `needs attention` condition, not an automatic hard failure.

### 3. Separate Three Execution Modes

#### Mode A. Short Tracked Command
For quick commands:
- inject sentinel-wrapped command
- capture output
- finalize result inline

Examples:
- `pwd`
- `ls`
- `git status`

#### Mode B. Monitored Long-Running Command
For long-running but noninteractive commands:
- inject command
- create active execution card
- keep showing live tail
- do not block the entire product on command completion
- allow the user and agent to inspect progress while it runs

Examples:
- `sleep 300`
- `rsync`
- `docker build`
- test suites

#### Mode C. Interactive Handoff Command
For commands that need terminal ownership:
- submit command to the pane chosen by controller policy for that execution
- expose live tail immediately
- allow explicit `F2` handoff into the exact tracked execution pane
- on detach, reconcile shell state and resume Shuttle

Examples:
- `sudo`
- `ssh`
- `read`
- `vim`
- `btop`

Do not depend on perfect prompt heuristics to enter this mode.
Use:
- explicit manual handoff
- optional known-command hints
- live shell tail as the source of truth

### 4. Add an Active Command Card
The UI should show one compact active-command area when a shell job is live.

Recommended contents:
- command summary
- state badge: `running`, `awaiting input`, `handoff active`, `monitoring`
- elapsed time
- last few shell lines
- actions:
  - `F2` take control
  - `Ctrl+C` send interrupt to the shell pane
  - `Ctrl+O` inspect full output
  - `Esc` collapse card

This is better than treating active shell work as just another transcript row.

### 5. Make Agent Continuation Event-Driven
Agent-owned commands should not just block and then continue.

Recommended behavior:
- when an agent starts a command, Shuttle records the execution as agent-owned
- if the command completes quickly, continue automatically as it does today
- if the command runs longer than a soft threshold, move into monitored mode
- for agent-owned monitored commands, schedule periodic check-ins
- for `awaiting_input` and `interactive_fullscreen`, stop automatic check-ins after a bounded retry count and hand control back to the user until they explicitly resume with `Ctrl+G` or send the agent a new note

Check-in prompt shape:
- command is still running
- here is the latest shell tail
- here is elapsed time
- decide whether to:
  - wait
  - ask the user for handoff
  - offer interruption
  - revise the plan

This is the closest Shuttle analogue to the Warp behavior you described:
- "still waiting on the command"
- "let me take a look"

Important constraint:
- do not spam the model every second
- rate-limit check-ins aggressively, for example after 10s, 30s, 60s, then every few minutes
- interactive waits should have a breakpoint rather than retry forever; once the pause threshold is hit, Shuttle should stop polling the model until the user explicitly resumes

### 6. Treat Interactive Handoff as a Normal State Transition
When the user presses `F2`:
- cancel only the local wait operation
- do not treat that cancellation as a user-facing error
- transition the execution into `handoff_active`
- attach to the controller-selected take-control pane, which may be the persistent shell pane or a temporary owned execution pane
- suspend automated waiting logic

When the user detaches back:
- refresh shell context
- capture the latest tail
- if the command completed, finalize it
- if the command is still alive, return it to `running` or `awaiting_input`
- if the command is agent-owned, trigger `ResumeAfterTakeControl`

This should be modeled as a first-class controller event, not a side effect.

### 7. Distinguish Shell Monitoring from Shell Context Refresh
Prompt awareness and remote-awareness are useful, but they should not own command lifecycle.

Use prompt/context refresh for:
- user@host
- cwd
- root vs normal prompt
- remote badge

Do not rely on prompt parsing alone for:
- whether a command is still running
- whether input is required
- whether the command failed

Those belong to execution-state tracking plus live tail monitoring.

### 8. Keep Heuristics as Hints, Not Ground Truth
Known commands such as `sudo`, `ssh`, `vim`, and `btop` should still be recognized.

But heuristics should only help with:
- suggesting handoff
- switching to monitored mode sooner
- changing labels in the UI

They should not be required for correctness.
The live shell tail and explicit `F2` flow are the correctness path.

### 8.1 Immediate Next Slice
The next practical slice after prompt-return reconciliation is:
- classify `awaiting_input` conservatively from live shell tail evidence
- keep `running` for commands that are clearly progressing
- reserve `lost` for cases where Shuttle truly cannot reconcile the command confidently, rather than treating quiet commands as lost by default

This should improve:
- password and confirmation prompts
- `read` / `input()` / "press any key" flows
- agent check-ins that currently say "still running" when the shell is actually waiting for input

### 8.2 Current In-Progress Hardening
The next execution-monitor hardening pass should focus on:
- improving confidence around quiet-but-still-valid long-running commands so they do not drift toward `lost`
- distinguishing "shell returned to prompt" from "prompt-like output happened inside an app or script"
- reducing duplicate or stale recovery messaging when the agent is already operating against a live ambiguous execution
- refining when active ambiguous states should trigger a `keys` proposal versus plain recovery guidance

### 9. Model Shell Connectivity as Capability Tiers
Shuttle should not assume every shell has the same observability.

This is especially important once the user is inside:
- an interactive remote SSH session
- a shell with a heavily customized multi-line prompt
- a non-Linux remote system
- a shell where we cannot install hooks or helpers

Instead of pretending one strategy is universal, Shuttle should classify the current shell session by capability.

Recommended capability tiers:

#### Tier A. `local_managed`
Shuttle owns the local tmux pane and injects commands directly into the local shell.

Available signals:
- sentinel begin/end markers
- tmux pane metadata
- live shell tail
- prompt/context refresh
- explicit local interrupt/handoff events

This is the strongest mode and should provide the best execution guarantees.

#### Tier B. `stream_observed`
Shuttle can observe the live pane output stream closely enough to detect terminal behavior, not just visible text.

Available signals:
- alternate-screen transitions
- fullscreen redraw behavior
- mouse-mode or cursor-mode changes
- shell-tail changes

Why this matters:
- it catches aliases, functions, and wrappers that eventually launch fullscreen apps
- it works for remote shells because the remote program still emits terminal control behavior into the local pane
- it is a better source of truth for fullscreen-app detection than a command-name list

Important constraint:
- this requires tmux-side pane stream observation, not just periodic capture of cooked pane text

#### Tier C. `text_observed`
Shuttle can see terminal output and prompt-like returns, but cannot rely on shell hooks, pane-stream parsing, or remote helpers.

Available signals:
- terminal stream text
- best-effort prompt recognition
- explicit user handoff
- shell-tail changes

Unavailable or untrusted signals:
- remote tmux or remote process metadata
- shell hooks
- platform-specific assumptions about Linux tools

This tier should remain usable, but Shuttle must treat completion and interruption as lower-confidence inferences.

#### Tier D. `hook_integrated`
Shuttle has shell-aware hooks inside the current shell session, for example prompt or command lifecycle hooks.

Available signals:
- explicit preexec/precmd-style command lifecycle events
- prompt/context changes
- terminal stream text

This is likely the best long-term answer for local shells, but it requires shell-specific integration.

#### Tier E. `remote_enhanced`
Shuttle successfully bootstrapped a temporary remote helper or session integration for the current remote shell.

Important constraints:
- it must not require remote tmux
- it must not assume Linux-only tooling
- it must degrade safely if the remote host does not support it

Available signals may include:
- explicit remote command lifecycle markers
- session-aware pwd/user/host reporting
- stronger remote execution reconciliation

This tier should be optional enhancement, not a requirement for basic SSH support.

Design implication:
- when Shuttle launches a command, it should use the strongest mechanism available for the current capability tier
- when the user drops into an opaque interactive remote shell, Shuttle should downgrade gracefully to `text_observed`
- prompt parsing should remain fallback logic, not the core execution contract

## Proposed Implementation Sequence

### Phase 1. Execution State Machine
- add `CommandExecution` state in the controller
- add execution IDs and state transitions
- stop treating long-running commands as immediate failures

### Phase 2. Timeout Redesign
- replace the single command timeout with:
  - submission timeout
  - observation timeout
  - watchdog timeout
- remove the universal completion deadline for ordinary commands

### Phase 3. Active Command UI
- add active command card
- move live shell tail there
- keep transcript focused on completed or durable events

### Phase 4. Handoff as State
- model `F2` as `handoff_active`
- suppress cancellation noise during handoff
- resume and reconcile on detach

### Phase 5. Agent Check-Ins
- only for agent-owned commands
- rate-limited progress turns
- allow the agent to wait, summarize, revise, or request user input

### Phase 5a. Input-Wait Classification
- add conservative `awaiting_input` classification from shell-tail evidence
- surface that state in the active command card
- feed that state into agent check-ins so the agent can say "waiting for shell input" instead of "still running"
- do not classify a quiet long-running command as `lost` just because it is silent

### Phase 5b. Fullscreen and Alternate-Screen Detection
- add a pane-stream observer for active executions in `local_managed` mode
- detect fullscreen terminal apps from terminal behavior rather than command names
- introduce a stronger interactive/fullscreen state so commands like `btop`, `vim`, and wrapped aliases do not get reconciled from weak prompt heuristics
- keep command-name classification only as a hint, not as the correctness path
- use this state to decide when Shuttle should recommend or eventually auto-enter take-control mode

This should improve:
- fullscreen TUIs launched directly or through aliases/functions
- remote fullscreen apps inside SSH sessions
- correctness when there is little or no line-oriented shell output

### Phase 5c. Agent Recovery Snapshot
- when control flow goes ambiguous because the shell or a fullscreen app unexpectedly takes over, capture a richer recovery snapshot instead of relying on a tiny live tail
- snapshot inputs should include:
  - a larger terminal page dump, for example the last 100 to 200 visible lines
  - current execution state and confidence
  - shell context, if available
  - fullscreen and alternate-screen indicators
  - local-vs-remote capability hints
- use that recovery snapshot as an explicit agent check-in path for ambiguous states, not as the default on every command
- let the agent use it to answer:
  - is the shell waiting for input
  - is a fullscreen app active
  - did control likely return to a prompt
  - is tracking confidence low enough that Shuttle should mark the execution `lost`

Status:
- implemented in first form
- ambiguous execution states now feed a dedicated recovery snapshot into agent context
- agent check-ins are state-aware for `awaiting_input`, `interactive_fullscreen`, and `lost`
- remaining work is to make monitor-side classification more confident so fewer cases fall back to low-confidence recovery

### Phase 6. Runtime Durability
- align with the runtime-management design
- keep tracked executions recoverable across Shuttle restarts
- treat remote SSH sessions as resumable state where possible

## Manual Regression Checklist
Use this checklist after meaningful execution-monitor changes. It reflects the real bug history on this branch and should be kept current.

### 1. Local Long-Running Command
Run:

```bash
bash -lc 'for i in {1..10}; do echo "$i"; sleep 1; done'
```

Expect:
- active command card shows normal running state
- no false cancel
- command completes with visible output

### 2. Local Awaiting Input
Run:

```bash
bash -lc 'sleep 3; read -n 1 -s -r -p "Press any key to continue..." _; echo ready'
```

Expect:
- state changes from running to `awaiting_input`
- `F2` works
- `S` / `KEYS>` works
- after input, command completes cleanly

### 3. Local Fullscreen App
Run:

```bash
nano completed/ui-scratchpad.md
```

Expect:
- state becomes `interactive_fullscreen`
- no live tail rendering while fullscreen is active
- `S` / `KEYS>` works
- `F2` handoff works

### 4. Remote Awaiting Input
SSH to a remote host and run:

```bash
bash -lc 'sleep 3; read -n 1 -s -r -p "Press any key to continue..." _; echo ready'
```

Expect:
- remote prompt remains marked remote
- `awaiting_input` is detected
- `S` / `KEYS>` works without needing an `F2/F2` round trip
- no bogus local interrupt path appears

### 5. Remote Fullscreen App
SSH to a remote host and run:

```bash
nano test.txt
```

or

```bash
less prd.md
```

Expect:
- `interactive_fullscreen`
- no local kill affordance
- `S` / `KEYS>` works
- `F2` handoff works
- no false cancel while the app still owns the pane

### 6. Handoff Cancel / Reconcile
Local or remote, start a long-running command:

```bash
bash -lc 'for i in {1..30}; do echo "$i"; sleep 1; done'
```

Then:
- `F2`
- interrupt or exit from the shell side
- `F2`

Expect:
- active command clears
- no stale `handoff active`
- no duplicate “not confirmed local” spam
- no ghost “still running” message after the shell prompt returns

## Recommendation
Do not keep tuning the current synchronous command-wait loop.

The next meaningful work should be:
1. execution state machine
2. timeout redesign
3. active command UI
4. agent check-ins
5. pane-stream/fullscreen detection as the next execution-monitor slice

That will reduce the current whack-a-mole around:
- `context canceled`
- long command timeouts
- interactive prompts
- shell tail confusion
- fullscreen TUI false completions

## References
- Warp Full Terminal Use: https://docs.warp.dev/agent-platform/capabilities/full-terminal-use
- Warp Blocks as Context: https://docs.warp.dev/agent-platform/local-agents/agent-context/blocks-as-context
- Warp Task Lists: https://docs.warp.dev/agent-platform/capabilities/task-lists
- Warp Profiles & Permissions: https://docs.warp.dev/agent-platform/capabilities/agent-profiles-permissions
- Wave Terminal README: https://github.com/wavetermdev/waveterm
- Wave Durable Sessions: https://docs.waveterm.dev/durable-sessions
- Wave Connections / `wsh`: https://docs.waveterm.dev/connections
- Wave AI: https://docs.waveterm.dev/waveai
- Wave `wsh` overview: https://docs.waveterm.dev/wsh

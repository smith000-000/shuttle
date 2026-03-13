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

## Recommended Direction

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
- submit command to the same top pane
- expose live tail immediately
- allow explicit `F2` handoff into the exact same tmux pane
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

### 6. Treat Interactive Handoff as a Normal State Transition
When the user presses `F2`:
- cancel only the local wait operation
- do not treat that cancellation as a user-facing error
- transition the execution into `handoff_active`
- attach to the same tmux session and same top pane
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

#### Tier B. `text_observed`
Shuttle can see terminal output and prompt-like returns, but cannot rely on shell hooks or remote helpers.

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

#### Tier C. `hook_integrated`
Shuttle has shell-aware hooks inside the current shell session, for example prompt or command lifecycle hooks.

Available signals:
- explicit preexec/precmd-style command lifecycle events
- prompt/context changes
- terminal stream text

This is likely the best long-term answer for local shells, but it requires shell-specific integration.

#### Tier D. `remote_enhanced`
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

### Phase 6. Runtime Durability
- align with the runtime-management design
- keep tracked executions recoverable across Shuttle restarts
- treat remote SSH sessions as resumable state where possible

## Recommendation
Do not keep tuning the current synchronous command-wait loop.

The next meaningful work should be:
1. execution state machine
2. timeout redesign
3. active command UI
4. agent check-ins

That will reduce the current whack-a-mole around:
- `context canceled`
- long command timeouts
- interactive prompts
- shell tail confusion

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

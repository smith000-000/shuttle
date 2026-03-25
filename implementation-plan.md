# Shuttle Implementation Plan

## Purpose
Translate the product docs into an execution plan that is practical for a small team, explicit about debugging strategy, and sequenced to prove the risky parts before building polish.

## Current Status
As of March 11, 2026, the implementation state is:
- Milestone 0: complete
- Milestone 1: complete
- Milestone 2: complete
- Milestone 3: materially complete for `P0`, including transcript drill-down, scrolling, composer history, approvals, refine flow, and the compact two-pane TUI shell
- Milestone 4: complete for the mock-runtime path
- Milestone 5: in progress

Execution-monitor redesign / semantic shell hardening status on `semantic-shell-bootstrap`:
- implemented: first-class command monitor, local managed shell transport, `awaiting_input` detection, `interactive_fullscreen` detection, `lost` execution state, `F2` handoff/reconciliation, raw `KEYS>` terminal input, remote prompt-return reconciliation, and agent-driven `keys` proposals
- implemented: state-aware agent recovery guidance using active execution state plus a larger recovery snapshot
- implemented: first-pass local semantic shell integration using `OSC 133` / `OSC 7` shims plus semantic metadata in monitor/provider context, with best-effort raw-marker parsing from tmux capture
- implemented on `semantic-shell-bootstrap`: collector abstraction for semantic sources, spec-correct `ST`-terminated local marker emission, `osc_stream` via `tmux pipe-pane -O`, generation-scoped semantic stream files, conservative stale-generation pruning, and source precedence `osc_stream > osc_capture > state_file > heuristics`
- implemented on `semantic-shell-bootstrap`: subshell transition detection, local nested-shell semantic bootstrap, manual foreground attach, tracked-pane/session recovery, explicit `TrackedShellTarget`, and shell-only destructive tmux recovery
- implemented on `semantic-shell-bootstrap`: serial execution registry scaffolding, single-owner submission enforcement, serial auto-continue prompt hardening, hidden `proposal_kind:"answer"` state fix, and informational-only plan cards that no longer overwrite or outlive real completion state
- implemented on `semantic-shell-bootstrap`: hybrid shell execution model with a persistent user shell context, structured recent manual-shell commands/actions from shell history, owned tmux execution panes for agent-approved commands, owned-pane cleanup, and TUI interrupt/key routing that targets the active execution pane instead of always targeting the persistent user shell
- implemented on `semantic-shell-bootstrap`: prompt-validation hardening so stale scrollback cannot reconcile running commands as completed after `F2`, plus compact exit-aware transcript result rendering
- current focus should shift away from shell-tracking surgery and toward transcript/UI cleanup plus decomposition of the large controller and TUI files
- next: keep the serial tracking model stable, add more integration-style regression coverage opportunistically, and defer any parallel-command UI work to a later branch

Security hardening branch scope on `security-hardening-runtime`:
- audit runtime artifact placement, permissions, and retention now that `main` includes both execution-monitor and provider-onboarding work
- tighten trace/privacy policy boundaries, especially around sensitive traces and recovery snapshots
- review semantic shell integration trust boundaries for local vs remote/subshell sessions
- review provider secret handling end to end:
  - confirm when API keys are stored in OS keyring vs env-var references
  - verify no raw API keys are written to provider config files, traces, transcript entries, or debug logs
  - define behavior when OS keyring is unavailable or fails
  - policy decision: manual API key entry remains allowed for first-run usability
  - preferred storage order:
    - OS keyring when available
    - env-var reference when configured by the user
    - session-only secret use when the user does not want persistence
    - explicit plaintext local fallback only with user consent if no keyring backend is available
- implemented on this branch:
    - manual key entry remains allowed
    - keyring failure no longer blocks provider use for the current session
    - opt-in plaintext local fallback is available through `SHUTTLE_ALLOW_PLAINTEXT_PROVIDER_SECRETS` / `--allow-plaintext-provider-secrets`
    - provider UI now labels `OS keyring`, `session only`, and `local file (less secure)` auth sources
    - provider config and selected-provider reads now use no-follow file access instead of plain `os.ReadFile`
    - semantic shell state reads now use no-follow file access as well
    - provider startup logging now records only coarse auth-source labels such as `env_ref`, `os_keyring`, `local_file`, or `session_only` instead of raw env-var names
    - safe trace mode now redacts provider auth metadata fields like `api_key_env` and `api_key_ref`
    - the TUI now shows a blocking startup warning when the active provider is using the plaintext local secret fallback
    - Codex CLI model selection now uses the OpenAI models endpoint as a suggestions source when available, while keeping manual free-text model entry and explicit caveats that the live Codex CLI picker may differ
  - no local encrypted fallback in the first pass; without a trustworthy local key source it adds complexity more than security
  - surface storage status in onboarding/settings so the user can see whether a provider secret is coming from keyring, env, session-only entry, or less-secure local storage
  - close the current onboarding gap: onboarding/settings must be able to ingest a newly entered provider API key and route it through the same storage policy instead of assuming the key already exists in env or persistent config
  - tighten redaction around provider/auth metadata in traces and onboarding flows
- document any remaining privacy/security tradeoffs explicitly before the next merge back to `main`

Milestone 5 currently includes:
- provider profile and resolver scaffolding
- backend/auth abstraction layers
- provider factory wiring
- provider registration is still static and hand-wired across profile resolution, factory construction, onboarding detection, settings ordering, and model listing; a future provider registry should make first-class providers self-registering instead of requiring edits in multiple switch statements
- one real `responses_http` path for the standard OpenAI API endpoint with API-key auth
- `httptest` coverage for the OpenAI-compatible adapter
- execution monitor redesign for long-running and interactive shell commands
- raw terminal input flow for active prompts and fullscreen apps
- first-class agent `keys` proposals for interactive recovery

Milestone 5 still needs:
- OpenRouter verification and preset-specific tests
- onboarding and health checks
- saved provider profiles
- Codex CLI delegation path
- provider switching UI
- release-grade runtime management for socket/session lifecycle and crash recovery
- a real patch-application path so proposed diffs can become actual workspace changes
- guardrails that prevent the agent from claiming proposed files exist before the patch is applied
- stronger monitor-side confidence for ambiguous remote/container takeovers beyond the now-stable local serial model
- integration-style tests for tracked-command recovery flows, not just per-layer unit tests
- pane-stream/fullscreen detection beyond tmux alternate-screen heuristics so aliases, wrappers, and remote fullscreen apps can be recognized from terminal behavior instead of command-name lists alone
- richer state-aware agent recovery actions for ambiguous shell takeovers, including deciding when to propose raw terminal input versus simple recovery guidance
- broader semantic shell integration and subshell/bootstrap support using signals such as `OSC 133` and `OSC 7`
- any richer bootstrap or injected helper mode should come later, after the standards-based marker path exists
- before touching richer subshell/bootstrap behavior for `ssh`, `docker exec -it`, or nested shells, run the manual regression checklist in [shell-execution-strategy.md](shell-execution-strategy.md) to avoid regressing the current moderately functional context-transition path
- move runtime state, staged shell scripts, semantic state files, shell history, and logs out of the repo-local `.shuttle/` directory into a user-private runtime directory such as XDG state/runtime space, with `0700` directory permissions and no-follow/exclusive writes for staged files
- do not add filesystem encryption to the runtime state directory in the first pass; prefer private location, strict permissions, and minimal retention over ad hoc app-level encryption
- split tracing into at least two levels:
  - safe/debug trace that avoids recording raw terminal contents, raw key input, provider bodies, and staged shell commands
  - explicit sensitive trace that is opt-in only
- when sensitive trace is requested, block normal launch behind a one-time explicit consent step that states trace may capture commands, shell output, key input, prompts, and provider payloads
- rework semantic shell state serialization away from ad hoc tab-delimited text to a robust encoded format such as JSON so `cwd` and other fields cannot corrupt parsing
- keep recovery snapshots as a supported feature, but add policy controls so future approval/sandbox modes can reduce or disable automatic terminal snapshot upload for untrusted providers or higher-sensitivity sessions
- make the boundary explicit in docs and UX: trace mode governs local logging only, not the provider context needed for normal agent reasoning
- the next time `internal/tui/model.go` is touched for execution-control behavior, treat it as a refactor slice:
  - extract composer/input-mode routing from command-execution control
  - move fullscreen/raw-key submission logic behind a narrower interface
  - reduce duplicated busy/lock/handoff gating in the TUI state machine
  - keep plan cards passive/informational unless the product explicitly introduces a dedicated interactive checklist workflow
- add a backlog item to break up monolithic orchestration files before they become unmaintainable:
  - split `internal/tui/model.go` into smaller execution-control, composer/input, transcript/rendering, and handoff/fullscreen modules
  - split `internal/controller/controller.go` into execution lifecycle, agent-turn normalization, plan management, and tracked-shell ownership/recovery helpers
  - prefer narrow types/modules and integration tests over one more round of point fixes in the monoliths

### Semantic Shell And Subshell Bootstrap Plan

This item is now concrete enough to split into separate implementation phases.

Research-backed constraints:
- tmux 3.4 already understands semantic prompt markers well enough to power `next-prompt` / `previous-prompt`, and `-o` output jumping depends on `OSC 133;C`.
- tmux `capture-pane -e` can expose escape sequences, but it is still not a reliable sole source of truth for lifecycle metadata. Shuttle should treat raw OSC capture as opportunistic, not guaranteed.
- Warp's public subshell design is two-layered:
  - detect a compatible transition such as `bash`, `ssh`, or `docker exec`
  - wait for an RC-file readiness signal, then run a setup script
- Wave's public implementation is also two-layered:
  - standards-based `OSC 7` for cwd
  - richer shell context tracking through shell-specific hooks and optional `wsh` bootstrap on remote connections
- Conclusion: Shuttle should keep the standards path and the bootstrap path separate.

Design rules:
- standards first:
  - `OSC 133` for prompt and command lifecycle
  - `OSC 7` for cwd
- bootstrap second:
  - only to extend semantic coverage into nested shells and remote/container sessions
  - never required for correctness
  - silent fallback to existing heuristic monitoring
- do not adopt a proprietary helper protocol before the standards path is strong and well-tested

Phase 1: make local semantic markers spec-correct and durable
- tighten the current local `bash` / `zsh` shell hooks so Shuttle emits a proper semantic lifecycle rather than the current partial approximation
- target lifecycle:
  - `A` prompt start
  - `B` prompt end / command line start
  - `C` command execution start
  - `D;<exit>` command finished
- keep `OSC 7` for cwd
- preserve shell-specific behavior:
  - `bash` will still need `preexec` / `precmd` support, likely via `bash-preexec`
  - `zsh` should continue using hooks such as `precmd`, `preexec`, and `chpwd`
- define source precedence clearly:
  - `osc_stream`
  - `osc_capture`
  - `state_file`
  - heuristic prompt/tail inference
- success criteria:
  - local prompt return, exit code, and cwd updates no longer rely on heuristic parsing when shell integration is active
  - semantic metadata is explicit in monitor/controller/provider state

Phase 2: improve semantic consumption beyond best-effort capture
- keep the sidecar semantic state file as a compatibility fallback
- completed spike result:
  - tmux `pipe-pane -O` preserves raw `OSC 133` / `OSC 7` bytes
  - that makes `osc_stream` a viable transport, not just a theory
  - but a cumulative pane-output stream cannot be parsed with the same "scan the whole buffer and keep the last marker" logic used for snapshots
  - later prompt markers overwrite earlier command markers unless Shuttle reduces the stream incrementally
- preferred options in order:
  - `osc_stream` backed by tmux pane-output piping and an incremental event reducer
  - best-effort `capture-pane -e` parsing where it works
  - state-file fallback where tmux drops or normalizes control data
- add a semantic collector abstraction so the observer no longer knows about individual transport details
- record:
  - semantic source
  - semantic event timestamp
  - confidence tier
- next concrete implementation slice:
  - add `semanticSourceStream`
  - implement a reducer that tracks:
    - prompt start
    - command-line start
    - command execution start
    - command finish with exit code
    - cwd updates
  - make source precedence explicit and observable:
    - `osc_stream`
    - `osc_capture`
    - `state_file`
    - heuristics
- success criteria:
  - Shuttle can explain why a lifecycle decision came from semantic data versus heuristics
  - `osc_stream` becomes the preferred primary semantic source on supported local panes
  - `osc_capture` remains a snapshot fallback, not the hoped-for end state

Phase 3: subshell transition detection
- add a first-class transition detector for commands likely to hand control to a nested interactive shell
- initial targets:
  - nested local `bash` / `zsh`
  - `ssh`
  - `docker exec -it`
  - `kubectl exec -it`
  - `sudo -i`
  - `sudo -s`
- do not rely on a fixed command-name allowlist as the only mechanism
- use multiple signals:
  - submitted command text when available
  - prompt/context transition
  - pane foreground command
  - later, semantic readiness signal from the child shell
- classify transitions as:
  - likely local nested shell
  - likely remote shell
  - likely container/exec shell
  - unknown interactive transition
- success criteria:
  - Shuttle can tell when it should attempt semantic bootstrap versus when it should remain heuristic-only

Phase 4: conservative bootstrap for nested shells and remote sessions
- once a supported transition settles and a new prompt is visible, inject a per-session semantic integration snippet into the child shell
- bootstrap requirements:
  - only after prompt settlement
  - idempotent inside the target shell session
  - per-session only by default
  - no persistent RC-file edits without explicit user action
  - no helper binary requirement in the first pass
- implementation preference:
  - inline shell snippet or one-shot staged script plus source command
  - export a session marker like `SHUTTLE_SEMANTIC_SHELL_V1=1` inside the child shell
- remote/container bootstrap should be treated as best-effort:
  - if injection fails, Shuttle stays on heuristic monitoring
  - no partial failure should wedge the shell
- success criteria:
  - nested local shells gain semantic tracking after prompt settlement
  - remote `ssh` and container shells can opt into the same lifecycle semantics without persistent machine changes

Phase 5: optional persistent bootstrap UX
- only after per-session bootstrap is stable
- offer an explicit user-approved install snippet for shells where persistent setup is desired
- this is the closest Shuttle analogue to Warp's auto-warpify RC-file snippet
- keep it clearly separate from the standards implementation:
  - standards describe the protocol
  - persistent bootstrap is only a convenience install path
- success criteria:
  - users can choose convenience without Shuttle silently mutating remote or nested shell startup files

Out-of-scope for this branch unless forced by implementation:
- a proprietary helper protocol like Wave's custom OSC 16162 channel
- a required remote helper binary
- running background helper commands during remote idle time
- shell editing/completion features beyond execution tracking and recovery

Validation plan:
- unit tests:
  - spec-correct `OSC 133` parsing and precedence
  - stream reducer correctness over cumulative pane output
  - bootstrap decision engine for local vs remote vs unsupported contexts
  - idempotent child-shell bootstrap decisions
- integration tests:
  - local `bash` nested inside local `zsh`
  - local `zsh` nested inside local `bash`
  - semantic prompt return after `F2 -> Ctrl+C -> F2`
  - explicit `pipe-pane -O` stream parsing when tmux preserves markers
  - `capture-pane -e` fallback parsing when stream mode is unavailable
- manual tests:
  - existing remote/subshell regression checklist in [shell-execution-strategy.md](shell-execution-strategy.md)
  - nested local shell followed by tracked command
  - `ssh` followed by tracked command, awaiting-input, and fullscreen app
  - `docker exec -it` or `kubectl exec -it` where available
  - bootstrap failure path where Shuttle must degrade back to heuristic monitoring without user-visible corruption

## Guiding Decisions
- Build `P0` only first. That means Epics 1 through 4 in [requirements-mvp.md](requirements-mvp.md).
- Prove the shell substrate before investing in the full TUI.
- Keep the first controller implementation simple and observable rather than abstract and clever.
- Use a mock provider until the shell loop and approvals are stable.
- Treat tmux and shell integration as the primary engineering risk, not the model backend.

---

# 1. Delivery Strategy

## 1.1 What We Are Actually Trying to Prove
The product is real when Shuttle can:
1. create or attach to a two-pane tmux workspace
2. observe the top shell pane
3. inject a command into that exact pane
4. track the command start, output, end, and exit code
5. do that even after the user SSHs from the top pane into a remote shell

Everything else is downstream of that proof.

## 1.2 Scope Lock for First Pass
Included:
- workspace creation and pane discovery
- command injection into the top pane
- rolling shell observation
- sentinel parsing
- a minimal transcript-driven TUI
- Agent mode, Shell mode, and approval flow
- a mock provider, then a real provider

Deferred until the shell loop is stable:
- full persistence
- rich settings UI
- plugin and extension work
- worktree inspection features
- deeper project-aware context gathering

---

# 2. Milestones

## Milestone 0. Repo Bootstrap

### Goal
Create a Go repository skeleton and local dev workflow that is easy to run and debug.

### Deliverables
- `go.mod`
- `cmd/shuttle/main.go`
- basic config loading
- structured logging setup
- `Makefile` or equivalent task runner targets
- a tmux server naming convention for local dev, such as `shuttle-dev`

### Exit Criteria
- `go test ./...` runs cleanly
- `go run ./cmd/shuttle` starts a no-op stub
- logs can be written to a predictable local file for debugging

## Milestone 1. Workspace and Pane Control

### Goal
Own the tmux substrate reliably before building observation or agent logic.

### Deliverables
- create or attach to a Shuttle workspace
- create or discover top and bottom panes
- identify pane IDs and relevant session metadata
- set and preserve a default layout
- inject plain shell text into the top pane

### Exit Criteria
- Shuttle can create the two-pane layout repeatedly without manual cleanup
- the controller can rediscover pane IDs after restart
- a manual command injected from the controller appears in the top pane

Detailed plan: [milestone-1-workspace.md](milestone-1-workspace.md)

## Milestone 2. Observation and Sentinel Tracking

### Goal
Prove that Shuttle can observe and track controller-driven commands end to end.

### Deliverables
- rolling capture of top-pane output
- on-demand pane snapshot capture
- sentinel begin and end marker injection
- command lifecycle record with command ID and exit status
- structured event emission to logs or stdout

### Exit Criteria
- an injected `pwd` can be tracked from submission to exit code
- output is attached to the correct command record
- the flow works in a normal local shell and through a simple SSH session

## Milestone 3. Minimal TUI Shell

### Goal
Put a thin Bubble Tea interface over the working substrate without adding feature sprawl.

### Deliverables
- transcript panel
- composer
- mode indicator
- key hints
- approval card rendering
- shell event rendering

### Exit Criteria
- a user can type in Agent mode and Shell mode
- shell events render as structured transcript entries
- approval requests can be accepted, rejected, or refined

### Deferred Follow-Up
- Add a dedicated "full tmux view" action for temporarily handing the terminal over to the live tmux session when the user needs to interact with fullscreen TUIs such as `btop`, `vim`, or `less`.
- That flow should likely zoom or otherwise prioritize the top pane, then return cleanly to the Bubble Tea interface after detach without leaving the session in a bad state.
- Do not treat fullscreen top-pane TUIs as part of the normal tracked-command workflow until that escape hatch exists and is reliable.
- Add app-owned composer history navigation with Up and Down arrows, scoped separately for Agent mode and Shell mode.
- Investigate a shell-aware submission path that avoids polluting the user's normal shell history where possible, with explicit degraded behavior when the active shell or SSH target cannot guarantee that.

## Milestone 4. Agent Workflow with Mock Provider

### Goal
Prove the interaction loop without mixing in real API complexity.

### Deliverables
- controller state machine for tasks
- mock provider that returns canned plans, commands, and approval requests
- approval flow wired to actual command injection
- refine flow returning to a seeded composer

### Exit Criteria
- a user can ask for help, receive a mock plan, approve it, and see a real shell command execute
- the task loop continues until completion or cancellation

Detailed design note: [agent-runtime-design.md](agent-runtime-design.md)

## Milestone 5. Real Provider Integration

### Goal
Replace the mock provider with a real provider while preserving the same controller contract.

### Deliverables
- provider interface
- one real provider implementation
- profile-based model selection
- request and response normalization

### Current State
Implemented now:
- provider resolution and factory wiring in the app startup path
- one real OpenAI-compatible Responses adapter using API-key auth
- normalized mapping from structured provider output into Shuttle `AgentResponse`
- agent/runtime support for `proposal_kind = "keys"` so the model can request raw terminal input for active prompts and fullscreen apps

Next:
- validate and harden the OpenRouter preset
- add provider detection and health checks
- add profile persistence
- add the Codex CLI bridge
- add runtime-management work so release builds do not expose raw tmux socket/session details

### Exit Criteria
- the same loop used in Milestone 4 works with a real provider
- provider failures are surfaced as structured errors rather than crashing the app

### Backlog Note
- Patch proposals currently stop at proposal generation. Shuttle still needs an explicit apply/approve/apply-result flow for diffs and file creation.
- Until that exists, the controller and provider prompts should treat proposed patches as inert and should not let the agent narrate them as already-created files.
- The next execution-monitor slice should classify `awaiting_input` conservatively from shell-tail evidence and reserve `lost` for genuinely low-confidence tracking failures rather than silent long-running jobs.
- After the current `awaiting_input` work, the next execution-monitor slice should add pane-stream/fullscreen detection in the same redesign track rather than as a separate feature branch.
- That slice should prefer terminal behavior over command names so aliases, functions, and wrapped fullscreen apps do not regress back into weak prompt-return heuristics.
- After fullscreen detection, add an explicit recovery-snapshot path so ambiguous shell takeovers can be fed back into the agent using a larger terminal page dump plus execution confidence metadata instead of only a tiny shell tail.
- Recovery snapshots and state-aware agent check-ins are now implemented in a first pass; the next step is to improve monitor-side confidence so more ambiguous states are classified correctly before they reach the agent.
- Agent-driven raw terminal input is now implemented in a first pass; the next step is to harden when the agent should use `keys` proposals versus plain recovery guidance, and to keep dangerous key sequences reviewable.
- Security remediation follow-up:
  - stop using repo-local `.shuttle/` as the default state root
  - add no-follow/exclusive writes for staged scripts and semantic state artifacts
  - add safe vs sensitive trace modes plus explicit sensitive-trace consent on launch
  - migrate semantic shell sidecar serialization to a robust encoded format
  - keep recovery snapshots, but make their upload policy configurable later
  - refactor `internal/tui/model.go` before adding more execution-control behaviors

Framework and ACP guidance: [agent-runtime-design.md](agent-runtime-design.md)
Detailed provider decomposition: [provider-integration-design.md](provider-integration-design.md)
Execution plan: [provider-integration-plan.md](provider-integration-plan.md)
Runtime lifecycle design: [runtime-management-design.md](runtime-management-design.md)
Shell and agent execution strategy: [shell-execution-strategy.md](shell-execution-strategy.md)
Deferred patch/apply research: [patch-apply-research.md](patch-apply-research.md)

### Execution Regression Note
- After meaningful execution-monitor changes, run the manual regression checklist in [shell-execution-strategy.md](shell-execution-strategy.md) before treating the branch as stable.

---

# 3. Proposed Repository Layout

```text
cmd/
  shuttle/
    main.go
internal/
  app/
    app.go
  config/
    config.go
  controller/
    controller.go
    state.go
    events.go
    approvals.go
  tmux/
    client.go
    workspace.go
    panes.go
    inject.go
    capture.go
  shell/
    observer.go
    buffer.go
  protocol/
    sentinel.go
    parser.go
  tui/
    model.go
    update.go
    view.go
    transcript.go
    composer.go
  provider/
    provider.go
    mock.go
    openai.go
  logging/
    logging.go
  persistence/
    store.go
integration/
  tmux_workspace_test.go
  tmux_observer_test.go
```

## Layout Rationale
- `tmux` owns tmux command execution and pane operations only.
- `shell` owns raw observation buffers and pane-content capture.
- `protocol` owns sentinel formatting and parsing.
- `controller` owns orchestration and state transitions.
- `tui` stays presentation-focused.
- `provider` stays behind a narrow interface so it can be mocked early.

---

# 4. Package Boundaries

## 4.1 `internal/tmux`
Responsibilities:
- start or attach to a tmux session
- discover pane IDs
- resize panes
- send keys or commands into a pane
- capture pane content

Rules:
- no agent logic
- no TUI state
- no provider awareness

## 4.2 `internal/protocol`
Responsibilities:
- generate begin and end markers
- parse observed output for markers
- return structured lifecycle events

Rules:
- pure logic where possible
- heavy unit test coverage

## 4.3 `internal/shell`
Responsibilities:
- maintain a rolling output buffer
- normalize pane capture data
- publish raw observation chunks upward

Rules:
- no business decisions about approvals or tasks

## 4.4 `internal/controller`
Responsibilities:
- coordinate tmux, shell observation, protocol parsing, provider calls, and TUI-facing events
- own task state and approval state
- convert raw shell reality into structured events

Rules:
- this is the state machine layer
- keep concurrency simple until the substrate is proven

## 4.5 `internal/tui`
Responsibilities:
- render transcript and composer state
- display mode and key hints
- render approval cards
- emit user intents to the controller

Rules:
- not the source of truth for command lifecycle state

## 4.6 `internal/provider`
Responsibilities:
- expose a stable interface for prompt-in and response-out interactions
- support a mock provider early
- support a real provider later

Rules:
- provider output should normalize into controller-friendly message types

---

# 5. Debugging Strategy

## 5.1 Make Shell Events Visible
Every important transition should be loggable:
- workspace created or attached
- pane IDs discovered
- command injected
- begin marker observed
- stdout chunk received
- end marker observed
- exit code parsed
- task state changed

If a bug happens, the log should let us answer whether it is:
- tmux control failure
- observation failure
- parser failure
- controller state failure
- TUI rendering failure

## 5.2 Keep the First Controller Mostly Synchronous
Avoid clever goroutine fan-out early. Start with:
- one main controller event loop
- one observation feed
- explicit event structs

The goal is debuggability, not peak throughput.

## 5.3 Separate Unit Tests from Real Integration Tests
Unit-test heavily:
- sentinel formatting
- sentinel parsing
- controller state transitions
- approval flow state handling

Integration-test with real tmux:
- workspace creation
- pane rediscovery
- command injection
- output capture
- command lifecycle tracking

## 5.4 Use an Isolated tmux Server for Tests
Do not run tests against the user's default tmux server.

Use a dedicated server name or socket, for example:
- `tmux -L shuttle-test`

That prevents tests from touching real sessions and makes failures reproducible.

---

# 6. Developer Workflow

## 6.1 Default Commands
The exact targets can be adjusted, but the workflow should support:
- `go test ./...`
- `go run ./cmd/shuttle`
- `go test ./integration/...`

Optional task aliases:
- `make test`
- `make run`
- `make test-integration`

## 6.2 Logging
During early development, write logs to a plain local file and also allow stderr logging when running interactively.

Do not hide the logs behind a complex observability stack.

## 6.3 Manual Smoke Tests
Keep a short repeatable smoke-test script for:
1. launch Shuttle
2. verify two panes exist
3. inject `pwd`
4. inject a failing command
5. verify exit code capture
6. SSH from the top pane and repeat

---

# 7. Risk Management

## 7.1 Highest-Risk Areas
- sentinel reliability through SSH
- top-pane observation in noisy shells
- keeping command output attached to the right lifecycle record
- not overcomplicating the controller too early

## 7.2 How We Reduce Risk
- prove tmux control before TUI work
- prove protocol parsing before provider work
- use a mock provider before real API integration
- keep the first end-to-end flow narrow and testable

---

# 8. Recommended Next Step

Start with Milestone 0 and Milestone 1 only.

Do not start Bubble Tea, provider integration, persistence, or advanced keybinding work until:
- the workspace can be created reliably
- pane IDs can be rediscovered
- the controller can inject visible commands into the top pane

If those pieces are unstable, everything above them will be misleadingly expensive to debug.

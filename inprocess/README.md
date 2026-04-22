# Shuttle

Shuttle is a tmux-backed AI terminal assistant.

It runs a persistent real shell in the top tmux pane and a Bubble Tea TUI in the bottom pane. Agent-approved commands can also run in owned tmux execution panes for local work. The persistent shell remains the continuity surface for shell-command input, cwd, recent manual shell activity, and remote SSH continuity. `F2` always hands control to that persistent tracked shell. `F3` is only advertised when Shuttle has a distinct owned execution pane live, and then it targets that command pane.

## Status

This repo is still pre-release.

What is working now:
- tmux workspace bootstrap and rediscovery
- shell command injection into the real top pane
- tracked command observation with execution states
- persistent user-shell context for cwd, recent shell output, and recent manual shell actions
- owned tmux execution panes for agent-approved commands
- controller-owned execution overview and help-card copy that keep `F2` bound to the persistent shell while only surfacing `F3` when a distinct owned execution pane is live
- remote tracked-shell execution stays in the tracked shell instead of spilling into a local owned pane
- Agent and Shell modes in the TUI
- approval and refine flow
- local and remote handoff with `F2`
- `KEYS>` mode for sending raw terminal input
- bounded agent check-ins for interactive/fullscreen waits, with explicit `Ctrl+G` continuation cards after Shuttle pauses automatic retries or needs confirmation to continue from the latest command result
- partial semantic shell integration for local shells
- serial agentic command loops with one proposal at a time and auto-continue after results
- active checklist reconciliation on continuation turns, so plan cards can reflect explicit agent status updates instead of only command-completion guesses
- fresh user prompts now supersede stale active checklists unless the user is explicitly asking to continue or resume the current plan
- refinement notes can now explicitly abandon or hand off the current checklist so stale plan cards do not reassert after the user redirects a proposed next step
- fresh user prompts and continuation turns now explicitly treat rerun/retry requests and shell-target shifts as reasons to distrust earlier completion and reassess the work in the current shell context
- agent prompt decoration now explicitly classifies requests by execution surface and treats controller session state, not matching path strings, as the source of truth for shell identity and target context
- first-class shell-context inspection support so the model can refresh authoritative user@host/cwd state instead of guessing from stale prompt text
- inspect-context and provider turn context now include cwd source/confidence metadata so prompt-derived remote directories like `~` are treated as approximate while probe-confirmed directories are treated as authoritative
- ordinary agent turns refresh tracked-shell identity and manual shell history without blindly reusing whatever old scrollback is still visible in the top pane as fresh command output
- Shuttle now uses a single sanitized plain-text capture path for both controller logic and rendered command summaries/tails so shell tracking and cwd reconciliation stay aligned
- tracked command capture now keeps cwd/prompt reconciliation stable across directory changes by joining wrapped pane lines, bounding result extraction to Shuttle markers when available, and stripping leaked Shuttle shell-plumbing fragments from transcript output
- native unified-diff patch proposals with explicit apply/reject/ask-agent flow
- controller-synthesized patch proposals for common single-file text edits, so the model can express insert/replace intent without hand-authoring unified hunks
- target-aware patch application for both the local workspace and the active tracked remote shell
- local file creation and edits through native patch application
- remote tracked-shell file edits through the same patch UX, preferring native remote patches over ad hoc shell rewrites and using staged remote payloads with transport selection `git`, then `python3`, then verified shell fallback
- foreground attach and handoff reconciliation for manually started shell commands
- tracked `ssh` and similar transport transitions now surface password/confirmation waits as `awaiting_input`, distinguish the outer transport from the inner remote command, and reconcile `F2` return from observed shell state even when prompt text is incomplete
- `KEYS>` mode now exits automatically when the active fullscreen or awaiting-input execution settles, so the composer returns to the underlying `AGENT` or `SHELL` mode instead of staying in raw-key input mode
- real OpenAI Responses API path with API-key auth
- provider onboarding and settings UI with:
  - one shared settings flow behind `F10`, `/onboard`, `/provider`, and `/model`
  - `F10` runtime selection with persisted `builtin`, `auto`, `codex_sdk`, and `codex_app_server` choices, inline command-path editing, and runtime health/resolution preview that validates on selection changes or when you leave the command field
  - `F10` shell settings with separate persistent-shell and execution-shell startup profiles:
    - `inherit`: leave shell startup untouched
    - `managed-prompt`: keep the shell family and usually keep user rc/env, but force a simple deterministic prompt with right prompt disabled and preload Shuttle's local semantic-shell hooks for those Shuttle-created PTYs
    - `managed-minimal`: use Shuttle-owned startup files for the same shell family for a more controlled bootstrap path, including the same startup-owned prompt and semantic-shell setup
    - per-target toggles for shell family (`auto`, `zsh`, `bash`), sourcing user rc, and inheriting the launch environment
  - `/onboard` and `/provider` jumping straight to `Configure Providers`
  - `/model` jumping to the current provider detail with the model field focused
  - provider detail editing with the discovered model list on the same screen
  - provider detail `Thinking` controls for OpenAI, OpenRouter, Anthropic, and Ollama
  - conditional `Reasoning Effort` controls for OpenAI and OpenRouter when `Thinking` is enabled
  - clearer auth-source and provider-test feedback during provider/settings flows
  - session-local approval-mode selection
  - `F7` provider health/auth test from provider details
  - `F8` save-and-activate from provider details
- saved provider profiles and startup reloading
- provider secret handling with:
  - OS keyring persistence
  - session-only fallback
  - explicit plaintext local fallback with warning
- safer runtime state and trace defaults
- Codex CLI login-based provider support
- Codex CLI model suggestions sourced from the OpenAI models catalog when available, with free-text entry still allowed
- task-context controls for `/new` and `/compact`
- session-local `/approvals` control with `confirm`, bounded `auto`, and explicit-confirmation `dangerous` modes
- lower-right model status showing approximate live context-window usage
- initial release packaging via versioned platform archives (`.tar.gz` for Linux/macOS, `.zip` for Windows), checksum output, and `--version` build metadata

What is still in progress:
- broader semantic shell integration (`OSC 133` / `OSC 7`) consumption and subshell/bootstrap support
- deeper shell-transition verification for more interactive and nested remote cases beyond the current prompt-plus-probe state machine
- broader provider onboarding polish for additional backends and more proactive pre-selection health probes
- provider registry/plugin architecture instead of static first-class wiring
- richer managed-shell bootstrap and broader shell-family coverage beyond the current `bash`/`zsh` deterministic prompt profiles
- transcript/UI cleanup and continued TUI/controller decomposition
- stronger bounded-command guidance for event-stream listeners such as `xinput test`, `tail -f`, and similar monitors
- multi-card or parallel execution UI
- package-manager distribution and other post-archive release UX

Tracking notes for current work-in-progress:
- command/result display now deliberately follows the same sanitized capture used for shell reconciliation instead of a separate ANSI-preserving render path
- if shell styling is important for a debugging session, rely on the real shell pane rather than the Shuttle transcript/result preview
- this behavior is intentionally tracked in GitHub while we continue to tighten start/end marker-driven completion and prompt-return fallback paths

## Requirements

To run Shuttle from a release:
- `tmux` installed and available in `PATH`
- a normal terminal environment capable of running Bubble Tea

To build Shuttle from source:
- Go `1.25.0`
- `tmux` installed and available in `PATH`
- a normal terminal environment capable of running Bubble Tea

Optional:
- `OPENAI_API_KEY` for the real OpenAI provider
- `OPENROUTER_API_KEY` for the OpenRouter preset

## Quick Start

Run Shuttle from a release:

1. Install the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/smith000-000/shuttle/main/scripts/install-release.sh | bash
```

Windows:
- download the matching `shuttle_<version>_windows_<arch>.zip` asset from Releases
- extract `shuttle.exe`
- add the extracted directory to `PATH`, or run `shuttle.exe --tui` from that directory

2. Confirm the installed build:

```bash
shuttle --version
```

3. Launch Shuttle from the project you want to work in:

```bash
shuttle --tui
```

Build and run Shuttle from source:

1. Create a local env file:

```bash
cp env.sh.sample env.sh
```

2. Edit `env.sh` and set the provider you want.

For OpenAI:

```bash
export SHUTTLE_PROVIDER="openai"
export SHUTTLE_AUTH="api_key"
export SHUTTLE_MODEL="gpt-5-nano-2025-08-07"
export OPENAI_API_KEY="..."
```

3. Launch Shuttle:

```bash
./launch.sh
```

By default this runs:

```bash
go run ./cmd/shuttle --tui
```

## Environment

Use [env.sh.sample](env.sh.sample) as the template for local configuration.

Important variables:
- `SHUTTLE_PROVIDER`: `mock`, `openai`, `openrouter`, or `custom`
- `SHUTTLE_AUTH`: `auto`, `api_key`, or `none`
- `SHUTTLE_MODEL`: model name for the active provider
- `SHUTTLE_BASE_URL`: optional custom base URL
- `OPENAI_API_KEY`: OpenAI API key
- `OPENROUTER_API_KEY`: OpenRouter API key
- `SHUTTLE_SESSION`: optional tmux session name override
- `SHUTTLE_TMUX_SOCKET`: optional tmux socket/server name override
- `SHUTTLE_STATE_DIR`: optional persistent state directory for logs and shell history
- `SHUTTLE_RUNTIME_DIR`: optional private runtime directory for staged shell scripts, semantic shell state, and other ephemeral runtime artifacts
- `SHUTTLE_ALLOW_PLAINTEXT_PROVIDER_SECRETS`: allow an explicit less-secure local file fallback if OS keyring storage is unavailable
- `SHUTTLE_TRACE`: `off`, `safe`, or `sensitive`
- `SHUTTLE_TRACE_CONSENT`: must be true or passed as `--trace-consent` when using sensitive trace; Shuttle rejects sensitive trace at config-parse time until consent is explicit
- `SHUTTLE_RUNTIME`: coding runtime selection: `builtin`, `auto`, `pi`, `codex_sdk`, or `codex_app_server`
- `SHUTTLE_RUNTIME_COMMAND`: optional explicit runtime command path override
- `SHUTTLE_TUI_DISABLE`: optional comma-separated TUI diagnostic disable list; also available as `--tui-disable`

`launch.sh` loads `./env.sh` if present, otherwise it falls back to `./env.sh.sample`.

## TUI Lag Isolation

Shuttle now supports targeted TUI disable switches so you can isolate composer lag without editing code:

```bash
./launch.sh --tui-disable shell-completion
```

Or with environment config:

```bash
export SHUTTLE_TUI_DISABLE="shell-completion,completion-ghost"
./launch.sh
```

Available disable values:
- `shell-completion`
- `history-completion`
- `slash-completion`
- `completion-ghost`
- `footer-hints`
- `status-line`
- `shell-context`
- `approval-label`
- `model-status`
- `context-usage`
- `busy-indicator`
- `action-card`
- `plan-card`
- `execution-card`
- `shell-tail`
- `transcript`
- `transcript-chrome`
- `mouse`
- `busy-tick`
- `execution-polling`
- `shell-context-polling`

For repeatable manual sweeps, use the helper script:

```bash
scripts/diagnose-tui-lag.sh list
scripts/diagnose-tui-lag.sh shell-completion
scripts/diagnose-tui-lag.sh chrome-off --tui
```

Release-oriented tmux defaults:
- Shuttle now derives a stable workspace ID from the absolute project path
- by default it uses a managed tmux socket at `$XDG_RUNTIME_DIR/shuttle/tmux.sock` or the XDG state fallback
- by default it uses a derived tmux session name like `shuttle_<workspace-id>`
- `--socket`, `--session`, `SHUTTLE_TMUX_SOCKET`, and `SHUTTLE_SESSION` still work as explicit dev/debug overrides

## Build and Test

Run the full test suite:

```bash
make test
```

Or directly:

```bash
go test ./...
```

Integration-only tests:

```bash
make test-integration
```

Interactive tmux harness tests under `./integration/harness` are currently
opt-in only while UX automation is paused. Run them explicitly with
`SHUTTLE_RUN_INTERACTIVE_HARNESS=1`.

Run from source without the launcher:

```bash
go run ./cmd/shuttle --tui
```

Build a local binary:

```bash
make build
./bin/shuttle --version
```

Create release archives:

```bash
make package VERSION=v0.1.0
```

By default `make package` builds `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64` archives under `./dist/` and writes `SHA256SUMS`. Linux/macOS assets are `.tar.gz`; Windows assets are `.zip`.

Install the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/smith000-000/shuttle/main/scripts/install-release.sh | bash
```

Install a specific release or custom location:

```bash
curl -fsSL https://raw.githubusercontent.com/smith000-000/shuttle/main/scripts/install-release.sh | \
  VERSION=v0.1.0 INSTALL_DIR="$HOME/.local/bin" bash
```

GitHub release packaging:
- pushing a `v*` tag runs the release workflow and publishes the packaged archives plus `SHA256SUMS`
- `Actions -> Release` also supports manual packaging via `workflow_dispatch`, with an optional publish toggle for dry runs
- `make test-packaging` smoke-tests Linux installer flow and verifies that the Windows release zip contains `shuttle.exe` plus the bundled docs/files

## Provider Smoke Test

Noninteractive agent smoke test:

```bash
source ./env.sh
go run ./cmd/shuttle \
  --agent "Give me a one-sentence summary of what you can do in Shuttle." \
  --provider openai --auth api_key --model "${SHUTTLE_MODEL}"
```

## Runtime Selection

Runtime selection controls how Shuttle labels and routes the coding runtime layer behind `internal/agentruntime`. You can pick the runtime at startup with `--runtime` / `SHUTTLE_RUNTIME`, or change and persist it from `F10 -> Runtime`. Current choices:
- `builtin`: use Shuttle's built-in runtime behavior
- `auto`: prefer the installed authoritative runtime with the best declared parity, then fall back to `builtin`
- `pi`: currently rejected for authoritative use until it supports the full required request-kind set
- `codex_sdk`: select the CLI-backed authoritative Codex bridge explicitly
- `codex_app_server`: native Codex App Server bridge over stdio JSON-RPC with a long-lived app-server process per Shuttle runtime session and in-memory per-task native thread reuse across turns

Current implementation boundary:
- runtime selection is real and deterministic, including default command filling for explicit external runtime selections
- runtime selection is session-authoritative for agent reasoning; Shuttle still constructs every turn, but it sends each agent decision turn for that task/session back to the selected runtime
- Shuttle still owns execution panes, approvals, patch validation/application, transcript mutation, and task/session state
- `codex_sdk` currently uses a codex-specific turn handler over the shared Shuttle orchestration helpers and a local `codex` CLI compatibility check rather than a fully independent external execution stack
- Shuttle does not silently fall back to builtin for ordinary continuation turns once an authoritative external runtime is selected; if the runtime cannot continue, Shuttle stops and requires an explicit retry or runtime switch

Use `SHUTTLE_RUNTIME_COMMAND` to override the executable path for an explicit runtime selection. When you switch runtimes from `F10`, Shuttle persists both the selected runtime type and the current runtime command path, unless `--runtime` or `--runtime-command` was explicitly provided on startup. For `codex_sdk` and `codex_app_server`, the command must be installed and report a compatible Codex version (`0.118.0` or newer). `codex_sdk` remains the primary CLI-backed UX-validation bridge. `codex_app_server` now keeps one app-server process alive for the Shuttle runtime session and reuses in-memory native thread state for the active Shuttle task across ordinary continuation turns, including compaction. It still needs stronger restart/reconnect policy before `auto` should prefer it.

Current confidence level:
- cursory manual smoke testing now shows both `codex_sdk` and `codex_app_server` can at least accept ordinary prompts and return agent responses in the TUI
- the deeper runtime acceptance pass in [runtime-manual-test.md](runtime-manual-test.md) is still required before promoting `codex_app_server` further

Startup behavior note:
- interactive TUI launch no longer hard-fails if the persisted explicit `codex_sdk` or `codex_app_server` command is missing from `PATH` or otherwise invalid
- in that case Shuttle opens with an explicit startup error entry, keeps the selected runtime visible in runtime detail, and falls back to builtin for that launch only
- noninteractive startup paths such as one-shot `--agent` execution still fail fast on invalid explicit runtime configuration

Manual runtime validation steps live in [runtime-manual-test.md](runtime-manual-test.md).

## Trace Logging

Enable safe trace logging:

```bash
export SHUTTLE_TRACE="safe"
./launch.sh --trace --tui
```

Then inspect the log:

```bash
tail -f "${XDG_STATE_HOME:-$HOME/.local/state}/shuttle/trace.log"
```

Trace modes:
- `safe`: redacts raw commands, terminal contents, key input, and provider bodies
- `sensitive`: keeps full raw trace data, but Shuttle requires explicit consent before launch

Important:
- `trace.log` owns trace-detail behavior; `shuttle.log` is operational-only and redacts raw commands, prompts, key input, and provider payloads
- trace mode only controls what Shuttle writes to its trace log
- it does not disable normal runtime context sent to the active provider, such as shell output or recovery snapshots needed for agent reasoning

Persistent logs and shell history now default to XDG state space instead of the repo-local `.shuttle/` directory. Ephemeral staged command scripts, shell integration scripts, semantic shell state, and semantic stream captures now live in a separate private runtime directory. On startup Shuttle prunes stale staged artifacts, removes semantic state entries for panes that no longer exist in the managed tmux server, and keeps live pane state plus the managed socket intact. The local semantic state payload is now versioned so newer builds can reject incompatible future formats while still reading the older unversioned form.

Provider secret storage policy:
- preferred: OS keyring
- also supported: env var references
- if secure persistence is unavailable, Shuttle can still use a manually entered key for the current session
- optional fallback: plaintext local file storage, but only when explicitly enabled
- if the active provider is using the plaintext local fallback, the TUI shows a startup warning before normal composer interaction

Codex CLI model selection:
- Shuttle does not have an authoritative machine-readable Codex CLI picker feed
- when OpenAI model listing is available, Shuttle uses that catalog as a suggestions source for Codex-related models
- the settings UI labels those entries as suggestions; the live Codex CLI picker may differ
- manual model entry is still allowed for Codex CLI profiles

## TUI Notes

Core controls:
- `F1`: open the in-app help view
- `Shift-Tab`: switch Agent/Shell mode
- `Tab`: cycle composer completions, or insert a literal tab when no completion is available
- `Right Arrow`: accept the current ghost-text completion
- `Enter`: submit composer input
- `Ctrl+J`: insert newline in the composer
- `Home` / `End`: move to the start or end of the current composer line
- `Ctrl+Home` / `Ctrl+End`: jump the transcript to the top or bottom
- `Insert`: toggle composer overwrite mode
- `Esc`: clear composer or interrupt active work, depending on state
- `F2`: take control of the live shell pane, or the active temporary execution pane when an owned command is currently running there
- `Ctrl+G`: continue an active plan, or resume paused interactive agent check-ins after you handled a prompt/fullscreen app
- `S`: enter `KEYS>` mode when the active terminal is waiting for input or a fullscreen app owns the pane
- Shuttle also auto-enters `KEYS>` once when a fresh `awaiting_input` prompt is observed, such as a `sudo` password prompt
- in `KEYS>` mode, `Enter` normally sends the current buffer; for password, passphrase, confirmation, and menu-style prompts including common `ssh`/`sudo` auth waits Shuttle also appends `Enter` automatically, while true "press any key" prompts stay exact
- `Ctrl+Y` sends the current buffer plus `Enter`, and `Ctrl+J` inserts a literal `Enter` into the key sequence
- in `KEYS>` mode, `Shift-Tab` dismisses `KEYS>` and suppresses auto-reopen for the current waiting prompt until Shuttle observes a material prompt-state change
- `KEYS>` also accepts explicit tmux control-key tokens such as `<Ctrl+C>` or `<Esc>` for key events the TUI cannot capture directly
- each `KEYS>` send requires a fresh observed read of the active terminal; after Shuttle sends keys it refreshes the active execution and shell tail before allowing another send, and it briefly suppresses `KEYS>` auto-reopen while the prompt state settles so successful auth/input transitions do not bounce the composer back into key-send mode
- when the active interactive execution ends or becomes noninteractive again, Shuttle automatically exits `KEYS>` and restores the underlying composer mode
- text selection while Bubble Tea mouse mode is active depends on the terminal emulator: iTerm2 uses `Option`-drag, while some other terminals use `Shift`-drag
- `Ctrl+O`: inspect the selected transcript entry
- while the `Ctrl+O` detail view is open, typing incrementally filters visible detail lines; `Backspace` edits the filter and `Esc` clears it before closing the view
- `F10`: open settings

Slash commands in agent mode:
- `/help`: open the in-app help view
- `/approvals`: show or change the current session approval mode
- `/new`: start a fresh task without restarting Shuttle or losing shell continuity
- `/compact`: summarize older task context and keep a shorter live context window
- `/onboard`, `/provider`, `/model`, `/quit`: provider/settings/session commands; `/onboard` and `/provider` open `Configure Providers`, and `/model` opens the current provider detail focused on model selection

Approval modes:
- `confirm`: current default; safe commands stay as explicit proposals and risky actions still require approval
- `auto`: Shuttle auto-runs controller-classified safe local inspection and test commands, but still requires approval for writes, patches, remote work, network/process-control, and other risky actions
- `dangerous`: after an explicit warning confirmation, Shuttle auto-runs agent commands and auto-applies agent patches for the current session
- `/approvals` without an argument shows the current session mode; `/approvals confirm`, `/approvals auto`, and `/approvals dangerous` switch it

Settings notes:
- `F10` opens settings with `Session Settings`, `Runtime`, and `Configure Providers`; the runtime screen switches the active runtime immediately, lets you edit the runtime command path, previews selected versus effective runtime resolution, validates runtime health on selection changes or when you leave the command field, and persists that selection for future launches, while selecting a provider opens the shared provider detail screen that also contains model selection
- provider detail editing supports `F7` to test the provider config, automatic provider validation when loading that provider's model list, and `F8` to save and activate it immediately
- supported providers expose a `Thinking` radio control; OpenAI and OpenRouter also expose `Reasoning Effort` when `Thinking` is on
- the provider list and model list support direct mouse clicks; `Thinking` and `Reasoning Effort` stay keyboard toggles via `Space` or `Left`/`Right`
- pressing `Enter` on the `Model` field selects the highlighted filtered model result and runs a provider/model test without saving immediately
- multiline composer rendering is capped to 15 visible lines and scrolls older lines off the top as you keep inserting newlines

Terminal selection notes:
- use `Shift` + drag for your terminal emulator's normal text selection while Bubble Tea mouse mode is active
- use your terminal copy/paste shortcuts such as `Ctrl+Shift+C` and `Ctrl+Shift+V` for selected text and pasted input

Transcript result notes:
- successful silent commands collapse to a compact result line instead of showing `exit=0` and `(no output)`
- silent directory-changing commands can show the resulting cwd
- result tags are exit-aware: nonzero exits no longer render as green success entries
- completed shell commands now collapse into a single result block that shows the command header plus inline output, instead of keeping a separate expandable preview row
- `Ctrl+O` still opens the full detail view for the selected transcript entry, and clicking a transcript icon does the same
- the detail view supports incremental typed filtering so large command results and plans can be narrowed without leaving the keyboard
- very long result-command headers stay single-line by default and can be expanded inline by clicking the command text
- model-reply metadata is kept in the selected entry's `Ctrl+O` detail view instead of as a separate visible transcript row
- the initial workspace-ready system notice is retained in trace data but hidden from the default transcript view
- when the model needs authoritative shell identity or location, Shuttle can satisfy a native context inspection step internally instead of guessing from stale local/remote path context

Status line notes:
- the lower-right status uses compact inline segments separated with `*`, instead of filled badge backgrounds
- approvals render as lowercase `confirm`, `auto`, or `dangerous`
- the active model renders as `provider / model`, and context usage shows as a color-coded ASCII fill bar with current usage against the known window when available
- active work renders as a braille spinner plus a fixed-width elapsed-seconds label until it grows into minute-scale durations

The TUI is intentionally keyboard-first. Current priorities now live in [../BACKLOG.md](../BACKLOG.md), and older UX scratch notes are archived in [../completed/ui-scratchpad.md](../completed/ui-scratchpad.md).

## Important Docs

- [shell-tracking-architecture.md](shell-tracking-architecture.md)
- [architecture.md](architecture.md)
- [../BACKLOG.md](../BACKLOG.md)
- [provider-auth-guide.md](provider-auth-guide.md)
- [shell-execution-strategy.md](shell-execution-strategy.md)
- [agent-runtime-design.md](agent-runtime-design.md)
- [runtime-management-design.md](runtime-management-design.md)
- [provider-integration-design.md](provider-integration-design.md)
- [../completed/implementation-plan.md](../completed/implementation-plan.md)

## Current Limitations

- patch proposals still require explicit user apply/approval; there is no auto-apply mode
- patch editing is not implemented; patch proposals support apply, reject, or ask-agent
- native patch apply is text-unified-diff only; binary and other advanced patch forms are rejected
- the serial shell-tracking model is in good shape, but remote/container semantic bootstrap is still incomplete
- transcript and UI polish is still catching up with the newer shell/runtime model
- multi-card or parallel execution UI is intentionally deferred
- release packaging now has a GitHub release workflow, install script, and managed tmux defaults, but there is still no package-manager distribution path

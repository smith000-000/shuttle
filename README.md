# Shuttle

Shuttle is a tmux-backed AI terminal assistant.

It runs a persistent real shell in the top tmux pane and a Bubble Tea TUI in the bottom pane. Agent-approved commands can also run in owned tmux execution panes, while the persistent shell remains the continuity surface for `F2`, `$>`, cwd, and recent manual shell activity.

## Status

This repo is still pre-release.

What is working now:
- tmux workspace bootstrap and rediscovery
- shell command injection into the real top pane
- tracked command observation with execution states
- persistent user-shell context for cwd, recent shell output, and recent manual shell actions
- owned tmux execution panes for agent-approved commands
- Agent and Shell modes in the TUI
- approval and refine flow
- local and remote handoff with `F2`
- `KEYS>` mode for sending raw terminal input
- partial semantic shell integration for local shells
- serial agentic command loops with one proposal at a time and auto-continue after results
- native unified-diff patch proposals with explicit apply/reject/ask-agent flow
- local file creation and edits through native patch application
- foreground attach and handoff reconciliation for manually started shell commands
- real OpenAI Responses API path with API-key auth
- provider settings UI with:
  - active provider switching
  - active model switching
  - provider detail editing
  - `F8` save-and-activate from provider details
- saved provider profiles and startup reloading
- provider secret handling with:
  - OS keyring persistence
  - session-only fallback
  - explicit plaintext local fallback with warning
- safer runtime state and trace defaults
- Codex CLI login-based provider support
- Codex CLI model suggestions sourced from the OpenAI models catalog when available, with free-text entry still allowed

What is still in progress:
- broader semantic shell integration (`OSC 133` / `OSC 7`) consumption and subshell/bootstrap support
- provider onboarding polish and provider-auth validation
- provider registry/plugin architecture instead of static first-class wiring
- any richer shell bootstrap/helper mode beyond those standards
- transcript/UI cleanup and continued TUI/controller decomposition
- multi-card or parallel execution UI
- release packaging

## Requirements

- Go `1.25.0`
- `tmux` installed and available in `PATH`
- a normal terminal environment capable of running Bubble Tea

Optional:
- `OPENAI_API_KEY` for the real OpenAI provider
- `OPENROUTER_API_KEY` for the OpenRouter preset

## Quick Start

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
go run ./cmd/shuttle --socket shuttle-dev --session shuttle-dev --tui
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
- `SHUTTLE_RUNTIME_DIR`: optional private runtime directory for staged shell scripts and semantic shell state
- `SHUTTLE_ALLOW_PLAINTEXT_PROVIDER_SECRETS`: allow an explicit less-secure local file fallback if OS keyring storage is unavailable
- `SHUTTLE_TRACE`: `off`, `safe`, or `sensitive`
- `SHUTTLE_TRACE_CONSENT`: must be true or passed as `--trace-consent` when using sensitive trace

`launch.sh` loads `./env.sh` if present, otherwise it falls back to `./env.sh.sample`.

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

Run without the launcher:

```bash
go run ./cmd/shuttle --socket shuttle-dev --session shuttle-dev --tui
```

## Provider Smoke Test

Noninteractive agent smoke test:

```bash
source ./env.sh
go run ./cmd/shuttle --socket shuttle-dev --session shuttle-dev \
  --agent "Give me a one-sentence summary of what you can do in Shuttle." \
  --provider openai --auth api_key --model "${SHUTTLE_MODEL}"
```

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
- trace mode only controls what Shuttle writes to its trace log
- it does not disable normal runtime context sent to the active provider, such as shell output or recovery snapshots needed for agent reasoning

Persistent logs and shell history now default to XDG state space instead of the repo-local `.shuttle/` directory. Ephemeral staged command scripts and semantic shell state now live in a separate private runtime directory.

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
- `Ctrl+]`: switch Agent/Shell mode
- `Tab`: cycle composer completions, or insert a literal tab when no completion is available
- `Right Arrow`: accept the current ghost-text completion
- `Enter`: submit composer input
- `Ctrl+J`: insert newline in the composer
- `Esc`: clear composer or interrupt active work, depending on state
- `F2`: take control of the live shell pane
- `S`: enter `KEYS>` mode when the active terminal is waiting for input or a fullscreen app owns the pane
- `Ctrl+O`: inspect the selected transcript entry

Transcript result notes:
- successful silent commands collapse to a compact result line instead of showing `exit=0` and `(no output)`
- silent directory-changing commands can show the resulting cwd
- result tags are exit-aware: nonzero exits no longer render as green success entries

The TUI is intentionally keyboard-first. Current behavior is still evolving, so see [ui-scratchpad.md](ui-scratchpad.md) for active UX backlog notes.

## Important Docs

- [shell-tracking-architecture.md](shell-tracking-architecture.md)
- [architecture.md](architecture.md)
- [implementation-plan.md](implementation-plan.md)
- [provider-auth-guide.md](provider-auth-guide.md)
- [shell-execution-strategy.md](shell-execution-strategy.md)
- [provider-integration-plan.md](provider-integration-plan.md)
- [agent-runtime-design.md](agent-runtime-design.md)
- [runtime-management-design.md](runtime-management-design.md)
- [requirements-mvp.md](requirements-mvp.md)
- [patch-apply-research.md](patch-apply-research.md)

## Current Limitations

- patch proposals still require explicit user apply/approval; there is no auto-apply mode
- patch editing is not implemented; patch proposals support apply, reject, or ask-agent
- native patch apply is text-unified-diff only; binary and other advanced patch forms are rejected
- the serial shell-tracking model is in good shape, but remote/container semantic bootstrap is still incomplete
- transcript and UI polish is still catching up with the newer shell/runtime model
- multi-card or parallel execution UI is intentionally deferred
- release packaging is intentionally deferred for now

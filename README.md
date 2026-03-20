# Shuttle

Shuttle is a tmux-backed AI terminal assistant.

It runs a real shell in the top tmux pane and a Bubble Tea TUI in the bottom pane. The current branch also includes an execution monitor for long-running commands, interactive prompts, fullscreen apps, handoff into the live shell with `F2`, and agent-driven recovery actions like raw terminal key proposals.

## Status

This repo is still pre-release.

What is working now:
- tmux workspace bootstrap and rediscovery
- shell command injection into the real top pane
- tracked command observation with execution states
- Agent and Shell modes in the TUI
- approval and refine flow
- local and remote handoff with `F2`
- `KEYS>` mode for sending raw terminal input
- partial semantic shell integration for local shells
- real OpenAI Responses API path with API-key auth

What is still in progress:
- provider onboarding and saved profiles
- patch application and file creation flow
- more execution-monitor confidence hardening
- broader semantic shell integration (`OSC 133` / `OSC 7`) consumption and subshell/bootstrap support
- any richer shell bootstrap/helper mode beyond those standards
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

Persistent logs and shell history now default to XDG state space instead of the repo-local `.shuttle/` directory. Ephemeral staged command scripts and semantic shell state now live in a separate private runtime directory.

## TUI Notes

Core controls:
- `Tab`: switch Agent/Shell mode
- `Enter`: submit composer input
- `Ctrl+J`: insert newline in the composer
- `Esc`: clear composer or interrupt active work, depending on state
- `F2`: take control of the live shell pane
- `S`: enter `KEYS>` mode when the active terminal is waiting for input or a fullscreen app owns the pane
- `Ctrl+O`: inspect the selected transcript entry

The TUI is intentionally keyboard-first. Current behavior is still evolving, so see [ui-scratchpad.md](ui-scratchpad.md) for active UX backlog notes.

## Important Docs

- [implementation-plan.md](implementation-plan.md)
- [shell-execution-strategy.md](shell-execution-strategy.md)
- [provider-integration-plan.md](provider-integration-plan.md)
- [agent-runtime-design.md](agent-runtime-design.md)
- [runtime-management-design.md](runtime-management-design.md)
- [requirements-mvp.md](requirements-mvp.md)
- [patch-apply-research.md](patch-apply-research.md)

## Worktrees

This repo is currently being developed with multiple git worktrees.

Primary interactive execution branch:
- `/home/jsmith/source/repos/aiterm`

Secondary onboarding/provider branch:
- `/home/jsmith/source/repos/aiterm-model-onboarding`

List them with:

```bash
git worktree list
```

## Current Limitations

- proposed patches are not yet applied automatically
- the agent cannot yet create files unless Shuttle grows a real patch/apply path
- execution monitoring is much stronger now, but still being hardened for ambiguous shell takeover cases
- release packaging is intentionally deferred for now

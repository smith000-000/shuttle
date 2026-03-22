# Provider Authentication Guide

This document explains how provider authentication works in Shuttle today so you can test it deliberately instead of guessing from the UI.

It covers:
- provider/auth types
- where credentials come from
- how credentials are persisted
- how startup chooses the active provider
- what the settings UI is actually doing
- what to test manually

## Terms

- `provider`: the backend Shuttle talks to, for example `openai`, `openrouter`, `anthropic`, `ollama`, `custom`, or `codex_cli`
- `auth method`: how that provider is authenticated
- `profile`: Shuttle's resolved in-memory provider config
- `stored provider config`: provider metadata saved under the Shuttle state dir
- `secret store`: the actual place an API key is stored, if Shuttle persists one

## Supported Providers and Auth Modes

Current first-class providers:
- `mock`
- `openai`
- `openrouter`
- `openwebui`
- `anthropic`
- `ollama`
- `custom`
- `codex_cli`

Current auth modes:
- `none`
- `api_key`
- `codex_login`

Provider notes:
- `mock` uses `none`
- `openai`, `openrouter`, `openwebui`, `anthropic`, and `custom` normally use `api_key`
- `ollama` normally uses `none`, but can optionally use `api_key`
- `codex_cli` normally uses `codex_login`, but Shuttle also allows `api_key` or `none`

## Resolution Order at Startup

Shuttle resolves provider config in this order:

1. explicit provider flags/env that count as provider flags
2. otherwise, stored provider config from the Shuttle state dir
3. otherwise, onboarding/detection candidates
4. otherwise, defaults such as `mock`

Important behavior:
- if provider flags are set, Shuttle does not load stored provider config on top of them
- if provider flags are not set, Shuttle tries to load the selected stored provider first

In practice, if you launch with `./launch.sh` and `env.sh` exports provider values, those values usually win.

## Environment-Based Auth

The sample env file is [env.sh.sample](/home/jsmith/source/repos/aiterm/env.sh.sample).

Common environment variables:
- `SHUTTLE_PROVIDER`
- `SHUTTLE_AUTH`
- `SHUTTLE_MODEL`
- `SHUTTLE_BASE_URL`
- `SHUTTLE_API_KEY`
- `OPENAI_API_KEY`
- `OPENROUTER_API_KEY`
- `ANTHROPIC_API_KEY`

Detection behavior:
- `SHUTTLE_API_KEY` is a generic fallback if no provider-specific key is found
- provider-specific env vars are preferred for their matching presets
- `detectCodexCLICandidate()` checks `codex login status`
- `detectOllamaCandidate()` probes the local Ollama endpoint

Auth source labels you may see in the UI:
- `OPENAI_API_KEY`
- `OPENROUTER_API_KEY`
- `ANTHROPIC_API_KEY`
- `OS keyring`
- `session only`
- `local file (less secure)`
- `codex login`
- `none`

## Persistent Storage Model

Provider metadata and secrets are intentionally split.

### Stored provider config

Shuttle stores provider metadata in the state dir under:
- `providers/<preset>.json`
- `selected-provider`

That JSON includes:
- provider preset
- auth method
- base URL
- model
- CLI command when relevant
- `api_key_ref`

It does not normally store the raw API key inline.

### Secret storage order

If a provider uses `api_key`, Shuttle prefers:

1. env-var reference
2. OS keyring
3. session-only in-memory use
4. explicit plaintext local fallback, but only when allowed

More precisely:

- If the current profile points to an env var and the env var value matches the active key, Shuttle persists the env var name as the reference.
- If the key was manually entered and no env-var reference is being persisted, Shuttle tries to store the key in OS keyring.
- If keyring persistence fails during a settings/onboarding save:
  - Shuttle can still keep the provider active for the current session
  - persistence may fail with a visible warning
- If plaintext fallback is explicitly enabled, Shuttle may write the raw key to a private local secret file instead of failing persistence.

### Plaintext fallback

Plaintext local fallback is disabled by default.

To allow it:
- set `SHUTTLE_ALLOW_PLAINTEXT_PROVIDER_SECRETS=true`
- or pass `--allow-plaintext-provider-secrets`

When used:
- the secret is written under the provider-secrets area in the state dir
- the UI shows `local file (less secure)`
- Shuttle shows a blocking startup warning before normal composer interaction

This is intentionally a usability fallback, not a secure-at-rest design.

## Session-Only Behavior

If keyring storage is unavailable and plaintext fallback is not allowed:
- the provider can still be active for the current run
- the secret remains only in memory for that session
- on restart, that manually entered key will not be available unless it comes from env or is entered again

UI/auth-source behavior:
- if the profile has a raw API key in memory and no persisted reference, the label is `session only`

## Provider-Specific Auth Behavior

### OpenAI / OpenRouter / OpenWebUI / Custom

Backend family:
- `responses_http` for OpenAI, OpenWebUI, and Custom
- `openrouter` for OpenRouter

Auth:
- `api_key` sends `Authorization: Bearer <key>`
- `none` sends no auth header

Notes:
- OpenAI-compatible providers require a model
- `custom` requires `SHUTTLE_BASE_URL` or equivalent form entry

### Anthropic

Backend family:
- `anthropic`

Auth:
- `api_key` sends:
  - `x-api-key: <key>`
  - `anthropic-version: 2023-06-01`

### Ollama

Backend family:
- `ollama`

Auth:
- usually `none`
- optional `api_key` adds `Authorization: Bearer <key>`

Notes:
- Shuttle probes Ollama reachability during detection
- base URL is normalized toward `/api`

### Codex CLI

Backend family:
- `cli_agent`

Auth:
- `codex_login`
- `api_key`
- `none`

Current behavior:
- `codex_login` checks `codex login status` before agent creation
- if not logged in, provider creation fails with a clear error
- the actual agent turn is executed by spawning the local `codex` CLI

Important:
- Codex model suggestions in Shuttle are not authoritative
- Shuttle now uses the OpenAI model catalog as a suggestions source when available
- the live Codex CLI picker may differ
- manual model entry is still allowed

## Settings and Onboarding Behavior

Relevant settings sections:
- `Providers`
- `Active Provider`
- `Active Model`

Current behavior:
- `Enter` applies a provider/model switch in place
- `Esc` goes back one level
- `F10` fully closes settings

Provider form behavior:
- saving may persist config and secret state
- if the edited provider is also the active provider, Shuttle switches the live provider immediately
- if persistence fails because secure secret storage is unavailable, the provider may still be active for the session and Shuttle shows a warning

Model validation:
- strict validation happens only when Shuttle can fetch a model catalog for that provider
- if no catalog is available, free-text model entry is still allowed
- for `codex_cli`, the model list is suggestion-only

## UI Signals You Should Expect

### Provider switch transcript entry

When a provider switch succeeds, Shuttle writes a system entry like:
- provider name
- provider preset
- model
- auth source label

### Startup warning

You should only see the blocking startup warning when:
- the active provider is using `local file (less secure)`

You should not see it for:
- env-backed providers
- keyring-backed providers
- session-only providers that were not persisted locally

## Manual Test Matrix

These are the most useful auth tests to run.

### 1. Env-backed OpenAI

Setup:
- `SHUTTLE_PROVIDER=openai`
- `SHUTTLE_AUTH=api_key`
- `OPENAI_API_KEY=...`

Expected:
- no less-secure startup warning
- settings should show env-based auth source, not `local file (less secure)`
- provider works across restarts as long as env is present

### 2. Env-backed OpenRouter

Setup:
- `SHUTTLE_PROVIDER=openrouter`
- `SHUTTLE_AUTH=api_key`
- `OPENROUTER_API_KEY=...`

Expected:
- same as OpenAI, but using the OpenRouter env source

### 3. Manual API key with working keyring

Setup:
- remove the relevant provider API key env var
- open settings and enter the API key manually

Expected:
- save succeeds
- after restart, provider still works
- auth source shows `OS keyring`
- no less-secure startup warning

### 4. Manual API key with no keyring, plaintext fallback disabled

Setup:
- simulate no usable keyring backend
- do not enable plaintext fallback

Expected:
- provider can still become active for the current session
- Shuttle warns that secure persistence was unavailable
- after restart, the key is gone
- auth source during the live session should be `session only`

### 5. Manual API key with no keyring, plaintext fallback enabled

Setup:
- simulate no usable keyring backend
- set `SHUTTLE_ALLOW_PLAINTEXT_PROVIDER_SECRETS=true`
- enter the API key manually

Expected:
- save succeeds using plaintext local fallback
- after restart, provider still works
- auth source shows `local file (less secure)`
- startup warning appears before normal composer use

### 6. Codex login path

Setup:
- configure `codex_cli`
- use `codex_login`

Expected:
- if `codex login status` reports logged in, provider can activate
- if not logged in, Shuttle should fail clearly and tell you to run `codex login`

### 7. Codex API-key path

Setup:
- configure `codex_cli`
- set auth to `api_key`
- provide a key manually or by env

Expected:
- provider profile resolves
- model suggestions may come from OpenAI catalog
- manual model entry still allowed

## Known Limitations

- Provider registration is still static, not plugin-driven.
- Codex CLI model suggestions are only a best-effort proxy from OpenAI's model catalog.
- Safe trace mode protects local trace logs, but it does not stop normal provider context flow needed for agent operation.
- There is not yet a dedicated end-user screen that fully explains secret persistence decisions after every save; some of this is still inferred from the auth-source label and warning messages.

## Files That Define This Behavior

If you want to inspect code while testing:
- [internal/provider/profiles.go](/home/jsmith/source/repos/aiterm/internal/provider/profiles.go)
- [internal/provider/detect.go](/home/jsmith/source/repos/aiterm/internal/provider/detect.go)
- [internal/provider/storage.go](/home/jsmith/source/repos/aiterm/internal/provider/storage.go)
- [internal/provider/factory.go](/home/jsmith/source/repos/aiterm/internal/provider/factory.go)
- [internal/provider/responses.go](/home/jsmith/source/repos/aiterm/internal/provider/responses.go)
- [internal/provider/anthropic.go](/home/jsmith/source/repos/aiterm/internal/provider/anthropic.go)
- [internal/provider/ollama.go](/home/jsmith/source/repos/aiterm/internal/provider/ollama.go)
- [internal/provider/codex_cli.go](/home/jsmith/source/repos/aiterm/internal/provider/codex_cli.go)
- [internal/tui/model.go](/home/jsmith/source/repos/aiterm/internal/tui/model.go)

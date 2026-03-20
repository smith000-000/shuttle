# Shuttle Onboarding/Auth Worktree Plan

## Purpose
This worktree is dedicated to provider onboarding, auth detection, profile selection, and fallback tooling.

Use this document as the primary brief for the secondary Codex session. It is a surgical subset of:
- [provider-integration-design.md](provider-integration-design.md)
- [provider-integration-plan.md](provider-integration-plan.md)
- [implementation-plan.md](implementation-plan.md)
- [requirements-mvp.md](requirements-mvp.md)

Do not broaden this branch into execution-monitor, tmux, handoff, fullscreen, or shell-tracking work. That work is already active on `command-execution-redesign`.

## Branch Scope
This branch should own:
- provider profile modeling and persistence
- onboarding candidate detection and ranking
- provider health checks
- profile selection and switching primitives
- Codex CLI delegation path for existing login reuse
- UX/doc flows for onboarding and provider selection

This branch should not own:
- shell command lifecycle tracking
- tmux pane monitoring
- fullscreen interaction
- execution-state UX
- patch application or file-edit approval systems

## Product Constraints
Keep these rules intact:
- preserve the existing `controller.Agent` contract
- do not reimplement proprietary login flows inside Shuttle
- prefer env-var references over storing raw secrets
- treat Codex-login reuse as CLI delegation first, not native Shuttle auth
- keep HTTP providers and CLI providers separate in the model
- make onboarding detection-driven, not endpoint-first

## Current State
Already implemented on this worktree baseline:
- provider/backend/auth decomposition
- provider factory
- OpenAI-compatible `responses_http` adapter
- OpenAI API-key path
- core config normalization for provider selection

Not yet implemented:
- OpenRouter preset verification and manual flow hardening
- onboarding candidate detection and ranking
- provider health-check command/path
- persisted named provider profiles
- profile switching UI/command flow
- Codex CLI adapter

## Requirements To Satisfy
These come directly from [requirements-mvp.md](requirements-mvp.md):
- `FR-45`: support configurable provider and model profiles
- `FR-46`: user can inspect and modify provider settings through the TUI
- `FR-47`: provider config supports provider type, model name, base URL, authentication reference, and profile name
- `FR-48`: support multiple named provider profiles and switching

The current branch does not need to finish the full TUI for provider settings, but it should produce the backend/profile machinery needed for that UI.

## Implementation Order
Follow this sequence. Do not skip ahead to Codex CLI until the earlier slices are stable.

### Slice 1: OpenRouter + Custom Endpoint Hardening
Goal:
- finish the shared `responses_http` path so OpenAI, OpenRouter, and custom endpoints are all explicitly verified

Deliverables:
- OpenRouter-specific request/header tests
- custom base URL smoke coverage
- clear resolver defaults for all supported presets
- manual test instructions for OpenAI and OpenRouter

Exit criteria:
- switching presets changes base URL, auth source, and default model as expected
- adapter tests cover OpenAI and OpenRouter differences

### Slice 2: Provider Health Check
Goal:
- add a product-owned health-check path that can validate a resolved profile before full use

Deliverables:
- `provider health` command or equivalent internal path
- HTTP health check for `responses_http`
- structured success/failure output
- transcript-safe error mapping

Exit criteria:
- a profile can be validated without launching the whole interactive flow
- failures explain whether the issue is auth, endpoint, model, or transport

### Slice 3: Detection-Based Onboarding
Goal:
- synthesize likely working provider choices automatically

Detection inputs:
- `OPENAI_API_KEY`
- `OPENROUTER_API_KEY`
- `SHUTTLE_API_KEY`
- installed `codex`
- previously saved Shuttle profiles

Deliverables:
- candidate detection
- candidate ranking
- chosen/default profile synthesis
- onboarding result summary showing:
  - chosen path
  - base URL
  - model
  - auth source

Ranking order:
1. existing authenticated Codex CLI
2. OpenAI API key
3. OpenRouter API key
4. previously saved Shuttle profile
5. custom endpoint

Exit criteria:
- a new user with common env vars or Codex installed can get to one working profile in one flow

### Slice 4: Profile Persistence
Goal:
- persist named provider profiles cleanly without storing secrets directly

Deliverables:
- persisted provider profiles under `.shuttle/`
- explicit selected-profile ID
- non-secret profile fields:
  - name
  - preset
  - backend family
  - auth method
  - base URL
  - model
  - env-var reference
  - CLI command/args where relevant
- last-used profile persistence

Rules:
- store env var names, not raw API keys
- do not copy Codex tokens into Shuttle-managed config

Exit criteria:
- at least two profiles can be saved and reloaded
- startup can resolve the selected profile deterministically

### Slice 5: Codex CLI Delegation
Goal:
- support “use my existing Codex login” via CLI delegation, not native auth replication

Deliverables:
- `codex_cli` profile preset
- `cli_agent` adapter for installed `codex`
- onboarding detection for Codex availability/auth readiness
- noninteractive subprocess contract with timeout/error handling

Rules:
- do not scrape an interactive Codex terminal UI
- do not invent a new Codex login flow inside Shuttle
- keep failure messages explicit when Codex is present but not authenticated

Exit criteria:
- Shuttle can use installed Codex CLI as an `Agent`
- auth is clearly delegated, not managed by Shuttle

### Slice 6: Minimal Selection Surface
Goal:
- make profile switching usable without env-var editing between runs

Deliverables:
- current profile indicator
- basic profile switch command/path
- health/capability summary per profile

This does not need a polished final TUI settings screen yet. A CLI/internal command path is acceptable if it unblocks real usage.

## Files and Packages To Prefer
Work mainly in:

```text
internal/
  provider/
    profiles.go
    resolve.go
    factory.go
    responses.go
    detect.go
    codex_cli.go
  config/
  app/
```

Likely additions:

```text
internal/
  provider/
    detect_test.go
    codex_cli_test.go
    health.go
    health_test.go
```

If persistence support is needed, add a small focused helper instead of inventing a large subsystem.

## Testing Expectations
Prefer unit and subprocess-style tests over broad end-to-end UI work on this branch.

Required coverage:
- profile resolution
- candidate ranking
- env/CLI detection logic
- auth header and base URL coverage
- health-check success/failure mapping
- Codex CLI adapter contract with fake subprocess binaries

Useful manual checks:
- OpenAI API-key flow
- OpenRouter API-key flow
- onboarding with only `OPENAI_API_KEY`
- onboarding with only `OPENROUTER_API_KEY`
- onboarding with no keys but with installed `codex`

## Explicit Non-Goals For This Branch
Do not spend time here on:
- shell monitor redesign
- fullscreen app handling
- send-keys UX
- active-command cards
- transcript scrolling or modal UX

If those concerns come up, leave a note and stop. This branch should stay narrow.

## Suggested Prompt For Secondary Codex Session
Use this file as the brief.

Suggested kickoff:

```text
Read onboarding-auth-worktree-plan.md and execute Slice 1 first. Stay on this branch only. Do not touch execution-monitor or tmux logic from the other worktree. Prefer small vertical slices with tests.
```

## Handoff Rule
Before stopping, update:
- this file with current slice status if the plan changes materially
- [provider-integration-plan.md](provider-integration-plan.md) if milestones move

Keep this worktree understandable without requiring the other branch’s context.

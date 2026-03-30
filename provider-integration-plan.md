# Shuttle Provider Integration Plan

## Purpose
Turn the provider integration design into a staged execution plan that keeps onboarding simple, avoids early auth fragility, and preserves the existing controller contract.

Design reference: [provider-integration-design.md](provider-integration-design.md)

## Current Status
As of March 30, 2026:
- Phase 1 is complete.
- Phase 2 is materially complete.
- Parts of Phases 3 through 5 are now implemented.

Currently implemented:
- provider profile type and backend/auth decomposition
- config normalization for provider preset, auth mode, base URL, and API-key sourcing
- provider factory
- OpenAI-compatible `responses_http` adapter
- OpenRouter preset support through the shared OpenAI-compatible path
- Codex CLI login-based provider path
- app wiring that instantiates the resolved real provider
- persisted provider profiles and active-provider reloading
- provider switching and model switching UI
- provider detail editing, save-and-activate, and health/auth tests from settings
- unit tests and `httptest` coverage for resolver and HTTP adapter paths

Not yet implemented:
- detection-based first-run onboarding and candidate ranking
- broader provider registry/plugin loading beyond the built-in paths
- richer provider capability surfacing
- a generalized CLI-agent bridge beyond the current Codex path

---

# 1. Implementation Strategy

Build provider integration in vertical slices:

1. model the profile and resolution system
2. ship native HTTP providers with API-key auth
3. add onboarding and health checks
4. add Codex CLI delegation for existing login reuse
5. extend to other CLI agents only after the Codex path is stable

This sequencing is deliberate:
- native HTTP adapters are simpler to verify
- onboarding can be validated without full TUI configuration work
- Codex login reuse is easier to ship safely through CLI delegation than through custom auth replication

---

# 2. Phase 1: Profile Model and Resolver

## Goal
Create the product-owned configuration model for providers before adding more backends.

## Deliverables
- `ProviderProfile` type
- backend family, preset, and auth-method enums
- preset resolver for OpenAI, OpenRouter, custom, and Codex CLI
- config loading for selected profile and profile overrides
- profile persistence under `.shuttle/`

## Likely Packages
- `internal/provider/profiles.go`
- `internal/provider/resolve.go`
- `internal/config/config.go`
- `internal/persistence/` or `.shuttle/` JSON helpers

## Exit Criteria
- Shuttle can load one resolved provider profile deterministically
- the selected profile is explicit in logs and startup output
- profile resolution is covered by unit tests

## Status
Completed.

---

# 3. Phase 2: Native Responses HTTP Adapter

## Goal
Support direct OpenAI and OpenRouter usage over a shared Responses-compatible adapter.

## Deliverables
- shared HTTP adapter behind `controller.Agent`
- OpenAI preset with API key auth
- OpenRouter preset with API key auth
- custom base URL preset
- structured response mapping into Shuttle `AgentResponse`
- provider-specific request and response tests using `httptest`

## Notes
- request the model to emit Shuttle-shaped structured JSON
- keep the adapter stateless beyond profile config
- surface provider errors as structured transcript-safe messages

## Exit Criteria
- one real provider works end to end in the existing TUI loop
- OpenAI and OpenRouter both pass adapter tests
- profile selection changes base URL, auth header, and default model as expected

## Status
Materially completed.

Completed:
- OpenAI-compatible Responses adapter with API-key auth
- structured JSON normalization into Shuttle agent responses
- OpenRouter support through the same shared adapter path
- custom base URL support
- `httptest` coverage for success, approval/plan mapping, and error handling

Remaining:
- broader preset-specific verification and manual smoke coverage

---

# 4. Phase 3: Detection-Based Onboarding

## Goal
Make first run choose the best likely provider path automatically.

## Deliverables
- environment detection:
  - `OPENAI_API_KEY`
  - `OPENROUTER_API_KEY`
  - `SHUTTLE_API_KEY`
- CLI detection:
  - installed `codex`
- candidate profile synthesis and ranking
- provider health check command
- onboarding command or first-run prompt

## UX Requirements
- show the chosen base URL, model, and auth source
- explain why one candidate was ranked above another
- let the user override the default choice

## Exit Criteria
- a new user with either `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, or logged-in Codex can get to a working profile in one flow
- health check failures are actionable

## Status
Partially completed.

Completed:
- provider health/auth test path from settings

Remaining:
- automatic candidate detection and ranking for first-run onboarding
- clearer explanation of why a candidate was chosen by default

---

# 5. Phase 4: Codex CLI Adapter

## Goal
Support "use my existing Codex login" without implementing a new login flow inside Shuttle.

## Deliverables
- `cli_agent` adapter for `codex`
- profile preset `codex_cli`
- noninteractive invocation contract
- timeout and error handling
- onboarding detection path for authenticated Codex usage

## Design Rules
- do not copy Codex tokens into Shuttle config
- do not scrape a fullscreen interactive TUI
- require a command path that is deterministic and scriptable

## Exit Criteria
- Shuttle can use an installed Codex CLI as an agent backend
- the profile shows that auth is delegated rather than Shuttle-managed
- failure modes are clear when Codex is installed but not authenticated

## Status
Implemented in a first pass through the Codex CLI login-based provider path.

---

# 6. Phase 5: Profile Switching and UI Surfacing

## Goal
Make provider selection visible and manageable from Shuttle itself.

## Deliverables
- current profile indicator in status or settings
- basic profile switch command
- provider health and capability display
- last-used profile persistence

## Exit Criteria
- a user can switch between at least two configured profiles without editing env vars between runs

## Status
Implemented in a first pass.

Completed:
- current profile indicator in the status line
- profile switching through settings
- model switching through settings
- provider detail editing, health/auth tests, and save-and-activate flow

Remaining:
- richer capability and health surfacing beyond the current settings actions

---

# 7. Deferred Work

Do not treat these as part of the current provider milestone:
- native Shuttle-managed Codex login flow
- generalized "any CLI coding agent" support
- ACP-backed tool/runtime integration
- richer onboarding automation and ranking UX

They are valuable, but each adds a distinct failure class.

---

# 8. Test Strategy

## Unit Tests
- profile resolution
- candidate ranking
- env and CLI detection logic
- structured response normalization

## HTTP Adapter Tests
- `httptest` request and response validation
- auth header coverage
- base URL and model override coverage
- error mapping tests

## CLI Adapter Tests
- subprocess contract tests with fake binaries
- timeout handling
- auth-missing detection

## End-to-End Tests
- TUI flow with mock provider remains green
- TUI flow with a real HTTP provider behind a test server
- onboarding creates a usable selected profile

---

# 9. Recommended Initial Package Additions

```text
internal/
  provider/
    profiles.go
    resolve.go
    factory.go
    responses.go
    responses_test.go
    codex_cli.go
    codex_cli_test.go
    detect.go
    detect_test.go
```

This keeps profile modeling, detection, and runtime adapters grouped without leaking provider-specific logic into the controller.

---

# 10. Immediate Next Slice

The next code slice should be:

1. add detection and candidate ranking for first-run onboarding
2. improve preset-specific verification and manual smoke coverage
3. surface provider capabilities and health more clearly in the UI
4. only then broaden CLI-provider support beyond the current Codex path if there is a concrete need

That keeps the next work on onboarding and operator clarity instead of reopening the provider abstraction again.

## Resume Commands

```bash
go test -count=1 ./...
```

```bash
export OPENAI_API_KEY=your_key_here
go run ./cmd/shuttle --socket shuttle-openai --session shuttle-openai --tui --provider openai --auth api_key --model gpt-5-nano-2025-08-07
```

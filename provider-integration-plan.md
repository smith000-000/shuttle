# Shuttle Provider Integration Plan

## Purpose
Turn the provider integration design into a staged execution plan that keeps onboarding simple, avoids early auth fragility, and preserves the existing controller contract.

Design reference: [provider-integration-design.md](provider-integration-design.md)

## Current Status
As of March 11, 2026:
- Phase 1 is complete enough for a first real provider path.
- Phase 2 is partially complete.
- Phase 3 onward has not been implemented yet.

Currently implemented:
- provider profile type and backend/auth decomposition
- config normalization for provider preset, auth mode, base URL, and API-key sourcing
- provider factory
- OpenAI-compatible `responses_http` adapter
- app wiring that instantiates either the mock agent or the resolved real provider
- unit tests and `httptest` coverage for the resolver and HTTP adapter

Not yet implemented:
- OpenRouter-specific verification
- custom-endpoint verification beyond resolver support
- onboarding candidate detection and ranking
- health-check command
- Codex CLI adapter
- persisted named provider profiles
- provider switching UI

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
Partially completed.

Completed:
- OpenAI-compatible Responses adapter with API-key auth
- structured JSON normalization into Shuttle agent responses
- `httptest` coverage for success, approval/plan mapping, and error handling

Remaining:
- OpenRouter-specific request and header verification
- custom endpoint smoke coverage
- live manual validation against a real OpenAI API key on a developer machine

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
Not started.

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
Not started.

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
Not started.

---

# 7. Deferred Work

Do not treat these as part of the first provider milestone:
- native Shuttle-managed Codex login flow
- generalized "any CLI coding agent" support
- ACP-backed tool/runtime integration
- direct profile editing UI with secret storage

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

1. add OpenRouter preset verification and tests on the existing HTTP adapter
2. add a provider health-check path
3. add detection and candidate ranking for first-run onboarding
4. persist the selected provider profile under `.shuttle/`
5. only then start the Codex CLI bridge

That keeps the next work on the now-existing provider path instead of reopening the abstraction layer again.

## Resume Commands

```bash
go test -count=1 ./...
```

```bash
export OPENAI_API_KEY=your_key_here
go run ./cmd/shuttle --socket shuttle-openai --session shuttle-openai --tui --provider openai --auth api_key --model gpt-5-nano-2025-08-07
```

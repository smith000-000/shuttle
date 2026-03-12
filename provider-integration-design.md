# Shuttle Provider Integration Design

## Purpose
Define how Shuttle should support multiple provider backends, multiple auth methods, and onboarding that can detect and prefer the best available path without turning the controller into provider-specific code.

This document is the architecture note for the work currently grouped under Milestone 5.

---

# 1. Goals

Shuttle provider integration should:
- support direct OpenAI usage with API keys
- support OpenRouter usage with API keys
- support Codex-style login reuse without forcing the user to create a second auth flow
- support a custom Responses-compatible base URL
- allow talking to an existing CLI coding agent where that is operationally safer than reimplementing its auth/runtime
- keep the existing `controller.Agent` contract stable
- make onboarding detect likely working options instead of forcing the user to understand endpoints first

## 1.1 Non-Goals

This design does not try to:
- standardize every future provider under one generic SDK
- reimplement arbitrary proprietary login flows inside Shuttle on day one
- promise that every interactive CLI agent can be driven safely through subprocess text mode
- make ACP a v1 dependency

---

# 2. Core Design Rule

Separate these concerns explicitly:

1. backend family
2. provider preset
3. auth method
4. runtime adapter

If those are collapsed into a single "provider" string, onboarding and debugging become messy immediately.

---

# 3. Provider Model

## 3.1 Backend Family

The backend family defines the transport and operational model.

```go
type BackendFamily string

const (
    BackendResponsesHTTP BackendFamily = "responses_http"
    BackendCLIAgent      BackendFamily = "cli_agent"
    BackendACPStdio      BackendFamily = "acp_stdio" // deferred
)
```

Initial meaning:
- `responses_http`: Shuttle talks directly to an OpenAI-compatible Responses endpoint over HTTP.
- `cli_agent`: Shuttle delegates agent turns to an installed CLI agent.
- `acp_stdio`: future structured tool/runtime bridge, not part of the first implementation.

## 3.2 Provider Preset

The preset selects a known backend configuration shape.

```go
type ProviderPreset string

const (
    PresetOpenAI     ProviderPreset = "openai"
    PresetOpenRouter ProviderPreset = "openrouter"
    PresetCustom     ProviderPreset = "custom"
    PresetCodexCLI   ProviderPreset = "codex_cli"
)
```

Deferred presets can include:
- `claude_code`
- `gemini_cli`
- other CLI agents with stable noninteractive contracts

## 3.3 Auth Method

Auth must be modeled independently from the preset.

```go
type AuthMethod string

const (
    AuthAPIKey      AuthMethod = "api_key"
    AuthCodexLogin  AuthMethod = "codex_login"
    AuthInherited   AuthMethod = "inherited_env"
    AuthNone        AuthMethod = "none"
)
```

Notes:
- `api_key` is the standard direct HTTP path.
- `codex_login` means Shuttle is reusing an existing Codex-authenticated environment rather than performing its own ChatGPT login flow.
- `inherited_env` is useful for CLI agents that already discover their own credentials from the environment or local config.

## 3.4 Provider Profile

Shuttle should persist provider configuration as a profile, not a loose set of flags.

```go
type ProviderProfile struct {
    ID             string
    Name           string
    BackendFamily  BackendFamily
    Preset         ProviderPreset
    AuthMethod     AuthMethod
    BaseURL        string
    Model          string
    APIKeyEnvVar   string
    CLICommand     string
    CLIArgs        []string
    Capabilities   CapabilitySet
    Source         ProfileSource
    HealthStatus   HealthStatus
}
```

Rules:
- prefer storing env var references over raw secrets
- do not copy Codex or other CLI tokens into Shuttle-managed config
- keep profile shape broad enough to support both HTTP and CLI adapters

---

# 4. Recommended First-Class Paths

## 4.1 Native HTTP Paths

These should be native first-class integrations:

1. OpenAI + API key
2. OpenRouter + API key
3. custom Responses-compatible endpoint + API key

These all fit naturally under `responses_http`.

## 4.2 Codex Login Path

The safest first implementation of "use my existing OpenAI Codex login" is:
- detect an installed, already-authenticated `codex` CLI
- expose it as a `cli_agent` profile
- delegate turns to that CLI instead of trying to recreate Codex login inside Shuttle

Why:
- it avoids duplicating a fragile auth flow
- it satisfies the onboarding goal faster
- it also covers the user's broader request to talk to an existing CLI coding agent

This means the first Codex-login story is a CLI bridge, not a native Shuttle-managed ChatGPT login flow.

## 4.3 Native Codex Backend Reuse

Native direct use of the Codex ChatGPT-backed Responses endpoint is worth considering later, but should be deferred until the simpler paths are stable.

Rationale:
- the transport exists, but Shuttle should not couple itself early to private or fast-moving auth/runtime details when a safer delegation path already exists

---

# 5. Intelligent Onboarding

## 5.1 Detection First

Onboarding should detect available paths before asking the user to choose.

Detection inputs:
- `OPENAI_API_KEY`
- `OPENROUTER_API_KEY`
- `SHUTTLE_API_KEY`
- installed `codex` CLI
- any persisted Shuttle profiles
- optional custom endpoint env vars if already configured

## 5.2 Candidate Generation

The onboarding flow should synthesize candidate profiles such as:
- "Use OpenAI API key from `OPENAI_API_KEY`"
- "Use OpenRouter API key from `OPENROUTER_API_KEY`"
- "Use existing Codex login through installed `codex` CLI"
- "Use custom Responses endpoint"

## 5.3 Ranking

Rank candidates by:
1. least user setup required
2. strongest confidence that the path already works
3. lowest operational brittleness

Recommended default ranking:
1. existing authenticated Codex CLI
2. OpenAI API key
3. OpenRouter API key
4. previously saved Shuttle profile
5. custom endpoint

## 5.4 Health Check

Before finalizing a selected profile, Shuttle should run a small health check:
- HTTP profile: make a minimal authenticated request or lightweight metadata probe
- CLI profile: run a noninteractive smoke command with a short timeout

The onboarding result should clearly say:
- which path was chosen
- what model and base URL will be used
- what auth source was detected

---

# 6. Endpoint Resolution

Shuttle should own a small resolver that maps `{preset, auth method}` into defaults.

## 6.1 Initial Resolution Matrix

| Preset | Backend | Auth | Default Base URL | Default Model | Notes |
| --- | --- | --- | --- | --- | --- |
| `openai` | `responses_http` | `api_key` | `https://api.openai.com/v1` | `gpt-5-nano-2025-08-07` | Use Responses API |
| `openrouter` | `responses_http` | `api_key` | `https://openrouter.ai/api/v1` | user-selected | Use OpenAI-compatible Responses API |
| `custom` | `responses_http` | `api_key` or `none` | user-supplied | user-supplied | Treat as Responses-compatible |
| `codex_cli` | `cli_agent` | `codex_login` or `inherited_env` | n/a | CLI-managed | Delegate to installed Codex CLI |

Important rule:
- the resolver sets defaults
- the profile still stores the resolved values explicitly after onboarding

---

# 7. Runtime Adapter Strategy

## 7.1 Shuttle-Owned Contract

The controller should continue to depend only on:

```go
type Agent interface {
    Respond(ctx context.Context, input AgentInput) (AgentResponse, error)
}
```

No controller code should care whether the result came from:
- OpenAI over HTTP
- OpenRouter over HTTP
- Codex CLI
- a later ACP bridge

## 7.2 HTTP Responses Adapter

The HTTP adapter should:
- accept a resolved provider profile
- build one normalized request shape
- include compact relevant conversation and shell context on each turn
- request structured JSON output from the model
- map the returned JSON into Shuttle's `AgentResponse`

The adapter should be written once for Responses-compatible endpoints and parameterized by:
- base URL
- auth header policy
- model
- provider-specific extra headers where needed

## 7.3 CLI Agent Adapter

The CLI adapter should:
- launch a known CLI agent in a noninteractive subprocess mode
- pass normalized task context
- request machine-readable output if the CLI supports it
- map the result into `AgentResponse`

The CLI adapter should be profile-scoped, not global. Different CLI tools may need different command shapes.

## 7.4 Why CLI Adapters Are Separate

CLI agents are not just another endpoint. They differ in:
- startup latency
- authentication ownership
- prompt format
- output structure
- failure modes

Trying to hide that under a pure HTTP-style provider abstraction will create poor diagnostics.

---

# 8. On-Disk Configuration and Persistence

## 8.1 Storage Rule

Shuttle should persist provider profiles separately from ephemeral task state.

Recommended early storage:
- JSON or SQLite-backed profile records under `.shuttle/`

Recommended default fields to persist:
- selected profile ID
- profile name
- preset
- backend family
- auth method
- model
- base URL
- referenced env var name
- CLI command path if applicable
- last successful health check timestamp

## 8.2 Secret Handling

Initial rule:
- prefer env var references over raw secret storage
- do not read and rewrite another tool's tokens into Shuttle config

Future enhancement:
- optional OS keyring integration if direct key entry becomes a required UX path

---

# 9. Recommended Initial Scope

## Phase A

Build these first:
- provider profile model
- detection-based onboarding
- native `responses_http` adapter for OpenAI and OpenRouter
- custom base URL support

## Phase B

Then add:
- `cli_agent` adapter for Codex CLI
- "use existing Codex login" onboarding path

## Phase C

Only after those are stable:
- additional CLI agents
- ACP-backed adapters
- native Shuttle-managed Codex auth reuse if still needed

---

# 10. Open Questions

1. What noninteractive Codex CLI contract is stable enough to depend on for a first CLI adapter?
2. Should Shuttle expose multiple saved profiles in the TUI immediately, or keep profile switching CLI-only at first?
3. How much provider-specific capability metadata should be surfaced in the UI?
4. Is direct native Codex-authenticated HTTP integration still worth pursuing once the Codex CLI bridge exists?

---

# 11. Reference Inputs

As of March 11, 2026, these references materially inform this design:
- OpenAI Codex CLI docs: <https://developers.openai.com/codex/cli>
- OpenAI "Unrolling the Codex agent loop": <https://openai.com/index/unrolling-the-codex-agent-loop/>
- OpenRouter Responses API overview: <https://openrouter.ai/docs/api-reference/responses-api/overview>

# Shuttle Pi Runtime Integration Plan

## Purpose
Adopt `pi` as Shuttle's primary agent runtime integration while preserving Shuttle's product boundary and enabling future runtime adapters (Codex SDK/CLI SDK bridge, Claude Agent SDK, OpenCode SDK, and others) without controller churn.

This plan assumes ongoing pane/context hardening continues in parallel and does not block architecture decisions here.

---

## 1) Decision and Constraints

### Decision
- Use `pi` as the default runtime adapter for coding-agent behavior.
- Plan and ship a first-class Codex SDK/CLI-SDK adapter in the same runtime framework.
- Keep Shuttle as the system of record for shell execution, approvals, transcript, and patch application.
- Treat any external runtime as a replaceable dependency via adapter interfaces and a runtime registry.

### Non-goals
- Do not build a custom agent loop in Shuttle.
- Do not let `pi` (or any runtime) directly mutate workspace state without Shuttle-managed approval/apply.
- Do not require every runtime to expose identical capabilities.

### Hard constraints
- Controller interfaces remain Shuttle-owned and stable.
- Runtime adapter boundaries remain narrow and testable.
- Provider credentials are never copied from one runtime into another runtime's local stores.
- Inference provider/model configuration remains a separate concern from runtime selection (`provider+model` are passed into runtime sessions; runtimes are not treated as providers).

---

## 2) Target Architecture

## 2.1 Layering
1. **TUI + Controller (existing Shuttle ownership)**
   - Input collection, transcript rendering, approvals, execution decisions.
2. **Runtime Orchestration Layer (new, Shuttle-owned)**
   - Runtime registry, capability negotiation, version policy checks, lifecycle management.
3. **Runtime Adapter Layer (new)**
   - `pi` adapter now; Codex SDK/CLI SDK adapter next; Claude/OpenCode adapters later.
4. **External Runtime SDK/Process**
   - `pi` SDK/runtime.

## 2.2 Core Interfaces

```go
type RuntimeAdapter interface {
    ID() string
    DisplayName() string
    Capabilities(ctx context.Context) (RuntimeCapabilities, error)
    HealthCheck(ctx context.Context) error
    NewSession(ctx context.Context, req NewSessionRequest) (RuntimeSession, error)
}

type RuntimeSession interface {
    SendTurn(ctx context.Context, input AgentInput) (AgentResponse, error)
    Close(ctx context.Context) error
}
```

Keep `controller.Agent` as a compatibility facade by backing it with a selected `RuntimeSession`.

## 2.3 Capability Model

```go
type RuntimeCapabilities struct {
    SupportsStreamingEvents bool
    SupportsCommandProposal bool
    SupportsPatchProposal   bool
    SupportsToolExecution   bool
    SupportsWorkingDir      bool
    SupportsApprovalHints   bool
    SupportsResume          bool
    AuthModes               []AuthMode
    Version                 string
}
```

Capabilities drive UI and policy gates; missing capability means graceful fallback, not failure.

## 2.4 Runtime Registry

Implement `internal/runtime/registry` with:
- registration by ID (`pi`, `codex_sdk`, `claude_sdk`, `opencode`)
- dependency metadata (sdk version, min/max compatible runtime versions)
- feature flags (`experimental`, `default_enabled`)

This prevents switch-statement sprawl and enables adding adapters without cross-cutting edits.

## 2.5 Provider/Model vs Runtime Separation
- Keep provider configuration canonical and independent:
  - `provider`
  - `model`
  - `base_url` / auth / endpoint options
- Keep runtime configuration separate and optional:
  - `runtime_id` (`builtin`, `pi`, `codex_sdk`, etc.)
  - runtime-local options (command path, bridge mode, diagnostics flags)
- Runtime manager always receives resolved provider/model settings as input so runtime choice never rewrites inference identity.
- Add an explicit compatibility matrix for runtime × provider families (e.g., Codex runtime may reject incompatible non-Codex backends; `pi` may support broader provider sets).

---

## 3) Pi Adapter Design

## 3.1 Adapter responsibilities
- Translate Shuttle `AgentInput` to `pi` turn/session API.
- Map `pi` events/results into Shuttle `AgentResponse`.
- Convert runtime actions into Shuttle proposals/approvals (command/patch) rather than direct execution.
- Normalize runtime errors into typed categories:
  - auth
  - transport
  - timeout
  - policy violation
  - incompatible version

## 3.2 Session model
- One Shuttle task maps to one logical runtime session.
- Session handle persisted in Shuttle runtime state for resume/recovery.
- On crash/restart, attempt session resume if runtime supports it; otherwise reopen with compressed context.

## 3.3 Context policy
- Explicit context envelope from Shuttle:
  - task summary
  - compact transcript window
  - recent shell output summary
  - pending approvals and latest command result
- Never pass raw full terminal history by default.
- Add bounded token/size budgets per context segment.

## 3.4 Approval boundary
- `pi` can propose command/patch/tool actions.
- Shuttle decides:
  - whether approval is required
  - how risk is labeled
  - whether execution/apply proceeds

---

## 4) Extension Mechanism for Future SDKs

## 4.1 Contract stability strategy
- Freeze `RuntimeAdapter` and `RuntimeSession` interfaces behind a package-level version (`runtimeapi/v1`).
- New optional behavior appears through capabilities, not interface breakage.
- Breaking changes require `runtimeapi/v2` and dual-stack transition period.

## 4.2 Adapter packaging
- `internal/runtime/adapters/pi`
- `internal/runtime/adapters/codexsdk` (planned in this rollout)
- `internal/runtime/adapters/claudesdk` (future)
- `internal/runtime/adapters/opencode` (future)

Each adapter owns:
- mapping logic
- auth wiring
- health checks
- compatibility policy
- adapter-specific tests

## 4.3 Version management
- Pin SDK dependencies exactly in lockfiles.
- Maintain an internal compatibility matrix doc:
  - Shuttle version
  - adapter version
  - upstream runtime SDK/runtime versions
- Enforce startup compatibility check:
  - hard fail on known-incompatible versions
  - warning on unknown-new versions with opt-in override

## 4.4 Runtime selection policy
Priority for automatic selection:
1. user-pinned runtime
2. previously healthy runtime for workspace/task
3. default runtime (`pi`)
4. fallback candidates by capability match

---

## 5) Implementation Plan (Phased)

## Phase 0: Scaffolding (1-2 days)
- Add `internal/runtime` packages:
  - `runtimeapi`
  - `registry`
  - `manager`
  - `errors`
- Add config fields:
  - selected runtime ID
  - runtime-specific options blob
  - compatibility override flag
- Add no-op/mock runtime adapter for contract tests.

**Exit criteria**
- Controller can resolve runtime through manager and call through existing `Agent` facade.

## Phase 1: Pi adapter MVP (2-4 days)
- Implement `pi` adapter with:
  - health check
  - session create/send/close
  - response/proposal mapping
- Wire onboarding/settings runtime picker for `pi`.
- Add structured logs for runtime decisions (non-sensitive).

**Exit criteria**
- End-to-end task turn works via `pi` with command proposals and plain message responses.

## Phase 2: Reliability and resume (2-3 days)
- Add session persistence + resume flow.
- Add timeouts/cancellation controls.
- Add retry strategy for transient transport errors.
- Add fallback path when runtime resume is unsupported.

**Exit criteria**
- Restart and reconnection behavior is deterministic and user-visible.

## Phase 3: Capability-gated UX (2-3 days)
- Render runtime capability status in settings.
- Gate features (patch proposal, streaming plan steps, etc.) by capability flags.
- Add user-facing diagnostics for unsupported features.

**Exit criteria**
- No runtime-specific behavior leaks into controller logic without capability checks.

## Phase 4: Version/compatibility hardening (1-2 days)
- Enforce adapter/runtime compatibility matrix checks at startup.
- Add command to print compatibility report.
- Add upgrade playbook docs for runtime version bumps.

**Exit criteria**
- Runtime version drift fails safely with clear remediation.

## Phase 5: Codex SDK/CLI-SDK adapter implementation (2-4 days)
- Implement `codexsdk` adapter on top of the same runtime contracts.
- Add Codex-specific health checks and auth-mode detection:
  - delegated local login path (CLI-managed auth)
  - API-key path where supported by installed tooling
- Add adapter compatibility gates for Codex SDK/CLI versions.
- Validate controller requires no changes beyond runtime selection/config.

**Exit criteria**
- Codex adapter passes conformance + integration suites using only adapter-layer changes.

## Phase 6: Additional adapter as proof of repeated extensibility (optional, 2-4 days)
- Implement one additional adapter (OpenCode or Claude SDK).
- Validate no controller changes required.

**Exit criteria**
- Third runtime passes same conformance suite using only adapter-layer changes.

---

## 6) Separate Subagent Evaluation: Stability

Assume a "Stability Reviewer" subagent with only architecture + failure-mode context.

### Findings
- Primary risk is dependency/version drift at runtime boundary.
- Second risk is session lifecycle ambiguity after crashes.
- Third risk is silent capability mismatch across adapters.

### Refinements applied
- Added compatibility matrix + startup version gate (Phase 4).
- Added deterministic resume/fallback rules (Phase 2).
- Added explicit capability-gated UX and behavior checks (Phase 3).
- Added typed runtime error taxonomy in adapter contract.

### Stability score after refinements
- **Before refinements:** Medium
- **After refinements:** High for single-runtime deployment; Medium-High for multi-runtime deployments

---

## 7) Separate Subagent Evaluation: Usability

Assume a "Usability Reviewer" subagent with only UX/onboarding context.

### Findings
- Users need clear runtime selection and health status, or they cannot debug failures.
- Capability differences must be visible or users perceive bugs.
- Resume/reconnect status must be explicit in transcript/system messages.

### Refinements applied
- Runtime picker in settings/onboarding with default + rationale.
- Capability panel in provider/runtime settings.
- User-facing diagnostics for unsupported features and compatibility failures.
- Explicit session state messages (`connected`, `resumed`, `restarted`).

### Usability score after refinements
- **Before refinements:** Medium
- **After refinements:** High

---

## 8) Separate Subagent Evaluation: Security

Assume a "Security Reviewer" subagent with prompt-injection/sandbox focus.

### Threat model focus
1. Prompt injection via shell output or repository content.
2. Tool/policy escalation from runtime-proposed actions.
3. Sandbox escape attempts through suggested commands.
4. Secret exfiltration through runtime context or traces.
5. Cross-session context bleed between tasks.

### Required controls
- **Context minimization + labeling**
  - Tag untrusted shell/repo text as untrusted observations.
  - Include policy preamble forbidding instruction override from untrusted content.
- **Approval and execution policy firewall**
  - Runtime never executes directly; Shuttle approval required for risky actions.
  - High-risk command classes always require explicit approval.
- **Command risk classifier (pre-exec)**
  - Detect network egress, destructive fs ops, credential access paths, privilege escalation.
- **Secret redaction and isolation**
  - Redact secrets in transcript/trace/provider payload snapshots.
  - Keep credential references out of runtime prompt context.
- **Session isolation**
  - Per-task runtime session IDs and context budgets.
  - No automatic transcript sharing across tasks unless user-approved.
- **Auditability**
  - Persist decision log: proposed action, risk score, approval outcome, executor path.

### Security refinements applied to plan
- Added explicit approval boundary in architecture section.
- Added context-envelope policy with bounded budgets.
- Added typed policy-violation runtime errors.
- Added mandatory risk-gating rules before execution/apply.

### Security score after refinements
- **Before refinements:** Medium-Low
- **After refinements:** Medium-High (pending classifier quality and redaction testing)

---

## 9) Testing and Validation Plan

## 9.1 Contract tests
- Shared conformance tests for any adapter:
  - health check behavior
  - session lifecycle
  - capability reporting
  - error category mapping

## 9.2 Integration tests
- Pi adapter happy path and key failure paths:
  - unauthenticated
  - incompatible runtime version
  - timeout
  - malformed response/event

## 9.3 Security tests
- Prompt-injection simulation fixtures in shell output.
- Command risk classifier test corpus.
- Redaction tests for traces/transcripts/runtime payload snapshots.

## 9.4 Manual validation checklist
- Fresh launch with `pi` default.
- Runtime switch and fallback behavior.
- Restart + resume behavior.
- Approval flow for risky command/patch proposals.

---

## 10) Deliverables

1. Runtime API + registry + manager scaffolding.
2. Pi adapter MVP + tests.
3. Capability-aware settings/onboarding UX.
4. Compatibility matrix + startup gate.
5. Codex SDK/CLI-SDK adapter + tests.
6. Security policy enforcement hooks for proposal/approval boundary.
7. Documentation updates for runtime selection, compatibility, and troubleshooting.

---

## 11) Rollout and Operations

- Start with `pi` as default runtime behind a feature flag for one release cycle.
- Collect operational metrics:
  - runtime health check failures
  - session resume success rate
  - approval rejection rate by risk class
  - adapter error distribution
- Promote `pi` from feature-flagged to default-on when error and resume metrics meet threshold.

---

## 12) Open Questions

1. What exact `pi` SDK/runtime versions will Shuttle pin initially?
2. What exact Codex SDK/CLI-SDK versions and auth modes will Shuttle support first?
3. Should compatibility overrides be hidden behind an explicit "unsafe" flag?
4. What minimum capability set is required for a runtime to be selectable in onboarding?
5. Should cross-runtime transcript normalization include a runtime-specific diagnostics panel in transcript entries?

---

## 13) Codex SDK/CLI-SDK Adapter Plan (Detailed)

This section makes the Codex path explicit so it can be implemented in parallel with or immediately after `pi`.

## 13.1 Adapter scope
- Implement `internal/runtime/adapters/codexsdk`.
- Support two operation modes behind one adapter:
  - **SDK mode** when stable programmatic SDK APIs are available.
  - **CLI-SDK bridge mode** when the SDK delegates to local CLI runtime behaviors.
- Keep mode selection internal to adapter; expose only capabilities + diagnostics upward.

## 13.2 Auth and credential behavior
- Primary: delegated local Codex login reuse (no token copying into Shuttle config).
- Secondary: API-key mode where Codex tooling supports noninteractive API-key auth.
- Persist only auth-source metadata and health status in Shuttle state.
- Require explicit user-facing diagnostics when login exists but is incompatible with requested mode.

## 13.3 Session and context model
- Match `pi` integration behavior:
  - one Shuttle task maps to one Codex runtime session
  - resumable when supported; compact-context reopen when not
  - bounded context envelope with untrusted-content labeling

## 13.4 Compatibility and upgrade policy
- Track Codex adapter compatibility separately in matrix:
  - Shuttle version
  - codex adapter version
  - supported Codex SDK version range
  - supported CLI bridge version range
- On startup:
  - fail fast for known incompatible versions
  - warn and require explicit override for unknown-new versions

## 13.5 Test plan additions for Codex adapter
- Contract conformance suite (shared).
- Integration suite:
  - logged-in delegated auth happy path
  - not-logged-in failure path with actionable remediation
  - API-key mode happy path (if supported)
  - timeout/cancellation path
  - malformed event/response mapping path
- Security checks:
  - proposal/approval boundary enforcement
  - prompt-injection resilience in context envelope inputs

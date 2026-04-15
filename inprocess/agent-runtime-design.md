# Shuttle Agent Runtime Design

## Purpose
Define how Shuttle should integrate with an agent runtime without turning the product itself into an agent framework.

Current implementation note:
- the first extraction seam now lives in `internal/agentruntime`
- Shuttle still owns execution, approvals, patch application, transcript mutation, and all session/task state changes
- the built-in runtime currently handles request-kind orchestration, inspect-context recursion, patch-payload normalization, invalid-patch repair retry, and structured-edit synthesis through a controller-owned host
- the controller no longer stores a direct provider/model agent dependency; provider-backed response generation now sits behind the runtime host adapter
- Shuttle now resolves `builtin`, `auto`, `pi`, `codex_sdk`, and `codex_app_server` selections consistently, and the active runtime can be changed from the TUI settings flow and persisted across launches
- runtime selection is now session-authoritative for agent reasoning: the controller still constructs every turn, but it sends each agent decision turn back to the selected runtime
- `codex_sdk` is the first non-builtin authoritative runtime path; the current phase uses the local `codex` CLI executable plus a codex-specific turn handler to validate Shuttle turn semantics and operator UX while still reusing Shuttle-owned orchestration helpers and host callbacks
- `pi` remains detectable but is rejected for authoritative selection until it reaches full parity across the required request kinds
- interchangeable fully delegated external runtimes remain follow-on work after this seam stabilizes
- explicit dual-runtime delegation or builtin-driven escalation is not part of the current seam and is now tracked separately in [P5](P5.md)

This document is the implementation-facing design note for Milestone 4 and Milestone 5:
- Milestone 4: mock provider and controller-driven approval flow
- Milestone 5: real provider or framework integration

Detailed provider/auth decomposition lives in [provider-integration-design.md](provider-integration-design.md), and the current implementation priorities live in [../BACKLOG.md](../BACKLOG.md).

---

# 1. Core Position

Shuttle is not the agent framework.

Shuttle is responsible for:
- tmux workspace control
- shell observation
- tracked command execution
- approvals
- transcript rendering
- TUI interaction
- local task and session state

An agent runtime is responsible for:
- model requests and responses
- conversation state formatting
- tool-call planning
- optional tool-call execution contracts
- structured response generation

ACP or similar protocols are optional tool wiring standards, not the core product architecture.

This keeps the product boundary clean:
- Shuttle owns shell reality and UX
- the runtime owns model-facing reasoning and tool intent

---

# 2. Architectural Placement

## 2.1 Layers

1. TUI layer
   Collects user intent and renders transcript events, approvals, plans, and results.

2. Controller layer
   Owns orchestration across TUI, shell observation, tracked command execution, approvals, and task state.

3. Agent runtime layer
   Produces structured agent responses from normalized task input.

4. Provider or framework integration layer
   Talks to a concrete backend:
   - mock runtime
   - direct API integration
   - a framework such as `pi`
   - later, an ACP-backed tool runtime if needed

## 2.1.1 Current Runtime Selection Status

Current shipped behavior:
- `builtin` uses the built-in runtime directly
- `F10 -> Runtime` now acts as the user-facing runtime selector and persists the selected runtime type plus current command path unless startup flags explicitly overrode them; the settings view also previews selected versus effective runtime resolution and current runtime-command health before applying the change
- `auto` prefers the installed runtime with the best declared authoritative parity, then falls back to `builtin` when none are available
- explicit `codex_sdk` selection enables the first authoritative secondary runtime path
- explicit `codex_app_server` selection now uses a native Codex App Server client over stdio JSON-RPC. Shuttle keeps a long-lived app-server process alive for the runtime session, reuses in-memory native thread bindings per task across continuation turns, and routes compaction through the same native thread
- explicit `pi` selection is rejected until `pi` can own the full required request-kind set
- controller ownership does not change when an external runtime is selected; Shuttle still owns execution, approvals, patch validation/application, transcript updates, and session/task state

This means the selection seam is live and authoritative, but product ownership still stays inside Shuttle. The current `codex_sdk` implementation remains the primary CLI-backed bridge. `codex_app_server` now exists as a separate runtime that uses the real app-server transport for turn handling while Shuttle still owns state, approvals, shell execution, transcript mutation, patch validation, and patch apply. The next step is hardening stale-thread/process recovery and reconnect behavior around that long-lived app-server session. Hybrid builtin-to-secondary delegation is intentionally out of scope for this model and is tracked in [P5](P5.md).

## 2.1.2 Shipped Runtime Workflow

Today the runtime seam is best understood as a strict workflow boundary with session-authoritative runtime selection, not a transfer of product ownership:

1. The controller builds a product-owned `agentruntime.Request`.
2. The selected runtime remains the reasoning owner for every agent turn in that session/task.
3. The runtime calls back into a controller-owned `agentruntime.Host` for every privileged action.
4. The host is the only layer allowed to:
   - refresh tracked shell and local host context
   - read or mutate task/session state
   - validate patches
   - synthesize structured edit proposals from Shuttle state
   - invoke the underlying provider/model agent
5. The runtime returns a normalized `agentruntime.Outcome`.
6. The controller translates that outcome into transcript events, approval state, auto-run/auto-apply behavior, and any subsequent shell or patch action.

Host callbacks are not fallback. A real runtime fallback would mean the selected runtime can no longer remain the reasoning owner at all. Shuttle does not silently hand ordinary continuation turns to builtin in that case; it stops and requires an explicit retry or runtime switch. Shuttle remains the sole owner of shell reality, approvals, execution, and persistence.

## 2.2 Design Rule

The controller should only depend on a narrow `Agent` interface.

It should not depend on:
- a specific SDK's conversation objects
- framework-specific callback types
- raw provider response formats
- ACP message shapes

Those details belong in adapters behind the `Agent` interface.

---

# 3. Controller and Agent Interface

## 2.3 External Runtime Contract

For the current seam, an external runtime adapter is allowed to own:
- request-kind specific prompting/orchestration strategy
- model-facing response normalization rules
- adapter metadata such as runtime type, selected command path, provider preset, and requested model

It is not allowed to own:
- tmux interaction
- shell reads or shell writes
- approval gating decisions
- patch application
- transcript mutation
- task/session persistence
- command execution lifecycle

If a future runtime needs broader authority, that should be introduced as a new host capability contract explicitly, not by letting an adapter reach around the controller.

## 3.1 Agent Interface

The first usable contract should stay narrow:

```go
type Agent interface {
    Respond(ctx context.Context, input AgentInput) (AgentResponse, error)
}
```

This is sufficient for:
- a mock implementation in Milestone 4
- a direct provider implementation in Milestone 5
- a framework-backed implementation later

## 3.2 Agent Input

The controller should normalize shell and task state into a product-owned input shape.

```go
type AgentInput struct {
    Session SessionContext
    Task    TaskContext
    Prompt  string
}

type SessionContext struct {
    SessionName          string
    BottomPaneID         string
    TrackedShell         TrackedShellTarget
    WorkingDirectory     string
    LocalWorkingDirectory string
    LocalHomeDirectory   string
    LocalUsername        string
    LocalHostname        string
    LocalWorkspaceRoot   string
    UserShellHistoryFile string
    RecentShellOutput    string
    RecentManualCommands []string
    RecentManualActions  []string
    ApprovalMode         ApprovalMode
    CurrentShell         *shell.PromptContext
}

type TaskContext struct {
    TaskID             string
    PriorTranscript    []TranscriptEvent
    PendingApproval    *ApprovalRequest
    LastCommandResult  *CommandResultSummary
    ActivePlan         *ActivePlan
    PrimaryExecutionID string
    ExecutionRegistry  []CommandExecution
}
```

Notes:
- `RecentShellOutput` should come from Shuttle's observed shell buffer, not from the runtime.
- `PriorTranscript` should be a compact structured history, not a raw terminal dump.
- `PendingApproval` allows the runtime to continue a refine or approval flow coherently.
- `RecentManualCommands` and `RecentManualActions` let the runtime resolve prompts like "see the file I just renamed".
- `ApprovalMode` is Shuttle-owned session policy; it informs the runtime, but the controller remains authoritative about whether a command can auto-run or must stay gated.
- `TrackedShell` and `CurrentShell` describe the persistent user shell context; approved agent commands may still run in owned execution panes tracked separately in `ExecutionRegistry`.

## 3.2.1 Runtime Request and Host Boundary

The shipped `agentruntime` seam is slightly richer than the original `Agent` sketch. The effective contract is now:

```go
type Runtime interface {
    Handle(ctx context.Context, host Host, req Request) (Outcome, error)
}

type Host interface {
    Respond(ctx context.Context, req Request) (Outcome, error)
    InspectContext(ctx context.Context, req Request) error
    SynthesizeStructuredEdit(ctx context.Context, outcome Outcome) (Outcome, error)
    ValidatePatch(ctx context.Context, patch string, target string) error
}
```

Interpretation:
- `Runtime` owns turn-level policy.
- `Host` owns every controller-privileged operation.
- `Respond` is the only path that reaches the underlying provider/model agent.
- `InspectContext`, `SynthesizeStructuredEdit`, and `ValidatePatch` are controller-owned side channels that keep shell, patch, and edit semantics inside Shuttle.

This is the contract external runtimes should target in P1. They should not expect direct access to tmux panes, controller internals, or mutable transcript state.

## 3.3 Agent Response

The runtime should return a single structured response per turn.

```go
type AgentResponse struct {
    Message   string
    Plan      *Plan
    Proposal  *Proposal
    Approval  *ApprovalRequest
}

type Plan struct {
    Summary string
    Steps   []string
}

type Proposal struct {
    Kind        ProposalKind
    Command     string
    Patch       string
    Description string
}

type ProposalKind string

const (
    ProposalAnswer  ProposalKind = "answer"
    ProposalCommand ProposalKind = "command"
    ProposalPatch   ProposalKind = "patch"
)
```

Rules:
- not every field should be populated on every response
- the controller decides how the response is rendered into transcript events
- the controller decides whether a proposal must become an approval card before execution
- a `ProposalPatch` is not applied workspace state by itself; it is only a proposed change until Shuttle runs an explicit patch-application flow
- the runtime must not describe patch-created files as present or runnable until the controller confirms the patch was applied successfully

---

# 4. Approval Model

Approvals are a first-class Shuttle concept and should remain product-owned even if the runtime suggests them.

## 4.1 Approval Request

```go
type ApprovalRequest struct {
    ID          string
    Kind        ApprovalKind
    Title       string
    Summary     string
    Command     string
    Patch       string
    Risk        RiskLevel
}

type ApprovalKind string

const (
    ApprovalCommand ApprovalKind = "command"
    ApprovalPatch   ApprovalKind = "patch"
    ApprovalPlan    ApprovalKind = "plan"
)

type RiskLevel string

const (
    RiskLow    RiskLevel = "low"
    RiskMedium RiskLevel = "medium"
    RiskHigh   RiskLevel = "high"
)

type ApprovalMode string

const (
    ApprovalModeConfirm ApprovalMode = "confirm"
    ApprovalModeAuto    ApprovalMode = "auto"
    ApprovalModeDanger  ApprovalMode = "dangerous"
)
```

## 4.2 Approval Actions

The controller should translate user actions into product-owned decisions:

```go
type ApprovalDecision string

const (
    DecisionApprove ApprovalDecision = "approve"
    DecisionReject  ApprovalDecision = "reject"
    DecisionRefine  ApprovalDecision = "refine"
)
```

The runtime may be informed of those decisions in later turns, but Shuttle owns:
- when the card appears
- whether approval is required
- what execution path approval unlocks
- whether a low-risk local command may auto-run under the active session policy
- whether a session has explicitly entered a dangerous auto-run mode; the runtime may see that policy in context, but the controller is still the enforcement point

---

# 5. Transcript Event Model

The transcript should be driven by structured events, not ad hoc strings.

## 5.1 Base Event

```go
type TranscriptEvent struct {
    ID        string
    Kind      TranscriptEventKind
    Timestamp time.Time
    Payload   any
}

type TranscriptEventKind string

const (
    EventUserMessage    TranscriptEventKind = "user_message"
    EventAgentMessage   TranscriptEventKind = "agent_message"
    EventPlan           TranscriptEventKind = "plan"
    EventProposal       TranscriptEventKind = "proposal"
    EventApproval       TranscriptEventKind = "approval"
    EventCommandStart   TranscriptEventKind = "command_start"
    EventCommandResult  TranscriptEventKind = "command_result"
    EventSystemNotice   TranscriptEventKind = "system_notice"
    EventError          TranscriptEventKind = "error"
)
```

## 5.2 Why This Matters

This keeps the TUI simple:
- it renders known event kinds
- it does not need direct knowledge of provider response formats
- it does not need to inspect shell internals to decide presentation

This also keeps persistence and replay sane later.

---

# 6. Controller Responsibilities in Milestone 4

Milestone 4 should introduce a small controller layer between the TUI and the shell observer.

## 6.1 TUI Should Stop Doing This Directly

The TUI should not:
- call the runtime directly
- decide whether approval is required
- decide whether a proposal becomes a shell command
- own task state transitions

## 6.2 Controller Should Do This

The controller should:
- accept user input events
- build `AgentInput`
- call `Agent.Respond`
- map `AgentResponse` into transcript events
- create approval state when required
- execute approved shell commands through the tracked-command path
- feed command results back into transcript and task state

## 6.3 Narrow Controller Surface

The first controller surface can stay small:

```go
type Controller interface {
    SubmitAgentPrompt(ctx context.Context, prompt string) ([]TranscriptEvent, error)
    SubmitShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error)
    DecideApproval(ctx context.Context, approvalID string, decision ApprovalDecision, refineText string) ([]TranscriptEvent, error)
}
```

The return type is event-oriented on purpose. The TUI should consume events, not business logic.

---

# 7. Mock Runtime Plan for Milestone 4

The Milestone 4 runtime should be fake but structured.

## 7.1 Mock Behavior

It should return canned responses such as:
- a plain explanatory answer
- a shell command proposal
- a short plan
- an approval request

Example mappings:
- input contains `list files`
  return a command proposal for `ls -lah`
- input contains `show plan`
  return a 3-step plan
- input contains `delete` or `remove`
  return a high-risk approval request

## 7.2 Why Mock First

This lets us debug:
- task state
- transcript mapping
- approval behavior
- command execution handoff

without mixing in:
- model prompt quality
- API auth failures
- provider latency
- framework-specific bugs

---

# 8. Real Runtime Integration Plan

Milestone 5 should swap the mock runtime for a real implementation without changing the controller contract.

## 8.1 Acceptable Real Backends

Any of these can sit behind the `Agent` interface:
- direct OpenAI API client
- an internal provider adapter
- a framework such as `pi`

## 8.2 Adapter Rule

If a framework is adopted, create an adapter:

```go
type PiAgent struct {
    // framework-specific fields
}

func (a *PiAgent) Respond(ctx context.Context, input AgentInput) (AgentResponse, error) {
    // translate Shuttle input -> framework input
    // call framework
    // translate framework output -> Shuttle response
}
```

Do not leak framework-native objects above that boundary.

---

# 9. Where ACP Fits

ACP is relevant if the runtime wants a standard way to call tools.

## 9.1 Good Future Uses

ACP may become useful when Shuttle exposes structured tools such as:
- run tracked shell command
- view command result
- inspect session state
- inspect diff
- request approval

## 9.2 What ACP Should Not Do

ACP should not define:
- the TUI state model
- the transcript model
- tmux session control
- approval policy
- the product's execution rules

Those remain Shuttle-owned.

## 9.3 Recommendation

Do not make ACP a Milestone 4 dependency.

If adopted later:
- place ACP behind the `Agent` or tool adapter boundary
- keep the Shuttle controller consuming only product-owned response types

---

# 10. Recommended Implementation Sequence

1. Add controller-owned transcript events and task state.
2. Add the `Agent` interface and a mock implementation.
3. Move Agent-mode handling out of the TUI and into the controller.
4. Add approval cards and approval decisions as controller-owned state.
5. Wire approved shell proposals to the tracked-command path.
6. Replace the mock runtime with a real provider or framework adapter.
7. Revisit ACP only after the tool surface is stable.

---

# 11. Practical Next Step

The next coding milestone should implement:
- `internal/controller`
- `internal/provider/mock.go`
- product-owned transcript event types
- approval request state
- Agent mode backed by the mock runtime instead of the current placeholder

That is enough to prove the product flow before choosing a real agent framework.

## Related UX Follow-Ups

- The TUI should gain app-owned Up and Down arrow history cycling for Agent mode and Shell mode separately.
- That history should remain distinct from the shell's own interactive history.
- Shell-history pollution should be treated as a shell-integration concern owned by Shuttle's execution path, not by the agent runtime boundary.

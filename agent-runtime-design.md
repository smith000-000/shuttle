# Shuttle Agent Runtime Design

## Purpose
Define how Shuttle should integrate with an agent runtime without turning the product itself into an agent framework.

This document is the implementation-facing design note for Milestone 4 and Milestone 5:
- Milestone 4: mock provider and controller-driven approval flow
- Milestone 5: real provider or framework integration

Detailed provider/auth decomposition and rollout sequencing live in [provider-integration-design.md](provider-integration-design.md) and [provider-integration-plan.md](provider-integration-plan.md).

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
    SessionName         string
    BottomPaneID        string
    TrackedShell        TrackedShellTarget
    WorkingDirectory    string
    RecentShellOutput   string
    RecentManualActions []string
}

type TaskContext struct {
    TaskID             string
    PriorTranscript    []TranscriptEvent
    PendingApproval    *ApprovalRequest
    LastCommandResult  *CommandResultSummary
}
```

Notes:
- `RecentShellOutput` should come from Shuttle's observed shell buffer, not from the runtime.
- `PriorTranscript` should be a compact structured history, not a raw terminal dump.
- `PendingApproval` allows the runtime to continue a refine or approval flow coherently.

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

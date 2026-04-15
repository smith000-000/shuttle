package controller

import (
	"context"
	"time"

	"aiterm/internal/agentruntime"
	"aiterm/internal/shell"
)

type Agent = agentruntime.ModelAgent
type AgentInput = agentruntime.AgentInput
type TrackedShellTarget = agentruntime.TrackedShellTarget
type SessionContext = agentruntime.SessionContext
type TaskContext = agentruntime.TaskContext
type AgentResponse = agentruntime.AgentResponse
type AgentModelInfo = agentruntime.ModelInfo
type Plan = agentruntime.Plan
type PlanStepStatus = agentruntime.PlanStepStatus
type PlanStep = agentruntime.PlanStep
type ActivePlan = agentruntime.ActivePlan
type Proposal = agentruntime.Proposal
type ProposalKind = agentruntime.ProposalKind
type EditOperation = agentruntime.EditOperation
type EditIntent = agentruntime.EditIntent
type ApprovalRequest = agentruntime.ApprovalRequest
type PatchTarget = agentruntime.PatchTarget
type ApprovalKind = agentruntime.ApprovalKind
type RiskLevel = agentruntime.RiskLevel
type ApprovalMode = agentruntime.ApprovalMode
type TranscriptEvent = agentruntime.TranscriptEvent
type TranscriptEventKind = agentruntime.TranscriptEventKind
type PatchApplyFile = agentruntime.PatchApplyFile
type PatchApplySummary = agentruntime.PatchApplySummary
type PatchTransport = agentruntime.PatchTransport
type RemoteCapabilitySummary = agentruntime.RemoteCapabilitySummary
type CommandOrigin = agentruntime.CommandOrigin
type CommandExecutionState = agentruntime.CommandExecutionState
type CommandOwnershipMode = agentruntime.CommandOwnershipMode
type CommandExecution = agentruntime.CommandExecution
type CommandResultSummary = agentruntime.CommandResultSummary

const (
	PlanStepPending    = agentruntime.PlanStepPending
	PlanStepInProgress = agentruntime.PlanStepInProgress
	PlanStepDone       = agentruntime.PlanStepDone

	ProposalAnswer         = agentruntime.ProposalAnswer
	ProposalCommand        = agentruntime.ProposalCommand
	ProposalKeys           = agentruntime.ProposalKeys
	ProposalPatch          = agentruntime.ProposalPatch
	ProposalEdit           = agentruntime.ProposalEdit
	ProposalInspectContext = agentruntime.ProposalInspectContext

	EditInsertBefore = agentruntime.EditInsertBefore
	EditInsertAfter  = agentruntime.EditInsertAfter
	EditReplaceExact = agentruntime.EditReplaceExact
	EditReplaceRange = agentruntime.EditReplaceRange

	PatchTargetLocalWorkspace = agentruntime.PatchTargetLocalWorkspace
	PatchTargetRemoteShell    = agentruntime.PatchTargetRemoteShell

	ApprovalCommand = agentruntime.ApprovalCommand
	ApprovalPatch   = agentruntime.ApprovalPatch
	ApprovalPlan    = agentruntime.ApprovalPlan

	RiskLow    = agentruntime.RiskLow
	RiskMedium = agentruntime.RiskMedium
	RiskHigh   = agentruntime.RiskHigh

	ApprovalModeConfirm = agentruntime.ApprovalModeConfirm
	ApprovalModeAuto    = agentruntime.ApprovalModeAuto
	ApprovalModeDanger  = agentruntime.ApprovalModeDanger

	EventUserMessage      = agentruntime.EventUserMessage
	EventAgentMessage     = agentruntime.EventAgentMessage
	EventPlan             = agentruntime.EventPlan
	EventProposal         = agentruntime.EventProposal
	EventApproval         = agentruntime.EventApproval
	EventCommandStart     = agentruntime.EventCommandStart
	EventCommandResult    = agentruntime.EventCommandResult
	EventPatchApplyResult = agentruntime.EventPatchApplyResult
	EventModelInfo        = agentruntime.EventModelInfo
	EventSystemNotice     = agentruntime.EventSystemNotice
	EventError            = agentruntime.EventError

	PatchTransportNone   = agentruntime.PatchTransportNone
	PatchTransportGit    = agentruntime.PatchTransportGit
	PatchTransportPython = agentruntime.PatchTransportPython
	PatchTransportShell  = agentruntime.PatchTransportShell

	CommandOriginUserShell     = agentruntime.CommandOriginUserShell
	CommandOriginAgentProposal = agentruntime.CommandOriginAgentProposal
	CommandOriginAgentApproval = agentruntime.CommandOriginAgentApproval
	CommandOriginAgentAuto     = agentruntime.CommandOriginAgentAuto
	CommandOriginAgentPlan     = agentruntime.CommandOriginAgentPlan

	CommandExecutionQueued                = agentruntime.CommandExecutionQueued
	CommandExecutionRunning               = agentruntime.CommandExecutionRunning
	CommandExecutionAwaitingInput         = agentruntime.CommandExecutionAwaitingInput
	CommandExecutionInteractiveFullscreen = agentruntime.CommandExecutionInteractiveFullscreen
	CommandExecutionHandoffActive         = agentruntime.CommandExecutionHandoffActive
	CommandExecutionBackgroundMonitor     = agentruntime.CommandExecutionBackgroundMonitor
	CommandExecutionCompleted             = agentruntime.CommandExecutionCompleted
	CommandExecutionFailed                = agentruntime.CommandExecutionFailed
	CommandExecutionCanceled              = agentruntime.CommandExecutionCanceled
	CommandExecutionLost                  = agentruntime.CommandExecutionLost

	CommandOwnershipExclusive      = agentruntime.CommandOwnershipExclusive
	CommandOwnershipSharedObserver = agentruntime.CommandOwnershipSharedObserver
	CommandOwnershipHandoff        = agentruntime.CommandOwnershipHandoff

	RuntimeBuiltin        = agentruntime.RuntimeBuiltin
	RuntimePi             = agentruntime.RuntimePi
	RuntimeCodexSDK       = agentruntime.RuntimeCodexSDK
	RuntimeCodexAppServer = agentruntime.RuntimeCodexAppServer
	RuntimeAuto           = agentruntime.RuntimeAuto
)

type ApprovalDecision string

const (
	DecisionApprove ApprovalDecision = "approve"
	DecisionReject  ApprovalDecision = "reject"
	DecisionRefine  ApprovalDecision = "refine"
)

type ContextWindowUsage struct {
	ApproxPromptTokens int
}

type ActiveExecutionOverview struct {
	ID                         string
	Command                    string
	Origin                     CommandOrigin
	State                      CommandExecutionState
	StartedAt                  time.Time
	UsesTrackedShell           bool
	ExecutionTakeControlTarget TrackedShellTarget
}

type ExecutionOverview struct {
	TrackedShell    TrackedShellTarget
	ActiveExecution *ActiveExecutionOverview
}

type Controller interface {
	SubmitAgentPrompt(ctx context.Context, prompt string) ([]TranscriptEvent, error)
	SubmitRefinement(ctx context.Context, approval ApprovalRequest, note string) ([]TranscriptEvent, error)
	SubmitProposalRefinement(ctx context.Context, proposal ProposalPayload, note string) ([]TranscriptEvent, error)
	ContinueActivePlan(ctx context.Context) ([]TranscriptEvent, error)
	ContinueAfterCommand(ctx context.Context) ([]TranscriptEvent, error)
	ContinueAfterPatchApply(ctx context.Context) ([]TranscriptEvent, error)
	CheckActiveExecution(ctx context.Context) ([]TranscriptEvent, error)
	ResumeAfterTakeControl(ctx context.Context) ([]TranscriptEvent, error)
	RefreshActiveExecution(ctx context.Context) ([]TranscriptEvent, *CommandExecution, error)
	SubmitShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error)
	SubmitProposedShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error)
	InspectProposedContext(ctx context.Context) ([]TranscriptEvent, error)
	ApplyProposedPatch(ctx context.Context, patch string, target PatchTarget) ([]TranscriptEvent, error)
	DecideApproval(ctx context.Context, approvalID string, decision ApprovalDecision, refineText string) ([]TranscriptEvent, error)
	SetApprovalMode(ctx context.Context, mode ApprovalMode) ([]TranscriptEvent, error)
	StartNewTask(ctx context.Context) ([]TranscriptEvent, error)
	CompactTask(ctx context.Context) ([]TranscriptEvent, error)
	RefreshShellContext(ctx context.Context) (*shell.PromptContext, error)
	PeekShellTail(ctx context.Context, lines int) (string, error)
	EstimateContextUsage(prompt string) ContextWindowUsage
	ApprovalMode() ApprovalMode
	ActiveExecution() *CommandExecution
	ExecutionOverview() ExecutionOverview
	AbandonActiveExecution(reason string) *CommandExecution
	TakeControlTarget() TrackedShellTarget
	TrackedShellTarget() TrackedShellTarget
}

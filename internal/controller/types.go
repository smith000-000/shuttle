package controller

import (
	"context"
	"strings"
	"time"

	"aiterm/internal/search"
	"aiterm/internal/shell"
)

type Agent interface {
	Respond(ctx context.Context, input AgentInput) (AgentResponse, error)
}

type AgentInput struct {
	Session                 SessionContext
	Task                    TaskContext
	Prompt                  string
	PreserveExternalSession bool
}

type TrackedShellTarget struct {
	SessionName string
	PaneID      string
}

type SessionContext struct {
	SessionName              string
	BottomPaneID             string
	TrackedShell             TrackedShellTarget
	WorkingDirectory         string
	LocalWorkspaceRoot       string
	PreferredExternalRuntime string
	ExternalRuntimeAvailable bool
	ExternalResumeAvailable  bool
	Search                   search.Availability
	PreferredExternalSearch  search.Availability
	UserShellHistoryFile     string
	RecentShellOutput        string
	RecentManualCommands     []string
	RecentManualActions      []string
	ApprovalMode             ApprovalMode
	CurrentShell             *shell.PromptContext
}

type TaskContext struct {
	TaskID               string
	CompactedSummary     string
	PriorTranscript      []TranscriptEvent
	PendingApproval      *ApprovalRequest
	PendingHandoff       *HandoffRequest
	LastCommandResult    *CommandResultSummary
	LastPatchApplyResult *PatchApplySummary
	ActivePlan           *ActivePlan
	PrimaryExecutionID   string
	ExecutionRegistry    []CommandExecution
	CurrentExecution     *CommandExecution
	RecoverySnapshot     string
}

type AgentResponse struct {
	Message       string
	Plan          *Plan
	Proposal      *Proposal
	Approval      *ApprovalRequest
	Handoff       *HandoffRequest
	ModelInfo     *AgentModelInfo
	RuntimeEvents []RuntimeEvent
}

type RuntimeEvent struct {
	Runtime string
	Kind    string
	Title   string
	Body    string
	Detail  string
}

type HandoffRequest struct {
	ID               string
	Title            string
	Summary          string
	Reason           string
	SuggestedRuntime string
	ResumeAvailable  bool
}

type ConversationOwner string

const (
	ConversationOwnerBuiltin  ConversationOwner = "builtin"
	ConversationOwnerExternal ConversationOwner = "external"
)

type ExternalState struct {
	PreferredRuntime string
	Owner            ConversationOwner
	HasHistory       bool
	Resumable        bool
	Available        bool
	Detail           string
	ConfirmationMode string
	Search           search.Availability
}

func ExternalConfirmationRequired(state ExternalState) bool {
	return strings.TrimSpace(strings.ToLower(state.ConfirmationMode)) != "off"
}

type RuntimeActivityItem struct {
	Key       string
	Timestamp time.Time
	Runtime   string
	Kind      string
	Title     string
	Body      string
	Detail    string
	Done      bool
	Replace   bool
}

type RuntimeActivitySnapshot struct {
	Owner     ConversationOwner
	Active    bool
	Runtime   string
	StartedAt time.Time
	UpdatedAt time.Time
	Items     []RuntimeActivityItem
}

type AgentModelInfo struct {
	ProviderPreset  string
	RequestedModel  string
	ResponseModel   string
	ResponseBaseURL string
}

type Plan struct {
	Summary string
	Steps   []string
}

type PlanStepStatus string

const (
	PlanStepPending    PlanStepStatus = "pending"
	PlanStepInProgress PlanStepStatus = "in_progress"
	PlanStepDone       PlanStepStatus = "done"
)

type PlanStep struct {
	Text   string
	Status PlanStepStatus
}

type ActivePlan struct {
	Summary string
	Steps   []PlanStep
}

type Proposal struct {
	Kind        ProposalKind
	Command     string
	Keys        string
	Patch       string
	Description string
}

type ProposalKind string

const (
	ProposalAnswer  ProposalKind = "answer"
	ProposalCommand ProposalKind = "command"
	ProposalKeys    ProposalKind = "keys"
	ProposalPatch   ProposalKind = "patch"
)

type ApprovalRequest struct {
	ID      string
	Kind    ApprovalKind
	Title   string
	Summary string
	Command string
	Patch   string
	Risk    RiskLevel
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

type ApprovalDecision string

const (
	DecisionApprove ApprovalDecision = "approve"
	DecisionReject  ApprovalDecision = "reject"
	DecisionRefine  ApprovalDecision = "refine"
)

type ApprovalMode string

const (
	ApprovalModeConfirm ApprovalMode = "confirm"
	ApprovalModeAuto    ApprovalMode = "auto"
	ApprovalModeDanger  ApprovalMode = "dangerous"
)

type TranscriptEvent struct {
	ID        string
	Kind      TranscriptEventKind
	Timestamp time.Time
	Payload   any
}

type TranscriptEventKind string

const (
	EventUserMessage      TranscriptEventKind = "user_message"
	EventAgentMessage     TranscriptEventKind = "agent_message"
	EventPlan             TranscriptEventKind = "plan"
	EventProposal         TranscriptEventKind = "proposal"
	EventApproval         TranscriptEventKind = "approval"
	EventHandoff          TranscriptEventKind = "handoff"
	EventCommandStart     TranscriptEventKind = "command_start"
	EventCommandResult    TranscriptEventKind = "command_result"
	EventPatchApplyResult TranscriptEventKind = "patch_apply_result"
	EventModelInfo        TranscriptEventKind = "model_info"
	EventSystemNotice     TranscriptEventKind = "system_notice"
	EventError            TranscriptEventKind = "error"
)

type PatchApplyFile struct {
	Operation string
	OldPath   string
	NewPath   string
}

type PatchApplySummary struct {
	WorkspaceRoot string
	Validation    string
	Applied       bool
	Created       int
	Updated       int
	Deleted       int
	Renamed       int
	Files         []PatchApplyFile
	Error         string
}

type CommandOrigin string

const (
	CommandOriginUserShell     CommandOrigin = "user_shell"
	CommandOriginAgentProposal CommandOrigin = "agent_proposal"
	CommandOriginAgentApproval CommandOrigin = "agent_approval"
	CommandOriginAgentAuto     CommandOrigin = "agent_auto"
	CommandOriginAgentPlan     CommandOrigin = "agent_plan"
)

type CommandExecutionState string

const (
	CommandExecutionQueued                CommandExecutionState = "queued"
	CommandExecutionRunning               CommandExecutionState = "running"
	CommandExecutionAwaitingInput         CommandExecutionState = "awaiting_input"
	CommandExecutionInteractiveFullscreen CommandExecutionState = "interactive_fullscreen"
	CommandExecutionHandoffActive         CommandExecutionState = "handoff_active"
	CommandExecutionBackgroundMonitor     CommandExecutionState = "background_monitoring"
	CommandExecutionCompleted             CommandExecutionState = "completed"
	CommandExecutionFailed                CommandExecutionState = "failed"
	CommandExecutionCanceled              CommandExecutionState = "canceled"
	CommandExecutionLost                  CommandExecutionState = "lost"
)

type CommandOwnershipMode string

const (
	CommandOwnershipExclusive      CommandOwnershipMode = "exclusive"
	CommandOwnershipSharedObserver CommandOwnershipMode = "shared_observer"
	CommandOwnershipHandoff        CommandOwnershipMode = "handoff"
)

type CommandExecution struct {
	ID                 string
	Command            string
	Origin             CommandOrigin
	TrackedShell       TrackedShellTarget
	OwnershipMode      CommandOwnershipMode
	State              CommandExecutionState
	StartedAt          time.Time
	CompletedAt        *time.Time
	ExitCode           *int
	LatestOutputTail   string
	ForegroundCommand  string
	SemanticShell      bool
	SemanticSource     string
	Error              string
	ShellContextBefore *shell.PromptContext
	ShellContextAfter  *shell.PromptContext
}

type CommandResultSummary struct {
	ExecutionID    string
	CommandID      string
	Command        string
	Origin         CommandOrigin
	State          CommandExecutionState
	Cause          shell.CompletionCause
	Confidence     shell.SignalConfidence
	SemanticShell  bool
	SemanticSource string
	ExitCode       int
	Summary        string
	ShellContext   *shell.PromptContext
}

type ContextWindowUsage struct {
	ApproxPromptTokens int
}

type Controller interface {
	SubmitAgentPrompt(ctx context.Context, prompt string) ([]TranscriptEvent, error)
	SubmitExternalPrompt(ctx context.Context, prompt string) ([]TranscriptEvent, error)
	SubmitRefinement(ctx context.Context, approval ApprovalRequest, note string) ([]TranscriptEvent, error)
	SubmitProposalRefinement(ctx context.Context, proposal ProposalPayload, note string) ([]TranscriptEvent, error)
	DecideHandoff(ctx context.Context, handoffID string, accept bool) ([]TranscriptEvent, error)
	ContinueActivePlan(ctx context.Context) ([]TranscriptEvent, error)
	ContinueAfterCommand(ctx context.Context) ([]TranscriptEvent, error)
	ContinueAfterPatchApply(ctx context.Context) ([]TranscriptEvent, error)
	CheckActiveExecution(ctx context.Context) ([]TranscriptEvent, error)
	ResumeAfterTakeControl(ctx context.Context) ([]TranscriptEvent, error)
	ResumeExternal(ctx context.Context) ([]TranscriptEvent, error)
	ReturnToBuiltin(ctx context.Context) ([]TranscriptEvent, error)
	RefreshActiveExecution(ctx context.Context) ([]TranscriptEvent, *CommandExecution, error)
	SubmitShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error)
	SubmitProposedShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error)
	ApplyProposedPatch(ctx context.Context, patch string) ([]TranscriptEvent, error)
	DecideApproval(ctx context.Context, approvalID string, decision ApprovalDecision, refineText string) ([]TranscriptEvent, error)
	SetApprovalMode(ctx context.Context, mode ApprovalMode) ([]TranscriptEvent, error)
	StartNewTask(ctx context.Context) ([]TranscriptEvent, error)
	CompactTask(ctx context.Context) ([]TranscriptEvent, error)
	RefreshShellContext(ctx context.Context) (*shell.PromptContext, error)
	PeekShellTail(ctx context.Context, lines int) (string, error)
	EstimateContextUsage(prompt string) ContextWindowUsage
	ApprovalMode() ApprovalMode
	ExternalState() ExternalState
	RuntimeActivity() RuntimeActivitySnapshot
	ActiveExecution() *CommandExecution
	AbandonActiveExecution(reason string) *CommandExecution
	TakeControlTarget() TrackedShellTarget
	TrackedShellTarget() TrackedShellTarget
}

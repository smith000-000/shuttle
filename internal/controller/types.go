package controller

import (
	"context"
	"time"

	"aiterm/internal/shell"
)

type Agent interface {
	Respond(ctx context.Context, input AgentInput) (AgentResponse, error)
}

type AgentInput struct {
	Session SessionContext
	Task    TaskContext
	Prompt  string
}

type SessionContext struct {
	SessionName       string
	TopPaneID         string
	BottomPaneID      string
	WorkingDirectory  string
	RecentShellOutput string
	CurrentShell      *shell.PromptContext
}

type TaskContext struct {
	TaskID            string
	PriorTranscript   []TranscriptEvent
	PendingApproval   *ApprovalRequest
	LastCommandResult *CommandResultSummary
	ActivePlan        *ActivePlan
	CurrentExecution  *CommandExecution
	RecoverySnapshot  string
}

type AgentResponse struct {
	Message  string
	Plan     *Plan
	Proposal *Proposal
	Approval *ApprovalRequest
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

type TranscriptEvent struct {
	ID        string
	Kind      TranscriptEventKind
	Timestamp time.Time
	Payload   any
}

type TranscriptEventKind string

const (
	EventUserMessage   TranscriptEventKind = "user_message"
	EventAgentMessage  TranscriptEventKind = "agent_message"
	EventPlan          TranscriptEventKind = "plan"
	EventProposal      TranscriptEventKind = "proposal"
	EventApproval      TranscriptEventKind = "approval"
	EventCommandStart  TranscriptEventKind = "command_start"
	EventCommandResult TranscriptEventKind = "command_result"
	EventSystemNotice  TranscriptEventKind = "system_notice"
	EventError         TranscriptEventKind = "error"
)

type CommandOrigin string

const (
	CommandOriginUserShell     CommandOrigin = "user_shell"
	CommandOriginAgentProposal CommandOrigin = "agent_proposal"
	CommandOriginAgentApproval CommandOrigin = "agent_approval"
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

type CommandExecution struct {
	ID                 string
	Command            string
	Origin             CommandOrigin
	State              CommandExecutionState
	StartedAt          time.Time
	CompletedAt        *time.Time
	ExitCode           *int
	LatestOutputTail   string
	ForegroundCommand  string
	Error              string
	ShellContextBefore *shell.PromptContext
	ShellContextAfter  *shell.PromptContext
}

type CommandResultSummary struct {
	ExecutionID  string
	CommandID    string
	Command      string
	Origin       CommandOrigin
	State        CommandExecutionState
	Cause        shell.CompletionCause
	Confidence   shell.SignalConfidence
	ExitCode     int
	Summary      string
	ShellContext *shell.PromptContext
}

type Controller interface {
	SubmitAgentPrompt(ctx context.Context, prompt string) ([]TranscriptEvent, error)
	SubmitRefinement(ctx context.Context, approval ApprovalRequest, note string) ([]TranscriptEvent, error)
	SubmitProposalRefinement(ctx context.Context, proposal ProposalPayload, note string) ([]TranscriptEvent, error)
	ContinueActivePlan(ctx context.Context) ([]TranscriptEvent, error)
	ContinueAfterCommand(ctx context.Context) ([]TranscriptEvent, error)
	CheckActiveExecution(ctx context.Context) ([]TranscriptEvent, error)
	ResumeAfterTakeControl(ctx context.Context) ([]TranscriptEvent, error)
	SubmitShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error)
	SubmitProposedShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error)
	DecideApproval(ctx context.Context, approvalID string, decision ApprovalDecision, refineText string) ([]TranscriptEvent, error)
	RefreshShellContext(ctx context.Context) (*shell.PromptContext, error)
	PeekShellTail(ctx context.Context, lines int) (string, error)
	ActiveExecution() *CommandExecution
	AbandonActiveExecution(reason string) *CommandExecution
}

package controller

import (
	"context"
	"time"
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
}

type TaskContext struct {
	TaskID            string
	PriorTranscript   []TranscriptEvent
	PendingApproval   *ApprovalRequest
	LastCommandResult *CommandResultSummary
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

type CommandResultSummary struct {
	CommandID string
	Command   string
	ExitCode  int
	Summary   string
}

type Controller interface {
	SubmitAgentPrompt(ctx context.Context, prompt string) ([]TranscriptEvent, error)
	SubmitRefinement(ctx context.Context, approval ApprovalRequest, note string) ([]TranscriptEvent, error)
	SubmitShellCommand(ctx context.Context, command string) ([]TranscriptEvent, error)
	DecideApproval(ctx context.Context, approvalID string, decision ApprovalDecision, refineText string) ([]TranscriptEvent, error)
}

package controller

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aiterm/internal/patchapply"
	"aiterm/internal/shell"
)

const autoContinuePrompt = "The previously approved or proposed command has completed. First summarize the result briefly. If there is an active plan, continue it from the current step. If there is no active plan, prefer stopping after reporting the outcome once the user's request is satisfied. Only propose more shell work when the transcript or command result clearly shows unresolved work. Do not propose extra verification just because a command succeeded unless the user explicitly asked for verification or the result is ambiguous. If risky, request approval."
const continueAfterPatchApplyPrompt = "The previously approved or proposed patch was applied to the local workspace. First summarize the file changes briefly. If there is an active plan, continue it from the current step. If there is no active plan and the user's request is satisfied by the applied patch, stop after reporting the outcome. Do not propose extra verification or follow-up edits unless the user explicitly asked for them or the patch result/transcript shows unresolved work. If risky, request approval."
const continueAfterPatchFailurePrompt = "The previously proposed or approved patch did not apply cleanly. First summarize the patch failure briefly. If the task still needs a file change, propose exactly one corrected patch as proposal_kind:\"patch\" with a valid unified diff. Emit only raw unified diff text with exact hunk headers and counts. Do not emit a shell command that invokes apply_patch, git apply, patch, or any heredoc-based patch tool. If the next step is risky, request approval. If the failure already resolves the task or the user should intervene manually, answer briefly."
const autoContinuePromptSerialSuffix = "The recent transcript indicates the user asked for serial or ordered shell work. If the completed command only unlocked the next step, propose exactly one next command now instead of waiting for another nudge. Do not lump multiple shell actions together, and do not wait for the user to say 'go' unless they explicitly asked to approve each step separately."
const autoContinuePromptUnresolvedInspectionSuffix = "The latest command was a read-only inspection and the transcript still shows unresolved work. Do not stop at diagnosis. Propose exactly one next action now as a command, patch, or approval, whichever best addresses the unresolved issue."
const autoContinuePromptChecklistSuffix = "The user's request is an ordered multi-step workflow. If there is no active plan yet, emit a concise checklist in plan_summary and plan_steps for the remaining steps, then propose only the next immediate action."
const continuePlanPrompt = "Continue the active plan from the current step. Propose the next safe action if one is needed. If the next action is risky, request approval. If the plan is complete, answer briefly."
const resumeAfterTakeControlPrompt = "The user temporarily took control of the shell to handle an interactive step such as a password prompt, remote login, or fullscreen terminal app. Reassess the latest shell state and continue the task. If another action is needed, propose it. If risky, request approval. If the task is complete, answer briefly."
const activeExecutionCheckInPrompt = "An agent-started shell command is still active. Use the execution state and latest shell output to decide whether it is running normally or merely quiet. If there is no new output, say that no new shell output has appeared yet. Do not claim the command has completed or that the shell returned to a prompt unless the context shows that. Do not propose a new command, plan, or approval unless the shell is clearly blocked and needs user intervention; if so, say that the user should press F2 to take control."
const awaitingInputCheckInPrompt = "An agent-started shell command is waiting for shell input. Use the latest shell output and recovery snapshot to explain what input is likely needed. Do not claim the command has completed. Prefer a concise recovery message that tells the user to press F2 to take control. If the task only needs a small raw key sequence, mention KEYS> as an option."
const fullscreenCheckInPrompt = "An agent-started shell command has occupied a fullscreen terminal app. Use the latest shell output and recovery snapshot to identify the app or state as best you can. Do not claim the command has completed. Prefer a concise recovery message telling the user to press F2 to take control, or use KEYS> if they only need to send a few raw keys."
const lostTrackingCheckInPrompt = "Tracking confidence for an agent-started shell command is low. Use the latest shell output and recovery snapshot to explain what likely happened, without claiming completion unless the context clearly proves it. Prefer a recovery-oriented message that suggests inspecting the shell with F2 if the state is ambiguous."
const initialChecklistPromptSuffix = "The user's request is an ordered multi-step workflow. Start by creating a concise checklist in plan_summary and plan_steps that matches the requested sequence, then propose only the next immediate action."

const recoverySnapshotLines = 200

type ShellRunner interface {
	RunTrackedCommand(ctx context.Context, paneID string, command string, timeout time.Duration) (shell.TrackedExecution, error)
}

type MonitoringShellRunner interface {
	StartTrackedCommand(ctx context.Context, paneID string, command string, timeout time.Duration) (shell.CommandMonitor, error)
}

type ForegroundMonitoringShellRunner interface {
	AttachForegroundCommand(ctx context.Context, paneID string) (shell.CommandMonitor, error)
}

type OwnedExecutionShellRunner interface {
	CreateOwnedExecutionPane(ctx context.Context, startDir string) (shell.OwnedExecutionPane, func(context.Context) error, error)
}

type TrackedPaneResolver interface {
	ResolveTrackedPane(ctx context.Context, paneID string) (string, error)
}

type ShellContextReader interface {
	CaptureRecentOutput(ctx context.Context, paneID string, lines int) (string, error)
	CaptureShellContext(ctx context.Context, paneID string) (shell.PromptContext, error)
}

type TextPayload struct {
	Text string
}

type PatchApplier interface {
	Validate(ctx context.Context, patch string) (patchapply.Result, error)
	Apply(ctx context.Context, patch string) (patchapply.Result, error)
}

type PlanPayload = ActivePlan

type ProposalPayload struct {
	Kind        ProposalKind
	Command     string
	Keys        string
	Patch       string
	Description string
}

type CommandStartPayload struct {
	Command   string
	Execution CommandExecution
}

type LocalController struct {
	agent        Agent
	runner       ShellRunner
	reader       ShellContextReader
	session      SessionContext
	patches      PatchApplier
	patchInitErr error

	mu                       sync.Mutex
	counter                  atomic.Uint64
	task                     TaskContext
	executions               map[string]*CommandExecution
	primaryExecution         string
	executionCleanups        map[string]func(context.Context) error
	attachedMonitorCancels   map[string]context.CancelFunc
	foregroundAttachInFlight bool
}

func New(agent Agent, runner ShellRunner, reader ShellContextReader, session SessionContext) *LocalController {
	session = normalizeSessionContext(session)
	controller := &LocalController{
		agent:   agent,
		runner:  runner,
		reader:  reader,
		session: session,
		task: TaskContext{
			TaskID: "task-1",
		},
	}
	if session.LocalWorkspaceRoot != "" {
		patches, err := patchapply.New(session.LocalWorkspaceRoot)
		if err != nil {
			controller.patchInitErr = err
		} else {
			controller.patches = patches
		}
	}
	controller.syncTaskExecutionViewsLocked()
	return controller
}

func normalizeSessionContext(session SessionContext) SessionContext {
	session.SessionName = strings.TrimSpace(session.SessionName)
	session.BottomPaneID = strings.TrimSpace(session.BottomPaneID)
	session.WorkingDirectory = normalizeWorkingDirectory(session.WorkingDirectory)
	session.LocalWorkspaceRoot = normalizeWorkingDirectory(session.LocalWorkspaceRoot)
	session.UserShellHistoryFile = strings.TrimSpace(session.UserShellHistoryFile)
	session.RecentShellOutput = strings.TrimSpace(session.RecentShellOutput)
	session.TrackedShell.SessionName = strings.TrimSpace(session.TrackedShell.SessionName)
	session.TrackedShell.PaneID = strings.TrimSpace(session.TrackedShell.PaneID)
	session.RecentManualCommands = trimStringSlice(session.RecentManualCommands)
	session.RecentManualActions = trimStringSlice(session.RecentManualActions)
	if session.CurrentShell != nil {
		contextCopy := *session.CurrentShell
		session.CurrentShell = &contextCopy
		if session.WorkingDirectory == "" {
			session.WorkingDirectory = normalizeWorkingDirectory(contextCopy.Directory)
		}
	}
	if session.LocalWorkspaceRoot == "" {
		session.LocalWorkspaceRoot = session.WorkingDirectory
	}

	if session.TrackedShell.SessionName == "" {
		session.TrackedShell.SessionName = session.SessionName
	}
	if session.SessionName == "" {
		session.SessionName = session.TrackedShell.SessionName
	}

	return session
}

func trimStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		trimmed = append(trimmed, value)
	}
	if len(trimmed) == 0 {
		return nil
	}
	return trimmed
}

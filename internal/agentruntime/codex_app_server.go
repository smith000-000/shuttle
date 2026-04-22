package agentruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

var codexAppServerClientFactory = func(command string, meta RuntimeMetadata) codexAppServerClientFactoryFunc {
	return func(ctx context.Context) (codexAppServerClient, error) {
		return newStdioCodexAppServerClient(ctx, command, meta)
	}
}

type CodexAppServerTurnHandler interface {
	Respond(context.Context, Request) (Outcome, error)
}

type codexAppServerRuntime struct {
	meta    RuntimeMetadata
	handler CodexAppServerTurnHandler
}

type codexAppServerClientFactoryFunc func(context.Context) (codexAppServerClient, error)

type codexAppServerDefaultHandler struct {
	meta             RuntimeMetadata
	clientFactory    codexAppServerClientFactoryFunc
	mu               sync.Mutex
	client           codexAppServerClient
	threads          map[string]string
	pendingApprovals map[string]codexAppServerPendingApproval
}

type codexAppServerClient interface {
	Initialize(context.Context) error
	StartThread(context.Context, codexAppServerThreadStartParams) (codexAppServerThreadStartResult, error)
	StartTurn(context.Context, codexAppServerTurnStartParams) (codexAppServerTurnStartResult, error)
	StartThreadCompaction(context.Context, string) error
	WaitForTurnCompletion(context.Context, string, string) (codexAppServerWaitResult, error)
	WaitForThreadCompaction(context.Context, string) error
	ResolveApproval(context.Context, codexAppServerPendingApproval, ApprovalDecision) error
	Close() error
}

type codexAppServerJSONRPC struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type codexAppServerPermissionsRequestApprovalParams struct {
	ItemID      string `json:"itemId"`
	Permissions any    `json:"permissions"`
	Reason      string `json:"reason,omitempty"`
	ThreadID    string `json:"threadId"`
	TurnID      string `json:"turnId"`
}

type codexAppServerCommandExecutionRequestApprovalParams struct {
	ItemID                string `json:"itemId"`
	ApprovalID            string `json:"approvalId,omitempty"`
	ThreadID              string `json:"threadId"`
	TurnID                string `json:"turnId"`
	Command               string `json:"command,omitempty"`
	Cwd                   string `json:"cwd,omitempty"`
	Reason                string `json:"reason,omitempty"`
	AdditionalPermissions any    `json:"additionalPermissions,omitempty"`
}

type codexAppServerPermissionsApprovalResponse struct {
	Permissions any    `json:"permissions"`
	Scope       string `json:"scope,omitempty"`
}

type codexAppServerCommandExecutionApprovalResponse struct {
	Decision string `json:"decision"`
}

type codexAppServerFileChangeRequestApprovalParams struct {
	ItemID    string `json:"itemId"`
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	Reason    string `json:"reason,omitempty"`
	GrantRoot string `json:"grantRoot,omitempty"`
}

type codexAppServerFileChangeApprovalResponse struct {
	Decision string `json:"decision"`
}

type codexAppServerTurnPlanUpdatedNotification struct {
	ThreadID    string                       `json:"threadId"`
	TurnID      string                       `json:"turnId"`
	Explanation string                       `json:"explanation,omitempty"`
	Plan        []codexAppServerTurnPlanStep `json:"plan"`
}

type codexAppServerTurnPlanStep struct {
	Status string `json:"status"`
	Step   string `json:"step"`
}

type codexAppServerThreadStartParams struct {
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
	Ephemeral      bool   `json:"ephemeral,omitempty"`
	Model          string `json:"model,omitempty"`
	ModelProvider  string `json:"modelProvider,omitempty"`
	Personality    string `json:"personality,omitempty"`
	Sandbox        string `json:"sandbox,omitempty"`
}

type codexAppServerThreadStartResult struct {
	Thread codexAppServerThread `json:"thread"`
}

type codexAppServerTurnStartParams struct {
	ThreadID          string                    `json:"threadId"`
	Input             []codexAppServerUserInput `json:"input"`
	Cwd               string                    `json:"cwd,omitempty"`
	Model             string                    `json:"model,omitempty"`
	OutputSchema      map[string]any            `json:"outputSchema,omitempty"`
	ApprovalPolicy    string                    `json:"approvalPolicy,omitempty"`
	ApprovalsReviewer string                    `json:"approvalsReviewer,omitempty"`
	Personality       string                    `json:"personality,omitempty"`
}

type codexAppServerUserInput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexAppServerTurnStartResult struct {
	Turn codexAppServerTurn `json:"turn"`
}

type codexAppServerTurnCompletedNotification struct {
	ThreadID string             `json:"threadId"`
	Turn     codexAppServerTurn `json:"turn"`
}

type codexAppServerThreadCompactedNotification struct {
	ThreadID string `json:"threadId"`
}

type codexAppServerItemCompletedNotification struct {
	ThreadID string                   `json:"threadId"`
	TurnID   string                   `json:"turnId"`
	Item     codexAppServerThreadItem `json:"item"`
}

type codexAppServerThread struct {
	ID     string          `json:"id"`
	Status json.RawMessage `json:"status,omitempty"`
}

type codexAppServerTurn struct {
	ID       string                     `json:"id"`
	Status   string                     `json:"status,omitempty"`
	Items    []codexAppServerThreadItem `json:"items,omitempty"`
	Plan     []codexAppServerTurnPlanStep
	PlanNote string
}

type codexAppServerPendingApproval struct {
	RequestID            string
	Method               string
	ThreadID             string
	TurnID               string
	ItemID               string
	ApprovalID           string
	Command              string
	Cwd                  string
	Reason               string
	GrantRoot            string
	RequestedPermissions any
}

type codexAppServerWaitResult struct {
	Turn            *codexAppServerTurn
	PendingApproval *codexAppServerPendingApproval
}

type codexAppServerFailureClass string

const (
	codexAppServerFailureNone             codexAppServerFailureClass = ""
	codexAppServerFailureStaleThread      codexAppServerFailureClass = "stale_thread"
	codexAppServerFailureTransientProcess codexAppServerFailureClass = "transient_process"
)

type codexAppServerThreadItem struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Phase string `json:"phase,omitempty"`
}

type stdioCodexAppServerClient struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	reader   *bufio.Reader
	stderr   bytes.Buffer
	nextID   int64
	readMu   sync.Mutex
	writeMu  sync.Mutex
	waitOnce sync.Once
	waitErr  error
}

type shuttleStructuredResponse struct {
	Message                string   `json:"message"`
	PlanSummary            string   `json:"plan_summary"`
	PlanSteps              []string `json:"plan_steps"`
	PlanStepStatuses       []string `json:"plan_step_statuses"`
	ProposalKind           string   `json:"proposal_kind"`
	ProposalCommand        string   `json:"proposal_command"`
	ProposalKeys           string   `json:"proposal_keys"`
	ProposalPatch          string   `json:"proposal_patch"`
	ProposalPatchTarget    string   `json:"proposal_patch_target"`
	ProposalEditPath       string   `json:"proposal_edit_path"`
	ProposalEditOperation  string   `json:"proposal_edit_operation"`
	ProposalEditAnchorText string   `json:"proposal_edit_anchor_text"`
	ProposalEditOldText    string   `json:"proposal_edit_old_text"`
	ProposalEditNewText    string   `json:"proposal_edit_new_text"`
	ProposalEditStartLine  int      `json:"proposal_edit_start_line"`
	ProposalEditEndLine    int      `json:"proposal_edit_end_line"`
	ProposalDescription    string   `json:"proposal_description"`
	ApprovalKind           string   `json:"approval_kind"`
	ApprovalTitle          string   `json:"approval_title"`
	ApprovalSummary        string   `json:"approval_summary"`
	ApprovalCommand        string   `json:"approval_command"`
	ApprovalPatch          string   `json:"approval_patch"`
	ApprovalPatchTarget    string   `json:"approval_patch_target"`
	ApprovalRisk           string   `json:"approval_risk"`
}

func NewCodexAppServerRuntime(meta RuntimeMetadata, handler CodexAppServerTurnHandler) Runtime {
	meta.Type = normalizeRuntimeSelection(meta.Type)
	if meta.Type == "" {
		meta.Type = RuntimeCodexAppServer
	}
	runtime := codexAppServerRuntime{meta: meta, handler: handler}
	if handler == nil {
		runtime.handler = &codexAppServerDefaultHandler{
			meta:             meta,
			clientFactory:    codexAppServerClientFactory(strings.TrimSpace(meta.Command), meta),
			threads:          map[string]string{},
			pendingApprovals: map[string]codexAppServerPendingApproval{},
		}
	}
	return runtime
}

func (r codexAppServerRuntime) Handle(ctx context.Context, host Host, req Request) (Outcome, error) {
	handler := r.handler
	outcome, err := handleRuntimeTurn(ctx, host, req, func(ctx context.Context, req Request) (Outcome, error) {
		return handler.Respond(ctx, req)
	})
	if err != nil {
		return Outcome{}, err
	}
	return annotateRuntimeMetadata(outcome, r.meta), nil
}

func (h *codexAppServerDefaultHandler) respond(ctx context.Context, req Request) (Outcome, error) {
	if h.clientFactory == nil {
		return Outcome{}, errors.New("codex app server client factory is required")
	}
	if req.Kind == RequestResolveApproval {
		return h.resolvePendingApproval(ctx, req)
	}
	if req.Kind == RequestCompactTask {
		return h.respondCompact(ctx, req)
	}
	if req.Kind == RequestApprovalRefinement {
		if err := h.prepareApprovalRefinement(ctx, req); err != nil {
			return Outcome{}, err
		}
	}

	outcome, reusedStoredThread, err := h.respondOnce(ctx, req)
	if err == nil {
		return outcome, nil
	}
	failureClass := classifyCodexAppServerFailure(err)
	if errors.Is(err, context.Canceled) || failureClass == codexAppServerFailureNone {
		return Outcome{}, err
	}
	recoveryNote := "Recovered from a stale Codex app-server thread by starting a fresh native thread."
	if !reusedStoredThread {
		recoveryNote = "Recovered from a transient Codex app-server process failure by retrying the turn with a fresh native thread."
	}
	h.resetClientAndThread(req)
	outcome, retryErr := h.respondOnceFresh(ctx, req)
	if retryErr != nil {
		return Outcome{}, retryErr
	}
	if outcome.ModelInfo == nil {
		outcome.ModelInfo = &ModelInfo{}
	}
	if strings.TrimSpace(outcome.ModelInfo.RuntimeFailureReason) == "" {
		outcome.ModelInfo.RuntimeFailureReason = recoveryNote
	}
	return outcome, nil
}

func (h *codexAppServerDefaultHandler) Respond(ctx context.Context, req Request) (Outcome, error) {
	return h.respond(ctx, req)
}

func (h *codexAppServerDefaultHandler) respondCompact(ctx context.Context, req Request) (Outcome, error) {
	threadID, found := h.threadFor(req)
	threadID = strings.TrimSpace(threadID)
	if !found || threadID == "" {
		return h.respondOnceFresh(ctx, req)
	}

	client, err := h.clientFor(ctx)
	if err != nil {
		return Outcome{}, err
	}
	if err := client.StartThreadCompaction(ctx, threadID); err != nil {
		if failureClass := classifyCodexAppServerFailure(err); failureClass != codexAppServerFailureNone {
			h.resetClientAndThread(req)
			return Outcome{}, codexAppServerCompactionError(failureClass)
		}
		return Outcome{}, err
	}
	if err := client.WaitForThreadCompaction(ctx, threadID); err != nil {
		if failureClass := classifyCodexAppServerFailure(err); failureClass != codexAppServerFailureNone {
			h.resetClientAndThread(req)
			return Outcome{}, codexAppServerCompactionError(failureClass)
		}
		return Outcome{}, err
	}
	return Outcome{NativeCompaction: true}, nil
}

func (h *codexAppServerDefaultHandler) respondOnce(ctx context.Context, req Request) (Outcome, bool, error) {
	threadID, found := h.threadFor(req)
	outcome, err := h.respondWithThreadPreference(ctx, req, strings.TrimSpace(threadID), found)
	return outcome, found, err
}

func (h *codexAppServerDefaultHandler) respondOnceFresh(ctx context.Context, req Request) (Outcome, error) {
	return h.respondWithThreadPreference(ctx, req, "", false)
}

func (h *codexAppServerDefaultHandler) respondWithThreadPreference(ctx context.Context, req Request, storedThreadID string, useStoredThread bool) (Outcome, error) {
	client, err := h.clientFor(ctx)
	if err != nil {
		return Outcome{}, err
	}
	cwd, _ := os.Getwd()
	threadID := strings.TrimSpace(storedThreadID)
	if !useStoredThread || threadID == "" {
		thread, startErr := client.StartThread(ctx, codexAppServerThreadStartParams{
			ApprovalPolicy: "on-request",
			Cwd:            cwd,
			Ephemeral:      false,
			Model:          strings.TrimSpace(h.meta.Model),
			ModelProvider:  modelProviderForPreset(h.meta.ProviderPreset),
			Personality:    "pragmatic",
			Sandbox:        "read-only",
		})
		if startErr != nil {
			return Outcome{}, startErr
		}
		threadID = strings.TrimSpace(thread.Thread.ID)
		h.storeThread(req, threadID)
	}
	turn, err := client.StartTurn(ctx, codexAppServerTurnStartParams{
		ThreadID: threadID,
		Input: []codexAppServerUserInput{{
			Type: "text",
			Text: buildNativeDelegatedRuntimePrompt(req),
		}},
		Cwd:               cwd,
		Model:             strings.TrimSpace(h.meta.Model),
		ApprovalPolicy:    "on-request",
		ApprovalsReviewer: "user",
		Personality:       "pragmatic",
	})
	if err != nil {
		return Outcome{}, err
	}
	waitResult, err := client.WaitForTurnCompletion(ctx, threadID, turn.Turn.ID)
	if err != nil {
		return Outcome{}, err
	}
	if waitResult.PendingApproval != nil {
		approval := *waitResult.PendingApproval
		h.storePendingApproval(req, approval)
		return Outcome{Approval: codexAppServerPendingApprovalToApprovalRequest(approval)}, nil
	}
	if waitResult.Turn == nil {
		return Outcome{}, errors.New("codex app server turn completed without a result")
	}
	completedTurn := *waitResult.Turn
	outcome, err := codexAppServerTurnOutcome(completedTurn)
	if err != nil {
		return Outcome{}, err
	}
	return applyCodexAppServerTurnPlanToOutcome(outcome, completedTurn.Plan, completedTurn.PlanNote), nil
}

func (h *codexAppServerDefaultHandler) resolvePendingApproval(ctx context.Context, req Request) (Outcome, error) {
	if req.Approval == nil {
		return Outcome{}, errors.New("runtime approval resolution requires approval context")
	}
	if req.ApprovalDecision == "" {
		return Outcome{}, errors.New("runtime approval resolution requires a decision")
	}
	pending, ok := h.pendingApprovalFor(req)
	if !ok {
		return Outcome{}, errors.New("runtime approval request not found")
	}
	if token := strings.TrimSpace(req.Approval.ContinuationToken); token != "" && token != pending.RequestID {
		return Outcome{}, errors.New("runtime approval continuation token does not match the active request")
	}

	client, err := h.clientFor(ctx)
	if err != nil {
		return Outcome{}, err
	}
	if err := client.ResolveApproval(ctx, pending, req.ApprovalDecision); err != nil {
		if failureClass := classifyCodexAppServerFailure(err); failureClass != codexAppServerFailureNone {
			h.resetClientAndThread(req)
			return Outcome{}, codexAppServerApprovalResumeError(failureClass)
		}
		return Outcome{}, err
	}
	h.clearPendingApproval(req)

	waitResult, err := client.WaitForTurnCompletion(ctx, pending.ThreadID, pending.TurnID)
	if err != nil {
		if failureClass := classifyCodexAppServerFailure(err); failureClass != codexAppServerFailureNone {
			h.resetClientAndThread(req)
			return Outcome{}, codexAppServerApprovalResumeError(failureClass)
		}
		return Outcome{}, err
	}
	if waitResult.PendingApproval != nil {
		approval := *waitResult.PendingApproval
		h.storePendingApproval(req, approval)
		return Outcome{Approval: codexAppServerPendingApprovalToApprovalRequest(approval)}, nil
	}
	if waitResult.Turn == nil {
		return Outcome{}, errors.New("codex app server approval resolution completed without a turn result")
	}
	completedTurn := *waitResult.Turn
	outcome, err := codexAppServerTurnOutcome(completedTurn)
	if err != nil {
		return Outcome{}, err
	}
	return applyCodexAppServerTurnPlanToOutcome(outcome, completedTurn.Plan, completedTurn.PlanNote), nil
}

func (h *codexAppServerDefaultHandler) prepareApprovalRefinement(ctx context.Context, req Request) error {
	pending, ok := h.pendingApprovalFor(req)
	if !ok {
		return nil
	}
	if req.Approval != nil {
		if token := strings.TrimSpace(req.Approval.ContinuationToken); token != "" && token != pending.RequestID {
			return errors.New("runtime approval continuation token does not match the active request")
		}
	}

	client, err := h.clientFor(ctx)
	if err != nil {
		return err
	}
	if err := client.ResolveApproval(ctx, pending, DecisionReject); err != nil {
		if errors.Is(err, context.Canceled) || classifyCodexAppServerFailure(err) == codexAppServerFailureNone {
			return err
		}
		h.resetClientAndThread(req)
		return nil
	}
	h.clearPendingApproval(req)

	waitResult, err := client.WaitForTurnCompletion(ctx, pending.ThreadID, pending.TurnID)
	if err != nil {
		if errors.Is(err, context.Canceled) || classifyCodexAppServerFailure(err) == codexAppServerFailureNone {
			return err
		}
		h.resetClientAndThread(req)
		return nil
	}
	if waitResult.PendingApproval != nil {
		// The original suspended turn stayed in an approval loop; abandon the old thread
		// before sending the new refinement turn so the refinement runs on a clean thread.
		h.resetClientAndThread(req)
	}
	return nil
}

func (h *codexAppServerDefaultHandler) clientFor(ctx context.Context) (codexAppServerClient, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.client != nil {
		return h.client, nil
	}
	client, err := h.clientFactory(ctx)
	if err != nil {
		return nil, err
	}
	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	h.client = client
	if h.threads == nil {
		h.threads = map[string]string{}
	}
	if h.pendingApprovals == nil {
		h.pendingApprovals = map[string]codexAppServerPendingApproval{}
	}
	return h.client, nil
}

func (h *codexAppServerDefaultHandler) threadFor(req Request) (string, bool) {
	h.mu.Lock()
	threadID, ok := h.threads[codexAppServerMemoryThreadKey(req.SessionName, req.TaskID)]
	h.mu.Unlock()
	threadID = strings.TrimSpace(threadID)
	if ok && threadID != "" {
		return threadID, true
	}
	if stored, found, err := LoadStoredCodexAppServerThreadBinding(strings.TrimSpace(h.meta.StateDir), req.SessionName, req.TaskID); err == nil && found {
		h.mu.Lock()
		if h.threads == nil {
			h.threads = map[string]string{}
		}
		h.threads[codexAppServerMemoryThreadKey(req.SessionName, req.TaskID)] = strings.TrimSpace(stored)
		h.mu.Unlock()
		return strings.TrimSpace(stored), true
	}
	return "", false
}

func (h *codexAppServerDefaultHandler) storeThread(req Request, threadID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.threads == nil {
		h.threads = map[string]string{}
	}
	threadID = strings.TrimSpace(threadID)
	h.threads[codexAppServerMemoryThreadKey(req.SessionName, req.TaskID)] = threadID
	if threadID != "" {
		_ = SaveStoredCodexAppServerThreadBinding(strings.TrimSpace(h.meta.StateDir), req.SessionName, req.TaskID, threadID)
	}
}

func (h *codexAppServerDefaultHandler) pendingApprovalFor(req Request) (codexAppServerPendingApproval, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	approval, ok := h.pendingApprovals[codexAppServerMemoryThreadKey(req.SessionName, req.TaskID)]
	return approval, ok
}

func (h *codexAppServerDefaultHandler) storePendingApproval(req Request, approval codexAppServerPendingApproval) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pendingApprovals == nil {
		h.pendingApprovals = map[string]codexAppServerPendingApproval{}
	}
	h.pendingApprovals[codexAppServerMemoryThreadKey(req.SessionName, req.TaskID)] = approval
}

func (h *codexAppServerDefaultHandler) clearPendingApproval(req Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.pendingApprovals, codexAppServerMemoryThreadKey(req.SessionName, req.TaskID))
}

func (h *codexAppServerDefaultHandler) resetClientAndThread(req Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.client != nil {
		_ = h.client.Close()
		h.client = nil
	}
	delete(h.threads, codexAppServerMemoryThreadKey(req.SessionName, req.TaskID))
	delete(h.pendingApprovals, codexAppServerMemoryThreadKey(req.SessionName, req.TaskID))
	_ = DeleteStoredCodexAppServerThreadBinding(strings.TrimSpace(h.meta.StateDir), req.SessionName, req.TaskID)
}

func codexAppServerMemoryThreadKey(sessionName string, taskID string) string {
	return strings.TrimSpace(sessionName) + "\x00" + strings.TrimSpace(taskID)
}

func newStdioCodexAppServerClient(ctx context.Context, command string, meta RuntimeMetadata) (codexAppServerClient, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		command = "codex"
	}
	cmd := exec.CommandContext(ctx, command, "app-server", "--listen", "stdio://")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("start codex app server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("start codex app server stdout: %w", err)
	}
	client := &stdioCodexAppServerClient{cmd: cmd, stdin: stdin, reader: bufio.NewReader(stdout)}
	cmd.Stderr = &client.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex app server %q: %w", command, err)
	}
	return client, nil
}

func (c *stdioCodexAppServerClient) Initialize(ctx context.Context) error {
	params := map[string]any{
		"clientInfo": map[string]any{"name": "shuttle", "version": "0.1"},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}
	var result map[string]any
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return err
	}
	return c.sendNotification(ctx, "initialized", nil)
}

func (c *stdioCodexAppServerClient) StartThread(ctx context.Context, params codexAppServerThreadStartParams) (codexAppServerThreadStartResult, error) {
	var result codexAppServerThreadStartResult
	return result, c.call(ctx, "thread/start", params, &result)
}

func (c *stdioCodexAppServerClient) StartTurn(ctx context.Context, params codexAppServerTurnStartParams) (codexAppServerTurnStartResult, error) {
	var result codexAppServerTurnStartResult
	return result, c.call(ctx, "turn/start", params, &result)
}

func (c *stdioCodexAppServerClient) StartThreadCompaction(ctx context.Context, threadID string) error {
	params := map[string]any{"threadId": strings.TrimSpace(threadID)}
	return c.call(ctx, "thread/compact/start", params, nil)
}

func (c *stdioCodexAppServerClient) WaitForTurnCompletion(ctx context.Context, threadID string, turnID string) (codexAppServerWaitResult, error) {
	var latestAgentMessage string
	var latestPlan []codexAppServerTurnPlanStep
	var latestPlanNote string
	for {
		message, err := c.readMessage(ctx)
		if err != nil {
			return codexAppServerWaitResult{}, err
		}
		if message.Method == "error" {
			return codexAppServerWaitResult{}, fmt.Errorf("codex app server error notification: %s", strings.TrimSpace(string(message.Params)))
		}
		if strings.TrimSpace(message.Method) != "" && strings.TrimSpace(message.ID) != "" {
			pendingApproval, handled, err := c.decodePendingApproval(message)
			if err != nil {
				return codexAppServerWaitResult{}, err
			}
			if handled {
				return codexAppServerWaitResult{PendingApproval: pendingApproval}, nil
			}
			return codexAppServerWaitResult{}, c.sendRequestError(ctx, message.ID, -32601, "unsupported request from codex app server")
		}
		switch message.Method {
		case "turn/plan/updated":
			var notification codexAppServerTurnPlanUpdatedNotification
			if err := json.Unmarshal(message.Params, &notification); err != nil {
				return codexAppServerWaitResult{}, fmt.Errorf("decode codex app server plan update: %w", err)
			}
			if strings.TrimSpace(notification.ThreadID) != strings.TrimSpace(threadID) || strings.TrimSpace(notification.TurnID) != strings.TrimSpace(turnID) {
				continue
			}
			latestPlan = append([]codexAppServerTurnPlanStep(nil), notification.Plan...)
			latestPlanNote = strings.TrimSpace(notification.Explanation)
		case "item/completed":
			var notification codexAppServerItemCompletedNotification
			if err := json.Unmarshal(message.Params, &notification); err != nil {
				return codexAppServerWaitResult{}, fmt.Errorf("decode codex app server item completion: %w", err)
			}
			if strings.TrimSpace(notification.ThreadID) != strings.TrimSpace(threadID) || strings.TrimSpace(notification.TurnID) != strings.TrimSpace(turnID) {
				continue
			}
			if notification.Item.Type == "agentMessage" && strings.TrimSpace(notification.Item.Text) != "" {
				latestAgentMessage = strings.TrimSpace(notification.Item.Text)
			}
			if notification.Item.Type == "plan" && strings.TrimSpace(notification.Item.Text) != "" {
				latestPlan = append(latestPlan, codexAppServerTurnPlanStep{Step: notification.Item.Text, Status: "pending"})
			}
		case "turn/completed":
			var notification codexAppServerTurnCompletedNotification
			if err := json.Unmarshal(message.Params, &notification); err != nil {
				return codexAppServerWaitResult{}, fmt.Errorf("decode codex app server turn completion: %w", err)
			}
			if strings.TrimSpace(notification.ThreadID) != strings.TrimSpace(threadID) {
				continue
			}
			if strings.TrimSpace(notification.Turn.ID) != strings.TrimSpace(turnID) {
				continue
			}
			if len(notification.Turn.Items) == 0 && latestAgentMessage != "" {
				notification.Turn.Items = []codexAppServerThreadItem{{Type: "agentMessage", Text: latestAgentMessage, Phase: "final_answer"}}
			}
			notification.Turn.Plan = latestPlan
			notification.Turn.PlanNote = latestPlanNote
			return codexAppServerWaitResult{Turn: &notification.Turn}, nil
		}
	}
}

func (c *stdioCodexAppServerClient) WaitForThreadCompaction(ctx context.Context, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	sawMatchingStatus := false
	for {
		message, err := c.readMessage(ctx)
		if err != nil {
			return err
		}
		if message.Method == "error" {
			return fmt.Errorf("codex app server error notification: %s", strings.TrimSpace(string(message.Params)))
		}
		if strings.TrimSpace(message.Method) != "" && strings.TrimSpace(message.ID) != "" {
			pendingApproval, handled, err := c.decodePendingApproval(message)
			if err != nil {
				return err
			}
			if handled {
				return fmt.Errorf("codex app server requested approval while compacting thread %s via %s", threadID, pendingApproval.Method)
			}
			return c.sendRequestError(ctx, message.ID, -32601, "unsupported request from codex app server")
		}
		switch message.Method {
		case "thread/compacted":
			var notification codexAppServerThreadCompactedNotification
			if err := json.Unmarshal(message.Params, &notification); err != nil {
				return fmt.Errorf("decode codex app server thread compaction: %w", err)
			}
			if strings.TrimSpace(notification.ThreadID) == threadID {
				return nil
			}
		case "thread/status/changed":
			matched, status, err := decodeCodexAppServerThreadStatus(message.Params, threadID)
			if err != nil {
				return err
			}
			if !matched {
				continue
			}
			sawMatchingStatus = true
			if status == "idle" {
				return nil
			}
		case "turn/completed":
			if sawMatchingStatus {
				var notification codexAppServerTurnCompletedNotification
				if err := json.Unmarshal(message.Params, &notification); err != nil {
					return fmt.Errorf("decode codex app server turn completion: %w", err)
				}
				if strings.TrimSpace(notification.ThreadID) == threadID {
					return nil
				}
			}
		}
	}
}

func (c *stdioCodexAppServerClient) ResolveApproval(ctx context.Context, pending codexAppServerPendingApproval, decision ApprovalDecision) error {
	switch pending.Method {
	case "item/permissions/requestApproval":
		response := codexAppServerPermissionsApprovalResponse{Scope: "turn"}
		if decision == DecisionApprove {
			response.Permissions = pending.RequestedPermissions
		} else {
			response.Permissions = map[string]any{}
		}
		return c.sendRequestResponse(ctx, pending.RequestID, response)
	case "item/commandExecution/requestApproval":
		response := codexAppServerCommandExecutionApprovalResponse{Decision: "decline"}
		if decision == DecisionApprove {
			response.Decision = "accept"
		}
		return c.sendRequestResponse(ctx, pending.RequestID, response)
	case "item/fileChange/requestApproval":
		response := codexAppServerFileChangeApprovalResponse{Decision: "decline"}
		if decision == DecisionApprove {
			response.Decision = "accept"
		}
		return c.sendRequestResponse(ctx, pending.RequestID, response)
	default:
		return fmt.Errorf("unsupported codex app server approval method %q", pending.Method)
	}
}

func (c *stdioCodexAppServerClient) Close() error {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	_ = c.cmd.Process.Kill()
	return c.wait()
}

func (c *stdioCodexAppServerClient) wait() error {
	c.waitOnce.Do(func() {
		c.waitErr = c.cmd.Wait()
	})
	return c.waitErr
}

func (c *stdioCodexAppServerClient) call(ctx context.Context, method string, params any, result any) error {
	requestID := c.nextRequestID()
	payload := codexAppServerJSONRPC{JSONRPC: "2.0", ID: requestID, Method: method}
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal codex app server params for %s: %w", method, err)
		}
		payload.Params = encoded
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal codex app server request %s: %w", method, err)
	}
	c.writeMu.Lock()
	_, err = c.stdin.Write(append(encoded, '\n'))
	c.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("write codex app server request %s: %w", method, err)
	}
	for {
		message, err := c.readMessage(ctx)
		if err != nil {
			return err
		}
		if message.Method != "" {
			continue
		}
		if message.ID != requestID {
			continue
		}
		if message.Error != nil {
			return fmt.Errorf("codex app server %s error: %s", method, strings.TrimSpace(message.Error.Message))
		}
		if result != nil {
			if err := json.Unmarshal(message.Result, result); err != nil {
				return fmt.Errorf("decode codex app server %s result: %w", method, err)
			}
		}
		return nil
	}
}

func (c *stdioCodexAppServerClient) nextRequestID() string {
	c.nextID++
	return strconv.FormatInt(c.nextID, 10)
}

func (c *stdioCodexAppServerClient) decodePendingApproval(message codexAppServerJSONRPC) (*codexAppServerPendingApproval, bool, error) {
	if message.Method == "" || message.ID == "" {
		return nil, false, nil
	}
	switch message.Method {
	case "item/permissions/requestApproval":
		var params codexAppServerPermissionsRequestApprovalParams
		if err := json.Unmarshal(message.Params, &params); err != nil {
			return nil, false, fmt.Errorf("decode codex app server permissions approval request: %w", err)
		}
		return &codexAppServerPendingApproval{
			RequestID:            message.ID,
			Method:               message.Method,
			ThreadID:             params.ThreadID,
			TurnID:               params.TurnID,
			ItemID:               params.ItemID,
			Reason:               strings.TrimSpace(params.Reason),
			RequestedPermissions: params.Permissions,
		}, true, nil
	case "item/commandExecution/requestApproval":
		var params codexAppServerCommandExecutionRequestApprovalParams
		if err := json.Unmarshal(message.Params, &params); err != nil {
			return nil, false, fmt.Errorf("decode codex app server command approval request: %w", err)
		}
		return &codexAppServerPendingApproval{
			RequestID:            message.ID,
			Method:               message.Method,
			ThreadID:             params.ThreadID,
			TurnID:               params.TurnID,
			ItemID:               params.ItemID,
			ApprovalID:           params.ApprovalID,
			Command:              strings.TrimSpace(params.Command),
			Cwd:                  strings.TrimSpace(params.Cwd),
			Reason:               strings.TrimSpace(params.Reason),
			RequestedPermissions: params.AdditionalPermissions,
		}, true, nil
	case "item/fileChange/requestApproval":
		var params codexAppServerFileChangeRequestApprovalParams
		if err := json.Unmarshal(message.Params, &params); err != nil {
			return nil, false, fmt.Errorf("decode codex app server file-change approval request: %w", err)
		}
		return &codexAppServerPendingApproval{
			RequestID: message.ID,
			Method:    message.Method,
			ThreadID:  params.ThreadID,
			TurnID:    params.TurnID,
			ItemID:    params.ItemID,
			Reason:    strings.TrimSpace(params.Reason),
			GrantRoot: strings.TrimSpace(params.GrantRoot),
		}, true, nil
	}
	return nil, false, nil
}

func (c *stdioCodexAppServerClient) sendRequestResponse(ctx context.Context, requestID string, result any) error {
	payload := codexAppServerJSONRPC{JSONRPC: "2.0", ID: requestID, Result: mustMarshalJSON(result)}
	return c.sendJSONRPC(ctx, payload)
}

func (c *stdioCodexAppServerClient) sendNotification(ctx context.Context, method string, params any) error {
	payload := codexAppServerJSONRPC{JSONRPC: "2.0", Method: strings.TrimSpace(method)}
	if params != nil {
		payload.Params = mustMarshalJSON(params)
	}
	return c.sendJSONRPC(ctx, payload)
}

func (c *stdioCodexAppServerClient) sendRequestError(ctx context.Context, requestID string, code int, message string) error {
	payload := codexAppServerJSONRPC{
		JSONRPC: "2.0",
		ID:      requestID,
		Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{
			Code:    code,
			Message: message,
		},
	}
	return c.sendJSONRPC(ctx, payload)
}

func (c *stdioCodexAppServerClient) sendJSONRPC(ctx context.Context, payload codexAppServerJSONRPC) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal codex app server json-rpc message: %w", err)
	}
	c.writeMu.Lock()
	_, err = c.stdin.Write(append(encoded, '\n'))
	c.writeMu.Unlock()
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		return fmt.Errorf("write codex app server json-rpc message: %w", err)
	}
	return nil
}

func mustMarshalJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(encoded)
}

func (c *stdioCodexAppServerClient) readMessage(ctx context.Context) (codexAppServerJSONRPC, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return codexAppServerJSONRPC{}, err
		}
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				waitErr := c.wait()
				stderr := strings.TrimSpace(c.stderr.String())
				if stderr == "" && waitErr != nil {
					stderr = waitErr.Error()
				}
				if stderr == "" {
					stderr = "codex app server exited before returning a response"
				}
				return codexAppServerJSONRPC{}, errors.New(stderr)
			}
			return codexAppServerJSONRPC{}, fmt.Errorf("read codex app server response: %w", err)
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var message codexAppServerJSONRPC
		if err := json.Unmarshal(line, &message); err != nil {
			return codexAppServerJSONRPC{}, fmt.Errorf("decode codex app server message: %w", err)
		}
		return message, nil
	}
}

func latestCodexAppServerAgentMessage(items []codexAppServerThreadItem) string {
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if item.Type != "agentMessage" {
			continue
		}
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		return strings.TrimSpace(item.Text)
	}
	return ""
}

func codexAppServerTurnOutcome(turn codexAppServerTurn) (Outcome, error) {
	text := latestCodexAppServerAgentMessage(turn.Items)
	if strings.TrimSpace(text) == "" {
		if len(turn.Plan) > 0 || strings.TrimSpace(turn.PlanNote) != "" {
			return Outcome{}, nil
		}
		return Outcome{}, errors.New("codex app server completed turn without a final agent message")
	}
	return Outcome{Message: strings.TrimSpace(text)}, nil
}

func modelProviderForPreset(preset string) string {
	preset = strings.ToLower(strings.TrimSpace(preset))
	switch {
	case strings.Contains(preset, "openrouter"):
		return "openrouter"
	case strings.Contains(preset, "anthropic"):
		return "anthropic"
	case strings.Contains(preset, "ollama"):
		return "ollama"
	default:
		return "openai"
	}
}

func classifyCodexAppServerFailure(err error) codexAppServerFailureClass {
	if err == nil {
		return codexAppServerFailureNone
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(message, "unknown thread"):
		return codexAppServerFailureStaleThread
	case strings.Contains(message, "exited before returning a response"):
		return codexAppServerFailureTransientProcess
	case strings.Contains(message, "broken pipe"):
		return codexAppServerFailureTransientProcess
	case strings.Contains(message, "connection reset"):
		return codexAppServerFailureTransientProcess
	case strings.Contains(message, "eof"):
		return codexAppServerFailureTransientProcess
	case strings.Contains(message, "without a final agent message"):
		return codexAppServerFailureTransientProcess
	default:
		return codexAppServerFailureNone
	}
}

func codexAppServerApprovalResumeError(class codexAppServerFailureClass) error {
	switch class {
	case codexAppServerFailureStaleThread:
		return errors.New("Codex app-server lost the suspended approval turn because its native thread went stale. Retry the task or switch runtimes.")
	case codexAppServerFailureTransientProcess:
		return errors.New("Codex app-server lost the suspended approval turn because the app-server process disconnected. Retry the task or switch runtimes.")
	default:
		return errors.New("Codex app-server could not resume the suspended approval turn. Retry the task or switch runtimes.")
	}
}

func codexAppServerCompactionError(class codexAppServerFailureClass) error {
	switch class {
	case codexAppServerFailureStaleThread:
		return errors.New("Codex app-server could not compact the task because its native thread went stale. Retry the task or switch runtimes.")
	case codexAppServerFailureTransientProcess:
		return errors.New("Codex app-server could not compact the task because the app-server process disconnected. Retry the task or switch runtimes.")
	default:
		return errors.New("Codex app-server could not compact the task. Retry the task or switch runtimes.")
	}
}

func decodeCodexAppServerThreadStatus(params json.RawMessage, threadID string) (bool, string, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(params, &payload); err != nil {
		return false, "", fmt.Errorf("decode codex app server thread status change: %w", err)
	}
	if id := strings.TrimSpace(decodeJSONString(payload["threadId"])); id != threadID {
		return false, "", nil
	}
	if status := strings.TrimSpace(decodeJSONString(payload["status"])); status != "" {
		return true, strings.ToLower(status), nil
	}
	if rawThread, ok := payload["thread"]; ok && len(bytes.TrimSpace(rawThread)) > 0 {
		var thread codexAppServerThread
		if err := json.Unmarshal(rawThread, &thread); err != nil {
			return false, "", fmt.Errorf("decode codex app server thread status change thread: %w", err)
		}
		return true, strings.ToLower(strings.TrimSpace(decodeJSONString(thread.Status))), nil
	}
	return true, "", nil
}

func decodeJSONString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return strings.TrimSpace(string(raw))
}

func codexAppServerPendingApprovalToApprovalRequest(pending codexAppServerPendingApproval) *ApprovalRequest {
	approval := &ApprovalRequest{
		ID:                "runtime-approval:" + strings.TrimSpace(pending.RequestID),
		Title:             "Runtime approval required",
		Risk:              RiskMedium,
		ContinuationToken: strings.TrimSpace(pending.RequestID),
	}
	switch pending.Method {
	case "item/commandExecution/requestApproval":
		approval.Kind = ApprovalCommand
		approval.Title = "Approve command execution"
		approval.Command = strings.TrimSpace(pending.Command)
		parts := make([]string, 0, 4)
		if reason := strings.TrimSpace(pending.Reason); reason != "" {
			parts = append(parts, reason)
		}
		if cwd := strings.TrimSpace(pending.Cwd); cwd != "" {
			parts = append(parts, "cwd: "+cwd)
		}
		if perms := formatCodexAppServerPermissionSummary(pending.RequestedPermissions); perms != "" {
			parts = append(parts, "additional permissions: "+perms)
		}
		approval.Summary = strings.Join(parts, "\n")
	case "item/fileChange/requestApproval":
		approval.Kind = ApprovalPatch
		approval.Title = "Approve file changes"
		parts := make([]string, 0, 3)
		if reason := strings.TrimSpace(pending.Reason); reason != "" {
			parts = append(parts, reason)
		}
		if root := strings.TrimSpace(pending.GrantRoot); root != "" {
			parts = append(parts, "grant root: "+root)
		}
		approval.Summary = strings.Join(parts, "\n")
	case "item/permissions/requestApproval":
		approval.Kind = ApprovalPlan
		approval.Title = "Approve additional permissions"
		parts := make([]string, 0, 2)
		if reason := strings.TrimSpace(pending.Reason); reason != "" {
			parts = append(parts, reason)
		}
		if perms := formatCodexAppServerPermissionSummary(pending.RequestedPermissions); perms != "" {
			parts = append(parts, "requested permissions: "+perms)
		}
		approval.Summary = strings.Join(parts, "\n")
	}
	return approval
}

func formatCodexAppServerPermissionSummary(value any) string {
	if value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(encoded))
}

func structuredToOutcome(input shuttleStructuredResponse) (Outcome, error) {
	outcome := Outcome{Message: strings.TrimSpace(input.Message)}
	if len(input.PlanStepStatuses) > 0 {
		statuses := make([]PlanStepStatus, 0, len(input.PlanStepStatuses))
		for _, status := range input.PlanStepStatuses {
			stepStatus, err := normalizeCodexPlanStepStatus(status)
			if err != nil {
				return Outcome{}, err
			}
			statuses = append(statuses, stepStatus)
		}
		outcome.PlanStatuses = statuses
	}
	planSummary := strings.TrimSpace(input.PlanSummary)
	if planSummary != "" || len(input.PlanSteps) > 0 {
		steps := make([]string, 0, len(input.PlanSteps))
		for _, step := range input.PlanSteps {
			step = strings.TrimSpace(step)
			if step != "" {
				steps = append(steps, step)
			}
		}
		outcome.Plan = &Plan{Summary: planSummary, Steps: steps}
	}
	proposal, err := parseStructuredProposal(input)
	if err != nil {
		return Outcome{}, err
	}
	outcome.Proposal = proposal
	approval, err := parseStructuredApproval(input)
	if err != nil {
		return Outcome{}, err
	}
	outcome.Approval = approval
	return outcome, nil
}

func applyCodexAppServerTurnPlanToOutcome(outcome Outcome, plan []codexAppServerTurnPlanStep, note string) Outcome {
	steps := make([]string, 0, len(plan))
	statuses := make([]PlanStepStatus, 0, len(plan))
	for _, step := range plan {
		stepText := strings.TrimSpace(step.Step)
		if stepText == "" {
			continue
		}
		steps = append(steps, stepText)
		stepStatus, err := normalizeCodexPlanStepStatus(step.Status)
		if err != nil {
			stepStatus = PlanStepPending
		}
		statuses = append(statuses, stepStatus)
	}
	if len(steps) == 0 {
		return outcome
	}

	if outcome.Plan == nil {
		outcome.Plan = &Plan{}
	}
	if len(outcome.Plan.Steps) == 0 {
		outcome.Plan.Steps = steps
	}
	if strings.TrimSpace(outcome.Plan.Summary) == "" {
		outcome.Plan.Summary = strings.TrimSpace(note)
	}
	if len(outcome.PlanStatuses) == 0 {
		outcome.PlanStatuses = statuses
	}
	return outcome
}

func normalizeCodexPlanStepStatus(status string) (PlanStepStatus, error) {
	switch PlanStepStatus(strings.TrimSpace(status)) {
	case "", "pending":
		if strings.TrimSpace(status) == "" {
			return "", fmt.Errorf("unsupported empty plan status")
		}
		return PlanStepPending, nil
	case "in_progress", "inProgress":
		return PlanStepInProgress, nil
	case "done", "completed":
		return PlanStepDone, nil
	default:
		return "", fmt.Errorf("unsupported plan status %q", status)
	}
}

func parseStructuredProposal(input shuttleStructuredResponse) (*Proposal, error) {
	if strings.TrimSpace(input.ProposalKind) == "" &&
		strings.TrimSpace(input.ProposalCommand) == "" &&
		strings.TrimSpace(input.ProposalKeys) == "" &&
		strings.TrimSpace(input.ProposalPatch) == "" &&
		strings.TrimSpace(input.ProposalEditPath) == "" &&
		strings.TrimSpace(input.ProposalEditOperation) == "" &&
		strings.TrimSpace(input.ProposalEditAnchorText) == "" &&
		strings.TrimSpace(input.ProposalEditOldText) == "" &&
		strings.TrimSpace(input.ProposalEditNewText) == "" &&
		input.ProposalEditStartLine == 0 &&
		input.ProposalEditEndLine == 0 &&
		strings.TrimSpace(input.ProposalDescription) == "" {
		return nil, nil
	}
	kind := ProposalKind(strings.TrimSpace(input.ProposalKind))
	switch kind {
	case "":
		switch {
		case strings.TrimSpace(input.ProposalCommand) != "":
			kind = ProposalCommand
		case strings.TrimSpace(input.ProposalKeys) != "":
			kind = ProposalKeys
		case strings.TrimSpace(input.ProposalPatch) != "":
			kind = ProposalPatch
		case strings.TrimSpace(input.ProposalEditOperation) != "" || strings.TrimSpace(input.ProposalEditPath) != "":
			kind = ProposalEdit
		case strings.EqualFold(strings.TrimSpace(input.ProposalKind), string(ProposalInspectContext)):
			kind = ProposalInspectContext
		default:
			kind = ProposalAnswer
		}
	case ProposalAnswer, ProposalCommand, ProposalKeys, ProposalPatch, ProposalEdit, ProposalInspectContext:
	default:
		return nil, fmt.Errorf("unsupported proposal kind %q", input.ProposalKind)
	}
	var edit *EditIntent
	if kind == ProposalEdit {
		operation := EditOperation(strings.TrimSpace(input.ProposalEditOperation))
		switch operation {
		case EditInsertBefore, EditInsertAfter, EditReplaceExact, EditReplaceRange:
		default:
			return nil, fmt.Errorf("unsupported edit operation %q", input.ProposalEditOperation)
		}
		edit = &EditIntent{
			Target:     normalizePatchTarget(input.ProposalPatchTarget),
			Path:       strings.TrimSpace(input.ProposalEditPath),
			Operation:  operation,
			AnchorText: input.ProposalEditAnchorText,
			OldText:    input.ProposalEditOldText,
			NewText:    input.ProposalEditNewText,
			StartLine:  input.ProposalEditStartLine,
			EndLine:    input.ProposalEditEndLine,
		}
	}
	return &Proposal{
		Kind:        kind,
		Command:     strings.TrimSpace(input.ProposalCommand),
		Keys:        input.ProposalKeys,
		Patch:       strings.TrimSpace(input.ProposalPatch),
		PatchTarget: normalizePatchTarget(input.ProposalPatchTarget),
		Edit:        edit,
		Description: strings.TrimSpace(input.ProposalDescription),
	}, nil
}

func parseStructuredApproval(input shuttleStructuredResponse) (*ApprovalRequest, error) {
	if strings.TrimSpace(input.ApprovalKind) == "" &&
		strings.TrimSpace(input.ApprovalTitle) == "" &&
		strings.TrimSpace(input.ApprovalSummary) == "" &&
		strings.TrimSpace(input.ApprovalCommand) == "" &&
		strings.TrimSpace(input.ApprovalPatch) == "" {
		return nil, nil
	}
	kind := ApprovalKind(strings.TrimSpace(input.ApprovalKind))
	switch kind {
	case "":
		switch {
		case strings.TrimSpace(input.ApprovalCommand) != "":
			kind = ApprovalCommand
		case strings.TrimSpace(input.ApprovalPatch) != "":
			kind = ApprovalPatch
		default:
			kind = ApprovalPlan
		}
	case ApprovalCommand, ApprovalPatch, ApprovalPlan:
	default:
		return nil, fmt.Errorf("unsupported approval kind %q", input.ApprovalKind)
	}
	risk := RiskLevel(strings.TrimSpace(input.ApprovalRisk))
	switch risk {
	case "":
		risk = RiskMedium
	case RiskLow, RiskMedium, RiskHigh:
	default:
		return nil, fmt.Errorf("unsupported approval risk %q", input.ApprovalRisk)
	}
	return &ApprovalRequest{
		ID:          "approval-runtime",
		Kind:        kind,
		Title:       strings.TrimSpace(input.ApprovalTitle),
		Summary:     strings.TrimSpace(input.ApprovalSummary),
		Command:     strings.TrimSpace(input.ApprovalCommand),
		Patch:       strings.TrimSpace(input.ApprovalPatch),
		PatchTarget: normalizePatchTarget(input.ApprovalPatchTarget),
		Risk:        risk,
	}, nil
}

func normalizePatchTarget(value string) PatchTarget {
	switch PatchTarget(strings.TrimSpace(value)) {
	case PatchTargetRemoteShell:
		return PatchTargetRemoteShell
	default:
		return PatchTargetLocalWorkspace
	}
}

func shuttleAgentResponseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message":                   map[string]any{"type": "string"},
			"plan_summary":              map[string]any{"type": "string"},
			"plan_steps":                map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"plan_step_statuses":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"proposal_kind":             map[string]any{"type": "string"},
			"proposal_command":          map[string]any{"type": "string"},
			"proposal_keys":             map[string]any{"type": "string"},
			"proposal_patch":            map[string]any{"type": "string"},
			"proposal_patch_target":     map[string]any{"type": "string"},
			"proposal_edit_path":        map[string]any{"type": "string"},
			"proposal_edit_operation":   map[string]any{"type": "string"},
			"proposal_edit_anchor_text": map[string]any{"type": "string"},
			"proposal_edit_old_text":    map[string]any{"type": "string"},
			"proposal_edit_new_text":    map[string]any{"type": "string"},
			"proposal_edit_start_line":  map[string]any{"type": "integer"},
			"proposal_edit_end_line":    map[string]any{"type": "integer"},
			"proposal_description":      map[string]any{"type": "string"},
			"approval_kind":             map[string]any{"type": "string"},
			"approval_title":            map[string]any{"type": "string"},
			"approval_summary":          map[string]any{"type": "string"},
			"approval_command":          map[string]any{"type": "string"},
			"approval_patch":            map[string]any{"type": "string"},
			"approval_patch_target":     map[string]any{"type": "string"},
			"approval_risk":             map[string]any{"type": "string"},
		},
		"required": []string{
			"message",
			"plan_summary",
			"plan_steps",
			"plan_step_statuses",
			"proposal_kind",
			"proposal_command",
			"proposal_keys",
			"proposal_patch",
			"proposal_patch_target",
			"proposal_edit_path",
			"proposal_edit_operation",
			"proposal_edit_anchor_text",
			"proposal_edit_old_text",
			"proposal_edit_new_text",
			"proposal_edit_start_line",
			"proposal_edit_end_line",
			"proposal_description",
			"approval_kind",
			"approval_title",
			"approval_summary",
			"approval_command",
			"approval_patch",
			"approval_patch_target",
			"approval_risk",
		},
		"additionalProperties": false,
	}
}

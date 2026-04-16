package agentruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type stubHost struct {
	responses     []Outcome
	requests      []Request
	respondCalls  int
	inspectCalls  int
	validateError error
}

func (s *stubHost) Respond(_ context.Context, req Request) (Outcome, error) {
	s.requests = append(s.requests, req)
	if s.respondCalls >= len(s.responses) {
		return Outcome{}, errors.New("unexpected respond call")
	}
	response := s.responses[s.respondCalls]
	s.respondCalls++
	return response, nil
}

func (s *stubHost) InspectContext(_ context.Context, _ Request) error {
	s.inspectCalls++
	return nil
}

func (s *stubHost) SynthesizeStructuredEdit(_ context.Context, outcome Outcome) (Outcome, error) {
	return outcome, nil
}

func (s *stubHost) ValidatePatch(_ context.Context, _ string, _ string) error {
	return s.validateError
}

type stubCodexSDKHandler struct {
	responses []Outcome
	calls     int
}

func (s *stubCodexSDKHandler) Respond(_ context.Context, _ Host, _ Request) (Outcome, error) {
	if s.calls >= len(s.responses) {
		return Outcome{}, errors.New("unexpected codex handler call")
	}
	response := s.responses[s.calls]
	s.calls++
	return response, nil
}

type stubCodexAppServerHandler struct {
	responses []Outcome
	calls     int
}

func (s *stubCodexAppServerHandler) Respond(_ context.Context, _ Request) (Outcome, error) {
	if s.calls >= len(s.responses) {
		return Outcome{}, errors.New("unexpected codex app server handler call")
	}
	response := s.responses[s.calls]
	s.calls++
	return response, nil
}

type fakeCodexAppServerClient struct {
	startThreadCalls int
	startTurnCalls   []codexAppServerTurnStartParams
	resolveCalls     []ApprovalDecision
	threadID         string
	turnPlan         []codexAppServerTurnPlanStep
	turnPlanNote     string
	turnResponse     shuttleStructuredResponse
	pendingApproval  *codexAppServerPendingApproval
	startTurnErr     error
	waitErr          error
}

func (f *fakeCodexAppServerClient) Initialize(context.Context) error { return nil }

func (f *fakeCodexAppServerClient) StartThread(_ context.Context, _ codexAppServerThreadStartParams) (codexAppServerThreadStartResult, error) {
	f.startThreadCalls++
	threadID := strings.TrimSpace(f.threadID)
	if threadID == "" {
		threadID = fmt.Sprintf("thread-%d", f.startThreadCalls)
	}
	return codexAppServerThreadStartResult{
		Thread: codexAppServerThread{ID: threadID},
	}, nil
}

func (f *fakeCodexAppServerClient) StartTurn(_ context.Context, params codexAppServerTurnStartParams) (codexAppServerTurnStartResult, error) {
	f.startTurnCalls = append(f.startTurnCalls, params)
	if f.startTurnErr != nil {
		return codexAppServerTurnStartResult{}, f.startTurnErr
	}
	return codexAppServerTurnStartResult{
		Turn: codexAppServerTurn{ID: "turn-1"},
	}, nil
}

func (f *fakeCodexAppServerClient) WaitForTurnCompletion(context.Context, string, string) (codexAppServerWaitResult, error) {
	if f.waitErr != nil {
		return codexAppServerWaitResult{}, f.waitErr
	}
	if f.pendingApproval != nil {
		approval := *f.pendingApproval
		f.pendingApproval = nil
		return codexAppServerWaitResult{PendingApproval: &approval}, nil
	}
	turn := codexAppServerTurn{
		ID:       "turn-1",
		Status:   "completed",
		Plan:     append([]codexAppServerTurnPlanStep(nil), f.turnPlan...),
		PlanNote: strings.TrimSpace(f.turnPlanNote),
		Items: []codexAppServerThreadItem{{
			Type: "agentMessage",
			Text: `{"message":"` + f.turnResponse.Message + `","plan_summary":"","plan_steps":[],"plan_step_statuses":[],"proposal_kind":"","proposal_command":"","proposal_keys":"","proposal_patch":"","proposal_patch_target":"","proposal_edit_path":"","proposal_edit_operation":"","proposal_edit_anchor_text":"","proposal_edit_old_text":"","proposal_edit_new_text":"","proposal_edit_start_line":0,"proposal_edit_end_line":0,"proposal_description":"","approval_kind":"","approval_title":"","approval_summary":"","approval_command":"","approval_patch":"","approval_patch_target":"","approval_risk":""}`,
		}},
	}
	return codexAppServerWaitResult{Turn: &turn}, nil
}

func (f *fakeCodexAppServerClient) ResolveApproval(_ context.Context, _ codexAppServerPendingApproval, decision ApprovalDecision) error {
	f.resolveCalls = append(f.resolveCalls, decision)
	return nil
}

func (f *fakeCodexAppServerClient) Close() error { return nil }

func TestBuiltinRuntimeReplaysAfterInspectContext(t *testing.T) {
	host := &stubHost{
		responses: []Outcome{
			{Proposal: &Proposal{Kind: "inspect_context"}},
			{Message: "stable now"},
		},
	}

	outcome, err := NewBuiltin().Handle(context.Background(), host, Request{
		Kind:          RequestUserTurn,
		Prompt:        "help",
		InspectBudget: 2,
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if host.inspectCalls != 1 {
		t.Fatalf("expected one inspect call, got %d", host.inspectCalls)
	}
	if strings.TrimSpace(outcome.Message) != "stable now" {
		t.Fatalf("expected final message after inspect, got %#v", outcome)
	}
}

func TestBuiltinRuntimeRepairsInvalidPatchOnce(t *testing.T) {
	host := &stubHost{
		responses: []Outcome{
			{Proposal: &Proposal{Kind: "patch", Patch: "bad patch", PatchTarget: "local_workspace"}},
			{Message: "fixed", Proposal: &Proposal{Kind: "patch", Patch: "still bad", PatchTarget: "local_workspace"}},
		},
		validateError: errors.New("fragment header miscounts lines"),
	}

	outcome, err := NewBuiltin().Handle(context.Background(), host, Request{
		Kind:   RequestUserTurn,
		Prompt: "fix it",
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if host.respondCalls != 2 {
		t.Fatalf("expected repair retry, got %d calls", host.respondCalls)
	}
	if outcome.Proposal != nil {
		t.Fatalf("expected invalid repaired proposal to be dropped, got %#v", outcome.Proposal)
	}
	if !strings.Contains(outcome.Message, invalidPatchProposalNotice) {
		t.Fatalf("expected invalid patch notice, got %#v", outcome.Message)
	}
}

func TestNewSelectedRuntimeBuiltinReturnsBuiltinRuntime(t *testing.T) {
	runtime := NewSelectedRuntime(RuntimeMetadata{Type: RuntimeBuiltin})
	if _, ok := runtime.(BuiltinRuntime); !ok {
		t.Fatalf("expected builtin runtime, got %T", runtime)
	}
}

func TestNewSelectedRuntimePiIsRejected(t *testing.T) {
	runtime := NewSelectedRuntime(RuntimeMetadata{
		Type:           RuntimePi,
		Command:        "/usr/local/bin/pi",
		ProviderPreset: "openai",
		Model:          "gpt-5",
	})

	_, err := runtime.Handle(context.Background(), &stubHost{}, Request{Kind: RequestUserTurn, Prompt: "help"})
	if err == nil || !strings.Contains(err.Error(), "not yet supported as an authoritative Shuttle runtime") {
		t.Fatalf("expected pi runtime rejection, got %v", err)
	}
}

func TestNewSelectedRuntimeCodexAppServerReturnsAdapter(t *testing.T) {
	runtime := NewSelectedRuntime(RuntimeMetadata{
		Type:           RuntimeCodexAppServer,
		Command:        "/usr/local/bin/codex",
		ProviderPreset: "openai",
		Model:          "gpt-5",
	})

	if _, ok := runtime.(codexAppServerRuntime); !ok {
		t.Fatalf("expected codex app server runtime adapter, got %T", runtime)
	}
}

func TestNewCodexSDKRuntimeUsesCodexSDKAdapter(t *testing.T) {
	runtime := NewCodexSDKRuntime(RuntimeMetadata{Type: RuntimeCodexSDK, Command: "codex"}, nil)
	if _, ok := runtime.(codexSDKRuntime); !ok {
		t.Fatalf("expected codex sdk runtime adapter, got %T", runtime)
	}
}

func TestCodexSDKRuntimeUsesInjectedHandler(t *testing.T) {
	host := &stubHost{}
	handler := &stubCodexSDKHandler{responses: []Outcome{{Message: "from handler"}}}
	runtime := NewCodexSDKRuntime(RuntimeMetadata{Type: RuntimeCodexSDK, Command: "codex"}, handler)

	outcome, err := runtime.Handle(context.Background(), host, Request{Kind: RequestUserTurn, Prompt: "help"})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if handler.calls != 1 {
		t.Fatalf("expected codex handler call, got %d", handler.calls)
	}
	if host.respondCalls != 0 {
		t.Fatalf("expected host respond not to be called, got %d", host.respondCalls)
	}
	if outcome.Message != "from handler" {
		t.Fatalf("expected handler response, got %#v", outcome)
	}
}

func TestCodexAppServerRuntimeUsesInjectedHandler(t *testing.T) {
	host := &stubHost{}
	handler := &stubCodexAppServerHandler{responses: []Outcome{{Message: "from app server"}}}
	runtime := NewCodexAppServerRuntime(RuntimeMetadata{Type: RuntimeCodexAppServer, Command: "codex"}, handler)

	outcome, err := runtime.Handle(context.Background(), host, Request{Kind: RequestUserTurn, Prompt: "help"})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if handler.calls != 1 {
		t.Fatalf("expected codex app server handler call, got %d", handler.calls)
	}
	if host.respondCalls != 0 {
		t.Fatalf("expected host respond not to be called, got %d", host.respondCalls)
	}
	if outcome.Message != "from app server" {
		t.Fatalf("expected handler response, got %#v", outcome)
	}
}

func TestCodexAppServerDefaultHandlerPersistsThreadAcrossTurns(t *testing.T) {
	client := &fakeCodexAppServerClient{threadID: "thread-a", turnResponse: shuttleStructuredResponse{Message: "first"}}
	handler := &codexAppServerDefaultHandler{
		meta:          RuntimeMetadata{Type: RuntimeCodexAppServer, Command: "codex"},
		clientFactory: func(context.Context) (codexAppServerClient, error) { return client, nil },
		threads:       map[string]string{},
	}

	req := Request{Kind: RequestUserTurn, Prompt: "help", SessionName: "shuttle", TaskID: "task-a"}
	first, err := handler.Respond(context.Background(), req)
	if err != nil {
		t.Fatalf("first Respond() error = %v", err)
	}
	second, err := handler.Respond(context.Background(), req)
	if err != nil {
		t.Fatalf("second Respond() error = %v", err)
	}

	if first.Message != "first" || second.Message != "first" {
		t.Fatalf("expected in-memory thread reuse responses, got first=%q second=%q", first.Message, second.Message)
	}
	if client.startThreadCalls != 1 {
		t.Fatalf("expected one thread start on the reused client, got %d", client.startThreadCalls)
	}
	if len(client.startTurnCalls) != 2 || client.startTurnCalls[1].ThreadID != "thread-a" {
		t.Fatalf("expected second turn to reuse thread-a on the same client, got %#v", client.startTurnCalls)
	}
}

func TestCodexAppServerDefaultHandlerUsesTurnPlanWhenStructuredPlanMissing(t *testing.T) {
	client := &fakeCodexAppServerClient{threadID: "thread-a", turnResponse: shuttleStructuredResponse{Message: "ready"}, turnPlan: []codexAppServerTurnPlanStep{{Step: "Inspect workspace", Status: "in_progress"}, {Step: "Run tests", Status: "completed"}}, turnPlanNote: "Check current task state"}
	handler := &codexAppServerDefaultHandler{
		meta:             RuntimeMetadata{Type: RuntimeCodexAppServer, Command: "codex"},
		clientFactory:    func(context.Context) (codexAppServerClient, error) { return client, nil },
		threads:          map[string]string{},
		pendingApprovals: map[string]codexAppServerPendingApproval{},
	}

	outcome, err := handler.Respond(context.Background(), Request{Kind: RequestUserTurn, Prompt: "help", SessionName: "shuttle", TaskID: "task-a"})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if outcome.Plan == nil {
		t.Fatalf("expected fallback plan from turn update")
	}
	if len(outcome.Plan.Steps) != 2 {
		t.Fatalf("expected two plan steps, got %d", len(outcome.Plan.Steps))
	}
	if outcome.Plan.Steps[0] != "Inspect workspace" || outcome.Plan.Steps[1] != "Run tests" {
		t.Fatalf("unexpected plan steps: %#v", outcome.Plan.Steps)
	}
	if strings.TrimSpace(outcome.Plan.Summary) != "Check current task state" {
		t.Fatalf("expected plan summary from turn note, got %#v", outcome.Plan.Summary)
	}
	if len(outcome.PlanStatuses) != 2 {
		t.Fatalf("expected two plan statuses, got %d", len(outcome.PlanStatuses))
	}
	if outcome.PlanStatuses[0] != PlanStepInProgress || outcome.PlanStatuses[1] != PlanStepDone {
		t.Fatalf("unexpected plan statuses: %#v", outcome.PlanStatuses)
	}
}

func TestCodexAppServerDefaultHandlerSuspendsForRuntimeApprovalAndResumesTurn(t *testing.T) {
	client := &fakeCodexAppServerClient{
		threadID: "thread-a",
		pendingApproval: &codexAppServerPendingApproval{
			RequestID: "req-1",
			Method:    "item/commandExecution/requestApproval",
			ThreadID:  "thread-a",
			TurnID:    "turn-1",
			ItemID:    "item-1",
			Command:   "ls",
			Cwd:       "/tmp",
			Reason:    "need to inspect the workspace",
		},
		turnResponse: shuttleStructuredResponse{Message: "done"},
	}
	handler := &codexAppServerDefaultHandler{
		meta:             RuntimeMetadata{Type: RuntimeCodexAppServer, Command: "codex"},
		clientFactory:    func(context.Context) (codexAppServerClient, error) { return client, nil },
		threads:          map[string]string{},
		pendingApprovals: map[string]codexAppServerPendingApproval{},
	}

	req := Request{Kind: RequestUserTurn, Prompt: "help", SessionName: "shuttle", TaskID: "task-a"}
	outcome, err := handler.Respond(context.Background(), req)
	if err != nil {
		t.Fatalf("Respond() approval phase error = %v", err)
	}
	if outcome.Approval == nil {
		t.Fatalf("expected runtime-managed approval outcome")
	}
	if outcome.Approval.Command != "ls" || outcome.Approval.ContinuationToken != "req-1" {
		t.Fatalf("unexpected approval payload: %#v", outcome.Approval)
	}

	resolved, err := handler.Respond(context.Background(), Request{
		Kind:             RequestResolveApproval,
		SessionName:      "shuttle",
		TaskID:           "task-a",
		ApprovalDecision: DecisionApprove,
		Approval:         outcome.Approval,
	})
	if err != nil {
		t.Fatalf("Respond() resolve phase error = %v", err)
	}
	if resolved.Message != "done" {
		t.Fatalf("expected resumed turn message, got %#v", resolved)
	}
	if len(client.resolveCalls) != 1 || client.resolveCalls[0] != DecisionApprove {
		t.Fatalf("expected one approval resolution call, got %#v", client.resolveCalls)
	}
}

type closeBuffer struct {
	bytes.Buffer
}

func (b *closeBuffer) Close() error { return nil }

func TestStdioCodexAppServerClientInitializeSendsInitializedNotification(t *testing.T) {
	buffer := &closeBuffer{}
	client := &stdioCodexAppServerClient{
		stdin:  buffer,
		reader: bufio.NewReader(strings.NewReader(`{"jsonrpc":"2.0","id":"1","result":{}}` + "\n")),
	}
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	frames := bytes.Split(bytes.TrimSpace(buffer.Bytes()), []byte("\n"))
	if len(frames) != 2 {
		t.Fatalf("expected initialize request plus initialized notification, got %d frame(s)", len(frames))
	}
	var request codexAppServerJSONRPC
	if err := json.Unmarshal(frames[0], &request); err != nil {
		t.Fatalf("decode initialize request: %v", err)
	}
	if request.Method != "initialize" {
		t.Fatalf("expected initialize method, got %q", request.Method)
	}
	var notification codexAppServerJSONRPC
	if err := json.Unmarshal(frames[1], &notification); err != nil {
		t.Fatalf("decode initialized notification: %v", err)
	}
	if notification.Method != "initialized" {
		t.Fatalf("expected initialized notification, got %q", notification.Method)
	}
}

func TestStdioCodexAppServerClientDecodesPendingApprovalRequests(t *testing.T) {
	requestID := "9"

	client := &stdioCodexAppServerClient{}
	request := codexAppServerJSONRPC{JSONRPC: "2.0", ID: requestID, Method: "item/commandExecution/requestApproval", Params: []byte(`{"itemId":"item-1","threadId":"thread-1","turnId":"turn-1","command":"ls","cwd":"/tmp"}`)}
	pending, handled, err := client.decodePendingApproval(request)
	if err != nil {
		t.Fatalf("decodePendingApproval() error = %v", err)
	}
	if !handled || pending == nil {
		t.Fatalf("expected handled command approval request, got handled=%v pending=%#v", handled, pending)
	}
	if pending.RequestID != requestID || pending.Command != "ls" || pending.Cwd != "/tmp" {
		t.Fatalf("unexpected pending approval: %#v", pending)
	}
}

func TestStdioCodexAppServerClientResolveApprovalWritesExpectedResponses(t *testing.T) {
	buffer := &closeBuffer{}
	client := &stdioCodexAppServerClient{stdin: buffer}

	if err := client.ResolveApproval(context.Background(), codexAppServerPendingApproval{
		RequestID:            "req-1",
		Method:               "item/permissions/requestApproval",
		RequestedPermissions: map[string]any{"network": map[string]any{"enabled": true}},
	}, DecisionApprove); err != nil {
		t.Fatalf("ResolveApproval(permission) error = %v", err)
	}
	if err := client.ResolveApproval(context.Background(), codexAppServerPendingApproval{
		RequestID: "req-2",
		Method:    "item/commandExecution/requestApproval",
	}, DecisionReject); err != nil {
		t.Fatalf("ResolveApproval(command) error = %v", err)
	}

	frames := bytes.Split(bytes.TrimSpace(buffer.Bytes()), []byte("\n"))
	if len(frames) != 2 {
		t.Fatalf("expected two approval responses, got %d", len(frames))
	}
	var permissionResponse codexAppServerJSONRPC
	if err := json.Unmarshal(frames[0], &permissionResponse); err != nil {
		t.Fatalf("decode permission response: %v", err)
	}
	if permissionResponse.ID != "req-1" {
		t.Fatalf("expected permission response id req-1, got %q", permissionResponse.ID)
	}
	var commandResponse codexAppServerJSONRPC
	if err := json.Unmarshal(frames[1], &commandResponse); err != nil {
		t.Fatalf("decode command response: %v", err)
	}
	if commandResponse.ID != "req-2" {
		t.Fatalf("expected command response id req-2, got %q", commandResponse.ID)
	}
	var decision struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(commandResponse.Result, &decision); err != nil {
		t.Fatalf("decode command decision: %v", err)
	}
	if decision.Decision != "decline" {
		t.Fatalf("expected declined command approval, got %q", decision.Decision)
	}
}

func TestCodexAppServerDefaultHandlerSeparatesBindingsByTask(t *testing.T) {
	client := &fakeCodexAppServerClient{threadID: "thread-a", turnResponse: shuttleStructuredResponse{Message: "task"}}
	handler := &codexAppServerDefaultHandler{
		meta:             RuntimeMetadata{Type: RuntimeCodexAppServer, Command: "codex"},
		clientFactory:    func(context.Context) (codexAppServerClient, error) { return client, nil },
		threads:          map[string]string{},
		pendingApprovals: map[string]codexAppServerPendingApproval{},
	}

	if _, err := handler.Respond(context.Background(), Request{Kind: RequestUserTurn, Prompt: "help", SessionName: "shuttle", TaskID: "task-a"}); err != nil {
		t.Fatalf("Respond(task-a) error = %v", err)
	}
	if _, err := handler.Respond(context.Background(), Request{Kind: RequestUserTurn, Prompt: "help", SessionName: "shuttle", TaskID: "task-b"}); err != nil {
		t.Fatalf("Respond(task-b) error = %v", err)
	}
	if client.startThreadCalls != 2 {
		t.Fatalf("expected a fresh thread per task on the shared client, got %d", client.startThreadCalls)
	}
}

func TestCodexAppServerDefaultHandlerRetriesSameTurnAfterStaleThreadStartFailure(t *testing.T) {
	staleClient := &fakeCodexAppServerClient{startTurnErr: errors.New("unknown thread")}
	freshClient := &fakeCodexAppServerClient{threadID: "thread-fresh", turnResponse: shuttleStructuredResponse{Message: "recovered"}}
	clients := []codexAppServerClient{staleClient, freshClient}
	handler := &codexAppServerDefaultHandler{
		meta: RuntimeMetadata{Type: RuntimeCodexAppServer, Command: "codex"},
		clientFactory: func(context.Context) (codexAppServerClient, error) {
			client := clients[0]
			clients = clients[1:]
			return client, nil
		},
		threads:          map[string]string{"shuttle\x00task-a": "thread-stale"},
		pendingApprovals: map[string]codexAppServerPendingApproval{},
	}

	outcome, err := handler.Respond(context.Background(), Request{Kind: RequestUserTurn, Prompt: "help", SessionName: "shuttle", TaskID: "task-a"})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if outcome.Message != "recovered" {
		t.Fatalf("expected recovered response, got %#v", outcome)
	}
	if outcome.ModelInfo == nil || !strings.Contains(outcome.ModelInfo.RuntimeFailureReason, "stale Codex app-server thread") {
		t.Fatalf("expected recovery note in model info, got %#v", outcome.ModelInfo)
	}
	if staleClient.startThreadCalls != 0 {
		t.Fatalf("expected stale client to try stored thread only, got %d thread starts", staleClient.startThreadCalls)
	}
	if freshClient.startThreadCalls != 1 {
		t.Fatalf("expected fresh client to create replacement thread, got %d", freshClient.startThreadCalls)
	}
	if len(freshClient.startTurnCalls) != 1 || freshClient.startTurnCalls[0].ThreadID != "thread-fresh" {
		t.Fatalf("expected replacement thread turn, got %#v", freshClient.startTurnCalls)
	}
	threadID, ok := handler.threadFor(Request{SessionName: "shuttle", TaskID: "task-a"})
	if !ok || threadID != "thread-fresh" {
		t.Fatalf("expected refreshed in-memory binding, got ok=%v thread=%q", ok, threadID)
	}
}

func TestCodexAppServerDefaultHandlerRetriesSameTurnAfterStaleThreadWaitFailure(t *testing.T) {
	staleClient := &fakeCodexAppServerClient{waitErr: errors.New("codex app server exited before returning a response")}
	freshClient := &fakeCodexAppServerClient{threadID: "thread-fresh", turnResponse: shuttleStructuredResponse{Message: "recovered-after-wait"}}
	clients := []codexAppServerClient{staleClient, freshClient}
	handler := &codexAppServerDefaultHandler{
		meta: RuntimeMetadata{Type: RuntimeCodexAppServer, Command: "codex"},
		clientFactory: func(context.Context) (codexAppServerClient, error) {
			client := clients[0]
			clients = clients[1:]
			return client, nil
		},
		threads: map[string]string{"shuttle\x00task-a": "thread-stale"},
	}

	outcome, err := handler.Respond(context.Background(), Request{Kind: RequestUserTurn, Prompt: "help", SessionName: "shuttle", TaskID: "task-a"})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if outcome.Message != "recovered-after-wait" {
		t.Fatalf("expected recovered wait response, got %#v", outcome)
	}
	if outcome.ModelInfo == nil || !strings.Contains(outcome.ModelInfo.RuntimeFailureReason, "stale Codex app-server thread") {
		t.Fatalf("expected stale-thread recovery note in model info, got %#v", outcome.ModelInfo)
	}
	if freshClient.startThreadCalls != 1 {
		t.Fatalf("expected replacement thread creation after wait failure, got %d", freshClient.startThreadCalls)
	}
}

func TestCodexAppServerDefaultHandlerRetriesSameTurnAfterFreshProcessFailure(t *testing.T) {
	failedClient := &fakeCodexAppServerClient{threadID: "thread-a", waitErr: errors.New("broken pipe")}
	retryClient := &fakeCodexAppServerClient{threadID: "thread-b", turnResponse: shuttleStructuredResponse{Message: "recovered-fresh"}}
	clients := []codexAppServerClient{failedClient, retryClient}
	handler := &codexAppServerDefaultHandler{
		meta: RuntimeMetadata{Type: RuntimeCodexAppServer, Command: "codex"},
		clientFactory: func(context.Context) (codexAppServerClient, error) {
			client := clients[0]
			clients = clients[1:]
			return client, nil
		},
		threads: map[string]string{},
	}

	outcome, err := handler.Respond(context.Background(), Request{Kind: RequestUserTurn, Prompt: "help", SessionName: "shuttle", TaskID: "task-a"})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if outcome.Message != "recovered-fresh" {
		t.Fatalf("expected recovered fresh-process response, got %#v", outcome)
	}
	if outcome.ModelInfo == nil || !strings.Contains(outcome.ModelInfo.RuntimeFailureReason, "transient Codex app-server process failure") {
		t.Fatalf("expected fresh-process recovery note in model info, got %#v", outcome.ModelInfo)
	}
	if failedClient.startThreadCalls != 1 || retryClient.startThreadCalls != 1 {
		t.Fatalf("expected one fresh-thread start per attempt, got first=%d retry=%d", failedClient.startThreadCalls, retryClient.startThreadCalls)
	}
	threadID, ok := handler.threadFor(Request{SessionName: "shuttle", TaskID: "task-a"})
	if !ok || threadID != "thread-b" {
		t.Fatalf("expected retry thread binding to win in memory, got ok=%v thread=%q", ok, threadID)
	}
}

func TestCodexSDKRuntimeShapesProposalRefinementPrompt(t *testing.T) {
	host := &stubHost{responses: []Outcome{{Message: "ready"}}}
	runtime := NewCodexSDKRuntime(RuntimeMetadata{Type: RuntimeCodexSDK, Command: "codex"}, nil)

	_, err := runtime.Handle(context.Background(), host, Request{
		Kind:       RequestProposalRefinement,
		UserPrompt: "Make it one second.",
		Prompt:     "Refine the proposal.",
		Proposal: &Proposal{
			Kind:        ProposalCommand,
			Command:     "sleep 5",
			Description: "Run a short sleep.",
		},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if host.respondCalls != 1 || len(host.requests) != 1 {
		t.Fatalf("expected one host request, got calls=%d requests=%d", host.respondCalls, len(host.requests))
	}
	prompt := host.requests[0].Prompt
	for _, fragment := range []string{
		"Shuttle Codex runtime turn",
		"request_kind: proposal_refinement",
		"user_prompt: Make it one second.",
		"proposal.kind: command",
		"proposal.command: sleep 5",
		"proposal.description: Run a short sleep.",
		"Controller instructions:\nRefine the proposal.",
	} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("expected prompt to contain %q, got %q", fragment, prompt)
		}
	}
}

func TestCodexSDKRuntimeShapesApprovalRefinementPrompt(t *testing.T) {
	host := &stubHost{responses: []Outcome{{Message: "ready"}}}
	runtime := NewCodexSDKRuntime(RuntimeMetadata{Type: RuntimeCodexSDK, Command: "codex"}, nil)

	_, err := runtime.Handle(context.Background(), host, Request{
		Kind:       RequestApprovalRefinement,
		UserPrompt: "Add a dry-run first.",
		Prompt:     "Refine the approval.",
		Approval: &ApprovalRequest{
			ID:      "approve-1",
			Kind:    ApprovalCommand,
			Title:   "Run migration",
			Summary: "Run the schema migration.",
			Command: "./migrate up",
			Risk:    "medium",
		},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if host.respondCalls != 1 || len(host.requests) != 1 {
		t.Fatalf("expected one host request, got calls=%d requests=%d", host.respondCalls, len(host.requests))
	}
	prompt := host.requests[0].Prompt
	for _, fragment := range []string{
		"request_kind: approval_refinement",
		"approval.id: approve-1",
		"approval.kind: command",
		"approval.title: Run migration",
		"approval.summary: Run the schema migration.",
		"approval.command: ./migrate up",
		"approval.risk: medium",
	} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("expected prompt to contain %q, got %q", fragment, prompt)
		}
	}
}

func TestCodexSDKRuntimePreservesExistingResponseModelInfo(t *testing.T) {
	host := &stubHost{responses: []Outcome{{
		Message: "ready",
		ModelInfo: &ModelInfo{
			ProviderPreset:  "anthropic",
			RequestedModel:  "claude-opus",
			ResponseModel:   "claude-opus-live",
			ResponseBaseURL: "https://api.anthropic.com",
		},
	}}}

	runtime := NewCodexSDKRuntime(RuntimeMetadata{
		Type:           RuntimeCodexSDK,
		Command:        "codex",
		ProviderPreset: "openai",
		Model:          "gpt-5",
	}, nil)

	outcome, err := runtime.Handle(context.Background(), host, Request{Kind: RequestUserTurn, Prompt: "help"})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if outcome.ModelInfo == nil {
		t.Fatal("expected model info")
	}
	if outcome.ModelInfo.ProviderPreset != "anthropic" {
		t.Fatalf("expected provider preset to be preserved, got %#v", outcome.ModelInfo)
	}
	if outcome.ModelInfo.RequestedModel != "claude-opus" {
		t.Fatalf("expected requested model to be preserved, got %#v", outcome.ModelInfo)
	}
	if outcome.ModelInfo.ResponseModel != "claude-opus-live" {
		t.Fatalf("expected response model to remain intact, got %#v", outcome.ModelInfo)
	}
	if outcome.ModelInfo.SelectedRuntime != RuntimeCodexSDK {
		t.Fatalf("expected selected runtime metadata, got %#v", outcome.ModelInfo)
	}
	if outcome.ModelInfo.EffectiveRuntime != RuntimeCodexSDK {
		t.Fatalf("expected effective runtime metadata, got %#v", outcome.ModelInfo)
	}
	if outcome.ModelInfo.RuntimeCommand != "codex" {
		t.Fatalf("expected runtime command metadata, got %#v", outcome.ModelInfo)
	}
	if outcome.ModelInfo.RuntimeAuthority != RuntimeAuthorityAuthoritative {
		t.Fatalf("expected authoritative runtime metadata, got %#v", outcome.ModelInfo)
	}
}

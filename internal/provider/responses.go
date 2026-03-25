package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"aiterm/internal/controller"
	"aiterm/internal/logging"
)

type ResponsesAgent struct {
	profile Profile
	client  *http.Client
	counter atomic.Uint64
}

const (
	shuttleUserAgent = "Shuttle/0.1"
)

type responsesRequest struct {
	Model           string                  `json:"model"`
	Input           []responsesInputMessage `json:"input"`
	Text            *responsesTextConfig    `json:"text,omitempty"`
	MaxOutputTokens int                     `json:"max_output_tokens,omitempty"`
	Reasoning       *responsesReasoning     `json:"reasoning,omitempty"`
}

type responsesInputMessage struct {
	Role    string                  `json:"role"`
	Content []responsesInputContent `json:"content"`
}

type responsesInputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesTextConfig struct {
	Format responsesFormat `json:"format"`
}

type responsesFormat struct {
	Type   string         `json:"type"`
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
	Strict bool           `json:"strict"`
}

type responsesReasoning struct {
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Exclude   bool   `json:"exclude,omitempty"`
}

type responsesAPIResponse struct {
	Model      string                `json:"model"`
	Output     []responsesOutputItem `json:"output"`
	OutputText string                `json:"output_text"`
	Error      *responsesError       `json:"error"`
}

type responsesOutputItem struct {
	Type    string                   `json:"type"`
	Content []responsesOutputContent `json:"content"`
}

type responsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

type shuttleStructuredResponse struct {
	Message             string   `json:"message"`
	PlanSummary         string   `json:"plan_summary"`
	PlanSteps           []string `json:"plan_steps"`
	ProposalKind        string   `json:"proposal_kind"`
	ProposalCommand     string   `json:"proposal_command"`
	ProposalKeys        string   `json:"proposal_keys"`
	ProposalPatch       string   `json:"proposal_patch"`
	ProposalDescription string   `json:"proposal_description"`
	ApprovalKind        string   `json:"approval_kind"`
	ApprovalTitle       string   `json:"approval_title"`
	ApprovalSummary     string   `json:"approval_summary"`
	ApprovalCommand     string   `json:"approval_command"`
	ApprovalPatch       string   `json:"approval_patch"`
	ApprovalRisk        string   `json:"approval_risk"`
}

func NewResponsesAgent(profile Profile, client *http.Client) (*ResponsesAgent, error) {
	if profile.BackendFamily != BackendResponsesHTTP {
		return nil, fmt.Errorf("profile %q is not an HTTP responses backend", profile.Preset)
	}
	if strings.TrimSpace(profile.BaseURL) == "" {
		return nil, errors.New("provider base URL must not be empty")
	}
	if strings.TrimSpace(profile.Model) == "" {
		return nil, errors.New("provider model must not be empty")
	}
	if profile.AuthMethod == AuthAPIKey && strings.TrimSpace(profile.APIKey) == "" {
		if profile.APIKeyEnvVar != "" {
			return nil, fmt.Errorf("%w: set %s or SHUTTLE_API_KEY", ErrMissingAPIKey, profile.APIKeyEnvVar)
		}

		return nil, ErrMissingAPIKey
	}
	if client == nil {
		client = &http.Client{Timeout: 75 * time.Second}
	}

	return &ResponsesAgent{
		profile: profile,
		client:  client,
	}, nil
}

func (a *ResponsesAgent) Respond(ctx context.Context, input controller.AgentInput) (controller.AgentResponse, error) {
	requestID := fmt.Sprintf("req-%d", a.counter.Add(1))
	requestBody, err := newStructuredResponsesRequest(a.profile.Model, input)
	if err != nil {
		return controller.AgentResponse{}, err
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("marshal provider request: %w", err)
	}

	endpoint, err := responsesEndpoint(a.profile.BaseURL)
	if err != nil {
		return controller.AgentResponse{}, err
	}

	logging.Trace(
		"provider.responses.request",
		"request_id", requestID,
		"preset", a.profile.Preset,
		"model", a.profile.Model,
		"base_url", a.profile.BaseURL,
		"endpoint", endpoint,
		"auth_method", a.profile.AuthMethod,
		"api_key_env", a.profile.APIKeyEnvVar,
		"body", string(payload),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("build provider request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", shuttleUserAgent)
	if a.profile.AuthMethod == AuthAPIKey {
		req.Header.Set("Authorization", "Bearer "+a.profile.APIKey)
	}
	for key, value := range providerRequestHeaders(a.profile) {
		req.Header.Set(key, value)
	}

	startedAt := time.Now()
	resp, err := a.client.Do(req)
	if err != nil {
		logging.TraceError(
			"provider.responses.request_error",
			err,
			"request_id", requestID,
			"endpoint", endpoint,
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
		return controller.AgentResponse{}, fmt.Errorf("request provider: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logging.TraceError(
			"provider.responses.read_error",
			err,
			"request_id", requestID,
			"endpoint", endpoint,
			"status_code", resp.StatusCode,
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
		return controller.AgentResponse{}, fmt.Errorf("read provider response: %w", err)
	}

	logging.Trace(
		"provider.responses.response",
		"request_id", requestID,
		"endpoint", endpoint,
		"status_code", resp.StatusCode,
		"duration_ms", time.Since(startedAt).Milliseconds(),
		"body", string(body),
	)

	if resp.StatusCode >= http.StatusBadRequest {
		return controller.AgentResponse{}, parseProviderError(resp.StatusCode, body)
	}

	var apiResp responsesAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return controller.AgentResponse{}, fmt.Errorf("decode provider response: %w", err)
	}
	if apiResp.Error != nil && apiResp.Error.Message != "" {
		return controller.AgentResponse{}, fmt.Errorf("provider error: %s", apiResp.Error.Message)
	}

	responseText, err := extractResponseText(apiResp)
	if err != nil {
		return controller.AgentResponse{}, err
	}

	var structured shuttleStructuredResponse
	if err := json.Unmarshal([]byte(responseText), &structured); err != nil {
		return controller.AgentResponse{}, fmt.Errorf("decode structured provider output: %w", err)
	}

	response, err := a.toAgentResponse(structured)
	if err != nil {
		return controller.AgentResponse{}, err
	}

	response.ModelInfo = &controller.AgentModelInfo{
		ProviderPreset:  string(a.profile.Preset),
		RequestedModel:  a.profile.Model,
		ResponseModel:   strings.TrimSpace(apiResp.Model),
		ResponseBaseURL: a.profile.BaseURL,
	}

	return response, nil
}

func (a *ResponsesAgent) CheckHealth(ctx context.Context) error {
	_, err := a.Respond(ctx, controller.AgentInput{
		Prompt: "Respond with a short confirmation that the provider path works.",
	})
	return err
}

func (a *ResponsesAgent) toAgentResponse(input shuttleStructuredResponse) (controller.AgentResponse, error) {
	response := controller.AgentResponse{
		Message: strings.TrimSpace(input.Message),
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
		response.Plan = &controller.Plan{
			Summary: planSummary,
			Steps:   steps,
		}
	}

	proposal, err := parseProposal(input)
	if err != nil {
		return controller.AgentResponse{}, err
	}
	response.Proposal = proposal

	approval, err := a.parseApproval(input)
	if err != nil {
		return controller.AgentResponse{}, err
	}
	response.Approval = approval

	return response, nil
}

func parseProposal(input shuttleStructuredResponse) (*controller.Proposal, error) {
	if strings.TrimSpace(input.ProposalKind) == "" &&
		strings.TrimSpace(input.ProposalCommand) == "" &&
		strings.TrimSpace(input.ProposalKeys) == "" &&
		strings.TrimSpace(input.ProposalPatch) == "" &&
		strings.TrimSpace(input.ProposalDescription) == "" {
		return nil, nil
	}

	kind := controller.ProposalKind(strings.TrimSpace(input.ProposalKind))
	switch kind {
	case "":
		switch {
		case strings.TrimSpace(input.ProposalCommand) != "":
			kind = controller.ProposalCommand
		case strings.TrimSpace(input.ProposalKeys) != "":
			kind = controller.ProposalKeys
		case strings.TrimSpace(input.ProposalPatch) != "":
			kind = controller.ProposalPatch
		default:
			kind = controller.ProposalAnswer
		}
	case controller.ProposalAnswer, controller.ProposalCommand, controller.ProposalKeys, controller.ProposalPatch:
	default:
		return nil, fmt.Errorf("unsupported proposal kind %q", input.ProposalKind)
	}

	return &controller.Proposal{
		Kind:        kind,
		Command:     strings.TrimSpace(input.ProposalCommand),
		Keys:        input.ProposalKeys,
		Patch:       strings.TrimSpace(input.ProposalPatch),
		Description: strings.TrimSpace(input.ProposalDescription),
	}, nil
}

func (a *ResponsesAgent) parseApproval(input shuttleStructuredResponse) (*controller.ApprovalRequest, error) {
	if strings.TrimSpace(input.ApprovalKind) == "" &&
		strings.TrimSpace(input.ApprovalTitle) == "" &&
		strings.TrimSpace(input.ApprovalSummary) == "" &&
		strings.TrimSpace(input.ApprovalCommand) == "" &&
		strings.TrimSpace(input.ApprovalPatch) == "" {
		return nil, nil
	}

	kind := controller.ApprovalKind(strings.TrimSpace(input.ApprovalKind))
	switch kind {
	case "":
		switch {
		case strings.TrimSpace(input.ApprovalCommand) != "":
			kind = controller.ApprovalCommand
		case strings.TrimSpace(input.ApprovalPatch) != "":
			kind = controller.ApprovalPatch
		default:
			kind = controller.ApprovalPlan
		}
	case controller.ApprovalCommand, controller.ApprovalPatch, controller.ApprovalPlan:
	default:
		return nil, fmt.Errorf("unsupported approval kind %q", input.ApprovalKind)
	}

	risk := controller.RiskLevel(strings.TrimSpace(input.ApprovalRisk))
	switch risk {
	case "":
		risk = controller.RiskMedium
	case controller.RiskLow, controller.RiskMedium, controller.RiskHigh:
	default:
		return nil, fmt.Errorf("unsupported approval risk %q", input.ApprovalRisk)
	}

	return &controller.ApprovalRequest{
		ID:      a.nextID("approval"),
		Kind:    kind,
		Title:   strings.TrimSpace(input.ApprovalTitle),
		Summary: strings.TrimSpace(input.ApprovalSummary),
		Command: strings.TrimSpace(input.ApprovalCommand),
		Patch:   strings.TrimSpace(input.ApprovalPatch),
		Risk:    risk,
	}, nil
}

func (a *ResponsesAgent) nextID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, a.counter.Add(1))
}

func responsesEndpoint(baseURL string) (string, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return "", errors.New("provider base URL must not be empty")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse provider base URL: %w", err)
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/responses"
	return parsed.String(), nil
}

func providerRequestHeaders(profile Profile) map[string]string {
	switch profile.BackendFamily {
	case BackendOpenRouter:
		return map[string]string{
			"X-Title":            "Shuttle",
			"X-OpenRouter-Title": "Shuttle",
		}
	default:
		return nil
	}
}

func parseProviderError(statusCode int, body []byte) error {
	var apiErr struct {
		Error *responsesError `json:"error"`
	}
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Error != nil && apiErr.Error.Message != "" {
		return fmt.Errorf("provider returned %d: %s", statusCode, apiErr.Error.Message)
	}

	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(statusCode)
	}
	return fmt.Errorf("provider returned %d: %s", statusCode, message)
}

func extractResponseText(response responsesAPIResponse) (string, error) {
	if strings.TrimSpace(response.OutputText) != "" {
		return response.OutputText, nil
	}

	fragments := make([]string, 0, 2)
	for _, item := range response.Output {
		for _, content := range item.Content {
			if strings.TrimSpace(content.Text) != "" {
				fragments = append(fragments, content.Text)
			}
		}
	}

	if len(fragments) == 0 {
		return "", errors.New("provider returned no output text")
	}

	return strings.Join(fragments, "\n"), nil
}

func newStructuredResponsesRequest(model string, input controller.AgentInput) (responsesRequest, error) {
	request := responsesRequest{
		Model: model,
		Input: []responsesInputMessage{
			{
				Role: "system",
				Content: []responsesInputContent{
					{Type: "input_text", Text: shuttleSystemPrompt},
				},
			},
			{
				Role: "user",
				Content: []responsesInputContent{
					{Type: "input_text", Text: buildTurnContext(input)},
				},
			},
		},
		Text: &responsesTextConfig{
			Format: responsesFormat{
				Type:   "json_schema",
				Name:   "shuttle_agent_response",
				Schema: shuttleAgentResponseSchema(),
				Strict: true,
			},
		},
	}

	return request, nil
}

func newPromptOnlyResponsesRequest(model string, input controller.AgentInput) (responsesRequest, error) {
	schemaJSON, err := json.Marshal(shuttleAgentResponseSchema())
	if err != nil {
		return responsesRequest{}, fmt.Errorf("marshal shuttle schema: %w", err)
	}

	systemPrompt := shuttleSystemPrompt + "\n\nReturn only a valid JSON object that matches this schema exactly:\n" + string(schemaJSON)
	request := responsesRequest{
		Model: model,
		Input: []responsesInputMessage{
			{
				Role: "system",
				Content: []responsesInputContent{
					{Type: "input_text", Text: systemPrompt},
				},
			},
			{
				Role: "user",
				Content: []responsesInputContent{
					{Type: "input_text", Text: buildTurnContext(input)},
				},
			},
		},
	}

	return request, nil
}

func buildTurnContext(input controller.AgentInput) string {
	sections := make([]string, 0, 5)
	sections = append(sections, "User prompt:\n"+strings.TrimSpace(input.Prompt))
	seenSnippets := make(map[string]struct{})

	sessionLines := []string{}
	if input.Session.CurrentShell != nil && strings.TrimSpace(input.Session.CurrentShell.PromptLine()) != "" {
		sessionLines = append(sessionLines, "prompt="+input.Session.CurrentShell.PromptLine())
		if input.Session.CurrentShell.Remote {
			sessionLines = append(sessionLines, "remote=true")
		}
	}
	if input.Session.SessionName != "" {
		sessionLines = append(sessionLines, "session="+input.Session.SessionName)
	}
	if input.Session.TrackedShell.SessionName != "" {
		sessionLines = append(sessionLines, "tracked_session="+input.Session.TrackedShell.SessionName)
	}
	if input.Session.WorkingDirectory != "" {
		sessionLines = append(sessionLines, "cwd="+input.Session.WorkingDirectory)
	}
	if input.Session.LocalWorkspaceRoot != "" {
		sessionLines = append(sessionLines, "workspace_root="+input.Session.LocalWorkspaceRoot)
	}
	if input.Session.TrackedShell.PaneID != "" {
		sessionLines = append(sessionLines, "tracked_pane="+input.Session.TrackedShell.PaneID)
	}
	if input.Session.BottomPaneID != "" {
		sessionLines = append(sessionLines, "bottom_pane="+input.Session.BottomPaneID)
	}
	if len(sessionLines) > 0 {
		sections = append(sections, "Session:\n"+strings.Join(sessionLines, "\n"))
	}

	recentOutput := compactShellOutput(input.Session.RecentShellOutput, 8, 4, 1200)
	if shouldIncludeContextSnippet(seenSnippets, recentOutput) {
		sections = append(sections, "Recent shell output:\n"+recentOutput)
	}
	if len(input.Session.RecentManualCommands) > 0 {
		lines := append([]string(nil), input.Session.RecentManualCommands...)
		sections = append(sections, "Recent manual shell commands:\n"+strings.Join(lines, "\n"))
	}
	if len(input.Session.RecentManualActions) > 0 {
		lines := append([]string(nil), input.Session.RecentManualActions...)
		sections = append(sections, "Recent manual shell actions:\n"+strings.Join(lines, "\n"))
	}

	if input.Task.LastCommandResult != nil {
		last := input.Task.LastCommandResult
		lines := []string{
			"command=" + last.Command,
			"state=" + string(last.State),
			fmt.Sprintf("exit_code=%d", last.ExitCode),
		}
		if last.Cause != "" {
			lines = append(lines, "cause="+string(last.Cause))
		}
		if last.Confidence != "" {
			lines = append(lines, "confidence="+string(last.Confidence))
		}
		if last.SemanticShell {
			lines = append(lines, "semantic_shell=true")
		}
		if strings.TrimSpace(last.SemanticSource) != "" {
			lines = append(lines, "semantic_source="+last.SemanticSource)
		}
		if summary := compactShellOutput(last.Summary, 8, 4, 800); shouldIncludeContextSnippet(seenSnippets, summary) {
			lines = append(lines, "summary="+summary)
		}
		sections = append(sections, "Last command result:\n"+strings.Join(lines, "\n"))
	}

	if input.Task.LastPatchApplyResult != nil {
		last := input.Task.LastPatchApplyResult
		lines := []string{
			fmt.Sprintf("applied=%t", last.Applied),
			fmt.Sprintf("created=%d", last.Created),
			fmt.Sprintf("updated=%d", last.Updated),
			fmt.Sprintf("deleted=%d", last.Deleted),
			fmt.Sprintf("renamed=%d", last.Renamed),
		}
		if strings.TrimSpace(last.WorkspaceRoot) != "" {
			lines = append(lines, "workspace_root="+last.WorkspaceRoot)
		}
		if strings.TrimSpace(last.Validation) != "" {
			lines = append(lines, "validation="+last.Validation)
		}
		if strings.TrimSpace(last.Error) != "" {
			lines = append(lines, "error="+clipText(last.Error, 240))
		}
		if len(last.Files) > 0 {
			fileLines := make([]string, 0, len(last.Files))
			for _, file := range last.Files {
				path := strings.TrimSpace(file.NewPath)
				if path == "" {
					path = strings.TrimSpace(file.OldPath)
				}
				fileLines = append(fileLines, fmt.Sprintf("%s %s", file.Operation, path))
			}
			lines = append(lines, "files="+clipText(strings.Join(fileLines, "; "), 400))
		}
		sections = append(sections, "Last patch apply result:\n"+strings.Join(lines, "\n"))
	}

	if input.Task.PrimaryExecutionID != "" || len(input.Task.ExecutionRegistry) > 0 {
		lines := make([]string, 0, 2)
		if input.Task.PrimaryExecutionID != "" {
			lines = append(lines, "primary_execution="+input.Task.PrimaryExecutionID)
		}
		if len(input.Task.ExecutionRegistry) > 0 {
			lines = append(lines, fmt.Sprintf("active_execution_count=%d", len(input.Task.ExecutionRegistry)))
		}
		sections = append(sections, "Execution registry:\n"+strings.Join(lines, "\n"))
	}

	if input.Task.CurrentExecution != nil {
		current := input.Task.CurrentExecution
		lines := []string{
			"id=" + current.ID,
			"command=" + current.Command,
			"origin=" + string(current.Origin),
			"state=" + string(current.State),
		}
		if current.TrackedShell.SessionName != "" {
			lines = append(lines, "execution_session="+current.TrackedShell.SessionName)
		}
		if current.TrackedShell.PaneID != "" {
			lines = append(lines, "execution_pane="+current.TrackedShell.PaneID)
		}
		if strings.TrimSpace(current.ForegroundCommand) != "" {
			lines = append(lines, "foreground_command="+current.ForegroundCommand)
		}
		if current.SemanticShell {
			lines = append(lines, "semantic_shell=true")
		}
		if strings.TrimSpace(current.SemanticSource) != "" {
			lines = append(lines, "semantic_source="+current.SemanticSource)
		}
		if !current.StartedAt.IsZero() {
			lines = append(lines, fmt.Sprintf("elapsed_seconds=%d", int(time.Since(current.StartedAt).Seconds())))
		}
		if current.ShellContextBefore != nil && strings.TrimSpace(current.ShellContextBefore.PromptLine()) != "" {
			lines = append(lines, "prompt_before="+current.ShellContextBefore.PromptLine())
		}
		if current.ShellContextAfter != nil && strings.TrimSpace(current.ShellContextAfter.PromptLine()) != "" {
			lines = append(lines, "prompt_after="+current.ShellContextAfter.PromptLine())
		}
		if tail := compactShellOutput(current.LatestOutputTail, 6, 3, 600); shouldIncludeContextSnippet(seenSnippets, tail) {
			lines = append(lines, "latest_output="+tail)
		}
		sections = append(sections, "Current active command:\n"+strings.Join(lines, "\n"))
	}

	if snapshot := compactShellOutput(input.Task.RecoverySnapshot, 20, 20, 4000); shouldIncludeContextSnippet(seenSnippets, snapshot) {
		sections = append(sections, "Recovery terminal snapshot:\n"+snapshot)
	}

	if input.Task.ActivePlan != nil {
		sections = append(sections, "Active plan:\n"+formatActivePlan(*input.Task.ActivePlan))
	}

	if input.Task.PendingApproval != nil {
		pending := input.Task.PendingApproval
		approvalLines := []string{
			"title=" + pending.Title,
			"summary=" + pending.Summary,
			"kind=" + string(pending.Kind),
			"risk=" + string(pending.Risk),
		}
		if pending.Command != "" {
			approvalLines = append(approvalLines, "command="+pending.Command)
		}
		if pending.Patch != "" {
			approvalLines = append(approvalLines, "patch="+clipText(pending.Patch, 1200))
		}
		sections = append(sections, "Pending approval under refinement:\n"+strings.Join(approvalLines, "\n"))
	}

	if transcript := summarizeTranscript(input.Task.PriorTranscript, 8); transcript != "" {
		sections = append(sections, "Recent transcript:\n"+transcript)
	}

	return strings.Join(sections, "\n\n")
}

func summarizeTranscript(events []controller.TranscriptEvent, maxEvents int) string {
	if len(events) == 0 {
		return ""
	}
	if maxEvents <= 0 {
		maxEvents = len(events)
	}

	start := len(events) - maxEvents
	if start < 0 {
		start = 0
	}

	lines := make([]string, 0, len(events)-start)
	for _, event := range events[start:] {
		lines = append(lines, summarizeTranscriptEvent(event))
	}

	return strings.Join(lines, "\n")
}

func summarizeTranscriptEvent(event controller.TranscriptEvent) string {
	switch event.Kind {
	case controller.EventUserMessage, controller.EventAgentMessage, controller.EventSystemNotice, controller.EventError:
		payload, _ := event.Payload.(controller.TextPayload)
		return string(event.Kind) + ": " + clipText(payload.Text, 240)
	case controller.EventPlan:
		payload, _ := event.Payload.(controller.PlanPayload)
		progress := ""
		if len(payload.Steps) > 0 {
			done := 0
			for _, step := range payload.Steps {
				if step.Status == controller.PlanStepDone {
					done++
				}
			}
			progress = fmt.Sprintf(" (%d/%d done)", done, len(payload.Steps))
		}
		return string(event.Kind) + progress + ": " + clipText(payload.Summary, 240)
	case controller.EventProposal:
		payload, _ := event.Payload.(controller.ProposalPayload)
		text := payload.Description
		if text == "" {
			text = payload.Command
		}
		if text == "" {
			text = payload.Patch
		}
		return string(event.Kind) + ": " + clipText(text, 240)
	case controller.EventApproval:
		payload, _ := event.Payload.(controller.ApprovalRequest)
		text := payload.Summary
		if text == "" {
			text = payload.Command
		}
		return string(event.Kind) + ": " + clipText(text, 240)
	case controller.EventCommandStart:
		payload, _ := event.Payload.(controller.CommandStartPayload)
		return string(event.Kind) + ": " + clipText(payload.Command, 240)
	case controller.EventCommandResult:
		payload, _ := event.Payload.(controller.CommandResultSummary)
		if payload.State == controller.CommandExecutionCanceled {
			return fmt.Sprintf("%s: canceled %s", event.Kind, clipText(payload.Command, 180))
		}
		return fmt.Sprintf("%s: exit=%d %s", event.Kind, payload.ExitCode, clipText(payload.Command, 180))
	case controller.EventPatchApplyResult:
		payload, _ := event.Payload.(controller.PatchApplySummary)
		if payload.Applied {
			return fmt.Sprintf("%s: created=%d updated=%d deleted=%d renamed=%d", event.Kind, payload.Created, payload.Updated, payload.Deleted, payload.Renamed)
		}
		return fmt.Sprintf("%s: failed %s", event.Kind, clipText(payload.Error, 180))
	case controller.EventModelInfo:
		payload, _ := event.Payload.(controller.AgentModelInfo)
		model := payload.ResponseModel
		if strings.TrimSpace(model) == "" {
			model = payload.RequestedModel
		}
		return string(event.Kind) + ": " + clipText(model, 240)
	default:
		return string(event.Kind)
	}
}

func clipText(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}

	return value[:maxLen] + "...(truncated)"
}

func formatActivePlan(plan controller.ActivePlan) string {
	lines := make([]string, 0, len(plan.Steps)+1)
	if strings.TrimSpace(plan.Summary) != "" {
		lines = append(lines, "summary="+plan.Summary)
	}

	for index, step := range plan.Steps {
		status := string(step.Status)
		if status == "" {
			status = string(controller.PlanStepPending)
		}
		lines = append(lines, fmt.Sprintf("%d. [%s] %s", index+1, status, step.Text))
	}

	if len(lines) == 0 {
		return "(empty)"
	}

	return strings.Join(lines, "\n")
}

func shuttleAgentResponseSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"message",
			"plan_summary",
			"plan_steps",
			"proposal_kind",
			"proposal_command",
			"proposal_keys",
			"proposal_patch",
			"proposal_description",
			"approval_kind",
			"approval_title",
			"approval_summary",
			"approval_command",
			"approval_patch",
			"approval_risk",
		},
		"properties": map[string]any{
			"message": map[string]any{
				"type": "string",
			},
			"plan_summary": map[string]any{
				"type": "string",
			},
			"plan_steps": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
			},
			"proposal_kind": map[string]any{
				"type": "string",
				"enum": []string{"", "answer", "command", "keys", "patch"},
			},
			"proposal_command": map[string]any{
				"type": "string",
			},
			"proposal_keys": map[string]any{
				"type": "string",
			},
			"proposal_patch": map[string]any{
				"type": "string",
			},
			"proposal_description": map[string]any{
				"type": "string",
			},
			"approval_kind": map[string]any{
				"type": "string",
				"enum": []string{"", "command", "patch", "plan"},
			},
			"approval_title": map[string]any{
				"type": "string",
			},
			"approval_summary": map[string]any{
				"type": "string",
			},
			"approval_command": map[string]any{
				"type": "string",
			},
			"approval_patch": map[string]any{
				"type": "string",
			},
			"approval_risk": map[string]any{
				"type": "string",
				"enum": []string{"", "low", "medium", "high"},
			},
		},
	}
}

const shuttleSystemPrompt = `You are the Shuttle agent runtime.

Return only the JSON object required by the schema.

Rules:
- Keep "message" concise and useful.
- Only use "plan_summary" and "plan_steps" when the user is asking for a plan, next steps, strategy, troubleshooting, or how to fix/approach something, or when a multi-step plan is genuinely necessary to answer well.
- If the user asks for an ordered multi-step workflow, reversible edit-and-restore flow, or checklist-like execution, emit a concise plan/checklist that matches that requested sequence and keep the next action aligned to it.
- Do not emit a plan for simple descriptive, factual, or status-summary requests.
- If an active plan is present in context, prefer continuing it from the current in-progress or pending step instead of inventing a new unrelated plan.
- For requests to inspect the current directory, repository, files, environment, or system state, prefer a "proposal_command" over answering from stale context.
- Only answer directly from shell state when the current turn already includes the necessary command result, or when the user is explicitly asking for a summary of a result that is already in context.
- Never imply that you executed a shell command unless Shuttle has actual command/result context for it.
- Never imply that a proposed patch or diff has already been applied.
- Do not claim that files created by a proposed patch already exist, are executable, or can be referenced by later commands until Shuttle explicitly confirms the patch was applied.
- Patch proposals and approvals must use git-style unified diff text in "proposal_patch" or "approval_patch", relative to the local workspace root shown in context.
- Do not emit the non-standard "*** Begin Patch" format.
- When you emit a patch, output only the raw unified diff text with exact hunk headers and counts. Do not wrap the diff in prose, bullets, or code fences.
- Patch application mutates the local workspace root, not the active remote shell host. If the shell prompt is remote, keep local patch effects and remote shell effects clearly separate.
- Never propose a shell command that invokes apply_patch, git apply, patch, or any heredoc-based patch application tool for local workspace edits. Use "proposal_kind":"patch" or patch approval fields instead.
- If you propose a shell action, set "proposal_kind" to "command" and fill "proposal_command".
- If you propose sending a small raw key sequence to an already-active prompt or fullscreen app, set "proposal_kind" to "keys" and fill "proposal_keys". Use a literal newline in "proposal_keys" when Enter should be sent.
- If you propose a patch, set "proposal_kind" to "patch" and fill "proposal_patch".
- If no proposal is needed, leave proposal fields empty.
- If an action is destructive, risky, or should be user-confirmed, leave proposal fields empty and fill the approval fields instead.
- For approvals, set "approval_kind" to "command", "patch", or "plan" and set "approval_risk" to "low", "medium", or "high".
- If the task is a refinement of a pending approval, preserve the original command or patch unless the context clearly requires changing it.
- If the current turn says an active command is still running, use "message" for a brief status update. Do not emit a plan, proposal, or approval unless the shell is clearly waiting for user intervention.
- If the current active command state is "awaiting_input", explain what input is likely needed from the shell output or recovery snapshot and tell the user to press F2 to take control. If a small raw keystroke sequence would likely help, you may propose it with "proposal_kind":"keys" and "proposal_keys".
- If the current active command state is "interactive_fullscreen", explain that a fullscreen terminal app currently owns the pane and tell the user to press F2 to take control. Do not suggest unrelated shell commands while that app is active.
- If the current active command state is "lost", explain that tracking confidence is low, use the recovery snapshot to infer what likely happened, and avoid claiming completion unless the context clearly proves it.
- If a recovery terminal snapshot is present, use it to reason about the current terminal state. Prefer actionable recovery guidance over abstract commentary.
- If a current active command is in "awaiting_input", "interactive_fullscreen", or "lost" and the user asks a general question such as what to do next, what happened, help, or how to continue, prioritize recovery guidance over new proposals or plans.
- If a current active command is in "awaiting_input" or "interactive_fullscreen" and the user explicitly asks you to send the needed input on their behalf, prefer a "keys" proposal over prose.
- After a proposed or approved command completes, if there is no active plan, stop only when the user's request is already satisfied. If the user's request clearly still requires more shell work, propose the next action.
- If the latest command was a read-only inspection command and the transcript or command result still shows unresolved work, do not stop at diagnosis. Propose exactly one next action now.
- Prefer stopping after a satisfied one-shot request. Do not invent extra verification or follow-up work unless the user explicitly asked for verification, the request is part of an active multi-step plan, or the available result is ambiguous.
- If the recent transcript shows that the user explicitly asked for serial, ordered, or one-command-at-a-time shell work, and the latest command only completed one step of that request, summarize briefly and propose exactly one next command now. Do not lump multiple shell actions together, and do not wait for an extra "go ahead" unless the user explicitly asked to approve each step separately.
- Leave unused fields as empty strings, and leave unused arrays empty.`

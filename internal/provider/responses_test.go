package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"aiterm/internal/controller"
	"aiterm/internal/search"
)

func TestResponsesAgentRespondMapsStructuredOutput(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}

		if r.URL.String() != "https://provider.test/v1/responses" {
			t.Fatalf("expected provider responses endpoint, got %q", r.URL.String())
		}

		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("expected bearer auth, got %q", got)
		}

		if got := r.Header.Get("X-Title"); got != "" {
			t.Fatalf("expected no OpenRouter title header, got %q", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}

		if payload["model"] != "gpt-5-nano-2025-08-07" {
			t.Fatalf("expected model gpt-5-nano-2025-08-07, got %#v", payload["model"])
		}

		text, ok := payload["text"].(map[string]any)
		if !ok {
			t.Fatalf("expected text config in payload, got %#v", payload["text"])
		}
		format, ok := text["format"].(map[string]any)
		if !ok || format["type"] != "json_schema" {
			t.Fatalf("expected json_schema format, got %#v", text["format"])
		}

		if _, ok := payload["max_output_tokens"]; ok {
			t.Fatalf("expected no OpenAI max_output_tokens override, got %#v", payload["max_output_tokens"])
		}
		if _, ok := payload["reasoning"]; ok {
			t.Fatalf("expected no OpenAI reasoning override, got %#v", payload["reasoning"])
		}

		return jsonResponse(http.StatusOK, `{
			"id":"resp_123",
			"object":"response",
			"model":"gpt-5-nano-2025-08-07",
			"output":[
				{
					"type":"message",
					"content":[
						{
							"type":"output_text",
							"text":"{\"message\":\"I can inspect the current directory.\",\"plan_summary\":\"\",\"plan_steps\":[],\"proposal_kind\":\"command\",\"proposal_command\":\"ls -lah\",\"proposal_patch\":\"\",\"proposal_description\":\"List files with permissions and sizes.\",\"approval_kind\":\"\",\"approval_title\":\"\",\"approval_summary\":\"\",\"approval_command\":\"\",\"approval_patch\":\"\",\"approval_risk\":\"\"}"
						}
					]
				}
			]
		}`), nil
	})}

	agent, err := NewResponsesAgent(Profile{
		BackendFamily: BackendResponsesHTTP,
		Preset:        PresetOpenAI,
		AuthMethod:    AuthAPIKey,
		BaseURL:       "https://provider.test/v1",
		Model:         "gpt-5-nano-2025-08-07",
		APIKey:        "test-key",
		APIKeyEnvVar:  "OPENAI_API_KEY",
	}, client)
	if err != nil {
		t.Fatalf("NewResponsesAgent() error = %v", err)
	}

	response, err := agent.Respond(context.Background(), controller.AgentInput{
		Prompt: "list files",
		Session: controller.SessionContext{
			SessionName:      "shuttle-test",
			WorkingDirectory: "/workspace",
		},
	})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if response.Message != "I can inspect the current directory." {
		t.Fatalf("expected message, got %q", response.Message)
	}
	if response.Proposal == nil {
		t.Fatal("expected command proposal")
	}
	if response.Proposal.Kind != controller.ProposalCommand {
		t.Fatalf("expected command proposal, got %s", response.Proposal.Kind)
	}
	if response.Proposal.Command != "ls -lah" {
		t.Fatalf("expected ls -lah, got %q", response.Proposal.Command)
	}

	if response.ModelInfo == nil {
		t.Fatal("expected model info metadata")
	}

	if response.ModelInfo.ResponseModel != "gpt-5-nano-2025-08-07" {
		t.Fatalf("expected response model metadata, got %#v", response.ModelInfo)
	}
}

func TestStructuredResponsesRequestIncludesSerialContinuationGuidance(t *testing.T) {
	request, err := newStructuredResponsesRequest("gpt-5-test", controller.AgentInput{
		Prompt: "continue",
	})
	if err != nil {
		t.Fatalf("newStructuredResponsesRequest() error = %v", err)
	}
	if len(request.Input) < 1 || len(request.Input[0].Content) < 1 {
		t.Fatalf("expected system prompt content, got %#v", request.Input)
	}
	systemPrompt := request.Input[0].Content[0].Text
	if !strings.Contains(systemPrompt, "propose exactly one next command now") {
		t.Fatalf("expected serial continuation guidance in system prompt, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "Prefer stopping after a satisfied one-shot request.") {
		t.Fatalf("expected stop-biased continuation guidance in system prompt, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "ordered multi-step workflow") || !strings.Contains(systemPrompt, "do not stop at diagnosis") {
		t.Fatalf("expected workflow checklist and unresolved-inspection guidance in system prompt, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "git-style unified diff text") || !strings.Contains(systemPrompt, "Do not emit the non-standard \"*** Begin Patch\" format.") {
		t.Fatalf("expected patch format guidance in system prompt, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "Never propose a shell command that invokes apply_patch, git apply, patch") {
		t.Fatalf("expected shell patch-tool prohibition in system prompt, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "approval_mode=auto") || !strings.Contains(systemPrompt, "safe local inspection or verification commands may be auto-executed") {
		t.Fatalf("expected approval auto-mode guidance in system prompt, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "approval_mode=dangerous") || !strings.Contains(systemPrompt, "auto-apply agent patches without confirmation") {
		t.Fatalf("expected approval dangerous-mode guidance in system prompt, got %q", systemPrompt)
	}
}

func TestBuildTurnContextIncludesCompactedTaskSummary(t *testing.T) {
	context := buildTurnContext(controller.AgentInput{
		Prompt: "continue",
		Task: controller.TaskContext{
			CompactedSummary: "User asked for a smaller continuation context after the repo scan.",
			PriorTranscript: []controller.TranscriptEvent{
				{Kind: controller.EventUserMessage, Payload: controller.TextPayload{Text: "old prompt"}},
			},
		},
	})

	if !strings.Contains(context, "Compacted task summary:\nUser asked for a smaller continuation context") {
		t.Fatalf("expected compacted summary in turn context, got %q", context)
	}
	if !strings.Contains(context, "Recent transcript:\nuser_message: old prompt") {
		t.Fatalf("expected recent transcript tail alongside compacted summary, got %q", context)
	}
}

func TestBuildTurnContextShrinksAfterCompaction(t *testing.T) {
	events := make([]controller.TranscriptEvent, 0, 24)
	for index := 0; index < 24; index++ {
		events = append(events, controller.TranscriptEvent{
			Kind:    controller.EventUserMessage,
			Payload: controller.TextPayload{Text: strings.Repeat("context line ", 12) + strconv.Itoa(index)},
		})
	}

	uncompacted := buildTurnContext(controller.AgentInput{
		Prompt: "continue",
		Task: controller.TaskContext{
			PriorTranscript: events,
		},
	})
	compacted := buildTurnContext(controller.AgentInput{
		Prompt: "continue",
		Task: controller.TaskContext{
			CompactedSummary: "Condensed summary.",
			PriorTranscript:  events,
		},
	})

	if len(compacted) >= len(uncompacted) {
		t.Fatalf("expected compacted context to be smaller, got uncompacted=%d compacted=%d", len(uncompacted), len(compacted))
	}
}

func TestBuildTurnContextIncludesSearchCapabilities(t *testing.T) {
	context := buildTurnContext(controller.AgentInput{
		Prompt: "research this",
		Session: controller.SessionContext{
			Search:                  search.ShuttleAvailability(search.ProviderBrave),
			PreferredExternalSearch: search.RuntimeAvailability("pi", search.ProviderBrave),
		},
	})

	for _, fragment := range []string{
		"search_mode=shuttle",
		"search_provider=brave",
		"preferred_external_search_mode=runtime_native_with_shuttle_fallback",
		"preferred_external_search_runtime=pi",
	} {
		if !strings.Contains(context, fragment) {
			t.Fatalf("expected %q in turn context, got %q", fragment, context)
		}
	}
}

func TestResponsesAgentCustomBaseURLSmoke(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/custom/v1/responses" {
			t.Fatalf("expected /custom/v1/responses, got %s", r.URL.Path)
		}

		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("expected no auth header, got %q", got)
		}

		io.WriteString(w, `{"output_text":"{\"message\":\"Custom endpoint works.\"}"}`)
	}))
	defer server.Close()

	agent, err := NewResponsesAgent(Profile{
		BackendFamily: BackendResponsesHTTP,
		Preset:        PresetCustom,
		AuthMethod:    AuthNone,
		BaseURL:       server.URL + "/custom/v1/",
		Model:         "custom-model",
	}, server.Client())
	if err != nil {
		t.Fatalf("NewResponsesAgent() error = %v", err)
	}

	response, err := agent.Respond(context.Background(), controller.AgentInput{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if response.Message != "Custom endpoint works." {
		t.Fatalf("expected custom endpoint response message, got %q", response.Message)
	}
}

func TestResponsesAgentReturnsProviderErrors(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusUnauthorized, `{"error":{"message":"bad API key"}}`), nil
	})}

	agent, err := NewResponsesAgent(Profile{
		BackendFamily: BackendResponsesHTTP,
		Preset:        PresetOpenAI,
		AuthMethod:    AuthAPIKey,
		BaseURL:       "https://provider.test/v1",
		Model:         "gpt-5-nano-2025-08-07",
		APIKey:        "bad-key",
		APIKeyEnvVar:  "OPENAI_API_KEY",
	}, client)
	if err != nil {
		t.Fatalf("NewResponsesAgent() error = %v", err)
	}

	_, err = agent.Respond(context.Background(), controller.AgentInput{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected provider error")
	}

	if !strings.Contains(err.Error(), "bad API key") {
		t.Fatalf("expected provider error message, got %v", err)
	}
}

func TestResponsesAgentMapsApprovalAndPlan(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"output":[
				{
					"type":"message",
					"content":[
						{
							"type":"output_text",
							"text":"{\"message\":\"This action requires approval.\",\"plan_summary\":\"Inspect before deletion.\",\"plan_steps\":[\"Review the target directory.\",\"Confirm the removal scope.\"],\"proposal_kind\":\"\",\"proposal_command\":\"\",\"proposal_patch\":\"\",\"proposal_description\":\"\",\"approval_kind\":\"command\",\"approval_title\":\"Destructive command approval\",\"approval_summary\":\"rm -rf tmp\",\"approval_command\":\"rm -rf tmp\",\"approval_patch\":\"\",\"approval_risk\":\"high\"}"
						}
					]
				}
			]
		}`), nil
	})}

	agent, err := NewResponsesAgent(Profile{
		BackendFamily: BackendResponsesHTTP,
		Preset:        PresetOpenAI,
		AuthMethod:    AuthAPIKey,
		BaseURL:       "https://provider.test/v1",
		Model:         "gpt-5-nano-2025-08-07",
		APIKey:        "test-key",
		APIKeyEnvVar:  "OPENAI_API_KEY",
	}, client)
	if err != nil {
		t.Fatalf("NewResponsesAgent() error = %v", err)
	}

	response, err := agent.Respond(context.Background(), controller.AgentInput{
		Prompt: "rm -rf tmp",
	})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if response.Plan == nil || len(response.Plan.Steps) != 2 {
		t.Fatalf("expected plan with 2 steps, got %#v", response.Plan)
	}
	if response.Approval == nil {
		t.Fatal("expected approval request")
	}
	if response.Approval.Kind != controller.ApprovalCommand {
		t.Fatalf("expected command approval, got %s", response.Approval.Kind)
	}
	if response.Approval.Risk != controller.RiskHigh {
		t.Fatalf("expected high risk, got %s", response.Approval.Risk)
	}
	if response.Approval.ID == "" {
		t.Fatal("expected generated approval ID")
	}
}

func TestResponsesAgentMapsKeysProposal(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"output":[
				{
					"type":"message",
					"content":[
						{
							"type":"output_text",
							"text":"{\"message\":\"I can send Enter to continue.\",\"plan_summary\":\"\",\"plan_steps\":[],\"proposal_kind\":\"keys\",\"proposal_command\":\"\",\"proposal_keys\":\"\\n\",\"proposal_patch\":\"\",\"proposal_description\":\"Send Enter to the active terminal.\",\"approval_kind\":\"\",\"approval_title\":\"\",\"approval_summary\":\"\",\"approval_command\":\"\",\"approval_patch\":\"\",\"approval_risk\":\"\"}"
						}
					]
				}
			]
		}`), nil
	})}

	agent, err := NewResponsesAgent(Profile{
		BackendFamily: BackendResponsesHTTP,
		Preset:        PresetOpenAI,
		AuthMethod:    AuthAPIKey,
		BaseURL:       "https://provider.test/v1",
		Model:         "gpt-5-nano-2025-08-07",
		APIKey:        "test-key",
		APIKeyEnvVar:  "OPENAI_API_KEY",
	}, client)
	if err != nil {
		t.Fatalf("NewResponsesAgent() error = %v", err)
	}

	response, err := agent.Respond(context.Background(), controller.AgentInput{
		Prompt: "go ahead and press enter",
	})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if response.Proposal == nil {
		t.Fatal("expected keys proposal")
	}
	if response.Proposal.Kind != controller.ProposalKeys {
		t.Fatalf("expected keys proposal, got %s", response.Proposal.Kind)
	}
	if response.Proposal.Keys != "\n" {
		t.Fatalf("expected enter key proposal, got %#v", response.Proposal.Keys)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func jsonResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

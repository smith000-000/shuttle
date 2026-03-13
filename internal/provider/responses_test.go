package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"aiterm/internal/controller"
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

		return jsonResponse(http.StatusOK, `{
			"id":"resp_123",
			"object":"response",
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

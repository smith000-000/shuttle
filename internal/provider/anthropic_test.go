package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"aiterm/internal/controller"
)

func TestAnthropicAgentRespondsWithStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("expected /v1/messages, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("x-api-key"); got != "anthropic-key" {
			t.Fatalf("expected x-api-key header, got %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != anthropicVersionHeaderValue {
			t.Fatalf("expected anthropic-version header, got %q", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if payload["model"] != "claude-sonnet-4-20250514" {
			t.Fatalf("expected model, got %#v", payload["model"])
		}
		if payload["max_tokens"] != float64(1200) {
			t.Fatalf("expected max_tokens, got %#v", payload["max_tokens"])
		}
		if _, ok := payload["system"].(string); !ok {
			t.Fatalf("expected system prompt, got %#v", payload["system"])
		}

		io.WriteString(w, `{"id":"msg_123","type":"message","model":"claude-sonnet-4-20250514","content":[{"type":"text","text":"{\"message\":\"Anthropic path works.\",\"plan_summary\":\"\",\"plan_steps\":[],\"proposal_kind\":\"\",\"proposal_command\":\"\",\"proposal_patch\":\"\",\"proposal_description\":\"\",\"approval_kind\":\"\",\"approval_title\":\"\",\"approval_summary\":\"\",\"approval_command\":\"\",\"approval_patch\":\"\",\"approval_risk\":\"\"}"}]}`)
	}))
	defer server.Close()

	agent, err := NewAnthropicAgent(Profile{
		BackendFamily: BackendAnthropic,
		Preset:        PresetAnthropic,
		AuthMethod:    AuthAPIKey,
		BaseURL:       server.URL,
		Model:         "claude-sonnet-4-20250514",
		APIKey:        "anthropic-key",
		APIKeyEnvVar:  "ANTHROPIC_API_KEY",
	}, server.Client())
	if err != nil {
		t.Fatalf("NewAnthropicAgent() error = %v", err)
	}

	response, err := agent.Respond(context.Background(), controller.AgentInput{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if response.Message != "Anthropic path works." {
		t.Fatalf("expected response message, got %q", response.Message)
	}
	if response.ModelInfo == nil || response.ModelInfo.ResponseModel != "claude-sonnet-4-20250514" {
		t.Fatalf("expected model info, got %#v", response.ModelInfo)
	}
}

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

func TestOllamaAgentRespondsWithStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("expected /api/chat, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}

		if payload["model"] != "qwen2.5-coder:7b" {
			t.Fatalf("expected model qwen2.5-coder:7b, got %#v", payload["model"])
		}
		if payload["stream"] != false {
			t.Fatalf("expected stream false, got %#v", payload["stream"])
		}
		if _, ok := payload["format"].(map[string]any); !ok {
			t.Fatalf("expected schema format, got %#v", payload["format"])
		}

		options, ok := payload["options"].(map[string]any)
		if !ok || options["temperature"] != float64(0) {
			t.Fatalf("expected temperature 0, got %#v", payload["options"])
		}

		io.WriteString(w, `{"model":"qwen2.5-coder:7b","message":{"role":"assistant","content":"{\"message\":\"Ollama path works.\",\"plan_summary\":\"\",\"plan_steps\":[],\"proposal_kind\":\"\",\"proposal_command\":\"\",\"proposal_patch\":\"\",\"proposal_description\":\"\",\"approval_kind\":\"\",\"approval_title\":\"\",\"approval_summary\":\"\",\"approval_command\":\"\",\"approval_patch\":\"\",\"approval_risk\":\"\"}"}}`)
	}))
	defer server.Close()

	agent, err := NewOllamaAgent(Profile{
		BackendFamily: BackendOllama,
		Preset:        PresetOllama,
		AuthMethod:    AuthNone,
		BaseURL:       server.URL + "/api",
		Model:         "qwen2.5-coder:7b",
	}, server.Client())
	if err != nil {
		t.Fatalf("NewOllamaAgent() error = %v", err)
	}

	response, err := agent.Respond(context.Background(), controller.AgentInput{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if response.Message != "Ollama path works." {
		t.Fatalf("expected response message, got %q", response.Message)
	}
	if response.ModelInfo == nil || response.ModelInfo.ResponseModel != "qwen2.5-coder:7b" {
		t.Fatalf("expected model info, got %#v", response.ModelInfo)
	}
}

func TestNewOllamaAgentRequiresModel(t *testing.T) {
	_, err := NewOllamaAgent(Profile{
		BackendFamily: BackendOllama,
		Preset:        PresetOllama,
		AuthMethod:    AuthNone,
		BaseURL:       "http://localhost:11434/api",
	}, nil)
	if err == nil {
		t.Fatal("expected missing model error")
	}
}

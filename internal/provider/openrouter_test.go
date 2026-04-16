package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aiterm/internal/controller"
)

func TestOpenRouterAgentAppliesProviderPolicyForStructuredModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/responses" {
			t.Fatalf("expected /api/v1/responses, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer openrouter-key" {
			t.Fatalf("expected bearer auth, got %q", got)
		}
		if got := r.Header.Get("X-Title"); got != "Shuttle" {
			t.Fatalf("expected X-Title header, got %q", got)
		}

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}

		if payload["model"] != "qwen/qwen3.5-9b" {
			t.Fatalf("expected model qwen/qwen3.5-9b, got %#v", payload["model"])
		}
		if payload["max_output_tokens"] != float64(1200) {
			t.Fatalf("expected max_output_tokens=1200, got %#v", payload["max_output_tokens"])
		}

		reasoning, ok := payload["reasoning"].(map[string]any)
		if !ok {
			t.Fatalf("expected reasoning config, got %#v", payload["reasoning"])
		}
		if reasoning["effort"] != "medium" || reasoning["exclude"] != true {
			t.Fatalf("unexpected reasoning config %#v", reasoning)
		}

		text, ok := payload["text"].(map[string]any)
		if !ok {
			t.Fatalf("expected structured output config, got %#v", payload["text"])
		}
		format, ok := text["format"].(map[string]any)
		if !ok || format["type"] != "json_schema" {
			t.Fatalf("expected json schema format, got %#v", text)
		}

		providerConfig, ok := payload["provider"].(map[string]any)
		if !ok || providerConfig["require_parameters"] != true {
			t.Fatalf("expected provider.require_parameters=true, got %#v", payload["provider"])
		}

		if _, err := io.WriteString(w, `{"model":"qwen/qwen3.5-9b-20260310","output_text":"{\"message\":\"OpenRouter path works.\"}"}`); err != nil {
			t.Fatalf("WriteString() error = %v", err)
		}
	}))
	defer server.Close()

	agent, err := NewOpenRouterAgent(Profile{
		BackendFamily: BackendOpenRouter,
		Preset:        PresetOpenRouter,
		AuthMethod:    AuthAPIKey,
		BaseURL:       server.URL + "/api/v1",
		Model:         "qwen/qwen3.5-9b",
		APIKey:        "openrouter-key",
		APIKeyEnvVar:  "OPENROUTER_API_KEY",
		SelectedModel: &ModelOption{
			ID:                  "qwen/qwen3.5-9b",
			SupportedParameters: []string{"max_tokens", "reasoning", "structured_outputs"},
		},
	}, server.Client())
	if err != nil {
		t.Fatalf("NewOpenRouterAgent() error = %v", err)
	}

	response, err := agent.Respond(context.Background(), controller.AgentInput{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if response.Message != "OpenRouter path works." {
		t.Fatalf("expected response message, got %q", response.Message)
	}
	if response.ModelInfo == nil || response.ModelInfo.ResponseModel != "qwen/qwen3.5-9b-20260310" {
		t.Fatalf("expected routed model metadata, got %#v", response.ModelInfo)
	}
}

func TestOpenRouterAgentFallsBackToPromptJSONWhenModelLacksStructuredOutputs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}

		if _, ok := payload["text"]; ok {
			t.Fatalf("expected no text schema config, got %#v", payload["text"])
		}
		if _, ok := payload["reasoning"]; ok {
			t.Fatalf("expected no reasoning config, got %#v", payload["reasoning"])
		}
		if payload["max_output_tokens"] != float64(900) {
			t.Fatalf("expected capped max_output_tokens=900, got %#v", payload["max_output_tokens"])
		}

		input, ok := payload["input"].([]any)
		if !ok || len(input) == 0 {
			t.Fatalf("expected input messages, got %#v", payload["input"])
		}
		first, ok := input[0].(map[string]any)
		if !ok {
			t.Fatalf("expected system message, got %#v", input[0])
		}
		content, ok := first["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("expected system content, got %#v", first["content"])
		}
		textContent, ok := content[0].(map[string]any)
		if !ok {
			t.Fatalf("expected text content, got %#v", content[0])
		}
		if !strings.Contains(textContent["text"].(string), "Return only a valid JSON object") {
			t.Fatalf("expected prompt-only JSON instruction, got %q", textContent["text"])
		}

		if _, err := io.WriteString(w, `{"model":"anthropic/claude-3-haiku","output_text":"{\"message\":\"Fallback path works.\"}"}`); err != nil {
			t.Fatalf("WriteString() error = %v", err)
		}
	}))
	defer server.Close()

	agent, err := NewOpenRouterAgent(Profile{
		BackendFamily: BackendOpenRouter,
		Preset:        PresetOpenRouter,
		AuthMethod:    AuthAPIKey,
		BaseURL:       server.URL + "/api/v1",
		Model:         "anthropic/claude-3-haiku",
		APIKey:        "openrouter-key",
		APIKeyEnvVar:  "OPENROUTER_API_KEY",
		SelectedModel: &ModelOption{
			ID:                  "anthropic/claude-3-haiku",
			SupportedParameters: []string{"max_tokens", "stop", "temperature"},
			TopProvider: ModelTopProvider{
				MaxCompletionTokens: 900,
			},
		},
	}, server.Client())
	if err != nil {
		t.Fatalf("NewOpenRouterAgent() error = %v", err)
	}

	response, err := agent.Respond(context.Background(), controller.AgentInput{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if response.Message != "Fallback path works." {
		t.Fatalf("expected fallback response, got %q", response.Message)
	}
}

func TestOpenRouterAgentAutoModelOmitsReasoningOverride(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}

		if _, ok := payload["reasoning"]; ok {
			t.Fatalf("expected no reasoning override for openrouter/auto, got %#v", payload["reasoning"])
		}
		if _, ok := payload["text"]; !ok {
			t.Fatalf("expected structured outputs for openrouter/auto, got %#v", payload["text"])
		}

		if _, err := io.WriteString(w, `{"model":"openai/gpt-5-nano-2025-08-07","output_text":"{\"message\":\"Auto path works.\"}"}`); err != nil {
			t.Fatalf("WriteString() error = %v", err)
		}
	}))
	defer server.Close()

	agent, err := NewOpenRouterAgent(Profile{
		BackendFamily: BackendOpenRouter,
		Preset:        PresetOpenRouter,
		AuthMethod:    AuthAPIKey,
		BaseURL:       server.URL + "/api/v1",
		Model:         "openrouter/auto",
		APIKey:        "openrouter-key",
		APIKeyEnvVar:  "OPENROUTER_API_KEY",
		SelectedModel: &ModelOption{
			ID:                  "openrouter/auto",
			CanonicalSlug:       "openrouter/auto",
			SupportedParameters: []string{"max_tokens", "reasoning", "structured_outputs"},
		},
	}, server.Client())
	if err != nil {
		t.Fatalf("NewOpenRouterAgent() error = %v", err)
	}

	response, err := agent.Respond(context.Background(), controller.AgentInput{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if response.Message != "Auto path works." {
		t.Fatalf("expected auto response, got %q", response.Message)
	}
}

func TestOpenRouterAgentUsesConfiguredReasoningEffort(t *testing.T) {
	policy := openRouterPolicyForProfile(Profile{
		Preset:          PresetOpenRouter,
		Thinking:        string(ThinkingOn),
		ReasoningEffort: string(ReasoningEffortHigh),
		SelectedModel:   &ModelOption{ID: "openai/gpt-5", SupportedParameters: []string{"reasoning", "structured_outputs", "max_tokens"}},
	})
	if policy.Reasoning == nil || policy.Reasoning.Effort != string(ReasoningEffortHigh) || !policy.Reasoning.Exclude {
		t.Fatalf("expected reasoning effort override, got %#v", policy.Reasoning)
	}
}

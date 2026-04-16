package provider

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListModelsOpenAI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("expected /v1/models, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer openai-key" {
			t.Fatalf("expected auth header, got %q", got)
		}
		if _, err := io.WriteString(w, `{"data":[{"id":"gpt-5-mini"},{"id":"gpt-5-nano"}]}`); err != nil {
			t.Fatalf("WriteString() error = %v", err)
		}
	}))
	defer server.Close()

	models, err := ListModels(Profile{
		BackendFamily: BackendResponsesHTTP,
		Preset:        PresetOpenAI,
		AuthMethod:    AuthAPIKey,
		BaseURL:       server.URL + "/v1",
		APIKey:        "openai-key",
	}, server.Client())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}

	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "gpt-5-mini" || models[1].ID != "gpt-5-nano" {
		t.Fatalf("unexpected models %#v", models)
	}
}

func TestListModelsOpenRouter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models/user" {
			t.Fatalf("expected /api/v1/models/user, got %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Title"); got != "Shuttle" {
			t.Fatalf("expected X-Title header, got %q", got)
		}
		if got := r.Header.Get("X-OpenRouter-Title"); got != "Shuttle" {
			t.Fatalf("expected compatibility title header, got %q", got)
		}
		if _, err := io.WriteString(w, `{"data":[{"id":"openrouter/auto","canonical_slug":"openrouter/auto","name":"Auto","description":"Routes automatically","context_length":200000,"top_provider":{"context_length":200000,"max_completion_tokens":64000,"is_moderated":false},"architecture":{"modality":"text+image->text","input_modalities":["text","image"],"output_modalities":["text"],"tokenizer":"Router"},"supported_parameters":["max_tokens","structured_outputs","reasoning"],"default_parameters":{"temperature":1},"pricing":{"prompt":"0","completion":"0"}},{"id":"openai/gpt-5-mini","canonical_slug":"openai/gpt-5-mini-20260305","name":"GPT-5 Mini","context_length":128000,"top_provider":{"context_length":128000,"max_completion_tokens":32768,"is_moderated":true},"architecture":{"modality":"text->text","input_modalities":["text"],"output_modalities":["text"],"tokenizer":"GPT"},"supported_parameters":["max_tokens","response_format"],"pricing":{"prompt":"0.00000025","completion":"0.000002"}}]}`); err != nil {
			t.Fatalf("WriteString() error = %v", err)
		}
	}))
	defer server.Close()

	models, err := ListModels(Profile{
		BackendFamily: BackendOpenRouter,
		Preset:        PresetOpenRouter,
		AuthMethod:    AuthAPIKey,
		BaseURL:       server.URL + "/api/v1",
		APIKey:        "openrouter-key",
	}, server.Client())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}

	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "openai/gpt-5-mini" || models[1].ID != "openrouter/auto" {
		t.Fatalf("expected sorted models, got %#v", models)
	}
	if models[1].CanonicalSlug != "openrouter/auto" {
		t.Fatalf("expected canonical slug, got %#v", models[1])
	}
	if !models[1].SupportsAnyParameter("structured_outputs") {
		t.Fatalf("expected structured_outputs support, got %#v", models[1].SupportedParameters)
	}
	if models[1].MaxCompletionTokens != 64000 {
		t.Fatalf("expected max completion tokens, got %#v", models[1])
	}
	if models[0].Architecture.Tokenizer != "GPT" {
		t.Fatalf("expected tokenizer metadata, got %#v", models[0].Architecture)
	}
}

func TestListModelsAnthropic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("expected /v1/models, got %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "anthropic-key" {
			t.Fatalf("expected x-api-key header, got %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != anthropicVersionHeaderValue {
			t.Fatalf("expected anthropic-version header, got %q", got)
		}
		if _, err := io.WriteString(w, `{"data":[{"id":"claude-sonnet-4-20250514","display_name":"Claude Sonnet 4","type":"model"},{"id":"claude-3-5-haiku-20241022","display_name":"Claude 3.5 Haiku","type":"model"}]}`); err != nil {
			t.Fatalf("WriteString() error = %v", err)
		}
	}))
	defer server.Close()

	models, err := ListModels(Profile{
		BackendFamily: BackendAnthropic,
		Preset:        PresetAnthropic,
		AuthMethod:    AuthAPIKey,
		BaseURL:       server.URL,
		APIKey:        "anthropic-key",
	}, server.Client())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}

	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "claude-3-5-haiku-20241022" || models[1].ID != "claude-sonnet-4-20250514" {
		t.Fatalf("unexpected anthropic models %#v", models)
	}
}

func TestListModelsCodexCLI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("expected /v1/models, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer codex-key" {
			t.Fatalf("expected auth header, got %q", got)
		}
		if _, err := io.WriteString(w, `{"data":[
			{"id":"gpt-5.4"},
			{"id":"gpt-5.3-codex"},
			{"id":"gpt-4.1"},
			{"id":"o4-mini"}
		]}`); err != nil {
			t.Fatalf("WriteString() error = %v", err)
		}
	}))
	defer server.Close()

	models, err := ListModels(Profile{
		BackendFamily: BackendCLIAgent,
		Preset:        PresetCodexCLI,
		Model:         "gpt-5.2-codex",
		BaseURL:       server.URL + "/v1",
		APIKey:        "codex-key",
	}, server.Client())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}

	if len(models) != 3 {
		t.Fatalf("expected 3 codex suggestions, got %#v", models)
	}
	if models[0].ID != "gpt-5.2-codex" || models[1].ID != "gpt-5.3-codex" || models[2].ID != "gpt-5.4" {
		t.Fatalf("unexpected codex model list %#v", models)
	}
	for _, model := range models {
		if strings.Contains(model.ID, "gpt-4.1") || strings.Contains(model.ID, "o4-mini") {
			t.Fatalf("unexpected non-codex suggestion %#v", models)
		}
		if !strings.Contains(model.Description, "Codex CLI's own picker may differ") {
			t.Fatalf("expected warning text in description, got %#v", model)
		}
	}
}

func TestListModelsCodexCLIFallsBackToCuratedList(t *testing.T) {
	models, err := ListModels(Profile{
		BackendFamily: BackendCLIAgent,
		Preset:        PresetCodexCLI,
		Model:         "gpt-5.2-codex",
	}, nil)
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) == 0 {
		t.Fatal("expected curated codex models")
	}
	if models[0].ID == "" {
		t.Fatalf("expected non-empty curated model IDs %#v", models)
	}
}

func TestListModelsOllama(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("expected /api/tags, got %s", r.URL.Path)
		}
		if _, err := io.WriteString(w, `{"models":[{"name":"qwen2.5-coder:7b","model":"qwen2.5-coder:7b","details":{"family":"qwen2","parameter_size":"7B","quantization_level":"Q4_K_M"}},{"name":"llama3.1:8b","model":"llama3.1:8b","details":{"family":"llama","parameter_size":"8B","quantization_level":"Q8_0"}}]}`); err != nil {
			t.Fatalf("WriteString() error = %v", err)
		}
	}))
	defer server.Close()

	models, err := ListModels(Profile{
		BackendFamily: BackendOllama,
		Preset:        PresetOllama,
		BaseURL:       server.URL + "/api",
		AuthMethod:    AuthNone,
	}, server.Client())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}

	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "llama3.1:8b" || models[1].ID != "qwen2.5-coder:7b" {
		t.Fatalf("unexpected ollama models %#v", models)
	}
	if models[1].Description != "qwen2 7B Q4_K_M" {
		t.Fatalf("expected model description, got %#v", models[1])
	}
}

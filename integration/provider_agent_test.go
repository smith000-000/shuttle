package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenRouterAgentOneShotEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}

	testCases := []struct {
		name                string
		model               string
		responseModel       string
		supportedParameters []string
		maxCompletionTokens int
		expectStructured    bool
		expectReasoning     bool
		expectedMaxOutput   float64
	}{
		{
			name:                "structured_qwen_model",
			model:               "qwen/qwen3.5-9b",
			responseModel:       "qwen/qwen3.5-9b-20260310",
			supportedParameters: []string{"max_tokens", "reasoning", "structured_outputs"},
			expectStructured:    true,
			expectReasoning:     true,
			expectedMaxOutput:   1200,
		},
		{
			name:                "prompt_only_claude_model",
			model:               "anthropic/claude-3-haiku",
			responseModel:       "anthropic/claude-3-haiku",
			supportedParameters: []string{"max_tokens", "stop", "temperature"},
			maxCompletionTokens: 900,
			expectStructured:    false,
			expectReasoning:     false,
			expectedMaxOutput:   900,
		},
		{
			name:                "auto_router_model",
			model:               "openrouter/auto",
			responseModel:       "openai/gpt-5-nano-2025-08-07",
			supportedParameters: []string{"max_tokens", "reasoning", "structured_outputs", "response_format"},
			expectStructured:    true,
			expectReasoning:     false,
			expectedMaxOutput:   1200,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var captured map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/models/user":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(fmt.Sprintf(`{
						"data":[
							{
								"id":%q,
								"canonical_slug":%q,
								"name":"Test Model",
								"context_length":262144,
								"top_provider":{"context_length":262144,"max_completion_tokens":%d,"is_moderated":false},
								"architecture":{"modality":"text->text","input_modalities":["text"],"output_modalities":["text"],"tokenizer":"Test"},
								"supported_parameters":%s,
								"pricing":{"prompt":"0","completion":"0"}
							}
						]
					}`, tc.model, tc.responseModel, tc.maxCompletionTokens, mustJSON(tc.supportedParameters))))
					return
				case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
					if got := r.Header.Get("Authorization"); got != "Bearer test-openrouter-key" {
						t.Fatalf("expected bearer auth header, got %q", got)
					}
					if got := r.Header.Get("X-Title"); got != "Shuttle" {
						t.Fatalf("expected OpenRouter title header, got %q", got)
					}
					if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
						t.Fatalf("Decode() error = %v", err)
					}

					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(fmt.Sprintf(`{
						"model":%q,
						"output_text":"{\"message\":\"INTEGRATION_OK\",\"plan_summary\":\"\",\"plan_steps\":[],\"proposal_kind\":\"\",\"proposal_command\":\"\",\"proposal_patch\":\"\",\"proposal_description\":\"\",\"approval_kind\":\"\",\"approval_title\":\"\",\"approval_summary\":\"\",\"approval_command\":\"\",\"approval_patch\":\"\",\"approval_risk\":\"\"}"
					}`, tc.responseModel)))
					return
				default:
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			}))
			defer server.Close()

			socketName := fmt.Sprintf("shuttle-provider-e2e-%d", time.Now().UnixNano())
			sessionName := fmt.Sprintf("shuttle-provider-e2e-%d", time.Now().UnixNano())
			stateDir := t.TempDir()

			t.Cleanup(func() {
				_ = exec.Command("tmux", "-L", socketName, "kill-session", "-t", sessionName).Run()
			})

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			command := exec.CommandContext(
				ctx,
				"go",
				"run",
				"./cmd/shuttle",
				"--socket", socketName,
				"--session", sessionName,
				"--state-dir", stateDir,
				"--provider", "openrouter",
				"--auth", "api_key",
				"--base-url", server.URL+"/v1",
				"--model", tc.model,
				"--agent", "Reply with exactly INTEGRATION_OK.",
			)
			command.Dir = ".."
			command.Env = append(os.Environ(),
				"OPENROUTER_API_KEY=test-openrouter-key",
				"OPENAI_API_KEY=",
				"SHUTTLE_MODEL=",
				"GOCACHE=/tmp/aiterm-go-build",
				"GOTMPDIR=/tmp",
			)

			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("go run shuttle error = %v\noutput:\n%s", err, output)
			}

			if captured["max_output_tokens"] != tc.expectedMaxOutput {
				t.Fatalf("expected max_output_tokens=%v, got %#v", tc.expectedMaxOutput, captured["max_output_tokens"])
			}

			if tc.expectReasoning {
				reasoning, ok := captured["reasoning"].(map[string]any)
				if !ok {
					t.Fatalf("expected reasoning config, got %#v", captured["reasoning"])
				}
				if reasoning["effort"] != "medium" || reasoning["exclude"] != true {
					t.Fatalf("unexpected reasoning config %#v", reasoning)
				}
			} else if _, ok := captured["reasoning"]; ok {
				t.Fatalf("expected no reasoning config, got %#v", captured["reasoning"])
			}

			if tc.expectStructured {
				text, ok := captured["text"].(map[string]any)
				if !ok {
					t.Fatalf("expected structured text config, got %#v", captured["text"])
				}
				format, ok := text["format"].(map[string]any)
				if !ok || format["type"] != "json_schema" {
					t.Fatalf("expected json schema format, got %#v", captured["text"])
				}
			} else if _, ok := captured["text"]; ok {
				t.Fatalf("expected no structured text config, got %#v", captured["text"])
			}

			providerConfig, ok := captured["provider"].(map[string]any)
			if !ok || providerConfig["require_parameters"] != true {
				t.Fatalf("expected provider.require_parameters=true, got %#v", captured["provider"])
			}

			stdout := string(output)
			if !strings.Contains(stdout, "[AGENT_MESSAGE]\nINTEGRATION_OK") {
				t.Fatalf("expected agent output, got:\n%s", stdout)
			}
			if !strings.Contains(stdout, "[MODEL_INFO]") {
				t.Fatalf("expected model info output, got:\n%s", stdout)
			}
			if !strings.Contains(stdout, "requested: "+tc.model) {
				t.Fatalf("expected requested model in output, got:\n%s", stdout)
			}
			if !strings.Contains(stdout, "response: "+tc.responseModel) {
				t.Fatalf("expected response model in output, got:\n%s", stdout)
			}
		})
	}
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestCodexCLIOneShotEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}

	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	content := `#!/bin/sh
set -eu

if [ "${1-}" = "login" ] && [ "${2-}" = "status" ]; then
  printf '%s\n' "Logged in using ChatGPT"
  exit 0
fi

if [ "${1-}" = "exec" ]; then
  shift
  output=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --output-last-message)
        output="$2"
        shift 2
        ;;
      *)
        shift
        ;;
    esac
  done
  printf '%s' '{"message":"CODEX_OK","plan_summary":"","plan_steps":[],"proposal_kind":"","proposal_command":"","proposal_patch":"","proposal_description":"","approval_kind":"","approval_title":"","approval_summary":"","approval_command":"","approval_patch":"","approval_risk":""}' > "$output"
  exit 0
fi

echo "unexpected command" >&2
exit 1
`
	if err := os.WriteFile(codexPath, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	socketName := fmt.Sprintf("shuttle-codex-e2e-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-codex-e2e-%d", time.Now().UnixNano())
	stateDir := t.TempDir()

	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socketName, "kill-session", "-t", sessionName).Run()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	command := exec.CommandContext(
		ctx,
		"go",
		"run",
		"./cmd/shuttle",
		"--socket", socketName,
		"--session", sessionName,
		"--state-dir", stateDir,
		"--provider", "codex_cli",
		"--model", "gpt-5.2-codex",
		"--agent", "Reply with exactly CODEX_OK.",
	)
	command.Dir = ".."
	command.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"GOCACHE=/tmp/aiterm-go-build",
		"GOTMPDIR=/tmp",
	)

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go run shuttle error = %v\noutput:\n%s", err, output)
	}

	stdout := string(output)
	if !strings.Contains(stdout, "[AGENT_MESSAGE]\nCODEX_OK") {
		t.Fatalf("expected agent output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "provider: codex_cli") {
		t.Fatalf("expected codex model info output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "requested: gpt-5.2-codex") {
		t.Fatalf("expected codex requested model output, got:\n%s", stdout)
	}
}

func TestOllamaOneShotEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/chat" {
			t.Fatalf("expected /api/chat, got %s", r.URL.Path)
		}

		var captured map[string]any
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if captured["model"] != "qwen2.5-coder:7b" {
			t.Fatalf("expected model qwen2.5-coder:7b, got %#v", captured["model"])
		}
		if captured["stream"] != false {
			t.Fatalf("expected stream=false, got %#v", captured["stream"])
		}
		if _, ok := captured["format"].(map[string]any); !ok {
			t.Fatalf("expected schema format, got %#v", captured["format"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model":"qwen2.5-coder:7b",
			"message":{"role":"assistant","content":"{\"message\":\"OLLAMA_OK\",\"plan_summary\":\"\",\"plan_steps\":[],\"proposal_kind\":\"\",\"proposal_command\":\"\",\"proposal_patch\":\"\",\"proposal_description\":\"\",\"approval_kind\":\"\",\"approval_title\":\"\",\"approval_summary\":\"\",\"approval_command\":\"\",\"approval_patch\":\"\",\"approval_risk\":\"\"}"}
		}`))
	}))
	defer server.Close()

	socketName := fmt.Sprintf("shuttle-ollama-e2e-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-ollama-e2e-%d", time.Now().UnixNano())
	stateDir := t.TempDir()

	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socketName, "kill-session", "-t", sessionName).Run()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	command := exec.CommandContext(
		ctx,
		"go",
		"run",
		"./cmd/shuttle",
		"--socket", socketName,
		"--session", sessionName,
		"--state-dir", stateDir,
		"--provider", "ollama",
		"--base-url", server.URL,
		"--model", "qwen2.5-coder:7b",
		"--agent", "Reply with exactly OLLAMA_OK.",
	)
	command.Dir = ".."
	command.Env = append(os.Environ(),
		"GOCACHE=/tmp/aiterm-go-build",
		"GOTMPDIR=/tmp",
	)

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go run shuttle error = %v\noutput:\n%s", err, output)
	}

	stdout := string(output)
	if !strings.Contains(stdout, "[AGENT_MESSAGE]\nOLLAMA_OK") {
		t.Fatalf("expected agent output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "provider: ollama") {
		t.Fatalf("expected ollama model info output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "response: qwen2.5-coder:7b") {
		t.Fatalf("expected ollama response model output, got:\n%s", stdout)
	}
}

func TestAnthropicOneShotEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("expected /v1/messages, got %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "anthropic-test-key" {
			t.Fatalf("expected x-api-key header, got %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Fatalf("expected anthropic-version header, got %q", got)
		}

		var captured map[string]any
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if captured["model"] != "claude-sonnet-4-20250514" {
			t.Fatalf("expected model, got %#v", captured["model"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_123",
			"type":"message",
			"model":"claude-sonnet-4-20250514",
			"content":[{"type":"text","text":"{\"message\":\"ANTHROPIC_OK\",\"plan_summary\":\"\",\"plan_steps\":[],\"proposal_kind\":\"\",\"proposal_command\":\"\",\"proposal_patch\":\"\",\"proposal_description\":\"\",\"approval_kind\":\"\",\"approval_title\":\"\",\"approval_summary\":\"\",\"approval_command\":\"\",\"approval_patch\":\"\",\"approval_risk\":\"\"}"}]
		}`))
	}))
	defer server.Close()

	socketName := fmt.Sprintf("shuttle-anthropic-e2e-%d", time.Now().UnixNano())
	sessionName := fmt.Sprintf("shuttle-anthropic-e2e-%d", time.Now().UnixNano())
	stateDir := t.TempDir()

	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socketName, "kill-session", "-t", sessionName).Run()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	command := exec.CommandContext(
		ctx,
		"go",
		"run",
		"./cmd/shuttle",
		"--socket", socketName,
		"--session", sessionName,
		"--state-dir", stateDir,
		"--provider", "anthropic",
		"--base-url", server.URL,
		"--model", "claude-sonnet-4-20250514",
		"--agent", "Reply with exactly ANTHROPIC_OK.",
	)
	command.Dir = ".."
	command.Env = append(os.Environ(),
		"ANTHROPIC_API_KEY=anthropic-test-key",
		"GOCACHE=/tmp/aiterm-go-build",
		"GOTMPDIR=/tmp",
	)

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go run shuttle error = %v\noutput:\n%s", err, output)
	}

	stdout := string(output)
	if !strings.Contains(stdout, "[AGENT_MESSAGE]\nANTHROPIC_OK") {
		t.Fatalf("expected agent output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "provider: anthropic") {
		t.Fatalf("expected anthropic model info output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "response: claude-sonnet-4-20250514") {
		t.Fatalf("expected anthropic response model output, got:\n%s", stdout)
	}
}

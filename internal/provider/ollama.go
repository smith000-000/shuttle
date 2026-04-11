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

	"aiterm/internal/controller"
)

type OllamaAgent struct {
	ResponsesAgent
}

type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	Format   map[string]any      `json:"format,omitempty"`
	Options  map[string]any      `json:"options,omitempty"`
	Think    *bool               `json:"think,omitempty"`
}

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatResponse struct {
	Model   string            `json:"model"`
	Message ollamaChatMessage `json:"message"`
	Error   string            `json:"error"`
}

func NewOllamaAgent(profile Profile, client *http.Client) (*OllamaAgent, error) {
	if profile.BackendFamily != BackendOllama {
		return nil, fmt.Errorf("profile %q is not an Ollama backend", profile.Preset)
	}
	if strings.TrimSpace(profile.BaseURL) == "" {
		return nil, errors.New("ollama base URL must not be empty")
	}
	if strings.TrimSpace(profile.Model) == "" {
		return nil, errors.New("ollama provider requires a model; use --model or onboarding model selection")
	}
	if client == nil {
		client = &http.Client{}
	}

	return &OllamaAgent{
		ResponsesAgent: ResponsesAgent{
			profile: profile,
			client:  client,
		},
	}, nil
}

func (a *OllamaAgent) Respond(ctx context.Context, input controller.AgentInput) (controller.AgentResponse, error) {
	requestBody := ollamaChatRequest{
		Model: a.profile.Model,
		Messages: []ollamaChatMessage{
			{
				Role:    "system",
				Content: shuttleSystemPrompt + "\n\nReturn only a valid JSON object that matches this schema exactly:\n" + mustMarshalSchema(),
			},
			{
				Role:    "user",
				Content: buildTurnContext(input),
			},
		},
		Stream: false,
		Format: shuttleAgentResponseSchema(),
		Options: map[string]any{
			"temperature": 0,
		},
	}
	if SupportsThinking(a.profile) {
		thinking := ThinkingEnabled(a.profile)
		requestBody.Think = &thinking
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("marshal ollama request: %w", err)
	}

	endpoint, err := ollamaChatEndpoint(a.profile.BaseURL)
	if err != nil {
		return controller.AgentResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("build ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", shuttleUserAgent)
	if a.profile.AuthMethod == AuthAPIKey && strings.TrimSpace(a.profile.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+a.profile.APIKey)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("request ollama: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("read ollama response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return controller.AgentResponse{}, parseProviderError(resp.StatusCode, body)
	}

	var apiResp ollamaChatResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return controller.AgentResponse{}, fmt.Errorf("decode ollama response: %w", err)
	}
	if strings.TrimSpace(apiResp.Error) != "" {
		return controller.AgentResponse{}, fmt.Errorf("ollama error: %s", strings.TrimSpace(apiResp.Error))
	}

	var structured shuttleStructuredResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(apiResp.Message.Content)), &structured); err != nil {
		return controller.AgentResponse{}, fmt.Errorf("decode ollama structured output: %w", err)
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

func (a *OllamaAgent) CheckHealth(ctx context.Context) error {
	_, err := a.Respond(ctx, controller.AgentInput{
		Prompt: "Respond with a short confirmation that the provider path works.",
	})
	return err
}

func ollamaChatEndpoint(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("parse ollama base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("ollama base URL must include host")
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/chat"
	return parsed.String(), nil
}

func mustMarshalSchema() string {
	schemaJSON, err := json.Marshal(shuttleAgentResponseSchema())
	if err != nil {
		return `{}`
	}

	return string(schemaJSON)
}

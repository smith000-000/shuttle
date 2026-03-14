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
	"time"

	"aiterm/internal/controller"
)

const anthropicVersionHeaderValue = "2023-06-01"

type AnthropicAgent struct {
	ResponsesAgent
}

type anthropicMessagesRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Temperature float64            `json:"temperature,omitempty"`
}

type anthropicMessage struct {
	Role    string                    `json:"role"`
	Content []anthropicMessageContent `json:"content"`
}

type anthropicMessageContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type anthropicMessagesResponse struct {
	ID      string                    `json:"id"`
	Model   string                    `json:"model"`
	Type    string                    `json:"type"`
	Content []anthropicMessageContent `json:"content"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func NewAnthropicAgent(profile Profile, client *http.Client) (*AnthropicAgent, error) {
	if profile.BackendFamily != BackendAnthropic {
		return nil, fmt.Errorf("profile %q is not an Anthropic backend", profile.Preset)
	}
	if strings.TrimSpace(profile.BaseURL) == "" {
		return nil, errors.New("anthropic base URL must not be empty")
	}
	if strings.TrimSpace(profile.Model) == "" {
		return nil, errors.New("anthropic provider requires a model")
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

	return &AnthropicAgent{
		ResponsesAgent: ResponsesAgent{
			profile: profile,
			client:  client,
		},
	}, nil
}

func (a *AnthropicAgent) Respond(ctx context.Context, input controller.AgentInput) (controller.AgentResponse, error) {
	requestBody := anthropicMessagesRequest{
		Model:     a.profile.Model,
		MaxTokens: 1200,
		System:    shuttleSystemPrompt + "\n\nReturn only a valid JSON object that matches this schema exactly:\n" + mustMarshalSchema(),
		Messages: []anthropicMessage{
			{
				Role: "user",
				Content: []anthropicMessageContent{
					{Type: "text", Text: buildTurnContext(input)},
				},
			},
		},
		Temperature: 0,
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("marshal anthropic request: %w", err)
	}

	endpoint, err := anthropicMessagesEndpoint(a.profile.BaseURL)
	if err != nil {
		return controller.AgentResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("build anthropic request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", shuttleUserAgent)
	req.Header.Set("anthropic-version", anthropicVersionHeaderValue)
	if a.profile.AuthMethod == AuthAPIKey {
		req.Header.Set("x-api-key", a.profile.APIKey)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("request anthropic: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("read anthropic response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return controller.AgentResponse{}, parseProviderError(resp.StatusCode, body)
	}

	var apiResp anthropicMessagesResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return controller.AgentResponse{}, fmt.Errorf("decode anthropic response: %w", err)
	}
	if apiResp.Error != nil && strings.TrimSpace(apiResp.Error.Message) != "" {
		return controller.AgentResponse{}, fmt.Errorf("anthropic error: %s", strings.TrimSpace(apiResp.Error.Message))
	}

	responseText, err := anthropicExtractText(apiResp)
	if err != nil {
		return controller.AgentResponse{}, err
	}

	var structured shuttleStructuredResponse
	if err := json.Unmarshal([]byte(responseText), &structured); err != nil {
		return controller.AgentResponse{}, fmt.Errorf("decode anthropic structured output: %w", err)
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

func (a *AnthropicAgent) CheckHealth(ctx context.Context) error {
	_, err := a.Respond(ctx, controller.AgentInput{
		Prompt: "Respond with a short confirmation that the provider path works.",
	})
	return err
}

func anthropicMessagesEndpoint(baseURL string) (string, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return "", errors.New("anthropic base URL must not be empty")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse anthropic base URL: %w", err)
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/v1") {
		parsed.Path = path + "/messages"
	} else {
		parsed.Path = path + "/v1/messages"
	}
	return parsed.String(), nil
}

func anthropicModelsEndpoint(baseURL string) (string, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return "", errors.New("anthropic base URL must not be empty")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse anthropic base URL: %w", err)
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/v1") {
		parsed.Path = path + "/models"
	} else {
		parsed.Path = path + "/v1/models"
	}
	return parsed.String(), nil
}

func anthropicExtractText(response anthropicMessagesResponse) (string, error) {
	fragments := make([]string, 0, len(response.Content))
	for _, content := range response.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			fragments = append(fragments, content.Text)
		}
	}
	if len(fragments) == 0 {
		return "", errors.New("anthropic returned no text content")
	}

	return strings.Join(fragments, "\n"), nil
}

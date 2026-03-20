package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aiterm/internal/controller"
)

type OpenRouterAgent struct {
	ResponsesAgent
}

type openRouterProviderPreferences struct {
	RequireParameters bool `json:"require_parameters,omitempty"`
}

type openRouterResponsesRequest struct {
	Model           string                         `json:"model"`
	Input           []responsesInputMessage        `json:"input"`
	Text            *responsesTextConfig           `json:"text,omitempty"`
	MaxOutputTokens int                            `json:"max_output_tokens,omitempty"`
	Reasoning       *responsesReasoning            `json:"reasoning,omitempty"`
	Provider        *openRouterProviderPreferences `json:"provider,omitempty"`
}

func NewOpenRouterAgent(profile Profile, client *http.Client) (*OpenRouterAgent, error) {
	if profile.BackendFamily != BackendOpenRouter {
		return nil, fmt.Errorf("profile %q is not an OpenRouter backend", profile.Preset)
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
	if profile.SelectedModel == nil {
		if selectedModel, err := lookupOpenRouterModel(profile, client); err == nil {
			profile.SelectedModel = selectedModel
		}
	}

	return &OpenRouterAgent{
		ResponsesAgent: ResponsesAgent{
			profile: profile,
			client:  client,
		},
	}, nil
}

func (a *OpenRouterAgent) Respond(ctx context.Context, input controller.AgentInput) (controller.AgentResponse, error) {
	requestBody, err := a.newRequest(input)
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

	resp, err := a.client.Do(req)
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("request provider: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return controller.AgentResponse{}, fmt.Errorf("read provider response: %w", err)
	}
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

func (a *OpenRouterAgent) CheckHealth(ctx context.Context) error {
	_, err := a.Respond(ctx, controller.AgentInput{
		Prompt: "Respond with a short confirmation that the provider path works.",
	})
	return err
}

func (a *OpenRouterAgent) newRequest(input controller.AgentInput) (openRouterResponsesRequest, error) {
	policy := openRouterPolicyForProfile(a.profile)

	var baseRequest responsesRequest
	var err error
	if policy.UseStructuredOutputs {
		baseRequest, err = newStructuredResponsesRequest(a.profile.Model, input)
	} else {
		baseRequest, err = newPromptOnlyResponsesRequest(a.profile.Model, input)
	}
	if err != nil {
		return openRouterResponsesRequest{}, err
	}
	if policy.MaxOutputTokens > 0 {
		baseRequest.MaxOutputTokens = policy.MaxOutputTokens
	}
	if policy.Reasoning != nil {
		baseRequest.Reasoning = policy.Reasoning
	}

	return openRouterResponsesRequest{
		Model:           baseRequest.Model,
		Input:           baseRequest.Input,
		Text:            baseRequest.Text,
		MaxOutputTokens: baseRequest.MaxOutputTokens,
		Reasoning:       baseRequest.Reasoning,
		Provider: &openRouterProviderPreferences{
			RequireParameters: policy.RequireParameters,
		},
	}, nil
}

type openRouterRequestPolicy struct {
	UseStructuredOutputs bool
	RequireParameters    bool
	MaxOutputTokens      int
	Reasoning            *responsesReasoning
}

func openRouterPolicyForProfile(profile Profile) openRouterRequestPolicy {
	policy := openRouterRequestPolicy{
		UseStructuredOutputs: true,
		RequireParameters:    true,
		MaxOutputTokens:      1200,
		Reasoning: &responsesReasoning{
			MaxTokens: 64,
			Exclude:   true,
		},
	}

	selectedModel := profile.SelectedModel
	if strings.EqualFold(strings.TrimSpace(profile.Model), "openrouter/auto") {
		policy.Reasoning = nil
	}
	if selectedModel == nil {
		return policy
	}

	if strings.EqualFold(selectedModel.ID, "openrouter/auto") || strings.EqualFold(selectedModel.CanonicalSlug, "openrouter/auto") {
		policy.Reasoning = nil
	}
	if !selectedModel.SupportsAnyParameter("structured_outputs", "response_format") {
		policy.UseStructuredOutputs = false
	}
	if !selectedModel.SupportsAnyParameter("reasoning", "include_reasoning", "reasoning_effort") {
		policy.Reasoning = nil
	}
	if !selectedModel.SupportsAnyParameter("max_tokens") {
		policy.MaxOutputTokens = 0
	}
	if selectedModel.TopProvider.MaxCompletionTokens > 0 && (policy.MaxOutputTokens == 0 || selectedModel.TopProvider.MaxCompletionTokens < policy.MaxOutputTokens) {
		policy.MaxOutputTokens = selectedModel.TopProvider.MaxCompletionTokens
	}

	return policy
}

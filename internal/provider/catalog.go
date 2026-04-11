package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type ModelArchitecture struct {
	Modality         string
	InputModalities  []string
	OutputModalities []string
	Tokenizer        string
	InstructType     string
}

type ModelTopProvider struct {
	ContextWindow       int
	MaxCompletionTokens int
	Moderated           bool
}

type ModelOption struct {
	ID                  string
	CanonicalSlug       string
	Name                string
	Description         string
	ContextWindow       int
	MaxCompletionTokens int
	PromptPrice         string
	CompletionPrice     string
	Architecture        ModelArchitecture
	SupportedParameters []string
	DefaultParameters   map[string]any
	TopProvider         ModelTopProvider
}

func (m ModelOption) SupportsAnyParameter(names ...string) bool {
	if len(m.SupportedParameters) == 0 {
		return false
	}

	supported := make(map[string]struct{}, len(m.SupportedParameters))
	for _, value := range m.SupportedParameters {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		supported[value] = struct{}{}
	}

	for _, name := range names {
		if _, ok := supported[strings.ToLower(strings.TrimSpace(name))]; ok {
			return true
		}
	}

	return false
}

func ListModels(profile Profile, client *http.Client) ([]ModelOption, error) {
	switch profile.BackendFamily {
	case BackendCLIAgent:
		return listCodexCLIModels(profile, client)
	case BackendAnthropic:
		return listAnthropicModels(profile, client)
	case BackendOllama:
		return listOllamaModels(profile, client)
	case BackendResponsesHTTP:
		return listResponsesModels(profile, client)
	case BackendOpenRouter:
		return listOpenRouterModels(profile, client)
	default:
		return nil, fmt.Errorf("profile %q does not support model enumeration", profile.Preset)
	}
}

func lookupOpenRouterModel(profile Profile, client *http.Client) (*ModelOption, error) {
	models, err := ListModels(profile, client)
	if err != nil {
		return nil, err
	}

	target := strings.TrimSpace(profile.Model)
	for index := range models {
		if models[index].ID == target || models[index].CanonicalSlug == target {
			selected := models[index]
			return &selected, nil
		}
	}

	return nil, fmt.Errorf("model %q not found in OpenRouter catalog", target)
}

func listCodexCLIModels(profile Profile, client *http.Client) ([]ModelOption, error) {
	models, err := listOpenAICodexSuggestions(profile, client)
	if err != nil || len(models) == 0 {
		models = curatedCodexCLIModels()
	}
	models = appendCurrentCodexModel(models, profile)
	sortModelOptions(models)
	return models, nil
}

func curatedCodexCLIModels() []ModelOption {
	return []ModelOption{
		{
			ID:          "gpt-5.2-codex",
			Name:        "GPT-5.2 Codex",
			Description: "Curated Codex suggestion. Codex CLI's own picker may differ; manual entry is still allowed.",
		},
		{
			ID:          "gpt-5.1-codex-max",
			Name:        "GPT-5.1 Codex Max",
			Description: "Curated Codex suggestion. Codex CLI's own picker may differ; manual entry is still allowed.",
		},
		{
			ID:          "gpt-5.1-codex",
			Name:        "GPT-5.1 Codex",
			Description: "Curated Codex suggestion. Codex CLI's own picker may differ; manual entry is still allowed.",
		},
		{
			ID:          "gpt-5-codex",
			Name:        "GPT-5 Codex",
			Description: "Curated Codex suggestion. Codex CLI's own picker may differ; manual entry is still allowed.",
		},
	}
}

func appendCurrentCodexModel(models []ModelOption, profile Profile) []ModelOption {
	if selected := strings.TrimSpace(profile.Model); selected != "" && !containsExactModelID(models, selected) {
		models = append(models, ModelOption{
			ID:          selected,
			Name:        selected,
			Description: "Currently selected Codex CLI model. Codex CLI's own picker may differ; manual entry is still allowed.",
		})
	}
	return models
}

func containsExactModelID(models []ModelOption, target string) bool {
	target = strings.TrimSpace(target)
	for _, model := range models {
		if strings.TrimSpace(model.ID) == target {
			return true
		}
	}
	return false
}

func listOpenAICodexSuggestions(profile Profile, client *http.Client) ([]ModelOption, error) {
	apiKey := strings.TrimSpace(profile.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if apiKey == "" {
		return nil, nil
	}

	openAIDescriptor := DescriptorForPreset(PresetOpenAI)
	openAIProfile := Profile{
		BackendFamily: openAIDescriptor.BackendFamily,
		Preset:        PresetOpenAI,
		AuthMethod:    AuthAPIKey,
		BaseURL:       firstNonEmpty(strings.TrimSpace(profile.BaseURL), openAIDescriptor.DefaultBaseURL),
		APIKey:        apiKey,
	}
	models, err := listResponsesModels(openAIProfile, client)
	if err != nil {
		return nil, err
	}

	suggestions := make([]ModelOption, 0, len(models))
	for _, model := range models {
		if !isSuggestedCodexModelID(model.ID) {
			continue
		}
		detailSuffix := strings.TrimSpace(SuggestedModelDetailSuffix(PresetCodexCLI))
		if detailSuffix == "" {
			detailSuffix = "Codex CLI's own picker may differ; manual entry is still allowed."
		}
		suggestion := "Suggested from the OpenAI model catalog. " + detailSuffix
		if strings.TrimSpace(model.Description) == "" {
			model.Description = suggestion
		} else {
			model.Description = strings.TrimSpace(model.Description) + " " + suggestion
		}
		suggestions = append(suggestions, model)
	}
	return suggestions, nil
}

func isSuggestedCodexModelID(id string) bool {
	value := strings.ToLower(strings.TrimSpace(id))
	if value == "" {
		return false
	}
	return strings.Contains(value, "codex") || strings.HasPrefix(value, "gpt-5")
}

func listOllamaModels(profile Profile, client *http.Client) ([]ModelOption, error) {
	endpoint, err := ollamaTagsEndpoint(profile.BaseURL)
	if err != nil {
		return nil, err
	}

	body, err := executeModelsRequest(profile, client, endpoint)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Models []struct {
			Name    string `json:"name"`
			Model   string `json:"model"`
			Details struct {
				Family        string `json:"family"`
				ParameterSize string `json:"parameter_size"`
				Quantization  string `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode ollama tags response: %w", err)
	}

	models := make([]ModelOption, 0, len(payload.Models))
	for _, item := range payload.Models {
		descriptionParts := []string{}
		if value := strings.TrimSpace(item.Details.Family); value != "" {
			descriptionParts = append(descriptionParts, value)
		}
		if value := strings.TrimSpace(item.Details.ParameterSize); value != "" {
			descriptionParts = append(descriptionParts, value)
		}
		if value := strings.TrimSpace(item.Details.Quantization); value != "" {
			descriptionParts = append(descriptionParts, value)
		}

		id := strings.TrimSpace(item.Name)
		if id == "" {
			id = strings.TrimSpace(item.Model)
		}
		models = append(models, ModelOption{
			ID:          id,
			Name:        strings.TrimSpace(item.Model),
			Description: strings.Join(descriptionParts, " "),
		})
	}

	sortModelOptions(models)
	return models, nil
}

func listAnthropicModels(profile Profile, client *http.Client) ([]ModelOption, error) {
	endpoint, err := anthropicModelsEndpoint(profile.BaseURL)
	if err != nil {
		return nil, err
	}

	body, err := executeModelsRequest(profile, client, endpoint)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			Type        string `json:"type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode anthropic models response: %w", err)
	}

	models := make([]ModelOption, 0, len(payload.Data))
	for _, item := range payload.Data {
		models = append(models, ModelOption{
			ID:          strings.TrimSpace(item.ID),
			Name:        strings.TrimSpace(item.DisplayName),
			Description: strings.TrimSpace(item.Type),
		})
	}

	sortModelOptions(models)
	return models, nil
}

func listResponsesModels(profile Profile, client *http.Client) ([]ModelOption, error) {
	endpoint, err := modelsEndpoint(profile.BaseURL)
	if err != nil {
		return nil, err
	}

	body, err := executeModelsRequest(profile, client, endpoint)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Description   string `json:"description"`
			ContextLength int    `json:"context_length"`
			Pricing       struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}

	models := make([]ModelOption, 0, len(payload.Data))
	for _, item := range payload.Data {
		models = append(models, ModelOption{
			ID:              strings.TrimSpace(item.ID),
			Name:            strings.TrimSpace(item.Name),
			Description:     strings.TrimSpace(item.Description),
			ContextWindow:   item.ContextLength,
			PromptPrice:     strings.TrimSpace(item.Pricing.Prompt),
			CompletionPrice: strings.TrimSpace(item.Pricing.Completion),
		})
	}

	sortModelOptions(models)
	return models, nil
}

func listOpenRouterModels(profile Profile, client *http.Client) ([]ModelOption, error) {
	endpoint, err := openRouterModelsEndpoint(profile.BaseURL)
	if err != nil {
		return nil, err
	}

	body, err := executeModelsRequest(profile, client, endpoint)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Data []struct {
			ID              string         `json:"id"`
			CanonicalSlug   string         `json:"canonical_slug"`
			Name            string         `json:"name"`
			Description     string         `json:"description"`
			ContextLength   int            `json:"context_length"`
			SupportedParams []string       `json:"supported_parameters"`
			DefaultParams   map[string]any `json:"default_parameters"`
			Architecture    struct {
				Modality         string   `json:"modality"`
				InputModalities  []string `json:"input_modalities"`
				OutputModalities []string `json:"output_modalities"`
				Tokenizer        string   `json:"tokenizer"`
				InstructType     string   `json:"instruct_type"`
			} `json:"architecture"`
			TopProvider struct {
				ContextLength       int  `json:"context_length"`
				MaxCompletionTokens int  `json:"max_completion_tokens"`
				IsModerated         bool `json:"is_moderated"`
			} `json:"top_provider"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}

	models := make([]ModelOption, 0, len(payload.Data))
	for _, item := range payload.Data {
		models = append(models, ModelOption{
			ID:                  strings.TrimSpace(item.ID),
			CanonicalSlug:       strings.TrimSpace(item.CanonicalSlug),
			Name:                strings.TrimSpace(item.Name),
			Description:         strings.TrimSpace(item.Description),
			ContextWindow:       item.ContextLength,
			MaxCompletionTokens: item.TopProvider.MaxCompletionTokens,
			PromptPrice:         strings.TrimSpace(item.Pricing.Prompt),
			CompletionPrice:     strings.TrimSpace(item.Pricing.Completion),
			Architecture: ModelArchitecture{
				Modality:         strings.TrimSpace(item.Architecture.Modality),
				InputModalities:  append([]string(nil), item.Architecture.InputModalities...),
				OutputModalities: append([]string(nil), item.Architecture.OutputModalities...),
				Tokenizer:        strings.TrimSpace(item.Architecture.Tokenizer),
				InstructType:     strings.TrimSpace(item.Architecture.InstructType),
			},
			SupportedParameters: append([]string(nil), item.SupportedParams...),
			DefaultParameters:   cloneModelDefaults(item.DefaultParams),
			TopProvider: ModelTopProvider{
				ContextWindow:       item.TopProvider.ContextLength,
				MaxCompletionTokens: item.TopProvider.MaxCompletionTokens,
				Moderated:           item.TopProvider.IsModerated,
			},
		})
	}

	sortModelOptions(models)
	return models, nil
}

func executeModelsRequest(profile Profile, client *http.Client, endpoint string) ([]byte, error) {
	if strings.TrimSpace(profile.BaseURL) == "" {
		return nil, fmt.Errorf("profile %q base URL must not be empty", profile.Preset)
	}
	if profile.AuthMethod == AuthAPIKey && strings.TrimSpace(profile.APIKey) == "" {
		if profile.APIKeyEnvVar != "" {
			return nil, fmt.Errorf("%w: set %s or SHUTTLE_API_KEY", ErrMissingAPIKey, profile.APIKeyEnvVar)
		}

		return nil, ErrMissingAPIKey
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build models request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", shuttleUserAgent)
	applyProviderAuthHeaders(req, profile)
	for key, value := range providerRequestHeaders(profile) {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request models: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read models response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, parseProviderError(resp.StatusCode, body)
	}

	return body, nil
}

func applyProviderAuthHeaders(req *http.Request, profile Profile) {
	if req == nil || profile.AuthMethod != AuthAPIKey || strings.TrimSpace(profile.APIKey) == "" {
		return
	}

	switch profile.BackendFamily {
	case BackendAnthropic:
		req.Header.Set("x-api-key", profile.APIKey)
		req.Header.Set("anthropic-version", anthropicVersionHeaderValue)
	default:
		req.Header.Set("Authorization", "Bearer "+profile.APIKey)
	}
}

func modelsEndpoint(baseURL string) (string, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return "", fmt.Errorf("provider base URL must not be empty")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse provider base URL: %w", err)
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/models"
	return parsed.String(), nil
}

func openRouterModelsEndpoint(baseURL string) (string, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return "", fmt.Errorf("provider base URL must not be empty")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse provider base URL: %w", err)
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/models/user"
	return parsed.String(), nil
}

func ollamaTagsEndpoint(baseURL string) (string, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return "", fmt.Errorf("provider base URL must not be empty")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse provider base URL: %w", err)
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/tags"
	return parsed.String(), nil
}

func cloneModelDefaults(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}

	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}

	return cloned
}

func sortModelOptions(models []ModelOption) {
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})
}

package provider

import (
	"encoding/json"
	"fmt"

	"aiterm/internal/controller"
)

func BuildStructuredPrompt(input controller.AgentInput) (string, error) {
	schemaJSON, err := json.Marshal(shuttleAgentResponseSchema())
	if err != nil {
		return "", fmt.Errorf("marshal shuttle schema: %w", err)
	}
	return shuttleSystemPrompt + "\n\nReturn only a valid JSON object that matches this schema exactly:\n" + string(schemaJSON) + "\n\nShuttle turn context:\n" + buildTurnContext(input), nil
}

func ParseStructuredResponseText(text string) (controller.AgentResponse, error) {
	return ParseStructuredResponseTextWithIDFactory(text, func(prefix string) string {
		return prefix + "-pi"
	})
}

func ParseStructuredResponseTextWithIDFactory(text string, idFactory func(prefix string) string) (controller.AgentResponse, error) {
	var structured shuttleStructuredResponse
	if err := json.Unmarshal([]byte(text), &structured); err != nil {
		return controller.AgentResponse{}, fmt.Errorf("decode structured provider output: %w", err)
	}
	return structuredToAgentResponse(structured, idFactory)
}

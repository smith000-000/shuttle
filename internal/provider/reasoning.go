package provider

import "strings"

type ThinkingMode string

type ReasoningEffort string

const (
	ThinkingOff ThinkingMode = "off"
	ThinkingOn  ThinkingMode = "on"
)

const (
	ReasoningEffortLow    ReasoningEffort = "low"
	ReasoningEffortMedium ReasoningEffort = "medium"
	ReasoningEffortHigh   ReasoningEffort = "high"
	ReasoningEffortXHigh  ReasoningEffort = "xhigh"
)

func SupportsThinking(profile Profile) bool {
	return DescriptorForPreset(profile.Preset).SupportsThinking
}

func SupportsReasoningEffort(profile Profile) bool {
	return DescriptorForPreset(profile.Preset).SupportsReasoningEffort
}

func DefaultThinkingMode(profile Profile) ThinkingMode {
	return DescriptorForPreset(profile.Preset).DefaultThinking
}

func NormalizeThinkingMode(value string, profile Profile) ThinkingMode {
	if !SupportsThinking(profile) {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "yes":
		return ThinkingOn
	case "off", "false", "no":
		return ThinkingOff
	default:
		return DefaultThinkingMode(profile)
	}
}

func ThinkingEnabled(profile Profile) bool {
	return NormalizeThinkingMode(profile.Thinking, profile) == ThinkingOn
}

func NormalizeReasoningEffort(value string) ReasoningEffort {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(ReasoningEffortLow):
		return ReasoningEffortLow
	case string(ReasoningEffortHigh):
		return ReasoningEffortHigh
	case string(ReasoningEffortXHigh):
		return ReasoningEffortXHigh
	default:
		return ReasoningEffortMedium
	}
}

func EffectiveReasoningEffort(profile Profile) ReasoningEffort {
	if !SupportsReasoningEffort(profile) {
		return ""
	}
	return NormalizeReasoningEffort(profile.ReasoningEffort)
}

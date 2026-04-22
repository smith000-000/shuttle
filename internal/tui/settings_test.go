package tui

import (
	"aiterm/internal/controller"
	"aiterm/internal/provider"
	"aiterm/internal/shell"
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
)

func settingsFieldIndex(t *testing.T, form *onboardingFormState, key string) int {
	t.Helper()
	if form == nil {
		t.Fatal("expected settings form")
	}
	for index, field := range form.fields {
		if field.key == key {
			return index
		}
	}
	t.Fatalf("expected field %q", key)
	return -1
}

func TestSlashProviderOpensConfigureProvidersSettings(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.mode = AgentMode
	model.input = "/provider"
	model = model.WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5", BaseURL: "https://api.openai.com/v1", APIKeyEnvVar: "OPENAI_API_KEY"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{
				{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5", BaseURL: "https://api.openai.com/v1"}},
				{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter", Model: "openai/gpt-5", BaseURL: "https://openrouter.ai/api/v1"}},
			}, nil
		},
		func(provider.Profile) ([]provider.ModelOption, error) { return nil, nil },
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)

	updated, cmd := model.submit()
	next := updated.(Model)
	if cmd != nil {
		t.Fatal("expected /provider to open synchronously")
	}
	if !next.settingsOpen || next.settingsStep != settingsStepProviders {
		t.Fatalf("expected configure providers list, got open=%t step=%q", next.settingsOpen, next.settingsStep)
	}
}

func TestSettingsMenuOpensShellSettings(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithShellSettings(shell.DefaultLaunchProfiles(), func(shell.LaunchProfiles) error {
		return nil
	})
	model.settingsOpen = true
	model.settingsStep = settingsStepMenu
	model.settingsIndex = 2

	updated, cmd := model.applySettingsSelection()
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("expected shell settings to open synchronously")
	}
	if model.settingsStep != settingsStepShell {
		t.Fatalf("expected shell settings step, got %q", model.settingsStep)
	}
	if model.settingsConfig == nil {
		t.Fatal("expected shell settings form")
	}
}

func TestShellSettingsSavePersistsProfiles(t *testing.T) {
	var saved shell.LaunchProfiles
	model := NewModel(fakeWorkspace(), &fakeController{}).WithShellSettings(shell.DefaultLaunchProfiles(), func(profiles shell.LaunchProfiles) error {
		saved = profiles
		return nil
	})
	model = model.openSettingsShellStep()
	model.settingsOpen = true
	model.settingsConfig.fields[settingsFieldIndex(t, model.settingsConfig, "persistent_source_rc")].value = shellSettingsNo
	model.settingsConfig.fields[settingsFieldIndex(t, model.settingsConfig, "execution_mode")].value = string(shell.LaunchModeInherit)

	updated, cmd := model.applySettingsSelection()
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected shell settings save command")
	}
	msg := cmd().(shellSettingsSavedMsg)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	if saved.Persistent.SourceUserRC {
		t.Fatalf("expected persistent source rc to be disabled, got %#v", saved.Persistent)
	}
	if saved.Execution.Mode != shell.LaunchModeInherit {
		t.Fatalf("expected execution inherit mode, got %#v", saved.Execution)
	}
	if model.settingsStep != settingsStepShell {
		t.Fatalf("expected shell settings to stay open, got %q", model.settingsStep)
	}
	if !strings.Contains(model.settingsBanner, "Saved shell settings") {
		t.Fatalf("expected save banner, got %q", model.settingsBanner)
	}
}

func TestSlashModelOpensCurrentProviderDetailAndLoadsModels(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.mode = AgentMode
	model.input = "/model"
	model = model.WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5", BaseURL: "https://api.openai.com/v1", APIKeyEnvVar: "OPENAI_API_KEY"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{
				{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5", BaseURL: "https://api.openai.com/v1", APIKeyEnvVar: "OPENAI_API_KEY"}},
				{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter", Model: "openai/gpt-5", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}},
			}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			switch profile.Preset {
			case provider.PresetOpenAI:
				return []provider.ModelOption{{ID: "gpt-5"}, {ID: "gpt-5-mini"}}, nil
			case provider.PresetOpenRouter:
				return []provider.ModelOption{{ID: "openai/gpt-5"}}, nil
			default:
				return nil, nil
			}
		},
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)

	updated, cmd := model.submit()
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected /model to open provider detail and load models")
	}
	if !next.settingsOpen || next.settingsStep != settingsStepProviderForm {
		t.Fatalf("expected provider detail from /model, got open=%t step=%q", next.settingsOpen, next.settingsStep)
	}
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = next.Update(loaded)
	next = updated.(Model)
	if len(next.settingsModels) != 2 {
		t.Fatalf("expected only active-provider models, got %d", len(next.settingsModels))
	}
	for _, choice := range next.settingsModels {
		if choice.profile.Preset != provider.PresetOpenAI {
			t.Fatalf("expected active provider model scope, got %#v", choice)
		}
	}
	if next.settingsConfig == nil || next.settingsCurrentFieldKey() != "model" {
		t.Fatalf("expected /model to focus the model field")
	}
}

func TestUnknownSlashCommandRendersNotice(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.mode = AgentMode
	model.input = "/wat"

	updated, cmd := model.submit()
	next := updated.(Model)
	if cmd != nil {
		t.Fatal("expected unknown slash command to stay in TUI")
	}
	last := next.entries[len(next.entries)-1]
	if last.Title != "system" || !strings.Contains(last.Body, "Unknown slash command: /wat") {
		t.Fatalf("expected unknown slash command notice, got %#v", last)
	}
	if !strings.Contains(last.Body, "/help") || !strings.Contains(last.Body, "/approvals") || !strings.Contains(last.Body, "/new") || !strings.Contains(last.Body, "/compact") {
		t.Fatalf("expected updated slash command hint, got %#v", last)
	}
}

func TestSlashQuitReturnsQuitCmd(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.mode = AgentMode
	model.input = "/quit"

	_, cmd := model.submit()
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("expected /quit to return tea.Quit")
	}
}

func TestSlashNewStartsFreshTaskContext(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "/new"
	model.entries = append(model.entries, Entry{Title: "user", Body: "stale transcript entry"})
	model.pendingApproval = &controller.ApprovalRequest{ID: "approval-1", Title: "pending"}
	model.activePlan = &controller.ActivePlan{
		Summary: "Finish the old task",
		Steps:   []controller.PlanStep{{Text: "Do the thing", Status: controller.PlanStepInProgress}},
	}

	updated, cmd := model.submit()
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected /new to call the controller")
	}
	updated, _ = model.Update(controllerEventsFromCmd(t, cmd))
	model = updated.(Model)

	if ctrl.newTaskCalls != 1 {
		t.Fatalf("expected one /new controller call, got %d", ctrl.newTaskCalls)
	}
	if len(model.entries) != 2 {
		t.Fatalf("expected transcript reset plus success notice, got %#v", model.entries)
	}
	if strings.Contains(model.View(), "stale transcript entry") {
		t.Fatalf("expected old transcript to be cleared, got %q", model.View())
	}
	if model.pendingApproval != nil || model.activePlan != nil {
		t.Fatalf("expected task-local UI state to clear, got approval=%#v plan=%#v", model.pendingApproval, model.activePlan)
	}
}

func TestSlashCompactCallsController(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "/compact"

	updated, cmd := model.submit()
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected /compact to call the controller")
	}
	updated, _ = model.Update(controllerEventsFromCmd(t, cmd))
	model = updated.(Model)

	if ctrl.compactTaskCalls != 1 {
		t.Fatalf("expected one /compact controller call, got %d", ctrl.compactTaskCalls)
	}
	last := model.entries[len(model.entries)-1]
	if last.Title != "system" || !strings.Contains(last.Body, "Compacted task context") {
		t.Fatalf("expected compaction notice, got %#v", last)
	}
}

func TestSlashApprovalsShowsCurrentMode(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "/approvals"

	updated, cmd := model.submit()
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("expected bare /approvals to stay in the TUI")
	}
	last := model.entries[len(model.entries)-1]
	if last.Title != "system" || !strings.Contains(last.Body, "Approvals: confirm") {
		t.Fatalf("expected approval mode status notice, got %#v", last)
	}
}

func TestSlashApprovalsAutoCallsController(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "/approvals auto"

	updated, cmd := model.submit()
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected /approvals auto to call the controller")
	}
	updated, _ = model.Update(controllerEventsFromCmd(t, cmd))
	model = updated.(Model)

	if ctrl.approvalMode != controller.ApprovalModeAuto {
		t.Fatalf("expected approval mode auto, got %q", ctrl.approvalMode)
	}
	last := model.entries[len(model.entries)-1]
	if last.Title != "system" || !strings.Contains(last.Body, "Approvals set to auto") {
		t.Fatalf("expected approvals-auto notice, got %#v", last)
	}
}

func TestSlashApprovalsDangerousRequiresConfirmation(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "/approvals dangerous"

	updated, cmd := model.submit()
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("expected dangerous mode to wait for confirmation")
	}
	if model.pendingDangerousConfirm == nil {
		t.Fatal("expected pending dangerous confirmation")
	}
	if !strings.Contains(model.View(), "Enable Dangerous Mode") || !strings.Contains(model.View(), "trusted workspace") {
		t.Fatalf("expected dangerous warning in action card, got %q", model.View())
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected dangerous confirmation to call the controller")
	}
	updated, _ = model.Update(controllerEventsFromCmd(t, cmd))
	model = updated.(Model)
	if ctrl.approvalMode != controller.ApprovalModeDanger {
		t.Fatalf("expected dangerous approval mode, got %q", ctrl.approvalMode)
	}
}

func TestConfigureProvidersSelectionSwitchesController(t *testing.T) {
	initialCtrl := &fakeController{}
	switchedCtrl := &fakeController{}
	model := NewModel(fakeWorkspace(), initialCtrl).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1", APIKeyEnvVar: "OPENAI_API_KEY"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openai/gpt-5", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}, AuthSource: "OPENROUTER_API_KEY"}}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			return []provider.ModelOption{{ID: "openrouter/auto"}, {ID: "openai/gpt-5-mini"}}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return switchedCtrl, profile, nil
		},
		func(provider.Profile) error { return nil },
	)

	updated, cmd := model.openOnboarding()
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("expected configure providers to open synchronously")
	}
	if model.settingsStep != settingsStepProviders {
		t.Fatalf("expected provider list, got %q", model.settingsStep)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected provider detail model load")
	}
	loaded, ok := cmd().(settingsModelsLoadedMsg)
	if !ok {
		t.Fatalf("expected settingsModelsLoadedMsg")
	}
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	model.settingsConfig.fields[1].value = "openai/gpt-5-mini"
	model.syncSettingsModelFilterFromConfig()
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyF8})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected save+activate command")
	}
	savedMsg, ok := cmd().(settingsProviderSavedMsg)
	if !ok {
		t.Fatalf("expected settingsProviderSavedMsg")
	}
	updated, _ = model.Update(savedMsg)
	model = updated.(Model)
	if model.ctrl != switchedCtrl {
		t.Fatal("expected controller to be replaced after provider switch")
	}
	if model.activeProvider.Preset != provider.PresetOpenRouter || model.activeProvider.Model != "openai/gpt-5-mini" {
		t.Fatalf("expected selected provider/model, got %#v", model.activeProvider)
	}
	if !model.settingsOpen || model.settingsStep != settingsStepProviders {
		t.Fatalf("expected settings to remain on provider list, got open=%t step=%q", model.settingsOpen, model.settingsStep)
	}
}

func TestOpenOnboardingUsesCollapsedProviderSettingsList(t *testing.T) {
	active := provider.Profile{Preset: provider.PresetCodexCLI, Name: "Codex CLI", Model: "gpt-5.3-codex", AuthMethod: provider.AuthCodexLogin}
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		active,
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{
				{Profile: provider.Profile{Preset: provider.PresetCodexCLI, Name: "Codex CLI", Model: "gpt-5-nano-2025-08-07", AuthMethod: provider.AuthCodexLogin}, AuthSource: "codex login", Source: provider.OnboardingCandidateDetected},
				{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1", APIKeyEnvVar: "OPENAI_API_KEY"}, AuthSource: "OPENAI_API_KEY", Source: provider.OnboardingCandidateDetected},
				{Profile: active, AuthSource: "codex login", Source: provider.OnboardingCandidateStored},
				{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5.3-codex", BaseURL: "https://api.openai.com/v1", APIKeyEnvVar: "os_keyring"}, AuthSource: "os_keyring", Source: provider.OnboardingCandidateStored},
				{Profile: provider.Profile{Preset: provider.PresetCodexCLI, Name: "Codex CLI", AuthMethod: provider.AuthCodexLogin}, Manual: true, Source: provider.OnboardingCandidateManual},
			}, nil
		},
		nil,
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)

	updated, cmd := model.openOnboarding()
	next := updated.(Model)
	if cmd != nil {
		t.Fatal("expected provider settings open to be synchronous")
	}
	if next.settingsStep != settingsStepProviders {
		t.Fatalf("expected provider settings list, got %q", next.settingsStep)
	}
	if len(next.settingsProviders) != 2 {
		t.Fatalf("expected one entry per preset, got %#v", next.settingsProviders)
	}
	if next.settingsProviders[0].candidate == nil || next.settingsProviders[0].candidate.Profile.Preset != provider.PresetCodexCLI || next.settingsProviders[0].candidate.Profile.Model != "gpt-5.3-codex" {
		t.Fatalf("expected exact current codex profile to win collapsed entry, got %#v", next.settingsProviders[0])
	}
	if next.settingsProviderIdx != 0 {
		t.Fatalf("expected current provider selection to point at collapsed current profile, got %d", next.settingsProviderIdx)
	}
	view := next.renderSettingsProviders(120)
	joined := strings.Join(view, "\n")
	if strings.Count(joined, "Codex CLI") != 1 {
		t.Fatalf("expected deduped Codex entry, got %q", joined)
	}
	if !strings.Contains(joined, "Codex CLI (current)") {
		t.Fatalf("expected exact current label, got %q", joined)
	}
}

func TestManualProviderSettingsCollectConfigAndPersist(t *testing.T) {
	switchedCtrl := &fakeController{}
	var savedProfile provider.Profile
	var savedCount int
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetMock, Name: "Mock Provider"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openai/gpt-5", BaseURL: "https://openrouter.ai/api/v1"}, Reason: "Manual setup.", Manual: true}}, nil
		},
		nil,
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return switchedCtrl, profile, nil
		},
		func(profile provider.Profile) error {
			savedProfile = profile
			savedCount++
			return nil
		},
	)

	updated, cmd := model.openOnboarding()
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("expected provider settings open to be synchronous")
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("expected manual provider detail to open synchronously")
	}
	apiKeyIndex := settingsFieldIndex(t, model.settingsConfig, "api_key")
	model.settingsConfig.index = apiKeyIndex
	model.settingsConfig.fields[apiKeyIndex].value = "router-secret"
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyF8})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected save+activate command")
	}
	savedMsg, ok := cmd().(settingsProviderSavedMsg)
	if !ok {
		t.Fatalf("expected settingsProviderSavedMsg")
	}
	updated, _ = model.Update(savedMsg)
	model = updated.(Model)
	if model.ctrl != switchedCtrl {
		t.Fatal("expected controller to switch")
	}
	if savedCount != 1 || savedProfile.APIKey != "router-secret" || savedProfile.Model != "openai/gpt-5" {
		t.Fatalf("unexpected saved profile %#v count=%d", savedProfile, savedCount)
	}
}

func TestF10OpensSettingsMenuWithSessionRuntimeAndConfigureProvidersEntries(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{
				{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"}, Reason: "Configured."},
				{Profile: provider.Profile{Preset: provider.PresetOpenWebUI, Name: "OpenWebUI", BaseURL: "http://localhost:3000/api/v1"}, Manual: true, Reason: "Manual setup."},
				{Profile: provider.Profile{Preset: provider.PresetAnthropic, Name: "Anthropic Messages", BaseURL: "https://api.anthropic.com"}, Manual: true, Reason: "Manual setup."},
			}, nil
		},
		nil,
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyF10})
	model = updated.(Model)
	if !model.settingsOpen || model.settingsStep != settingsStepMenu || len(model.settingsProviders) < 3 {
		t.Fatalf("unexpected settings state")
	}
	model.width = 140
	model.height = 40
	view := model.View()
	for _, fragment := range []string{"Session Settings", "Runtime", "Shell Settings", "Configure Providers"} {
		if !strings.Contains(view, fragment) {
			t.Fatalf("expected settings menu entry %q in view %q", fragment, view)
		}
	}
	for _, fragment := range []string{"Change Active Provider", "Select Model"} {
		if strings.Contains(view, fragment) {
			t.Fatalf("did not expect legacy settings menu entry %q in view %q", fragment, view)
		}
	}
}

func TestSettingsRuntimeSelectionSwitchesAndPersistsRuntime(t *testing.T) {
	switchedCtrl := &fakeController{}
	var savedType string
	var savedCommand string
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) { return nil, nil },
		nil,
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	).WithRuntimeSettings(provider.RuntimeBuiltin, "/custom/codex", func(runtimeType string, runtimeCommand string, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, string, string, error) {
		return switchedCtrl, runtimeType, runtimeCommand, nil
	}, func(runtimeType string, runtimeCommand string) error {
		savedType = runtimeType
		savedCommand = runtimeCommand
		return nil
	})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyF10})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("expected runtime list to open synchronously")
	}
	if model.settingsStep != settingsStepRuntime {
		t.Fatalf("expected runtime settings step, got %q", model.settingsStep)
	}
	model.settingsRuntimeIdx = 2
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected runtime switch command")
	}
	msg, ok := cmd().(runtimeSwitchedMsg)
	if !ok {
		t.Fatalf("expected runtimeSwitchedMsg, got %T", cmd())
	}
	updated, _ = model.Update(msg)
	model = updated.(Model)
	if model.ctrl != switchedCtrl {
		t.Fatal("expected controller to switch with runtime")
	}
	if model.activeRuntimeType != provider.RuntimeCodexSDK {
		t.Fatalf("expected codex runtime active, got %q", model.activeRuntimeType)
	}
	if savedType != provider.RuntimeCodexSDK || savedCommand != "/custom/codex" {
		t.Fatalf("expected saved runtime values, got type=%q command=%q", savedType, savedCommand)
	}
	if !model.settingsOpen || model.settingsStep != settingsStepRuntime {
		t.Fatalf("expected runtime settings to remain open, got open=%t step=%q", model.settingsOpen, model.settingsStep)
	}
	if !strings.Contains(model.settingsBanner, "Codex CLI Bridge") {
		t.Fatalf("expected runtime banner, got %q", model.settingsBanner)
	}
}

func TestSettingsRuntimeCommandFieldEditsAndApplies(t *testing.T) {
	switchedCtrl := &fakeController{}
	var savedType string
	var savedCommand string
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) { return nil, nil },
		nil,
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	).WithRuntimeSettings(provider.RuntimeAuto, "", func(runtimeType string, runtimeCommand string, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, string, string, error) {
		return switchedCtrl, runtimeType, runtimeCommand, nil
	}, func(runtimeType string, runtimeCommand string) error {
		savedType = runtimeType
		savedCommand = runtimeCommand
		return nil
	})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyF10})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settingsStep != settingsStepRuntime {
		t.Fatalf("expected runtime settings step, got %q", model.settingsStep)
	}
	model.settingsRuntimeIdx = 3
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if !model.settingsRuntimeCommandFocus {
		t.Fatal("expected runtime command field to be focused")
	}
	model.settingsRuntimeCommand = "/opt/codex"
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected runtime apply command")
	}
	msg := cmd().(runtimeSwitchedMsg)
	updated, _ = model.Update(msg)
	model = updated.(Model)
	if model.ctrl != switchedCtrl {
		t.Fatal("expected controller to switch")
	}
	if savedType != provider.RuntimeCodexAppServer || savedCommand != "/opt/codex" {
		t.Fatalf("expected saved runtime command override, got type=%q command=%q", savedType, savedCommand)
	}
	view := model.View()
	if !strings.Contains(view, "Command Path") || !strings.Contains(view, "Preview:") || !strings.Contains(view, "Health:") {
		t.Fatalf("expected runtime command preview in view, got %q", view)
	}
}

func TestSettingsRuntimeSelectionPassesCurrentTrackedShellTarget(t *testing.T) {
	switchedCtrl := &fakeController{}
	var gotTrackedShell controller.TrackedShellTarget
	currentCtrl := &fakeController{sessionName: "shuttle-live", trackedPaneID: "%8"}
	model := NewModel(fakeWorkspace(), currentCtrl).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) { return nil, nil },
		nil,
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	).WithRuntimeSettings(provider.RuntimeBuiltin, "", func(runtimeType string, runtimeCommand string, _ *shell.PromptContext, trackedShell controller.TrackedShellTarget) (controller.Controller, string, string, error) {
		gotTrackedShell = trackedShell
		return switchedCtrl, runtimeType, runtimeCommand, nil
	}, func(string, string) error { return nil })

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyF10})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	model.settingsRuntimeIdx = 2
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected runtime switch command")
	}
	msg := cmd().(runtimeSwitchedMsg)
	if gotTrackedShell.SessionName != "shuttle-live" || gotTrackedShell.PaneID != "%8" {
		t.Fatalf("expected live tracked shell target, got %#v", gotTrackedShell)
	}
	updated, _ = model.Update(msg)
	model = updated.(Model)
	if model.ctrl != switchedCtrl {
		t.Fatal("expected controller to switch")
	}
}

func TestOpenSettingsDefersRuntimeDiscoveryUntilRuntimeStep(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) { return nil, nil },
		nil,
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	).WithRuntimeSettings(provider.RuntimeBuiltin, "", func(runtimeType string, runtimeCommand string, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, string, string, error) {
		return &fakeController{}, runtimeType, runtimeCommand, nil
	}, func(string, string) error { return nil })

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyF10})
	model = updated.(Model)
	if len(model.settingsRuntimeCandidates) != 0 || len(model.settingsRuntimes) != 0 || len(model.settingsRuntimePreview) != 0 {
		t.Fatalf("expected runtime settings to stay unloaded until the runtime step opens, got candidates=%d entries=%d preview=%d", len(model.settingsRuntimeCandidates), len(model.settingsRuntimes), len(model.settingsRuntimePreview))
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("expected runtime settings to open synchronously")
	}
	if model.settingsStep != settingsStepRuntime {
		t.Fatalf("expected runtime step, got %q", model.settingsStep)
	}
	if len(model.settingsRuntimeCandidates) == 0 || len(model.settingsRuntimes) == 0 || len(model.settingsRuntimePreview) == 0 {
		t.Fatalf("expected runtime step to load candidates and preview, got candidates=%d entries=%d preview=%d", len(model.settingsRuntimeCandidates), len(model.settingsRuntimes), len(model.settingsRuntimePreview))
	}
}

func TestSettingsRuntimeCommandEditingDefersHealthValidationUntilBlur(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) { return nil, nil },
		nil,
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	).WithRuntimeSettings(provider.RuntimeBuiltin, "", func(runtimeType string, runtimeCommand string, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, string, string, error) {
		return &fakeController{}, runtimeType, runtimeCommand, nil
	}, func(string, string) error { return nil })

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyF10})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	model.settingsRuntimeIdx = 2
	model.refreshSettingsRuntimePreview(true)

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	model.settingsRuntimeCommand = "/opt/codex"
	model.refreshSettingsRuntimePreview(false)
	view := model.View()
	if !strings.Contains(view, "Health: pending validation while editing the command path.") {
		t.Fatalf("expected pending validation hint while editing, got %q", view)
	}
	if !strings.Contains(view, "Leave the command field or press Enter to validate the current command path.") {
		t.Fatalf("expected blur/enter validation hint, got %q", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(Model)
	view = model.View()
	if strings.Contains(view, "pending validation while editing the command path") {
		t.Fatalf("expected blur to trigger validation, got %q", view)
	}
	if !strings.Contains(view, "Health:") {
		t.Fatalf("expected validated health line after blur, got %q", view)
	}
}

func TestProviderDetailModelSelectionRunsTestFromModelField(t *testing.T) {
	tested := provider.Profile{}
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}}}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			return []provider.ModelOption{{ID: "openrouter/auto"}, {ID: "qwen/qwen3.5-9b"}}, nil
		},
		nil,
		func(provider.Profile) error { return nil },
	).WithProviderTester(func(profile provider.Profile) error {
		tested = profile
		return nil
	})

	updated, cmd := model.openActiveModelSettings()
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected model detail to load provider models")
	}
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	model.settingsConfig.fields[1].value = ""
	model.syncSettingsModelFilterFromConfig()
	model.settingsModelIdx = 1
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected enter on model field to trigger provider test")
	}
	testMsg := cmd().(settingsProviderTestedMsg)
	if testMsg.err != nil {
		t.Fatalf("expected provider test success, got %v", testMsg.err)
	}
	if tested.Model != "qwen/qwen3.5-9b" {
		t.Fatalf("expected highlighted model to be tested, got %#v", tested)
	}
}

func TestSettingsProviderListSwitchesProviderFromDetail(t *testing.T) {
	switchedCtrl := &fakeController{}
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1", APIKeyEnvVar: "OPENAI_API_KEY"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{
				{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1", APIKeyEnvVar: "OPENAI_API_KEY"}},
				{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}},
			}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			if profile.Preset == provider.PresetOpenRouter {
				return []provider.ModelOption{{ID: "openrouter/auto"}}, nil
			}
			return []provider.ModelOption{{ID: profile.Model}}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return switchedCtrl, profile, nil
		},
		func(provider.Profile) error { return nil },
	)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyF10})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settingsStep != settingsStepProviders {
		t.Fatalf("expected provider list, got %q", model.settingsStep)
	}
	model.settingsProviderIdx = 1
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected provider detail to load models")
	}
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	if model.settingsStep != settingsStepProviderForm {
		t.Fatalf("expected provider detail step, got %q", model.settingsStep)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyF8})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected save+activate from provider detail")
	}
	savedMsg := cmd().(settingsProviderSavedMsg)
	updated, _ = model.Update(savedMsg)
	model = updated.(Model)
	if model.ctrl != switchedCtrl || model.activeProvider.Preset != provider.PresetOpenRouter || model.activeProvider.Model != "openrouter/auto" || !model.settingsOpen {
		t.Fatalf("unexpected active provider switch result %#v", model.activeProvider)
	}
}

func TestSettingsProviderListEscReturnsToMenu(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{
				{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"}},
				{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}},
			}, nil
		},
		nil,
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyF10})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settingsStep != settingsStepProviders {
		t.Fatalf("expected provider list step, got %q", model.settingsStep)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.settingsStep != settingsStepMenu {
		t.Fatalf("expected Esc to return to settings menu, got %q", model.settingsStep)
	}
}

func TestSettingsModelListRequiresF5ForKeyboardNavigation(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}}}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			return []provider.ModelOption{{ID: "openrouter/auto"}, {ID: "qwen/qwen3.5-9b"}}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)
	updated, cmd := model.openActiveModelSettings()
	model = updated.(Model)
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	if !model.isSettingsModelFieldFocused() {
		t.Fatal("expected model field focus")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.settingsConfig.index != settingsFieldIndex(t, model.settingsConfig, "thinking") {
		t.Fatalf("expected Down without F5 to move to next field, got index %d", model.settingsConfig.index)
	}
	model.settingsConfig.index = settingsFieldIndex(t, model.settingsConfig, "model")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	if !model.settingsModelListActive {
		t.Fatal("expected F5 to activate model list")
	}
	if !model.settingsModelBrowseAll {
		t.Fatal("expected F5 browse mode to expand beyond the current exact slug")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.settingsModelIdx != 1 {
		t.Fatalf("expected Down in list mode to move highlighted model, got %d", model.settingsModelIdx)
	}
}

func TestSettingsModelListRequiresRightArrowAndEscReturnsToFields(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}}}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			return []provider.ModelOption{{ID: "openrouter/auto"}, {ID: "qwen/qwen3.5-9b"}}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)
	updated, cmd := model.openActiveModelSettings()
	model = updated.(Model)
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	if !model.settingsModelListActive {
		t.Fatal("expected Right Arrow to enter model list")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.settingsModelListActive {
		t.Fatal("expected Esc to leave model list")
	}
	if !model.isSettingsModelFieldFocused() {
		t.Fatal("expected focus to remain on model field after Esc")
	}
}

func TestSettingsModelListStaysFocusedAtCatalogEdges(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}}}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			return []provider.ModelOption{{ID: "openrouter/auto"}, {ID: "qwen/qwen3.5-9b"}}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)
	updated, cmd := model.openActiveModelSettings()
	model = updated.(Model)
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(Model)
	if !model.settingsModelListActive || !model.isSettingsModelFieldFocused() || model.settingsConfig.index != settingsFieldIndex(t, model.settingsConfig, "model") {
		t.Fatalf("expected top-edge Up to keep model list focus, got active=%t index=%d field=%d", model.settingsModelListActive, model.settingsModelIdx, model.settingsConfig.index)
	}
	model.settingsModelIdx = len(model.settingsModels) - 1
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if !model.settingsModelListActive || !model.isSettingsModelFieldFocused() || model.settingsConfig.index != settingsFieldIndex(t, model.settingsConfig, "model") {
		t.Fatalf("expected bottom-edge Down to keep model list focus, got active=%t index=%d field=%d", model.settingsModelListActive, model.settingsModelIdx, model.settingsConfig.index)
	}
}

func TestSettingsModelListEnterReturnsToFieldsAfterSelection(t *testing.T) {
	var tested provider.Profile
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}}}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			return []provider.ModelOption{{ID: "openrouter/auto"}, {ID: "qwen/qwen3.5-9b"}}, nil
		},
		nil,
		func(provider.Profile) error { return nil },
	).WithProviderTester(func(profile provider.Profile) error {
		tested = profile
		return nil
	})
	updated, cmd := model.openActiveModelSettings()
	model = updated.(Model)
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	model.settingsModelIdx = 1
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected Enter in model list to trigger provider test")
	}
	if model.settingsModelListActive {
		t.Fatal("expected Enter to leave model list mode")
	}
	if !model.isSettingsModelFieldFocused() {
		t.Fatal("expected focus to return to model field after Enter")
	}
	msg := cmd().(settingsProviderTestedMsg)
	if msg.err != nil || tested.Model != "qwen/qwen3.5-9b" {
		t.Fatalf("unexpected tested profile %#v err=%v", tested, msg.err)
	}
}

func TestTypingInModelFieldLeavesModelListMode(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}}}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			return []provider.ModelOption{{ID: "openrouter/auto"}, {ID: "qwen/qwen3.5-9b"}}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)
	updated, cmd := model.openActiveModelSettings()
	model = updated.(Model)
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	if !model.settingsModelListActive {
		t.Fatal("expected list mode active")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	model = updated.(Model)
	if model.settingsModelListActive {
		t.Fatal("expected typing to leave list mode")
	}
	if !strings.Contains(model.settingsModelFilter, "q") {
		t.Fatalf("expected typed filter update, got %q", model.settingsModelFilter)
	}
}

func TestSettingsProviderDetailModelFilterNarrowsChoices(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}}}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			return []provider.ModelOption{{ID: "openrouter/auto"}, {ID: "qwen/qwen3.5-9b"}, {ID: "qwen/qwen3.5-32b"}}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)
	updated, cmd := model.openActiveModelSettings()
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected provider detail to load models")
	}
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	model.settingsConfig.fields[1].value = "q35"
	model.syncSettingsModelFilterFromConfig()
	if model.settingsModelFilter != "q35" || len(model.settingsModelCatalog) != 3 || len(model.settingsModels) != 2 || model.settingsModels[0].model.ID != "qwen/qwen3.5-9b" {
		t.Fatalf("unexpected filtered settings state")
	}
	view := model.View()
	if !strings.Contains(view, "Model filter: q35  (2 matches)") || strings.Contains(view, "openrouter/auto") {
		t.Fatalf("unexpected filtered view %q", view)
	}
}

func TestSaveSettingsProfileRejectsUnknownModelWhenCatalogIsAvailable(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
		nil,
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			return []provider.ModelOption{{ID: "gpt-5-nano-2025-08-07"}, {ID: "gpt-5"}}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)
	form := buildOnboardingForm(provider.OnboardingCandidate{
		Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
	})
	form.fields[1].value = "gpt-5-typo"
	form.fields[settingsFieldIndex(t, &form, "api_key")].value = "manual-secret"
	model.settingsConfig = &form
	model.settingsStep = settingsStepProviderForm

	updated, cmd := model.saveSettingsProfile(false)
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected settings save command")
	}
	msg := cmd()
	savedMsg, ok := msg.(settingsProviderSavedMsg)
	if !ok {
		t.Fatalf("expected settingsProviderSavedMsg, got %T", msg)
	}
	if savedMsg.err == nil || !strings.Contains(savedMsg.err.Error(), "is not in the provider model list") {
		t.Fatalf("expected model validation error, got %v", savedMsg.err)
	}
}

func TestSettingsProviderDetailEscReturnsToMenu(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1", APIKeyEnvVar: "OPENAI_API_KEY"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1", APIKeyEnvVar: "OPENAI_API_KEY"}}}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			return []provider.ModelOption{{ID: profile.Model}}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)
	updated, cmd := model.openActiveModelSettings()
	model = updated.(Model)
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	model.settingsConfig.fields[1].value = "nano"
	model.syncSettingsModelFilterFromConfig()
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.settingsStep != settingsStepProviders {
		t.Fatalf("expected esc to return to provider list, got %q", model.settingsStep)
	}
}

func TestSettingsProviderDetailModelInfoToggleShowsSelectedDetailsOnly(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "allenai/olmo-3-7b-think", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "allenai/olmo-3-7b-think", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}}}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			return []provider.ModelOption{
				{ID: "allenai/olmo-3-7b-think", Name: "AllenAI: Olmo 3 7B Think", ContextWindow: 65536, MaxCompletionTokens: 65536, PromptPrice: "0.00000012", CompletionPrice: "0.0000002", SupportedParameters: []string{"reasoning", "structured_outputs"}, Architecture: provider.ModelArchitecture{Modality: "text->text"}, Description: "Long form provider description that should only appear when info is toggled."},
				{ID: "qwen/qwen3.5-9b"},
			}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)
	updated, cmd := model.openActiveModelSettings()
	model = updated.(Model)
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	model.width = 140
	model.height = 40
	view := model.View()
	if !strings.Contains(view, "Current: OpenRouter Responses (openrouter) / allenai/olmo-3-7b-think") {
		t.Fatalf("expected provider label next to model slug, got %q", view)
	}
	if !strings.Contains(view, "AllenAI: Olmo 3 7B Think  context 65536  max out 65536  pricing p=0.00000012 c=0.0000002") || !strings.Contains(view, "mode") || !strings.Contains(view, "text->text") || strings.Contains(view, "Long form provider description") {
		t.Fatalf("unexpected default model info view %q", view)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	model = updated.(Model)
	view = model.View()
	if !model.settingsModelInfo || !strings.Contains(view, "Long form provider description") {
		t.Fatalf("expected expanded model info, got %q", view)
	}
	model.settingsConfig.fields[1].value = ""
	model.syncSettingsModelFilterFromConfig()
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	view = model.View()
	if model.settingsModelInfo || strings.Contains(view, "Long form provider description") {
		t.Fatalf("expected model info to clear on selection change")
	}
}

func TestSaveSettingsProfileRefreshesCurrentProviderEvenWhenPersistenceFails(t *testing.T) {
	switchedCtrl := &fakeController{}
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) { return nil, nil },
		nil,
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return switchedCtrl, profile, nil
		},
		func(provider.Profile) error {
			return provider.ErrSecretStoreUnavailable
		},
	)
	model.activeProvider = provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"}
	form := buildOnboardingForm(provider.OnboardingCandidate{
		Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
	})
	form.fields[settingsFieldIndex(t, &form, "api_key")].value = "manual-secret"
	model.settingsConfig = &form
	model.settingsStep = settingsStepProviderForm

	updated, cmd := model.saveSettingsProfile(false)
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected settings save command")
	}
	msg := cmd()
	savedMsg, ok := msg.(settingsProviderSavedMsg)
	if !ok {
		t.Fatalf("expected settingsProviderSavedMsg, got %T", msg)
	}
	if !errors.Is(savedMsg.persistErr, provider.ErrSecretStoreUnavailable) {
		t.Fatalf("expected secret-store persistence error, got %v", savedMsg.persistErr)
	}
	if savedMsg.ctrl != switchedCtrl {
		t.Fatal("expected current provider to refresh even when persistence fails")
	}
	updated, _ = model.Update(savedMsg)
	model = updated.(Model)
	last := model.entries[len(model.entries)-1]
	if last.Title != "error" || !strings.Contains(last.Body, "active for this session") {
		t.Fatalf("expected session-only persistence warning, got %#v", last)
	}
}

func TestF8SavesAndActivatesProviderFromSettingsForm(t *testing.T) {
	switchedCtrl := &fakeController{}
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) { return nil, nil },
		nil,
		func(profile provider.Profile, _ *shell.PromptContext, _ controller.TrackedShellTarget) (controller.Controller, provider.Profile, error) {
			return switchedCtrl, profile, nil
		},
		func(provider.Profile) error { return nil },
	)
	model.settingsOpen = true
	model.settingsStep = settingsStepProviderForm
	form := buildOnboardingForm(provider.OnboardingCandidate{
		Profile: provider.Profile{
			Preset:       provider.PresetOpenRouter,
			Name:         "OpenRouter Responses",
			Model:        "openai/gpt-5",
			BaseURL:      "https://openrouter.ai/api/v1",
			APIKeyEnvVar: "OPENROUTER_API_KEY",
		},
	})
	model.settingsConfig = &form

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyF8})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected F8 to trigger a save-and-activate command")
	}
	msg := cmd()
	savedMsg, ok := msg.(settingsProviderSavedMsg)
	if !ok {
		t.Fatalf("expected settingsProviderSavedMsg, got %T", msg)
	}
	if !savedMsg.activated {
		t.Fatalf("expected activated save result, got %#v", savedMsg)
	}
	if savedMsg.ctrl != switchedCtrl {
		t.Fatal("expected switched controller when activating provider")
	}
	updated, _ = model.Update(savedMsg)
	model = updated.(Model)
	if model.activeProvider.Preset != provider.PresetOpenRouter {
		t.Fatalf("expected active provider to switch, got %#v", model.activeProvider)
	}
	last := model.entries[len(model.entries)-1]
	if last.Title != "system" || !strings.Contains(last.Body, "Saved and activated provider settings") {
		t.Fatalf("expected activation success message, got %#v", last)
	}
	if model.settingsBanner != "Activated OpenRouter Responses." {
		t.Fatalf("expected activation banner, got %q", model.settingsBanner)
	}
}

func TestF7TestsProviderFromSettingsForm(t *testing.T) {
	tested := provider.Profile{}
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) { return nil, nil },
		nil,
		nil,
		nil,
	).WithProviderTester(func(profile provider.Profile) error {
		tested = profile
		return nil
	})
	model.settingsOpen = true
	model.settingsStep = settingsStepProviderForm
	form := buildOnboardingForm(provider.OnboardingCandidate{
		Profile: provider.Profile{
			Preset:       provider.PresetOpenRouter,
			Name:         "OpenRouter Responses",
			Model:        "openai/gpt-5",
			BaseURL:      "https://openrouter.ai/api/v1",
			APIKeyEnvVar: "OPENROUTER_API_KEY",
		},
	})
	form.fields[settingsFieldIndex(t, &form, "api_key")].value = "router-secret"
	model.settingsConfig = &form

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyF7})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected F7 to trigger a provider test")
	}
	msg := cmd()
	testMsg, ok := msg.(settingsProviderTestedMsg)
	if !ok {
		t.Fatalf("expected settingsProviderTestedMsg, got %T", msg)
	}
	if testMsg.err != nil {
		t.Fatalf("expected successful provider test, got %v", testMsg.err)
	}
	updated, _ = model.Update(testMsg)
	model = updated.(Model)
	if tested.Preset != provider.PresetOpenRouter || tested.APIKey != "router-secret" || tested.Thinking != "on" || tested.ReasoningEffort != "medium" {
		t.Fatalf("expected tested provider profile, got %#v", tested)
	}
	if model.settingsBanner != "Provider test succeeded for OpenRouter Responses (https://openrouter.ai/api/v1, auth OS keyring)." {
		t.Fatalf("expected provider test success banner, got %q", model.settingsBanner)
	}
}

func TestSettingsSessionDangerousPromptsForConfirmation(t *testing.T) {
	ctrl := &fakeController{}
	model := NewModel(fakeWorkspace(), ctrl).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) { return nil, nil },
		nil,
		nil,
		nil,
	)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyF10})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settingsStep != settingsStepSession {
		t.Fatalf("expected session settings step, got %q", model.settingsStep)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("expected dangerous selection to wait for confirmation")
	}
	if model.pendingDangerousConfirm == nil {
		t.Fatal("expected dangerous confirmation card")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	model = updated.(Model)
	if model.pendingDangerousConfirm != nil {
		t.Fatal("expected cancel to clear dangerous confirmation")
	}
	if model.settingsStep != settingsStepSession {
		t.Fatalf("expected session settings to remain active, got %q", model.settingsStep)
	}
}

func TestStatusLineShowsActiveRuntimeBeforeModelInfo(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenRouter, Model: "openai/gpt-5-nano-2025-08-07"},
		nil,
		nil,
		nil,
		nil,
	).WithRuntimeSettings(provider.RuntimeCodexSDK, "/custom/codex", nil, nil)
	model.width = 100
	model.height = 20
	model.shellContext = shell.PromptContext{User: "localuser", Host: "workstation", Directory: "~/workspace/project", PromptSymbol: "%"}

	if !strings.Contains(model.View(), "Codex CLI Bridge") {
		t.Fatalf("expected active runtime in status line, got %q", model.View())
	}
}

func TestStatusLineShowsLastReplyModel(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{
		contextUsage: controller.ContextWindowUsage{ApproxPromptTokens: 3200},
	}).WithProviderOnboarding(
		provider.Profile{
			Preset: provider.PresetOpenRouter,
			Model:  "openai/gpt-5-nano-2025-08-07",
			SelectedModel: &provider.ModelOption{
				ContextWindow: 128000,
			},
		},
		nil,
		nil,
		nil,
		nil,
	)
	model.width = 100
	model.height = 20
	model.shellContext = shell.PromptContext{User: "localuser", Host: "workstation", Directory: "~/workspace/project", PromptSymbol: "%"}
	updated, _ := model.Update(controllerEventsMsg{events: []controller.TranscriptEvent{{Kind: controller.EventModelInfo, Payload: controller.AgentModelInfo{ProviderPreset: "openrouter", RequestedModel: "openrouter/auto", ResponseModel: "openai/gpt-5-nano-2025-08-07"}}}})
	model = updated.(Model)
	if !strings.Contains(model.View(), "confirm") || !strings.Contains(model.View(), "OpenRouter / openai/gpt-5-nano-2025-08-07") {
		t.Fatalf("expected last reply model in status line, got %q", model.View())
	}
	if !strings.Contains(model.View(), "[") || !strings.Contains(model.View(), "3.2k/128k") {
		t.Fatalf("expected context usage in status line, got %q", model.View())
	}
}

func TestAutoApprovalModeRendersInStatusWithoutModelInfo(t *testing.T) {
	ctrl := &fakeController{approvalMode: controller.ApprovalModeAuto}
	model := NewModel(fakeWorkspace(), ctrl)
	model.width = 80
	model.height = 20

	if !strings.Contains(model.View(), "auto") {
		t.Fatalf("expected auto approval mode in status line, got %q", model.View())
	}
}

func TestDangerousApprovalModeRendersInStatusWithoutModelInfo(t *testing.T) {
	ctrl := &fakeController{approvalMode: controller.ApprovalModeDanger}
	model := NewModel(fakeWorkspace(), ctrl)
	model.width = 80
	model.height = 20

	if !strings.Contains(model.View(), "dangerous") {
		t.Fatalf("expected dangerous approval mode in status line, got %q", model.View())
	}
}

func TestProviderSummaryLineShowsAuthSource(t *testing.T) {
	profile := provider.Profile{
		Preset:       provider.PresetOpenAI,
		Model:        "gpt-5-nano-2025-08-07",
		BaseURL:      "https://api.openai.com/v1",
		APIKeyEnvVar: "os_keyring",
	}
	line := providerSummaryLine(profile)
	if !strings.Contains(line, "auth=OS keyring") {
		t.Fatalf("expected keyring auth source in summary, got %q", line)
	}
}

func TestProviderSummaryLineShowsPlaintextFallbackSource(t *testing.T) {
	profile := provider.Profile{
		Preset:       provider.PresetAnthropic,
		Model:        "claude-sonnet-4-6",
		BaseURL:      "https://api.anthropic.com",
		APIKeyEnvVar: "local_file",
	}
	line := providerSummaryLine(profile)
	if !strings.Contains(line, "auth=local file (less secure)") {
		t.Fatalf("expected plaintext fallback auth source in summary, got %q", line)
	}
}

func TestProviderTestFailureBannerIncludesHealthGuidance(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	updated, _ := model.Update(settingsProviderTestedMsg{
		profile: provider.Profile{Preset: provider.PresetOllama, Name: "Ollama Chat", BaseURL: "http://localhost:11434/api"},
		err:     errors.New("dial tcp timeout"),
	})
	model = updated.(Model)
	if !strings.Contains(model.settingsBanner, "Verify the Ollama server is reachable") {
		t.Fatalf("expected guided failure banner, got %q", model.settingsBanner)
	}
}

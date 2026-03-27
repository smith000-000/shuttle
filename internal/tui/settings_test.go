package tui

import (
	"aiterm/internal/controller"
	"aiterm/internal/provider"
	shuttleruntime "aiterm/internal/runtime"
	"aiterm/internal/search"
	"aiterm/internal/shell"
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
)

func TestSlashProviderOpensActiveProviderSettings(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.mode = AgentMode
	model.input = "/provider"
	model = model.WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{
				{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5", BaseURL: "https://api.openai.com/v1"}},
				{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter", Model: "openai/gpt-5", BaseURL: "https://openrouter.ai/api/v1"}},
			}, nil
		},
		func(provider.Profile) ([]provider.ModelOption, error) { return nil, nil },
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)

	updated, cmd := model.submit()
	next := updated.(Model)
	if cmd != nil {
		t.Fatal("expected /provider to open synchronously")
	}
	if !next.settingsOpen || next.settingsStep != settingsStepActiveProvider {
		t.Fatalf("expected active provider settings, got open=%t step=%q", next.settingsOpen, next.settingsStep)
	}
}

func TestSlashModelOpensScopedActiveModelSettings(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{})
	model.mode = AgentMode
	model.input = "/model"
	model = model.WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{
				{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5", BaseURL: "https://api.openai.com/v1"}},
				{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter", Model: "openai/gpt-5", BaseURL: "https://openrouter.ai/api/v1"}},
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
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)

	updated, cmd := model.submit()
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("expected /model to load models asynchronously")
	}
	if !next.settingsOpen || next.settingsStep != settingsStepActiveModels {
		t.Fatalf("expected active model settings, got open=%t step=%q", next.settingsOpen, next.settingsStep)
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
	if !strings.Contains(last.Body, "/approvals") || !strings.Contains(last.Body, "/coder") || !strings.Contains(last.Body, "/new") || !strings.Contains(last.Body, "/compact") {
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

func TestSlashCoderRoutesPromptToExternalAgent(t *testing.T) {
	ctrl := &fakeController{
		externalState: controller.ExternalState{
			PreferredRuntime: "pi",
			Available:        true,
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "/coder please fix the script"

	updated, cmd := model.submit()
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("expected /coder prompt to stage confirmation by default")
	}
	if model.pendingHandoff == nil || model.pendingHandoffPrompt != "please fix the script" {
		t.Fatalf("expected staged handoff, got handoff=%#v prompt=%q", model.pendingHandoff, model.pendingHandoffPrompt)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected confirmed /coder prompt to call the controller")
	}
	updated, _ = model.Update(controllerEventsFromCmd(t, cmd))
	model = updated.(Model)

	if len(ctrl.externalPrompts) != 1 || ctrl.externalPrompts[0] != "please fix the script" {
		t.Fatalf("expected /coder to route prompt externally, got %#v", ctrl.externalPrompts)
	}
	last := model.entries[len(model.entries)-1]
	if last.Title != "system" || !strings.Contains(last.Body, "routed to external agent") {
		t.Fatalf("expected external routing notice, got %#v", last)
	}
}

func TestSlashCoderRoutesPromptDirectlyWhenConfirmationDisabled(t *testing.T) {
	ctrl := &fakeController{
		externalState: controller.ExternalState{
			PreferredRuntime: "pi",
			Available:        true,
			ConfirmationMode: "off",
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "/coder please fix the script"

	updated, cmd := model.submit()
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected /coder prompt to route immediately when confirmation is disabled")
	}
	updated, _ = model.Update(controllerEventsFromCmd(t, cmd))
	model = updated.(Model)

	if len(ctrl.externalPrompts) != 1 || ctrl.externalPrompts[0] != "please fix the script" {
		t.Fatalf("expected direct external routing, got %#v", ctrl.externalPrompts)
	}
}

func TestSlashCoderResumesExternalWhenNoPromptProvided(t *testing.T) {
	ctrl := &fakeController{
		externalState: controller.ExternalState{
			PreferredRuntime: "pi",
			Available:        true,
			Resumable:        true,
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "/coder"

	updated, cmd := model.submit()
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected bare /coder to resume external context")
	}
	updated, _ = model.Update(controllerEventsFromCmd(t, cmd))
	model = updated.(Model)

	last := model.entries[len(model.entries)-1]
	if last.Title != "system" || !strings.Contains(last.Body, "external resumed") {
		t.Fatalf("expected external resume notice, got %#v", last)
	}
}

func TestSlashCoderTogglesBackToBuiltinWhenExternalOwnsConversation(t *testing.T) {
	ctrl := &fakeController{
		externalState: controller.ExternalState{
			PreferredRuntime: "pi",
			Available:        true,
			Owner:            controller.ConversationOwnerExternal,
		},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.mode = AgentMode
	model.input = "/coder"

	updated, cmd := model.submit()
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected bare /coder to return to builtin while external mode is active")
	}
	updated, _ = model.Update(controllerEventsFromCmd(t, cmd))
	model = updated.(Model)

	last := model.entries[len(model.entries)-1]
	if last.Title != "system" || !strings.Contains(last.Body, "returned to builtin") {
		t.Fatalf("expected builtin-return notice, got %#v", last)
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

func TestRuntimeSettingsShowSearchStatus(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).
		WithRuntimeSupport(
			shuttleruntime.Selection{
				ID:     shuttleruntime.RuntimePi,
				Search: search.RuntimeAvailability("pi", search.ProviderBrave),
			},
			func(shuttleruntime.Selection) error { return nil },
			func(bool) error { return nil },
			func(provider.Profile) []shuttleruntime.Choice {
				return []shuttleruntime.Choice{{
					Selection: shuttleruntime.Selection{
						ID:     shuttleruntime.RuntimePi,
						Search: search.RuntimeAvailability("pi", search.ProviderBrave),
					},
					Label: "pi",
				}}
			},
		).
		WithSearchSupport(search.ShuttleAvailability(search.ProviderBrave))
	model.settingsOpen = true
	model.settingsStep = settingsStepRuntime
	model.settingsRuntimes = buildSettingsRuntimeEntries([]shuttleruntime.Choice{{
		Selection: shuttleruntime.Selection{
			ID:     shuttleruntime.RuntimePi,
			Search: search.RuntimeAvailability("pi", search.ProviderBrave),
		},
		Label: "pi",
	}})

	view := model.View()
	for _, fragment := range []string{
		"Builtin web search: Shuttle-managed (Brave)",
		"External web search: Runtime-native (pi) with Shuttle fallback (Brave)",
	} {
		if !strings.Contains(view, fragment) {
			t.Fatalf("expected %q in runtime settings, got %q", fragment, view)
		}
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

func TestProviderOnboardingSelectionSwitchesController(t *testing.T) {
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
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
			return switchedCtrl, profile, nil
		},
		func(provider.Profile) error { return nil },
	)

	updated, cmd := model.openOnboarding()
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("expected onboarding open to be synchronous")
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected model enumeration command")
	}
	modelsMsg, ok := cmd().(providerModelsLoadedMsg)
	if !ok {
		t.Fatalf("expected providerModelsLoadedMsg")
	}
	updated, _ = model.Update(modelsMsg)
	model = updated.(Model)
	model.onboardingModelIdx = 1
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected provider switch command")
	}
	switchMsg, ok := cmd().(providerSwitchedMsg)
	if !ok {
		t.Fatalf("expected providerSwitchedMsg")
	}
	updated, _ = model.Update(switchMsg)
	model = updated.(Model)
	if model.ctrl != switchedCtrl {
		t.Fatal("expected controller to be replaced after provider switch")
	}
	if model.activeProvider.Preset != provider.PresetOpenRouter || model.activeProvider.Model != "openai/gpt-5-mini" {
		t.Fatalf("expected selected provider/model, got %#v", model.activeProvider)
	}
	if model.onboardingOpen {
		t.Fatal("expected onboarding view to close after selection")
	}
}

func TestManualProviderOnboardingCollectsConfigAndPersists(t *testing.T) {
	switchedCtrl := &fakeController{}
	var savedProfile provider.Profile
	var savedCount int
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetMock, Name: "Mock Provider"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openai/gpt-5", BaseURL: "https://openrouter.ai/api/v1"}, Reason: "Manual setup.", Manual: true}}, nil
		},
		nil,
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
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
		t.Fatal("expected onboarding open to be synchronous")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	model.onboardingForm.index = 2
	model.onboardingForm.fields[2].value = "router-secret"
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("expected provider switch command")
	}
	switchMsg, ok := cmd().(providerSwitchedMsg)
	if !ok {
		t.Fatalf("expected providerSwitchedMsg")
	}
	updated, _ = model.Update(switchMsg)
	model = updated.(Model)
	if model.ctrl != switchedCtrl {
		t.Fatal("expected controller to switch")
	}
	if savedCount != 1 || savedProfile.APIKey != "router-secret" || savedProfile.Model != "openai/gpt-5" {
		t.Fatalf("unexpected saved profile %#v count=%d", savedProfile, savedCount)
	}
}

func TestF10OpensSettingsMenuWithSessionAndProviderEntries(t *testing.T) {
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
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyF10})
	model = updated.(Model)
	if !model.settingsOpen || model.settingsStep != settingsStepMenu || len(model.settingsProviders) < 4 {
		t.Fatalf("unexpected settings state")
	}
	view := model.View()
	for _, fragment := range []string{"Session Settings", "Configure Providers", "Change Active Provider", "Select Model"} {
		if !strings.Contains(view, fragment) {
			t.Fatalf("expected settings menu entry %q in view %q", fragment, view)
		}
	}
}

func TestSettingsActiveModelSelectionSwitchesProvider(t *testing.T) {
	switchedCtrl := &fakeController{}
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{
				{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"}},
				{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openai/gpt-5", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}},
			}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			if profile.Preset == provider.PresetOpenRouter {
				return []provider.ModelOption{{ID: "openrouter/auto"}, {ID: "qwen/qwen3.5-9b"}}, nil
			}
			return []provider.ModelOption{{ID: profile.Model}}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
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
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	model.settingsModelIdx = 3
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	switchMsg := cmd().(providerSwitchedMsg)
	updated, _ = model.Update(switchMsg)
	model = updated.(Model)
	if model.ctrl != switchedCtrl || model.activeProvider.Preset != provider.PresetOpenRouter || model.activeProvider.Model != "qwen/qwen3.5-9b" || !model.settingsOpen || model.settingsStep != settingsStepActiveModels {
		t.Fatalf("unexpected settings switch result %#v", model.activeProvider)
	}
}

func TestSettingsActiveProviderSwitchesProviderWithoutChangingModel(t *testing.T) {
	switchedCtrl := &fakeController{}
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{
				{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"}},
				{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}},
			}, nil
		},
		nil,
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
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
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settingsStep != settingsStepActiveProvider {
		t.Fatalf("expected active provider step, got %q", model.settingsStep)
	}
	model.settingsProviderIdx = 1
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	switchMsg := cmd().(providerSwitchedMsg)
	updated, _ = model.Update(switchMsg)
	model = updated.(Model)
	if model.ctrl != switchedCtrl || model.activeProvider.Preset != provider.PresetOpenRouter || model.activeProvider.Model != "openrouter/auto" || !model.settingsOpen || model.settingsStep != settingsStepActiveProvider {
		t.Fatalf("unexpected active provider switch result %#v", model.activeProvider)
	}
}

func TestSettingsActiveProviderEscReturnsToMenu(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{
				{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"}},
				{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openrouter/auto", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}},
			}, nil
		},
		nil,
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
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
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settingsStep != settingsStepActiveProvider {
		t.Fatalf("expected active provider step, got %q", model.settingsStep)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.settingsStep != settingsStepMenu {
		t.Fatalf("expected Esc to return to settings menu, got %q", model.settingsStep)
	}
}

func TestSettingsActiveModelFilterNarrowsChoices(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{
				{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"}},
				{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "openai/gpt-5", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnvVar: "OPENROUTER_API_KEY"}},
			}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			if profile.Preset == provider.PresetOpenRouter {
				return []provider.ModelOption{{ID: "openrouter/auto"}, {ID: "qwen/qwen3.5-9b"}, {ID: "qwen/qwen3.5-32b"}}, nil
			}
			return []provider.ModelOption{{ID: profile.Model}}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
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
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q35")})
	model = updated.(Model)
	if model.settingsModelFilter != "q35" || len(model.settingsModelCatalog) != 5 || len(model.settingsModels) != 2 || model.settingsModels[0].model.ID != "qwen/qwen3.5-9b" {
		t.Fatalf("unexpected filtered settings state")
	}
	view := model.View()
	if !strings.Contains(view, "Filter: q35  (2 matches)") || strings.Contains(view, "openrouter/auto") {
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
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
			return &fakeController{}, profile, nil
		},
		func(provider.Profile) error { return nil },
	)
	form := buildOnboardingForm(provider.OnboardingCandidate{
		Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
	})
	form.fields[1].value = "gpt-5-typo"
	form.fields[2].value = "manual-secret"
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

func TestSettingsActiveModelEscClearsFilterBeforeClosing(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenAI, Name: "OpenAI Responses", Model: "gpt-5-nano-2025-08-07", BaseURL: "https://api.openai.com/v1"}}}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			return []provider.ModelOption{{ID: profile.Model}}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
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
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("nano")})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.settingsStep != settingsStepActiveModels || model.settingsModelFilter != "" {
		t.Fatalf("expected esc to clear filter first")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.settingsStep != settingsStepMenu {
		t.Fatalf("expected second esc to return to menu")
	}
}

func TestSettingsActiveModelInfoToggleShowsSelectedDetailsOnly(t *testing.T) {
	model := NewModel(fakeWorkspace(), &fakeController{}).WithProviderOnboarding(
		provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "allenai/olmo-3-7b-think", BaseURL: "https://openrouter.ai/api/v1"},
		func() ([]provider.OnboardingCandidate, error) {
			return []provider.OnboardingCandidate{{Profile: provider.Profile{Preset: provider.PresetOpenRouter, Name: "OpenRouter Responses", Model: "allenai/olmo-3-7b-think", BaseURL: "https://openrouter.ai/api/v1"}}}, nil
		},
		func(profile provider.Profile) ([]provider.ModelOption, error) {
			return []provider.ModelOption{
				{ID: "allenai/olmo-3-7b-think", Name: "AllenAI: Olmo 3 7B Think", ContextWindow: 65536, MaxCompletionTokens: 65536, PromptPrice: "0.00000012", CompletionPrice: "0.0000002", SupportedParameters: []string{"reasoning", "structured_outputs"}, Architecture: provider.ModelArchitecture{Modality: "text->text"}, Description: "Long form provider description that should only appear when info is toggled."},
				{ID: "qwen/qwen3.5-9b"},
			}, nil
		},
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
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
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	loaded := cmd().(settingsModelsLoadedMsg)
	updated, _ = model.Update(loaded)
	model = updated.(Model)
	view := model.View()
	if !strings.Contains(view, "OpenRouter / allenai/olmo-3-7b-think") {
		t.Fatalf("expected provider label next to model slug, got %q", view)
	}
	if !strings.Contains(view, "AllenAI: Olmo 3 7B Think  context 65536  max out 65536  pricing p=0.00000012 c=0.0000002") || !strings.Contains(view, "mode text->text") || strings.Contains(view, "Long form provider description") || strings.Contains(view, "params reasoning,structured_outputs") {
		t.Fatalf("unexpected default model info view %q", view)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	model = updated.(Model)
	view = model.View()
	if !model.settingsModelInfo || !strings.Contains(view, "Long form provider description") || !strings.Contains(view, "params reasoning,structured_outputs") {
		t.Fatalf("expected expanded model info, got %q", view)
	}
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
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
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
	form.fields[2].value = "manual-secret"
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
		func(profile provider.Profile, _ *shell.PromptContext) (controller.Controller, provider.Profile, error) {
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
	form.fields[2].value = "router-secret"
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
	if tested.Preset != provider.PresetOpenRouter || tested.APIKey != "router-secret" {
		t.Fatalf("expected tested provider profile, got %#v", tested)
	}
	if model.settingsBanner != "Provider test succeeded for OpenRouter Responses." {
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
	if !strings.Contains(model.View(), "APR confirm  MODEL OpenRouter / openai/gpt-5-nano-2025-08-07") {
		t.Fatalf("expected last reply model in status line, got %q", model.View())
	}
	if !strings.Contains(model.View(), "CTX ~3.2k/128k") {
		t.Fatalf("expected context usage in status line, got %q", model.View())
	}
}

func TestAutoApprovalModeRendersInStatusWithoutModelInfo(t *testing.T) {
	ctrl := &fakeController{approvalMode: controller.ApprovalModeAuto}
	model := NewModel(fakeWorkspace(), ctrl)
	model.width = 80
	model.height = 20

	if !strings.Contains(model.View(), "APR auto") {
		t.Fatalf("expected auto approval mode in status line, got %q", model.View())
	}
}

func TestDangerousApprovalModeRendersInStatusWithoutModelInfo(t *testing.T) {
	ctrl := &fakeController{approvalMode: controller.ApprovalModeDanger}
	model := NewModel(fakeWorkspace(), ctrl)
	model.width = 80
	model.height = 20

	if !strings.Contains(model.View(), "APR dangerous") {
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

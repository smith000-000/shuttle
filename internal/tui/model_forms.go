package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"aiterm/internal/config"
	"aiterm/internal/controller"
	"aiterm/internal/provider"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) updateOnboarding(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyF2:
		return m.takeControlNow()
	case tea.KeyEsc:
		if m.onboardingStep == onboardingStepConfig {
			m.onboardingStep = onboardingStepProviders
			m.onboardingForm = nil
			m.onboardingSelected = nil
			return m, nil
		}
		if m.onboardingStep == onboardingStepModels {
			m.onboardingStep = onboardingStepProviders
			m.onboardingSelected = nil
			m.onboardingForm = nil
			m.onboardingModels = nil
			m.onboardingModelIdx = 0
			return m, nil
		}
		m.onboardingOpen = false
		m.onboardingStep = onboardingStepProviders
		m.onboardingIndex = 0
		m.onboardingChoices = nil
		m.onboardingSelected = nil
		m.onboardingForm = nil
		m.onboardingModels = nil
		m.onboardingModelIdx = 0
		return m, nil
	case tea.KeyUp:
		if m.onboardingStep == onboardingStepConfig {
			if m.onboardingForm != nil && m.onboardingForm.index > 0 {
				m.onboardingForm.index--
			}
			return m, nil
		}
		if m.onboardingStep == onboardingStepModels {
			if m.onboardingModelIdx > 0 {
				m.onboardingModelIdx--
			}
			return m, nil
		}
		if m.onboardingIndex > 0 {
			m.onboardingIndex--
		}
		return m, nil
	case tea.KeyDown:
		if m.onboardingStep == onboardingStepConfig {
			if m.onboardingForm != nil && m.onboardingForm.index < len(m.onboardingForm.fields)-1 {
				m.onboardingForm.index++
			}
			return m, nil
		}
		if m.onboardingStep == onboardingStepModels {
			if m.onboardingModelIdx < len(m.onboardingModels)-1 {
				m.onboardingModelIdx++
			}
			return m, nil
		}
		if m.onboardingIndex < len(m.onboardingChoices)-1 {
			m.onboardingIndex++
		}
		return m, nil
	case tea.KeyTab:
		if m.onboardingStep == onboardingStepConfig {
			if m.onboardingForm != nil && len(m.onboardingForm.fields) > 0 {
				m.onboardingForm.index = (m.onboardingForm.index + 1) % len(m.onboardingForm.fields)
			}
			return m, nil
		}
		return m, nil
	case tea.KeyPgUp:
		if m.onboardingStep == onboardingStepModels {
			m.onboardingModelIdx -= 8
			if m.onboardingModelIdx < 0 {
				m.onboardingModelIdx = 0
			}
		}
		return m, nil
	case tea.KeyPgDown:
		if m.onboardingStep == onboardingStepModels {
			m.onboardingModelIdx += 8
			if m.onboardingModelIdx >= len(m.onboardingModels) {
				m.onboardingModelIdx = max(0, len(m.onboardingModels)-1)
			}
		}
		return m, nil
	case tea.KeyEnter:
		return m.applyOnboardingSelection()
	case tea.KeyBackspace, tea.KeyDelete:
		if m.onboardingStep == onboardingStepConfig && m.onboardingForm != nil {
			field := &m.onboardingForm.fields[m.onboardingForm.index]
			if len(field.value) > 0 {
				field.value = field.value[:len(field.value)-1]
			}
			return m, nil
		}
		return m, nil
	case tea.KeySpace:
		if m.onboardingStep == onboardingStepConfig && m.onboardingForm != nil {
			m.onboardingForm.fields[m.onboardingForm.index].value += " "
			return m, nil
		}
		return m, nil
	default:
		if m.onboardingStep == onboardingStepConfig && m.onboardingForm != nil && !msg.Alt && msg.Type == tea.KeyRunes {
			m.onboardingForm.fields[m.onboardingForm.index].value += string(msg.Runes)
			return m, nil
		}
		return m, nil
	}
}

func (m Model) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyF2:
		return m.takeControlNow()
	case tea.KeyF10:
		m.settingsOpen = false
		m.settingsStep = settingsStepMenu
		m.settingsConfig = nil
		m.settingsModelCatalog = nil
		m.settingsModels = nil
		m.settingsModelFilter = ""
		m.settingsModelInfo = false
		m.settingsBanner = ""
		return m, nil
	case tea.KeyF7:
		if m.settingsStep == settingsStepProviderForm {
			return m.testSettingsProfile()
		}
		return m, nil
	case tea.KeyF8:
		if m.settingsStep == settingsStepProviderForm {
			return m.saveSettingsProfile(true)
		}
		return m, nil
	case tea.KeyEsc:
		switch m.settingsStep {
		case settingsStepProviderForm:
			m.settingsStep = settingsStepProviders
			m.settingsConfig = nil
			m.settingsBanner = ""
		case settingsStepRuntime:
			m.settingsStep = settingsStepMenu
			m.settingsBanner = ""
		case settingsStepActiveModels:
			if m.settingsModelFilter != "" {
				m.settingsModelFilter = ""
				m.settingsModelInfo = false
				m.applySettingsModelFilter()
				return m, nil
			}
			m.settingsStep = settingsStepMenu
			m.settingsModelCatalog = nil
			m.settingsModels = nil
			m.settingsModelIdx = 0
			m.settingsModelInfo = false
			m.settingsBanner = ""
		case settingsStepActiveProvider:
			m.settingsStep = settingsStepMenu
			m.settingsBanner = ""
		case settingsStepProviders:
			m.settingsStep = settingsStepMenu
			m.settingsBanner = ""
		case settingsStepSession:
			m.settingsStep = settingsStepMenu
			m.settingsBanner = ""
		default:
			m.settingsOpen = false
			m.settingsBanner = ""
		}
		return m, nil
	case tea.KeyUp:
		switch m.settingsStep {
		case settingsStepMenu:
			if m.settingsIndex > 0 {
				m.settingsIndex--
			}
		case settingsStepSession:
			if m.settingsApprovalIdx > 0 {
				m.settingsApprovalIdx--
			}
		case settingsStepRuntime:
			if m.settingsRuntimeIdx > 0 {
				m.settingsRuntimeIdx--
			}
		case settingsStepProviders, settingsStepActiveProvider:
			if m.settingsProviderIdx > 0 {
				m.settingsProviderIdx--
			}
		case settingsStepActiveModels:
			if m.settingsModelIdx > 0 {
				m.settingsModelIdx--
				m.settingsModelInfo = false
			}
		case settingsStepProviderForm:
			if m.settingsConfig != nil && m.settingsConfig.index > 0 {
				m.settingsConfig.index--
			}
		}
		return m, nil
	case tea.KeyDown:
		switch m.settingsStep {
		case settingsStepMenu:
			if m.settingsIndex < len(m.settingsMenuEntries())-1 {
				m.settingsIndex++
			}
		case settingsStepSession:
			if m.settingsApprovalIdx < len(settingsApprovalEntries())-1 {
				m.settingsApprovalIdx++
			}
		case settingsStepRuntime:
			if m.settingsRuntimeIdx < len(m.settingsRuntimes)-1 {
				m.settingsRuntimeIdx++
			}
		case settingsStepProviders, settingsStepActiveProvider:
			if m.settingsProviderIdx < len(m.settingsProviders)-1 {
				m.settingsProviderIdx++
			}
		case settingsStepActiveModels:
			if m.settingsModelIdx < len(m.settingsModels)-1 {
				m.settingsModelIdx++
				m.settingsModelInfo = false
			}
		case settingsStepProviderForm:
			if m.settingsConfig != nil && m.settingsConfig.index < len(m.settingsConfig.fields)-1 {
				m.settingsConfig.index++
			}
		}
		return m, nil
	case tea.KeyTab:
		if m.settingsStep == settingsStepProviderForm && m.settingsConfig != nil && len(m.settingsConfig.fields) > 0 {
			m.settingsConfig.index = (m.settingsConfig.index + 1) % len(m.settingsConfig.fields)
		}
		return m, nil
	case tea.KeyPgUp:
		if m.settingsStep == settingsStepActiveModels {
			m.settingsModelIdx -= 8
			if m.settingsModelIdx < 0 {
				m.settingsModelIdx = 0
			}
			m.settingsModelInfo = false
		}
		return m, nil
	case tea.KeyPgDown:
		if m.settingsStep == settingsStepActiveModels {
			m.settingsModelIdx += 8
			if m.settingsModelIdx >= len(m.settingsModels) {
				m.settingsModelIdx = max(0, len(m.settingsModels)-1)
			}
			m.settingsModelInfo = false
		}
		return m, nil
	case tea.KeyBackspace, tea.KeyDelete:
		if m.settingsStep == settingsStepActiveModels && m.settingsModelFilter != "" {
			m.settingsModelFilter = trimLastRune(m.settingsModelFilter)
			m.settingsModelInfo = false
			m.applySettingsModelFilter()
			return m, nil
		}
		if m.settingsStep == settingsStepProviderForm && m.settingsConfig != nil {
			field := &m.settingsConfig.fields[m.settingsConfig.index]
			if len(field.value) > 0 {
				field.value = field.value[:len(field.value)-1]
			}
		}
		return m, nil
	case tea.KeySpace:
		if m.settingsStep == settingsStepActiveModels {
			m.settingsModelFilter += " "
			m.settingsModelInfo = false
			m.applySettingsModelFilter()
			return m, nil
		}
		if m.settingsStep == settingsStepProviderForm && m.settingsConfig != nil {
			m.settingsConfig.fields[m.settingsConfig.index].value += " "
		}
		return m, nil
	case tea.KeyEnter:
		return m.applySettingsSelection()
	default:
		if m.settingsStep == settingsStepActiveModels && msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 'I' {
			if len(m.settingsModels) > 0 {
				m.settingsModelInfo = !m.settingsModelInfo
			}
			return m, nil
		}
		if m.settingsStep == settingsStepActiveModels && !msg.Alt && msg.Type == tea.KeyRunes {
			m.settingsModelFilter += string(msg.Runes)
			m.settingsModelInfo = false
			m.applySettingsModelFilter()
			return m, nil
		}
		if m.settingsStep == settingsStepProviderForm && m.settingsConfig != nil && !msg.Alt && msg.Type == tea.KeyRunes {
			m.settingsConfig.fields[m.settingsConfig.index].value += string(msg.Runes)
			return m, nil
		}
		return m, nil
	}
}

func (m Model) applyOnboardingSelection() (tea.Model, tea.Cmd) {
	if m.busy || m.switchProvider == nil || len(m.onboardingChoices) == 0 {
		return m, nil
	}

	if m.onboardingStep == onboardingStepProviders {
		choice := m.onboardingChoices[m.onboardingIndex]
		if choice.Manual {
			return m.openOnboardingConfig(choice)
		}
		if m.loadModels == nil {
			return m.switchOnboardingProfile(choice.Profile)
		}

		m.busy = true
		m.busyStartedAt = time.Now()
		return m, func() tea.Msg {
			models, err := m.loadModels(choice.Profile)
			return providerModelsLoadedMsg{
				candidate: choice,
				models:    models,
				err:       err,
			}
		}
	}

	if m.onboardingStep == onboardingStepConfig {
		return m.submitOnboardingConfig()
	}

	if m.onboardingSelected == nil || len(m.onboardingModels) == 0 {
		return m, nil
	}

	selectedProfile := m.onboardingSelected.Profile
	selectedModel := m.onboardingModels[m.onboardingModelIdx]
	selectedProfile.Model = selectedModel.ID
	selectedProfile.SelectedModel = &selectedModel
	return m.switchOnboardingProfile(selectedProfile)
}

func (m Model) switchOnboardingProfile(profile provider.Profile) (tea.Model, tea.Cmd) {
	return m.switchProfile(profile, "")
}

func (m Model) switchSettingsProfile(profile provider.Profile, step settingsStep) (tea.Model, tea.Cmd) {
	return m.switchProfile(profile, step)
}

func (m Model) switchProfile(profile provider.Profile, step settingsStep) (tea.Model, tea.Cmd) {
	shellContext := m.currentShellContext()
	m.busy = true
	m.busyStartedAt = time.Now()
	return m, func() tea.Msg {
		ctrl, profile, runtimeSelection, err := m.switchProvider(profile, shellContext)
		var persistErr error
		if err == nil && m.saveProvider != nil {
			persistErr = m.saveProvider(profile)
		}
		if err == nil && m.saveRuntime != nil {
			if runtimeErr := m.saveRuntime(runtimeSelection); runtimeErr != nil && persistErr == nil {
				persistErr = runtimeErr
			}
		}
		return providerSwitchedMsg{
			ctrl:         ctrl,
			profile:      profile,
			runtime:      runtimeSelection,
			err:          err,
			persistErr:   persistErr,
			settingsStep: step,
		}
	}
}

func (m Model) openOnboardingConfig(choice provider.OnboardingCandidate) (tea.Model, tea.Cmd) {
	form := buildOnboardingForm(choice)
	m.onboardingStep = onboardingStepConfig
	m.onboardingSelected = &choice
	m.onboardingForm = &form
	m.onboardingModels = nil
	m.onboardingModelIdx = 0
	return m, nil
}

func (m Model) submitOnboardingConfig() (tea.Model, tea.Cmd) {
	if m.onboardingForm == nil {
		return m, nil
	}

	profile, err := m.resolveOnboardingFormProfile()
	if err != nil {
		m.entries = append(m.entries, Entry{
			Title: "error",
			Body:  fmt.Sprintf("provider onboarding: %v", err),
		})
		m.selectedEntry = len(m.entries) - 1
		m.clampSelection()
		return m, nil
	}

	return m.switchOnboardingProfile(profile)
}

func (m Model) applySettingsSelection() (tea.Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}

	switch m.settingsStep {
	case settingsStepMenu:
		if m.settingsIndex == 0 {
			m.settingsStep = settingsStepSession
			m.settingsApprovalIdx = currentSettingsApprovalIndex(m.ctrl)
			m.settingsBanner = ""
			return m, nil
		}
		if m.settingsIndex == 1 {
			m.settingsStep = settingsStepProviders
			m.settingsProviderIdx = m.currentSettingsProviderIndex()
			m.settingsModelCatalog = nil
			m.settingsModels = nil
			m.settingsModelIdx = 0
			m.settingsModelFilter = ""
			m.settingsModelInfo = false
			m.settingsBanner = ""
			return m, nil
		}
		if m.settingsIndex == 2 {
			m.settingsStep = settingsStepActiveProvider
			m.settingsProviderIdx = m.currentSettingsProviderIndex()
			m.settingsBanner = ""
			return m, nil
		}
		if m.settingsIndex == 3 {
			m.settingsBanner = ""
			return m.loadSettingsModels()
		}
		if m.loadRuntimeChoices != nil {
			m.settingsRuntimes = buildSettingsRuntimeEntries(m.loadRuntimeChoices(m.activeProvider))
		} else {
			m.settingsRuntimes = nil
		}
		m.settingsStep = settingsStepRuntime
		m.settingsRuntimeIdx = currentSettingsRuntimeIndex(m.settingsRuntimes, m.activeRuntime)
		m.settingsBanner = ""
		return m, nil
	case settingsStepSession:
		entries := settingsApprovalEntries()
		if len(entries) == 0 {
			return m, nil
		}
		selected := entries[m.settingsApprovalIdx]
		if selected.mode == controller.ApprovalModeDanger {
			m.pendingDangerousConfirm = &dangerousApprovalConfirm{mode: selected.mode}
			return m, nil
		}
		if m.ctrl == nil {
			return m, nil
		}
		m.busy = true
		m.busyStartedAt = time.Now()
		return m, func() tea.Msg {
			events, err := m.ctrl.SetApprovalMode(context.Background(), selected.mode)
			return controllerEventsMsg{events: events, err: err}
		}
	case settingsStepRuntime:
		if len(m.settingsRuntimes) == 0 {
			return m, nil
		}
		entry := m.settingsRuntimes[m.settingsRuntimeIdx]
		if entry.kind == settingsRuntimeEntryConfirmation {
			nextValue := !controller.ExternalConfirmationRequired(m.externalState)
			if m.saveRuntimeConfirmation != nil {
				if err := m.saveRuntimeConfirmation(nextValue); err != nil {
					m.settingsBanner = fmt.Sprintf("Runtime save failed: %v", err)
					return m, nil
				}
			}
			if nextValue {
				m.externalState.ConfirmationMode = "confirm"
			} else {
				m.externalState.ConfirmationMode = "off"
			}
			if m.ctrl != nil {
				m.externalState = m.ctrl.ExternalState()
				if nextValue && !controller.ExternalConfirmationRequired(m.externalState) {
					m.externalState.ConfirmationMode = "confirm"
				}
				if !nextValue && controller.ExternalConfirmationRequired(m.externalState) {
					m.externalState.ConfirmationMode = "off"
				}
			}
			if nextValue {
				m.settingsBanner = "External-agent confirmation is on."
			} else {
				m.settingsBanner = "External-agent confirmation is off."
			}
			return m, nil
		}
		if entry.disabled {
			return m, nil
		}
		m.activeRuntime = entry.selection
		if m.saveRuntime != nil {
			if err := m.saveRuntime(entry.selection); err != nil {
				m.settingsBanner = fmt.Sprintf("Runtime save failed: %v", err)
				return m, nil
			}
		}
		return m.switchSettingsProfile(m.activeProvider, settingsStepRuntime)
	case settingsStepProviders:
		if len(m.settingsProviders) == 0 {
			return m, nil
		}
		entry := m.settingsProviders[m.settingsProviderIdx]
		if entry.disabled || entry.candidate == nil {
			return m, nil
		}
		form := buildOnboardingForm(*entry.candidate)
		m.settingsStep = settingsStepProviderForm
		m.settingsConfig = &form
		return m, nil
	case settingsStepActiveProvider:
		if len(m.settingsProviders) == 0 {
			return m, nil
		}
		entry := m.settingsProviders[m.settingsProviderIdx]
		if entry.disabled || entry.candidate == nil {
			return m, nil
		}
		return m.switchSettingsProfile(entry.candidate.Profile, settingsStepActiveProvider)
	case settingsStepActiveModels:
		if len(m.settingsModels) == 0 {
			return m, nil
		}
		choice := m.settingsModels[m.settingsModelIdx]
		profile := choice.profile
		model := choice.model
		profile.Model = model.ID
		profile.SelectedModel = &model
		return m.switchSettingsProfile(profile, settingsStepActiveModels)
	case settingsStepProviderForm:
		return m.saveSettingsProfile(false)
	default:
		return m, nil
	}
}

func (m Model) loadSettingsModels() (tea.Model, tea.Cmd) {
	profiles := m.settingsConfiguredProfiles()
	if m.settingsModelScope != "" {
		filtered := make([]provider.Profile, 0, len(profiles))
		for _, profile := range profiles {
			if profile.Preset == m.settingsModelScope {
				filtered = append(filtered, profile)
			}
		}
		profiles = filtered
	}
	if len(profiles) == 0 {
		m.entries = append(m.entries, Entry{
			Title: "system",
			Body:  "No configured providers are available yet. Configure one from Providers first.",
		})
		m.selectedEntry = len(m.entries) - 1
		m.clampSelection()
		return m, nil
	}
	if m.loadModels == nil {
		return m, nil
	}

	m.busy = true
	m.busyStartedAt = time.Now()
	return m, func() tea.Msg {
		choices := make([]settingsModelChoice, 0, 16)
		for _, profile := range profiles {
			models, err := m.loadModels(profile)
			if err != nil {
				if strings.TrimSpace(profile.Model) == "" {
					continue
				}
				models = []provider.ModelOption{{
					ID:          profile.Model,
					Description: "currently saved model",
				}}
			}
			if strings.TrimSpace(profile.Model) != "" && !containsModelOption(models, profile.Model) {
				models = append([]provider.ModelOption{{
					ID:          profile.Model,
					Description: "currently saved model",
				}}, models...)
			}
			for _, model := range models {
				choices = append(choices, settingsModelChoice{
					profile: profile,
					model:   model,
				})
			}
		}
		return settingsModelsLoadedMsg{choices: choices}
	}
}

func (m Model) testSettingsProfile() (tea.Model, tea.Cmd) {
	if m.settingsConfig == nil || m.testProvider == nil {
		return m, nil
	}
	profile, err := m.resolveSettingsFormProfile()
	if err != nil {
		m.settingsBanner = fmt.Sprintf("Provider test failed: %v", err)
		return m, nil
	}
	m.busy = true
	m.busyStartedAt = time.Now()
	return m, func() tea.Msg {
		err := m.testProvider(profile)
		return settingsProviderTestedMsg{profile: profile, err: err}
	}
}

func (m Model) saveSettingsProfile(activate bool) (tea.Model, tea.Cmd) {
	if m.settingsConfig == nil {
		return m, nil
	}

	profile, err := m.resolveSettingsFormProfile()
	if err != nil {
		m.entries = append(m.entries, Entry{
			Title: "error",
			Body:  fmt.Sprintf("provider settings: %v", err),
		})
		m.selectedEntry = len(m.entries) - 1
		m.clampSelection()
		return m, nil
	}

	m.busy = true
	m.busyStartedAt = time.Now()
	return m, func() tea.Msg {
		if m.loadModels != nil && shouldValidateProviderModel(profile) {
			models, modelErr := m.loadModels(profile)
			if modelErr == nil && len(models) > 0 && !containsModelOption(models, profile.Model) {
				return settingsProviderSavedMsg{
					err: fmt.Errorf("model %q is not in the provider model list; pick a model from Active Model or enter an exact supported ID", profile.Model),
				}
			}
		}
		var persistErr error
		if m.saveProvider != nil {
			persistErr = m.saveProvider(profile)
		}
		shouldActivate := activate || profile.Preset == m.activeProvider.Preset
		if !shouldActivate || m.switchProvider == nil {
			return settingsProviderSavedMsg{
				profile:    profile,
				persistErr: persistErr,
				activated:  false,
			}
		}
		ctrl, switchedProfile, runtimeSelection, err := m.switchProvider(profile, m.currentShellContext())
		return settingsProviderSavedMsg{
			ctrl:       ctrl,
			profile:    switchedProfile,
			runtime:    runtimeSelection,
			err:        err,
			persistErr: persistErr,
			activated:  err == nil,
		}
	}
}

func (m Model) resolveSettingsFormProfile() (provider.Profile, error) {
	if m.settingsConfig == nil {
		return provider.Profile{}, errors.New("settings form is not active")
	}

	return resolveFormProfile(*m.settingsConfig)
}

func (m Model) resolveOnboardingFormProfile() (provider.Profile, error) {
	if m.onboardingForm == nil {
		return provider.Profile{}, errors.New("onboarding form is not active")
	}

	return resolveFormProfile(*m.onboardingForm)
}

func buildOnboardingForm(choice provider.OnboardingCandidate) onboardingFormState {
	profile := choice.Profile
	form := onboardingFormState{
		title:   "Configure " + profile.Name,
		intro:   "Enter the provider settings to save and activate.",
		profile: profile,
	}

	switch profile.Preset {
	case provider.PresetOpenAI, provider.PresetOpenRouter, provider.PresetAnthropic:
		form.fields = []onboardingField{
			{key: "base_url", label: "Base URL", value: profile.BaseURL, required: true},
			{key: "model", label: "Model", value: profile.Model, required: true},
			providerAPIKeyField(profile, true),
		}
	case provider.PresetOpenWebUI:
		form.fields = []onboardingField{
			{key: "base_url", label: "Base URL", value: profile.BaseURL, required: true},
			{key: "model", label: "Model", value: profile.Model, required: true},
			providerAPIKeyField(profile, false),
		}
	case provider.PresetOllama:
		form.fields = []onboardingField{
			{key: "base_url", label: "Base URL", value: profile.BaseURL, required: true},
			{key: "model", label: "Model", value: profile.Model, required: true},
			providerAPIKeyField(profile, false),
		}
	case provider.PresetCustom:
		form.fields = []onboardingField{
			{key: "base_url", label: "Base URL", value: profile.BaseURL, required: true},
			{key: "model", label: "Model", value: profile.Model, required: true},
			providerAPIKeyField(profile, false),
		}
	case provider.PresetCodexCLI:
		form.fields = []onboardingField{
			{key: "cli_command", label: "CLI Command", value: profile.CLICommand, required: true},
			{key: "model", label: "Model", value: profile.Model, placeholder: "optional"},
		}
		form.intro = "Use an installed Codex CLI and the existing local login."
	default:
		form.fields = []onboardingField{
			{key: "model", label: "Model", value: profile.Model},
		}
	}

	return form
}

func resolveFormProfile(form onboardingFormState) (provider.Profile, error) {
	values := map[string]string{}
	for _, field := range form.fields {
		values[field.key] = strings.TrimSpace(field.value)
		if field.required && values[field.key] == "" {
			return provider.Profile{}, fmt.Errorf("%s is required", strings.ToLower(field.label))
		}
	}

	profile := form.profile
	apiKeyValue := values["api_key"]
	cfg := config.Config{
		ProviderType:       string(profile.Preset),
		ProviderAuthMethod: onboardingAuthMethod(profile.Preset, apiKeyValue, profile),
		ProviderBaseURL:    values["base_url"],
		ProviderModel:      values["model"],
		ProviderAPIKey:     apiKeyValue,
		ProviderCLICommand: values["cli_command"],
	}

	resolved, err := provider.ResolveProfile(cfg)
	if err != nil {
		return provider.Profile{}, err
	}
	if apiKeyValue != "" {
		resolved.APIKey = apiKeyValue
		resolved.APIKeyEnvVar = "os_keyring"
	}
	if apiKeyValue == "" && profile.AuthMethod == provider.AuthAPIKey {
		resolved.AuthMethod = provider.AuthAPIKey
		resolved.APIKey = profile.APIKey
		resolved.APIKeyEnvVar = profile.APIKeyEnvVar
	}
	return resolved, nil
}

func providerAPIKeyField(profile provider.Profile, required bool) onboardingField {
	field := onboardingField{
		key:    "api_key",
		label:  "API Key",
		secret: true,
	}

	source := ""
	switch {
	case strings.TrimSpace(profile.APIKeyEnvVar) != "":
		source = strings.TrimSpace(profile.APIKeyEnvVar)
	case strings.TrimSpace(profile.APIKey) != "":
		source = "stored key"
	case profile.AuthMethod == provider.AuthNone:
		source = "none"
	}

	switch source {
	case "":
		if required {
			field.placeholder = "stored in OS keyring"
		} else {
			field.placeholder = "optional"
		}
	default:
		field.placeholder = "leave blank to keep " + source
		required = false
	}
	field.required = required
	return field
}

func onboardingAuthMethod(preset provider.ProviderPreset, apiKey string, existing provider.Profile) string {
	switch preset {
	case provider.PresetOllama, provider.PresetCustom:
		if strings.TrimSpace(apiKey) == "" {
			if existing.AuthMethod == provider.AuthAPIKey {
				return "api_key"
			}
			return "none"
		}
		return "api_key"
	case provider.PresetOpenWebUI:
		if strings.TrimSpace(apiKey) == "" && existing.AuthMethod != provider.AuthAPIKey {
			return "none"
		}
		return "api_key"
	case provider.PresetCodexCLI:
		return "codex_login"
	default:
		return "api_key"
	}
}

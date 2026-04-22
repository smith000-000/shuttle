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
		return m.takeControlPersistentShellNow()
	case tea.KeyF3:
		return m.takeControlExecutionNow()
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

func (m *Model) focusSettingsModelField() {
	if m.settingsConfig == nil {
		return
	}
	for index, field := range m.settingsConfig.fields {
		if field.key == "model" && !field.hidden {
			m.settingsConfig.index = index
			return
		}
	}
}

func (m *Model) toggleSettingsModelListMode() {
	m.focusSettingsModelField()
	if !m.isSettingsModelFieldFocused() {
		return
	}
	if m.settingsModelListActive {
		m.settingsModelListActive = false
		m.settingsModelBrowseAll = false
		m.settingsModelInfo = false
		return
	}
	m.settingsModelListActive = len(m.settingsModelCatalog) > 0
	m.settingsModelBrowseAll = strings.TrimSpace(m.settingsModelFilter) != "" && len(m.settingsModels) <= 1 && len(m.settingsModelCatalog) > 1
	m.applySettingsModelFilter()
	m.settingsModelInfo = false
}

func (m Model) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyF2:
		return m.takeControlPersistentShellNow()
	case tea.KeyF3:
		return m.takeControlExecutionNow()
	case tea.KeyF10:
		m.settingsOpen = false
		m.settingsStep = settingsStepMenu
		m.settingsConfig = nil
		m.settingsModelCatalog = nil
		m.settingsModels = nil
		m.settingsModelFilter = ""
		m.settingsModelInfo = false
		m.settingsRuntimes = nil
		m.settingsRuntimeIdx = 0
		m.settingsRuntimeCommand = ""
		m.settingsRuntimeCommandFocus = false
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
	case tea.KeyF5:
		if m.settingsStep == settingsStepProviderForm && m.isSettingsModelFieldFocused() {
			m.toggleSettingsModelListMode()
			return m, nil
		}
		return m, nil
	case tea.KeyEsc:
		switch m.settingsStep {
		case settingsStepProviderForm:
			if m.isSettingsModelListFocused() {
				m.focusSettingsModelField()
				m.settingsModelListActive = false
				m.settingsModelBrowseAll = false
				m.settingsModelInfo = false
				return m, nil
			}
			m.settingsStep = m.settingsDetailReturnStep
			if m.settingsStep == "" {
				m.settingsStep = settingsStepProviders
			}
			m.settingsConfig = nil
			m.settingsModelCatalog = nil
			m.settingsModels = nil
			m.settingsModelIdx = 0
			m.settingsModelFilter = ""
			m.settingsModelInfo = false
			m.settingsModelListActive = false
			m.settingsModelBrowseAll = false
			m.settingsBanner = ""
		case settingsStepShell:
			m.settingsStep = settingsStepMenu
			m.settingsConfig = nil
			m.settingsBanner = ""
		case settingsStepProviders:
			m.settingsStep = settingsStepMenu
			m.settingsBanner = ""
		case settingsStepRuntime:
			m.settingsStep = settingsStepMenu
			m.settingsRuntimeCommandFocus = false
			m.settingsBanner = ""
		case settingsStepSession:
			m.settingsStep = settingsStepMenu
			m.settingsBanner = ""
		default:
			m.settingsOpen = false
			m.settingsBanner = ""
		}
		return m, nil
	case tea.KeyLeft:
		if m.settingsStep == settingsStepRuntime {
			if m.settingsRuntimeCommandFocus {
				m.settingsRuntimeCommandFocus = false
				m.refreshSettingsRuntimePreview(true)
			}
			return m, nil
		}
		if m.settingsStep == settingsStepProviderForm && m.isSettingsModelListFocused() {
			m.focusSettingsModelField()
			m.settingsModelListActive = false
			m.settingsModelBrowseAll = false
			m.settingsModelInfo = false
			return m, nil
		}
		if m.isSettingsConfigStep() && m.isSettingsChoiceFieldFocused() {
			m.cycleSettingsChoiceField(-1)
			return m, nil
		}
		return m, nil
	case tea.KeyRight:
		if m.settingsStep == settingsStepRuntime {
			m.settingsRuntimeCommandFocus = true
			m.refreshSettingsRuntimePreview(false)
			return m, nil
		}
		if m.settingsStep == settingsStepProviderForm && m.isSettingsModelFieldFocused() {
			if !m.isSettingsModelListFocused() {
				m.toggleSettingsModelListMode()
			}
			return m, nil
		}
		if m.isSettingsConfigStep() && m.isSettingsChoiceFieldFocused() {
			m.cycleSettingsChoiceField(1)
			return m, nil
		}
		return m, nil
	case tea.KeyUp:
		if m.settingsStep == settingsStepRuntime && m.settingsRuntimeCommandFocus {
			return m, nil
		}
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
				m.refreshSettingsRuntimePreview(true)
			}
		case settingsStepProviders:
			if m.settingsProviderIdx > 0 {
				m.settingsProviderIdx--
			}
		case settingsStepShell:
			m.moveSettingsFormSelection(-1)
		case settingsStepProviderForm:
			if m.isSettingsModelListFocused() {
				if m.settingsModelIdx > 0 {
					m.settingsModelIdx--
					m.settingsModelInfo = false
				}
			} else {
				m.moveSettingsFormSelection(-1)
			}
		}
		return m, nil
	case tea.KeyDown:
		if m.settingsStep == settingsStepRuntime && m.settingsRuntimeCommandFocus {
			return m, nil
		}
		switch m.settingsStep {
		case settingsStepMenu:
			if m.settingsIndex < len(settingsMenuEntries())-1 {
				m.settingsIndex++
			}
		case settingsStepSession:
			if m.settingsApprovalIdx < len(settingsApprovalEntries())-1 {
				m.settingsApprovalIdx++
			}
		case settingsStepRuntime:
			if m.settingsRuntimeIdx < len(m.settingsRuntimes)-1 {
				m.settingsRuntimeIdx++
				m.refreshSettingsRuntimePreview(true)
			}
		case settingsStepProviders:
			if m.settingsProviderIdx < len(m.settingsProviders)-1 {
				m.settingsProviderIdx++
			}
		case settingsStepShell:
			m.moveSettingsFormSelection(1)
		case settingsStepProviderForm:
			if m.isSettingsModelListFocused() {
				if m.settingsModelIdx < len(m.settingsModels)-1 {
					m.settingsModelIdx++
					m.settingsModelInfo = false
				}
			} else {
				m.moveSettingsFormSelection(1)
			}
		}
		return m, nil
	case tea.KeyTab:
		if m.settingsStep == settingsStepRuntime {
			m.settingsRuntimeCommandFocus = !m.settingsRuntimeCommandFocus
			m.refreshSettingsRuntimePreview(!m.settingsRuntimeCommandFocus)
			return m, nil
		}
		if m.isSettingsConfigStep() {
			m.moveSettingsFormSelection(1)
		}
		return m, nil
	case tea.KeyPgUp:
		if m.settingsStep == settingsStepProviderForm && m.isSettingsModelListFocused() {
			m.settingsModelIdx -= 8
			if m.settingsModelIdx < 0 {
				m.settingsModelIdx = 0
			}
			m.settingsModelInfo = false
		}
		return m, nil
	case tea.KeyPgDown:
		if m.settingsStep == settingsStepProviderForm && m.isSettingsModelListFocused() {
			m.settingsModelIdx += 8
			if m.settingsModelIdx >= len(m.settingsModels) {
				m.settingsModelIdx = max(0, len(m.settingsModels)-1)
			}
			m.settingsModelInfo = false
		}
		return m, nil
	case tea.KeyBackspace, tea.KeyDelete:
		if m.settingsStep == settingsStepRuntime && m.settingsRuntimeCommandFocus {
			if len(m.settingsRuntimeCommand) > 0 {
				m.settingsRuntimeCommand = m.settingsRuntimeCommand[:len(m.settingsRuntimeCommand)-1]
				m.refreshSettingsRuntimePreview(false)
			}
			return m, nil
		}
		if m.isSettingsConfigStep() {
			if field := m.currentSettingsField(); field != nil && len(field.options) == 0 {
				if len(field.value) > 0 {
					field.value = field.value[:len(field.value)-1]
				}
				if field.key == "model" {
					m.settingsModelListActive = false
					m.syncSettingsModelFilterFromConfig()
				}
			}
		}
		return m, nil
	case tea.KeySpace:
		if m.settingsStep == settingsStepRuntime && m.settingsRuntimeCommandFocus {
			m.settingsRuntimeCommand += " "
			m.refreshSettingsRuntimePreview(false)
			return m, nil
		}
		if m.isSettingsConfigStep() {
			if m.isSettingsChoiceFieldFocused() {
				m.cycleSettingsChoiceField(1)
				return m, nil
			}
			if field := m.currentSettingsField(); field != nil {
				field.value += " "
				if field.key == "model" {
					m.settingsModelListActive = false
					m.syncSettingsModelFilterFromConfig()
				}
			}
		}
		return m, nil
	case tea.KeyEnter:
		if m.settingsStep == settingsStepRuntime {
			return m.applySettingsSelection()
		}
		if m.settingsStep == settingsStepShell && m.isSettingsChoiceFieldFocused() {
			m.cycleSettingsChoiceField(1)
			return m, nil
		}
		if m.settingsStep == settingsStepProviderForm && m.isSettingsModelListFocused() {
			updated, cmd := m.applySettingsSelection()
			if next, ok := updated.(Model); ok {
				next.focusSettingsModelField()
				next.settingsModelListActive = false
				next.settingsModelBrowseAll = false
				next.settingsModelInfo = false
				return next, cmd
			}
			return updated, cmd
		}
		if m.settingsStep == settingsStepProviderForm && m.isSettingsChoiceFieldFocused() && !m.isSettingsModelFieldFocused() {
			m.cycleSettingsChoiceField(1)
			return m, nil
		}
		return m.applySettingsSelection()
	default:
		if m.settingsStep == settingsStepRuntime && m.settingsRuntimeCommandFocus && !msg.Alt && msg.Type == tea.KeyRunes {
			m.settingsRuntimeCommand += string(msg.Runes)
			m.refreshSettingsRuntimePreview(false)
			return m, nil
		}
		if m.settingsStep == settingsStepProviderForm && m.isSettingsModelFieldFocused() && msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 'I' {
			if len(m.settingsModels) > 0 {
				m.settingsModelInfo = !m.settingsModelInfo
			}
			return m, nil
		}
		if m.settingsStep == settingsStepProviderForm && !msg.Alt && msg.Type == tea.KeyRunes {
			if field := m.currentSettingsField(); field != nil && len(field.options) == 0 {
				field.value += string(msg.Runes)
				if field.key == "model" {
					m.settingsModelListActive = false
					m.syncSettingsModelFilterFromConfig()
				}
				return m, nil
			}
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

func (m Model) switchRuntimeSelection(runtimeType string, runtimeCommand string, step settingsStep) (tea.Model, tea.Cmd) {
	shellContext := m.currentShellContext()
	trackedShell := m.ctrl.TrackedShellTarget()
	m.busy = true
	m.busyStartedAt = time.Now()
	return m, func() tea.Msg {
		ctrl, runtimeType, runtimeCommand, err := m.switchRuntime(runtimeType, runtimeCommand, shellContext, trackedShell)
		var persistErr error
		if err == nil && m.saveRuntime != nil {
			persistErr = m.saveRuntime(runtimeType, runtimeCommand)
		}
		return runtimeSwitchedMsg{
			ctrl:           ctrl,
			runtimeType:    runtimeType,
			runtimeCommand: runtimeCommand,
			err:            err,
			persistErr:     persistErr,
			settingsStep:   step,
		}
	}
}

func (m Model) switchProfile(profile provider.Profile, step settingsStep) (tea.Model, tea.Cmd) {
	shellContext := m.currentShellContext()
	trackedShell := m.ctrl.TrackedShellTarget()
	m.busy = true
	m.busyStartedAt = time.Now()
	return m, func() tea.Msg {
		ctrl, profile, err := m.switchProvider(profile, shellContext, trackedShell)
		var persistErr error
		if err == nil && m.saveProvider != nil {
			persistErr = m.saveProvider(profile)
		}
		return providerSwitchedMsg{
			ctrl:         ctrl,
			profile:      profile,
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
		m.appendTranscriptEntries(Entry{
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
			m.settingsApprovalIdx = currentSettingsApprovalIndexForMode(m.approvalMode)
			m.settingsBanner = ""
			return m, nil
		}
		if m.settingsIndex == 1 {
			m.openSettingsRuntimeStep()
			m.settingsBanner = ""
			return m, nil
		}
		if m.settingsIndex == 2 {
			next := m.openSettingsShellStep()
			next.settingsBanner = ""
			return next, nil
		}
		if m.settingsIndex == 3 {
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
		m.setApprovalMode(selected.mode)
		m.settingsApprovalIdx = currentSettingsApprovalIndexForMode(m.approvalMode)
		m.busy = true
		m.busyStartedAt = time.Now()
		return m, func() tea.Msg {
			events, err := m.ctrl.SetApprovalMode(context.Background(), selected.mode)
			return controllerEventsMsg{events: events, err: err}
		}
	case settingsStepRuntime:
		if len(m.settingsRuntimes) == 0 || m.switchRuntime == nil {
			return m, nil
		}
		entry := m.settingsRuntimes[m.settingsRuntimeIdx]
		if entry.disabled {
			return m, nil
		}
		return m.switchRuntimeSelection(entry.runtimeType, strings.TrimSpace(m.settingsRuntimeCommand), settingsStepRuntime)
	case settingsStepShell:
		return m.saveShellSettings()
	case settingsStepProviders:
		if len(m.settingsProviders) == 0 {
			return m, nil
		}
		entry := m.settingsProviders[m.settingsProviderIdx]
		if entry.disabled || entry.candidate == nil {
			return m, nil
		}
		return m.openSettingsProviderDetail(*entry.candidate, settingsStepProviders, false)
	case settingsStepProviderForm:
		if m.isSettingsModelFieldFocused() {
			if len(m.settingsModels) > 0 && m.settingsModelIdx >= 0 && m.settingsModelIdx < len(m.settingsModels) {
				choice := m.settingsModels[m.settingsModelIdx]
				m.settingsConfig.fields[m.settingsConfig.index].value = choice.model.ID
				m.syncSettingsModelFilterFromConfig()
			}
			return m.testSettingsProfile()
		}
		return m.saveSettingsProfile(false)
	default:
		return m, nil
	}
}

func visibleFormFieldIndices(form *onboardingFormState) []int {
	if form == nil {
		return nil
	}
	indexes := make([]int, 0, len(form.fields))
	for index, field := range form.fields {
		if field.hidden {
			continue
		}
		indexes = append(indexes, index)
	}
	return indexes
}

func normalizeFormIndex(form *onboardingFormState) {
	if form == nil {
		return
	}
	visible := visibleFormFieldIndices(form)
	if len(visible) == 0 {
		form.index = 0
		return
	}
	for _, index := range visible {
		if form.index == index {
			return
		}
	}
	form.index = visible[0]
}

func (m *Model) moveSettingsFormSelection(delta int) {
	if m.settingsConfig == nil {
		return
	}
	visible := visibleFormFieldIndices(m.settingsConfig)
	if len(visible) == 0 {
		m.settingsConfig.index = 0
		return
	}
	current := 0
	for i, index := range visible {
		if index == m.settingsConfig.index {
			current = i
			break
		}
	}
	current = (current + delta + len(visible)) % len(visible)
	m.settingsConfig.index = visible[current]
	if m.isSettingsModelFieldFocused() {
		m.settingsModelListActive = false
		m.syncSettingsModelFilterFromConfig()
	}
}

func (m Model) currentSettingsField() *onboardingField {
	if m.settingsConfig == nil || m.settingsConfig.index < 0 || m.settingsConfig.index >= len(m.settingsConfig.fields) {
		return nil
	}
	field := &m.settingsConfig.fields[m.settingsConfig.index]
	if field.hidden {
		return nil
	}
	return field
}

func (m Model) isSettingsConfigStep() bool {
	return m.settingsStep == settingsStepProviderForm || m.settingsStep == settingsStepShell
}

func (m Model) settingsCurrentFieldKey() string {
	field := m.currentSettingsField()
	if field == nil {
		return ""
	}
	return field.key
}

func (m Model) isSettingsChoiceFieldFocused() bool {
	field := m.currentSettingsField()
	return m.isSettingsConfigStep() && field != nil && len(field.options) > 0
}

func (m Model) isSettingsModelFieldFocused() bool {
	return m.settingsStep == settingsStepProviderForm && m.settingsCurrentFieldKey() == "model"
}

func (m Model) isSettingsModelListFocused() bool {
	return m.isSettingsModelFieldFocused() && m.settingsModelListActive && len(m.settingsModels) > 0
}

func (m *Model) cycleSettingsChoiceField(delta int) bool {
	field := m.currentSettingsField()
	if field == nil || len(field.options) == 0 {
		return false
	}
	current := 0
	for index, option := range field.options {
		if option == field.value {
			current = index
			break
		}
	}
	current = (current + delta + len(field.options)) % len(field.options)
	field.value = field.options[current]
	m.applySettingsFormVisibility()
	return true
}

func (m *Model) applySettingsFormVisibility() {
	if m.settingsConfig == nil {
		return
	}
	if m.settingsStep == settingsStepProviderForm {
		applyProviderFormVisibility(m.settingsConfig)
	}
	if m.isSettingsModelFieldFocused() {
		m.settingsModelListActive = false
		m.syncSettingsModelFilterFromConfig()
	}
}

func (m Model) openSettingsShellStep() Model {
	next := m
	next.settingsStep = settingsStepShell
	form := buildShellSettingsForm(next.activeShellProfiles)
	next.settingsConfig = &form
	next.settingsDetailReturnStep = settingsStepMenu
	next.settingsBanner = ""
	return next
}

func (m Model) saveShellSettings() (tea.Model, tea.Cmd) {
	if m.settingsConfig == nil || m.saveShellProfiles == nil {
		return m, nil
	}
	profiles, err := resolveShellSettingsForm(*m.settingsConfig)
	if err != nil {
		m.settingsBanner = fmt.Sprintf("Shell settings are invalid: %v", err)
		return m, nil
	}
	m.busy = true
	m.busyStartedAt = time.Now()
	return m, func() tea.Msg {
		return shellSettingsSavedMsg{
			profiles: profiles,
			err:      m.saveShellProfiles(profiles),
		}
	}
}

func (m *Model) syncSettingsModelFilterFromConfig() {
	if m.settingsConfig == nil {
		m.settingsModelFilter = ""
		return
	}
	for _, field := range m.settingsConfig.fields {
		if field.key == "model" {
			m.settingsModelFilter = strings.TrimSpace(field.value)
			m.settingsConfig.profile.Model = m.settingsModelFilter
			m.applySettingsModelFilter()
			return
		}
	}
	m.settingsModelFilter = ""
}

func (m Model) openSettingsProviderDetail(choice provider.OnboardingCandidate, returnStep settingsStep, focusModel bool) (tea.Model, tea.Cmd) {
	form := buildOnboardingForm(choice)
	next := m
	next.settingsStep = settingsStepProviderForm
	next.settingsConfig = &form
	next.settingsDetailReturnStep = returnStep
	next.settingsModelCatalog = nil
	next.settingsModels = nil
	next.settingsModelIdx = 0
	next.settingsModelInfo = false
	next.settingsModelListActive = false
	next.settingsModelBrowseAll = false
	next.settingsBanner = ""
	next.applySettingsFormVisibility()
	next.syncSettingsModelFilterFromConfig()
	if focusModel {
		for index, field := range next.settingsConfig.fields {
			if field.key == "model" {
				next.settingsConfig.index = index
				break
			}
		}
	}
	profile, err := next.resolveSettingsFormProfile()
	if err != nil || next.loadModels == nil {
		return next, nil
	}
	return next.loadSettingsModelsForProfile(profile)
}

func (m Model) loadSettingsModelsForProfile(profile provider.Profile) (tea.Model, tea.Cmd) {
	if m.loadModels == nil {
		return m, nil
	}

	m.busy = true
	m.busyStartedAt = time.Now()
	return m, func() tea.Msg {
		if m.testProvider != nil {
			if err := m.testProvider(profile); err != nil {
				return settingsModelsLoadedMsg{profile: profile, err: err}
			}
		}
		models, err := m.loadModels(profile)
		if err != nil {
			return settingsModelsLoadedMsg{profile: profile, err: err}
		}
		if strings.TrimSpace(profile.Model) != "" && !containsModelOption(models, profile.Model) {
			models = append([]provider.ModelOption{{
				ID:          profile.Model,
				Description: "currently saved model",
			}}, models...)
		}
		choices := make([]settingsModelChoice, 0, len(models))
		for _, model := range models {
			choices = append(choices, settingsModelChoice{profile: profile, model: model})
		}
		return settingsModelsLoadedMsg{profile: profile, choices: choices}
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
		m.appendTranscriptEntries(Entry{
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
		ctrl, switchedProfile, err := m.switchProvider(profile, m.currentShellContext(), m.ctrl.TrackedShellTarget())
		return settingsProviderSavedMsg{
			ctrl:       ctrl,
			profile:    switchedProfile,
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

func providerThinkingField(profile provider.Profile) onboardingField {
	value := strings.TrimSpace(profile.Thinking)
	if value == "" {
		value = string(provider.DefaultThinkingMode(profile))
	}
	return onboardingField{
		key:     "thinking",
		label:   "Thinking",
		value:   value,
		options: []string{"on", "off"},
	}
}

func providerReasoningEffortField(profile provider.Profile) onboardingField {
	value := strings.TrimSpace(profile.ReasoningEffort)
	if value == "" {
		value = string(provider.NormalizeReasoningEffort(""))
	}
	return onboardingField{
		key:     "reasoning_effort",
		label:   "Reasoning Effort",
		value:   value,
		options: []string{"low", "medium", "high", "xhigh"},
	}
}

func formFieldValue(form onboardingFormState, key string) string {
	for _, field := range form.fields {
		if field.key == key {
			return strings.TrimSpace(field.value)
		}
	}
	return ""
}

func applyProviderFormVisibility(form *onboardingFormState) {
	if form == nil {
		return
	}
	if provider.SupportsThinking(form.profile) {
		form.profile.Thinking = formFieldValue(*form, "thinking")
	}
	if provider.SupportsReasoningEffort(form.profile) {
		form.profile.ReasoningEffort = formFieldValue(*form, "reasoning_effort")
	}
	showEffort := strings.EqualFold(formFieldValue(*form, "thinking"), string(provider.ThinkingOn)) && provider.SupportsReasoningEffort(form.profile)
	for index := range form.fields {
		if form.fields[index].key == "reasoning_effort" {
			form.fields[index].hidden = !showEffort
		}
	}
	normalizeFormIndex(form)
}

func buildOnboardingForm(choice provider.OnboardingCandidate) onboardingFormState {
	profile := choice.Profile
	descriptor := provider.DescriptorForPreset(profile.Preset)
	form := onboardingFormState{
		title:   "Configure " + profile.Name,
		intro:   "Enter the provider settings to save and activate.",
		profile: profile,
	}
	if strings.TrimSpace(descriptor.OnboardingIntro) != "" {
		form.intro = descriptor.OnboardingIntro
	}
	form.fields = make([]onboardingField, 0, len(descriptor.OnboardingFields))
	for _, fieldKind := range descriptor.OnboardingFields {
		switch fieldKind {
		case provider.OnboardingFieldBaseURL:
			form.fields = append(form.fields, onboardingField{key: "base_url", label: "Base URL", value: profile.BaseURL, required: true})
		case provider.OnboardingFieldModel:
			required, placeholder := provider.ModelFieldConfig(profile.Preset)
			field := onboardingField{key: "model", label: "Model", value: profile.Model, required: required, placeholder: placeholder}
			form.fields = append(form.fields, field)
		case provider.OnboardingFieldThinking:
			form.fields = append(form.fields, providerThinkingField(profile))
		case provider.OnboardingFieldReasoningEffort:
			form.fields = append(form.fields, providerReasoningEffortField(profile))
		case provider.OnboardingFieldAPIKey:
			form.fields = append(form.fields, providerAPIKeyField(profile, descriptor.RequiredAPIKeyByDefault))
		case provider.OnboardingFieldCLICommand:
			form.fields = append(form.fields, onboardingField{key: "cli_command", label: "CLI Command", value: profile.CLICommand, required: true})
		}
	}
	if len(form.fields) == 0 {
		form.fields = []onboardingField{{key: "model", label: "Model", value: profile.Model}}
	}

	applyProviderFormVisibility(&form)
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
		ProviderType:            string(profile.Preset),
		ProviderAuthMethod:      onboardingAuthMethod(profile.Preset, apiKeyValue, profile),
		ProviderBaseURL:         values["base_url"],
		ProviderModel:           values["model"],
		ProviderThinking:        values["thinking"],
		ProviderReasoningEffort: values["reasoning_effort"],
		ProviderAPIKey:          apiKeyValue,
		ProviderCLICommand:      values["cli_command"],
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
	return string(provider.ResolveOnboardingAuthMethod(preset, apiKey, existing))
}

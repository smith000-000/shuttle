package tui

import (
	"strings"

	"aiterm/internal/provider"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleSettingsMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m, nil
	case tea.MouseButtonWheelDown:
		return m, nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || msg.Y < 0 {
		return m, nil
	}

	switch m.settingsStep {
	case settingsStepMenu:
		if index, ok := m.settingsMenuIndexAtMouse(msg.Y); ok {
			m.settingsIndex = index
			return m.applySettingsSelection()
		}
	case settingsStepSession:
		if index, ok := m.settingsApprovalIndexAtMouse(msg.Y); ok {
			m.settingsApprovalIdx = index
			return m.applySettingsSelection()
		}
	case settingsStepProviders:
		if index, ok := m.settingsProviderIndexAtMouse(msg.Y); ok {
			m.settingsProviderIdx = index
			return m.applySettingsSelection()
		}
	case settingsStepProviderForm:
		if index, ok := m.settingsConfigFieldIndexAtMouse(msg.Y); ok {
			m.settingsConfig.index = index
			if m.isSettingsModelFieldFocused() {
				m.syncSettingsModelFilterFromConfig()
			}
			return m, nil
		}
		if index, ok := m.settingsModelIndexAtMouse(msg.Y); ok {
			if m.settingsConfig == nil || index < 0 || index >= len(m.settingsModels) {
				return m, nil
			}
			modelField := settingsFormFieldIndexByKey(m.settingsConfig, "model")
			if modelField >= 0 {
				m.settingsConfig.index = modelField
				m.settingsModelIdx = index
				m.settingsConfig.fields[modelField].value = m.settingsModels[index].model.ID
				m.syncSettingsModelFilterFromConfig()
				return m.testSettingsProfile()
			}
		}
	}
	return m, nil
}

func (m Model) settingsHeaderLineCount() int {
	count := 2
	if strings.TrimSpace(m.settingsBanner) != "" {
		count += 2
	}
	return count
}

func (m Model) settingsMenuIndexAtMouse(y int) (int, bool) {
	line := m.settingsHeaderLineCount() + 2
	for index := range settingsMenuEntries() {
		if y == line {
			return index, true
		}
		line++
	}
	return 0, false
}

func (m Model) settingsApprovalIndexAtMouse(y int) (int, bool) {
	line := m.settingsHeaderLineCount() + 2
	for index := range settingsApprovalEntries() {
		start := line
		line += 2
		if y >= start && y < line {
			return index, true
		}
	}
	return 0, false
}

func (m Model) settingsProviderIndexAtMouse(y int) (int, bool) {
	width := m.width
	if width <= 0 {
		width = 100
	}
	contentWidth := m.contentWidthFor(width, m.styles.detail)
	line := m.settingsHeaderLineCount() + 2
	for index, entry := range m.settingsProviders {
		start := line
		line++
		if entry.detail != "" {
			line += len(wrapParagraphs(entry.detail, max(10, contentWidth-2)))
		}
		if entry.candidate != nil {
			line++
		}
		if y >= start && y < line {
			return index, true
		}
	}
	return 0, false
}

func settingsFormFieldIndexByKey(form *onboardingFormState, key string) int {
	if form == nil {
		return -1
	}
	for index, field := range form.fields {
		if field.key == key {
			return index
		}
	}
	return -1
}

func (m Model) settingsConfigFieldIndexAtMouse(y int) (int, bool) {
	if m.settingsConfig == nil {
		return 0, false
	}
	line := m.settingsHeaderLineCount() + 2
	line++
	for index, field := range m.settingsConfig.fields {
		if field.hidden {
			continue
		}
		if y == line {
			return index, true
		}
		line++
	}
	return 0, false
}

func (m Model) settingsModelIndexAtMouse(y int) (int, bool) {
	if m.settingsConfig == nil || len(m.settingsModels) == 0 {
		return 0, false
	}
	width := m.width
	if width <= 0 {
		width = 100
	}
	contentWidth := m.contentWidthFor(width, m.styles.detail)
	line := m.settingsHeaderLineCount() + 2
	line++
	for _, field := range m.settingsConfig.fields {
		if field.hidden {
			continue
		}
		line++
	}
	line += 2
	line += 2
	line++
	if settingsModelChoicesContainPreset(m.settingsModelCatalog, provider.PresetCodexCLI) {
		line++
	}
	start, end := onboardingModelWindow(len(m.settingsModels), m.settingsModelIdx, 12)
	for index := start; index < end; index++ {
		choice := m.settingsModels[index]
		rowStart := line
		line++
		detail := modelSummaryLine(choice.model)
		if detail != "" {
			line += len(wrapParagraphs(detail, max(10, contentWidth-2)))
		}
		if index == m.settingsModelIdx && m.settingsModelInfo {
			for _, extra := range modelExtraDetailLines(choice.model) {
				line += len(wrapParagraphs(extra, max(10, contentWidth-2)))
			}
		}
		if y >= rowStart && y < line {
			return index, true
		}
	}
	return 0, false
}

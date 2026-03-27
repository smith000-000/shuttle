package tui

import (
	"fmt"
	"strings"
	"time"

	"aiterm/internal/controller"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) pollRuntimeActivityCmd() tea.Cmd {
	if m.ctrl == nil {
		return nil
	}
	return func() tea.Msg {
		return runtimeActivityMsg{snapshot: m.ctrl.RuntimeActivity()}
	}
}

func (m Model) toggleRuntimeActivity() (tea.Model, tea.Cmd) {
	if m.activityOpen {
		m.activityOpen = false
		m.activityScroll = 0
		return m, nil
	}
	m.helpOpen = false
	m.settingsOpen = false
	m.onboardingOpen = false
	m.detailOpen = false
	m.activityOpen = !m.activityOpen
	m.activityScroll = 0
	m.clampActivityScroll()
	return m, m.pollRuntimeActivityCmd()
}

func (m Model) updateActivity(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyF2:
		return m.takeControlNow()
	case tea.KeyF3, tea.KeyEsc:
		m.activityOpen = false
		m.activityScroll = 0
		return m, nil
	case tea.KeyUp:
		if m.activityScroll > 0 {
			m.activityScroll--
		}
		return m, nil
	case tea.KeyDown:
		m.activityScroll++
		m.clampActivityScroll()
		return m, nil
	case tea.KeyPgUp:
		m.activityScroll -= m.activityPageSize()
		m.clampActivityScroll()
		return m, nil
	case tea.KeyPgDown:
		m.activityScroll += m.activityPageSize()
		m.clampActivityScroll()
		return m, nil
	case tea.KeyHome:
		m.activityScroll = 0
		return m, nil
	case tea.KeyEnd:
		m.activityScroll = m.maxActivityScroll()
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) renderActivityFooter(width int) string {
	left := "Esc close  Up/Down scroll  PgUp/PgDn page"
	right := m.activityScrollIndicator()
	if right == "" {
		return left
	}
	padding := width - lipgloss.Width(left) - lipgloss.Width(right)
	if padding < 1 {
		return left + " " + right
	}
	return left + strings.Repeat(" ", padding) + right
}

func (m Model) activityScrollIndicator() string {
	maxScroll := m.maxActivityScroll()
	if maxScroll <= 0 {
		return ""
	}
	switch {
	case m.activityScroll <= 0:
		return "↓"
	case m.activityScroll >= maxScroll:
		return "↑"
	default:
		return "↑↓"
	}
}

func runtimeActivityLines(snapshot controller.RuntimeActivitySnapshot, width int) []string {
	lines := []string{}
	if snapshot.StartedAt.IsZero() {
		return []string{"No external-agent activity has been captured yet."}
	}
	lines = append(lines, fmt.Sprintf("Started: %s", snapshot.StartedAt.Local().Format(time.RFC822)))
	if !snapshot.UpdatedAt.IsZero() {
		lines = append(lines, fmt.Sprintf("Last update: %s", snapshot.UpdatedAt.Local().Format(time.RFC822)))
	}
	if len(snapshot.Items) == 0 {
		lines = append(lines, "", "Waiting for activity...")
		return lines
	}
	for _, item := range snapshot.Items {
		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = strings.TrimSpace(item.Kind)
		}
		if title == "" {
			title = "activity"
		}
		prefix := title
		if item.Done {
			prefix += " [done]"
		}
		lines = append(lines, "")
		lines = append(lines, prefix)
		body := strings.TrimSpace(item.Body)
		if body == "" {
			body = strings.TrimSpace(item.Detail)
		}
		if body == "" {
			continue
		}
		for _, line := range wrapParagraphs(body, width) {
			lines = append(lines, "> "+line)
		}
	}
	return lines
}

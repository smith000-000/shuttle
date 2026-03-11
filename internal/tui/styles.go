package tui

import "github.com/charmbracelet/lipgloss"

type styles struct {
	screen              lipgloss.Style
	header              lipgloss.Style
	headerRule          lipgloss.Style
	headerTitle         lipgloss.Style
	headerMeta          lipgloss.Style
	modeShell           lipgloss.Style
	modeAgent           lipgloss.Style
	modeBusy            lipgloss.Style
	modeApproval        lipgloss.Style
	modeProposal        lipgloss.Style
	transcript          lipgloss.Style
	actionCard          lipgloss.Style
	actionTitle         lipgloss.Style
	actionBody          lipgloss.Style
	detail              lipgloss.Style
	detailTitle         lipgloss.Style
	detailMeta          lipgloss.Style
	detailBody          lipgloss.Style
	composer            lipgloss.Style
	composerShell       lipgloss.Style
	composerAgent       lipgloss.Style
	composerRefine      lipgloss.Style
	composerBadgeShell  lipgloss.Style
	composerBadgeAgent  lipgloss.Style
	composerBadgeRefine lipgloss.Style
	input               lipgloss.Style
	footer              lipgloss.Style
	tagSystem           lipgloss.Style
	tagShell            lipgloss.Style
	tagResult           lipgloss.Style
	tagAgent            lipgloss.Style
	tagError            lipgloss.Style
	body                lipgloss.Style
}

func newStyles() styles {
	return styles{
		screen: lipgloss.NewStyle().
			Padding(0),
		header: lipgloss.NewStyle().
			Padding(0, 1),
		headerRule: lipgloss.NewStyle().
			Foreground(lipgloss.Color("62")),
		headerTitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("62")).
			Bold(true).
			Padding(0, 1),
		headerMeta: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")),
		modeShell: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("30")).
			Bold(true).
			Padding(0, 1),
		modeAgent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("166")).
			Bold(true).
			Padding(0, 1),
		modeBusy: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("160")).
			Bold(true).
			Padding(0, 1),
		modeApproval: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("160")).
			Bold(true).
			Padding(0, 1),
		modeProposal: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("31")).
			Bold(true).
			Padding(0, 1),
		transcript: lipgloss.NewStyle().
			Padding(0),
		actionCard: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("160")).
			Padding(0, 1),
		actionTitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Bold(true),
		actionBody: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")),
		detail: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1),
		detailTitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Bold(true),
		detailMeta: lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")),
		detailBody: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")),
		composer: lipgloss.NewStyle().
			Padding(0, 1),
		composerShell: lipgloss.NewStyle().
			Background(lipgloss.Color("24")).
			Foreground(lipgloss.Color("255")).
			Padding(0, 1),
		composerAgent: lipgloss.NewStyle().
			Background(lipgloss.Color("94")).
			Foreground(lipgloss.Color("255")).
			Padding(0, 1),
		composerRefine: lipgloss.NewStyle().
			Background(lipgloss.Color("100")).
			Foreground(lipgloss.Color("255")).
			Padding(0, 1),
		composerBadgeShell: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("30")).
			Bold(true).
			Padding(0, 1),
		composerBadgeAgent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("166")).
			Bold(true).
			Padding(0, 1),
		composerBadgeRefine: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("136")).
			Bold(true).
			Padding(0, 1),
		input: lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")),
		footer: lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")),
		tagSystem: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("63")).
			Bold(true).
			Padding(0, 1),
		tagShell: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("30")).
			Bold(true).
			Padding(0, 1),
		tagResult: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("64")).
			Bold(true).
			Padding(0, 1),
		tagAgent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("166")).
			Bold(true).
			Padding(0, 1),
		tagError: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("160")).
			Bold(true).
			Padding(0, 1),
		body: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")),
	}
}

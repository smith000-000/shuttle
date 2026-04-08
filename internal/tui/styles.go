package tui

import "github.com/charmbracelet/lipgloss"

type styles struct {
	screen                lipgloss.Style
	header                lipgloss.Style
	headerRule            lipgloss.Style
	headerTitle           lipgloss.Style
	headerMeta            lipgloss.Style
	modeShell             lipgloss.Style
	modeAgent             lipgloss.Style
	modeBusy              lipgloss.Style
	modeApproval          lipgloss.Style
	modeProposal          lipgloss.Style
	transcript            lipgloss.Style
	transcriptSelected    lipgloss.Style
	actionCard            lipgloss.Style
	actionTitle           lipgloss.Style
	actionBody            lipgloss.Style
	detail                lipgloss.Style
	detailTitle           lipgloss.Style
	detailMeta            lipgloss.Style
	detailBody            lipgloss.Style
	detailSelected        lipgloss.Style
	detailCurrent         lipgloss.Style
	detailSelectedCurrent lipgloss.Style
	detailDisabled        lipgloss.Style
	composer              lipgloss.Style
	composerShell         lipgloss.Style
	composerAgent         lipgloss.Style
	composerRefine        lipgloss.Style
	composerPromptShell   lipgloss.Style
	composerPromptAgent   lipgloss.Style
	composerPromptRefine  lipgloss.Style
	input                 lipgloss.Style
	inputGhost            lipgloss.Style
	status                lipgloss.Style
	statusMuted           lipgloss.Style
	statusBusy            lipgloss.Style
	statusConfirm         lipgloss.Style
	statusWarn            lipgloss.Style
	statusDanger          lipgloss.Style
	statusRoot            lipgloss.Style
	statusRemote          lipgloss.Style
	tail                  lipgloss.Style
	tailLabel             lipgloss.Style
	tailBody              lipgloss.Style
	tailHint              lipgloss.Style
	footer                lipgloss.Style
	tagSystem             lipgloss.Style
	tagShell              lipgloss.Style
	tagResult             lipgloss.Style
	tagAgent              lipgloss.Style
	tagError              lipgloss.Style
	bodySystem            lipgloss.Style
	bodyShell             lipgloss.Style
	bodyResult            lipgloss.Style
	bodyAgent             lipgloss.Style
	bodyError             lipgloss.Style
	glyphSystem           lipgloss.Style
	glyphShell            lipgloss.Style
	glyphResult           lipgloss.Style
	glyphAgent            lipgloss.Style
	glyphError            lipgloss.Style
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
		transcriptSelected: lipgloss.NewStyle().
			Background(lipgloss.Color("236")),
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
		detailSelected: lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Bold(true),
		detailCurrent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("151")).
			Bold(true),
		detailSelectedCurrent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Bold(true),
		detailDisabled: lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")),
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
		composerPromptShell: lipgloss.NewStyle().
			Foreground(lipgloss.Color("232")).
			Background(lipgloss.Color("250")).
			Bold(true).
			Padding(0, 1),
		composerPromptAgent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("232")).
			Background(lipgloss.Color("250")).
			Bold(true).
			Padding(0, 1),
		composerPromptRefine: lipgloss.NewStyle().
			Foreground(lipgloss.Color("232")).
			Background(lipgloss.Color("250")).
			Bold(true).
			Padding(0, 1),
		input: lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")),
		inputGhost: lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")),
		status: lipgloss.NewStyle().
			Foreground(lipgloss.Color("246")).
			Padding(0, 1),
		statusMuted: lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")),
		statusBusy: lipgloss.NewStyle().
			Foreground(lipgloss.Color("81")).
			Bold(true),
		statusConfirm: lipgloss.NewStyle().
			Foreground(lipgloss.Color("78")).
			Bold(true),
		statusWarn: lipgloss.NewStyle().
			Foreground(lipgloss.Color("221")).
			Bold(true),
		statusDanger: lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")).
			Bold(true),
		statusRoot: lipgloss.NewStyle().
			Foreground(lipgloss.Color("202")).
			Bold(true),
		statusRemote: lipgloss.NewStyle().
			Foreground(lipgloss.Color("75")).
			Bold(true),
		tail: lipgloss.NewStyle().
			Foreground(lipgloss.Color("248")).
			Padding(0, 1),
		tailLabel: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("238")).
			Bold(true).
			Padding(0, 1),
		tailBody: lipgloss.NewStyle().
			Foreground(lipgloss.Color("248")),
		tailHint: lipgloss.NewStyle().
			Foreground(lipgloss.Color("221")),
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
		bodySystem: lipgloss.NewStyle().
			Foreground(lipgloss.Color("246")),
		bodyShell: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")),
		bodyResult: lipgloss.NewStyle().
			Foreground(lipgloss.Color("151")),
		bodyAgent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")),
		bodyError: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")),
		glyphSystem: lipgloss.NewStyle().
			Foreground(lipgloss.Color("81")).
			Bold(true),
		glyphShell: lipgloss.NewStyle().
			Foreground(lipgloss.Color("111")).
			Bold(true),
		glyphResult: lipgloss.NewStyle().
			Foreground(lipgloss.Color("114")).
			Bold(true),
		glyphAgent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("221")).
			Bold(true),
		glyphError: lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")).
			Bold(true),
	}
}

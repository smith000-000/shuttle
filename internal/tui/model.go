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
	"aiterm/internal/shell"
	"aiterm/internal/tmux"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

type Mode string

const (
	AgentMode Mode = "AGENT"
	ShellMode Mode = "SHELL"
)

type Entry struct {
	Title  string
	Body   string
	Detail string
}

type controllerEventsMsg struct {
	events []controller.TranscriptEvent
	err    error
}

type busyTickMsg time.Time

type refreshedShellContextMsg struct {
	context *shell.PromptContext
	err     error
}

type shellTailMsg struct {
	tail string
	err  error
}

type providerSwitchedMsg struct {
	ctrl       controller.Controller
	profile    provider.Profile
	err        error
	persistErr error
}

type providerModelsLoadedMsg struct {
	candidate provider.OnboardingCandidate
	models    []provider.ModelOption
	err       error
}

type onboardingStep string

const (
	onboardingStepProviders onboardingStep = "providers"
	onboardingStepConfig    onboardingStep = "config"
	onboardingStepModels    onboardingStep = "models"
)

type onboardingField struct {
	key         string
	label       string
	value       string
	placeholder string
	required    bool
	secret      bool
}

type onboardingFormState struct {
	title   string
	intro   string
	profile provider.Profile
	fields  []onboardingField
	index   int
}

const (
	agentTurnTimeout     = 60 * time.Second
	shellTailPollLines   = 40
	shellTailPollTimeout = 750 * time.Millisecond
)

type Model struct {
	workspace          tmux.Workspace
	ctrl               controller.Controller
	mode               Mode
	input              string
	entries            []Entry
	selectedEntry      int
	width              int
	height             int
	busy               bool
	busyStartedAt      time.Time
	transcriptScroll   int
	transcriptFollow   bool
	detailOpen         bool
	detailScroll       int
	shellHistory       composerHistory
	agentHistory       composerHistory
	activePlan         *controller.ActivePlan
	shellContext       shell.PromptContext
	pendingApproval    *controller.ApprovalRequest
	pendingProposal    *controller.ProposalPayload
	refiningApproval   *controller.ApprovalRequest
	approvalInFlight   bool
	proposalRunPending bool
	directShellPending bool
	inFlightCancel     context.CancelFunc
	suppressCancelErr  bool
	resumeAfterHandoff bool
	takeControl        takeControlConfig
	liveShellTail      string
	showShellTail      bool
	activeProvider     provider.Profile
	onboardingOpen     bool
	onboardingStep     onboardingStep
	onboardingIndex    int
	onboardingChoices  []provider.OnboardingCandidate
	onboardingSelected *provider.OnboardingCandidate
	onboardingForm     *onboardingFormState
	onboardingModels   []provider.ModelOption
	onboardingModelIdx int
	loadOnboarding     func() ([]provider.OnboardingCandidate, error)
	loadModels         func(provider.Profile) ([]provider.ModelOption, error)
	switchProvider     func(provider.Profile, *shell.PromptContext) (controller.Controller, provider.Profile, error)
	saveProvider       func(provider.Profile) error
	lastModelInfo      *controller.AgentModelInfo
	styles             styles
}

func NewModel(workspace tmux.Workspace, ctrl controller.Controller) Model {
	return Model{
		workspace:        workspace,
		ctrl:             ctrl,
		mode:             ShellMode,
		transcriptFollow: true,
		entries: []Entry{
			{
				Title: "system",
				Body:  fmt.Sprintf("Workspace ready. Top pane: %s. Bottom pane TUI is active.", workspace.TopPane.ID),
			},
			{
				Title: "system",
				Body:  "Tab mode. Up/Down history. PgUp/PgDn scroll. Enter submit. F2 take control. F3 providers. /onboard opens provider onboarding. Esc clear. Ctrl+C quit.",
			},
		},
		selectedEntry: 1,
		styles:        newStyles(),
	}
}

func (m Model) WithShellContext(promptContext shell.PromptContext) Model {
	if promptContext.PromptLine() != "" {
		m.shellContext = promptContext
	}

	return m
}

func (m Model) WithTakeControl(socketName string, sessionName string, topPaneID string, detachKey string) Model {
	m.takeControl = takeControlConfig{
		SocketName:  socketName,
		SessionName: sessionName,
		TopPaneID:   topPaneID,
		DetachKey:   detachKey,
	}

	return m
}

func (m Model) WithProviderOnboarding(
	active provider.Profile,
	load func() ([]provider.OnboardingCandidate, error),
	loadModels func(provider.Profile) ([]provider.ModelOption, error),
	switcher func(provider.Profile, *shell.PromptContext) (controller.Controller, provider.Profile, error),
	saver func(provider.Profile) error,
) Model {
	m.activeProvider = active
	m.loadOnboarding = load
	m.loadModels = loadModels
	m.switchProvider = switcher
	m.saveProvider = saver
	return m
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampTranscriptScroll()
		m.clampSelection()
		m.clampDetailScroll()
		return m, nil
	case controllerEventsMsg:
		pinned := m.isTranscriptPinned()
		autoContinue := m.shouldAutoContinue(msg.events)
		m.busy = false
		m.busyStartedAt = time.Time{}
		m.inFlightCancel = nil
		if len(msg.events) > 0 {
			m.entries = append(m.entries, eventsToEntries(msg.events, !m.directShellPending)...)
			m.syncActionState(msg.events)
			if pinned {
				m.scrollTranscriptToBottom()
			} else {
				m.clampTranscriptScroll()
			}
			m.selectedEntry = len(m.entries) - 1
			m.clampSelection()
		}
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				m.suppressCancelErr = false
				return m, nil
			}
			m.suppressCancelErr = false
			m.approvalInFlight = false
			m.proposalRunPending = false
			m.directShellPending = false
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  m.formatShellError(msg.err),
			})
			if pinned {
				m.scrollTranscriptToBottom()
			} else {
				m.clampTranscriptScroll()
			}
			m.selectedEntry = len(m.entries) - 1
			m.clampSelection()
			return m, m.pollShellTailCmd()
		}

		m.suppressCancelErr = false
		if containsEventKind(msg.events, controller.EventError) {
			return m, m.pollShellTailCmd()
		}
		if autoContinue {
			m.busy = true
			m.busyStartedAt = time.Now()
			m.showShellTail = false
			m.liveShellTail = ""
			ctx, cancel := context.WithTimeout(context.Background(), agentTurnTimeout)
			m.inFlightCancel = cancel
			return m, tea.Batch(func() tea.Msg {
				defer cancel()

				events, err := m.ctrl.ContinueAfterCommand(ctx)
				return controllerEventsMsg{
					events: events,
					err:    err,
				}
			}, tickBusy(), m.pollShellTailCmd())
		}
		return m, m.pollShellTailCmd()
	case takeControlFinishedMsg:
		if msg.err != nil {
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  msg.err.Error(),
			})
			m.selectedEntry = len(m.entries) - 1
			m.clampSelection()
			return m, nil
		}
		if m.resumeAfterHandoff && m.ctrl != nil {
			m.resumeAfterHandoff = false
			m.busy = true
			m.busyStartedAt = time.Now()
			m.showShellTail = false
			m.liveShellTail = ""
			ctx, cancel := context.WithTimeout(context.Background(), agentTurnTimeout)
			m.inFlightCancel = cancel
			return m, tea.Batch(func() tea.Msg {
				defer cancel()

				events, err := m.ctrl.ResumeAfterTakeControl(ctx)
				return controllerEventsMsg{
					events: events,
					err:    err,
				}
			}, tickBusy(), m.refreshShellContextCmd(), m.pollShellTailCmd())
		}
		return m, tea.Batch(m.refreshShellContextCmd(), m.pollShellTailCmd())
	case refreshedShellContextMsg:
		if msg.context != nil {
			m.shellContext = *msg.context
		}
		return m, nil
	case shellTailMsg:
		if msg.err == nil {
			m.liveShellTail = msg.tail
		}
		return m, nil
	case providerSwitchedMsg:
		m.busy = false
		m.busyStartedAt = time.Time{}
		m.inFlightCancel = nil
		if msg.err != nil {
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  fmt.Sprintf("switch provider: %v", msg.err),
			})
			m.selectedEntry = len(m.entries) - 1
			m.clampSelection()
			return m, nil
		}

		m.ctrl = msg.ctrl
		m.activeProvider = msg.profile
		m.onboardingOpen = false
		m.onboardingStep = onboardingStepProviders
		m.onboardingIndex = 0
		m.onboardingChoices = nil
		m.onboardingSelected = nil
		m.onboardingForm = nil
		m.onboardingModels = nil
		m.onboardingModelIdx = 0
		m.pendingApproval = nil
		m.pendingProposal = nil
		m.refiningApproval = nil
		m.activePlan = nil
		m.lastModelInfo = nil
		m.approvalInFlight = false
		m.proposalRunPending = false
		m.directShellPending = false
		m.entries = append(m.entries, Entry{
			Title: "system",
			Body:  fmt.Sprintf("Provider switched to %s (%s, %s, auth %s).", msg.profile.Name, msg.profile.Preset, msg.profile.Model, providerAuthSourceLabel(msg.profile)),
		})
		if msg.persistErr != nil {
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  fmt.Sprintf("save provider config: %v", msg.persistErr),
			})
		}
		m.selectedEntry = len(m.entries) - 1
		m.clampSelection()
		return m, nil
	case providerModelsLoadedMsg:
		m.busy = false
		m.busyStartedAt = time.Time{}
		m.inFlightCancel = nil
		if msg.err != nil {
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  fmt.Sprintf("load models for %s: %v", msg.candidate.Profile.Name, msg.err),
			})
			m.selectedEntry = len(m.entries) - 1
			m.clampSelection()
			return m, nil
		}
		m.onboardingStep = onboardingStepModels
		candidate := msg.candidate
		m.onboardingSelected = &candidate
		m.onboardingModels = append([]provider.ModelOption(nil), msg.models...)
		m.onboardingModelIdx = m.currentProviderModelIndex()
		return m, nil
	case busyTickMsg:
		if !m.busy {
			return m, nil
		}

		return m, tea.Batch(tickBusy(), m.pollShellTailCmd())
	case tea.KeyMsg:
		if m.onboardingOpen {
			return m.updateOnboarding(msg)
		}
		if m.detailOpen {
			return m.updateDetail(msg)
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyF2:
			return m.takeControlNow()
		case tea.KeyF3:
			return m.openOnboarding()
		case tea.KeyEsc:
			m.input = ""
			return m, nil
		case tea.KeyTab:
			m.currentHistory().reset()
			if m.mode == ShellMode {
				m.mode = AgentMode
			} else {
				m.mode = ShellMode
			}
			return m, nil
		case tea.KeyUp:
			if msg.Alt {
				m.selectPreviousEntry()
				return m, nil
			}
			m.input = m.currentHistory().previous(m.input)
			return m, nil
		case tea.KeyDown:
			if msg.Alt {
				m.selectNextEntry()
				return m, nil
			}
			m.input = m.currentHistory().next(m.input)
			return m, nil
		case tea.KeyPgUp:
			m.scrollTranscriptBy(-m.pageScrollSize())
			return m, nil
		case tea.KeyPgDown:
			m.scrollTranscriptBy(m.pageScrollSize())
			return m, nil
		case tea.KeyHome:
			m.scrollTranscriptToTop()
			return m, nil
		case tea.KeyEnd:
			m.scrollTranscriptToBottom()
			return m, nil
		case tea.KeyCtrlU:
			m.scrollTranscriptBy(-m.halfPageScrollSize())
			return m, nil
		case tea.KeyCtrlD:
			m.scrollTranscriptBy(m.halfPageScrollSize())
			return m, nil
		case tea.KeyCtrlO:
			return m.openDetail()
		case tea.KeyCtrlG:
			return m.primaryAction()
		case tea.KeyCtrlJ:
			m.input += "\n"
			return m, nil
		case tea.KeyCtrlE:
			return m.primaryAction()
		case tea.KeyCtrlY:
			return m.primaryAction()
		case tea.KeyCtrlN:
			return m.decideApproval(controller.DecisionReject)
		case tea.KeyCtrlR:
			return m.refineApproval()
		case tea.KeyEnter:
			return m.submit()
		case tea.KeyBackspace, tea.KeyDelete:
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
			return m, nil
		case tea.KeySpace:
			m.input += " "
			return m, nil
		default:
			if !msg.Alt && msg.Type == tea.KeyRunes {
				m.input += string(msg.Runes)
				return m, nil
			}
		}
	}

	return m, nil
}

func (m Model) View() string {
	if m.onboardingOpen {
		return m.renderOnboardingView()
	}
	if m.detailOpen {
		return m.renderDetailView()
	}

	width := m.width
	if width <= 0 {
		width = 100
	}
	screenWidth := max(40, width)
	transcriptWidth := screenWidth
	actionWidth := m.contentWidthFor(screenWidth, m.styles.actionCard)
	statusWidth := m.contentWidthFor(screenWidth, m.styles.status)
	composerWidth := m.contentWidthFor(screenWidth, m.activeComposerStyle())
	footerWidth := screenWidth

	actionCard := m.renderActionCard(actionWidth)
	planCard := m.renderPlanCard(actionWidth)
	statusLine := m.renderStatusLine(statusWidth)
	shellTail := m.renderShellTail(statusWidth)
	composer := m.renderComposer(composerWidth)
	footer := m.renderFooter(footerWidth)

	screenHeight := m.height
	if screenHeight <= 0 {
		screenHeight = 24
	}

	transcriptHeight := m.transcriptViewportHeight(actionCard, planCard, statusLine, shellTail, composer, footer, screenHeight)

	transcript := m.renderTranscript(transcriptWidth, transcriptHeight)

	sections := []string{transcript}
	if actionCard != "" {
		sections = append(sections, actionCard)
	}
	if planCard != "" {
		sections = append(sections, planCard)
	}
	if statusLine != "" {
		sections = append(sections, statusLine)
	}
	if shellTail != "" {
		sections = append(sections, shellTail)
	}
	sections = append(sections, composer, footer)

	return m.styles.screen.
		Width(screenWidth).
		Render(lipgloss.JoinVertical(lipgloss.Left, sections...))
}

func (m Model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input)
	if text == "" || m.busy {
		return m, nil
	}

	if handled, next, cmd := m.handleComposerCommand(text); handled {
		return next, cmd
	}

	m.input = ""
	m.currentHistory().record(text)

	if m.mode == AgentMode {
		if m.ctrl == nil {
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  "controller is not available",
			})
			return m, nil
		}

		m.busy = true
		m.busyStartedAt = time.Now()
		m.showShellTail = false
		m.liveShellTail = ""
		prompt := text
		refining := m.refiningApproval
		m.refiningApproval = nil
		ctx, cancel := context.WithTimeout(context.Background(), agentTurnTimeout)
		m.inFlightCancel = cancel
		return m, tea.Batch(func() tea.Msg {
			defer cancel()

			var (
				events []controller.TranscriptEvent
				err    error
			)
			if refining != nil {
				events, err = m.ctrl.SubmitRefinement(ctx, *refining, prompt)
			} else {
				events, err = m.ctrl.SubmitAgentPrompt(ctx, prompt)
			}
			return controllerEventsMsg{
				events: events,
				err:    err,
			}
		}, tickBusy(), m.pollShellTailCmd())
	}

	if m.ctrl == nil {
		m.entries = append(m.entries, Entry{
			Title: "error",
			Body:  "controller is not available",
		})
		return m, nil
	}

	m.busy = true
	m.busyStartedAt = time.Now()
	m.directShellPending = true
	m.showShellTail = true
	command := text
	ctx, cancel := context.WithTimeout(context.Background(), shell.CommandTimeout(command))
	m.inFlightCancel = cancel
	return m, tea.Batch(func() tea.Msg {
		defer cancel()

		events, err := m.ctrl.SubmitShellCommand(ctx, command)
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}, tickBusy(), m.pollShellTailCmd())
}

func (m Model) handleComposerCommand(text string) (bool, tea.Model, tea.Cmd) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "/onboard", "/onboarding", "/provider", "/providers":
		m.input = ""
		m.currentHistory().reset()
		next, cmd := m.openOnboarding()
		return true, next, cmd
	default:
		return false, m, nil
	}
}

func (m Model) primaryAction() (tea.Model, tea.Cmd) {
	switch {
	case m.pendingApproval != nil:
		return m.decideApproval(controller.DecisionApprove)
	case m.pendingProposal != nil && m.pendingProposal.Command != "":
		return m.runProposalCommand()
	case m.activePlan != nil:
		return m.continueActivePlan()
	default:
		return m, nil
	}
}

func (m Model) shouldAutoContinue(events []controller.TranscriptEvent) bool {
	if m.ctrl == nil || m.directShellPending {
		return false
	}
	if !containsEventKind(events, controller.EventCommandResult) {
		return false
	}

	return m.proposalRunPending || m.approvalInFlight
}

func (m Model) continueActivePlan() (tea.Model, tea.Cmd) {
	if m.busy || m.ctrl == nil || m.activePlan == nil {
		return m, nil
	}

	m.busy = true
	m.busyStartedAt = time.Now()
	m.showShellTail = false
	m.liveShellTail = ""
	ctx, cancel := context.WithTimeout(context.Background(), agentTurnTimeout)
	m.inFlightCancel = cancel
	return m, tea.Batch(func() tea.Msg {
		defer cancel()

		events, err := m.ctrl.ContinueActivePlan(ctx)
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}, tickBusy(), m.pollShellTailCmd())
}

func (m Model) takeControlNow() (tea.Model, tea.Cmd) {
	if !m.takeControl.enabled() {
		return m, nil
	}

	if m.inFlightCancel != nil {
		m.resumeAfterHandoff = !m.directShellPending && (m.approvalInFlight || m.proposalRunPending || m.mode == AgentMode || m.activePlan != nil)
		m.suppressCancelErr = true
		m.inFlightCancel()
		m.inFlightCancel = nil
	}

	m.busy = false
	m.busyStartedAt = time.Time{}
	m.approvalInFlight = false
	m.proposalRunPending = false
	m.directShellPending = false
	if !m.resumeAfterHandoff {
		m.showShellTail = false
		m.liveShellTail = ""
	}

	return m, newTakeControlCmd(m.takeControl)
}

func (m Model) openOnboarding() (tea.Model, tea.Cmd) {
	if m.loadOnboarding == nil || m.switchProvider == nil {
		m.entries = append(m.entries, Entry{
			Title: "error",
			Body:  "provider onboarding is not configured in this session",
		})
		m.selectedEntry = len(m.entries) - 1
		m.clampSelection()
		return m, nil
	}

	choices, err := m.loadOnboarding()
	if err != nil {
		m.entries = append(m.entries, Entry{
			Title: "error",
			Body:  fmt.Sprintf("load provider onboarding candidates: %v", err),
		})
		m.selectedEntry = len(m.entries) - 1
		m.clampSelection()
		return m, nil
	}
	if len(choices) == 0 {
		m.entries = append(m.entries, Entry{
			Title: "system",
			Body:  "No provider onboarding candidates detected. Set OPENAI_API_KEY, OPENROUTER_API_KEY, or SHUTTLE_BASE_URL and try again.",
		})
		m.selectedEntry = len(m.entries) - 1
		m.clampSelection()
		return m, nil
	}

	m.onboardingChoices = append([]provider.OnboardingCandidate(nil), choices...)
	m.onboardingStep = onboardingStepProviders
	m.onboardingIndex = m.currentProviderChoiceIndex()
	m.onboardingSelected = nil
	m.onboardingForm = nil
	m.onboardingModels = nil
	m.onboardingModelIdx = 0
	m.onboardingOpen = true
	return m, nil
}

func (m Model) refreshShellContextCmd() tea.Cmd {
	if m.ctrl == nil {
		return nil
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		promptContext, err := m.ctrl.RefreshShellContext(ctx)
		return refreshedShellContextMsg{
			context: promptContext,
			err:     err,
		}
	}
}

func (m Model) pollShellTailCmd() tea.Cmd {
	if m.ctrl == nil || !m.showShellTail {
		return nil
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), shellTailPollTimeout)
		defer cancel()

		tail, err := m.ctrl.PeekShellTail(ctx, shellTailPollLines)
		return shellTailMsg{
			tail: tail,
			err:  err,
		}
	}
}

func (m Model) formatShellError(err error) string {
	if err == nil {
		return ""
	}

	message := err.Error()
	tail := strings.TrimSpace(m.liveShellTail)
	if tail == "" {
		return message
	}

	lines := strings.Split(tail, "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}

	return strings.TrimSpace(message + "\nlast shell output:\n" + strings.Join(lines, "\n"))
}

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
	shellContext := m.currentShellContext()
	m.busy = true
	m.busyStartedAt = time.Now()
	return m, func() tea.Msg {
		ctrl, profile, err := m.switchProvider(profile, shellContext)
		var persistErr error
		if err == nil && m.saveProvider != nil {
			persistErr = m.saveProvider(profile)
		}
		return providerSwitchedMsg{
			ctrl:       ctrl,
			profile:    profile,
			err:        err,
			persistErr: persistErr,
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

func (m Model) resolveOnboardingFormProfile() (provider.Profile, error) {
	if m.onboardingForm == nil {
		return provider.Profile{}, errors.New("onboarding form is not active")
	}

	values := map[string]string{}
	for _, field := range m.onboardingForm.fields {
		values[field.key] = strings.TrimSpace(field.value)
		if field.required && values[field.key] == "" {
			return provider.Profile{}, fmt.Errorf("%s is required", strings.ToLower(field.label))
		}
	}

	profile := m.onboardingForm.profile
	cfg := config.Config{
		ProviderType:       string(profile.Preset),
		ProviderAuthMethod: onboardingAuthMethod(profile.Preset, values["api_key"]),
		ProviderBaseURL:    values["base_url"],
		ProviderModel:      values["model"],
		ProviderAPIKey:     values["api_key"],
		ProviderCLICommand: values["cli_command"],
	}

	resolved, err := provider.ResolveProfile(cfg)
	if err != nil {
		return provider.Profile{}, err
	}
	if values["api_key"] != "" {
		resolved.APIKey = values["api_key"]
		resolved.APIKeyEnvVar = "os_keyring"
	}
	if values["api_key"] == "" && profile.AuthMethod == provider.AuthAPIKey {
		resolved.AuthMethod = provider.AuthAPIKey
	}
	return resolved, nil
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
			{key: "api_key", label: "API Key", value: "", placeholder: "stored in OS keyring", required: true, secret: true},
		}
	case provider.PresetOllama:
		form.fields = []onboardingField{
			{key: "base_url", label: "Base URL", value: profile.BaseURL, required: true},
			{key: "model", label: "Model", value: profile.Model, required: true},
			{key: "api_key", label: "API Key", value: "", placeholder: "optional", secret: true},
		}
	case provider.PresetCustom:
		form.fields = []onboardingField{
			{key: "base_url", label: "Base URL", value: profile.BaseURL, required: true},
			{key: "model", label: "Model", value: profile.Model, required: true},
			{key: "api_key", label: "API Key", value: "", placeholder: "optional", secret: true},
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

func onboardingAuthMethod(preset provider.ProviderPreset, apiKey string) string {
	switch preset {
	case provider.PresetOllama, provider.PresetCustom:
		if strings.TrimSpace(apiKey) == "" {
			return "none"
		}
		return "api_key"
	case provider.PresetCodexCLI:
		return "codex_login"
	default:
		return "api_key"
	}
}

func (m Model) currentShellContext() *shell.PromptContext {
	if m.shellContext.PromptLine() == "" {
		return nil
	}

	contextCopy := m.shellContext
	return &contextCopy
}

func (m Model) transcriptLines(width int) []string {
	lines := make([]string, 0, len(m.entries)*2)
	for index, entry := range m.entries {
		lines = append(lines, m.renderEntryLines(index, entry, width)...)
	}

	return lines
}

func (m Model) renderEntryLines(index int, entry Entry, width int) []string {
	prefix := "  "
	if index == m.selectedEntry {
		prefix = "› "
	}

	tag := m.renderTag(entry.Title)
	tagWidth := lipgloss.Width(tag)
	bodyWidth := max(10, width-lipgloss.Width(prefix)-tagWidth-1)
	bodyStyle := m.renderBodyStyle(entry.Title)
	indent := strings.Repeat(" ", lipgloss.Width(prefix)+tagWidth+1)

	rawLines := strings.Split(entry.Body, "\n")
	if len(rawLines) == 0 {
		rawLines = []string{""}
	}

	rendered := make([]string, 0, len(rawLines))
	firstBody := wrapText(rawLines[0], bodyWidth)
	if len(firstBody) == 0 {
		firstBody = []string{""}
	}
	rendered = append(rendered, prefix+tag+" "+bodyStyle.Render(firstBody[0]))
	for _, wrapped := range firstBody[1:] {
		rendered = append(rendered, indent+bodyStyle.Render(wrapped))
	}

	for _, rawLine := range rawLines[1:] {
		wrappedLines := wrapText(rawLine, bodyWidth)
		if len(wrappedLines) == 0 {
			wrappedLines = []string{""}
		}
		for _, wrapped := range wrappedLines {
			rendered = append(rendered, indent+bodyStyle.Render(wrapped))
		}
	}

	return rendered
}

func (m Model) renderBodyStyle(title string) lipgloss.Style {
	switch title {
	case "system":
		return m.styles.bodySystem
	case "user", "shell":
		return m.styles.bodyShell
	case "result":
		return m.styles.bodyResult
	case "agent", "plan", "proposal":
		return m.styles.bodyAgent
	case "approval", "error":
		return m.styles.bodyError
	default:
		return m.styles.bodyShell
	}
}

func (m Model) renderHeader(width int) string {
	modeStyle := m.styles.modeShell
	if m.mode == AgentMode {
		modeStyle = m.styles.modeAgent
	}

	mode := modeStyle.Render(string(m.mode))
	meta := []string{m.styles.headerTitle.Render("Shuttle"), mode}
	switch {
	case width >= 100:
		meta = append(meta,
			m.styles.headerMeta.Render("session="+m.workspace.SessionName),
			m.styles.headerMeta.Render("top="+m.workspace.TopPane.ID),
		)
	case width >= 72:
		meta = append(meta, m.styles.headerMeta.Render("top="+m.workspace.TopPane.ID))
	}
	if m.busy {
		meta = append(meta, m.styles.modeBusy.Render("BUSY"))
	}
	if m.pendingApproval != nil {
		risk := strings.ToUpper(string(m.pendingApproval.Risk))
		if risk == "" {
			risk = "REVIEW"
		}
		meta = append(meta, m.styles.modeApproval.Render("APPROVAL "+risk))
	} else if m.refiningApproval != nil {
		meta = append(meta, m.styles.modeProposal.Render("REFINING"))
	} else if m.pendingProposal != nil && m.pendingProposal.Command != "" {
		meta = append(meta, m.styles.modeProposal.Render("PROPOSAL"))
	}

	status := m.styles.header.Render(strings.Join(meta, " "))
	ruleWidth := width - lipgloss.Width(status)
	if ruleWidth > 1 {
		status = lipgloss.JoinHorizontal(lipgloss.Left, status, m.styles.headerRule.Render(strings.Repeat("━", ruleWidth-1)))
	}

	return status
}

func (m Model) renderActionCard(width int) string {
	if m.pendingApproval != nil {
		body := []string{
			m.pendingApproval.Title,
			m.pendingApproval.Summary,
		}
		if m.pendingApproval.Command != "" {
			body = append(body, "command: "+m.pendingApproval.Command)
		}
		if m.pendingApproval.Risk != "" {
			body = append(body, "risk: "+string(m.pendingApproval.Risk))
		}
		body = append(body, "Ctrl+E continue  Ctrl+N reject  Ctrl+R refine")
		content := lipgloss.JoinVertical(
			lipgloss.Left,
			m.styles.actionTitle.Render("Approval Required"),
			m.styles.actionBody.Render(strings.Join(body, "\n")),
		)
		return m.styles.actionCard.BorderForeground(lipgloss.Color("160")).Width(width).Render(content)
	}

	if m.refiningApproval != nil {
		body := []string{
			m.refiningApproval.Title,
			m.refiningApproval.Summary,
		}
		if m.refiningApproval.Command != "" {
			body = append(body, "command: "+m.refiningApproval.Command)
		}
		if m.refiningApproval.Risk != "" {
			body = append(body, "risk: "+string(m.refiningApproval.Risk))
		}
		body = append(body, "Enter a refinement note in the composer and press Enter")
		content := lipgloss.JoinVertical(
			lipgloss.Left,
			m.styles.actionTitle.Render("Refining Approval"),
			m.styles.actionBody.Render(strings.Join(body, "\n")),
		)
		return m.styles.actionCard.BorderForeground(lipgloss.Color("214")).Width(width).Render(content)
	}

	if m.pendingProposal != nil && m.pendingProposal.Command != "" {
		body := []string{}
		if m.pendingProposal.Description != "" {
			body = append(body, m.pendingProposal.Description)
		}
		body = append(body, "command: "+m.pendingProposal.Command)
		body = append(body, "Ctrl+E continue")
		content := lipgloss.JoinVertical(
			lipgloss.Left,
			m.styles.actionTitle.Render("Proposed Command"),
			m.styles.actionBody.Render(strings.Join(body, "\n")),
		)
		return m.styles.actionCard.BorderForeground(lipgloss.Color("31")).Width(width).Render(content)
	}

	return ""
}

func (m Model) renderPlanCard(width int) string {
	if m.activePlan == nil {
		return ""
	}

	body := make([]string, 0, len(m.activePlan.Steps)+2)
	if strings.TrimSpace(m.activePlan.Summary) != "" {
		body = append(body, m.activePlan.Summary)
	}

	visibleSteps := len(m.activePlan.Steps)
	if visibleSteps > 6 {
		visibleSteps = 6
	}
	for index := 0; index < visibleSteps; index++ {
		step := m.activePlan.Steps[index]
		body = append(body, fmt.Sprintf("%s %d. %s", planStepMarker(step.Status), index+1, step.Text))
	}
	if hiddenSteps := len(m.activePlan.Steps) - visibleSteps; hiddenSteps > 0 {
		body = append(body, fmt.Sprintf("... (%d more steps)", hiddenSteps))
	}
	body = append(body, m.planProgressSummary())
	body = append(body, "Ctrl+E continue")

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		m.styles.actionTitle.Render("Active Plan"),
		m.styles.actionBody.Render(strings.Join(body, "\n")),
	)
	return m.styles.actionCard.BorderForeground(lipgloss.Color("63")).Width(width).Render(content)
}

func (m Model) renderTranscript(width int, height int) string {
	lines := m.transcriptWindow(m.transcriptLines(width), height)
	return m.styles.transcript.Width(width).MaxWidth(width).Render(strings.Join(lines, "\n"))
}

func (m Model) renderComposer(width int) string {
	input := m.input
	if input == "" {
		input = " "
	}

	lines := strings.Split(input, "\n")
	firstLine := lines[0]
	if firstLine == " " {
		firstLine = ""
	}

	composerStyle := m.styles.composerShell
	if m.refiningApproval != nil {
		composerStyle = m.styles.composerRefine
	} else if m.mode == AgentMode {
		composerStyle = m.styles.composerAgent
	}

	promptStyle := m.styles.composerPromptShell
	prompt := "$>"
	switch {
	case m.refiningApproval != nil:
		promptStyle = m.styles.composerPromptRefine
		prompt = "Œ>"
	case m.mode == AgentMode:
		promptStyle = m.styles.composerPromptAgent
		prompt = "Œ>"
	case m.shellContext.Root:
		promptStyle = m.styles.composerPromptShell
		prompt = "#>"
	}

	rendered := []string{
		lipgloss.JoinHorizontal(lipgloss.Left, promptStyle.Render(prompt), m.styles.input.Render(" "+firstLine)),
	}
	for _, line := range lines[1:] {
		rendered = append(rendered, m.styles.input.Render(strings.Repeat(" ", lipgloss.Width(prompt)+1)+line))
	}

	return composerStyle.Width(width).Render(strings.Join(rendered, "\n"))
}

func (m Model) renderFooter(width int) string {
	if m.detailOpen {
		parts := []string{"[Esc] close", "[Up/Down] scroll", "[PgUp/PgDn] page", "[Home/End] bounds", "[F2] shell", "[Ctrl+C] quit"}
		return m.styles.footer.Width(width).Render(strings.Join(parts, "  "))
	}

	parts := m.footerParts(width)
	return m.styles.footer.Width(width).Render(strings.Join(parts, "  "))
}

func (m Model) renderStatusLine(width int) string {
	left := m.renderShellContext()
	rightParts := make([]string, 0, 4)
	if m.shellContext.Remote {
		rightParts = append(rightParts, m.styles.statusRemote.Render("REMOTE"))
	}
	if m.lastModelInfo != nil {
		label := strings.TrimSpace(m.lastModelInfo.ResponseModel)
		if label == "" {
			label = strings.TrimSpace(m.lastModelInfo.RequestedModel)
		}
		if label != "" {
			rightParts = append(rightParts, m.styles.statusRemote.Render("MODEL "+label))
		}
	}
	if m.busy {
		elapsed := 0
		if !m.busyStartedAt.IsZero() {
			elapsed = int(time.Since(m.busyStartedAt).Seconds())
		}
		rightParts = append(rightParts, m.styles.statusBusy.Render(fmt.Sprintf("Working (%ds)", elapsed)))
	}
	right := strings.Join(rightParts, " ")

	if left == "" && right == "" {
		return ""
	}

	if right == "" {
		return m.styles.status.Render(runewidth.Truncate(left, width, "…"))
	}
	if left == "" {
		return m.styles.status.Render(right)
	}

	availableLeft := width - lipgloss.Width(right) - 1
	if availableLeft < 0 {
		availableLeft = 0
	}
	left = runewidth.Truncate(left, availableLeft, "…")
	padding := width - lipgloss.Width(left) - lipgloss.Width(right)
	if padding < 1 {
		padding = 1
	}

	return m.styles.status.Render(left + strings.Repeat(" ", padding) + right)
}

func (m Model) renderShellTail(width int) string {
	if !m.showShellTail {
		return ""
	}

	tail := strings.TrimSpace(m.liveShellTail)
	if tail == "" {
		return ""
	}

	lines := strings.Split(tail, "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}

	contentWidth := max(10, width-2)
	rendered := make([]string, 0, len(lines)+1)
	rendered = append(rendered, m.styles.tailLabel.Render("shell"))
	for _, line := range lines {
		wrapped := wrapText(strings.TrimRight(line, "\r"), contentWidth)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for _, part := range wrapped {
			rendered = append(rendered, m.styles.tailBody.Render(part))
		}
	}
	if m.busy {
		rendered = append(rendered, m.styles.tailHint.Render("F2 to take control"))
	}

	return m.styles.tail.Width(width).Render(strings.Join(rendered, "\n"))
}

func (m Model) renderShellContext() string {
	return strings.TrimSpace(m.shellContext.PromptLine())
}

func (m Model) footerParts(width int) []string {
	switch {
	case width < 72:
		parts := []string{"[Tab]", "[Pg]", "[Enter]", "[Esc]", "[F2]", "[F3]", "[Ctrl+O]", "[Ctrl+C]"}
		if m.pendingApproval != nil {
			parts = append(parts, "[E/N/R]")
		} else if m.refiningApproval != nil {
			parts = append(parts, "[Enter]")
		} else if m.pendingProposal != nil && m.pendingProposal.Command != "" {
			parts = append(parts, "[Ctrl+E]")
		} else if m.activePlan != nil {
			parts = append(parts, "[Ctrl+E]")
		}
		return parts
	case width < 100:
		parts := []string{"[Tab] mode", "[Alt+Up/Down] entry", "[Ctrl+O] detail", "[PgUp/PgDn] scroll", "[Enter] submit", "[Esc] clear", "[F2] shell", "[F3] providers", "[Ctrl+C] quit"}
		if m.pendingApproval != nil {
			parts = append(parts, "[Ctrl+E/N/R]")
		} else if m.refiningApproval != nil {
			parts = append(parts, "[Enter] refine")
		} else if m.pendingProposal != nil && m.pendingProposal.Command != "" {
			parts = append(parts, "[Ctrl+E] continue")
		} else if m.activePlan != nil {
			parts = append(parts, "[Ctrl+E] plan")
		}
		return parts
	}

	parts := []string{"[Tab] mode", "[Up/Down] history", "[Alt+Up/Down] entry", "[Ctrl+O] detail", "[PgUp/PgDn] scroll", "[Ctrl+U/D] half-page", "[Home/End] bounds", "[Enter] submit", "[Esc] clear", "[Ctrl+J] newline", "[F2] shell", "[F3] providers"}
	if m.pendingApproval != nil {
		parts = append(parts, "[Ctrl+E] continue", "[Ctrl+N] reject", "[Ctrl+R] refine")
	} else if m.refiningApproval != nil {
		parts = append(parts, "[Enter] submit refine note")
	} else if m.pendingProposal != nil && m.pendingProposal.Command != "" {
		parts = append(parts, "[Ctrl+E] continue")
	} else if m.activePlan != nil {
		parts = append(parts, "[Ctrl+E] continue plan")
	}
	parts = append(parts, "[Ctrl+C] quit")
	return parts
}

func (m *Model) currentHistory() *composerHistory {
	if m.mode == AgentMode {
		return &m.agentHistory
	}

	return &m.shellHistory
}

func (m Model) transcriptViewportHeight(actionCard string, planCard string, statusLine string, shellTail string, composer string, footer string, screenHeight int) int {
	reservedHeight := lipgloss.Height(actionCard) + lipgloss.Height(planCard) + lipgloss.Height(statusLine) + lipgloss.Height(shellTail) + lipgloss.Height(composer) + lipgloss.Height(footer)
	transcriptChromeHeight := m.styles.transcript.GetVerticalFrameSize()
	transcriptHeight := screenHeight - reservedHeight - transcriptChromeHeight
	if transcriptHeight < 4 {
		transcriptHeight = 4
	}

	return transcriptHeight
}

func (m Model) transcriptWindow(lines []string, height int) []string {
	start := m.transcriptScroll
	maxStart := m.maxTranscriptScrollFor(lines, height)
	if m.transcriptFollow {
		start = maxStart
	}
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}

	end := start + height
	if end > len(lines) {
		end = len(lines)
	}

	window := append([]string(nil), lines[start:end]...)
	if len(window) < height {
		padding := make([]string, height-len(window))
		window = append(window, padding...)
	}

	return window
}

func (m Model) transcriptLineCount() int {
	return len(m.transcriptLines(m.currentTranscriptWidth()))
}

func (m Model) maxTranscriptScroll() int {
	return m.maxTranscriptScrollFor(m.transcriptLines(m.currentTranscriptWidth()), m.currentTranscriptHeight())
}

func (m Model) maxTranscriptScrollFor(lines []string, height int) int {
	if len(lines) <= height {
		return 0
	}

	return len(lines) - height
}

func (m Model) currentTranscriptHeight() int {
	if m.detailOpen {
		return max(4, m.height-4)
	}

	width := m.currentTranscriptWidth()
	actionCard := m.renderActionCard(m.contentWidthFor(width, m.styles.actionCard))
	planCard := m.renderPlanCard(m.contentWidthFor(width, m.styles.actionCard))
	statusLine := m.renderStatusLine(m.contentWidthFor(width, m.styles.status))
	shellTail := m.renderShellTail(m.contentWidthFor(width, m.styles.tail))
	composer := m.renderComposer(m.contentWidthFor(width, m.activeComposerStyle()))
	footer := m.renderFooter(width)

	screenHeight := m.height
	if screenHeight <= 0 {
		screenHeight = 24
	}

	return m.transcriptViewportHeight(actionCard, planCard, statusLine, shellTail, composer, footer, screenHeight)
}

func (m Model) currentTranscriptWidth() int {
	width := m.width
	if width <= 0 {
		width = 100
	}

	return max(40, width)
}

func (m Model) activeComposerStyle() lipgloss.Style {
	if m.refiningApproval != nil {
		return m.styles.composerRefine
	}
	if m.mode == AgentMode {
		return m.styles.composerAgent
	}

	return m.styles.composerShell
}

func (m Model) contentWidthFor(totalWidth int, style lipgloss.Style) int {
	width := totalWidth - style.GetHorizontalFrameSize()
	if width < 10 {
		return 10
	}

	return width
}

func (m Model) selectedEntryValue() Entry {
	if len(m.entries) == 0 {
		return Entry{}
	}

	index := m.selectedEntry
	if index < 0 {
		index = 0
	}
	if index >= len(m.entries) {
		index = len(m.entries) - 1
	}

	return m.entries[index]
}

func (e Entry) DetailBody() string {
	if strings.TrimSpace(e.Detail) != "" {
		return e.Detail
	}

	return e.Body
}

func (m *Model) clampSelection() {
	if len(m.entries) == 0 {
		m.selectedEntry = 0
		return
	}
	if m.selectedEntry < 0 {
		m.selectedEntry = 0
	}
	if m.selectedEntry >= len(m.entries) {
		m.selectedEntry = len(m.entries) - 1
	}
}

func (m *Model) selectPreviousEntry() {
	m.clampSelection()
	if m.selectedEntry > 0 {
		m.selectedEntry--
	}
}

func (m *Model) selectNextEntry() {
	m.clampSelection()
	if m.selectedEntry < len(m.entries)-1 {
		m.selectedEntry++
	}
}

func (m Model) openDetail() (tea.Model, tea.Cmd) {
	if len(m.entries) == 0 {
		return m, nil
	}

	m.clampSelection()
	m.detailOpen = true
	m.detailScroll = 0
	m.clampDetailScroll()
	return m, nil
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyF2:
		return m.takeControlNow()
	case tea.KeyEsc:
		m.detailOpen = false
		m.detailScroll = 0
		return m, nil
	case tea.KeyUp:
		if m.detailScroll > 0 {
			m.detailScroll--
		}
		return m, nil
	case tea.KeyDown:
		m.detailScroll++
		m.clampDetailScroll()
		return m, nil
	case tea.KeyPgUp:
		m.detailScroll -= m.detailPageSize()
		m.clampDetailScroll()
		return m, nil
	case tea.KeyPgDown:
		m.detailScroll += m.detailPageSize()
		m.clampDetailScroll()
		return m, nil
	case tea.KeyHome:
		m.detailScroll = 0
		return m, nil
	case tea.KeyEnd:
		m.detailScroll = m.maxDetailScroll()
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) renderDetailView() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	entry := m.selectedEntryValue()
	contentWidth := m.contentWidthFor(width, m.styles.detail)
	lines := []string{
		m.styles.detailTitle.Render(strings.ToUpper(entry.Title)),
		m.styles.detailMeta.Render(fmt.Sprintf("entry %d/%d", m.selectedEntry+1, max(1, len(m.entries)))),
		"",
	}
	bodyLines := wrapParagraphs(entry.DetailBody(), max(10, contentWidth))
	viewportHeight := height - lipgloss.Height(strings.Join(lines, "\n")) - m.styles.detail.GetVerticalFrameSize() - 2
	if viewportHeight < 4 {
		viewportHeight = 4
	}
	bodyLines = detailWindow(bodyLines, m.detailScroll, viewportHeight)
	for _, line := range bodyLines {
		lines = append(lines, m.styles.detailBody.Render(line))
	}
	lines = append(lines, "", m.styles.detailMeta.Render(m.renderDetailFooter(contentWidth)))

	return m.styles.detail.Width(contentWidth).Render(strings.Join(lines, "\n"))
}

func (m Model) renderOnboardingView() string {
	width := m.width
	if width <= 0 {
		width = 100
	}

	contentWidth := m.contentWidthFor(width, m.styles.detail)
	lines := []string{m.styles.detailTitle.Render("PROVIDER ONBOARDING")}
	if m.onboardingStep == onboardingStepConfig && m.onboardingForm != nil {
		lines = append(lines, m.styles.detailMeta.Render(m.onboardingForm.title))
	} else if m.onboardingStep == onboardingStepModels && m.onboardingSelected != nil {
		lines = append(lines, m.styles.detailMeta.Render("Select a model for "+m.onboardingSelected.Profile.Name))
	} else {
		lines = append(lines, m.styles.detailMeta.Render("Choose a saved, detected, or manual provider setup"))
	}
	lines = append(lines, "", m.styles.detailBody.Render("Current"), m.styles.detailMeta.Render(providerSummaryLine(m.activeProvider)), "")

	if m.onboardingStep == onboardingStepConfig && m.onboardingForm != nil {
		lines = append(lines, m.renderOnboardingConfig(contentWidth)...)
	} else if m.onboardingStep == onboardingStepModels && m.onboardingSelected != nil {
		lines = append(lines, m.renderOnboardingModels(contentWidth)...)
	} else {
		lines = append(lines, m.renderOnboardingProviders(contentWidth)...)
	}

	lines = append(lines, "", m.styles.detailMeta.Render(onboardingFooter(width, m.onboardingStep)))
	return m.styles.detail.Width(contentWidth).Render(strings.Join(lines, "\n"))
}

func (m Model) renderOnboardingProviders(contentWidth int) []string {
	lines := make([]string, 0, len(m.onboardingChoices)*4)
	for index, choice := range m.onboardingChoices {
		prefix := "  "
		if index == m.onboardingIndex {
			prefix = "› "
		}

		label := fmt.Sprintf("%s%s", prefix, choice.Profile.Name)
		if choice.Profile.Preset == m.activeProvider.Preset && choice.Profile.Preset != "" {
			label += " (current)"
		}

		lines = append(lines, m.styles.detailBody.Render(label))
		lines = append(lines, m.styles.detailMeta.Render("   "+providerSummaryLine(choice.Profile)))
		if choice.Manual {
			lines = append(lines, m.styles.detailMeta.Render("   setup: manual entry"))
		}
		if choice.AuthSource != "" {
			lines = append(lines, m.styles.detailMeta.Render("   auth source: "+choice.AuthSource))
		}
		if strings.TrimSpace(choice.Reason) != "" {
			wrapped := wrapParagraphs(choice.Reason, max(10, contentWidth-3))
			for _, line := range wrapped {
				lines = append(lines, m.styles.detailMeta.Render("   "+line))
			}
		}
		lines = append(lines, "")
	}

	return lines
}

func (m Model) renderOnboardingConfig(contentWidth int) []string {
	lines := []string{}
	if m.onboardingForm == nil {
		return lines
	}

	if intro := strings.TrimSpace(m.onboardingForm.intro); intro != "" {
		for _, line := range wrapParagraphs(intro, max(10, contentWidth)) {
			lines = append(lines, m.styles.detailMeta.Render(line))
		}
		lines = append(lines, "")
	}

	lines = append(lines, m.styles.detailMeta.Render(providerSummaryLine(m.onboardingForm.profile)), "")
	for index, field := range m.onboardingForm.fields {
		prefix := "  "
		if index == m.onboardingForm.index {
			prefix = "› "
		}

		value := field.value
		switch {
		case field.secret && strings.TrimSpace(value) != "":
			value = strings.Repeat("*", min(12, len(value)))
		case strings.TrimSpace(value) == "" && field.placeholder != "":
			value = "<" + field.placeholder + ">"
		case strings.TrimSpace(value) == "":
			value = "<empty>"
		}

		label := fmt.Sprintf("%s%s: %s", prefix, field.label, value)
		lines = append(lines, m.styles.detailBody.Render(label))
	}

	lines = append(lines, "", m.styles.detailMeta.Render("API keys entered here are stored in the OS keyring."))
	return lines
}

func (m Model) renderOnboardingModels(contentWidth int) []string {
	lines := []string{}
	if m.onboardingSelected == nil {
		return lines
	}

	lines = append(lines, m.styles.detailMeta.Render(providerSummaryLine(m.onboardingSelected.Profile)), "")
	if len(m.onboardingModels) == 0 {
		lines = append(lines, m.styles.detailBody.Render("No models returned by this provider."))
		return lines
	}

	start, end := onboardingModelWindow(len(m.onboardingModels), m.onboardingModelIdx, 8)
	if start > 0 {
		lines = append(lines, m.styles.detailMeta.Render(fmt.Sprintf("... %d earlier models ...", start)))
	}
	for index := start; index < end; index++ {
		model := m.onboardingModels[index]
		prefix := "  "
		if index == m.onboardingModelIdx {
			prefix = "› "
		}
		label := fmt.Sprintf("%s%s", prefix, model.ID)
		if model.ID == m.activeProvider.Model {
			label += " (current)"
		}
		lines = append(lines, m.styles.detailBody.Render(label))
		detail := modelDetailLine(model)
		if detail != "" {
			for _, line := range wrapParagraphs(detail, max(10, contentWidth-3)) {
				lines = append(lines, m.styles.detailMeta.Render("   "+line))
			}
		}
		lines = append(lines, "")
	}
	if end < len(m.onboardingModels) {
		lines = append(lines, m.styles.detailMeta.Render(fmt.Sprintf("... %d more models ...", len(m.onboardingModels)-end)))
	}

	return lines
}

func (m Model) renderDetailFooter(width int) string {
	left := "Esc close  Up/Down scroll  PgUp/PgDn page"
	right := m.detailScrollIndicator()
	if right == "" {
		return left
	}

	padding := width - lipgloss.Width(left) - lipgloss.Width(right)
	if padding < 1 {
		return left + " " + right
	}

	return left + strings.Repeat(" ", padding) + right
}

func (m Model) detailScrollIndicator() string {
	maxScroll := m.maxDetailScroll()
	if maxScroll <= 0 {
		return ""
	}

	switch {
	case m.detailScroll <= 0:
		return "↓"
	case m.detailScroll >= maxScroll:
		return "↑"
	default:
		return "↑↓"
	}
}

func detailWindow(lines []string, start int, height int) []string {
	if start < 0 {
		start = 0
	}
	if start > len(lines) {
		start = len(lines)
	}
	end := start + height
	if end > len(lines) {
		end = len(lines)
	}

	window := append([]string(nil), lines[start:end]...)
	for len(window) < height {
		window = append(window, "")
	}

	return window
}

func compactResultPreview(body string, maxLines int) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 {
		return "(no output)"
	}

	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}

	preview := append([]string(nil), lines[:maxLines]...)
	preview = append(preview, fmt.Sprintf("... (%d more lines, Ctrl+O to inspect)", len(lines)-maxLines))
	return strings.Join(preview, "\n")
}

func formatResultDetail(command string, exitCode int, output string) string {
	command = strings.TrimSpace(command)
	output = strings.TrimSpace(output)
	if output == "" {
		output = "(no output)"
	}

	sections := []string{
		"command:",
		command,
		"",
		fmt.Sprintf("exit=%d", exitCode),
		"",
		output,
	}

	return strings.Join(sections, "\n")
}

func compactPlanEntry(summary string, steps []controller.PlanStep) Entry {
	detailLines := make([]string, 0, len(steps)+2)
	if strings.TrimSpace(summary) != "" {
		detailLines = append(detailLines, summary)
	}
	for index, step := range steps {
		detailLines = append(detailLines, fmt.Sprintf("%s %d. %s", planStepMarker(step.Status), index+1, step.Text))
	}
	if len(detailLines) == 0 {
		detailLines = append(detailLines, "(empty plan)")
	}

	previewLines := make([]string, 0, 3)
	if strings.TrimSpace(summary) != "" {
		previewLines = append(previewLines, summary)
	}
	visibleSteps := min(2, len(steps))
	for index := 0; index < visibleSteps; index++ {
		previewLines = append(previewLines, fmt.Sprintf("%s %d. %s", planStepMarker(steps[index].Status), index+1, steps[index].Text))
	}
	if hiddenSteps := len(steps) - visibleSteps; hiddenSteps > 0 {
		previewLines = append(previewLines, fmt.Sprintf("... (%d more steps, Ctrl+O to inspect)", hiddenSteps))
	}
	if len(previewLines) == 0 {
		previewLines = append(previewLines, "(empty plan)")
	}

	return Entry{
		Title:  "plan",
		Body:   strings.Join(previewLines, "\n"),
		Detail: strings.Join(detailLines, "\n"),
	}
}

func compactProposalEntry(payload controller.ProposalPayload) Entry {
	detailLines := make([]string, 0, 5)
	if payload.Kind != "" {
		detailLines = append(detailLines, "kind: "+string(payload.Kind))
	}
	if payload.Description != "" {
		detailLines = append(detailLines, payload.Description)
	}
	if payload.Command != "" {
		detailLines = append(detailLines, "command: "+payload.Command)
	}
	if payload.Patch != "" {
		if len(detailLines) > 0 {
			detailLines = append(detailLines, "")
		}
		detailLines = append(detailLines, "patch:")
		detailLines = append(detailLines, payload.Patch)
	}
	if len(detailLines) == 0 {
		detailLines = append(detailLines, "(empty proposal)")
	}

	previewLines := make([]string, 0, 3)
	if payload.Description != "" {
		previewLines = append(previewLines, payload.Description)
	}
	switch {
	case payload.Command != "":
		previewLines = append(previewLines, "command: "+payload.Command)
	case payload.Patch != "":
		previewLines = append(previewLines, fmt.Sprintf("patch attached (%d lines, Ctrl+O to inspect)", countNonEmptyLines(payload.Patch)))
	case payload.Kind != "":
		previewLines = append(previewLines, "kind: "+string(payload.Kind))
	}
	if len(previewLines) == 0 {
		previewLines = append(previewLines, "(empty proposal)")
	}

	return Entry{
		Title:  "proposal",
		Body:   strings.Join(previewLines, "\n"),
		Detail: strings.Join(detailLines, "\n"),
	}
}

func compactApprovalEntry(payload controller.ApprovalRequest) Entry {
	detailLines := make([]string, 0, 7)
	if payload.Title != "" {
		detailLines = append(detailLines, payload.Title)
	}
	if payload.Summary != "" {
		detailLines = append(detailLines, payload.Summary)
	}
	if payload.Kind != "" {
		detailLines = append(detailLines, "kind: "+string(payload.Kind))
	}
	if payload.Risk != "" {
		detailLines = append(detailLines, "risk: "+string(payload.Risk))
	}
	if payload.Command != "" {
		detailLines = append(detailLines, "command: "+payload.Command)
	}
	if payload.Patch != "" {
		if len(detailLines) > 0 {
			detailLines = append(detailLines, "")
		}
		detailLines = append(detailLines, "patch:")
		detailLines = append(detailLines, payload.Patch)
	}
	if len(detailLines) == 0 {
		detailLines = append(detailLines, "(empty approval)")
	}

	previewLines := make([]string, 0, 4)
	if payload.Title != "" {
		previewLines = append(previewLines, payload.Title)
	}
	if payload.Summary != "" {
		previewLines = append(previewLines, payload.Summary)
	}
	if payload.Command != "" {
		previewLines = append(previewLines, "command: "+payload.Command)
	}
	if payload.Risk != "" {
		previewLines = append(previewLines, "risk: "+string(payload.Risk))
	}
	if len(previewLines) == 0 {
		previewLines = append(previewLines, "(empty approval)")
	}
	if len(previewLines) > 3 {
		previewLines = append(previewLines[:3], fmt.Sprintf("... (%d more lines, Ctrl+O to inspect)", len(previewLines)-3))
	}

	return Entry{
		Title:  "approval",
		Body:   strings.Join(previewLines, "\n"),
		Detail: strings.Join(detailLines, "\n"),
	}
}

func countNonEmptyLines(value string) int {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return 0
	}

	return len(lines)
}

func (m Model) detailPageSize() int {
	return max(1, m.height/2)
}

func (m *Model) clampDetailScroll() {
	if m.detailScroll < 0 {
		m.detailScroll = 0
		return
	}
	if m.detailScroll > m.maxDetailScroll() {
		m.detailScroll = m.maxDetailScroll()
	}
}

func (m Model) maxDetailScroll() int {
	entry := m.selectedEntryValue()
	width := m.width
	if width <= 0 {
		width = 100
	}
	contentWidth := m.contentWidthFor(max(40, width), m.styles.detail)
	lines := wrapParagraphs(entry.DetailBody(), max(10, contentWidth))
	height := m.height
	if height <= 0 {
		height = 24
	}
	viewportHeight := height - 8
	if viewportHeight < 4 {
		viewportHeight = 4
	}
	if len(lines) <= viewportHeight {
		return 0
	}

	return len(lines) - viewportHeight
}

func (m *Model) clampTranscriptScroll() {
	maxScroll := m.maxTranscriptScroll()
	if m.transcriptScroll < 0 {
		m.transcriptScroll = 0
		return
	}
	if m.transcriptScroll > maxScroll {
		m.transcriptScroll = maxScroll
	}
}

func (m *Model) scrollTranscriptBy(delta int) {
	if m.transcriptFollow {
		m.transcriptScroll = m.maxTranscriptScroll()
	}
	m.transcriptScroll += delta
	m.clampTranscriptScroll()
	m.transcriptFollow = m.transcriptScroll >= m.maxTranscriptScroll()
}

func (m *Model) scrollTranscriptToTop() {
	m.transcriptScroll = 0
	m.transcriptFollow = false
}

func (m *Model) scrollTranscriptToBottom() {
	m.transcriptScroll = m.maxTranscriptScroll()
	m.transcriptFollow = true
}

func (m Model) isTranscriptPinned() bool {
	return m.transcriptFollow || m.transcriptScroll >= m.maxTranscriptScroll()
}

func (m Model) pageScrollSize() int {
	height := m.currentTranscriptHeight()
	if height <= 1 {
		return 1
	}

	return max(1, height-2)
}

func (m Model) halfPageScrollSize() int {
	return max(1, m.currentTranscriptHeight()/2)
}

func (m Model) renderTag(title string) string {
	text := strings.ToUpper(title)

	switch title {
	case "system":
		return m.styles.tagSystem.Render(text)
	case "user":
		return m.styles.tagShell.Render(text)
	case "shell":
		return m.styles.tagShell.Render(text)
	case "result":
		return m.styles.tagResult.Render(text)
	case "agent":
		return m.styles.tagAgent.Render(text)
	case "plan":
		return m.styles.tagAgent.Render(text)
	case "proposal":
		return m.styles.tagAgent.Render(text)
	case "approval":
		return m.styles.tagError.Render(text)
	case "error":
		return m.styles.tagError.Render(text)
	default:
		return m.styles.tagSystem.Render(text)
	}
}

func (m Model) runProposalCommand() (tea.Model, tea.Cmd) {
	if m.busy || m.ctrl == nil || m.pendingProposal == nil || m.pendingProposal.Command == "" {
		return m, nil
	}

	m.busy = true
	m.busyStartedAt = time.Now()
	m.proposalRunPending = true
	m.showShellTail = true
	command := m.pendingProposal.Command
	ctx, cancel := context.WithTimeout(context.Background(), shell.CommandTimeout(command))
	m.inFlightCancel = cancel

	return m, tea.Batch(func() tea.Msg {
		defer cancel()

		events, err := m.ctrl.SubmitShellCommand(ctx, command)
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}, tickBusy(), m.pollShellTailCmd())
}

func (m Model) decideApproval(decision controller.ApprovalDecision) (tea.Model, tea.Cmd) {
	if m.busy || m.ctrl == nil || m.pendingApproval == nil {
		return m, nil
	}

	m.busy = true
	m.busyStartedAt = time.Now()
	m.approvalInFlight = true
	m.showShellTail = decision == controller.DecisionApprove
	if !m.showShellTail {
		m.liveShellTail = ""
	}
	approvalID := m.pendingApproval.ID
	command := m.pendingApproval.Command
	ctx, cancel := context.WithTimeout(context.Background(), shell.CommandTimeout(command))
	m.inFlightCancel = cancel

	return m, tea.Batch(func() tea.Msg {
		defer cancel()

		events, err := m.ctrl.DecideApproval(ctx, approvalID, decision, "")
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}, tickBusy(), m.pollShellTailCmd())
}

func (m Model) refineApproval() (tea.Model, tea.Cmd) {
	if m.busy || m.ctrl == nil || m.pendingApproval == nil {
		return m, nil
	}

	approval := *m.pendingApproval
	m.refiningApproval = &approval
	m.input = ""
	m.mode = AgentMode
	m.busy = true
	m.busyStartedAt = time.Now()
	m.approvalInFlight = true
	m.showShellTail = false
	m.liveShellTail = ""
	approvalID := m.pendingApproval.ID
	command := m.pendingApproval.Command
	ctx, cancel := context.WithTimeout(context.Background(), shell.CommandTimeout(command))
	m.inFlightCancel = cancel

	return m, tea.Batch(func() tea.Msg {
		defer cancel()

		events, err := m.ctrl.DecideApproval(ctx, approvalID, controller.DecisionRefine, "")
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}, tickBusy())
}

func (m *Model) syncActionState(events []controller.TranscriptEvent) {
	if shellContext := latestShellContext(events); shellContext != nil {
		m.shellContext = *shellContext
	}
	if modelInfo := latestModelInfo(events); modelInfo != nil {
		m.lastModelInfo = modelInfo
	}

	newPlan := latestPlan(events)
	if newPlan != nil {
		m.activePlan = newPlan
	}

	newApproval := latestApproval(events)
	if newApproval != nil {
		m.pendingApproval = newApproval
		m.refiningApproval = nil
		m.pendingProposal = nil
	}

	newProposal := latestProposal(events)
	if newProposal != nil {
		m.pendingProposal = newProposal
		if newApproval == nil {
			m.pendingApproval = nil
		}
	}

	if m.approvalInFlight && !containsEventKind(events, controller.EventApproval) {
		m.pendingApproval = nil
	}
	if m.proposalRunPending {
		m.pendingProposal = nil
	}

	m.approvalInFlight = false
	m.proposalRunPending = false
	m.directShellPending = false
}

func (m Model) planProgressSummary() string {
	if m.activePlan == nil || len(m.activePlan.Steps) == 0 {
		return "Active plan ready"
	}

	done := 0
	current := 0
	for index, step := range m.activePlan.Steps {
		if step.Status == controller.PlanStepDone {
			done++
		}
		if step.Status == controller.PlanStepInProgress {
			current = index + 1
		}
	}
	if current == 0 && done < len(m.activePlan.Steps) {
		current = done + 1
	}
	if done == len(m.activePlan.Steps) {
		return fmt.Sprintf("Plan complete (%d/%d)", done, len(m.activePlan.Steps))
	}

	return fmt.Sprintf("Plan %d/%d", current, len(m.activePlan.Steps))
}

func planStepMarker(status controller.PlanStepStatus) string {
	switch status {
	case controller.PlanStepDone:
		return "[x]"
	case controller.PlanStepInProgress:
		return "[>]"
	default:
		return "[ ]"
	}
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func tickBusy() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return busyTickMsg(t)
	})
}

func wrapParagraphs(value string, width int) []string {
	paragraphs := strings.Split(value, "\n")
	lines := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		lines = append(lines, wrapText(paragraph, width)...)
	}
	return lines
}

func wrapText(value string, width int) []string {
	if width <= 0 {
		return []string{value}
	}
	if value == "" {
		return []string{""}
	}

	remaining := value
	lines := make([]string, 0, 2)
	for runewidth.StringWidth(remaining) > width {
		cut := 0
		currentWidth := 0
		lastSpace := -1
		for index, r := range remaining {
			runeWidth := runewidth.RuneWidth(r)
			if currentWidth+runeWidth > width {
				break
			}
			currentWidth += runeWidth
			cut = index + len(string(r))
			if r == ' ' || r == '\t' {
				lastSpace = cut
			}
		}

		if cut <= 0 {
			break
		}

		breakAt := cut
		if lastSpace > 0 {
			breakAt = lastSpace
		}

		chunk := strings.TrimRight(remaining[:breakAt], " \t")
		if chunk == "" {
			chunk = remaining[:cut]
			breakAt = cut
		}

		lines = append(lines, chunk)
		remaining = strings.TrimLeft(remaining[breakAt:], " \t")
		if remaining == "" {
			return lines
		}
	}

	lines = append(lines, remaining)
	return lines
}

func latestApproval(events []controller.TranscriptEvent) *controller.ApprovalRequest {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != controller.EventApproval {
			continue
		}

		payload, ok := events[index].Payload.(controller.ApprovalRequest)
		if !ok {
			continue
		}

		approval := payload
		return &approval
	}

	return nil
}

func latestPlan(events []controller.TranscriptEvent) *controller.ActivePlan {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != controller.EventPlan {
			continue
		}

		payload, ok := events[index].Payload.(controller.PlanPayload)
		if !ok {
			continue
		}

		plan := controller.ActivePlan{
			Summary: payload.Summary,
			Steps:   append([]controller.PlanStep(nil), payload.Steps...),
		}
		return &plan
	}

	return nil
}

func latestShellContext(events []controller.TranscriptEvent) *shell.PromptContext {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != controller.EventCommandResult {
			continue
		}

		payload, ok := events[index].Payload.(controller.CommandResultSummary)
		if !ok || payload.ShellContext == nil {
			continue
		}

		context := *payload.ShellContext
		return &context
	}

	return nil
}

func latestProposal(events []controller.TranscriptEvent) *controller.ProposalPayload {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != controller.EventProposal {
			continue
		}

		payload, ok := events[index].Payload.(controller.ProposalPayload)
		if !ok || payload.Command == "" {
			continue
		}

		proposal := payload
		return &proposal
	}

	return nil
}

func latestModelInfo(events []controller.TranscriptEvent) *controller.AgentModelInfo {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != controller.EventModelInfo {
			continue
		}

		payload, ok := events[index].Payload.(controller.AgentModelInfo)
		if !ok {
			continue
		}

		info := payload
		return &info
	}

	return nil
}

func (m Model) currentProviderChoiceIndex() int {
	for index, choice := range m.onboardingChoices {
		if choice.Profile.Preset == m.activeProvider.Preset && choice.Profile.Model == m.activeProvider.Model && choice.Profile.BaseURL == m.activeProvider.BaseURL {
			return index
		}
	}

	return 0
}

func (m Model) currentProviderModelIndex() int {
	currentModel := m.activeProvider.Model
	if m.onboardingSelected != nil && strings.TrimSpace(m.onboardingSelected.Profile.Model) != "" {
		currentModel = m.onboardingSelected.Profile.Model
	}

	for index, model := range m.onboardingModels {
		if model.ID == currentModel {
			return index
		}
	}

	return 0
}

func providerSummaryLine(profile provider.Profile) string {
	if profile.Preset == "" {
		return "provider not configured"
	}

	return fmt.Sprintf("preset=%s  model=%s  base=%s", profile.Preset, profile.Model, profile.BaseURL)
}

func providerAuthSourceLabel(profile provider.Profile) string {
	if strings.TrimSpace(profile.APIKeyEnvVar) != "" {
		return profile.APIKeyEnvVar
	}
	if profile.AuthMethod == provider.AuthNone {
		return "none"
	}
	return string(profile.AuthMethod)
}

func onboardingFooter(width int, step onboardingStep) string {
	if width < 72 {
		if step == onboardingStepConfig {
			return "Type edit  Tab next  Enter save  Esc back  F2 shell"
		}
		if step == onboardingStepModels {
			return "Enter apply  Esc back  Up/Down move  Pg page  F2 shell"
		}
		return "Enter models  Esc close  Up/Down move  F2 shell"
	}

	if step == onboardingStepConfig {
		return "Type to edit fields  Tab/Up/Down move  Enter save and switch  Esc back  F2 shell"
	}
	if step == onboardingStepModels {
		return "Enter switch provider with selected model  Esc back  Up/Down move  PgUp/PgDn page  F2 shell"
	}

	return "Enter inspect models  Esc close  Up/Down move  F2 shell"
}

func onboardingModelWindow(total int, index int, size int) (int, int) {
	if total <= size {
		return 0, total
	}

	start := index - size/2
	if start < 0 {
		start = 0
	}
	end := start + size
	if end > total {
		end = total
		start = max(0, end-size)
	}

	return start, end
}

func modelDetailLine(model provider.ModelOption) string {
	parts := make([]string, 0, 7)
	if model.Name != "" && model.Name != model.ID {
		parts = append(parts, model.Name)
	}
	if model.ContextWindow > 0 {
		parts = append(parts, fmt.Sprintf("context %d", model.ContextWindow))
	}
	if model.MaxCompletionTokens > 0 {
		parts = append(parts, fmt.Sprintf("max out %d", model.MaxCompletionTokens))
	}
	if model.PromptPrice != "" || model.CompletionPrice != "" {
		price := fmt.Sprintf("pricing p=%s c=%s", model.PromptPrice, model.CompletionPrice)
		parts = append(parts, price)
	}
	if model.Architecture.Modality != "" {
		parts = append(parts, "mode "+model.Architecture.Modality)
	}
	if len(model.SupportedParameters) > 0 {
		parts = append(parts, "params "+strings.Join(model.SupportedParameters[:min(3, len(model.SupportedParameters))], ","))
	}
	if model.Description != "" {
		parts = append(parts, model.Description)
	}

	return strings.Join(parts, "  ")
}

type composerHistory struct {
	entries  []string
	index    int
	draft    string
	browsing bool
}

func (h *composerHistory) record(value string) {
	if value == "" {
		h.reset()
		return
	}

	if len(h.entries) == 0 || h.entries[len(h.entries)-1] != value {
		h.entries = append(h.entries, value)
	}

	h.reset()
}

func (h *composerHistory) previous(current string) string {
	if len(h.entries) == 0 {
		return current
	}

	if !h.browsing {
		h.draft = current
		h.index = len(h.entries) - 1
		h.browsing = true
		return h.entries[h.index]
	}

	if h.index > 0 {
		h.index--
	}

	return h.entries[h.index]
}

func (h *composerHistory) next(current string) string {
	if len(h.entries) == 0 || !h.browsing {
		return current
	}

	if h.index < len(h.entries)-1 {
		h.index++
		return h.entries[h.index]
	}

	draft := h.draft
	h.reset()
	return draft
}

func (h *composerHistory) reset() {
	h.index = 0
	h.draft = ""
	h.browsing = false
}

func eventsToEntries(events []controller.TranscriptEvent, collapseResults bool) []Entry {
	entries := make([]Entry, 0, len(events))
	for _, event := range events {
		switch event.Kind {
		case controller.EventUserMessage:
			payload, _ := event.Payload.(controller.TextPayload)
			entries = append(entries, Entry{Title: "user", Body: payload.Text})
		case controller.EventAgentMessage:
			payload, _ := event.Payload.(controller.TextPayload)
			entries = append(entries, Entry{Title: "agent", Body: payload.Text})
		case controller.EventPlan:
			payload, _ := event.Payload.(controller.PlanPayload)
			entries = append(entries, compactPlanEntry(payload.Summary, payload.Steps))
		case controller.EventProposal:
			payload, _ := event.Payload.(controller.ProposalPayload)
			entries = append(entries, compactProposalEntry(payload))
		case controller.EventApproval:
			payload, _ := event.Payload.(controller.ApprovalRequest)
			entries = append(entries, compactApprovalEntry(payload))
		case controller.EventCommandStart:
			payload, _ := event.Payload.(controller.CommandStartPayload)
			entries = append(entries, Entry{Title: "shell", Body: payload.Command})
		case controller.EventCommandResult:
			payload, _ := event.Payload.(controller.CommandResultSummary)
			fullBody := strings.TrimSpace(payload.Summary)
			if fullBody == "" {
				fullBody = "(no output)"
			}
			body := fullBody
			if collapseResults {
				body = compactResultPreview(fullBody, 6)
			}
			entries = append(entries, Entry{
				Title:  "result",
				Body:   fmt.Sprintf("exit=%d\n%s", payload.ExitCode, body),
				Detail: formatResultDetail(payload.Command, payload.ExitCode, fullBody),
			})
		case controller.EventModelInfo:
			payload, _ := event.Payload.(controller.AgentModelInfo)
			model := strings.TrimSpace(payload.ResponseModel)
			if model == "" {
				model = strings.TrimSpace(payload.RequestedModel)
			}
			body := "provider model metadata unavailable"
			if model != "" {
				body = "reply model: " + model
			}
			if payload.RequestedModel != "" && payload.ResponseModel != "" && payload.RequestedModel != payload.ResponseModel {
				body = fmt.Sprintf("reply model: %s (requested %s)", payload.ResponseModel, payload.RequestedModel)
			}
			entries = append(entries, Entry{Title: "system", Body: body})
		case controller.EventSystemNotice:
			payload, _ := event.Payload.(controller.TextPayload)
			entries = append(entries, Entry{Title: "system", Body: payload.Text})
		case controller.EventError:
			payload, _ := event.Payload.(controller.TextPayload)
			entries = append(entries, Entry{Title: "error", Body: payload.Text})
		}
	}
	return entries
}

func containsEventKind(events []controller.TranscriptEvent, kind controller.TranscriptEventKind) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"aiterm/internal/config"
	"aiterm/internal/controller"
	"aiterm/internal/logging"
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

type activeExecutionMsg struct {
	execution *controller.CommandExecution
}

type activeExecutionCheckInMsg struct {
	executionID string
	events      []controller.TranscriptEvent
	err         error
}

type shellInterruptMsg struct {
	err error
}

type exitConfirmExpiredMsg struct {
	token uint64
}

type fullscreenKeysSentMsg struct {
	keys string
	err  error
}

type fullscreenActionKind string

const (
	fullscreenActionShellSubmit fullscreenActionKind = "shell_submit"
	fullscreenActionProposalRun fullscreenActionKind = "proposal_run"
	fullscreenActionApprovalRun fullscreenActionKind = "approval_run"
)

type fullscreenAction struct {
	Kind       fullscreenActionKind
	Command    string
	ApprovalID string
}

type startupSecurityNotice struct {
	Title string
	Body  string
}

type providerSwitchedMsg struct {
	ctrl         controller.Controller
	profile      provider.Profile
	err          error
	persistErr   error
	settingsStep settingsStep
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

type settingsStep string

const (
	settingsStepMenu           settingsStep = "menu"
	settingsStepProviders      settingsStep = "providers"
	settingsStepActiveProvider settingsStep = "active_provider"
	settingsStepActiveModels   settingsStep = "active_models"
	settingsStepProviderForm   settingsStep = "provider_form"
)

type settingsMenuEntry struct {
	label string
}

type settingsProviderEntry struct {
	label     string
	detail    string
	candidate *provider.OnboardingCandidate
	disabled  bool
}

type settingsModelChoice struct {
	profile provider.Profile
	model   provider.ModelOption
}

type settingsModelsLoadedMsg struct {
	choices []settingsModelChoice
	err     error
}

type settingsProviderSavedMsg struct {
	ctrl       controller.Controller
	profile    provider.Profile
	err        error
	persistErr error
}

const (
	agentTurnTimeout     = 60 * time.Second
	shellTailPollLines   = 40
	shellTailPollTimeout = 750 * time.Millisecond
	firstCheckInDelay    = 10 * time.Second
	repeatCheckInDelay   = 30 * time.Second
)

type Model struct {
	workspace             tmux.Workspace
	ctrl                  controller.Controller
	mode                  Mode
	input                 string
	cursor                int
	entries               []Entry
	selectedEntry         int
	width                 int
	height                int
	busy                  bool
	busyStartedAt         time.Time
	transcriptScroll      int
	transcriptFollow      bool
	detailOpen            bool
	detailScroll          int
	shellHistory          composerHistory
	agentHistory          composerHistory
	activePlan            *controller.ActivePlan
	shellContext          shell.PromptContext
	pendingApproval       *controller.ApprovalRequest
	pendingProposal       *controller.ProposalPayload
	startupNotice         *startupSecurityNotice
	refiningApproval      *controller.ApprovalRequest
	refiningProposal      *controller.ProposalPayload
	editingProposal       *controller.ProposalPayload
	approvalInFlight      bool
	proposalRunPending    bool
	directShellPending    bool
	inFlightCancel        context.CancelFunc
	suppressCancelErr     bool
	resumeAfterHandoff    bool
	handoffVisible        bool
	handoffPriorState     controller.CommandExecutionState
	takeControl           takeControlConfig
	liveShellTail         string
	showShellTail         bool
	activeExecution       *controller.CommandExecution
	pendingFullscreen     *fullscreenAction
	sendingFullscreenKeys bool
	lastFullscreenKeys    string
	lastFullscreenKeysAt  time.Time
	exitConfirmUntil      time.Time
	exitConfirmToken      uint64
	checkInInFlight       bool
	lastCheckInAt         time.Time
	lastInterruptNoticeID string
	activeProvider        provider.Profile
	onboardingOpen        bool
	onboardingStep        onboardingStep
	onboardingIndex       int
	onboardingChoices     []provider.OnboardingCandidate
	onboardingSelected    *provider.OnboardingCandidate
	onboardingForm        *onboardingFormState
	onboardingModels      []provider.ModelOption
	onboardingModelIdx    int
	loadOnboarding        func() ([]provider.OnboardingCandidate, error)
	loadModels            func(provider.Profile) ([]provider.ModelOption, error)
	switchProvider        func(provider.Profile, *shell.PromptContext) (controller.Controller, provider.Profile, error)
	saveProvider          func(provider.Profile) error
	settingsOpen          bool
	settingsStep          settingsStep
	settingsIndex         int
	settingsProviders     []settingsProviderEntry
	settingsProviderIdx   int
	settingsConfig        *onboardingFormState
	settingsModelCatalog  []settingsModelChoice
	settingsModels        []settingsModelChoice
	settingsModelIdx      int
	settingsModelFilter   string
	settingsModelInfo     bool
	lastModelInfo         *controller.AgentModelInfo
	styles                styles
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
				Body:  "Tab mode. Up/Down history. PgUp/PgDn scroll. Enter submit. F2 take control. F3 providers. F10 settings. /onboard opens provider onboarding. Esc clear or interrupt. Ctrl+C quit.",
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
	m.startupNotice = startupNoticeForProfile(active)
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
		logging.Trace(
			"tui.controller_events",
			"entry_titles", traceEntryTitles(eventsToEntries(msg.events, !m.directShellPending)),
			"event_count", len(msg.events),
			"error", errString(msg.err),
			"busy", m.busy,
			"active_execution", activeExecutionID(m.activeExecution),
		)
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
		logging.Trace(
			"tui.take_control.finished",
			"error", errString(msg.err),
			"resume_after_handoff", m.resumeAfterHandoff,
			"active_execution", activeExecutionID(m.activeExecution),
		)
		if msg.err != nil {
			m.handoffVisible = false
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  msg.err.Error(),
			})
			m.selectedEntry = len(m.entries) - 1
			m.clampSelection()
			return m, nil
		}
		m.handoffVisible = false
		if m.activeExecution != nil && m.activeExecution.State == controller.CommandExecutionHandoffActive {
			updated := *m.activeExecution
			if m.handoffPriorState != "" {
				updated.State = m.handoffPriorState
			} else if isAgentOwnedExecution(updated.Origin) {
				updated.State = controller.CommandExecutionBackgroundMonitor
			} else {
				updated.State = controller.CommandExecutionRunning
			}
			m.activeExecution = &updated
		}
		m.handoffPriorState = ""
		followUpCmds := []tea.Cmd{m.refreshShellContextCmd(), m.pollShellTailCmd(), m.pollActiveExecutionCmd()}
		if m.activeExecution != nil {
			followUpCmds = append(followUpCmds, tickBusy())
		}
		if m.activeExecution != nil && m.ctrl != nil {
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
			}, tickBusy(), m.refreshShellContextCmd(), m.pollShellTailCmd(), m.pollActiveExecutionCmd())
		}
		return m, tea.Batch(followUpCmds...)
	case refreshedShellContextMsg:
		if msg.context != nil {
			m.shellContext = *msg.context
		}
		return m, nil
	case shellTailMsg:
		if msg.err == nil {
			m.liveShellTail = msg.tail
			if m.activeExecution != nil {
				updated := *m.activeExecution
				updated.LatestOutputTail = msg.tail
				m.activeExecution = &updated
			}
		}
		return m, nil
	case activeExecutionMsg:
		m.syncActiveExecution(msg.execution)
		return m, nil
	case activeExecutionCheckInMsg:
		m.checkInInFlight = false
		if msg.err != nil {
			m.lastCheckInAt = time.Now()
			if errors.Is(msg.err, context.Canceled) {
				return m, nil
			}
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  "agent check-in: " + msg.err.Error(),
			})
			m.selectedEntry = len(m.entries) - 1
			m.clampSelection()
			if m.isTranscriptPinned() {
				m.scrollTranscriptToBottom()
			}
			return m, nil
		}
		if m.activeExecution == nil || m.activeExecution.ID != msg.executionID {
			return m, nil
		}
		m.lastCheckInAt = time.Now()
		if len(msg.events) == 0 {
			return m, nil
		}
		pinned := m.isTranscriptPinned()
		m.entries = append(m.entries, eventsToEntries(msg.events, true)...)
		if pinned {
			m.scrollTranscriptToBottom()
		} else {
			m.clampTranscriptScroll()
		}
		m.selectedEntry = len(m.entries) - 1
		m.clampSelection()
		return m, nil
	case shellInterruptMsg:
		logging.Trace("tui.shell_interrupt.finished", "error", errString(msg.err), "active_execution", activeExecutionID(m.activeExecution))
		if msg.err != nil {
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  "interrupt shell command: " + msg.err.Error(),
			})
			m.selectedEntry = len(m.entries) - 1
			m.clampSelection()
			return m, nil
		}
		execution := m.abandonControllerExecution("user interrupted the active shell command")
		m.handleInterruptedExecution()
		pinned := m.isTranscriptPinned()
		m.entries = append(m.entries, interruptedExecutionEntry(execution, "Interrupted by user."))
		if pinned {
			m.scrollTranscriptToBottom()
		} else {
			m.clampTranscriptScroll()
		}
		m.selectedEntry = len(m.entries) - 1
		m.clampSelection()
		return m, tea.Batch(m.pollShellTailCmd(), m.pollActiveExecutionCmd())
	case exitConfirmExpiredMsg:
		if msg.token == m.exitConfirmToken {
			m.clearExitConfirm()
		}
		return m, nil
	case fullscreenKeysSentMsg:
		pinned := m.isTranscriptPinned()
		if msg.err != nil {
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  "send fullscreen keys: " + msg.err.Error(),
			})
		} else {
			m.lastFullscreenKeys = msg.keys
			m.lastFullscreenKeysAt = time.Now()
			m.entries = append(m.entries, Entry{
				Title: "system",
				Body:  "Sent keys to active terminal app: " + previewFullscreenKeys(msg.keys),
			})
		}
		if pinned {
			m.scrollTranscriptToBottom()
		} else {
			m.clampTranscriptScroll()
		}
		m.selectedEntry = len(m.entries) - 1
		m.clampSelection()
		return m, tea.Batch(m.pollActiveExecutionCmd(), m.pollShellTailCmd())
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
		m.startupNotice = startupNoticeForProfile(msg.profile)
		m.onboardingOpen = false
		m.onboardingStep = onboardingStepProviders
		m.onboardingIndex = 0
		m.onboardingChoices = nil
		m.onboardingSelected = nil
		m.onboardingForm = nil
		m.onboardingModels = nil
		m.onboardingModelIdx = 0
		if msg.settingsStep != "" {
			m.settingsOpen = true
			m.settingsStep = msg.settingsStep
			m.settingsConfig = nil
			m.settingsProviderIdx = m.currentSettingsProviderIndex()
			if msg.settingsStep == settingsStepActiveModels {
				m.applySettingsModelFilter()
			}
		} else {
			m.settingsOpen = false
			m.settingsStep = settingsStepMenu
			m.settingsIndex = 0
			m.settingsProviders = nil
			m.settingsProviderIdx = 0
			m.settingsConfig = nil
			m.settingsModelCatalog = nil
			m.settingsModels = nil
			m.settingsModelIdx = 0
			m.settingsModelFilter = ""
			m.settingsModelInfo = false
		}
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
				Body:  providerPersistenceErrorBody(msg.profile, msg.persistErr),
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
	case settingsModelsLoadedMsg:
		m.busy = false
		m.busyStartedAt = time.Time{}
		m.inFlightCancel = nil
		if msg.err != nil {
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  fmt.Sprintf("load active models: %v", msg.err),
			})
			m.selectedEntry = len(m.entries) - 1
			m.clampSelection()
			return m, nil
		}
		m.settingsStep = settingsStepActiveModels
		m.settingsModelCatalog = append([]settingsModelChoice(nil), msg.choices...)
		m.settingsModelFilter = ""
		m.settingsModelInfo = false
		m.applySettingsModelFilter()
		return m, nil
	case settingsProviderSavedMsg:
		m.busy = false
		m.busyStartedAt = time.Time{}
		m.inFlightCancel = nil
		if msg.err != nil {
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  fmt.Sprintf("save provider settings: %v", msg.err),
			})
			m.selectedEntry = len(m.entries) - 1
			m.clampSelection()
			return m, nil
		}
		if msg.ctrl != nil {
			m.ctrl = msg.ctrl
			m.activeProvider = msg.profile
		}
		m.settingsStep = settingsStepProviders
		m.settingsConfig = nil
		m.settingsModelCatalog = nil
		m.settingsModels = nil
		m.settingsModelIdx = 0
		m.settingsModelFilter = ""
		m.settingsModelInfo = false
		if msg.persistErr != nil {
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  providerPersistenceErrorBody(msg.profile, msg.persistErr),
			})
		} else {
			body := fmt.Sprintf("Saved provider settings for %s.", msg.profile.Name)
			if msg.ctrl != nil {
				body = fmt.Sprintf("Saved and refreshed provider settings for %s.", msg.profile.Name)
			}
			m.entries = append(m.entries, Entry{
				Title: "system",
				Body:  body,
			})
		}
		m.selectedEntry = len(m.entries) - 1
		m.clampSelection()
		return m, nil
	case busyTickMsg:
		if !m.busy && m.activeExecution == nil {
			return m, nil
		}

		return m, tea.Batch(tickBusy(), m.pollShellTailCmd(), m.pollActiveExecutionCmd(), m.maybeExecutionCheckInCmd(time.Time(msg)))
	case tea.KeyMsg:
		if m.sendingFullscreenKeys {
			logging.Trace("tui.fullscreen_keys.key", "type", int(msg.Type), "text", msg.String(), "runes", string(msg.Runes))
		}
		if m.settingsOpen {
			return m.updateSettings(msg)
		}
		if m.onboardingOpen {
			return m.updateOnboarding(msg)
		}
		if m.detailOpen {
			return m.updateDetail(msg)
		}
		if handledModel, handled, cmd := m.handleActionCardKey(msg); handled {
			return handledModel, cmd
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			return m.handleComposerCtrlC()
		case tea.KeyF2:
			return m.takeControlNow()
		case tea.KeyF3:
			return m.openOnboarding()
		case tea.KeyF10:
			return m.openSettings()
		case tea.KeyEsc:
			if m.sendingFullscreenKeys {
				m.sendingFullscreenKeys = false
				m.setInput("")
				return m, nil
			}
			if m.busy || m.activeExecution != nil {
				return m.interruptInFlight()
			}
			if m.editingProposal != nil {
				m.pendingProposal = m.editingProposal
				m.editingProposal = nil
				m.setInput("")
				return m, nil
			}
			if m.refiningProposal != nil {
				m.pendingProposal = m.refiningProposal
				m.refiningProposal = nil
				m.setInput("")
				return m, nil
			}
			if m.refiningApproval != nil {
				m.pendingApproval = m.refiningApproval
				m.refiningApproval = nil
				m.setInput("")
				return m, nil
			}
			m.setInput("")
			return m, nil
		case tea.KeyTab:
			if m.sendingFullscreenKeys {
				return m, nil
			}
			if m.composerLocked() {
				return m, nil
			}
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
			if m.sendingFullscreenKeys {
				return m, nil
			}
			if m.composerLocked() {
				return m, nil
			}
			m.setInput(m.currentHistory().previous(m.input))
			return m, nil
		case tea.KeyDown:
			if msg.Alt {
				m.selectNextEntry()
				return m, nil
			}
			if m.sendingFullscreenKeys {
				return m, nil
			}
			if m.composerLocked() {
				return m, nil
			}
			m.setInput(m.currentHistory().next(m.input))
			return m, nil
		case tea.KeyLeft:
			if m.sendingFullscreenKeys {
				m.moveCursor(-1)
				return m, nil
			}
			if m.composerLocked() {
				return m, nil
			}
			m.moveCursor(-1)
			return m, nil
		case tea.KeyRight:
			if m.sendingFullscreenKeys {
				m.moveCursor(1)
				return m, nil
			}
			if m.composerLocked() {
				return m, nil
			}
			m.moveCursor(1)
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
			if !m.sendingFullscreenKeys && m.composerLocked() {
				return m, nil
			}
			m.insertTextAtCursor("\n")
			return m, nil
		case tea.KeyCtrlE:
			return m.primaryAction()
		case tea.KeyCtrlY:
			return m.primaryAction()
		case tea.KeyCtrlN:
			if m.pendingProposal != nil {
				return m.rejectProposal()
			}
			return m.decideApproval(controller.DecisionReject)
		case tea.KeyCtrlR:
			if m.pendingProposal != nil {
				return m.refineProposal()
			}
			return m.refineApproval()
		case tea.KeyCtrlT:
			if m.pendingProposal != nil {
				return m.editProposalCommand()
			}
			return m, nil
		case tea.KeyEnter:
			if m.startupNotice != nil {
				m.startupNotice = nil
				return m, nil
			}
			if !m.sendingFullscreenKeys && m.composerLocked() {
				return m, nil
			}
			return m.submit()
		case tea.KeyBackspace:
			if m.sendingFullscreenKeys {
				m.backspaceAtCursor()
				return m, nil
			}
			if m.composerLocked() {
				return m, nil
			}
			m.backspaceAtCursor()
			return m, nil
		case tea.KeyDelete:
			if m.sendingFullscreenKeys {
				m.deleteAtCursor()
				return m, nil
			}
			if m.composerLocked() {
				return m, nil
			}
			m.deleteAtCursor()
			return m, nil
		case tea.KeySpace:
			if m.sendingFullscreenKeys {
				m.insertTextAtCursor(" ")
				return m, nil
			}
			if m.composerLocked() {
				return m, nil
			}
			m.insertTextAtCursor(" ")
			return m, nil
		default:
			if !msg.Alt && msg.Type == tea.KeyRunes {
				if len(msg.Runes) == 1 && (msg.Runes[0] == '\r' || msg.Runes[0] == '\n') {
					if !m.sendingFullscreenKeys && m.composerLocked() {
						return m, nil
					}
					return m.submit()
				}
				if len(msg.Runes) == 1 && unicode.ToUpper(msg.Runes[0]) == 'S' && m.canSendActiveKeys() && strings.TrimSpace(m.input) == "" {
					m.sendingFullscreenKeys = true
					m.setInput("")
					return m, nil
				}
				if len(msg.Runes) == 1 && strings.TrimSpace(m.input) == "" && m.editingProposal == nil && m.refiningProposal == nil && m.refiningApproval == nil {
					switch msg.Runes[0] {
					case 'Y':
						return m.primaryAction()
					case 'N':
						if m.pendingProposal != nil {
							return m.rejectProposal()
						}
						return m.decideApproval(controller.DecisionReject)
					case 'R':
						if m.pendingProposal != nil {
							return m.refineProposal()
						}
						return m.refineApproval()
					case 'E':
						if m.pendingProposal != nil {
							return m.editProposalCommand()
						}
					}
				}
				if !m.sendingFullscreenKeys && m.composerLocked() {
					return m, nil
				}
				m.insertTextAtCursor(string(msg.Runes))
				return m, nil
			}
		}
	}

	return m, nil
}

func (m Model) composerLocked() bool {
	return (m.startupNotice != nil || m.pendingFullscreen != nil || m.pendingProposal != nil || m.pendingApproval != nil) && m.editingProposal == nil && m.refiningProposal == nil && m.refiningApproval == nil
}

func (m Model) handleActionCardKey(msg tea.KeyMsg) (tea.Model, bool, tea.Cmd) {
	if !m.composerLocked() {
		return m, false, nil
	}

	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 || msg.Alt {
		return m, false, nil
	}

	if m.startupNotice != nil {
		switch unicode.ToUpper(msg.Runes[0]) {
		case 'Y':
			m.startupNotice = nil
			return m, true, nil
		}
		return m, true, nil
	}

	if m.pendingFullscreen != nil {
		switch unicode.ToUpper(msg.Runes[0]) {
		case 'Y':
			model, cmd := m.confirmFullscreenAction()
			return model, true, cmd
		case 'N':
			m.pendingFullscreen = nil
			return m, true, nil
		}
		return m, true, nil
	}

	switch unicode.ToUpper(msg.Runes[0]) {
	case 'Y':
		model, cmd := m.primaryAction()
		return model, true, cmd
	case 'N':
		if m.pendingProposal != nil {
			model, cmd := m.rejectProposal()
			return model, true, cmd
		}
		model, cmd := m.decideApproval(controller.DecisionReject)
		return model, true, cmd
	case 'R':
		if m.pendingProposal != nil {
			model, cmd := m.refineProposal()
			return model, true, cmd
		}
		model, cmd := m.refineApproval()
		return model, true, cmd
	case 'E':
		if m.pendingProposal != nil {
			model, cmd := m.editProposalCommand()
			return model, true, cmd
		}
	}

	return m, true, nil
}

func (m Model) View() string {
	if m.settingsOpen {
		return m.renderSettingsView()
	}
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
	activeExecutionCard := m.renderActiveExecutionCard(actionWidth)
	statusLine := m.renderStatusLine(statusWidth)
	shellTail := m.renderShellTail(statusWidth)
	composer := m.renderComposer(composerWidth)
	footer := m.renderFooter(footerWidth)

	screenHeight := m.height
	if screenHeight <= 0 {
		screenHeight = 24
	}

	transcriptHeight := m.transcriptViewportHeight(actionCard, planCard, activeExecutionCard, statusLine, shellTail, composer, footer, screenHeight)

	transcript := m.renderTranscript(transcriptWidth, transcriptHeight)

	sections := []string{transcript}
	if actionCard != "" {
		sections = append(sections, actionCard)
	}
	if planCard != "" {
		sections = append(sections, planCard)
	}
	if activeExecutionCard != "" {
		sections = append(sections, activeExecutionCard)
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
	if m.startupNotice != nil && !m.sendingFullscreenKeys {
		return m, nil
	}

	text := strings.TrimSpace(m.input)
	if m.sendingFullscreenKeys {
		rawKeys := normalizeFullscreenKeys(m.input)
		if rawKeys == "" {
			return m, nil
		}
		logging.Trace("tui.fullscreen_keys.submit", "keys", rawKeys)
		m.sendingFullscreenKeys = false
		m.setInput("")
		return m, sendFullscreenKeysCmd(m.takeControl, rawKeys)
	}

	recoveryAgentPrompt := m.mode == AgentMode && m.activeExecution != nil
	if m.busy && !recoveryAgentPrompt {
		return m, nil
	}

	if text == "" {
		return m, nil
	}

	if m.editingProposal != nil {
		logging.Trace("tui.proposal.edit.submit", "command", text)
		return m.submitEditedProposal(text)
	}

	if handled, next, cmd := m.handleComposerCommand(text); handled {
		return next, cmd
	}

	m.setInput("")
	m.currentHistory().record(text)

	if m.mode == AgentMode {
		logging.Trace(
			"tui.submit.agent",
			"prompt", text,
			"refining_approval", m.refiningApproval != nil,
			"refining_proposal", m.refiningProposal != nil,
		)
		if m.ctrl == nil {
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  "controller is not available",
			})
			return m, nil
		}

		m.busy = true
		m.busyStartedAt = time.Now()
		if !recoveryAgentPrompt {
			m.showShellTail = false
			m.liveShellTail = ""
			m.syncActiveExecution(nil)
		}
		prompt := text
		refining := m.refiningApproval
		refiningProposal := m.refiningProposal
		m.refiningApproval = nil
		m.refiningProposal = nil
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
			} else if refiningProposal != nil {
				events, err = m.ctrl.SubmitProposalRefinement(ctx, *refiningProposal, prompt)
			} else {
				events, err = m.ctrl.SubmitAgentPrompt(ctx, prompt)
			}
			return controllerEventsMsg{
				events: events,
				err:    err,
			}
		}, tickBusy(), m.pollShellTailCmd(), m.pollActiveExecutionCmd())
	}

	if m.ctrl == nil {
		m.entries = append(m.entries, Entry{
			Title: "error",
			Body:  "controller is not available",
		})
		return m, nil
	}
	if m.shouldConfirmFullscreenBeforeShellAction() {
		m.pendingFullscreen = &fullscreenAction{
			Kind:    fullscreenActionShellSubmit,
			Command: text,
		}
		return m, nil
	}

	m.busy = true
	m.busyStartedAt = time.Now()
	m.directShellPending = true
	m.showShellTail = true
	command := text
	logging.Trace("tui.submit.shell", "command", command)
	m.syncActiveExecution(newLocalExecution(command, controller.CommandOriginUserShell))
	ctx, cancel := context.WithCancel(context.Background())
	m.inFlightCancel = cancel
	return m, tea.Batch(func() tea.Msg {
		defer cancel()

		events, err := m.ctrl.SubmitShellCommand(ctx, command)
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}, tickBusy(), m.pollShellTailCmd(), m.pollActiveExecutionCmd())
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
		logging.Trace("tui.primary_action", "action", "approve", "approval_id", m.pendingApproval.ID)
		return m.decideApproval(controller.DecisionApprove)
	case m.pendingProposal != nil && m.pendingProposal.Keys != "":
		logging.Trace("tui.primary_action", "action", "send_proposal_keys", "keys", previewFullscreenKeys(m.pendingProposal.Keys))
		return m.runProposalKeys()
	case m.pendingProposal != nil && m.pendingProposal.Command != "":
		logging.Trace("tui.primary_action", "action", "run_proposal", "command", m.pendingProposal.Command)
		return m.runProposalCommand()
	case m.activePlan != nil:
		logging.Trace("tui.primary_action", "action", "continue_plan", "summary", m.activePlan.Summary)
		return m.continueActivePlan()
	default:
		return m, nil
	}
}

func (m Model) confirmFullscreenAction() (tea.Model, tea.Cmd) {
	if m.pendingFullscreen == nil || m.ctrl == nil {
		return m, nil
	}

	action := *m.pendingFullscreen
	m.pendingFullscreen = nil

	switch action.Kind {
	case fullscreenActionShellSubmit:
		m.busy = true
		m.busyStartedAt = time.Now()
		m.directShellPending = true
		m.showShellTail = true
		m.syncActiveExecution(newLocalExecution(action.Command, controller.CommandOriginUserShell))
		m.setInput("")
		ctx, cancel := context.WithCancel(context.Background())
		m.inFlightCancel = cancel
		return m, tea.Batch(func() tea.Msg {
			defer cancel()
			events, err := m.ctrl.SubmitShellCommand(ctx, action.Command)
			return controllerEventsMsg{events: events, err: err}
		}, tickBusy(), m.pollShellTailCmd(), m.pollActiveExecutionCmd())
	case fullscreenActionProposalRun:
		if m.pendingProposal == nil || strings.TrimSpace(m.pendingProposal.Command) == "" {
			return m, nil
		}
		logging.Trace("tui.proposal.run", "command", m.pendingProposal.Command)
		m.busy = true
		m.busyStartedAt = time.Now()
		m.proposalRunPending = true
		m.showShellTail = true
		command := m.pendingProposal.Command
		m.pendingProposal = nil
		m.refiningProposal = nil
		m.editingProposal = nil
		m.syncActiveExecution(newLocalExecution(command, controller.CommandOriginAgentProposal))
		ctx, cancel := context.WithCancel(context.Background())
		m.inFlightCancel = cancel

		return m, tea.Batch(func() tea.Msg {
			defer cancel()
			events, err := m.ctrl.SubmitProposedShellCommand(ctx, command)
			return controllerEventsMsg{events: events, err: err}
		}, tickBusy(), m.pollShellTailCmd(), m.pollActiveExecutionCmd())
	case fullscreenActionApprovalRun:
		if m.pendingApproval == nil || m.pendingApproval.ID != action.ApprovalID {
			return m, nil
		}
		logging.Trace(
			"tui.approval.decide",
			"approval_id", m.pendingApproval.ID,
			"decision", controller.DecisionApprove,
			"command", m.pendingApproval.Command,
		)
		m.busy = true
		m.busyStartedAt = time.Now()
		m.approvalInFlight = true
		m.showShellTail = true
		approvalID := m.pendingApproval.ID
		command := m.pendingApproval.Command
		m.pendingApproval = nil
		m.pendingProposal = nil
		m.refiningApproval = nil
		m.refiningProposal = nil
		m.editingProposal = nil
		m.syncActiveExecution(newLocalExecution(command, controller.CommandOriginAgentApproval))
		ctx, cancel := context.WithCancel(context.Background())
		m.inFlightCancel = cancel

		return m, tea.Batch(func() tea.Msg {
			defer cancel()
			events, err := m.ctrl.DecideApproval(ctx, approvalID, controller.DecisionApprove, "")
			return controllerEventsMsg{events: events, err: err}
		}, tickBusy(), m.pollShellTailCmd(), m.pollActiveExecutionCmd())
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

	logging.Trace("tui.plan.continue", "summary", m.activePlan.Summary)

	m.busy = true
	m.busyStartedAt = time.Now()
	m.showShellTail = false
	m.liveShellTail = ""
	m.activeExecution = nil
	ctx, cancel := context.WithTimeout(context.Background(), agentTurnTimeout)
	m.inFlightCancel = cancel
	return m, tea.Batch(func() tea.Msg {
		defer cancel()

		events, err := m.ctrl.ContinueActivePlan(ctx)
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}, tickBusy(), m.pollShellTailCmd(), m.pollActiveExecutionCmd())
}

func (m Model) takeControlNow() (tea.Model, tea.Cmd) {
	if !m.takeControl.enabled() {
		return m, nil
	}

	logging.Trace(
		"tui.take_control.start",
		"busy", m.busy,
		"active_execution", activeExecutionID(m.activeExecution),
		"resume_after_handoff", m.resumeAfterHandoff,
	)

	if m.inFlightCancel != nil && m.activeExecution == nil {
		m.resumeAfterHandoff = !m.directShellPending && (m.approvalInFlight || m.proposalRunPending || m.mode == AgentMode || m.activePlan != nil)
		m.suppressCancelErr = true
		m.inFlightCancel()
		m.inFlightCancel = nil
	}
	if m.activeExecution != nil {
		updated := *m.activeExecution
		m.handoffPriorState = updated.State
		updated.State = controller.CommandExecutionHandoffActive
		m.activeExecution = &updated
		m.handoffVisible = true
	}

	m.busy = false
	m.busyStartedAt = time.Time{}
	if m.activeExecution == nil {
		m.approvalInFlight = false
		m.proposalRunPending = false
		m.directShellPending = false
	}
	if !m.resumeAfterHandoff && m.activeExecution == nil {
		m.showShellTail = false
		m.liveShellTail = ""
	}

	return m, newTakeControlCmd(m.takeControl)
}

func (m Model) interruptInFlight() (tea.Model, tea.Cmd) {
	logging.Trace(
		"tui.interrupt.start",
		"busy", m.busy,
		"active_execution", activeExecutionID(m.activeExecution),
	)
	if m.activeExecution != nil && m.takeControl.enabled() {
		if m.activeExecution.State == controller.CommandExecutionInteractiveFullscreen {
			m.appendInterruptNotice("Fullscreen app is still active. Use F2 to take control and exit it manually, or use KEYS> to send input.")
			return m, nil
		}
		if !m.canAttemptLocalInterrupt() {
			m.appendInterruptNotice("Active command is not confirmed local. Use F2 to take control and interrupt it manually.")
			return m, nil
		}
		m.showShellTail = true
		return m, interruptShellCmd(m.takeControl)
	}

	if m.inFlightCancel != nil {
		m.suppressCancelErr = true
		m.inFlightCancel()
		m.inFlightCancel = nil
	}
	m.busy = false
	m.busyStartedAt = time.Time{}
	return m, nil
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

func (m Model) openSettings() (tea.Model, tea.Cmd) {
	if m.loadOnboarding == nil {
		m.entries = append(m.entries, Entry{
			Title: "error",
			Body:  "settings are not configured in this session",
		})
		m.selectedEntry = len(m.entries) - 1
		m.clampSelection()
		return m, nil
	}

	choices, err := m.loadOnboarding()
	if err != nil {
		m.entries = append(m.entries, Entry{
			Title: "error",
			Body:  fmt.Sprintf("load settings providers: %v", err),
		})
		m.selectedEntry = len(m.entries) - 1
		m.clampSelection()
		return m, nil
	}

	m.settingsOpen = true
	m.settingsStep = settingsStepMenu
	m.settingsIndex = 0
	m.settingsProviders = buildSettingsProviderEntries(choices)
	m.settingsProviderIdx = m.currentSettingsProviderIndex()
	m.settingsConfig = nil
	m.settingsModelCatalog = nil
	m.settingsModels = nil
	m.settingsModelIdx = 0
	m.settingsModelFilter = ""
	m.settingsModelInfo = false
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

func (m Model) pollActiveExecutionCmd() tea.Cmd {
	if m.ctrl == nil {
		return nil
	}

	return func() tea.Msg {
		return activeExecutionMsg{execution: m.ctrl.ActiveExecution()}
	}
}

func interruptShellCmd(config takeControlConfig) tea.Cmd {
	if !config.enabled() {
		return nil
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		client, err := tmux.NewClient(config.SocketName)
		if err != nil {
			return shellInterruptMsg{err: err}
		}
		err = client.SendKeys(ctx, config.TopPaneID, "C-c", false)
		return shellInterruptMsg{err: err}
	}
}

func sendFullscreenKeysCmd(config takeControlConfig, keys string) tea.Cmd {
	if !config.enabled() {
		return nil
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		client, err := tmux.NewClient(config.SocketName)
		if err != nil {
			return fullscreenKeysSentMsg{keys: keys, err: err}
		}

		parts := strings.Split(strings.ReplaceAll(keys, "\r\n", "\n"), "\n")
		for index, part := range parts {
			if part != "" {
				if err := client.SendLiteralKeys(ctx, config.TopPaneID, part); err != nil {
					return fullscreenKeysSentMsg{keys: keys, err: err}
				}
			}
			if index < len(parts)-1 {
				if err := client.SendKeys(ctx, config.TopPaneID, "Enter", false); err != nil {
					return fullscreenKeysSentMsg{keys: keys, err: err}
				}
			}
		}

		return fullscreenKeysSentMsg{keys: keys}
	}
}

func (m *Model) handleInterruptedExecution() {
	logging.Trace("tui.execution.interrupted", "active_execution", activeExecutionID(m.activeExecution))
	m.busy = false
	m.busyStartedAt = time.Time{}
	m.showShellTail = false
	m.liveShellTail = ""
	m.suppressCancelErr = true
	m.handoffVisible = false
	m.handoffPriorState = ""
	if m.inFlightCancel != nil {
		m.inFlightCancel()
		m.inFlightCancel = nil
	}
	m.proposalRunPending = false
	m.approvalInFlight = false
	m.directShellPending = false
	m.syncActiveExecution(nil)
}

func (m *Model) abandonControllerExecution(reason string) *controller.CommandExecution {
	if m.ctrl == nil {
		return nil
	}

	execution := m.ctrl.AbandonActiveExecution(reason)
	if execution != nil && strings.TrimSpace(execution.LatestOutputTail) != "" {
		m.liveShellTail = execution.LatestOutputTail
	}
	return execution
}

func interruptedExecutionEntry(execution *controller.CommandExecution, summary string) Entry {
	if execution == nil {
		return Entry{
			Title: "system",
			Body:  summary,
		}
	}

	bodyLines := []string{"status=canceled"}
	if strings.TrimSpace(execution.LatestOutputTail) != "" {
		bodyLines = append(bodyLines, compactResultPreview(strings.TrimSpace(execution.LatestOutputTail), 6))
	} else {
		bodyLines = append(bodyLines, "(no output)")
	}

	detail := []string{
		"command:",
		execution.Command,
		"",
		"status:",
		"canceled",
	}
	if strings.TrimSpace(summary) != "" {
		detail = append(detail, "", "summary:", summary)
	}
	if strings.TrimSpace(execution.LatestOutputTail) != "" {
		detail = append(detail, "", "output so far:", strings.TrimSpace(execution.LatestOutputTail))
	}

	return Entry{
		Title:  "result",
		Body:   strings.Join(bodyLines, "\n"),
		Detail: strings.Join(detail, "\n"),
	}
}

func activeExecutionID(execution *controller.CommandExecution) string {
	if execution == nil {
		return ""
	}

	return execution.ID
}

func errString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

func traceEntryTitles(entries []Entry) []string {
	titles := make([]string, 0, len(entries))
	for _, entry := range entries {
		titles = append(titles, entry.Title)
	}
	return titles
}

func (m *Model) maybeExecutionCheckInCmd(now time.Time) tea.Cmd {
	if m.ctrl == nil || m.checkInInFlight || m.activeExecution == nil {
		return nil
	}
	if !isAgentOwnedExecution(m.activeExecution.Origin) {
		return nil
	}
	switch m.activeExecution.State {
	case controller.CommandExecutionHandoffActive, controller.CommandExecutionCompleted, controller.CommandExecutionFailed, controller.CommandExecutionCanceled, controller.CommandExecutionLost:
		return nil
	}

	dueAt := m.activeExecution.StartedAt.Add(firstCheckInDelay)
	if !m.lastCheckInAt.IsZero() {
		dueAt = m.lastCheckInAt.Add(repeatCheckInDelay)
	}
	if now.Before(dueAt) {
		return nil
	}

	m.checkInInFlight = true
	executionID := m.activeExecution.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), agentTurnTimeout)
		defer cancel()

		events, err := m.ctrl.CheckActiveExecution(ctx)
		return activeExecutionCheckInMsg{
			executionID: executionID,
			events:      events,
			err:         err,
		}
	}
}

func (m *Model) syncActiveExecution(execution *controller.CommandExecution) {
	currentID := ""
	if m.activeExecution != nil {
		currentID = m.activeExecution.ID
	}

	if execution == nil {
		m.activeExecution = nil
		m.pendingFullscreen = nil
		m.lastFullscreenKeys = ""
		m.lastFullscreenKeysAt = time.Time{}
		m.checkInInFlight = false
		m.lastCheckInAt = time.Time{}
		m.lastInterruptNoticeID = ""
		m.handoffVisible = false
		m.handoffPriorState = ""
		return
	}

	if currentID != execution.ID {
		m.lastFullscreenKeys = ""
		m.lastFullscreenKeysAt = time.Time{}
		m.checkInInFlight = false
		m.lastCheckInAt = time.Time{}
		m.lastInterruptNoticeID = ""
		m.handoffVisible = false
		m.handoffPriorState = ""
	}
	if m.activeExecution != nil &&
		m.activeExecution.ID == execution.ID &&
		m.handoffVisible &&
		m.activeExecution.State == controller.CommandExecutionHandoffActive &&
		execution.State != controller.CommandExecutionCompleted &&
		execution.State != controller.CommandExecutionFailed &&
		execution.State != controller.CommandExecutionCanceled &&
		execution.State != controller.CommandExecutionLost {
		executionCopy := *execution
		executionCopy.State = controller.CommandExecutionHandoffActive
		execution = &executionCopy
	}
	m.activeExecution = execution
	if execution.State != controller.CommandExecutionInteractiveFullscreen {
		m.pendingFullscreen = nil
		m.lastFullscreenKeys = ""
		m.lastFullscreenKeysAt = time.Time{}
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
		return m, nil
	case tea.KeyEsc:
		switch m.settingsStep {
		case settingsStepProviderForm:
			m.settingsStep = settingsStepProviders
			m.settingsConfig = nil
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
		case settingsStepActiveProvider:
			m.settingsStep = settingsStepMenu
		case settingsStepProviders:
			m.settingsStep = settingsStepMenu
		default:
			m.settingsOpen = false
		}
		return m, nil
	case tea.KeyUp:
		switch m.settingsStep {
		case settingsStepMenu:
			if m.settingsIndex > 0 {
				m.settingsIndex--
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
			if m.settingsIndex < len(settingsMenuEntries())-1 {
				m.settingsIndex++
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
		ctrl, profile, err := m.switchProvider(profile, shellContext)
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
			m.settingsStep = settingsStepProviders
			m.settingsProviderIdx = m.currentSettingsProviderIndex()
			m.settingsModelCatalog = nil
			m.settingsModels = nil
			m.settingsModelIdx = 0
			m.settingsModelFilter = ""
			m.settingsModelInfo = false
			return m, nil
		}
		if m.settingsIndex == 1 {
			m.settingsStep = settingsStepActiveProvider
			m.settingsProviderIdx = m.currentSettingsProviderIndex()
			return m, nil
		}
		return m.loadSettingsModels()
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
		return m.saveSettingsProfile()
	default:
		return m, nil
	}
}

func (m Model) loadSettingsModels() (tea.Model, tea.Cmd) {
	profiles := m.settingsConfiguredProfiles()
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

func (m Model) saveSettingsProfile() (tea.Model, tea.Cmd) {
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
		if profile.Preset != m.activeProvider.Preset || m.switchProvider == nil {
			return settingsProviderSavedMsg{profile: profile, persistErr: persistErr}
		}
		ctrl, switchedProfile, err := m.switchProvider(profile, m.currentShellContext())
		return settingsProviderSavedMsg{
			ctrl:       ctrl,
			profile:    switchedProfile,
			err:        err,
			persistErr: persistErr,
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
	} else if m.pendingProposal != nil && (m.pendingProposal.Command != "" || m.pendingProposal.Keys != "") {
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
	if m.pendingFullscreen != nil {
		body := []string{
			"A fullscreen terminal app still appears active in the shell pane.",
			"command: " + m.pendingFullscreen.Command,
			"Y send anyway  N cancel  F2 take control",
		}
		content := lipgloss.JoinVertical(
			lipgloss.Left,
			m.styles.actionTitle.Render("Fullscreen Still Active"),
			m.styles.actionBody.Render(strings.Join(body, "\n")),
		)
		return m.styles.actionCard.BorderForeground(lipgloss.Color("214")).Width(width).Render(content)
	}

	if m.startupNotice != nil {
		body := []string{
			m.startupNotice.Body,
			"Y continue  F10 settings",
		}
		content := lipgloss.JoinVertical(
			lipgloss.Left,
			m.styles.actionTitle.Render(m.startupNotice.Title),
			m.styles.actionBody.Render(strings.Join(body, "\n")),
		)
		return m.styles.actionCard.BorderForeground(lipgloss.Color("214")).Width(width).Render(content)
	}

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
		body = append(body, "Y continue  N reject  R refine")
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

	if m.refiningProposal != nil {
		body := []string{}
		if m.refiningProposal.Description != "" {
			body = append(body, m.refiningProposal.Description)
		}
		if m.refiningProposal.Command != "" {
			body = append(body, "command: "+m.refiningProposal.Command)
		}
		body = append(body, "Enter a refinement note in the composer and press Enter")
		content := lipgloss.JoinVertical(
			lipgloss.Left,
			m.styles.actionTitle.Render("Refining Proposal"),
			m.styles.actionBody.Render(strings.Join(body, "\n")),
		)
		return m.styles.actionCard.BorderForeground(lipgloss.Color("214")).Width(width).Render(content)
	}

	if m.editingProposal != nil {
		body := []string{}
		if m.editingProposal.Description != "" {
			body = append(body, m.editingProposal.Description)
		}
		if m.editingProposal.Command != "" {
			body = append(body, "command: "+m.editingProposal.Command)
		}
		body = append(body, "Edit the command directly. Enter saves changes. Esc cancels.")
		content := lipgloss.JoinVertical(
			lipgloss.Left,
			m.styles.actionTitle.Render("Editing Proposed Command"),
			m.styles.actionBody.Render(strings.Join(body, "\n")),
		)
		return m.styles.actionCard.BorderForeground(lipgloss.Color("111")).Width(width).Render(content)
	}

	if m.pendingProposal != nil && m.pendingProposal.Keys != "" {
		body := []string{}
		if m.pendingProposal.Description != "" {
			body = append(body, m.pendingProposal.Description)
		}
		body = append(body, "keys: "+previewFullscreenKeys(m.pendingProposal.Keys))
		body = append(body, "Y send keys  N reject  R ask agent")
		content := lipgloss.JoinVertical(
			lipgloss.Left,
			m.styles.actionTitle.Render("Proposed Terminal Input"),
			m.styles.actionBody.Render(strings.Join(body, "\n")),
		)
		return m.styles.actionCard.BorderForeground(lipgloss.Color("31")).Width(width).Render(content)
	}

	if m.pendingProposal != nil && m.pendingProposal.Command != "" {
		body := []string{}
		if m.pendingProposal.Description != "" {
			body = append(body, m.pendingProposal.Description)
		}
		body = append(body, "command: "+m.pendingProposal.Command)
		body = append(body, "Y continue  N reject  R ask agent  E tweak command")
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
	body = append(body, "Y continue")

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		m.styles.actionTitle.Render("Active Plan"),
		m.styles.actionBody.Render(strings.Join(body, "\n")),
	)
	return m.styles.actionCard.BorderForeground(lipgloss.Color("63")).Width(width).Render(content)
}

func (m Model) renderActiveExecutionCard(width int) string {
	if m.activeExecution == nil {
		return ""
	}

	body := []string{
		fmt.Sprintf("state: %s", humanizeExecutionState(m.activeExecution.State)),
		fmt.Sprintf("origin: %s", humanizeExecutionOrigin(m.activeExecution.Origin)),
		fmt.Sprintf("elapsed: %s", humanizeExecutionElapsed(m.activeExecution.StartedAt)),
		"command: " + m.activeExecution.Command,
	}
	if m.activeExecution.State == controller.CommandExecutionInteractiveFullscreen {
		body = append(body, "Fullscreen terminal app detected.")
		if strings.TrimSpace(m.lastFullscreenKeys) != "" {
			body = append(body, "last keys: "+previewFullscreenKeys(m.lastFullscreenKeys))
		}
		body = append(body, "F2 take control  S send keys")
		body = append(body, "Exit or control the fullscreen app manually from the shell view.")
	} else if strings.TrimSpace(m.activeExecution.LatestOutputTail) != "" {
		lines := strings.Split(strings.TrimSpace(m.activeExecution.LatestOutputTail), "\n")
		if len(lines) > 2 {
			lines = lines[len(lines)-2:]
		}
		body = append(body, "tail: "+strings.Join(lines, " | "))
		body = append(body, "F2 take control")
	} else {
		body = append(body, "F2 take control")
	}

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		m.styles.actionTitle.Render("Active Command"),
		m.styles.actionBody.Render(strings.Join(body, "\n")),
	)

	borderColor := lipgloss.Color("31")
	if m.activeExecution.State == controller.CommandExecutionHandoffActive {
		borderColor = lipgloss.Color("214")
	}
	return m.styles.actionCard.BorderForeground(borderColor).Width(width).Render(content)
}

func (m Model) renderTranscript(width int, height int) string {
	lines := m.transcriptWindow(m.transcriptLines(width), height)
	return m.styles.transcript.Width(width).MaxWidth(width).Render(strings.Join(lines, "\n"))
}

func (m Model) renderComposer(width int) string {
	composerStyle := m.styles.composerShell
	if m.refiningApproval != nil || m.refiningProposal != nil || m.editingProposal != nil {
		composerStyle = m.styles.composerRefine
	} else if m.mode == AgentMode {
		composerStyle = m.styles.composerAgent
	}

	promptStyle := m.styles.composerPromptShell
	prompt := "$>"
	switch {
	case m.sendingFullscreenKeys:
		promptStyle = m.styles.composerPromptRefine
		prompt = "KEYS>"
	case m.editingProposal != nil:
		promptStyle = m.styles.composerPromptRefine
		prompt = "CMD>"
	case m.refiningApproval != nil || m.refiningProposal != nil:
		promptStyle = m.styles.composerPromptRefine
		prompt = "Œ>"
	case m.mode == AgentMode:
		promptStyle = m.styles.composerPromptAgent
		prompt = "Œ>"
	case m.shellContext.Root:
		promptStyle = m.styles.composerPromptShell
		prompt = "#>"
	}

	cursorStyle := m.styles.input.Copy().Reverse(true)
	lines := composerDisplayLines(m.input, m.cursor)
	prefixWidth := lipgloss.Width(prompt)
	rendered := make([]string, 0, len(lines))
	for index, line := range lines {
		lineBody := renderComposerLine(line, cursorStyle, m.styles.input)
		if index == 0 {
			rendered = append(rendered, lipgloss.JoinHorizontal(lipgloss.Left, promptStyle.Render(prompt), m.styles.input.Render(" "), lineBody))
			continue
		}
		rendered = append(rendered, m.styles.input.Render(strings.Repeat(" ", prefixWidth+1))+lineBody)
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
	if m.exitConfirmActive() {
		rightParts = append(rightParts, m.styles.statusBusy.Render("Hit Ctrl-C again to exit"))
	}
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
	if m.activeExecution != nil && m.activeExecution.State == controller.CommandExecutionInteractiveFullscreen {
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
	escHint := "[Esc] clear"
	if m.busy || m.activeExecution != nil {
		escHint = "[Esc] interrupt"
	}
	if m.activeExecution != nil && !m.canAttemptLocalInterrupt() {
		escHint = "[Esc] manual"
	}

	switch {
	case width < 72:
		parts := []string{"[Tab]", "[Pg]", "[Enter]", "[Esc]", "[F2]", "[F3]", "[F10]", "[Ctrl+O]", "[Ctrl+C]"}
		if m.canSendActiveKeys() {
			parts = append(parts, "[S]")
		}
		if m.startupNotice != nil {
			parts = append(parts, "[Y]")
		} else if m.pendingFullscreen != nil {
			parts = append(parts, "[Y/N]")
		} else if m.pendingApproval != nil {
			parts = append(parts, "[Y/N/R]")
		} else if m.editingProposal != nil {
			parts = append(parts, "[Enter]")
		} else if m.refiningApproval != nil || m.refiningProposal != nil {
			parts = append(parts, "[Enter]")
		} else if m.pendingProposal != nil && m.pendingProposal.Keys != "" {
			parts = append(parts, "[Y/N/R]")
		} else if m.pendingProposal != nil && m.pendingProposal.Command != "" {
			parts = append(parts, "[Y/N/R/E]")
		} else if m.activePlan != nil {
			parts = append(parts, "[Y]")
		}
		return parts
	case width < 100:
		parts := []string{"[Tab] mode", "[Alt+Up/Down] entry", "[Ctrl+O] detail", "[PgUp/PgDn] scroll", "[Enter] submit", escHint, "[F2] shell", "[F3] providers", "[F10] settings", "[Ctrl+C] quit"}
		if m.canSendActiveKeys() {
			parts = append(parts, "[S] keys")
		}
		if m.startupNotice != nil {
			parts = append(parts, "[Y] continue")
		} else if m.pendingFullscreen != nil {
			parts = append(parts, "[Y/N] fullscreen")
		} else if m.pendingApproval != nil {
			parts = append(parts, "[Y/N/R]")
		} else if m.editingProposal != nil {
			parts = append(parts, "[Enter] save")
		} else if m.refiningApproval != nil || m.refiningProposal != nil {
			parts = append(parts, "[Enter] refine")
		} else if m.pendingProposal != nil && m.pendingProposal.Keys != "" {
			parts = append(parts, "[Y/N/R]")
		} else if m.pendingProposal != nil && m.pendingProposal.Command != "" {
			parts = append(parts, "[Y/N/R/E]")
		} else if m.activePlan != nil {
			parts = append(parts, "[Y] plan")
		}
		return parts
	}

	parts := []string{"[Tab] mode", "[Up/Down] history", "[Alt+Up/Down] entry", "[Ctrl+O] detail", "[PgUp/PgDn] scroll", "[Ctrl+U/D] half-page", "[Home/End] bounds", "[Enter] submit", escHint, "[Ctrl+J] newline", "[F2] shell", "[F3] providers", "[F10] settings"}
	if m.canSendActiveKeys() {
		parts = append(parts, "[S] send keys")
	}
	if m.startupNotice != nil {
		parts = append(parts, "[Y] continue")
	} else if m.pendingFullscreen != nil {
		parts = append(parts, "[Y] send anyway", "[N] cancel")
	} else if m.pendingApproval != nil {
		parts = append(parts, "[Y] continue", "[N] reject", "[R] refine")
	} else if m.editingProposal != nil {
		parts = append(parts, "[Enter] save edited command")
	} else if m.refiningApproval != nil || m.refiningProposal != nil {
		parts = append(parts, "[Enter] submit refine note")
	} else if m.pendingProposal != nil && m.pendingProposal.Keys != "" {
		parts = append(parts, "[Y] send keys", "[N] reject", "[R] ask agent")
	} else if m.pendingProposal != nil && m.pendingProposal.Command != "" {
		parts = append(parts, "[Y] continue", "[N] reject", "[R] ask agent", "[E] tweak command")
	} else if m.activePlan != nil {
		parts = append(parts, "[Y] continue plan")
	}
	parts = append(parts, "[Ctrl+C] quit")
	return parts
}

func startupNoticeForProfile(profile provider.Profile) *startupSecurityNotice {
	if strings.TrimSpace(profile.APIKeyEnvVar) != "local_file" {
		return nil
	}
	return &startupSecurityNotice{
		Title: "Less Secure Secret Storage",
		Body:  fmt.Sprintf("The active provider %s is using a locally stored plaintext secret file. This is less secure than OS keyring storage.", profile.Name),
	}
}

func (m *Model) currentHistory() *composerHistory {
	if m.mode == AgentMode {
		return &m.agentHistory
	}

	return &m.shellHistory
}

func (m *Model) setInput(value string) {
	m.input = value
	m.cursor = utf8.RuneCountInString(value)
	if strings.TrimSpace(value) != "" {
		m.clearExitConfirm()
	}
}

func (m *Model) clampCursor() {
	maxCursor := utf8.RuneCountInString(m.input)
	if m.cursor < 0 {
		m.cursor = 0
		return
	}
	if m.cursor > maxCursor {
		m.cursor = maxCursor
	}
}

func (m *Model) moveCursor(delta int) {
	m.cursor += delta
	m.clampCursor()
}

func (m *Model) insertTextAtCursor(value string) {
	runes := []rune(m.input)
	index := m.cursor
	if index < 0 {
		index = 0
	}
	if index > len(runes) {
		index = len(runes)
	}
	inserted := []rune(value)
	runes = append(runes[:index], append(inserted, runes[index:]...)...)
	m.input = string(runes)
	m.cursor = index + len(inserted)
	if strings.TrimSpace(m.input) != "" {
		m.clearExitConfirm()
	}
}

func (m *Model) backspaceAtCursor() {
	runes := []rune(m.input)
	if m.cursor <= 0 || len(runes) == 0 {
		return
	}
	index := m.cursor
	if index > len(runes) {
		index = len(runes)
	}
	runes = append(runes[:index-1], runes[index:]...)
	m.input = string(runes)
	m.cursor = index - 1
	if strings.TrimSpace(m.input) != "" {
		m.clearExitConfirm()
	}
}

func (m *Model) deleteAtCursor() {
	runes := []rune(m.input)
	if len(runes) == 0 || m.cursor >= len(runes) {
		return
	}
	index := m.cursor
	if index < 0 {
		index = 0
	}
	runes = append(runes[:index], runes[index+1:]...)
	m.input = string(runes)
	m.cursor = index
	if strings.TrimSpace(m.input) != "" {
		m.clearExitConfirm()
	}
}

func (m Model) handleComposerCtrlC() (tea.Model, tea.Cmd) {
	if m.exitConfirmActive() {
		return m, tea.Quit
	}

	if m.input != "" {
		m.setInput("")
	}
	return m.armExitConfirm()
}

func (m Model) armExitConfirm() (tea.Model, tea.Cmd) {
	m.exitConfirmToken++
	token := m.exitConfirmToken
	m.exitConfirmUntil = time.Now().Add(3 * time.Second)
	return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg {
		return exitConfirmExpiredMsg{token: token}
	})
}

func (m *Model) clearExitConfirm() {
	m.exitConfirmUntil = time.Time{}
	m.exitConfirmToken = 0
}

func (m Model) exitConfirmActive() bool {
	return !m.exitConfirmUntil.IsZero() && time.Now().Before(m.exitConfirmUntil)
}

func (m Model) transcriptViewportHeight(actionCard string, planCard string, activeExecutionCard string, statusLine string, shellTail string, composer string, footer string, screenHeight int) int {
	reservedHeight := lipgloss.Height(actionCard) + lipgloss.Height(planCard) + lipgloss.Height(activeExecutionCard) + lipgloss.Height(statusLine) + lipgloss.Height(shellTail) + lipgloss.Height(composer) + lipgloss.Height(footer)
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
	activeExecutionCard := m.renderActiveExecutionCard(m.contentWidthFor(width, m.styles.actionCard))
	statusLine := m.renderStatusLine(m.contentWidthFor(width, m.styles.status))
	shellTail := m.renderShellTail(m.contentWidthFor(width, m.styles.tail))
	composer := m.renderComposer(m.contentWidthFor(width, m.activeComposerStyle()))
	footer := m.renderFooter(width)

	screenHeight := m.height
	if screenHeight <= 0 {
		screenHeight = 24
	}

	return m.transcriptViewportHeight(actionCard, planCard, activeExecutionCard, statusLine, shellTail, composer, footer, screenHeight)
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

func (m Model) renderSettingsView() string {
	width := m.width
	if width <= 0 {
		width = 100
	}

	contentWidth := m.contentWidthFor(width, m.styles.detail)
	lines := []string{
		m.styles.detailTitle.Render("SETTINGS"),
		m.styles.detailMeta.Render("Manage providers and choose the active model"),
	}

	switch m.settingsStep {
	case settingsStepProviders:
		lines = append(lines, m.styles.detailBody.Render("Providers"))
		lines = append(lines, m.styles.detailMeta.Render("Edit provider settings and save them for future sessions."))
		lines = append(lines, m.renderSettingsProviders(contentWidth)...)
	case settingsStepActiveProvider:
		lines = append(lines, m.styles.detailBody.Render("Active Provider"))
		lines = append(lines, m.styles.detailMeta.Render("Choose which configured provider Shuttle should use right now."))
		lines = append(lines, m.renderSettingsProviders(contentWidth)...)
	case settingsStepActiveModels:
		lines = append(lines, m.styles.detailBody.Render("Active Model"))
		lines = append(lines, m.styles.detailMeta.Render("Choose the provider/model Shuttle should use right now."))
		lines = append(lines, m.renderSettingsModels(contentWidth)...)
	case settingsStepProviderForm:
		if m.settingsConfig != nil {
			lines = append(lines, m.styles.detailBody.Render(m.settingsConfig.title))
			lines = append(lines, m.styles.detailMeta.Render(m.settingsConfig.intro))
			lines = append(lines, m.renderSettingsConfig(contentWidth)...)
		}
	default:
		lines = append(lines, m.styles.detailBody.Render("Current"))
		lines = append(lines, m.styles.detailMeta.Render(providerSummaryLine(m.activeProvider)))
		lines = append(lines, m.renderSettingsMenu(contentWidth)...)
	}

	lines = append(lines, "", m.styles.detailMeta.Render(settingsFooter(width, m.settingsStep)))
	return m.styles.detail.Width(contentWidth).Render(strings.Join(lines, "\n"))
}

func (m Model) renderSettingsMenu(contentWidth int) []string {
	lines := make([]string, 0, len(settingsMenuEntries())+2)
	for index, entry := range settingsMenuEntries() {
		lines = append(lines, m.renderSettingsRow(entry.label, index == m.settingsIndex, false, false))
	}
	if contentWidth > 0 {
		lines = append(lines, m.styles.detailMeta.Render("Current model: "+currentProviderModelLabel(m.activeProvider)))
	}
	return lines
}

func (m Model) renderSettingsProviders(contentWidth int) []string {
	lines := make([]string, 0, len(m.settingsProviders)*3)
	for index, entry := range m.settingsProviders {
		label := entry.label
		if entry.disabled {
			label += " (coming soon)"
		}
		current := entry.candidate != nil && entry.candidate.Profile.Preset == m.activeProvider.Preset
		lines = append(lines, m.renderSettingsRow(label, index == m.settingsProviderIdx, current, entry.disabled))
		if entry.detail != "" {
			for _, line := range wrapParagraphs(entry.detail, max(10, contentWidth-2)) {
				lines = append(lines, m.renderSettingsMetaLine(line, index == m.settingsProviderIdx, current, entry.disabled))
			}
		}
		if entry.candidate != nil {
			lines = append(lines, m.renderSettingsMetaLine(providerSummaryLine(entry.candidate.Profile), index == m.settingsProviderIdx, current, entry.disabled))
		}
	}
	return lines
}

func (m Model) renderSettingsModels(contentWidth int) []string {
	lines := []string{m.renderSettingsCurrentLine("Current: " + currentProviderModelLabel(m.activeProvider))}
	filterLine := "Filter: type to search models"
	if strings.TrimSpace(m.settingsModelFilter) != "" {
		filterLine = fmt.Sprintf("Filter: %s  (%d matches)", m.settingsModelFilter, len(m.settingsModels))
	}
	lines = append(lines, m.styles.detailMeta.Render(filterLine))
	lines = append(lines, m.styles.detailMeta.Render("Shift+I shows extra model details for the highlighted row."))
	if settingsModelChoicesContainPreset(m.settingsModelCatalog, provider.PresetCodexCLI) {
		lines = append(lines, m.styles.detailMeta.Render("Codex CLI entries are suggested from the OpenAI catalog when available. The live codex CLI picker may differ, and manual entry is still allowed."))
	}
	if len(m.settingsModels) == 0 {
		if strings.TrimSpace(m.settingsModelFilter) != "" && len(m.settingsModelCatalog) > 0 {
			lines = append(lines, m.styles.detailBody.Render("No models match the current filter."))
			return lines
		}
		lines = append(lines, m.styles.detailBody.Render("No configured provider models are available yet."))
		return lines
	}

	start, end := onboardingModelWindow(len(m.settingsModels), m.settingsModelIdx, 12)
	lastProvider := ""
	for index := start; index < end; index++ {
		choice := m.settingsModels[index]
		if choice.profile.Name != lastProvider {
			lines = append(lines, m.styles.detailMeta.Render(choice.profile.Name))
			lastProvider = choice.profile.Name
		}
		current := choice.profile.Preset == m.activeProvider.Preset && choice.model.ID == m.activeProvider.Model
		lines = append(lines, m.renderSettingsRow(choice.model.ID, index == m.settingsModelIdx, current, false))
		detail := modelSummaryLine(choice.model)
		if detail != "" {
			for _, line := range wrapParagraphs(detail, max(10, contentWidth-2)) {
				lines = append(lines, m.renderSettingsMetaLine(line, index == m.settingsModelIdx, current, false))
			}
		}
		if index == m.settingsModelIdx && m.settingsModelInfo {
			for _, extra := range modelExtraDetailLines(choice.model) {
				for _, line := range wrapParagraphs(extra, max(10, contentWidth-2)) {
					lines = append(lines, m.renderSettingsMetaLine(line, true, current, false))
				}
			}
		}
	}

	return lines
}

func (m Model) renderSettingsConfig(contentWidth int) []string {
	if m.settingsConfig == nil {
		return nil
	}

	lines := []string{m.styles.detailMeta.Render(providerSummaryLine(m.settingsConfig.profile))}
	for index, field := range m.settingsConfig.fields {
		value := field.value
		switch {
		case field.secret && strings.TrimSpace(value) != "":
			value = strings.Repeat("*", min(12, len(value)))
		case strings.TrimSpace(value) == "" && field.placeholder != "":
			value = "<" + field.placeholder + ">"
		case strings.TrimSpace(value) == "":
			value = "<empty>"
		}

		lines = append(lines, m.renderSettingsRow(fmt.Sprintf("%s: %s", field.label, value), index == m.settingsConfig.index, false, false))
	}
	lines = append(lines, m.styles.detailMeta.Render("API keys entered here are stored in the OS keyring."))
	return lines
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
		detail := modelSummaryLine(model)
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
	if payload.Keys != "" {
		detailLines = append(detailLines, "keys: "+previewFullscreenKeys(payload.Keys))
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
	case payload.Keys != "":
		previewLines = append(previewLines, "keys: "+previewFullscreenKeys(payload.Keys))
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
	if m.shouldConfirmFullscreenBeforeShellAction() {
		m.pendingFullscreen = &fullscreenAction{
			Kind:    fullscreenActionProposalRun,
			Command: m.pendingProposal.Command,
		}
		return m, nil
	}

	logging.Trace("tui.proposal.run", "command", m.pendingProposal.Command)
	m.busy = true
	m.busyStartedAt = time.Now()
	m.proposalRunPending = true
	m.showShellTail = true
	command := m.pendingProposal.Command
	m.pendingProposal = nil
	m.refiningProposal = nil
	m.editingProposal = nil
	m.syncActiveExecution(newLocalExecution(command, controller.CommandOriginAgentProposal))
	ctx, cancel := context.WithCancel(context.Background())
	m.inFlightCancel = cancel

	return m, tea.Batch(func() tea.Msg {
		defer cancel()

		events, err := m.ctrl.SubmitProposedShellCommand(ctx, command)
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}, tickBusy(), m.pollShellTailCmd(), m.pollActiveExecutionCmd())
}

func (m Model) runProposalKeys() (tea.Model, tea.Cmd) {
	if m.pendingProposal == nil || m.pendingProposal.Keys == "" || !m.canSendActiveKeys() {
		return m, nil
	}

	keys := normalizeFullscreenKeys(m.pendingProposal.Keys)
	logging.Trace("tui.proposal.send_keys", "keys", previewFullscreenKeys(keys))
	m.pendingProposal = nil
	m.refiningProposal = nil
	m.editingProposal = nil
	m.setInput("")
	return m, sendFullscreenKeysCmd(m.takeControl, keys)
}

func (m Model) decideApproval(decision controller.ApprovalDecision) (tea.Model, tea.Cmd) {
	if m.busy || m.ctrl == nil || m.pendingApproval == nil {
		return m, nil
	}

	logging.Trace(
		"tui.approval.decide",
		"approval_id", m.pendingApproval.ID,
		"decision", decision,
		"command", m.pendingApproval.Command,
	)
	m.busy = true
	m.busyStartedAt = time.Now()
	m.approvalInFlight = true
	m.showShellTail = decision == controller.DecisionApprove
	if !m.showShellTail {
		m.liveShellTail = ""
		m.syncActiveExecution(nil)
	}
	approvalID := m.pendingApproval.ID
	command := m.pendingApproval.Command
	if decision == controller.DecisionApprove && m.shouldConfirmFullscreenBeforeShellAction() {
		m.pendingFullscreen = &fullscreenAction{
			Kind:       fullscreenActionApprovalRun,
			Command:    command,
			ApprovalID: approvalID,
		}
		return m, nil
	}
	if decision == controller.DecisionApprove {
		m.pendingApproval = nil
		m.pendingProposal = nil
		m.refiningApproval = nil
		m.refiningProposal = nil
		m.editingProposal = nil
		m.syncActiveExecution(newLocalExecution(command, controller.CommandOriginAgentApproval))
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.inFlightCancel = cancel

	return m, tea.Batch(func() tea.Msg {
		defer cancel()

		events, err := m.ctrl.DecideApproval(ctx, approvalID, decision, "")
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}, tickBusy(), m.pollShellTailCmd(), m.pollActiveExecutionCmd())
}

func (m Model) rejectProposal() (tea.Model, tea.Cmd) {
	if m.busy || m.pendingProposal == nil {
		return m, nil
	}

	logging.Trace("tui.proposal.reject", "command", m.pendingProposal.Command)
	pinned := m.isTranscriptPinned()
	m.pendingProposal = nil
	m.refiningProposal = nil
	m.entries = append(m.entries, Entry{
		Title: "system",
		Body:  "Proposal dismissed.",
	})
	if pinned {
		m.scrollTranscriptToBottom()
	} else {
		m.clampTranscriptScroll()
	}
	m.selectedEntry = len(m.entries) - 1
	m.clampSelection()
	return m, nil
}

func (m Model) editProposalCommand() (tea.Model, tea.Cmd) {
	if m.busy || m.pendingProposal == nil || strings.TrimSpace(m.pendingProposal.Command) == "" {
		return m, nil
	}

	logging.Trace("tui.proposal.edit.begin", "command", m.pendingProposal.Command)
	proposal := *m.pendingProposal
	m.editingProposal = &proposal
	m.pendingProposal = nil
	m.refiningProposal = nil
	m.setInput(proposal.Command)
	m.mode = AgentMode
	return m, nil
}

func (m Model) refineProposal() (tea.Model, tea.Cmd) {
	if m.busy || m.pendingProposal == nil {
		return m, nil
	}

	logging.Trace("tui.proposal.refine.begin", "command", m.pendingProposal.Command)
	proposal := *m.pendingProposal
	m.refiningProposal = &proposal
	m.pendingProposal = nil
	m.editingProposal = nil
	m.setInput("")
	m.mode = AgentMode
	return m, nil
}

func (m Model) submitEditedProposal(command string) (tea.Model, tea.Cmd) {
	logging.Trace("tui.proposal.edit.complete", "command", command)
	updated := *m.editingProposal
	updated.Command = command
	if strings.TrimSpace(updated.Description) == "" {
		updated.Description = "Locally edited proposed command."
	}

	pinned := m.isTranscriptPinned()
	m.pendingProposal = &updated
	m.editingProposal = nil
	m.setInput("")
	m.entries = append(m.entries, compactProposalEntry(updated))
	if pinned {
		m.scrollTranscriptToBottom()
	} else {
		m.clampTranscriptScroll()
	}
	m.selectedEntry = len(m.entries) - 1
	m.clampSelection()
	return m, nil
}

func (m Model) refineApproval() (tea.Model, tea.Cmd) {
	if m.busy || m.ctrl == nil || m.pendingApproval == nil {
		return m, nil
	}

	logging.Trace("tui.approval.refine.begin", "approval_id", m.pendingApproval.ID, "command", m.pendingApproval.Command)
	approval := *m.pendingApproval
	m.refiningApproval = &approval
	m.setInput("")
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

	if execution := latestActiveExecution(events); execution != nil {
		m.syncActiveExecution(execution)
	}

	newPlan := latestPlan(events)
	if newPlan != nil {
		m.activePlan = newPlan
	}

	newApproval := latestApproval(events)
	if newApproval != nil {
		m.pendingApproval = newApproval
		m.refiningApproval = nil
		m.refiningProposal = nil
		m.editingProposal = nil
		m.pendingProposal = nil
	}

	newProposal := latestProposal(events)
	if newProposal != nil && newApproval == nil {
		m.pendingProposal = newProposal
		m.refiningProposal = nil
		m.editingProposal = nil
		m.pendingApproval = nil
	}

	if m.approvalInFlight && !containsEventKind(events, controller.EventApproval) {
		m.pendingApproval = nil
	}
	if m.proposalRunPending {
		m.pendingProposal = nil
	}
	if containsEventKind(events, controller.EventCommandResult) || containsEventKind(events, controller.EventError) {
		m.syncActiveExecution(nil)
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

type composerLine struct {
	Before string
	Cursor string
	After  string
}

func composerDisplayLines(input string, cursor int) []composerLine {
	runes := []rune(input)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}

	lines := strings.Split(string(runes), "\n")
	lineIndex := 0
	column := cursor
	for lineIndex < len(lines) {
		lineRunes := []rune(lines[lineIndex])
		if column <= len(lineRunes) {
			break
		}
		column -= len(lineRunes) + 1
		lineIndex++
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	if lineIndex >= len(lines) {
		lineIndex = len(lines) - 1
		column = len([]rune(lines[lineIndex]))
	}

	display := make([]composerLine, 0, len(lines))
	for index, line := range lines {
		lineRunes := []rune(line)
		if index != lineIndex {
			display = append(display, composerLine{Before: line})
			continue
		}

		if column < len(lineRunes) {
			display = append(display, composerLine{
				Before: string(lineRunes[:column]),
				Cursor: string(lineRunes[column]),
				After:  string(lineRunes[column+1:]),
			})
			continue
		}

		display = append(display, composerLine{
			Before: line,
			Cursor: " ",
		})
	}

	if len(display) == 0 {
		display = append(display, composerLine{Cursor: " "})
	}

	return display
}

func renderComposerLine(line composerLine, cursorStyle lipgloss.Style, inputStyle lipgloss.Style) string {
	return lipgloss.JoinHorizontal(
		lipgloss.Left,
		inputStyle.Render(line.Before),
		cursorStyle.Render(line.Cursor),
		inputStyle.Render(line.After),
	)
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
		if !ok || (payload.Command == "" && payload.Keys == "" && payload.Patch == "" && payload.Kind == "") {
			continue
		}

		proposal := payload
		return &proposal
	}

	return nil
}

func latestActiveExecution(events []controller.TranscriptEvent) *controller.CommandExecution {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != controller.EventCommandStart {
			continue
		}

		payload, ok := events[index].Payload.(controller.CommandStartPayload)
		if !ok || payload.Execution.ID == "" {
			continue
		}

		execution := payload.Execution
		return &execution
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

func newLocalExecution(command string, origin controller.CommandOrigin) *controller.CommandExecution {
	return &controller.CommandExecution{
		ID:        "local-pending",
		Command:   command,
		Origin:    origin,
		State:     controller.CommandExecutionRunning,
		StartedAt: time.Now(),
	}
}

func isAgentOwnedExecution(origin controller.CommandOrigin) bool {
	switch origin {
	case controller.CommandOriginAgentProposal, controller.CommandOriginAgentApproval, controller.CommandOriginAgentPlan:
		return true
	default:
		return false
	}
}

func (m Model) canAttemptLocalInterrupt() bool {
	if m.activeExecution == nil {
		return false
	}

	remoteSeen := false
	localSeen := false

	consider := func(context *shell.PromptContext) {
		if context == nil || context.PromptLine() == "" {
			return
		}
		if context.Remote {
			remoteSeen = true
			return
		}
		localSeen = true
	}

	contextCopy := m.shellContext
	consider(&contextCopy)
	consider(m.activeExecution.ShellContextAfter)
	consider(m.activeExecution.ShellContextBefore)

	if remoteSeen {
		return false
	}
	if localSeen {
		return true
	}

	return false
}

func (m Model) shouldConfirmFullscreenBeforeShellAction() bool {
	return m.pendingFullscreen == nil &&
		m.activeExecution != nil &&
		m.activeExecution.State == controller.CommandExecutionInteractiveFullscreen
}

func (m Model) canSendFullscreenKeys() bool {
	return m.pendingFullscreen == nil &&
		m.activeExecution != nil &&
		m.activeExecution.State == controller.CommandExecutionInteractiveFullscreen
}

func (m Model) canSendActiveKeys() bool {
	return m.pendingFullscreen == nil &&
		m.activeExecution != nil &&
		(m.activeExecution.State == controller.CommandExecutionInteractiveFullscreen ||
			m.activeExecution.State == controller.CommandExecutionAwaitingInput)
}

func (m *Model) appendInterruptNotice(body string) {
	if strings.TrimSpace(body) == "" {
		return
	}
	executionID := ""
	if m.activeExecution != nil {
		executionID = m.activeExecution.ID
	}
	if executionID != "" && m.lastInterruptNoticeID == executionID {
		return
	}
	pinned := m.isTranscriptPinned()
	m.entries = append(m.entries, Entry{
		Title: "system",
		Body:  body,
	})
	m.lastInterruptNoticeID = executionID
	if pinned {
		m.scrollTranscriptToBottom()
	} else {
		m.clampTranscriptScroll()
	}
	m.selectedEntry = len(m.entries) - 1
	m.clampSelection()
}

func previewFullscreenKeys(keys string) string {
	keys = strings.ReplaceAll(keys, "\n", "\\n")
	keys = strings.Trim(keys, "\r\n")
	if keys == "" {
		return "(empty)"
	}
	return logging.Preview(keys, 80)
}

func normalizeFullscreenKeys(keys string) string {
	keys = strings.ReplaceAll(keys, "\r\n", "\n")
	keys = strings.Trim(keys, "\r")
	return keys
}

func humanizeExecutionState(state controller.CommandExecutionState) string {
	switch state {
	case controller.CommandExecutionRunning:
		return "running"
	case controller.CommandExecutionAwaitingInput:
		return "awaiting input"
	case controller.CommandExecutionInteractiveFullscreen:
		return "interactive fullscreen"
	case controller.CommandExecutionHandoffActive:
		return "handoff active"
	case controller.CommandExecutionBackgroundMonitor:
		return "background monitoring"
	case controller.CommandExecutionCompleted:
		return "completed"
	case controller.CommandExecutionFailed:
		return "failed"
	case controller.CommandExecutionCanceled:
		return "canceled"
	case controller.CommandExecutionLost:
		return "lost"
	default:
		return string(state)
	}
}

func humanizeExecutionOrigin(origin controller.CommandOrigin) string {
	switch origin {
	case controller.CommandOriginUserShell:
		return "shell"
	case controller.CommandOriginAgentProposal:
		return "agent proposal"
	case controller.CommandOriginAgentApproval:
		return "agent approval"
	case controller.CommandOriginAgentPlan:
		return "agent plan"
	default:
		return string(origin)
	}
}

func humanizeExecutionElapsed(startedAt time.Time) string {
	if startedAt.IsZero() {
		return "0s"
	}

	elapsed := time.Since(startedAt).Round(time.Second)
	if elapsed < time.Second {
		elapsed = time.Second
	}
	return elapsed.String()
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

func (m Model) currentSettingsProviderIndex() int {
	for index, entry := range m.settingsProviders {
		if entry.candidate == nil {
			continue
		}
		if entry.candidate.Profile.Preset == m.activeProvider.Preset {
			return index
		}
	}
	return 0
}

func (m Model) currentSettingsModelIndex() int {
	for index, choice := range m.settingsModels {
		if choice.profile.Preset == m.activeProvider.Preset && choice.model.ID == m.activeProvider.Model {
			return index
		}
	}
	return 0
}

func (m Model) settingsConfiguredProfiles() []provider.Profile {
	profiles := []provider.Profile{}
	seen := map[string]struct{}{}

	if m.activeProvider.Preset != "" {
		key := settingsProfileKey(m.activeProvider)
		seen[key] = struct{}{}
		profiles = append(profiles, m.activeProvider)
	}

	for _, entry := range m.settingsProviders {
		if entry.candidate == nil || entry.candidate.Manual {
			continue
		}
		key := settingsProfileKey(entry.candidate.Profile)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		profiles = append(profiles, entry.candidate.Profile)
	}

	return profiles
}

func (m *Model) applySettingsModelFilter() {
	selectedKey := ""
	if len(m.settingsModels) > 0 && m.settingsModelIdx >= 0 && m.settingsModelIdx < len(m.settingsModels) {
		selectedKey = settingsModelChoiceKey(m.settingsModels[m.settingsModelIdx])
	}

	m.settingsModelInfo = false
	m.settingsModels = filterSettingsModels(m.settingsModelCatalog, m.settingsModelFilter)
	switch {
	case len(m.settingsModels) == 0:
		m.settingsModelIdx = 0
	case selectedKey != "":
		if index := findSettingsModelIndex(m.settingsModels, selectedKey); index >= 0 {
			m.settingsModelIdx = index
			return
		}
		fallthrough
	default:
		m.settingsModelIdx = m.currentSettingsModelIndex()
	}
}

func (m Model) renderSettingsRow(label string, selected bool, current bool, disabled bool) string {
	prefix := "  "
	if selected {
		prefix = "› "
	}

	style := m.settingsRowStyle(selected, current, disabled)
	return style.Render(prefix + label)
}

func (m Model) renderSettingsMetaLine(line string, selected bool, current bool, disabled bool) string {
	style := m.styles.detailMeta
	switch {
	case disabled:
		style = m.styles.detailDisabled
	case current && selected:
		style = m.styles.detailSelectedCurrent
	case current:
		style = m.styles.detailCurrent
	case selected:
		style = m.styles.detailSelected
	}
	return style.Render("  " + line)
}

func (m Model) renderSettingsCurrentLine(line string) string {
	return m.styles.detailCurrent.Render(line)
}

func (m Model) settingsRowStyle(selected bool, current bool, disabled bool) lipgloss.Style {
	switch {
	case disabled:
		return m.styles.detailDisabled
	case current && selected:
		return m.styles.detailSelectedCurrent
	case current:
		return m.styles.detailCurrent
	case selected:
		return m.styles.detailSelected
	default:
		return m.styles.detailBody
	}
}

func providerSummaryLine(profile provider.Profile) string {
	if profile.Preset == "" {
		return "provider not configured"
	}

	auth := providerAuthSourceLabel(profile)
	if strings.TrimSpace(auth) == "" {
		auth = "unknown"
	}
	return fmt.Sprintf("preset=%s  model=%s  base=%s  auth=%s", profile.Preset, profile.Model, profile.BaseURL, auth)
}

func providerAuthSourceLabel(profile provider.Profile) string {
	if strings.TrimSpace(profile.APIKeyEnvVar) != "" {
		if strings.TrimSpace(profile.APIKeyEnvVar) == "os_keyring" {
			return "OS keyring"
		}
		if strings.TrimSpace(profile.APIKeyEnvVar) == "local_file" {
			return "local file (less secure)"
		}
		return profile.APIKeyEnvVar
	}
	if strings.TrimSpace(profile.APIKey) != "" {
		return "session only"
	}
	if profile.AuthMethod == provider.AuthNone {
		return "none"
	}
	return string(profile.AuthMethod)
}

func providerPersistenceErrorBody(profile provider.Profile, err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, provider.ErrSecretStoreUnavailable) {
		return fmt.Sprintf("Provider settings for %s are active for this session, but the secret could not be persisted because secure key storage is unavailable: %v", profile.Name, err)
	}
	return fmt.Sprintf("save provider config: %v", err)
}

func currentProviderModelLabel(profile provider.Profile) string {
	if profile.Preset == "" {
		return "not configured"
	}
	providerLabel := profile.Name
	if strings.TrimSpace(providerLabel) == "" {
		providerLabel = settingsProviderLabel(profile.Preset)
	}
	if providerLabel == "" {
		providerLabel = string(profile.Preset)
	}
	if strings.TrimSpace(profile.Model) == "" {
		return fmt.Sprintf("%s (%s)", providerLabel, profile.Preset)
	}
	return fmt.Sprintf("%s (%s) / %s", providerLabel, profile.Preset, profile.Model)
}

func settingsMenuEntries() []settingsMenuEntry {
	return []settingsMenuEntry{
		{label: "Providers"},
		{label: "Active Provider"},
		{label: "Active Model"},
	}
}

func buildSettingsProviderEntries(candidates []provider.OnboardingCandidate) []settingsProviderEntry {
	chosen := map[provider.ProviderPreset]provider.OnboardingCandidate{}
	for _, preset := range settingsProviderOrder() {
		if candidate, ok := chooseSettingsCandidate(candidates, preset); ok {
			chosen[preset] = candidate
		}
	}

	entries := []settingsProviderEntry{}
	for _, preset := range settingsProviderOrder() {
		candidate, ok := chosen[preset]
		if !ok {
			continue
		}
		candidateCopy := candidate
		entries = append(entries, settingsProviderEntry{
			label:     settingsProviderLabel(preset),
			detail:    settingsProviderDetail(candidate),
			candidate: &candidateCopy,
		})
		if preset == provider.PresetAnthropic {
			entries = append(entries, settingsProviderEntry{
				label:    "Anthropic Agent SDK",
				detail:   "Reserved first-class Anthropic agent runtime integration.",
				disabled: true,
			})
		}
	}

	return entries
}

func chooseSettingsCandidate(candidates []provider.OnboardingCandidate, preset provider.ProviderPreset) (provider.OnboardingCandidate, bool) {
	var manual *provider.OnboardingCandidate
	for _, candidate := range candidates {
		if candidate.Profile.Preset != preset {
			continue
		}
		if !candidate.Manual {
			return candidate, true
		}
		if manual == nil {
			candidateCopy := candidate
			manual = &candidateCopy
		}
	}
	if manual != nil {
		return *manual, true
	}
	return provider.OnboardingCandidate{}, false
}

func settingsProviderOrder() []provider.ProviderPreset {
	return []provider.ProviderPreset{
		provider.PresetAnthropic,
		provider.PresetCodexCLI,
		provider.PresetOllama,
		provider.PresetOpenAI,
		provider.PresetOpenRouter,
		provider.PresetOpenWebUI,
		provider.PresetCustom,
	}
}

func settingsProviderLabel(preset provider.ProviderPreset) string {
	switch preset {
	case provider.PresetOpenAI:
		return "OpenAI"
	case provider.PresetOpenRouter:
		return "OpenRouter"
	case provider.PresetOpenWebUI:
		return "OpenWebUI"
	case provider.PresetCustom:
		return "OpenAI-Compatible"
	case provider.PresetCodexCLI:
		return "Codex CLI"
	case provider.PresetAnthropic:
		return "Anthropic"
	case provider.PresetOllama:
		return "Ollama"
	default:
		return string(preset)
	}
}

func settingsProviderDetail(candidate provider.OnboardingCandidate) string {
	status := "manual setup"
	if !candidate.Manual {
		status = "configured"
	}
	if candidate.AuthSource != "" {
		status += " via " + candidate.AuthSource
	}
	if strings.TrimSpace(candidate.Reason) == "" {
		return status
	}
	return status + ". " + strings.TrimSpace(candidate.Reason)
}

func settingsProfileKey(profile provider.Profile) string {
	return fmt.Sprintf("%s|%s|%s", profile.Preset, profile.BaseURL, profile.CLICommand)
}

func settingsModelChoiceKey(choice settingsModelChoice) string {
	return fmt.Sprintf("%s|%s|%s", choice.profile.Preset, choice.profile.BaseURL, choice.model.ID)
}

func findSettingsModelIndex(choices []settingsModelChoice, key string) int {
	for index, choice := range choices {
		if settingsModelChoiceKey(choice) == key {
			return index
		}
	}
	return -1
}

func filterSettingsModels(choices []settingsModelChoice, filter string) []settingsModelChoice {
	filter = strings.TrimSpace(strings.ToLower(filter))
	if filter == "" {
		return append([]settingsModelChoice(nil), choices...)
	}

	filtered := make([]settingsModelChoice, 0, len(choices))
	for _, choice := range choices {
		if settingsModelMatches(choice, filter) {
			filtered = append(filtered, choice)
		}
	}

	return filtered
}

func settingsModelMatches(choice settingsModelChoice, filter string) bool {
	tokens := strings.Fields(filter)
	if len(tokens) == 0 {
		tokens = []string{filter}
	}

	fields := []string{
		strings.ToLower(choice.model.ID),
		strings.ToLower(choice.model.Name),
		strings.ToLower(choice.profile.Name),
		strings.ToLower(string(choice.profile.Preset)),
	}

	for _, token := range tokens {
		matched := false
		for _, field := range fields {
			if strings.Contains(field, token) || fuzzySubsequenceMatch(field, token) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

func fuzzySubsequenceMatch(field string, filter string) bool {
	filterRunes := []rune(filter)
	if len(filterRunes) == 0 {
		return true
	}

	index := 0
	for _, r := range field {
		if index < len(filterRunes) && r == filterRunes[index] {
			index++
			if index == len(filterRunes) {
				return true
			}
		}
	}

	return false
}

func containsModelOption(models []provider.ModelOption, target string) bool {
	target = strings.TrimSpace(target)
	for _, model := range models {
		if strings.TrimSpace(model.ID) == target {
			return true
		}
	}
	return false
}

func settingsModelChoicesContainPreset(choices []settingsModelChoice, preset provider.ProviderPreset) bool {
	for _, choice := range choices {
		if choice.profile.Preset == preset {
			return true
		}
	}
	return false
}

func shouldValidateProviderModel(profile provider.Profile) bool {
	if strings.TrimSpace(profile.Model) == "" {
		return false
	}

	switch profile.Preset {
	case provider.PresetOpenAI, provider.PresetOpenRouter, provider.PresetOpenWebUI, provider.PresetAnthropic, provider.PresetOllama:
		return true
	default:
		return false
	}
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

func trimLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[:len(runes)-1])
}

func settingsFooter(width int, step settingsStep) string {
	if width < 72 {
		switch step {
		case settingsStepProviders:
			return "Enter edit  Esc back  Up/Down move  F2 shell  F10 close"
		case settingsStepActiveProvider:
			return "Enter switch  Esc back  Up/Down move  F2 shell  F10 close"
		case settingsStepActiveModels:
			return "Type filter  Shift+I info  Enter activate  Esc clear/back  Pg page  F2 shell  F10 close"
		case settingsStepProviderForm:
			return "Type edit  Tab next  Enter save  Esc back  F2 shell  F10 close"
		default:
			return "Enter open  Esc close  Up/Down move  F2 shell  F10 close"
		}
	}

	switch step {
	case settingsStepProviders:
		return "Enter edit provider settings  Esc back  Up/Down move  F2 shell  F10 close"
	case settingsStepActiveProvider:
		return "Enter switch active provider  Esc back  Up/Down move  F2 shell  F10 close"
	case settingsStepActiveModels:
		return "Type to filter models  Shift+I toggle info  Enter switch active model  Esc clear filter/back  Up/Down move  PgUp/PgDn page  F2 shell  F10 close"
	case settingsStepProviderForm:
		return "Type to edit fields  Tab/Up/Down move  Enter save settings  Esc back  F2 shell  F10 close"
	default:
		return "Enter open section  Esc close  Up/Down move  F2 shell  F10 close"
	}
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

func modelSummaryLine(model provider.ModelOption) string {
	parts := make([]string, 0, 6)
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

	return strings.Join(parts, "  ")
}

func modelExtraDetailLines(model provider.ModelOption) []string {
	lines := make([]string, 0, 2)
	if len(model.SupportedParameters) > 0 {
		lines = append(lines, "params "+strings.Join(model.SupportedParameters, ","))
	}
	if model.Description != "" {
		lines = append(lines, model.Description)
	}
	return lines
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
			if payload.State == controller.CommandExecutionCanceled {
				detail := []string{
					"command:",
					payload.Command,
					"",
					"status:",
					"canceled",
				}
				if strings.TrimSpace(fullBody) != "" && fullBody != "(no output)" {
					detail = append(detail, "", "output so far:", fullBody)
				}
				entries = append(entries, Entry{
					Title:  "result",
					Body:   "status=canceled\n" + body,
					Detail: strings.Join(detail, "\n"),
				})
				break
			}
			if payload.State == controller.CommandExecutionLost {
				detail := []string{
					"command:",
					payload.Command,
					"",
					"status:",
					"lost",
				}
				if payload.Cause != "" {
					detail = append(detail, "", "cause:", string(payload.Cause))
				}
				if payload.Confidence != "" {
					detail = append(detail, "confidence:", string(payload.Confidence))
				}
				if strings.TrimSpace(fullBody) != "" && fullBody != "(no output)" {
					detail = append(detail, "", "latest observed output:", fullBody)
				}
				entries = append(entries, Entry{
					Title:  "result",
					Body:   "status=lost\n" + body,
					Detail: strings.Join(detail, "\n"),
				})
				break
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

package tui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"aiterm/internal/controller"
	"aiterm/internal/logging"
	"aiterm/internal/provider"
	"aiterm/internal/shell"
	"aiterm/internal/tmux"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Mode string

const (
	AgentMode Mode = "AGENT"
	ShellMode Mode = "SHELL"
)

type Entry struct {
	Title   string
	Command string
	Body    string
	Detail  string
	TagKind entryTagKind
	Hidden  bool
}

type entryTagKind string

const (
	entryTagDefault        entryTagKind = ""
	entryTagResultSuccess  entryTagKind = "result_success"
	entryTagResultError    entryTagKind = "result_error"
	entryTagResultNoExec   entryTagKind = "result_noexec"
	entryTagResultNotFound entryTagKind = "result_not_found"
	entryTagResultSignal   entryTagKind = "result_signal"
	entryTagResultSigInt   entryTagKind = "result_sigint"
	entryTagResultCustom   entryTagKind = "result_custom"
	entryTagResultFatal    entryTagKind = "result_fatal"
)

type composerCompletion struct {
	Start      int
	End        int
	Fragment   string
	Candidates []string
	Index      int
}

type controllerEventsMsg struct {
	events []controller.TranscriptEvent
	err    error
}

type busyTickMsg time.Time
type shellContextPollTickMsg time.Time

type refreshedShellContextMsg struct {
	context *shell.PromptContext
	err     error
}

type shellTailMsg struct {
	tail string
	err  error
}

func restoreMouseTrackingCmd() tea.Cmd {
	return func() tea.Msg {
		return tea.EnableMouseCellMotion()
	}
}

type activeExecutionMsg struct {
	execution *controller.CommandExecution
	events    []controller.TranscriptEvent
	err       error
}

type transcriptRenderLine struct {
	text             string
	entryIndex       int
	tagStart         int
	tagEnd           int
	commandStart     int
	commandEnd       int
	commandClickable bool
	detailStart      int
	detailEnd        int
	detailClickable  bool
}

type actionCardAction string

const (
	actionCardContinueStartup   actionCardAction = "continue_startup"
	actionCardConfirmDangerous  actionCardAction = "confirm_dangerous"
	actionCardCancelDangerous   actionCardAction = "cancel_dangerous"
	actionCardConfirmFullscreen actionCardAction = "confirm_fullscreen"
	actionCardCancelFullscreen  actionCardAction = "cancel_fullscreen"
	actionCardTakeControl       actionCardAction = "take_control"
	actionCardResumeInteractive actionCardAction = "resume_interactive"
	actionCardApprove           actionCardAction = "approve"
	actionCardReject            actionCardAction = "reject"
	actionCardRefine            actionCardAction = "refine"
	actionCardEditProposal      actionCardAction = "edit_proposal"
)

type actionCardButton struct {
	label  string
	action actionCardAction
}

type actionCardSpec struct {
	title       string
	body        []string
	buttons     []actionCardButton
	borderColor lipgloss.TerminalColor
}

type actionCardButtonHit struct {
	action actionCardAction
	start  int
	end    int
}

type actionCardButtonLine struct {
	text string
	hits []actionCardButtonHit
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

type activeKeysLease struct {
	executionID  string
	state        controller.CommandExecutionState
	fingerprint  string
	observedAt   time.Time
	source       string
	lastNoticeID string
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

type contextActionKind string

const (
	contextActionNone    contextActionKind = ""
	contextActionNewTask contextActionKind = "new_task"
	contextActionCompact contextActionKind = "compact_task"
)

type startupSecurityNotice struct {
	Title string
	Body  string
}

type dangerousApprovalConfirm struct {
	mode controller.ApprovalMode
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
	settingsStepSession        settingsStep = "session"
	settingsStepProviders      settingsStep = "providers"
	settingsStepActiveProvider settingsStep = "active_provider"
	settingsStepActiveModels   settingsStep = "active_models"
	settingsStepProviderForm   settingsStep = "provider_form"
)

type settingsMenuEntry struct {
	label string
}

type settingsApprovalEntry struct {
	label string
	mode  controller.ApprovalMode
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
	activated  bool
}

type settingsProviderTestedMsg struct {
	profile provider.Profile
	err     error
}

const (
	agentTurnTimeout       = 60 * time.Second
	patchTurnTimeout       = 120 * time.Second
	ollamaAgentTurnTimeout = 180 * time.Second
	ollamaPatchTurnTimeout = 240 * time.Second
	shellTailPollLines     = 40
	shellTailPollTimeout   = 750 * time.Millisecond
	firstCheckInDelay      = 10 * time.Second
	repeatCheckInDelay     = 30 * time.Second
	maxInteractiveCheckIns = 3
)

var (
	ansiCSIPattern     = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	ansiOSCPattern     = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	ansiEscPattern     = regexp.MustCompile(`\x1b[@-_]`)
	mouseReportPattern = regexp.MustCompile(`(?:\x1b\[)?<\d+;\d+;\d+[mM]`)
)

type Model struct {
	workspace                    tmux.Workspace
	ctrl                         controller.Controller
	mode                         Mode
	input                        string
	cursor                       int
	entries                      []Entry
	selectedEntry                int
	width                        int
	height                       int
	busy                         bool
	busyStartedAt                time.Time
	transcriptScroll             int
	transcriptFollow             bool
	expandedCommandEntry         int
	helpOpen                     bool
	helpScroll                   int
	detailOpen                   bool
	detailScroll                 int
	detailFilter                 string
	shellHistory                 composerHistory
	agentHistory                 composerHistory
	activePlan                   *controller.ActivePlan
	shellContext                 shell.PromptContext
	pendingLocalEcho             *Entry
	pendingApproval              *controller.ApprovalRequest
	pendingProposal              *controller.ProposalPayload
	startupNotice                *startupSecurityNotice
	refiningApproval             *controller.ApprovalRequest
	refiningProposal             *controller.ProposalPayload
	editingProposal              *controller.ProposalPayload
	approvalInFlight             bool
	proposalRunPending           bool
	directShellPending           bool
	inFlightCancel               context.CancelFunc
	suppressCancelErr            bool
	resumeAfterHandoff           bool
	handoffVisible               bool
	handoffPriorState            controller.CommandExecutionState
	handoffReturnGraceUntil      time.Time
	activeExecutionMissingSince  time.Time
	takeControl                  takeControlConfig
	liveShellTail                string
	showShellTail                bool
	activeExecution              *controller.CommandExecution
	pendingFullscreen            *fullscreenAction
	sendingFullscreenKeys        bool
	autoOpenedFullscreenKeys     bool
	lastFullscreenKeys           string
	lastFullscreenKeysAt         time.Time
	activeKeysLease              activeKeysLease
	dismissedAutoKeysFingerprint string
	suppressAutoKeysUntil        time.Time
	exitConfirmUntil             time.Time
	exitConfirmToken             uint64
	checkInInFlight              bool
	lastCheckInAt                time.Time
	interactiveCheckInCount      int
	interactiveCheckInPaused     bool
	pendingContinueAfterCommand  bool
	lastInterruptNoticeID        string
	activeProvider               provider.Profile
	overwriteMode                bool
	onboardingOpen               bool
	onboardingStep               onboardingStep
	onboardingIndex              int
	onboardingChoices            []provider.OnboardingCandidate
	onboardingSelected           *provider.OnboardingCandidate
	onboardingForm               *onboardingFormState
	onboardingModels             []provider.ModelOption
	onboardingModelIdx           int
	loadOnboarding               func() ([]provider.OnboardingCandidate, error)
	loadModels                   func(provider.Profile) ([]provider.ModelOption, error)
	switchProvider               func(provider.Profile, *shell.PromptContext) (controller.Controller, provider.Profile, error)
	saveProvider                 func(provider.Profile) error
	testProvider                 func(provider.Profile) error
	settingsOpen                 bool
	settingsStep                 settingsStep
	settingsIndex                int
	settingsApprovalIdx          int
	settingsProviders            []settingsProviderEntry
	settingsProviderIdx          int
	settingsConfig               *onboardingFormState
	settingsModelCatalog         []settingsModelChoice
	settingsModels               []settingsModelChoice
	settingsModelIdx             int
	settingsModelFilter          string
	settingsModelScope           provider.ProviderPreset
	settingsModelInfo            bool
	settingsBanner               string
	lastModelInfo                *controller.AgentModelInfo
	pendingContextAction         contextActionKind
	pendingDangerousConfirm      *dangerousApprovalConfirm
	completion                   *composerCompletion
	styles                       styles
}

func (m Model) currentAgentTurnTimeout() time.Duration {
	if m.activeProvider.BackendFamily == provider.BackendOllama {
		return ollamaAgentTurnTimeout
	}
	return agentTurnTimeout
}

func (m Model) currentPatchTurnTimeout() time.Duration {
	if m.activeProvider.BackendFamily == provider.BackendOllama {
		return ollamaPatchTurnTimeout
	}
	return patchTurnTimeout
}

func NewModel(workspace tmux.Workspace, ctrl controller.Controller) Model {
	return Model{
		workspace:            workspace,
		ctrl:                 ctrl,
		mode:                 ShellMode,
		transcriptFollow:     true,
		entries:              initialEntries(workspace),
		selectedEntry:        0,
		expandedCommandEntry: -1,
		styles:               newStyles(),
	}
}

func initialEntries(workspace tmux.Workspace) []Entry {
	return []Entry{
		{
			Title:  "system",
			Body:   fmt.Sprintf("Workspace ready. Top pane: %s. Bottom pane TUI is active.", workspace.TopPane.ID),
			Hidden: true,
		},
	}
}

func (m Model) WithShellContext(promptContext shell.PromptContext) Model {
	if promptContext.PromptLine() != "" {
		m.shellContext = promptContext
	}

	return m
}

func (m Model) WithTakeControl(socketName string, sessionName string, trackedPaneID string, detachKey string) Model {
	m.takeControl = takeControlConfig{
		SocketName:    socketName,
		SessionName:   sessionName,
		TrackedPaneID: trackedPaneID,
		DetachKey:     detachKey,
	}

	return m
}

func (m Model) WithTakeControlStartDir(startDir string) Model {
	m.takeControl.StartDir = strings.TrimSpace(startDir)
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

func (m Model) WithProviderTester(tester func(provider.Profile) error) Model {
	m.testProvider = tester
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refreshShellContextCmd(), tickShellContext())
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
		eventEntries := m.collapseCommandEntries(m.consumePendingLocalEcho(eventsToEntries(msg.events, !m.directShellPending)))
		eventEntries = m.attachLatestModelInfo(msg.events, eventEntries)
		m.busy = false
		m.busyStartedAt = time.Time{}
		m.inFlightCancel = nil
		if len(msg.events) > 0 {
			m.syncActionState(msg.events)
		}
		contextAction := m.pendingContextAction
		m.pendingContextAction = contextActionNone
		contextActionSucceeded := msg.err == nil && !containsEventKind(msg.events, controller.EventError)
		if contextActionSucceeded {
			m.applySuccessfulContextAction(contextAction)
		}
		if len(eventEntries) > 0 {
			m.entries = append(m.entries, eventEntries...)
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
		hasPatchResult := containsEventKind(msg.events, controller.EventPatchApplyResult)
		if containsEventKind(msg.events, controller.EventError) && !hasPatchResult {
			return m, m.pollShellTailCmd()
		}
		if autoContinue {
			m.pendingContinueAfterCommand = false
			m.busy = true
			m.busyStartedAt = time.Now()
			m.showShellTail = false
			m.liveShellTail = ""
			continueAfterPatch := containsEventKind(msg.events, controller.EventPatchApplyResult) && !containsEventKind(msg.events, controller.EventCommandResult)
			timeout := m.currentAgentTurnTimeout()
			if continueAfterPatch {
				timeout = m.currentPatchTurnTimeout()
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			m.inFlightCancel = cancel
			return m, tea.Batch(func() tea.Msg {
				defer cancel()

				var (
					events []controller.TranscriptEvent
					err    error
				)
				if continueAfterPatch {
					events, err = m.ctrl.ContinueAfterPatchApply(ctx)
				} else {
					events, err = m.ctrl.ContinueAfterCommand(ctx)
				}
				return controllerEventsMsg{
					events: events,
					err:    err,
				}
			}, tickBusy(), m.pollShellTailCmd())
		}
		m.updatePendingContinueAfterCommand(msg.events, autoContinue)
		m.syncTrackedShellTarget()
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
			m.handoffReturnGraceUntil = time.Time{}
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  msg.err.Error(),
			})
			m.selectedEntry = len(m.entries) - 1
			m.clampSelection()
			return m, restoreMouseTrackingCmd()
		}
		m.handoffVisible = false
		m.syncTrackedShellTarget()
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
		if m.ctrl != nil {
			m.resumeAfterHandoff = false
			m.busy = true
			m.busyStartedAt = time.Now()
			m.handoffReturnGraceUntil = time.Now().Add(2 * time.Second)
			m.showShellTail = false
			m.liveShellTail = ""
			ctx, cancel := context.WithTimeout(context.Background(), m.currentAgentTurnTimeout())
			m.inFlightCancel = cancel
			return m, tea.Batch(func() tea.Msg {
				defer cancel()

				events, err := m.ctrl.ResumeAfterTakeControl(ctx)
				return controllerEventsMsg{
					events: events,
					err:    err,
				}
			}, tickBusy(), m.refreshShellContextCmd(), m.pollShellTailCmd(), restoreMouseTrackingCmd())
		}
		m.handoffReturnGraceUntil = time.Time{}
		followUpCmds = append(followUpCmds, restoreMouseTrackingCmd())
		return m, tea.Batch(followUpCmds...)
	case refreshedShellContextMsg:
		if msg.context != nil {
			m.shellContext = *msg.context
		}
		m.syncTrackedShellTarget()
		return m, nil
	case shellTailMsg:
		if msg.err == nil {
			if !m.shouldAcceptPolledShellTail() {
				return m, nil
			}
			m.liveShellTail = msg.tail
			if m.activeExecution != nil {
				updated := *m.activeExecution
				updated.LatestOutputTail = msg.tail
				m.activeExecution = &updated
			}
			m.observeActiveKeysLease("shell_tail")
			m.autoOpenFullscreenKeysIfNeeded()
		}
		return m, nil
	case activeExecutionMsg:
		if msg.err != nil {
			return m, nil
		}
		if len(msg.events) > 0 {
			pinned := m.isTranscriptPinned()
			eventEntries := m.collapseCommandEntries(eventsToEntries(msg.events, !m.directShellPending))
			eventEntries = m.attachLatestModelInfo(msg.events, eventEntries)
			m.entries = append(m.entries, eventEntries...)
			m.syncActionState(msg.events)
			if pinned {
				m.scrollTranscriptToBottom()
			} else {
				m.clampTranscriptScroll()
			}
			m.selectedEntry = len(m.entries) - 1
			m.clampSelection()
		}
		if msg.execution == nil && m.shouldPreserveExecutionAfterHandoff() {
			logging.Trace(
				"tui.active_execution.preserve_after_handoff",
				"execution_id", activeExecutionID(m.activeExecution),
			)
			return m, tea.Batch(m.pollActiveExecutionAfter(150*time.Millisecond), m.pollShellTailCmd())
		}
		if msg.execution == nil && m.shouldConfirmMissingActiveExecution() {
			if m.activeExecutionMissingSince.IsZero() {
				m.activeExecutionMissingSince = time.Now()
			}
			logging.Trace(
				"tui.active_execution.confirm_missing",
				"execution_id", activeExecutionID(m.activeExecution),
				"missing_for_ms", time.Since(m.activeExecutionMissingSince).Milliseconds(),
			)
			return m, tea.Batch(m.pollActiveExecutionAfter(250*time.Millisecond), m.pollShellTailCmd())
		}
		if msg.execution != nil {
			m.handoffReturnGraceUntil = time.Time{}
			m.activeExecutionMissingSince = time.Time{}
		}
		m.syncActiveExecution(msg.execution)
		m.observeActiveKeysLease("active_execution")
		m.autoOpenFullscreenKeysIfNeeded()
		m.syncTrackedShellTarget()
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
		if executionNeedsUserDrivenResume(m.activeExecution) {
			m.interactiveCheckInCount++
		}
		if len(msg.events) == 0 {
			if executionNeedsUserDrivenResume(m.activeExecution) && m.interactiveCheckInCount >= maxInteractiveCheckIns {
				m.pauseInteractiveCheckIns()
			}
			return m, nil
		}
		pinned := m.isTranscriptPinned()
		eventEntries := m.collapseCommandEntries(eventsToEntries(msg.events, true))
		eventEntries = m.attachLatestModelInfo(msg.events, eventEntries)
		m.entries = append(m.entries, eventEntries...)
		if pinned {
			m.scrollTranscriptToBottom()
		} else {
			m.clampTranscriptScroll()
		}
		m.selectedEntry = len(m.entries) - 1
		m.clampSelection()
		if executionNeedsUserDrivenResume(m.activeExecution) && m.interactiveCheckInCount >= maxInteractiveCheckIns {
			m.pauseInteractiveCheckIns()
		}
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
			m.autoOpenedFullscreenKeys = false
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

		currentApprovalMode := controller.ApprovalModeConfirm
		if m.ctrl != nil {
			currentApprovalMode = m.ctrl.ApprovalMode()
		}
		m.ctrl = msg.ctrl
		if m.ctrl != nil && currentApprovalMode != controller.ApprovalModeConfirm {
			if _, err := m.ctrl.SetApprovalMode(context.Background(), currentApprovalMode); err == nil {
				m.settingsApprovalIdx = currentSettingsApprovalIndex(m.ctrl)
			}
		}
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
			m.settingsBanner = fmt.Sprintf("Active provider switched to %s.", msg.profile.Name)
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
			m.settingsBanner = ""
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
		m.settingsBanner = ""
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
			currentApprovalMode := controller.ApprovalModeConfirm
			if m.ctrl != nil {
				currentApprovalMode = m.ctrl.ApprovalMode()
			}
			m.ctrl = msg.ctrl
			if m.ctrl != nil && currentApprovalMode != controller.ApprovalModeConfirm {
				if _, err := m.ctrl.SetApprovalMode(context.Background(), currentApprovalMode); err == nil {
					m.settingsApprovalIdx = currentSettingsApprovalIndex(m.ctrl)
				}
			}
			m.activeProvider = msg.profile
		}
		m.settingsStep = settingsStepProviders
		m.settingsConfig = nil
		m.settingsModelCatalog = nil
		m.settingsModels = nil
		m.settingsModelIdx = 0
		m.settingsModelFilter = ""
		m.settingsModelInfo = false
		if msg.activated {
			m.settingsBanner = fmt.Sprintf("Activated %s.", msg.profile.Name)
		} else {
			m.settingsBanner = fmt.Sprintf("Saved %s.", msg.profile.Name)
		}
		if msg.persistErr != nil {
			m.entries = append(m.entries, Entry{
				Title: "error",
				Body:  providerPersistenceErrorBody(msg.profile, msg.persistErr),
			})
		} else {
			body := fmt.Sprintf("Saved provider settings for %s.", msg.profile.Name)
			if msg.activated {
				body = fmt.Sprintf("Saved and activated provider settings for %s.", msg.profile.Name)
			} else if msg.ctrl != nil {
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
	case settingsProviderTestedMsg:
		m.busy = false
		m.busyStartedAt = time.Time{}
		m.inFlightCancel = nil
		if msg.err != nil {
			m.settingsBanner = fmt.Sprintf("Provider test failed for %s: %v", msg.profile.Name, msg.err)
			return m, nil
		}
		m.settingsBanner = fmt.Sprintf("Provider test succeeded for %s.", msg.profile.Name)
		return m, nil
	case busyTickMsg:
		if !m.busy && m.activeExecution == nil {
			return m, nil
		}

		return m, tea.Batch(tickBusy(), m.pollShellTailCmd(), m.pollActiveExecutionCmd(), m.maybeExecutionCheckInCmd(time.Time(msg)))
	case shellContextPollTickMsg:
		if m.ctrl == nil {
			return m, nil
		}
		return m, tea.Batch(tickShellContext(), m.refreshShellContextCmd())
	case tea.MouseMsg:
		if m.helpOpen || m.settingsOpen || m.onboardingOpen || m.detailOpen {
			return m, nil
		}
		return m.handleMouse(msg)
	case tea.KeyMsg:
		if m.sendingFullscreenKeys {
			logging.Trace("tui.fullscreen_keys.key", "type", int(msg.Type), "text", msg.String(), "runes", string(msg.Runes))
		}
		if msg.Type == tea.KeyF1 {
			return m.toggleHelp()
		}
		if m.helpOpen {
			return m.updateHelp(msg)
		}
		if m.pendingDangerousConfirm != nil {
			if handledModel, handled, cmd := m.handleActionCardKey(msg); handled {
				return handledModel, cmd
			}
			if msg.Type == tea.KeyEsc {
				m.pendingDangerousConfirm = nil
				return m, nil
			}
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
			return m.takeControlPersistentShellNow()
		case tea.KeyF3:
			return m.takeControlExecutionNow()
		case tea.KeyF10:
			return m.openSettings()
		case tea.KeyEsc:
			if m.pendingDangerousConfirm != nil {
				m.pendingDangerousConfirm = nil
				return m, nil
			}
			if m.sendingFullscreenKeys {
				m.sendingFullscreenKeys = false
				m.autoOpenedFullscreenKeys = false
				m.setInput("")
				return m, nil
			}
			if m.expandedCommandEntry >= 0 {
				m.expandedCommandEntry = -1
				return m, nil
			}
			if m.clearCompletion() {
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
		case tea.KeyShiftTab:
			if m.sendingFullscreenKeys {
				m.dismissFullscreenKeysAutoOpen()
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
			m.recomputeCompletion()
			return m, nil
		case tea.KeyTab:
			if m.advanceCompletion() {
				return m, nil
			}
			if m.sendingFullscreenKeys {
				m.insertTextAtCursor("\t")
				return m, nil
			}
			if m.composerLocked() {
				return m, nil
			}
			m.insertTextAtCursor("\t")
			return m, nil
		case tea.KeyUp:
			if msg.Alt {
				m.selectPreviousEntry()
				return m, nil
			}
			if m.sendingFullscreenKeys {
				m.moveCursorVertical(-1)
				return m, nil
			}
			if m.composerLocked() {
				return m, nil
			}
			if m.inputHasMultipleLines() {
				m.moveCursorVertical(-1)
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
				m.moveCursorVertical(1)
				return m, nil
			}
			if m.composerLocked() {
				return m, nil
			}
			if m.inputHasMultipleLines() {
				m.moveCursorVertical(1)
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
			if m.acceptCompletion() {
				return m, nil
			}
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
			if m.sendingFullscreenKeys {
				m.moveCursorToLineBoundary(false)
				return m, nil
			}
			if m.composerLocked() {
				return m, nil
			}
			m.moveCursorToLineBoundary(false)
			return m, nil
		case tea.KeyEnd:
			if m.sendingFullscreenKeys {
				m.moveCursorToLineBoundary(true)
				return m, nil
			}
			if m.composerLocked() {
				return m, nil
			}
			m.moveCursorToLineBoundary(true)
			return m, nil
		case tea.KeyCtrlHome:
			m.scrollTranscriptToTop()
			return m, nil
		case tea.KeyCtrlEnd:
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
			if m.pendingApproval == nil && m.pendingProposal == nil && m.interactiveCheckInPaused && executionNeedsUserDrivenResume(m.activeExecution) {
				return m.resumePausedInteractiveCheckIns()
			}
			if m.pendingApproval == nil && m.pendingProposal == nil && m.pendingContinueAfterCommand {
				return m.continueAfterLatestCommand()
			}
			if m.pendingApproval == nil && m.pendingProposal == nil && m.activePlan != nil {
				return m.continueActivePlan()
			}
			return m.primaryAction()
		case tea.KeyCtrlJ:
			if !m.sendingFullscreenKeys && m.composerLocked() {
				return m, nil
			}
			m.insertTextAtCursor("\n")
			return m, nil
		case tea.KeyCtrlE:
			if m.pendingApproval == nil && m.pendingProposal == nil && m.activePlan != nil {
				return m.continueActivePlan()
			}
			return m.primaryAction()
		case tea.KeyCtrlY:
			if m.sendingFullscreenKeys {
				return m.submitWithOptions(true)
			}
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
			if m.pendingApproval == nil && m.interactiveCheckInPaused && executionNeedsUserDrivenResume(m.activeExecution) {
				return m.focusAgentComposerForActiveExecution()
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
			if m.pendingDangerousConfirm != nil {
				return m.confirmDangerousApprovalMode()
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
		case tea.KeyInsert:
			m.toggleOverwriteMode()
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
					m.autoOpenedFullscreenKeys = false
					m.setInput("")
					return m, nil
				}
				if len(msg.Runes) == 1 && strings.TrimSpace(m.input) == "" && m.editingProposal == nil && m.refiningProposal == nil && m.refiningApproval == nil {
					switch msg.Runes[0] {
					case 'Y':
						if m.pendingDangerousConfirm != nil {
							return m.confirmDangerousApprovalMode()
						}
						return m.primaryAction()
					case 'N':
						if m.pendingDangerousConfirm != nil {
							m.pendingDangerousConfirm = nil
							return m, nil
						}
						if m.pendingProposal != nil {
							return m.rejectProposal()
						}
						return m.decideApproval(controller.DecisionReject)
					case 'R':
						if m.pendingProposal != nil {
							return m.refineProposal()
						}
						if m.pendingApproval == nil && m.interactiveCheckInPaused && executionNeedsUserDrivenResume(m.activeExecution) {
							return m.focusAgentComposerForActiveExecution()
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
				inserted := string(msg.Runes)
				if msg.Paste {
					inserted = sanitizePastedText(inserted)
				} else {
					inserted = sanitizeComposerInsertText(inserted)
				}
				if inserted == "" {
					return m, nil
				}
				m.insertTextAtCursor(inserted)
				return m, nil
			}
		}
	}

	return m, nil
}

func (m Model) composerLocked() bool {
	return (m.startupNotice != nil || m.pendingDangerousConfirm != nil || m.pendingFullscreen != nil || m.pendingProposal != nil || m.pendingApproval != nil) && m.editingProposal == nil && m.refiningProposal == nil && m.refiningApproval == nil
}

func shouldReplaceDisplayedActivePlan(activePlan *controller.ActivePlan, prompt string) bool {
	if activePlan == nil {
		return false
	}

	prompt = strings.ToLower(strings.TrimSpace(prompt))
	if prompt == "" {
		return false
	}

	for _, marker := range []string{
		"continue",
		"resume",
		"keep going",
		"go on",
		"what next",
		"what's next",
		"whats next",
		"next step",
		"next steps",
		"current plan",
		"active plan",
		"current checklist",
		"active checklist",
	} {
		if strings.Contains(prompt, marker) {
			return false
		}
	}

	return true
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

	if m.pendingDangerousConfirm != nil {
		switch unicode.ToUpper(msg.Runes[0]) {
		case 'Y':
			model, cmd := m.confirmDangerousApprovalMode()
			return model, true, cmd
		case 'N':
			m.pendingDangerousConfirm = nil
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

func (m Model) submit() (tea.Model, tea.Cmd) {
	return m.submitWithOptions(false)
}

func (m Model) submitWithOptions(appendEnterToFullscreenKeys bool) (tea.Model, tea.Cmd) {
	if (m.startupNotice != nil || m.pendingDangerousConfirm != nil) && !m.sendingFullscreenKeys {
		return m, nil
	}

	text := strings.TrimSpace(m.input)
	if m.sendingFullscreenKeys {
		autoAppendEnter := !appendEnterToFullscreenKeys &&
			m.activeExecution != nil &&
			m.activeExecution.State == controller.CommandExecutionAwaitingInput &&
			shouldAutoAppendEnterForActiveExecution(m.activeExecution, m.liveShellTail, m.activeExecution.LatestOutputTail)
		rawKeys := fullscreenKeysForSubmit(m.input, appendEnterToFullscreenKeys || autoAppendEnter)
		if rawKeys == "" {
			return m, nil
		}
		if !m.consumeFreshActiveKeysLease() {
			return m, m.activeKeysGuardCmd()
		}
		logging.Trace("tui.fullscreen_keys.submit", "keys", rawKeys)
		m.sendingFullscreenKeys = false
		m.suppressAutoFullscreenKeysForCurrentPrompt()
		m.setInput("")
		return m, sendFullscreenKeysCmd(m.takeControl, m.activeExecutionPaneID(), rawKeys)
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

	if m.mode == AgentMode && shouldReplaceDisplayedActivePlan(m.activePlan, text) {
		m.activePlan = nil
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

		m.appendLocalTranscriptEcho(Entry{Title: "user", Body: text})
		m.busy = true
		m.busyStartedAt = time.Now()
		if !recoveryAgentPrompt {
			m.showShellTail = false
			m.liveShellTail = ""
			m.syncActiveExecution(nil)
		}
		m.refreshTranscriptViewport()
		prompt := text
		refining := m.refiningApproval
		refiningProposal := m.refiningProposal
		m.refiningApproval = nil
		m.refiningProposal = nil
		ctx, cancel := context.WithTimeout(context.Background(), m.currentAgentTurnTimeout())
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

	m.appendLocalTranscriptEcho(Entry{Title: "shell", Body: text})
	m.busy = true
	command := text
	logging.Trace("tui.submit.shell", "command", command)
	return m.startTrackedShellRequest(func(next *Model) {
		next.directShellPending = true
	}, func(ctx context.Context) ([]controller.TranscriptEvent, error) {
		return m.ctrl.SubmitShellCommand(ctx, command)
	})
}

func (m Model) handleComposerCommand(text string) (bool, tea.Model, tea.Cmd) {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	switch lower {
	case "/onboard", "/onboarding":
		m.input = ""
		m.currentHistory().reset()
		next, cmd := m.openOnboarding()
		return true, next, cmd
	case "/provider", "/providers":
		m.input = ""
		m.currentHistory().reset()
		next, cmd := m.openActiveProviderSettings()
		return true, next, cmd
	case "/model", "/models":
		m.input = ""
		m.currentHistory().reset()
		next, cmd := m.openActiveModelSettings()
		return true, next, cmd
	case "/help":
		m.input = ""
		m.currentHistory().reset()
		m.helpOpen = true
		m.helpScroll = 0
		return true, m, nil
	case "/new":
		m.input = ""
		m.currentHistory().reset()
		if m.ctrl == nil {
			m.appendTranscriptEntry(Entry{Title: "error", Body: "controller is not available"})
			return true, m, nil
		}
		next, cmd := m.startControllerRequest(func(next *Model) {
			next.pendingContextAction = contextActionNewTask
		}, func(ctx context.Context) ([]controller.TranscriptEvent, error) {
			return m.ctrl.StartNewTask(ctx)
		}, false)
		return true, next, cmd
	case "/compact":
		m.input = ""
		m.currentHistory().reset()
		if m.ctrl == nil {
			m.appendTranscriptEntry(Entry{Title: "error", Body: "controller is not available"})
			return true, m, nil
		}
		next, cmd := m.startControllerRequest(func(next *Model) {
			next.pendingContextAction = contextActionCompact
		}, func(ctx context.Context) ([]controller.TranscriptEvent, error) {
			return m.ctrl.CompactTask(ctx)
		}, false)
		return true, next, cmd
	case "/quit", "/exit":
		m.input = ""
		m.currentHistory().reset()
		return true, m, tea.Quit
	default:
		if strings.HasPrefix(trimmed, "/") {
			if strings.HasPrefix(lower, "/approvals") {
				return m.handleApprovalsCommand(trimmed)
			}
			m.setInput("")
			m.currentHistory().reset()
			m.appendTranscriptEntry(Entry{
				Title: "system",
				Body:  fmt.Sprintf("Unknown slash command: %s. Try %s.", trimmed, strings.Join(primarySlashCommands(), ", ")),
			})
			return true, m, nil
		}
		return false, m, nil
	}
}

func primarySlashCommands() []string {
	return []string{"/help", "/approvals", "/new", "/compact", "/onboard", "/provider", "/model", "/quit"}
}

func (m Model) handleApprovalsCommand(text string) (bool, tea.Model, tea.Cmd) {
	m.input = ""
	m.currentHistory().reset()
	if m.ctrl == nil {
		m.appendTranscriptEntry(Entry{Title: "error", Body: "controller is not available"})
		return true, m, nil
	}

	fields := strings.Fields(strings.ToLower(strings.TrimSpace(text)))
	if len(fields) <= 1 {
		mode := m.ctrl.ApprovalMode()
		m.appendTranscriptEntry(Entry{Title: "system", Body: controller.ApprovalModeStatusBody(mode)})
		return true, m, nil
	}

	switch fields[1] {
	case "confirm", "auto":
		mode := controller.ApprovalMode(fields[1])
		next, cmd := m.startControllerRequest(nil, func(ctx context.Context) ([]controller.TranscriptEvent, error) {
			return m.ctrl.SetApprovalMode(ctx, mode)
		}, false)
		return true, next, cmd
	case "dangerous":
		m.pendingDangerousConfirm = &dangerousApprovalConfirm{mode: controller.ApprovalModeDanger}
		return true, m, nil
	default:
		m.appendTranscriptEntry(Entry{
			Title: "system",
			Body:  fmt.Sprintf("Unknown approvals mode: %s. Try /approvals, /approvals confirm, /approvals auto, or /approvals dangerous.", fields[1]),
		})
		return true, m, nil
	}
}

func (m Model) confirmDangerousApprovalMode() (tea.Model, tea.Cmd) {
	if m.pendingDangerousConfirm == nil || m.ctrl == nil {
		return m, nil
	}
	mode := m.pendingDangerousConfirm.mode
	m.pendingDangerousConfirm = nil
	next, cmd := m.startControllerRequest(nil, func(ctx context.Context) ([]controller.TranscriptEvent, error) {
		return m.ctrl.SetApprovalMode(ctx, mode)
	}, false)
	return next, cmd
}

func (m *Model) applySuccessfulContextAction(action contextActionKind) {
	switch action {
	case contextActionNewTask:
		m.entries = initialEntries(m.workspace)
		m.selectedEntry = len(m.entries) - 1
		m.transcriptScroll = 0
		m.transcriptFollow = true
		m.expandedCommandEntry = -1
		m.detailOpen = false
		m.detailScroll = 0
		m.pendingApproval = nil
		m.pendingProposal = nil
		m.refiningApproval = nil
		m.refiningProposal = nil
		m.editingProposal = nil
		m.activePlan = nil
		m.showShellTail = false
		m.liveShellTail = ""
		m.syncActiveExecution(nil)
	case contextActionCompact:
		m.refiningApproval = nil
		m.refiningProposal = nil
		m.editingProposal = nil
	}
}

func (m Model) startTrackedShellRequest(mark func(*Model), invoke func(context.Context) ([]controller.TranscriptEvent, error)) (tea.Model, tea.Cmd) {
	return m.startControllerRequest(mark, invoke, true)
}

func (m Model) startControllerRequest(mark func(*Model), invoke func(context.Context) ([]controller.TranscriptEvent, error), followsShell bool) (tea.Model, tea.Cmd) {
	m.busy = true
	m.busyStartedAt = time.Now()
	m.showShellTail = followsShell
	if !followsShell {
		m.liveShellTail = ""
		m.syncActiveExecution(nil)
	}
	if mark != nil {
		mark(&m)
	}
	m.refreshTranscriptViewport()

	ctx, cancel := context.WithCancel(context.Background())
	m.inFlightCancel = cancel
	cmds := []tea.Cmd{func() tea.Msg {
		defer cancel()

		events, err := invoke(ctx)
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}, tickBusy()}
	if followsShell {
		cmds = append(cmds, m.pollShellTailCmd(), m.pollActiveExecutionCmd())
	}
	return m, tea.Batch(cmds...)
}

func (m Model) primaryAction() (tea.Model, tea.Cmd) {
	switch {
	case m.pendingApproval == nil && m.pendingProposal == nil && m.interactiveCheckInPaused && executionNeedsUserDrivenResume(m.activeExecution):
		return m.resumePausedInteractiveCheckIns()
	case m.pendingApproval != nil:
		logging.Trace("tui.primary_action", "action", "approve", "approval_id", m.pendingApproval.ID)
		return m.decideApproval(controller.DecisionApprove)
	case m.pendingProposal != nil && m.pendingProposal.Keys != "":
		logging.Trace("tui.primary_action", "action", "send_proposal_keys", "keys", previewFullscreenKeys(m.pendingProposal.Keys))
		return m.runProposalKeys()
	case m.pendingProposal != nil && m.pendingProposal.Patch != "":
		logging.Trace("tui.primary_action", "action", "apply_proposal_patch")
		return m.runProposalPatch()
	case m.pendingProposal != nil && m.pendingProposal.Kind == controller.ProposalInspectContext:
		logging.Trace("tui.primary_action", "action", "inspect_proposal_context")
		return m.runProposalInspectContext()
	case m.pendingProposal != nil && m.pendingProposal.Command != "":
		logging.Trace("tui.primary_action", "action", "run_proposal", "command", m.pendingProposal.Command)
		return m.runProposalCommand()
	default:
		return m, nil
	}
}

func (m Model) resumePausedInteractiveCheckIns() (tea.Model, tea.Cmd) {
	if !m.interactiveCheckInPaused || m.activeExecution == nil {
		return m, nil
	}

	m.interactiveCheckInPaused = false
	m.interactiveCheckInCount = 0
	m.lastCheckInAt = time.Time{}
	return m, tea.Batch(tickBusy(), m.pollActiveExecutionCmd(), m.pollShellTailCmd())
}

func (m Model) continueAfterLatestCommand() (tea.Model, tea.Cmd) {
	if m.busy || m.ctrl == nil {
		return m, nil
	}

	m.pendingContinueAfterCommand = false
	m.busy = true
	m.busyStartedAt = time.Now()
	m.showShellTail = false
	m.liveShellTail = ""
	m.activeExecution = nil
	ctx, cancel := context.WithTimeout(context.Background(), m.currentAgentTurnTimeout())
	m.inFlightCancel = cancel
	return m, tea.Batch(func() tea.Msg {
		defer cancel()

		events, err := m.ctrl.ContinueAfterCommand(ctx)
		return controllerEventsMsg{
			events: events,
			err:    err,
		}
	}, tickBusy(), m.pollShellTailCmd(), m.pollActiveExecutionCmd())
}

func (m Model) focusAgentComposerForActiveExecution() (tea.Model, tea.Cmd) {
	if m.activeExecution == nil {
		return m, nil
	}

	m.mode = AgentMode
	m.recomputeCompletion()
	return m, nil
}

func (m Model) confirmFullscreenAction() (tea.Model, tea.Cmd) {
	if m.pendingFullscreen == nil || m.ctrl == nil {
		return m, nil
	}

	action := *m.pendingFullscreen
	m.pendingFullscreen = nil

	switch action.Kind {
	case fullscreenActionShellSubmit:
		m.setInput("")
		return m.startTrackedShellRequest(func(next *Model) {
			next.directShellPending = true
		}, func(ctx context.Context) ([]controller.TranscriptEvent, error) {
			return m.ctrl.SubmitShellCommand(ctx, action.Command)
		})
	case fullscreenActionProposalRun:
		if m.pendingProposal == nil || strings.TrimSpace(m.pendingProposal.Command) == "" {
			return m, nil
		}
		logging.Trace("tui.proposal.run", "command", m.pendingProposal.Command)
		command := m.pendingProposal.Command
		m.pendingProposal = nil
		m.refiningProposal = nil
		m.editingProposal = nil
		return m.startTrackedShellRequest(func(next *Model) {
			next.proposalRunPending = true
		}, func(ctx context.Context) ([]controller.TranscriptEvent, error) {
			return m.ctrl.SubmitProposedShellCommand(ctx, command)
		})
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
		approvalID := m.pendingApproval.ID
		m.pendingApproval = nil
		m.pendingProposal = nil
		m.refiningApproval = nil
		m.refiningProposal = nil
		m.editingProposal = nil
		return m.startTrackedShellRequest(func(next *Model) {
			next.approvalInFlight = true
		}, func(ctx context.Context) ([]controller.TranscriptEvent, error) {
			return m.ctrl.DecideApproval(ctx, approvalID, controller.DecisionApprove, "")
		})
	default:
		return m, nil
	}
}

func (m Model) shouldAutoContinue(events []controller.TranscriptEvent) bool {
	if m.ctrl == nil || m.directShellPending {
		return false
	}
	hasCommandResult := containsEventKind(events, controller.EventCommandResult)
	hasPatchResult := containsEventKind(events, controller.EventPatchApplyResult)
	if !hasCommandResult && !hasPatchResult {
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
	ctx, cancel := context.WithTimeout(context.Background(), m.currentAgentTurnTimeout())
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

func (m Model) persistentTakeControlConfig() takeControlConfig {
	config := m.takeControl
	config.TrackedPaneID = m.persistentTrackedPaneID()
	config.TemporaryPane = false
	return config
}

func (m Model) executionTakeControlConfig() takeControlConfig {
	if m.activeExecution == nil {
		return takeControlConfig{}
	}
	paneID := m.activeExecutionTakeControlPaneID()
	if strings.TrimSpace(paneID) == "" {
		return takeControlConfig{}
	}

	config := m.takeControl
	config.TrackedPaneID = paneID
	config.TemporaryPane = true
	config.DetachKey = ExecutionTakeControlKey
	if strings.TrimSpace(m.activeExecution.TrackedShell.SessionName) != "" {
		config.SessionName = strings.TrimSpace(m.activeExecution.TrackedShell.SessionName)
	}
	return config
}

func (m Model) takeControlPersistentShellNow() (tea.Model, tea.Cmd) {
	return m.takeControlNow(m.persistentTakeControlConfig(), false)
}

func (m Model) takeControlExecutionNow() (tea.Model, tea.Cmd) {
	return m.takeControlNow(m.executionTakeControlConfig(), true)
}

func (m Model) takeControlNow(config takeControlConfig, targetExecution bool) (tea.Model, tea.Cmd) {
	if !config.enabled() {
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
	if m.activeExecution != nil && (targetExecution || m.activeExecutionUsesTrackedShell()) {
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

	return m, newTakeControlCmd(config)
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
		return m, interruptShellCmd(m.takeControl, m.activeExecutionPaneID())
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
	m.settingsApprovalIdx = currentSettingsApprovalIndex(m.ctrl)
	m.settingsProviders = buildSettingsProviderEntries(choices)
	m.settingsProviderIdx = m.currentSettingsProviderIndex()
	m.settingsConfig = nil
	m.settingsModelCatalog = nil
	m.settingsModels = nil
	m.settingsModelIdx = 0
	m.settingsModelFilter = ""
	m.settingsModelScope = ""
	m.settingsModelInfo = false
	m.settingsBanner = ""
	return m, nil
}

func (m Model) openActiveProviderSettings() (tea.Model, tea.Cmd) {
	nextAny, cmd := m.openSettings()
	next := nextAny.(Model)
	if cmd != nil || !next.settingsOpen {
		return next, cmd
	}

	next.settingsStep = settingsStepActiveProvider
	next.settingsIndex = 2
	next.settingsProviderIdx = next.currentSettingsProviderIndex()
	return next, nil
}

func (m Model) openActiveModelSettings() (tea.Model, tea.Cmd) {
	nextAny, cmd := m.openSettings()
	next := nextAny.(Model)
	if cmd != nil || !next.settingsOpen {
		return next, cmd
	}

	next.settingsStep = settingsStepActiveModels
	next.settingsIndex = 3
	next.settingsModelScope = next.activeProvider.Preset
	return next.loadSettingsModels()
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
	command := m.pendingProposal.Command
	m.pendingProposal = nil
	m.refiningProposal = nil
	m.editingProposal = nil
	return m.startTrackedShellRequest(func(next *Model) {
		next.proposalRunPending = true
	}, func(ctx context.Context) ([]controller.TranscriptEvent, error) {
		return m.ctrl.SubmitProposedShellCommand(ctx, command)
	})
}

func (m Model) runProposalPatch() (tea.Model, tea.Cmd) {
	if m.busy || m.ctrl == nil || m.pendingProposal == nil || strings.TrimSpace(m.pendingProposal.Patch) == "" {
		return m, nil
	}

	logging.Trace("tui.proposal.apply_patch")
	patch := m.pendingProposal.Patch
	target := m.pendingProposal.PatchTarget
	m.pendingProposal = nil
	m.refiningProposal = nil
	m.editingProposal = nil
	return m.startControllerRequest(func(next *Model) {
		next.proposalRunPending = true
	}, func(ctx context.Context) ([]controller.TranscriptEvent, error) {
		return m.ctrl.ApplyProposedPatch(ctx, patch, target)
	}, false)
}

func (m Model) runProposalInspectContext() (tea.Model, tea.Cmd) {
	if m.busy || m.ctrl == nil || m.pendingProposal == nil || m.pendingProposal.Kind != controller.ProposalInspectContext {
		return m, nil
	}

	logging.Trace("tui.proposal.inspect_context")
	m.pendingProposal = nil
	m.refiningProposal = nil
	m.editingProposal = nil
	return m.startControllerRequest(func(next *Model) {
		next.proposalRunPending = true
	}, func(ctx context.Context) ([]controller.TranscriptEvent, error) {
		return m.ctrl.InspectProposedContext(ctx)
	}, false)
}

func (m Model) runProposalKeys() (tea.Model, tea.Cmd) {
	if m.pendingProposal == nil || m.pendingProposal.Keys == "" || !m.canSendActiveKeys() {
		return m, nil
	}
	if !m.consumeFreshActiveKeysLease() {
		return m, m.activeKeysGuardCmd()
	}

	keys := normalizeFullscreenKeys(m.pendingProposal.Keys)
	logging.Trace("tui.proposal.send_keys", "keys", previewFullscreenKeys(keys))
	m.pendingProposal = nil
	m.refiningProposal = nil
	m.editingProposal = nil
	m.suppressAutoFullscreenKeysForCurrentPrompt()
	m.setInput("")
	return m, sendFullscreenKeysCmd(m.takeControl, m.activeExecutionPaneID(), keys)
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
	approvalID := m.pendingApproval.ID
	command := m.pendingApproval.Command
	isPatchApproval := m.pendingApproval.Kind == controller.ApprovalPatch
	if decision == controller.DecisionApprove && !isPatchApproval && m.shouldConfirmFullscreenBeforeShellAction() {
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
		return m.startControllerRequest(func(next *Model) {
			next.approvalInFlight = true
		}, func(ctx context.Context) ([]controller.TranscriptEvent, error) {
			return m.ctrl.DecideApproval(ctx, approvalID, decision, "")
		}, !isPatchApproval)
	}

	m.busy = true
	m.busyStartedAt = time.Now()
	m.approvalInFlight = true
	m.showShellTail = false
	m.liveShellTail = ""
	m.syncActiveExecution(nil)
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

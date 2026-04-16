package shell

import (
	"strings"
	"sync"
	"time"
)

type MonitorState string

type CompletionCause string

const (
	CompletionCauseUnknown           CompletionCause = "unknown"
	CompletionCauseEndMarker         CompletionCause = "end_marker"
	CompletionCauseEndMarkerInferred CompletionCause = "end_marker_inferred"
	CompletionCauseSemanticLifecycle CompletionCause = "semantic_lifecycle"
	CompletionCausePromptReturn      CompletionCause = "prompt_return"
	CompletionCauseContextTransition CompletionCause = "context_transition"
)

type SignalConfidence string

const (
	ConfidenceStrong SignalConfidence = "strong"
	ConfidenceMedium SignalConfidence = "medium"
	ConfidenceLow    SignalConfidence = "low"
)

const (
	MonitorStateQueued                MonitorState = "queued"
	MonitorStateRunning               MonitorState = "running"
	MonitorStateAwaitingInput         MonitorState = "awaiting_input"
	MonitorStateInteractiveFullscreen MonitorState = "interactive_fullscreen"
	MonitorStateCompleted             MonitorState = "completed"
	MonitorStateFailed                MonitorState = "failed"
	MonitorStateCanceled              MonitorState = "canceled"
	MonitorStateLost                  MonitorState = "lost"
)

type MonitorSnapshot struct {
	CommandID         string
	Command           string
	State             MonitorState
	StartedAt         time.Time
	CompletedAt       *time.Time
	LatestOutputTail  string
	LatestDisplayTail string
	ForegroundCommand string
	SemanticShell     bool
	SemanticSource    string
	ExitCode          *int
	ShellContext      PromptContext
	Error             string
}

const InterruptedExitCode = 130

type CommandMonitor interface {
	Snapshot() MonitorSnapshot
	Updates() <-chan MonitorSnapshot
	Wait() (TrackedExecution, error)
}

type trackedCommandMonitor struct {
	mu       sync.RWMutex
	snapshot MonitorSnapshot
	updates  chan MonitorSnapshot
	done     chan struct{}

	result TrackedExecution
	err    error
}

func newTrackedCommandMonitor(commandID string, command string) *trackedCommandMonitor {
	now := time.Now()
	return &trackedCommandMonitor{
		snapshot: MonitorSnapshot{
			CommandID: commandID,
			Command:   command,
			State:     MonitorStateQueued,
			StartedAt: now,
		},
		updates: make(chan MonitorSnapshot, 32),
		done:    make(chan struct{}),
	}
}

func (m *trackedCommandMonitor) Snapshot() MonitorSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

func (m *trackedCommandMonitor) Updates() <-chan MonitorSnapshot {
	return m.updates
}

func (m *trackedCommandMonitor) Wait() (TrackedExecution, error) {
	<-m.done

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.result, m.err
}

func (m *trackedCommandMonitor) setState(state MonitorState) {
	m.mu.Lock()
	if m.snapshot.State != state {
		m.snapshot.State = state
	}
	snapshot := m.snapshot
	m.mu.Unlock()
	m.publish(snapshot)
}

func (m *trackedCommandMonitor) updateTail(tail string, displayTail string) {
	m.mu.Lock()
	if m.snapshot.LatestOutputTail == tail && m.snapshot.LatestDisplayTail == displayTail {
		m.mu.Unlock()
		return
	}
	m.snapshot.LatestOutputTail = tail
	m.snapshot.LatestDisplayTail = displayTail
	snapshot := m.snapshot
	m.mu.Unlock()
	m.publish(snapshot)
}

func (m *trackedCommandMonitor) updateForegroundCommand(command string) {
	command = strings.TrimSpace(command)
	m.mu.Lock()
	if m.snapshot.ForegroundCommand == command {
		m.mu.Unlock()
		return
	}
	m.snapshot.ForegroundCommand = command
	snapshot := m.snapshot
	m.mu.Unlock()
	m.publish(snapshot)
}

func (m *trackedCommandMonitor) updateSemanticMetadata(enabled bool, source string) {
	m.mu.Lock()
	if m.snapshot.SemanticShell == enabled && m.snapshot.SemanticSource == source {
		m.mu.Unlock()
		return
	}
	m.snapshot.SemanticShell = enabled
	m.snapshot.SemanticSource = source
	snapshot := m.snapshot
	m.mu.Unlock()
	m.publish(snapshot)
}

func (m *trackedCommandMonitor) updateShellContext(context PromptContext) {
	if context.PromptLine() == "" {
		return
	}

	m.mu.Lock()
	m.snapshot.ShellContext = context
	snapshot := m.snapshot
	m.mu.Unlock()
	m.publish(snapshot)
}

func (m *trackedCommandMonitor) finish(result TrackedExecution, err error, state MonitorState) {
	completedAt := time.Now()
	result.State = state

	m.mu.Lock()
	m.result = result
	m.err = err
	if result.CommandID != "" {
		m.snapshot.CommandID = result.CommandID
	}
	m.snapshot.State = state
	m.snapshot.CompletedAt = &completedAt
	m.snapshot.LatestOutputTail = result.Captured
	m.snapshot.LatestDisplayTail = result.DisplayCaptured
	m.snapshot.SemanticShell = result.SemanticShell
	m.snapshot.SemanticSource = result.SemanticSource
	if result.ShellContext.PromptLine() != "" {
		m.snapshot.ShellContext = result.ShellContext
	}
	if err != nil {
		m.snapshot.Error = err.Error()
	}
	if state == MonitorStateCompleted {
		exitCode := result.ExitCode
		m.snapshot.ExitCode = &exitCode
	}
	snapshot := m.snapshot
	m.mu.Unlock()

	m.publish(snapshot)
	close(m.done)
	close(m.updates)
}

func (m *trackedCommandMonitor) publish(snapshot MonitorSnapshot) {
	select {
	case m.updates <- snapshot:
	default:
	}
}

func monitorTail(body string, command string) string {
	cleanBody := sanitizeCapturedBody(body)
	cleanBody = stripEchoedCommand(cleanBody, command)
	if cleanBody == "" {
		return ""
	}

	lines := splitLines(cleanBody)
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}

	return stringsJoin(lines, "\n")
}

func monitorDisplayTail(body string, command string) string {
	displayBody := sanitizeDisplayBody(body)
	displayBody = stripEchoedCommand(displayBody, command)
	if displayBody == "" {
		return ""
	}

	lines := splitLines(displayBody)
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}

	return stringsJoin(lines, "\n")
}

func splitLines(value string) []string {
	return strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
}

func stringsJoin(lines []string, sep string) string {
	return strings.TrimSpace(strings.Join(lines, sep))
}

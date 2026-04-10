package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"aiterm/internal/patchapply"
	"aiterm/internal/shell"
)

type stubAgent struct {
	response  AgentResponse
	err       error
	lastInput AgentInput
}

func (s *stubAgent) Respond(_ context.Context, input AgentInput) (AgentResponse, error) {
	s.lastInput = input
	return s.response, s.err
}

func setPrimaryExecutionForTest(controller *LocalController, execution CommandExecution) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.registerExecutionLocked(execution)
}

type stubRunner struct {
	result         shell.TrackedExecution
	err            error
	commands       []string
	paneIDs        []string
	resolvedPaneID string
	run            func(context.Context, string, string, time.Duration) (shell.TrackedExecution, error)
}

func (s *stubRunner) RunTrackedCommand(ctx context.Context, paneID string, command string, timeout time.Duration) (shell.TrackedExecution, error) {
	s.commands = append(s.commands, command)
	s.paneIDs = append(s.paneIDs, paneID)
	if s.run != nil {
		return s.run(ctx, paneID, command, timeout)
	}
	if s.result.Command == "" {
		s.result.Command = command
	}
	if s.err != nil {
		return s.result, s.err
	}
	return s.result, nil
}

func (s *stubRunner) ResolveTrackedPane(_ context.Context, paneID string) (string, error) {
	if strings.TrimSpace(s.resolvedPaneID) != "" {
		return s.resolvedPaneID, nil
	}
	return paneID, nil
}

type ownedExecutionRunner struct {
	stubRunner
	ownedPane    shell.OwnedExecutionPane
	startDir     string
	cleanupCalls int
}

func (o *ownedExecutionRunner) CreateOwnedExecutionPane(_ context.Context, startDir string) (shell.OwnedExecutionPane, func(context.Context) error, error) {
	o.startDir = startDir
	cleanup := func(context.Context) error {
		o.cleanupCalls++
		return nil
	}
	return o.ownedPane, cleanup, nil
}

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
	result  shell.TrackedExecution
}

func (b *blockingRunner) RunTrackedCommand(_ context.Context, _ string, _ string, _ time.Duration) (shell.TrackedExecution, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-b.release
	return b.result, nil
}

type monitoringRunner struct {
	monitor       *manualMonitor
	attachMonitor *manualMonitor
	attachFunc    func(context.Context, string) (shell.CommandMonitor, error)
	attachCalls   int
	commands      []string
	started       chan struct{}
}

func (m *monitoringRunner) RunTrackedCommand(_ context.Context, _ string, _ string, _ time.Duration) (shell.TrackedExecution, error) {
	return shell.TrackedExecution{}, errors.New("unexpected fallback RunTrackedCommand call")
}

func (m *monitoringRunner) StartTrackedCommand(_ context.Context, _ string, command string, _ time.Duration) (shell.CommandMonitor, error) {
	m.commands = append(m.commands, command)
	if m.started != nil {
		select {
		case m.started <- struct{}{}:
		default:
		}
	}
	return m.monitor, nil
}

func (m *monitoringRunner) AttachForegroundCommand(ctx context.Context, paneID string) (shell.CommandMonitor, error) {
	m.attachCalls++
	if m.attachFunc != nil {
		return m.attachFunc(ctx, paneID)
	}
	if m.attachMonitor == nil {
		return nil, nil
	}
	return m.attachMonitor, nil
}

func (m *monitoringRunner) waitForStart(t *testing.T) {
	t.Helper()
	if m.started == nil {
		m.started = make(chan struct{}, 1)
	}
	select {
	case <-m.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for monitor runner to start")
	}
}

type manualMonitor struct {
	snapshot shell.MonitorSnapshot
	updates  chan shell.MonitorSnapshot
	done     chan struct{}
	result   shell.TrackedExecution
	err      error
}

func newManualMonitor() *manualMonitor {
	return &manualMonitor{
		updates: make(chan shell.MonitorSnapshot, 16),
		done:    make(chan struct{}),
	}
}

func (m *manualMonitor) Snapshot() shell.MonitorSnapshot {
	return m.snapshot
}

func (m *manualMonitor) Updates() <-chan shell.MonitorSnapshot {
	return m.updates
}

func (m *manualMonitor) Wait() (shell.TrackedExecution, error) {
	<-m.done
	return m.result, m.err
}

func (m *manualMonitor) publish(snapshot shell.MonitorSnapshot) {
	m.snapshot = snapshot
	m.updates <- snapshot
}

func (m *manualMonitor) finish(result shell.TrackedExecution, err error) {
	m.result = result
	m.err = err
	close(m.done)
	close(m.updates)
}

type stubContextReader struct {
	output         string
	snapshot       string
	context        shell.PromptContext
	contexts       []shell.PromptContext
	observed       shell.ObservedShellState
	err            error
	resolvedPaneID string
	paneIDs        []string
	contextCalls   int
}

func (s *stubContextReader) CaptureRecentOutput(_ context.Context, paneID string, _ int) (string, error) {
	s.paneIDs = append(s.paneIDs, paneID)
	if s.err != nil {
		return "", s.err
	}
	if s.snapshot != "" {
		return s.snapshot, nil
	}

	return s.output, nil
}

func (s *stubContextReader) CaptureRecentOutputDisplay(_ context.Context, paneID string, _ int) (string, error) {
	return s.CaptureRecentOutput(context.Background(), paneID, 0)
}

func (s *stubContextReader) CaptureShellContext(context.Context, string) (shell.PromptContext, error) {
	if s.err != nil {
		return shell.PromptContext{}, s.err
	}
	if len(s.contexts) > 0 {
		index := s.contextCalls
		if index >= len(s.contexts) {
			index = len(s.contexts) - 1
		}
		s.contextCalls++
		return s.contexts[index], nil
	}
	if s.context.PromptLine() != "" || s.context.LastExitCode != nil {
		return s.context, nil
	}

	return shell.PromptContext{
		User:      "localuser",
		Host:      "workstation",
		Directory: "/workspace/project",
	}, nil
}

func (s *stubContextReader) CaptureObservedShellState(ctx context.Context, paneID string) (shell.ObservedShellState, error) {
	if s.err != nil {
		return shell.ObservedShellState{}, s.err
	}
	if s.observed.PromptContext.PromptLine() != "" || s.observed.Location.Kind != "" || s.observed.HasPromptContext || s.observed.HasSemanticState {
		return s.observed, nil
	}
	context, err := s.CaptureShellContext(ctx, paneID)
	if err != nil {
		return shell.ObservedShellState{}, err
	}
	return shell.ObservedShellState{
		PromptContext:    context,
		HasPromptContext: context.PromptLine() != "" || context.LastExitCode != nil,
		Location:         shell.InferShellLocation(context, ""),
	}, nil
}

func (s *stubContextReader) ResolveTrackedPane(_ context.Context, paneID string) (string, error) {
	if strings.TrimSpace(s.resolvedPaneID) != "" {
		return s.resolvedPaneID, nil
	}
	return paneID, nil
}

type stubPatchApplier struct {
	result    patchapply.Result
	err       error
	patches   []string
	validates []string
}

func (s *stubPatchApplier) Validate(_ context.Context, patch string) (patchapply.Result, error) {
	s.validates = append(s.validates, patch)
	return s.result, s.err
}

func (s *stubPatchApplier) Apply(_ context.Context, patch string) (patchapply.Result, error) {
	s.patches = append(s.patches, patch)
	return s.result, s.err
}

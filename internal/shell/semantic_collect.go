package shell

import (
	"context"
	"strings"
	"time"
)

const (
	semanticSourceNone              = ""
	semanticSourceStream            = "osc_stream"
	semanticSourceOSCCapture        = "osc_capture"
	semanticSourceState             = "state_file"
	semanticStateCommandStaleWindow = 5 * time.Second
)

type semanticObservation struct {
	State  semanticShellState
	Source string
}

type semanticCollector interface {
	Collect(ctx context.Context, paneID string, paneTTY string, currentPaneCommand string, promptContext PromptContext) (semanticObservation, bool)
}

type oscCaptureSemanticCollector struct {
	client escapedPaneClient
}

func (c oscCaptureSemanticCollector) Collect(ctx context.Context, paneID string, _ string, _ string, _ PromptContext) (semanticObservation, bool) {
	raw, err := c.client.CapturePaneEscaped(ctx, paneID, -200)
	if err != nil {
		return semanticObservation{}, false
	}
	state, ok := parseSemanticShellStateFromOSCCapture(raw)
	if !ok {
		return semanticObservation{}, false
	}
	return semanticObservation{State: state, Source: semanticSourceOSCCapture}, true
}

type stateFileSemanticCollector struct {
	stateDir string
}

func (c stateFileSemanticCollector) Collect(_ context.Context, _ string, paneTTY string, currentPaneCommand string, _ PromptContext) (semanticObservation, bool) {
	state, ok := readSemanticShellState(c.stateDir, paneTTY)
	if !ok {
		return semanticObservation{}, false
	}
	currentShell := strings.TrimSpace(strings.ToLower(currentPaneCommand))
	if currentShell != "" && state.Shell != "" && currentShell != state.Shell {
		return semanticObservation{}, false
	}
	if state.Event == semanticEventCommand && semanticStateNow().Sub(state.UpdatedAt) > semanticStateCommandStaleWindow {
		return semanticObservation{}, false
	}
	return semanticObservation{State: state, Source: semanticSourceState}, true
}

func (o *Observer) semanticCollectors() []semanticCollector {
	collectors := make([]semanticCollector, 0, 3)
	if collector := o.semanticStreamCollector(); collector != nil {
		collectors = append(collectors, collector)
	}
	if escapedClient, ok := o.client.(escapedPaneClient); ok {
		collectors = append(collectors, oscCaptureSemanticCollector{client: escapedClient})
	}
	if strings.TrimSpace(o.stateDir) != "" {
		collectors = append(collectors, stateFileSemanticCollector{stateDir: o.stateDir})
	}
	return collectors
}

func (o *Observer) semanticStreamCollector() semanticCollector {
	if strings.TrimSpace(o.stateDir) == "" {
		return nil
	}
	if _, ok := o.client.(pipePaneClient); !ok {
		return nil
	}

	o.semanticMu.Lock()
	defer o.semanticMu.Unlock()

	if o.streamCollector == nil {
		o.streamCollector = newStreamSemanticCollector(o, o.stateDir)
	}
	return o.streamCollector
}

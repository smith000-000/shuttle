package shell

import (
	"context"
	"strings"
)

const (
	semanticSourceNone       = ""
	semanticSourceStream     = "osc_stream"
	semanticSourceOSCCapture = "osc_capture"
	semanticSourceState      = "state_file"
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

func (c stateFileSemanticCollector) Collect(_ context.Context, _ string, paneTTY string, _ string, _ PromptContext) (semanticObservation, bool) {
	state, ok := readSemanticShellState(c.stateDir, paneTTY)
	if !ok {
		return semanticObservation{}, false
	}
	return semanticObservation{State: state, Source: semanticSourceState}, true
}

func (o *Observer) semanticCollectors() []semanticCollector {
	collectors := make([]semanticCollector, 0, 2)
	if escapedClient, ok := o.client.(escapedPaneClient); ok {
		collectors = append(collectors, oscCaptureSemanticCollector{client: escapedClient})
	}
	if strings.TrimSpace(o.stateDir) != "" {
		collectors = append(collectors, stateFileSemanticCollector{stateDir: o.stateDir})
	}
	return collectors
}

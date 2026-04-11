package shell

import (
	"context"
	"strings"

	"aiterm/internal/tmux"
)

type ShellLocationKind string

type ShellDirectorySource string

const (
	ShellLocationUnknown   ShellLocationKind = "unknown"
	ShellLocationLocal     ShellLocationKind = "local"
	ShellLocationRemote    ShellLocationKind = "remote"
	ShellLocationContainer ShellLocationKind = "container"
	ShellLocationNested    ShellLocationKind = "nested"

	ShellDirectorySourceUnknown        ShellDirectorySource = "unknown"
	ShellDirectorySourcePrompt         ShellDirectorySource = "prompt"
	ShellDirectorySourceProbe          ShellDirectorySource = "probe"
	ShellDirectorySourceCarriedForward ShellDirectorySource = "carried_forward"
)

type ShellLocation struct {
	Kind                ShellLocationKind
	User                string
	Host                string
	Directory           string
	DirectorySource     ShellDirectorySource
	DirectoryConfidence SignalConfidence
	Confidence          SignalConfidence
}

type ObservedShellState struct {
	Capture              string
	Tail                 string
	PromptContext        PromptContext
	HasPromptContext     bool
	CurrentPaneCommand   string
	PaneTTY              string
	AlternateOn          bool
	SemanticState        semanticShellState
	SemanticSource       string
	HasSemanticState     bool
	RememberedTransition shellTransitionKind
	Location             ShellLocation
}

func (o *Observer) observeShellState(ctx context.Context, paneID string, command string, captured string, paneInfo *tmux.Pane, baseContext PromptContext) ObservedShellState {
	state := ObservedShellState{
		Capture:              captured,
		Tail:                 monitorTail(captured, command),
		RememberedTransition: o.rememberedTransition(paneID),
	}

	if paneInfo != nil {
		state.CurrentPaneCommand = strings.TrimSpace(paneInfo.CurrentCommand)
		state.PaneTTY = strings.TrimSpace(paneInfo.TTY)
		state.AlternateOn = paneInfo.AlternateOn
	}

	if promptContext, ok := ParsePromptContextFromCapture(captured); ok {
		state.PromptContext = promptContext
		state.HasPromptContext = true
	}

	semanticBaseContext := baseContext
	if state.HasPromptContext {
		semanticBaseContext = state.PromptContext
	} else if semanticBaseContext.PromptLine() == "" {
		semanticBaseContext = o.promptHint
	}

	semanticState, semanticSource, hasSemanticState := o.captureSemanticShellState(ctx, paneID, state.PaneTTY, command, state.CurrentPaneCommand, semanticBaseContext)
	state.SemanticState = semanticState
	state.SemanticSource = semanticSource
	state.HasSemanticState = hasSemanticState
	if hasSemanticState {
		synthesized := synthesizePromptContext(semanticBaseContext, semanticState)
		if synthesized.PromptLine() != "" {
			state.PromptContext = synthesized
			state.HasPromptContext = true
		}
	}
	state.Location = inferShellLocation(state.PromptContext, state.CurrentPaneCommand, state.RememberedTransition)
	return state
}

func inferShellLocation(promptContext PromptContext, currentPaneCommand string, remembered shellTransitionKind) ShellLocation {
	location := ShellLocation{
		User:      strings.TrimSpace(promptContext.User),
		Host:      strings.TrimSpace(promptContext.Host),
		Directory: strings.TrimSpace(promptContext.Directory),
	}
	location.DirectorySource, location.DirectoryConfidence = promptDirectoryMetadata(location.Directory)

	switch detectShellTransition("", currentPaneCommand, promptContext, remembered).Kind {
	case shellTransitionRemote:
		location.Kind = ShellLocationRemote
		location.Confidence = ConfidenceStrong
	case shellTransitionExec:
		location.Kind = ShellLocationContainer
		location.Confidence = ConfidenceMedium
	case shellTransitionLocal:
		location.Kind = ShellLocationNested
		location.Confidence = ConfidenceMedium
	default:
		if promptContext.Remote {
			location.Kind = ShellLocationRemote
			location.Confidence = ConfidenceMedium
		} else if promptContext.PromptLine() != "" {
			location.Kind = ShellLocationLocal
			location.Confidence = ConfidenceMedium
		} else {
			location.Kind = ShellLocationUnknown
			location.Confidence = ConfidenceLow
		}
	}

	return location
}

func InferShellLocation(promptContext PromptContext, currentPaneCommand string) ShellLocation {
	return inferShellLocation(promptContext, currentPaneCommand, shellTransitionNone)
}

func promptDirectoryMetadata(directory string) (ShellDirectorySource, SignalConfidence) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return ShellDirectorySourceUnknown, ConfidenceLow
	}
	switch {
	case directory == "~", strings.HasPrefix(directory, "~/"):
		return ShellDirectorySourcePrompt, ConfidenceLow
	default:
		return ShellDirectorySourcePrompt, ConfidenceMedium
	}
}

func MarkShellLocationDirectoryAuthoritative(location ShellLocation, directory string) ShellLocation {
	location.Directory = strings.TrimSpace(directory)
	if location.Directory == "" {
		location.DirectorySource = ShellDirectorySourceUnknown
		location.DirectoryConfidence = ConfidenceLow
		return location
	}
	location.DirectorySource = ShellDirectorySourceProbe
	location.DirectoryConfidence = ConfidenceStrong
	return location
}

func CarryForwardShellLocationDirectory(location ShellLocation, previous ShellLocation) ShellLocation {
	if strings.TrimSpace(location.Directory) != "" || strings.TrimSpace(previous.Directory) == "" {
		return location
	}
	location.Directory = strings.TrimSpace(previous.Directory)
	location.DirectorySource = ShellDirectorySourceCarriedForward
	location.DirectoryConfidence = ConfidenceLow
	return location
}

func MarkShellLocationDirectoryApproximate(location ShellLocation) ShellLocation {
	if strings.TrimSpace(location.Directory) == "" {
		location.DirectorySource = ShellDirectorySourceUnknown
		location.DirectoryConfidence = ConfidenceLow
		return location
	}
	if location.DirectorySource == "" || location.DirectorySource == ShellDirectorySourceUnknown {
		location.DirectorySource = ShellDirectorySourcePrompt
	}
	location.DirectoryConfidence = ConfidenceLow
	return location
}

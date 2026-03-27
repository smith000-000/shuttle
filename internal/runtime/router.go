package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"aiterm/internal/config"
	"aiterm/internal/controller"
	"aiterm/internal/provider"
	"aiterm/internal/search"
)

type Router struct {
	cfg     config.Config
	profile provider.Profile
	options provider.FactoryOptions
	builtin controller.Agent

	mu            sync.Mutex
	selection     Selection
	state         WorkspaceState
	owner         controller.ConversationOwner
	activity      *activityBuffer
	shuttleSearch search.Availability
}

func NewRouter(cfg config.Config, profile provider.Profile, options provider.FactoryOptions, selection Selection, state WorkspaceState) (*Router, error) {
	builtin, err := provider.NewFromProfile(profile, options)
	if err != nil {
		return nil, err
	}
	return &Router{
		cfg:           cfg,
		profile:       profile,
		options:       options,
		builtin:       builtin,
		selection:     selection,
		state:         state,
		owner:         controller.ConversationOwnerBuiltin,
		activity:      newActivityBuffer(),
		shuttleSearch: search.ShuttleAvailability(cfg.SearchProvider),
	}, nil
}

func (r *Router) Respond(ctx context.Context, input controller.AgentInput) (controller.AgentResponse, error) {
	r.mu.Lock()
	owner := r.owner
	r.mu.Unlock()

	if owner == controller.ConversationOwnerExternal {
		agent, err := r.externalAgentLocked()
		if err != nil {
			return controller.AgentResponse{}, err
		}
		input.Session.Search = r.selection.Search
		input.Session.PreferredExternalSearch = r.selection.Search
		r.activity.Start(controller.ConversationOwnerExternal, string(r.selection.ID))
		response, err := agent.Respond(ctx, input)
		r.activity.Finish(controller.ConversationOwnerExternal)
		if err != nil {
			return controller.AgentResponse{}, err
		}
		response.Handoff = nil
		r.reloadState()
		return response, nil
	}

	builtinInput := input
	builtinInput.Session.Search = r.shuttleSearch
	builtinInput.Session.PreferredExternalSearch = r.selection.Search
	builtinInput.Session.PreferredExternalRuntime = string(r.selection.ID)
	builtinInput.Session.ExternalRuntimeAvailable = r.externalAvailableLocked()
	builtinInput.Session.ExternalResumeAvailable = r.ExternalState().Resumable

	response, err := r.builtin.Respond(ctx, builtinInput)
	if err != nil {
		return controller.AgentResponse{}, err
	}
	if response.Handoff != nil {
		if !r.externalAvailableLocked() {
			response.Handoff = nil
			return response, nil
		}
		response.Handoff.SuggestedRuntime = string(r.selection.ID)
		response.Handoff.ResumeAvailable = r.ExternalState().Resumable
		if strings.TrimSpace(response.Handoff.Title) == "" {
			response.Handoff.Title = "Hand off to coding agent"
		}
		if strings.TrimSpace(response.Handoff.Summary) == "" {
			response.Handoff.Summary = "This looks like a larger coding task that should move into the external coding agent."
		}
	}
	return response, nil
}

func (r *Router) ActivateExternal(ctx context.Context, input controller.AgentInput, handoff controller.HandoffRequest) (controller.AgentResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.prepareExternalLocked(); err != nil {
		return controller.AgentResponse{}, err
	}

	agent, err := r.externalAgentLocked()
	if err != nil {
		return controller.AgentResponse{}, err
	}

	originalPrompt := input.Prompt
	input.Session.Search = r.selection.Search
	input.Session.PreferredExternalSearch = r.selection.Search
	input.Prompt = buildExternalHandoffPrompt(handoff)
	r.owner = controller.ConversationOwnerExternal
	r.activity.Start(controller.ConversationOwnerExternal, string(r.selection.ID))

	response, err := agent.Respond(ctx, input)
	r.activity.Finish(controller.ConversationOwnerExternal)
	if err != nil {
		r.owner = controller.ConversationOwnerBuiltin
		return controller.AgentResponse{}, err
	}
	if strings.TrimSpace(response.Message) == "" && strings.TrimSpace(originalPrompt) != "" {
		response.Message = "External agent resumed control of the task."
	}
	response.Handoff = nil
	r.afterExternalTurnLocked()
	return response, nil
}

func (r *Router) SubmitExternalPrompt(ctx context.Context, input controller.AgentInput, prompt string) (controller.AgentResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return controller.AgentResponse{}, fmt.Errorf("external prompt is empty")
	}
	if err := r.prepareExternalLocked(); err != nil {
		return controller.AgentResponse{}, err
	}

	agent, err := r.externalAgentLocked()
	if err != nil {
		return controller.AgentResponse{}, err
	}

	previousOwner := r.owner
	takeover := previousOwner != controller.ConversationOwnerExternal
	input.Session.Search = r.selection.Search
	input.Session.PreferredExternalSearch = r.selection.Search
	input.Prompt = buildExternalDirectPrompt(prompt, takeover, r.state.ExternalResumable)
	r.owner = controller.ConversationOwnerExternal
	r.activity.Start(controller.ConversationOwnerExternal, string(r.selection.ID))

	response, err := agent.Respond(ctx, input)
	r.activity.Finish(controller.ConversationOwnerExternal)
	if err != nil {
		r.owner = previousOwner
		return controller.AgentResponse{}, err
	}
	if strings.TrimSpace(response.Message) == "" && takeover {
		response.Message = "External agent took over the task."
	}
	response.Handoff = nil
	r.afterExternalTurnLocked()
	return response, nil
}

func (r *Router) ResumeExternal(ctx context.Context, input controller.AgentInput) (controller.AgentResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.state.ExternalResumable {
		return controller.AgentResponse{}, fmt.Errorf("no resumable external context is available for this workspace")
	}
	if err := r.prepareExternalLocked(); err != nil {
		return controller.AgentResponse{}, err
	}
	agent, err := r.externalAgentLocked()
	if err != nil {
		return controller.AgentResponse{}, err
	}
	input.Session.Search = r.selection.Search
	input.Session.PreferredExternalSearch = r.selection.Search
	input.Prompt = buildExternalResumePrompt()
	r.owner = controller.ConversationOwnerExternal
	r.activity.Start(controller.ConversationOwnerExternal, string(r.selection.ID))
	response, err := agent.Respond(ctx, input)
	r.activity.Finish(controller.ConversationOwnerExternal)
	if err != nil {
		r.owner = controller.ConversationOwnerBuiltin
		return controller.AgentResponse{}, err
	}
	response.Handoff = nil
	r.afterExternalTurnLocked()
	return response, nil
}

func (r *Router) ReturnToBuiltin() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.owner = controller.ConversationOwnerBuiltin
	return nil
}

func (r *Router) ExternalState() controller.ExternalState {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloadStateLocked()

	state := controller.ExternalState{
		PreferredRuntime: string(r.selection.ID),
		Owner:            r.owner,
		HasHistory:       r.state.ExternalHasHistory,
		Resumable:        r.state.ExternalResumable,
		Available:        r.externalAvailableLocked(),
		Detail:           strings.TrimSpace(r.selection.Detail),
		Search:           r.selection.Search,
	}
	if !ExternalConfirmationRequired(r.state) {
		state.ConfirmationMode = "off"
	} else {
		state.ConfirmationMode = "confirm"
	}
	if state.PreferredRuntime == "" {
		state.PreferredRuntime = string(RuntimeBuiltin)
	}
	return state
}

func (r *Router) Selection() Selection {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.selection
}

func (r *Router) RuntimeActivity() controller.RuntimeActivitySnapshot {
	r.mu.Lock()
	buffer := r.activity
	r.mu.Unlock()
	if buffer == nil {
		return controller.RuntimeActivitySnapshot{}
	}
	return buffer.Snapshot()
}

func (r *Router) externalAvailableLocked() bool {
	return r.selection.ID != RuntimeBuiltin && r.selection.ProviderAllowed
}

func (r *Router) prepareExternalLocked() error {
	if !r.externalAvailableLocked() {
		return fmt.Errorf("preferred external runtime is not available")
	}
	selection := r.selection
	if selection.ID != RuntimePi || r.state.PIDirectToolsOK {
		return nil
	}
	state, err := GrantPIDirectTools(r.cfg.StateDir, r.cfg.WorkspaceID, true)
	if err != nil {
		return err
	}
	r.state = state
	selection.Granted = true
	selection.RequiresGrant = false
	r.selection = selection
	return nil
}

func (r *Router) externalAgentLocked() (controller.Agent, error) {
	r.reloadStateLocked()
	switch r.selection.ID {
	case RuntimePi:
		return NewPIAgent(r.cfg, r.profile, r.selection, r.state, r.activity)
	default:
		return nil, fmt.Errorf("unsupported external runtime %q", r.selection.ID)
	}
}

func (r *Router) afterExternalTurnLocked() {
	r.reloadStateLocked()
	resumable := strings.TrimSpace(r.state.PISessionFile) != ""
	r.state.ExternalResumable = resumable
	r.state.ExternalHasHistory = true
	r.state.ExternalRuntimeID = r.selection.ID
	r.state.ExternalWorkedAt = timeNowUTC()
	_ = SaveWorkspaceState(r.cfg.StateDir, r.cfg.WorkspaceID, r.state)
}

func (r *Router) reloadState() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloadStateLocked()
}

func (r *Router) reloadStateLocked() {
	state, ok, err := LoadWorkspaceState(r.cfg.StateDir, r.cfg.WorkspaceID)
	if err == nil && ok {
		r.state = state
	}
}

func buildExternalHandoffPrompt(handoff controller.HandoffRequest) string {
	lines := []string{
		"Shuttle is handing this task to the external coding agent.",
		"Take ownership of the current task from this turn onward.",
	}
	if summary := strings.TrimSpace(handoff.Summary); summary != "" {
		lines = append(lines, "Handoff summary: "+summary)
	}
	if reason := strings.TrimSpace(handoff.Reason); reason != "" {
		lines = append(lines, "Handoff reason: "+reason)
	}
	lines = append(lines, "Use the Shuttle transcript and current task context as the source of truth. Continue the task directly.")
	return strings.Join(lines, "\n")
}

func buildExternalResumePrompt() string {
	return "Resume the prior Shuttle external-agent work for this repository using the saved runtime context plus the current Shuttle task context. Reorient quickly, summarize only if needed, and continue the task."
}

func buildExternalDirectPrompt(prompt string, takeover bool, resumable bool) string {
	prompt = strings.TrimSpace(prompt)
	if !takeover {
		return prompt
	}

	lines := []string{
		"Shuttle is routing this turn directly to the external coding agent.",
		"Take ownership of the current task from this turn onward.",
	}
	if resumable {
		lines = append(lines, "Resume prior external work for this repository if relevant before continuing.")
	}
	lines = append(lines,
		"User request:",
		prompt,
		"Use the Shuttle transcript and current task context as the source of truth. Continue the task directly.",
	)
	return strings.Join(lines, "\n")
}

var timeNowUTC = func() time.Time {
	return time.Now().UTC()
}

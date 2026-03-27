package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"aiterm/internal/config"
	"aiterm/internal/controller"
	"aiterm/internal/provider"
	"aiterm/internal/search"
)

var lookPath = exec.LookPath

func NormalizeID(value string) ID {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "builtin":
		return RuntimeBuiltin
	case "pi", "pi-runtime":
		return RuntimePi
	case "fakepi", "fake-pi", "fake_pi", "fake-pi-runtime":
		return RuntimeFakePi
	case "auto":
		return RuntimeAuto
	default:
		return ID(strings.ToLower(strings.TrimSpace(value)))
	}
}

func NewFromConfig(cfg config.Config, options provider.FactoryOptions) (controller.Agent, provider.Profile, Selection, error) {
	profile, err := provider.ResolveProfile(cfg)
	if err != nil {
		return nil, provider.Profile{}, Selection{}, err
	}
	agent, selection, err := NewFromProfile(cfg, profile, options)
	if err != nil {
		return nil, provider.Profile{}, Selection{}, err
	}
	return agent, profile, selection, nil
}

func NewFromProfile(cfg config.Config, profile provider.Profile, options provider.FactoryOptions) (controller.Agent, Selection, error) {
	state, _, err := LoadWorkspaceState(cfg.StateDir, cfg.WorkspaceID)
	if err != nil {
		return nil, Selection{}, err
	}
	selection := selectionFromConfig(cfg, profile, state)
	agent, err := NewRouter(cfg, profile, options, selection, state)
	return agent, selection, err
}

func SaveSelection(stateDir string, workspaceID string, selection Selection) error {
	state, _, err := LoadWorkspaceState(stateDir, workspaceID)
	if err != nil {
		return err
	}
	state.RuntimeID = selection.ID
	state.RuntimeCommand = strings.TrimSpace(selection.Command)
	return SaveWorkspaceState(stateDir, workspaceID, state)
}

func GrantPIWorkspace(stateDir string, workspaceID string) error {
	state, _, err := LoadWorkspaceState(stateDir, workspaceID)
	if err != nil {
		return err
	}
	state.PIDirectToolsOK = true
	return SaveWorkspaceState(stateDir, workspaceID, state)
}

func Choices(cfg config.Config, profile provider.Profile) []Choice {
	state, _, _ := LoadWorkspaceState(cfg.StateDir, cfg.WorkspaceID)
	choices := []Choice{
		{
			Selection: Selection{ID: RuntimeBuiltin, Search: search.ShuttleAvailability(cfg.SearchProvider)},
			Label:     "builtin only",
			Detail:    "Keep Shuttle as the only agent. No external coding-agent handoff.",
		},
	}
	if summary := choices[0].Selection.Search.Summary(); summary != "" {
		choices[0].Detail += " Web search: " + summary + "."
	}

	choices = append(choices, piChoice(cfg, profile, state))
	choices = append(choices, fakePIChoice(cfg, profile))
	return choices
}

func CheckHealth(ctx context.Context, cfg config.Config, profile provider.Profile) error {
	state, _, err := LoadWorkspaceState(cfg.StateDir, cfg.WorkspaceID)
	if err != nil {
		return err
	}
	selection := selectionFromConfig(cfg, profile, state)
	switch selection.ID {
	case RuntimeBuiltin:
		return provider.CheckHealth(ctx, profile, provider.FactoryOptions{})
	case RuntimePi, RuntimeFakePi:
		selectionCopy := selection
		selectionCopy.Granted = true
		selectionCopy.RequiresGrant = false
		if selection.ID == RuntimeFakePi {
			command, err := ensureFakePIBinary(cfg)
			if err != nil {
				return err
			}
			selectionCopy.Command = command
		}
		agent, err := NewPIAgent(cfg, profile, selectionCopy, WorkspaceState{}, nil)
		if err != nil {
			return err
		}
		return agent.CheckHealth(ctx)
	default:
		return fmt.Errorf("unsupported runtime %q", selection.ID)
	}
}

func selectionFromConfig(cfg config.Config, profile provider.Profile, state WorkspaceState) Selection {
	id := NormalizeID(cfg.RuntimeType)
	if id == RuntimeAuto {
		id = RuntimeBuiltin
	}
	selection := Selection{
		ID:              id,
		Command:         defaultRuntimeCommand(cfg, id),
		RequiresGrant:   id == RuntimePi && !state.PIDirectToolsOK,
		Granted:         state.PIDirectToolsOK,
		ProviderAllowed: true,
		Search:          runtimeSearchAvailability(id, cfg.SearchProvider),
	}
	if id == RuntimePi || id == RuntimeFakePi {
		if ok, detail := piProfileSupport(profile); !ok {
			selection.ProviderAllowed = false
			selection.Detail = detail
			selection.Search = search.Availability{Mode: search.AvailabilityNone, Detail: detail}
		}
	}
	if id == RuntimePi {
		if _, err := lookPath(selection.Command); err != nil {
			selection.ProviderAllowed = false
			selection.Detail = fmt.Sprintf("pi command %q not found in PATH.", selection.Command)
		}
	}
	if id == RuntimeFakePi {
		if ok, detail := fakePIAvailable(cfg); !ok {
			selection.ProviderAllowed = false
			selection.Detail = detail
		}
	}
	return selection
}

func runtimeSearchAvailability(id ID, shuttleProvider search.Provider) search.Availability {
	switch id {
	case RuntimeBuiltin, RuntimeAuto, "":
		return search.ShuttleAvailability(shuttleProvider)
	default:
		return search.RuntimeAvailability(string(id), shuttleProvider)
	}
}

func runtimeCommand(override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	return "pi"
}

func defaultRuntimeCommand(cfg config.Config, id ID) string {
	switch id {
	case RuntimeFakePi:
		return fakePICommand(cfg)
	default:
		return runtimeCommand(cfg.RuntimeCommand)
	}
}

func choiceRuntimeCommand(cfg config.Config, id ID) string {
	if NormalizeID(cfg.RuntimeType) == id {
		return defaultRuntimeCommand(cfg, id)
	}
	switch id {
	case RuntimeFakePi:
		return fakePIBinaryPath(cfg)
	default:
		return runtimeCommand("")
	}
}

func piChoice(cfg config.Config, profile provider.Profile, state WorkspaceState) Choice {
	selection := Selection{
		ID:              RuntimePi,
		Command:         choiceRuntimeCommand(cfg, RuntimePi),
		RequiresGrant:   !state.PIDirectToolsOK,
		Granted:         state.PIDirectToolsOK,
		ProviderAllowed: true,
		Search:          runtimeSearchAvailability(RuntimePi, cfg.SearchProvider),
	}
	if _, err := lookPath(selection.Command); err != nil {
		selection.ProviderAllowed = false
		selection.Detail = fmt.Sprintf("pi command %q not found in PATH.", selection.Command)
		return Choice{
			Selection: selection,
			Label:     "pi",
			Detail:    selection.Detail,
			Disabled:  true,
		}
	}
	if ok, detail := piProfileSupport(profile); !ok {
		selection.ProviderAllowed = false
		selection.Detail = detail
		return Choice{
			Selection: selection,
			Label:     "pi",
			Detail:    detail,
			Disabled:  true,
		}
	}
	selection.Detail = "PI RPC runtime used when Shuttle hands work to the external coding agent."
	if selection.RequiresGrant {
		selection.Detail += " The first handoff grants PI direct workspace tools."
	}
	if summary := selection.Search.Summary(); summary != "" && summary != "unavailable" {
		selection.Detail += " Web search: " + summary + "."
	}
	return Choice{
		Selection: selection,
		Label:     "pi",
		Detail:    selection.Detail,
	}
}

func fakePIChoice(cfg config.Config, profile provider.Profile) Choice {
	selection := Selection{
		ID:              RuntimeFakePi,
		Command:         choiceRuntimeCommand(cfg, RuntimeFakePi),
		ProviderAllowed: true,
		Search:          runtimeSearchAvailability(RuntimeFakePi, cfg.SearchProvider),
	}
	if ok, detail := piProfileSupport(profile); !ok {
		selection.ProviderAllowed = false
		selection.Detail = detail
		return Choice{
			Selection: selection,
			Label:     "fake pi",
			Detail:    detail,
			Disabled:  true,
		}
	}
	if ok, detail := fakePIAvailable(cfg); !ok {
		selection.ProviderAllowed = false
		selection.Detail = detail
		return Choice{
			Selection: selection,
			Label:     "fake pi",
			Detail:    detail,
			Disabled:  true,
		}
	}
	selection.Detail = "Repository-local fake PI RPC runtime for testing Shuttle external-agent flows without installing PI."
	if summary := selection.Search.Summary(); summary != "" && summary != "unavailable" {
		selection.Detail += " Web search: " + summary + "."
	}
	return Choice{
		Selection: selection,
		Label:     "fake pi",
		Detail:    selection.Detail,
	}
}

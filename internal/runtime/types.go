package runtime

import (
	"time"

	"aiterm/internal/search"
)

type ID string

const (
	RuntimeBuiltin ID = "builtin"
	RuntimePi      ID = "pi"
	RuntimeFakePi  ID = "fake_pi"
	RuntimeAuto    ID = "auto"
)

type Selection struct {
	ID              ID
	Command         string
	RequiresGrant   bool
	Granted         bool
	ProviderAllowed bool
	Detail          string
	Search          search.Availability
}

type Choice struct {
	Selection Selection
	Label     string
	Detail    string
	Disabled  bool
}

type WorkspaceState struct {
	RuntimeID              ID        `json:"runtime_id"`
	RuntimeCommand         string    `json:"runtime_command,omitempty"`
	PIDirectToolsOK        bool      `json:"pi_direct_tools_ok,omitempty"`
	PISessionFile          string    `json:"pi_session_file,omitempty"`
	PISessionID            string    `json:"pi_session_id,omitempty"`
	PITaskID               string    `json:"pi_task_id,omitempty"`
	PIConfigDir            string    `json:"pi_config_dir,omitempty"`
	ProviderPreset         string    `json:"provider_preset,omitempty"`
	ProviderModel          string    `json:"provider_model,omitempty"`
	LastHealthDetail       string    `json:"last_health_detail,omitempty"`
	ExternalHasHistory     bool      `json:"external_has_history,omitempty"`
	ExternalRuntimeID      ID        `json:"external_runtime_id,omitempty"`
	ExternalWorkedAt       time.Time `json:"external_worked_at,omitempty"`
	ExternalResumable      bool      `json:"external_resumable,omitempty"`
	ConfirmExternalHandoff *bool     `json:"confirm_external_handoff,omitempty"`
}

func ExternalConfirmationRequired(state WorkspaceState) bool {
	return state.ConfirmExternalHandoff == nil || *state.ConfirmExternalHandoff
}

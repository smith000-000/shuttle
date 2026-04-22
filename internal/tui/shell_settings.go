package tui

import (
	"fmt"
	"strings"

	"aiterm/internal/shell"
)

const (
	shellSettingsYes = "yes"
	shellSettingsNo  = "no"
)

func buildShellSettingsForm(profiles shell.LaunchProfiles) onboardingFormState {
	profiles = shell.NormalizeLaunchProfiles(profiles)
	return onboardingFormState{
		title: "Shell Settings",
		intro: "Choose how Shuttle-created shells start. Persistent shell settings affect new or recovered tracked shells. Execution shell settings affect new owned command panes immediately.",
		fields: []onboardingField{
			{key: "persistent_mode", label: "Persistent Mode", value: string(profiles.Persistent.Mode), options: []string{string(shell.LaunchModeInherit), string(shell.LaunchModeManagedPrompt), string(shell.LaunchModeManagedMinimal)}},
			{key: "persistent_shell", label: "Persistent Shell", value: string(profiles.Persistent.Shell), options: []string{string(shell.ShellTypeAuto), string(shell.ShellTypeZsh), string(shell.ShellTypeBash)}},
			{key: "persistent_source_rc", label: "Persistent Source User RC", value: yesNoValue(profiles.Persistent.SourceUserRC), options: []string{shellSettingsYes, shellSettingsNo}},
			{key: "persistent_inherit_env", label: "Persistent Inherit Env", value: yesNoValue(profiles.Persistent.InheritEnv), options: []string{shellSettingsYes, shellSettingsNo}},
			{key: "execution_mode", label: "Execution Mode", value: string(profiles.Execution.Mode), options: []string{string(shell.LaunchModeInherit), string(shell.LaunchModeManagedPrompt), string(shell.LaunchModeManagedMinimal)}},
			{key: "execution_shell", label: "Execution Shell", value: string(profiles.Execution.Shell), options: []string{string(shell.ShellTypeAuto), string(shell.ShellTypeZsh), string(shell.ShellTypeBash)}},
			{key: "execution_source_rc", label: "Execution Source User RC", value: yesNoValue(profiles.Execution.SourceUserRC), options: []string{shellSettingsYes, shellSettingsNo}},
			{key: "execution_inherit_env", label: "Execution Inherit Env", value: yesNoValue(profiles.Execution.InheritEnv), options: []string{shellSettingsYes, shellSettingsNo}},
		},
	}
}

func resolveShellSettingsForm(form onboardingFormState) (shell.LaunchProfiles, error) {
	return shell.NormalizeLaunchProfiles(shell.LaunchProfiles{
		Persistent: shell.LaunchProfile{
			Mode:         shell.LaunchMode(strings.TrimSpace(formFieldValue(form, "persistent_mode"))),
			Shell:        shell.ShellType(strings.TrimSpace(formFieldValue(form, "persistent_shell"))),
			SourceUserRC: parseYesNoValue(formFieldValue(form, "persistent_source_rc")),
			InheritEnv:   parseYesNoValue(formFieldValue(form, "persistent_inherit_env")),
		},
		Execution: shell.LaunchProfile{
			Mode:         shell.LaunchMode(strings.TrimSpace(formFieldValue(form, "execution_mode"))),
			Shell:        shell.ShellType(strings.TrimSpace(formFieldValue(form, "execution_shell"))),
			SourceUserRC: parseYesNoValue(formFieldValue(form, "execution_source_rc")),
			InheritEnv:   parseYesNoValue(formFieldValue(form, "execution_inherit_env")),
		},
	}), nil
}

func yesNoValue(value bool) string {
	if value {
		return shellSettingsYes
	}
	return shellSettingsNo
}

func parseYesNoValue(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), shellSettingsYes)
}

func shellSettingsSummaryLines(profiles shell.LaunchProfiles) []string {
	profiles = shell.NormalizeLaunchProfiles(profiles)
	return []string{
		shellProfileSummaryLine("Persistent", profiles.Persistent),
		shellProfileSummaryLine("Execution", profiles.Execution),
		"Changes apply to future owned panes immediately and to the tracked shell the next time Shuttle creates or recovers that PTY.",
	}
}

func shellProfileSummaryLine(label string, profile shell.LaunchProfile) string {
	mode := strings.TrimSpace(string(profile.Mode))
	shellLabel := strings.TrimSpace(string(profile.Shell))
	if mode == string(shell.LaunchModeInherit) {
		return fmt.Sprintf("%s: inherit your normal shell startup unchanged.", label)
	}
	return fmt.Sprintf("%s: %s via %s, source user rc=%s, inherit env=%s.", label, mode, shellLabel, yesNoValue(profile.SourceUserRC), yesNoValue(profile.InheritEnv))
}

func resolveSettingsShellPreview(form onboardingFormState) shell.LaunchProfiles {
	profiles, err := resolveShellSettingsForm(form)
	if err != nil {
		return shell.DefaultLaunchProfiles()
	}
	return profiles
}

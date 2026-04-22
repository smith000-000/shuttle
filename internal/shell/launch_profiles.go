package shell

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"aiterm/internal/config"
	"aiterm/internal/securefs"
	"aiterm/internal/tmux"
)

const (
	storedLaunchProfilesVersion = 1
	defaultShellPath            = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
)

type LaunchMode string

const (
	LaunchModeInherit        LaunchMode = "inherit"
	LaunchModeManagedPrompt  LaunchMode = "managed-prompt"
	LaunchModeManagedMinimal LaunchMode = "managed-minimal"
)

type ShellType string

const (
	ShellTypeAuto ShellType = "auto"
	ShellTypeZsh  ShellType = "zsh"
	ShellTypeBash ShellType = "bash"
)

type LaunchProfile struct {
	Mode         LaunchMode
	Shell        ShellType
	SourceUserRC bool
	InheritEnv   bool
}

type LaunchProfiles struct {
	Persistent LaunchProfile
	Execution  LaunchProfile
}

type persistedLaunchProfiles struct {
	Version    int                    `json:"version"`
	Persistent persistedLaunchProfile `json:"persistent"`
	Execution  persistedLaunchProfile `json:"execution"`
}

type persistedLaunchProfile struct {
	Mode         string `json:"mode"`
	Shell        string `json:"shell"`
	SourceUserRC bool   `json:"source_user_rc"`
	InheritEnv   bool   `json:"inherit_env"`
}

var shellLookPath = exec.LookPath

func DefaultPersistentLaunchProfile() LaunchProfile {
	return LaunchProfile{
		Mode:         LaunchModeManagedPrompt,
		Shell:        ShellTypeAuto,
		SourceUserRC: true,
		InheritEnv:   true,
	}
}

func DefaultExecutionLaunchProfile() LaunchProfile {
	return LaunchProfile{
		Mode:         LaunchModeManagedMinimal,
		Shell:        ShellTypeAuto,
		SourceUserRC: false,
		InheritEnv:   true,
	}
}

func DefaultLaunchProfiles() LaunchProfiles {
	return LaunchProfiles{
		Persistent: DefaultPersistentLaunchProfile(),
		Execution:  DefaultExecutionLaunchProfile(),
	}
}

func ConfigLaunchProfiles(cfg config.Config) LaunchProfiles {
	return NormalizeLaunchProfiles(LaunchProfiles{
		Persistent: launchProfileFromConfig(
			cfg.PersistentShellMode,
			cfg.PersistentShellType,
			cfg.PersistentShellSourceRC,
			cfg.PersistentShellInheritEnv,
			DefaultPersistentLaunchProfile(),
		),
		Execution: launchProfileFromConfig(
			cfg.ExecutionShellMode,
			cfg.ExecutionShellType,
			cfg.ExecutionShellSourceRC,
			cfg.ExecutionShellInheritEnv,
			DefaultExecutionLaunchProfile(),
		),
	})
}

func ApplyStoredLaunchProfiles(cfg config.Config) (config.Config, error) {
	stored, ok, err := LoadStoredLaunchProfiles(cfg.StateDir)
	if err != nil {
		return cfg, err
	}
	if ok {
		cfg = ApplyLaunchProfilesToConfig(cfg, stored)
	}
	return ApplyLaunchProfilesToConfig(cfg, ConfigLaunchProfiles(cfg)), nil
}

func ApplyLaunchProfilesToConfig(cfg config.Config, profiles LaunchProfiles) config.Config {
	profiles = NormalizeLaunchProfiles(profiles)
	cfg.PersistentShellMode = string(profiles.Persistent.Mode)
	cfg.PersistentShellType = string(profiles.Persistent.Shell)
	cfg.PersistentShellSourceRC = profiles.Persistent.SourceUserRC
	cfg.PersistentShellInheritEnv = profiles.Persistent.InheritEnv
	cfg.ExecutionShellMode = string(profiles.Execution.Mode)
	cfg.ExecutionShellType = string(profiles.Execution.Shell)
	cfg.ExecutionShellSourceRC = profiles.Execution.SourceUserRC
	cfg.ExecutionShellInheritEnv = profiles.Execution.InheritEnv
	return cfg
}

func NormalizeLaunchProfiles(profiles LaunchProfiles) LaunchProfiles {
	profiles.Persistent = normalizeLaunchProfile(profiles.Persistent, DefaultPersistentLaunchProfile())
	profiles.Execution = normalizeLaunchProfile(profiles.Execution, DefaultExecutionLaunchProfile())
	return profiles
}

func SaveStoredLaunchProfiles(stateDir string, profiles LaunchProfiles) error {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return errors.New("state dir must not be empty")
	}
	if err := securefs.EnsurePrivateDir(stateDir); err != nil {
		return fmt.Errorf("create shell settings state dir: %w", err)
	}
	profiles = NormalizeLaunchProfiles(profiles)
	stored := persistedLaunchProfiles{
		Version: storedLaunchProfilesVersion,
		Persistent: persistedLaunchProfile{
			Mode:         string(profiles.Persistent.Mode),
			Shell:        string(profiles.Persistent.Shell),
			SourceUserRC: profiles.Persistent.SourceUserRC,
			InheritEnv:   profiles.Persistent.InheritEnv,
		},
		Execution: persistedLaunchProfile{
			Mode:         string(profiles.Execution.Mode),
			Shell:        string(profiles.Execution.Shell),
			SourceUserRC: profiles.Execution.SourceUserRC,
			InheritEnv:   profiles.Execution.InheritEnv,
		},
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal shell settings: %w", err)
	}
	if err := securefs.WriteAtomicPrivate(shellProfilesPath(stateDir), data, 0o600); err != nil {
		return fmt.Errorf("write shell settings: %w", err)
	}
	return nil
}

func LoadStoredLaunchProfiles(stateDir string) (LaunchProfiles, bool, error) {
	path := shellProfilesPath(stateDir)
	data, _, err := securefs.ReadFileNoFollow(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return LaunchProfiles{}, false, nil
		}
		return LaunchProfiles{}, false, fmt.Errorf("read shell settings: %w", err)
	}
	var stored persistedLaunchProfiles
	if err := json.Unmarshal(data, &stored); err != nil {
		return LaunchProfiles{}, false, fmt.Errorf("decode shell settings: %w", err)
	}
	return NormalizeLaunchProfiles(LaunchProfiles{
		Persistent: LaunchProfile{
			Mode:         normalizeLaunchMode(stored.Persistent.Mode),
			Shell:        normalizeShellType(stored.Persistent.Shell),
			SourceUserRC: stored.Persistent.SourceUserRC,
			InheritEnv:   stored.Persistent.InheritEnv,
		},
		Execution: LaunchProfile{
			Mode:         normalizeLaunchMode(stored.Execution.Mode),
			Shell:        normalizeShellType(stored.Execution.Shell),
			SourceUserRC: stored.Execution.SourceUserRC,
			InheritEnv:   stored.Execution.InheritEnv,
		},
	}), true, nil
}

func PersistentLaunchSpec(runtimeDir string, profiles LaunchProfiles) (tmux.LaunchSpec, error) {
	return launchSpecForProfile(runtimeDir, "persistent", NormalizeLaunchProfiles(profiles).Persistent)
}

func ExecutionLaunchSpec(runtimeDir string, profiles LaunchProfiles) (tmux.LaunchSpec, error) {
	return launchSpecForProfile(runtimeDir, "execution", NormalizeLaunchProfiles(profiles).Execution)
}

func shellProfilesPath(stateDir string) string {
	return filepath.Join(strings.TrimSpace(stateDir), "shell_profiles.json")
}

func launchProfileFromConfig(modeRaw string, shellRaw string, sourceUserRC bool, inheritEnv bool, defaults LaunchProfile) LaunchProfile {
	profile := defaults
	configured := strings.TrimSpace(modeRaw) != "" || strings.TrimSpace(shellRaw) != ""
	if strings.TrimSpace(modeRaw) != "" {
		mode := normalizeLaunchMode(modeRaw)
		profile.Mode = mode
	}
	if strings.TrimSpace(shellRaw) != "" {
		shellType := normalizeShellType(shellRaw)
		profile.Shell = shellType
	}
	if configured {
		profile.SourceUserRC = sourceUserRC
		profile.InheritEnv = inheritEnv
	}
	return profile
}

func normalizeLaunchProfile(profile LaunchProfile, defaults LaunchProfile) LaunchProfile {
	profile.Mode = firstNonEmptyLaunchMode(normalizeLaunchMode(string(profile.Mode)), defaults.Mode)
	profile.Shell = firstNonEmptyShellType(normalizeShellType(string(profile.Shell)), defaults.Shell)
	return profile
}

func normalizeLaunchMode(value string) LaunchMode {
	switch strings.TrimSpace(strings.ToLower(strings.ReplaceAll(value, "_", "-"))) {
	case "", string(LaunchModeInherit):
		return LaunchModeInherit
	case string(LaunchModeManagedPrompt):
		return LaunchModeManagedPrompt
	case string(LaunchModeManagedMinimal):
		return LaunchModeManagedMinimal
	default:
		return LaunchModeInherit
	}
}

func normalizeShellType(value string) ShellType {
	switch strings.TrimSpace(strings.ToLower(strings.ReplaceAll(value, "_", "-"))) {
	case "", string(ShellTypeAuto):
		return ShellTypeAuto
	case string(ShellTypeZsh):
		return ShellTypeZsh
	case string(ShellTypeBash):
		return ShellTypeBash
	default:
		return ShellTypeAuto
	}
}

func firstNonEmptyLaunchMode(value LaunchMode, fallback LaunchMode) LaunchMode {
	if value == "" {
		return fallback
	}
	return value
}

func firstNonEmptyShellType(value ShellType, fallback ShellType) ShellType {
	if value == "" {
		return fallback
	}
	return value
}

func launchSpecForProfile(runtimeDir string, scope string, profile LaunchProfile) (tmux.LaunchSpec, error) {
	profile = normalizeLaunchProfile(profile, DefaultPersistentLaunchProfile())
	if profile.Mode == LaunchModeInherit {
		return tmux.LaunchSpec{}, nil
	}
	if strings.TrimSpace(runtimeDir) == "" {
		return tmux.LaunchSpec{}, nil
	}
	resolvedShell, shellPath, err := resolveManagedShellBinary(profile.Shell)
	if err != nil {
		return tmux.LaunchSpec{}, err
	}
	switch resolvedShell {
	case ShellTypeZsh:
		return zshLaunchSpec(runtimeDir, scope, shellPath, profile)
	case ShellTypeBash:
		return bashLaunchSpec(runtimeDir, scope, shellPath, profile)
	default:
		return tmux.LaunchSpec{}, fmt.Errorf("unsupported managed shell %q", resolvedShell)
	}
}

func resolveManagedShellBinary(shellType ShellType) (ShellType, string, error) {
	shellType = normalizeShellType(string(shellType))
	type candidate struct {
		shell ShellType
		path  string
	}
	candidates := make([]candidate, 0, 4)
	addCandidate := func(shell ShellType, path string) {
		if shell == "" {
			return
		}
		for _, existing := range candidates {
			if existing.shell == shell && existing.path == path {
				return
			}
		}
		candidates = append(candidates, candidate{shell: shell, path: path})
	}

	if shellType == ShellTypeAuto {
		if envShell := strings.TrimSpace(os.Getenv("SHELL")); envShell != "" {
			envType := normalizeShellType(filepath.Base(envShell))
			if envType == ShellTypeZsh || envType == ShellTypeBash {
				addCandidate(envType, envShell)
				addCandidate(envType, "")
			}
		}
		addCandidate(ShellTypeZsh, "")
		addCandidate(ShellTypeBash, "")
	} else {
		addCandidate(shellType, "")
	}

	for _, candidate := range candidates {
		if candidate.path != "" {
			if info, err := os.Stat(candidate.path); err == nil && !info.IsDir() {
				return candidate.shell, candidate.path, nil
			}
		}
		path, err := shellLookPath(string(candidate.shell))
		if err == nil && strings.TrimSpace(path) != "" {
			return candidate.shell, path, nil
		}
	}

	if shellType == ShellTypeAuto {
		return "", "", errors.New("managed shell profiles require zsh or bash in PATH")
	}
	return "", "", fmt.Errorf("configured shell %q is not available", shellType)
}

func zshLaunchSpec(runtimeDir string, scope string, shellPath string, profile LaunchProfile) (tmux.LaunchSpec, error) {
	launchDir := filepath.Join(strings.TrimSpace(runtimeDir), "shell-launch", sanitizeLaunchScope(scope)+"-zsh")
	if err := securefs.EnsurePrivateDir(launchDir); err != nil {
		return tmux.LaunchSpec{}, fmt.Errorf("prepare zsh launch dir: %w", err)
	}
	if err := securefs.EnsurePrivateDir(filepath.Join(strings.TrimSpace(runtimeDir), "shell-state")); err != nil {
		return tmux.LaunchSpec{}, fmt.Errorf("prepare zsh shell state dir: %w", err)
	}
	if err := securefs.WriteAtomicPrivate(filepath.Join(launchDir, ".zshenv"), []byte(zshenvScript()), 0o600); err != nil {
		return tmux.LaunchSpec{}, fmt.Errorf("write zshenv: %w", err)
	}
	if err := securefs.WriteAtomicPrivate(filepath.Join(launchDir, ".zshrc"), []byte(zshrcScript(profile.SourceUserRC)), 0o600); err != nil {
		return tmux.LaunchSpec{}, fmt.Errorf("write zshrc: %w", err)
	}

	command := "exec " + shellQuote(shellPath) + " -i"
	if profile.InheritEnv {
		return tmux.LaunchSpec{
			Command: command,
			Env: map[string]string{
				"ZDOTDIR":               launchDir,
				"SHELL":                 shellPath,
				"SHUTTLE_MANAGED_SHELL": "1",
				"SHUTTLE_RUNTIME_DIR":   runtimeDir,
			},
		}, nil
	}
	env := minimalShellEnv(shellPath, runtimeDir)
	env["ZDOTDIR"] = launchDir
	command = "exec env -i " + formatEnvAssignments(env) + " " + shellQuote(shellPath) + " -i"
	return tmux.LaunchSpec{Command: command}, nil
}

func bashLaunchSpec(runtimeDir string, scope string, shellPath string, profile LaunchProfile) (tmux.LaunchSpec, error) {
	launchDir := filepath.Join(strings.TrimSpace(runtimeDir), "shell-launch")
	if err := securefs.EnsurePrivateDir(launchDir); err != nil {
		return tmux.LaunchSpec{}, fmt.Errorf("prepare bash launch dir: %w", err)
	}
	if err := securefs.EnsurePrivateDir(filepath.Join(strings.TrimSpace(runtimeDir), "shell-state")); err != nil {
		return tmux.LaunchSpec{}, fmt.Errorf("prepare bash shell state dir: %w", err)
	}
	rcPath := filepath.Join(launchDir, sanitizeLaunchScope(scope)+"-bashrc")
	if err := securefs.WriteAtomicPrivate(rcPath, []byte(bashrcScript(profile.SourceUserRC)), 0o600); err != nil {
		return tmux.LaunchSpec{}, fmt.Errorf("write bashrc: %w", err)
	}

	command := "exec " + shellQuote(shellPath) + " --rcfile " + shellQuote(rcPath) + " -i"
	if profile.InheritEnv {
		return tmux.LaunchSpec{
			Command: command,
			Env: map[string]string{
				"SHELL":                 shellPath,
				"SHUTTLE_MANAGED_SHELL": "1",
				"SHUTTLE_RUNTIME_DIR":   runtimeDir,
			},
		}, nil
	}
	env := minimalShellEnv(shellPath, runtimeDir)
	command = "exec env -i " + formatEnvAssignments(env) + " " + shellQuote(shellPath) + " --rcfile " + shellQuote(rcPath) + " -i"
	return tmux.LaunchSpec{Command: command}, nil
}

func minimalShellEnv(shellPath string, runtimeDir string) map[string]string {
	env := map[string]string{
		"PATH":                  defaultShellPath,
		"SHELL":                 shellPath,
		"SHUTTLE_MANAGED_SHELL": "1",
		"SHUTTLE_RUNTIME_DIR":   runtimeDir,
	}
	if pathValue := strings.TrimSpace(os.Getenv("PATH")); pathValue != "" {
		env["PATH"] = pathValue
	}
	for _, key := range []string{"HOME", "TERM", "LANG", "LC_ALL", "LC_CTYPE", "TMPDIR", "USER", "LOGNAME"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			env[key] = value
		}
	}
	if env["USER"] == "" && env["LOGNAME"] != "" {
		env["USER"] = env["LOGNAME"]
	}
	if env["LOGNAME"] == "" && env["USER"] != "" {
		env["LOGNAME"] = env["USER"]
	}
	return env
}

func formatEnvAssignments(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+shellQuote(env[key]))
	}
	return strings.Join(parts, " ")
}

func sanitizeLaunchScope(scope string) string {
	scope = strings.TrimSpace(strings.ToLower(scope))
	if scope == "" {
		return "default"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-", "_", "-")
	scope = replacer.Replace(scope)
	return strings.Trim(scope, "-")
}

func zshenvScript() string {
	return strings.Join([]string{
		"export SHUTTLE_MANAGED_SHELL=1",
		"",
	}, "\n")
}

func zshrcScript(sourceUserRC bool) string {
	lines := []string{
		"unsetopt TRANSIENT_RPROMPT 2>/dev/null || true",
		"autoload -Uz add-zsh-hook",
	}
	if sourceUserRC {
		lines = append(lines, "[[ -f \"$HOME/.zshrc\" ]] && source \"$HOME/.zshrc\"")
	}
	lines = append(lines,
		"if [[ -n \"${SHUTTLE_HISTFILE:-}\" ]]; then",
		"  HISTFILE=\"$SHUTTLE_HISTFILE\"",
		"  HISTSIZE=\"${HISTSIZE:-5000}\"",
		"  SAVEHIST=\"${SAVEHIST:-5000}\"",
		"fi",
	)
	lines = append(lines, zshManagedSemanticInitLines()...)
	lines = append(lines,
		"function __shuttle_reset_prompt() {",
		"  PROMPT='%n@%m %~ %# '",
		"  RPROMPT=''",
		"  RPS1=''",
		"}",
		"typeset -ga precmd_functions",
		"precmd_functions=(${precmd_functions:#__shuttle_reset_prompt})",
		"add-zsh-hook precmd __shuttle_reset_prompt",
		"__shuttle_reset_prompt",
		"",
	)
	return strings.Join(lines, "\n")
}

func bashrcScript(sourceUserRC bool) string {
	lines := []string{}
	if sourceUserRC {
		lines = append(lines, "[ -f \"$HOME/.bashrc\" ] && . \"$HOME/.bashrc\"")
	}
	lines = append(lines,
		"if [ -n \"${SHUTTLE_HISTFILE:-}\" ]; then",
		"  HISTFILE=\"$SHUTTLE_HISTFILE\"",
		"  HISTSIZE=\"${HISTSIZE:-5000}\"",
		"  HISTFILESIZE=\"${HISTFILESIZE:-5000}\"",
		"fi",
	)
	lines = append(lines, bashManagedSemanticInitLines()...)
	lines = append(lines,
		"__shuttle_reset_prompt() {",
		"  PS1='\\u@\\h \\w \\$ '",
		"}",
		"if [ -n \"${PROMPT_COMMAND:-}\" ]; then",
		"  PROMPT_COMMAND=\"__shuttle_semantic_precmd;__shuttle_reset_prompt;${PROMPT_COMMAND}\"",
		"else",
		"  PROMPT_COMMAND=\"__shuttle_semantic_precmd;__shuttle_reset_prompt\"",
		"fi",
		"__shuttle_reset_prompt",
		"__shuttle_semantic_precmd",
		"",
	)
	return strings.Join(lines, "\n")
}

func zshManagedSemanticInitLines() []string {
	return []string{
		"__shuttle_semantic_state_path() {",
		"  local tty_path=\"${TTY:-}\"",
		"  if [[ -z \"$tty_path\" || \"$tty_path\" == \"not a tty\" ]]; then",
		"    tty_path=\"$(tty 2>/dev/null || true)\"",
		"  fi",
		"  tty_path=\"${tty_path//\\//_}\"",
		"  tty_path=\"${tty_path//\\\\/_}\"",
		"  tty_path=\"${tty_path//:/_}\"",
		"  tty_path=\"${tty_path// /_}\"",
		"  while [[ \"$tty_path\" == _* ]]; do",
		"    tty_path=\"${tty_path#_}\"",
		"  done",
		"  while [[ \"$tty_path\" == *_ ]]; do",
		"    tty_path=\"${tty_path%_}\"",
		"  done",
		"  if [[ -z \"$tty_path\" ]]; then",
		"    tty_path=\"unknown\"",
		"  fi",
		"  print -r -- \"${SHUTTLE_RUNTIME_DIR}/shell-state/${tty_path}.state\"",
		"}",
		"if [[ -n \"${SHUTTLE_RUNTIME_DIR:-}\" ]]; then",
		"  mkdir -p -- \"${SHUTTLE_RUNTIME_DIR}/shell-state\"",
		"  export SHUTTLE_SEMANTIC_SHELL_STATE_FILE=\"$(__shuttle_semantic_state_path)\"",
		"fi",
		"export SHUTTLE_SEMANTIC_SHELL_V1=1",
		"export SHUTTLE_SEMANTIC_SHELL_V1_PID=$$",
		"__shuttle_semantic_emit_osc133() { printf '\\033]133;%s\\033\\\\' \"$1\"; }",
		"__shuttle_semantic_emit_osc7() { printf '\\033]7;file://%s%s\\033\\\\' \"${HOST:-localhost}\" \"$PWD\"; }",
		"__shuttle_semantic_write_state() {",
		"  [[ -n \"${SHUTTLE_SEMANTIC_SHELL_STATE_FILE:-}\" ]] || return",
		"  local event=\"$1\"",
		"  local exit_code=\"$2\"",
		"  local cwd_json=\"$PWD\"",
		"  local exit_json=\"null\"",
		"  cwd_json=${cwd_json//\\\\/\\\\\\\\}",
		"  cwd_json=${cwd_json//\\\"/\\\\\\\"}",
		"  cwd_json=${cwd_json//$'\\n'/\\\\n}",
		"  cwd_json=${cwd_json//$'\\r'/\\\\r}",
		"  cwd_json=${cwd_json//$'\\t'/\\\\t}",
		"  if [[ -n \"$exit_code\" ]]; then",
		"    exit_json=\"$exit_code\"",
		"  fi",
		"  printf '{\"version\":1,\"event\":\"%s\",\"exit\":%s,\"cwd\":\"%s\",\"shell\":\"zsh\"}\\n' \"$event\" \"$exit_json\" \"$cwd_json\" >| \"$SHUTTLE_SEMANTIC_SHELL_STATE_FILE\"",
		"}",
		"__shuttle_semantic_preexec() {",
		"  __shuttle_semantic_emit_osc133 \"B\"",
		"  __shuttle_semantic_emit_osc133 \"C\"",
		"  __shuttle_semantic_write_state \"command\" \"\"",
		"}",
		"__shuttle_semantic_precmd() {",
		"  local exit_code=\"$?\"",
		"  __shuttle_semantic_emit_osc133 \"D;$exit_code\"",
		"  __shuttle_semantic_emit_osc7",
		"  __shuttle_semantic_emit_osc133 \"A\"",
		"  __shuttle_semantic_write_state \"prompt\" \"$exit_code\"",
		"}",
		"typeset -ga preexec_functions",
		"preexec_functions=(${preexec_functions:#__shuttle_semantic_preexec})",
		"precmd_functions=(${precmd_functions:#__shuttle_semantic_precmd})",
		"add-zsh-hook preexec __shuttle_semantic_preexec",
		"add-zsh-hook precmd __shuttle_semantic_precmd",
	}
}

func bashManagedSemanticInitLines() []string {
	return []string{
		"__shuttle_semantic_state_path() {",
		"  local tty_path=\"${TTY:-}\"",
		"  if [ -z \"$tty_path\" ] || [ \"$tty_path\" = \"not a tty\" ]; then",
		"    tty_path=\"$(tty 2>/dev/null || true)\"",
		"  fi",
		"  tty_path=\"${tty_path//\\//_}\"",
		"  tty_path=\"${tty_path//\\\\/_}\"",
		"  tty_path=\"${tty_path//:/_}\"",
		"  tty_path=\"${tty_path// /_}\"",
		"  while [[ \"$tty_path\" == _* ]]; do",
		"    tty_path=\"${tty_path#_}\"",
		"  done",
		"  while [[ \"$tty_path\" == *_ ]]; do",
		"    tty_path=\"${tty_path%_}\"",
		"  done",
		"  if [ -z \"$tty_path\" ]; then",
		"    tty_path=\"unknown\"",
		"  fi",
		"  printf '%s/shell-state/%s.state' \"$SHUTTLE_RUNTIME_DIR\" \"$tty_path\"",
		"}",
		"if [ -n \"${SHUTTLE_RUNTIME_DIR:-}\" ]; then",
		"  mkdir -p -- \"${SHUTTLE_RUNTIME_DIR}/shell-state\"",
		"  export SHUTTLE_SEMANTIC_SHELL_STATE_FILE=\"$(__shuttle_semantic_state_path)\"",
		"fi",
		"export SHUTTLE_SEMANTIC_SHELL_V1=1",
		"export SHUTTLE_SEMANTIC_SHELL_V1_PID=$$",
		"__shuttle_semantic_emit_osc133() { printf '\\033]133;%s\\033\\\\' \"$1\"; }",
		"__shuttle_semantic_emit_osc7() { printf '\\033]7;file://%s%s\\033\\\\' \"${HOSTNAME:-localhost}\" \"$PWD\"; }",
		"__shuttle_semantic_write_state() {",
		"  [ -n \"${SHUTTLE_SEMANTIC_SHELL_STATE_FILE:-}\" ] || return",
		"  local event=\"$1\"",
		"  local exit_code=\"$2\"",
		"  local cwd_json=\"$PWD\"",
		"  local exit_json=\"null\"",
		"  cwd_json=${cwd_json//\\\\/\\\\\\\\}",
		"  cwd_json=${cwd_json//\\\"/\\\\\\\"}",
		"  cwd_json=${cwd_json//$'\\n'/\\\\n}",
		"  cwd_json=${cwd_json//$'\\r'/\\\\r}",
		"  cwd_json=${cwd_json//$'\\t'/\\\\t}",
		"  if [ -n \"$exit_code\" ]; then",
		"    exit_json=\"$exit_code\"",
		"  fi",
		"  printf '{\"version\":1,\"event\":\"%s\",\"exit\":%s,\"cwd\":\"%s\",\"shell\":\"bash\"}\\n' \"$event\" \"$exit_json\" \"$cwd_json\" >| \"$SHUTTLE_SEMANTIC_SHELL_STATE_FILE\"",
		"}",
		"__shuttle_semantic_preexec() {",
		"  [ \"${__shuttle_semantic_in_precmd:-0}\" = \"1\" ] && return",
		"  [ \"${__shuttle_semantic_started:-0}\" = \"1\" ] && return",
		"  case \"$BASH_COMMAND\" in",
		"    __shuttle_semantic_* ) return ;;",
		"  esac",
		"  __shuttle_semantic_started=1",
		"  __shuttle_semantic_emit_osc133 \"B\"",
		"  __shuttle_semantic_emit_osc133 \"C\"",
		"  __shuttle_semantic_write_state \"command\" \"\"",
		"}",
		"__shuttle_semantic_precmd() {",
		"  local exit_code=\"$?\"",
		"  __shuttle_semantic_in_precmd=1",
		"  __shuttle_semantic_started=0",
		"  __shuttle_semantic_emit_osc133 \"D;$exit_code\"",
		"  __shuttle_semantic_emit_osc7",
		"  __shuttle_semantic_emit_osc133 \"A\"",
		"  __shuttle_semantic_write_state \"prompt\" \"$exit_code\"",
		"  __shuttle_semantic_in_precmd=0",
		"}",
		"trap '__shuttle_semantic_preexec' DEBUG",
	}
}

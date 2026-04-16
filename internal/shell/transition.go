package shell

import "strings"

type shellTransitionKind string

const (
	shellTransitionNone    shellTransitionKind = ""
	shellTransitionLocal   shellTransitionKind = "local_nested_shell"
	shellTransitionRemote  shellTransitionKind = "remote_shell"
	shellTransitionExec    shellTransitionKind = "container_shell"
	shellTransitionUnknown shellTransitionKind = "unknown_interactive_transition"
)

type shellTransition struct {
	Kind shellTransitionKind
}

func detectShellTransition(submittedCommand string, currentPaneCommand string, promptContext PromptContext, remembered shellTransitionKind) shellTransition {
	if promptContext.Remote {
		return shellTransition{Kind: shellTransitionRemote}
	}

	if kind := detectCommandTransition(submittedCommand); kind != shellTransitionNone {
		return shellTransition{Kind: kind}
	}

	if kind := detectForegroundTransition(currentPaneCommand); kind != shellTransitionNone {
		return shellTransition{Kind: kind}
	}

	if remembered != shellTransitionNone {
		return shellTransition{Kind: remembered}
	}

	return shellTransition{}
}

func settledShellTransition(submittedCommand string, currentPaneCommand string, promptContext PromptContext, remembered shellTransitionKind) shellTransitionKind {
	transition := detectShellTransition(submittedCommand, currentPaneCommand, promptContext, remembered)
	if transition.Kind != shellTransitionUnknown {
		return transition.Kind
	}

	fields := transitionCommandFields(submittedCommand)
	if len(fields) > 0 {
		switch fields[0] {
		case "exit", "logout":
			if promptContext.Remote {
				return shellTransitionRemote
			}
			if paneCommandIsShell(currentPaneCommand) {
				return shellTransitionNone
			}
		}
	}

	return transition.Kind
}

func detectCommandTransition(command string) shellTransitionKind {
	fields := transitionCommandFields(command)
	if len(fields) == 0 {
		return shellTransitionNone
	}

	commandName := fields[0]
	args := fields[1:]

	switch commandName {
	case "ssh", "slogin", "telnet", "mosh", "mosh-client":
		return shellTransitionRemote
	case "docker", "podman":
		if len(args) > 0 && args[0] == "exec" && hasAnyArg(args[1:], "-it", "-ti", "-i", "-t") {
			return shellTransitionExec
		}
	case "kubectl":
		if len(args) > 0 && args[0] == "exec" && hasAnyArg(args[1:], "-it", "-ti", "-i", "-t") {
			return shellTransitionExec
		}
	case "machinectl":
		if len(args) > 0 && args[0] == "shell" {
			return shellTransitionExec
		}
	case "nsenter":
		return shellTransitionExec
	case "su":
		return shellTransitionLocal
	case "sudo", "doas":
		if hasAnyArg(args, "-i", "-s") {
			return shellTransitionLocal
		}
		if inner := detectCommandTransition(strings.Join(stripLeadingOptions(args), " ")); inner != shellTransitionNone {
			return inner
		}
	case "exit", "logout":
		return shellTransitionUnknown
	default:
		if paneCommandIsShell(commandName) && shellArgsSuggestInteractiveTransition(args) {
			return shellTransitionLocal
		}
	}

	return shellTransitionNone
}

func detectForegroundTransition(currentPaneCommand string) shellTransitionKind {
	switch strings.TrimSpace(strings.ToLower(currentPaneCommand)) {
	case "ssh", "slogin", "telnet", "mosh", "mosh-client":
		return shellTransitionRemote
	case "docker", "podman", "kubectl", "machinectl", "nsenter":
		return shellTransitionExec
	default:
		return shellTransitionNone
	}
}

func transitionCommandFields(command string) []string {
	fields := strings.Fields(strings.TrimSpace(command))
	for len(fields) > 0 {
		token := strings.TrimSpace(fields[0])
		switch {
		case token == "":
			fields = fields[1:]
		case isInlineEnvAssignment(token):
			fields = fields[1:]
		case token == "command" || token == "builtin" || token == "exec" || token == "nohup":
			fields = fields[1:]
		case token == "env":
			fields = stripEnvCommandPrefix(fields[1:])
		default:
			return fields
		}
	}
	return nil
}

func stripEnvCommandPrefix(fields []string) []string {
	for len(fields) > 0 {
		token := strings.TrimSpace(fields[0])
		if token == "" {
			fields = fields[1:]
			continue
		}
		if strings.HasPrefix(token, "-") {
			fields = fields[1:]
			continue
		}
		if isInlineEnvAssignment(token) {
			fields = fields[1:]
			continue
		}
		break
	}
	return fields
}

func isInlineEnvAssignment(token string) bool {
	if strings.HasPrefix(token, "-") {
		return false
	}
	eq := strings.IndexByte(token, '=')
	return eq > 0
}

func stripLeadingOptions(args []string) []string {
	index := 0
	for index < len(args) {
		arg := strings.TrimSpace(args[index])
		if arg == "" {
			index++
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			break
		}
		index++
	}
	return args[index:]
}

func shellArgsSuggestInteractiveTransition(args []string) bool {
	if len(args) == 0 {
		return true
	}
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			return false
		}
		flags := strings.TrimLeft(arg, "-")
		if strings.Contains(flags, "c") {
			return false
		}
	}
	return true
}

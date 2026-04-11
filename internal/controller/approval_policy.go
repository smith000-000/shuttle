package controller

import (
	"context"
	"fmt"
	"strings"
)

func normalizeApprovalMode(mode ApprovalMode) ApprovalMode {
	switch ApprovalMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case ApprovalModeDanger:
		return ApprovalModeDanger
	case ApprovalModeAuto:
		return ApprovalModeAuto
	case ApprovalModeConfirm, "":
		return ApprovalModeConfirm
	default:
		return ApprovalModeConfirm
	}
}

func isValidApprovalMode(mode ApprovalMode) bool {
	switch ApprovalMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case ApprovalModeConfirm, ApprovalModeAuto, ApprovalModeDanger:
		return true
	default:
		return false
	}
}

func (c *LocalController) ApprovalMode() ApprovalMode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session.ApprovalMode
}

func (c *LocalController) SetApprovalMode(_ context.Context, mode ApprovalMode) ([]TranscriptEvent, error) {
	if !isValidApprovalMode(mode) {
		event := c.newEvent(EventError, TextPayload{Text: fmt.Sprintf("unknown approvals mode %q; try /approvals confirm, /approvals auto, or /approvals dangerous", strings.TrimSpace(string(mode)))})
		c.mu.Lock()
		c.appendEvents(event)
		c.mu.Unlock()
		return []TranscriptEvent{event}, nil
	}

	normalized := normalizeApprovalMode(mode)
	event := c.newEvent(EventSystemNotice, TextPayload{Text: ApprovalModeDescription(normalized)})

	c.mu.Lock()
	c.session.ApprovalMode = normalized
	c.appendEvents(event)
	c.mu.Unlock()
	return []TranscriptEvent{event}, nil
}

func ApprovalModeDescription(mode ApprovalMode) string {
	switch normalizeApprovalMode(mode) {
	case ApprovalModeDanger:
		return "Approvals set to dangerous for this session. Shuttle will auto-run agent commands and auto-apply agent patches without confirmation. Use this only in a trusted workspace."
	case ApprovalModeAuto:
		return "Approvals set to auto for this session. Shuttle will auto-run safe local inspection and test commands, and still require explicit approval for writes, patches, remote commands, network or process-control commands, and other risky actions."
	default:
		return "Approvals set to confirm for this session. Shuttle will keep low-risk agent commands as explicit proposals and still require approval for risky actions."
	}
}

func ApprovalModeStatusBody(mode ApprovalMode) string {
	mode = normalizeApprovalMode(mode)
	switch mode {
	case ApprovalModeDanger:
		return "Approvals: dangerous. Agent commands and patches can run without confirmation in this session."
	case ApprovalModeAuto:
		return "Approvals: auto. Safe local inspection and test commands can auto-run; risky actions still require approval."
	default:
		return "Approvals: confirm. Safe commands stay as proposals; risky actions still require approval."
	}
}

func ApprovalModeStatusLabel(mode ApprovalMode) string {
	return "APR " + string(normalizeApprovalMode(mode))
}

func autoRunNotice(command string) string {
	return fmt.Sprintf("Auto-running safe local command under /approvals auto: %s", strings.TrimSpace(command))
}

func DangerousModeWarning() string {
	return "Dangerous mode disables Shuttle's normal approval checks for agent-run commands and patches. Only enable it in a trusted workspace. Continue?"
}

type automaticAction struct {
	command     string
	patch       string
	patchTarget PatchTarget
	commandRisk bool
	patchRisk   bool
}

func automaticActionFromResponse(session SessionContext, response AgentResponse) automaticAction {
	mode := normalizeApprovalMode(session.ApprovalMode)
	if mode == ApprovalModeConfirm {
		return automaticAction{}
	}

	if response.Approval != nil {
		command := strings.TrimSpace(response.Approval.Command)
		patch := strings.TrimSpace(response.Approval.Patch)
		if mode == ApprovalModeDanger {
			switch response.Approval.Kind {
			case ApprovalPatch:
				if patch != "" {
					return automaticAction{patch: patch, patchTarget: response.Approval.PatchTarget, patchRisk: true}
				}
			case ApprovalCommand:
				if command != "" {
					return automaticAction{command: command, commandRisk: true}
				}
			}
		}
		if response.Approval.Kind == ApprovalCommand && command != "" && commandQualifiesForAutoRun(session, command) {
			return automaticAction{command: command, commandRisk: true}
		}
		return automaticAction{}
	}

	if response.Proposal == nil {
		return automaticAction{}
	}
	switch response.Proposal.Kind {
	case ProposalPatch:
		if mode == ApprovalModeDanger && strings.TrimSpace(response.Proposal.Patch) != "" {
			return automaticAction{patch: strings.TrimSpace(response.Proposal.Patch), patchTarget: response.Proposal.PatchTarget}
		}
	case ProposalCommand:
		command := strings.TrimSpace(response.Proposal.Command)
		if command == "" {
			return automaticAction{}
		}
		if mode == ApprovalModeDanger || commandQualifiesForAutoRun(session, command) {
			return automaticAction{command: command}
		}
	}
	return automaticAction{}
}

func commandQualifiesForAutoRun(session SessionContext, command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	if isRemoteShellLocation(session.CurrentShellLocation, session.CurrentShell) {
		return false
	}
	if hasDisallowedShellSyntax(command) {
		return false
	}

	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	if hasLeadingEnvAssignments(fields) {
		return false
	}
	if hasOutsidePathArg(fields[1:]) {
		return false
	}

	switch fields[0] {
	case "pwd", "ls", "tree", "grep", "rg", "cat", "head", "tail", "stat", "file", "pytest":
		return true
	case "sed":
		return !containsAnyField(fields[1:], "-i", "--in-place")
	case "find":
		return !containsAnyFieldPrefix(fields[1:], "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprintf", "-fls")
	case "git":
		return gitCommandQualifiesForAutoRun(fields[1:])
	case "go":
		return goCommandQualifiesForAutoRun(fields[1:])
	case "cargo":
		return cargoCommandQualifiesForAutoRun(fields[1:])
	case "npm", "pnpm", "yarn":
		return nodeTestCommandQualifiesForAutoRun(fields)
	default:
		return false
	}
}

func hasDisallowedShellSyntax(command string) bool {
	if strings.ContainsAny(command, "\n\r;`<>") {
		return true
	}
	for _, token := range []string{"&&", "||", "|", "$(", "${", "&"} {
		if strings.Contains(command, token) {
			return true
		}
	}
	return false
}

func hasLeadingEnvAssignments(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		if strings.HasPrefix(field, "-") {
			return false
		}
		if !strings.Contains(field, "=") {
			return false
		}
		return true
	}
	return false
}

func hasOutsidePathArg(fields []string) bool {
	for _, field := range fields {
		if strings.HasPrefix(field, "-") {
			continue
		}
		for _, token := range strings.Split(field, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			if strings.HasPrefix(token, "/") || strings.HasPrefix(token, "~/") || token == ".." || strings.HasPrefix(token, "../") || strings.Contains(token, "/../") {
				return true
			}
		}
	}
	return false
}

func containsAnyField(fields []string, values ...string) bool {
	for _, field := range fields {
		for _, value := range values {
			if field == value || strings.HasPrefix(field, value+"=") || strings.HasPrefix(field, value) {
				return true
			}
		}
	}
	return false
}

func containsAnyFieldPrefix(fields []string, prefixes ...string) bool {
	for _, field := range fields {
		for _, prefix := range prefixes {
			if strings.HasPrefix(field, prefix) {
				return true
			}
		}
	}
	return false
}

func gitCommandQualifiesForAutoRun(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "status", "diff", "log", "rev-parse", "ls-files":
		return true
	case "branch":
		return len(fields) >= 2 && fields[1] == "--show-current"
	default:
		return false
	}
}

func goCommandQualifiesForAutoRun(fields []string) bool {
	if len(fields) == 0 || fields[0] != "test" {
		return false
	}
	return !containsAnyField(fields[1:], "-c", "-o", "-exec")
}

func cargoCommandQualifiesForAutoRun(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "test", "check":
		return true
	default:
		return false
	}
}

func nodeTestCommandQualifiesForAutoRun(fields []string) bool {
	if len(fields) < 2 {
		return false
	}
	return fields[1] == "test"
}

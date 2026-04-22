package tuifeatures

import (
	"fmt"
	"sort"
	"strings"
)

const (
	ShellCompletion     = "shell-completion"
	HistoryCompletion   = "history-completion"
	SlashCompletion     = "slash-completion"
	CompletionGhost     = "completion-ghost"
	FooterHints         = "footer-hints"
	StatusLine          = "status-line"
	ShellContext        = "shell-context"
	ApprovalLabel       = "approval-label"
	ModelStatus         = "model-status"
	ContextUsage        = "context-usage"
	BusyIndicator       = "busy-indicator"
	ActionCard          = "action-card"
	PlanCard            = "plan-card"
	ExecutionCard       = "execution-card"
	ShellTail           = "shell-tail"
	Transcript          = "transcript"
	TranscriptChrome    = "transcript-chrome"
	Mouse               = "mouse"
	BusyTick            = "busy-tick"
	ExecutionPolling    = "execution-polling"
	ShellContextPolling = "shell-context-polling"
)

type Set map[string]struct{}

var known = []string{
	ShellCompletion,
	HistoryCompletion,
	SlashCompletion,
	CompletionGhost,
	FooterHints,
	StatusLine,
	ShellContext,
	ApprovalLabel,
	ModelStatus,
	ContextUsage,
	BusyIndicator,
	ActionCard,
	PlanCard,
	ExecutionCard,
	ShellTail,
	Transcript,
	TranscriptChrome,
	Mouse,
	BusyTick,
	ExecutionPolling,
	ShellContextPolling,
}

var allowed = func() map[string]struct{} {
	values := make(map[string]struct{}, len(known))
	for _, name := range known {
		values[name] = struct{}{}
	}
	return values
}()

func Known() []string {
	values := make([]string, len(known))
	copy(values, known)
	return values
}

func ParseDisabled(raw string) (Set, error) {
	names := strings.Split(raw, ",")
	disabled := make(Set, len(names))
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if _, ok := allowed[name]; !ok {
			return nil, fmt.Errorf("unknown TUI disable feature %q (expected one of: %s)", name, strings.Join(known, ", "))
		}
		disabled[name] = struct{}{}
	}
	if len(disabled) == 0 {
		return nil, nil
	}
	return disabled, nil
}

func (s Set) Disabled(name string) bool {
	if len(s) == 0 {
		return false
	}
	_, ok := s[name]
	return ok
}

func (s Set) Enabled(name string) bool {
	return !s.Disabled(name)
}

func (s Set) Clone() Set {
	if len(s) == 0 {
		return nil
	}
	cloned := make(Set, len(s))
	for name := range s {
		cloned[name] = struct{}{}
	}
	return cloned
}

func (s Set) Strings() []string {
	if len(s) == 0 {
		return nil
	}
	values := make([]string, 0, len(s))
	for name := range s {
		values = append(values, name)
	}
	sort.Strings(values)
	return values
}

package shell

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
)

type PromptContext struct {
	User         string
	Host         string
	System       string
	Directory    string
	GitBranch    string
	PromptSymbol string
	Root         bool
	Remote       bool
	RawLine      string
	LastExitCode *int
}

var gitBranchPattern = regexp.MustCompile(`\bgit:\(([^)]+)\)`)

func ParsePromptContextFromCapture(captured string) (PromptContext, bool) {
	localHost, _ := os.Hostname()
	lines := strings.Split(strings.ReplaceAll(captured, "\r\n", "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}

		context, ok := parsePromptContextLine(line, localHost)
		if ok {
			return context, true
		}
	}

	return PromptContext{}, false
}

func GuessLocalContext(workingDir string) PromptContext {
	context := PromptContext{
		Directory:    shortenHome(workingDir),
		PromptSymbol: defaultPromptSymbol(),
	}

	if currentUser, err := user.Current(); err == nil {
		context.User = currentUser.Username
		context.Root = currentUser.Username == "root"
	}

	if host, err := os.Hostname(); err == nil {
		context.Host = host
	}

	if branch := gitBranchForDirectory(workingDir); branch != "" {
		context.GitBranch = branch
	}

	if context.Root {
		context.PromptSymbol = "#"
	}

	return context
}

func (c PromptContext) PromptLine() string {
	parts := make([]string, 0, 4)
	if c.User != "" || c.Host != "" {
		switch {
		case c.User != "" && c.Host != "":
			parts = append(parts, c.User+"@"+c.Host)
		case c.User != "":
			parts = append(parts, c.User)
		case c.Host != "":
			parts = append(parts, c.Host)
		}
	}
	if c.Directory != "" {
		parts = append(parts, c.Directory)
	}
	if c.GitBranch != "" {
		parts = append(parts, "git:("+c.GitBranch+")")
	}
	if c.PromptSymbol != "" {
		parts = append(parts, c.PromptSymbol)
	}

	return strings.TrimSpace(strings.Join(parts, " "))
}

func parsePromptContextLine(line string, localHost string) (PromptContext, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return PromptContext{}, false
	}

	promptSymbol := lastPromptSymbol(trimmed)
	if promptSymbol == "" {
		return PromptContext{}, false
	}

	body := strings.TrimSpace(strings.TrimSuffix(trimmed, promptSymbol))
	if body == "" || body == "quote" || body == "dquote" || body == "bquote" {
		return PromptContext{}, false
	}

	branch := ""
	if match := gitBranchPattern.FindStringSubmatch(body); len(match) == 2 {
		branch = strings.TrimSpace(match[1])
		body = strings.TrimSpace(gitBranchPattern.ReplaceAllString(body, ""))
	}

	userHost, directory := parsePromptPrefix(body)
	if userHost == "" && directory == "" && branch == "" {
		return PromptContext{}, false
	}

	userName, host := splitUserHost(userHost)
	root := promptSymbol == "#" || userName == "root"

	return PromptContext{
		User:         userName,
		Host:         host,
		Directory:    directory,
		GitBranch:    branch,
		PromptSymbol: promptSymbol,
		Root:         root,
		Remote:       isRemoteHost(host, localHost),
		RawLine:      trimmed,
	}, true
}

func parsePromptPrefix(body string) (string, string) {
	tokens := strings.Fields(body)
	if len(tokens) == 0 {
		return "", ""
	}

	first := tokens[0]
	if at := strings.Index(first, "@"); at >= 0 {
		if colon := strings.Index(first, ":"); colon > at {
			userHost := first[:colon]
			directory := first[colon+1:]
			if directory == "" && len(tokens) > 1 {
				directory = tokens[1]
			}
			return userHost, directory
		}
		if len(tokens) > 1 && isPathLike(tokens[1]) {
			return first, tokens[1]
		}
		return first, ""
	}

	if isPathLike(first) {
		return "", first
	}

	if len(tokens) > 1 && isPathLike(tokens[1]) {
		return "", tokens[1]
	}

	return "", ""
}

func splitUserHost(value string) (string, string) {
	if value == "" {
		return "", ""
	}
	parts := strings.SplitN(value, "@", 2)
	if len(parts) != 2 {
		return value, ""
	}
	return parts[0], parts[1]
}

func isRemoteHost(host string, localHost string) bool {
	if host == "" || localHost == "" {
		return false
	}

	return normalizeHost(host) != normalizeHost(localHost)
}

func normalizeHost(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if index := strings.Index(value, "."); index >= 0 {
		value = value[:index]
	}
	return value
}

func lastPromptSymbol(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	last := value[len(value)-1]
	switch last {
	case '$', '#', '%', '>':
		return string(last)
	default:
		return ""
	}
}

func isPathLike(value string) bool {
	return strings.HasPrefix(value, "~") ||
		strings.HasPrefix(value, "/") ||
		strings.HasPrefix(value, ".") ||
		strings.Contains(value, "/")
}

func gitBranchForDirectory(workingDir string) string {
	if strings.TrimSpace(workingDir) == "" {
		return ""
	}

	output, err := exec.Command("git", "-C", workingDir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
}

func shortenHome(path string) string {
	if path == "" {
		return ""
	}

	absolutePath := path
	if resolved, err := filepath.Abs(path); err == nil {
		absolutePath = resolved
	}

	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		return absolutePath
	}

	if absolutePath == homeDir {
		return "~"
	}
	if strings.HasPrefix(absolutePath, homeDir+string(os.PathSeparator)) {
		return "~" + strings.TrimPrefix(absolutePath, homeDir)
	}

	return absolutePath
}

func defaultPromptSymbol() string {
	if os.Geteuid() == 0 {
		return "#"
	}
	if strings.Contains(strings.ToLower(filepath.Base(os.Getenv("SHELL"))), "zsh") {
		return "%"
	}
	return "$"
}

package shell

import (
	"path/filepath"
	"testing"
)

const (
	shellTestUser       = "localuser"
	shellTestHost       = "workstation"
	shellTestRemoteUser = "remoteuser"
	shellTestRemoteHost = "remotebox"
	shellTestBranch     = "main"
)

func shellTestHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	return home
}

func shellTestProjectDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(shellTestHome(t), "workspace", "project")
}

func shellTestProjectPrompt(t *testing.T) string {
	t.Helper()
	return shortenHome(shellTestProjectDir(t)) + " %"
}

func shellTestProjectBranchPrompt(t *testing.T) string {
	t.Helper()
	return shortenHome(shellTestProjectDir(t)) + " git:(" + shellTestBranch + ") %"
}

func shellTestLocalPromptContext(t *testing.T) PromptContext {
	t.Helper()
	dir := shellTestProjectDir(t)
	shortDir := shortenHome(dir)
	return PromptContext{
		User:         shellTestUser,
		Host:         shellTestHost,
		Directory:    shortDir,
		GitBranch:    shellTestBranch,
		PromptSymbol: "%",
		RawLine:      shellTestUser + "@" + shellTestHost + " " + shortDir + " git:(" + shellTestBranch + ") %",
	}
}

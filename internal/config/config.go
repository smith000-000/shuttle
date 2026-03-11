package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
)

const (
	defaultSessionName = "shuttle"
	defaultStateDir    = ".shuttle"
	defaultLogName     = "shuttle.log"
)

type Config struct {
	SessionName string
	StartDir    string
	TmuxSocket  string
	StateDir    string
	LogPath     string
	Inject      string
	Track       string
	TUI         bool
	InjectEnter bool
}

func Parse(args []string) (Config, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return Config{}, err
	}

	stateDir := envOrDefault("SHUTTLE_STATE_DIR", filepath.Join(workingDir, defaultStateDir))
	sessionName := envOrDefault("SHUTTLE_SESSION", defaultSessionName)
	socketName := os.Getenv("SHUTTLE_TMUX_SOCKET")

	fs := flag.NewFlagSet("shuttle", flag.ContinueOnError)

	cfg := Config{}
	fs.StringVar(&cfg.SessionName, "session", sessionName, "tmux session name")
	fs.StringVar(&cfg.StartDir, "dir", workingDir, "working directory for new panes")
	fs.StringVar(&cfg.TmuxSocket, "socket", socketName, "tmux socket name for an isolated server")
	fs.StringVar(&cfg.StateDir, "state-dir", stateDir, "state directory for logs and future local state")
	fs.StringVar(&cfg.Inject, "inject", "", "inject a shell command into the top pane after bootstrap")
	fs.StringVar(&cfg.Track, "track", "", "inject a tracked shell command into the top pane and wait for its result")
	fs.BoolVar(&cfg.TUI, "tui", false, "run the minimal interactive TUI shell")
	fs.BoolVar(&cfg.InjectEnter, "enter", true, "append Enter when injecting a command")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	if cfg.SessionName == "" {
		return Config{}, errors.New("session name must not be empty")
	}

	startDir, err := filepath.Abs(cfg.StartDir)
	if err != nil {
		return Config{}, err
	}
	cfg.StartDir = startDir

	stateDirAbs, err := filepath.Abs(cfg.StateDir)
	if err != nil {
		return Config{}, err
	}
	cfg.StateDir = stateDirAbs
	cfg.LogPath = filepath.Join(cfg.StateDir, defaultLogName)

	return cfg, nil
}

func envOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}

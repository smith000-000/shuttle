package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"aiterm/internal/app"
	"aiterm/internal/config"
	"aiterm/internal/logging"
)

func main() {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(2)
	}

	logger, closeLogger, err := logging.New(cfg.LogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger error: %v\n", err)
		os.Exit(1)
	}
	defer closeLogger()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result, err := app.New(cfg, logger).Run(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "shuttle canceled")
			os.Exit(130)
		}

		fmt.Fprintf(os.Stderr, "shuttle error: %v\n", err)
		os.Exit(1)
	}

	if result.Interactive {
		return
	}

	fmt.Printf(
		"session=%s created=%t top_pane=%s bottom_pane=%s\n",
		result.Workspace.SessionName,
		result.Created,
		result.Workspace.TopPane.ID,
		result.Workspace.BottomPane.ID,
	)

	if result.InjectedCommand != "" {
		fmt.Printf("injected=%q target=%s\n", result.InjectedCommand, result.Workspace.TopPane.ID)
	}

	if result.Tracked != nil {
		fmt.Printf(
			"tracked command_id=%s exit_code=%d\n%s\n",
			result.Tracked.CommandID,
			result.Tracked.ExitCode,
			result.Tracked.Captured,
		)
	}
}

package logging

import (
	"io"
	"log/slog"
	"os"

	"aiterm/internal/securefs"
)

func New(logPath string) (*slog.Logger, func() error, error) {
	file, err := securefs.OpenAppendPrivate(logPath, 0o600)
	if err != nil {
		return nil, nil, err
	}

	writer := io.MultiWriter(os.Stderr, file)
	handler := slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo})

	return slog.New(handler), file.Close, nil
}

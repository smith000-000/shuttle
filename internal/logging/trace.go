package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	traceMu      sync.RWMutex
	traceEnabled bool
	traceLogger  = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
)

func ConfigureTrace(tracePath string, enabled bool) (func() error, error) {
	traceMu.Lock()
	defer traceMu.Unlock()

	traceEnabled = enabled
	if !enabled {
		traceLogger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
		return func() error { return nil }, nil
	}

	if err := os.MkdirAll(filepath.Dir(tracePath), 0o755); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(tracePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	traceLogger = slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return file.Close, nil
}

func Trace(event string, attrs ...any) {
	traceMu.RLock()
	enabled := traceEnabled
	logger := traceLogger
	traceMu.RUnlock()

	if !enabled {
		return
	}

	logger.Info(event, attrs...)
}

func TraceError(event string, err error, attrs ...any) {
	if err == nil {
		Trace(event, attrs...)
		return
	}

	Trace(event, append(attrs, "error", err.Error())...)
}

func TraceEnabled() bool {
	traceMu.RLock()
	defer traceMu.RUnlock()

	return traceEnabled
}

func Preview(value string, maxRunes int) string {
	normalized := strings.ReplaceAll(value, "\r\n", "\n")
	normalized = strings.TrimSpace(normalized)
	if maxRunes <= 0 {
		maxRunes = 256
	}

	runes := []rune(normalized)
	if len(runes) <= maxRunes {
		return normalized
	}

	return fmt.Sprintf("%s… (%d chars)", string(runes[:maxRunes]), len(normalized))
}

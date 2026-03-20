package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"aiterm/internal/config"
	"aiterm/internal/securefs"
)

var (
	traceMu      sync.RWMutex
	traceEnabled bool
	traceMode    config.TraceMode
	traceLogger  = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
)

func ConfigureTrace(tracePath string, mode config.TraceMode) (func() error, error) {
	traceMu.Lock()
	defer traceMu.Unlock()

	traceEnabled = mode != config.TraceModeOff
	traceMode = mode
	if !traceEnabled {
		traceLogger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
		return func() error { return nil }, nil
	}

	file, err := securefs.OpenAppendPrivate(tracePath, 0o600)
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

	logger.Info(event, sanitizeTraceAttrs(traceMode, attrs...)...)
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

func TraceMode() config.TraceMode {
	traceMu.RLock()
	defer traceMu.RUnlock()
	return traceMode
}

func sanitizeTraceAttrs(mode config.TraceMode, attrs ...any) []any {
	if mode == config.TraceModeSensitive {
		return attrs
	}

	sanitized := make([]any, 0, len(attrs))
	for index := 0; index < len(attrs); index += 2 {
		if index+1 >= len(attrs) {
			sanitized = append(sanitized, attrs[index])
			break
		}

		key, ok := attrs[index].(string)
		if !ok {
			sanitized = append(sanitized, attrs[index], attrs[index+1])
			continue
		}
		sanitized = append(sanitized, key, sanitizeTraceValue(mode, key, attrs[index+1]))
	}
	return sanitized
}

func sanitizeTraceValue(mode config.TraceMode, key string, value any) any {
	if mode == config.TraceModeSensitive {
		return value
	}

	text, ok := value.(string)
	if !ok {
		return value
	}

	switch key {
	case "body", "keys", "command", "prompt", "user_prompt", "note", "text", "runes",
		"capture_preview", "captured_preview", "delta_preview", "tail_preview", "last_capture_preview",
		"summary_preview", "message_preview", "agent_prompt_preview", "output_preview":
		if strings.TrimSpace(text) == "" {
			return text
		}
		return "[redacted in safe trace]"
	case "args":
		if strings.Contains(text, "send-keys") {
			return "[redacted in safe trace]"
		}
	}

	return value
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

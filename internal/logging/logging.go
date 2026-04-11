package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"aiterm/internal/securefs"
)

func New(logPath string) (*slog.Logger, func() error, error) {
	file, err := securefs.OpenAppendPrivate(logPath, 0o600)
	if err != nil {
		return nil, nil, err
	}

	writer := io.MultiWriter(os.Stderr, file)
	handler := slog.NewTextHandler(writer, &slog.HandlerOptions{
		Level:       slog.LevelInfo,
		ReplaceAttr: sanitizeOperationalAttr,
	})

	return slog.New(handler), file.Close, nil
}

func sanitizeOperationalAttr(_ []string, attr slog.Attr) slog.Attr {
	attr.Value = sanitizeOperationalValue(attr.Key, attr.Value)
	return attr
}

func sanitizeOperationalValue(key string, value slog.Value) slog.Value {
	text, ok := operationalStringValue(value)
	if !ok {
		return value
	}

	switch key {
	case "body", "keys", "command", "prompt", "user_prompt", "note", "text", "runes",
		"capture_preview", "captured_preview", "delta_preview", "tail_preview", "last_capture_preview",
		"summary_preview", "message_preview", "agent_prompt_preview", "output_preview",
		"summary", "refine_text", "patch", "proposal_patch", "approval_patch", "description", "proposal_description", "approval_summary":
		if strings.TrimSpace(text) == "" {
			return value
		}
		return slog.StringValue("[redacted in operational log]")
	case "api_key", "api_key_env", "api_key_ref":
		if strings.TrimSpace(text) == "" {
			return value
		}
		return slog.StringValue("[redacted auth metadata]")
	case "args":
		if strings.Contains(text, "send-keys") {
			return slog.StringValue("[redacted in operational log]")
		}
	}

	return value
}

func operationalStringValue(value slog.Value) (string, bool) {
	switch value.Kind() {
	case slog.KindString:
		return value.String(), true
	default:
		return "", false
	}
}

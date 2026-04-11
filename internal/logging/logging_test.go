package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperationalLogRedactsSensitiveAttrs(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "shuttle.log")
	logger, closeLogger, err := New(logPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer closeLogger()

	logger.Info(
		"test",
		slog.String("command", "rm -rf /tmp/nope"),
		slog.String("body", "{\"secret\":true}"),
		slog.String("keys", "hunter2"),
		slog.String("prompt", "top-secret prompt"),
		slog.String("api_key_env", "OPENAI_API_KEY"),
		slog.String("summary", "delete temp files"),
		slog.String("args", "send-keys -t %0 evil"),
	)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	content := string(data)
	for _, forbidden := range []string{
		"rm -rf /tmp/nope",
		"{\"secret\":true}",
		"hunter2",
		"top-secret prompt",
		"OPENAI_API_KEY",
		"delete temp files",
		"send-keys -t %0 evil",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("expected operational log redaction for %q, got %q", forbidden, content)
		}
	}
	if !strings.Contains(content, "[redacted in operational log]") || !strings.Contains(content, "[redacted auth metadata]") {
		t.Fatalf("expected operational log redaction markers, got %q", content)
	}
}

func TestOperationalLogKeepsNonSensitiveAttrs(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "shuttle.log")
	logger, closeLogger, err := New(logPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer closeLogger()

	logger.Info("workspace ready", slog.String("session", "shuttle_abc123"), slog.Bool("created", true))

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "workspace ready") || !strings.Contains(content, "session=shuttle_abc123") || !strings.Contains(content, "created=true") {
		t.Fatalf("expected non-sensitive attrs to remain visible, got %q", content)
	}
}

func TestOperationalLogUsesPrivateFilePermissions(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "shuttle.log")
	_, closeLogger, err := New(logPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer closeLogger()

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected operational log perms 0600, got %#o", info.Mode().Perm())
	}
}

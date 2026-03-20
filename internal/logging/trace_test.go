package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aiterm/internal/config"
)

func TestSafeTraceRedactsSensitiveAttrs(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace.log")
	closeTrace, err := ConfigureTrace(tracePath, config.TraceModeSafe)
	if err != nil {
		t.Fatalf("ConfigureTrace() error = %v", err)
	}
	defer closeTrace()

	Trace("test", "command", "rm -rf /tmp/nope", "body", "{\"secret\":true}", "args", "send-keys -t %0 evil")

	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	content := string(data)
	if strings.Contains(content, "rm -rf /tmp/nope") || strings.Contains(content, "\"secret\":true") || strings.Contains(content, "send-keys -t %0 evil") {
		t.Fatalf("expected safe trace redaction, got %q", content)
	}
	if !strings.Contains(content, "[redacted in safe trace]") {
		t.Fatalf("expected redaction marker, got %q", content)
	}
}

func TestSensitiveTraceKeepsSensitiveAttrs(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace.log")
	closeTrace, err := ConfigureTrace(tracePath, config.TraceModeSensitive)
	if err != nil {
		t.Fatalf("ConfigureTrace() error = %v", err)
	}
	defer closeTrace()

	Trace("test", "command", "echo hi")

	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "echo hi") {
		t.Fatalf("expected sensitive trace to keep raw value, got %q", string(data))
	}
}

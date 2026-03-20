package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aiterm/internal/protocol"
	"aiterm/internal/securefs"
)

func (o *Observer) buildTrackedTransport(ctx context.Context, paneID string, command string, markers protocol.Markers) (string, func(), error) {
	if transport, cleanup, ok := o.buildLocalManagedTransport(ctx, paneID, command, markers); ok {
		return transport, cleanup, nil
	}

	return protocol.WrapCommand(command, markers), func() {}, nil
}

func (o *Observer) buildLocalManagedTransport(ctx context.Context, paneID string, command string, markers protocol.Markers) (string, func(), bool) {
	if strings.TrimSpace(o.stateDir) == "" {
		return "", nil, false
	}

	promptContext, err := o.CaptureShellContext(ctx, paneID)
	if err != nil || promptContext.PromptLine() == "" {
		promptContext = o.promptHint
	}
	if promptContext.PromptLine() == "" || promptContext.Remote {
		return "", nil, false
	}

	scriptPath, err := writeTrackedCommandScript(o.stateDir, command, markers)
	if err != nil {
		return "", nil, false
	}

	return ". " + shellQuote(scriptPath), func() {
		_ = os.Remove(scriptPath)
	}, true
}

func writeTrackedCommandScript(stateDir string, command string, markers protocol.Markers) (string, error) {
	commandsDir := filepath.Join(stateDir, "commands")
	if err := securefs.EnsurePrivateDir(commandsDir); err != nil {
		return "", fmt.Errorf("create command staging directory: %w", err)
	}

	scriptPath := filepath.Join(commandsDir, markers.CommandID+".sh")
	var builder strings.Builder
	builder.WriteString("printf '%s\\n' ")
	builder.WriteString(shellQuote(markers.BeginLine))
	builder.WriteString("\n")
	builder.WriteString(command)
	if !strings.HasSuffix(command, "\n") {
		builder.WriteString("\n")
	}
	builder.WriteString("__shuttle_status=$?\n")
	builder.WriteString("printf '%s%s\\n' ")
	builder.WriteString(shellQuote(markers.EndPrefix))
	builder.WriteString(" \"$__shuttle_status\"\n")
	builder.WriteString("unset __shuttle_status\n")

	if err := securefs.WriteExclusivePrivate(scriptPath, []byte(builder.String()), 0o600); err != nil {
		return "", fmt.Errorf("write tracked command script: %w", err)
	}

	return scriptPath, nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

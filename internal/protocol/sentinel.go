package protocol

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	beginMarker = "__SHUTTLE_B__:"
	endMarker   = "__SHUTTLE_E__:"
)

var markerCounter atomic.Uint64

type Markers struct {
	CommandID string
	BeginLine string
	EndPrefix string
}

func NewMarkers() Markers {
	commandID := shortCommandID(time.Now().UnixNano(), markerCounter.Add(1))

	return Markers{
		CommandID: commandID,
		BeginLine: beginMarker + commandID,
		EndPrefix: endMarker + commandID + ":",
	}
}

func WrapCommand(command string, markers Markers) string {
	var builder strings.Builder

	builder.WriteString("printf '%s\\n' ")
	builder.WriteString(shellQuote(markers.BeginLine))
	builder.WriteString("; eval \"$(printf '%s\\n'")
	for _, line := range splitCommandLines(command) {
		builder.WriteString(" ")
		builder.WriteString(shellQuote(line))
	}
	builder.WriteString(")\"; __shuttle_status=$?; printf '%s%s\\n' ")
	builder.WriteString(shellQuote(markers.EndPrefix))
	builder.WriteString(" \"$__shuttle_status\"")

	return builder.String()
}

func splitCommandLines(command string) []string {
	normalized := strings.ReplaceAll(command, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func shortCommandID(value int64, counter uint64) string {
	return strings.ToLower(strconv.FormatInt(value, 36) + strconv.FormatUint(counter, 36))
}

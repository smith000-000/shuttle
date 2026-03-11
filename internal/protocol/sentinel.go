package protocol

import (
	"strconv"
	"strings"
	"time"
)

const (
	beginMarker = "__SHUTTLE_B__:"
	endMarker   = "__SHUTTLE_E__:"
)

type Markers struct {
	CommandID string
	BeginLine string
	EndPrefix string
}

func NewMarkers() Markers {
	commandID := shortCommandID(time.Now().UnixNano())

	return Markers{
		CommandID: commandID,
		BeginLine: beginMarker + commandID,
		EndPrefix: endMarker + commandID + ":",
	}
}

func WrapCommand(command string, markers Markers) string {
	var builder strings.Builder

	builder.WriteString("echo ")
	builder.WriteString(markers.BeginLine)
	builder.WriteString("\n")
	builder.WriteString(command)
	if !strings.HasSuffix(command, "\n") {
		builder.WriteString("\n")
	}
	builder.WriteString("echo ")
	builder.WriteString(markers.EndPrefix)
	builder.WriteString("$?")

	return builder.String()
}

func shortCommandID(value int64) string {
	return strings.ToLower(strconv.FormatInt(value, 36))
}

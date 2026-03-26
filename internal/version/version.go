package version

import (
	"fmt"
	"strings"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func String() string {
	return fmt.Sprintf(
		"shuttle %s (commit %s, built %s)",
		normalized(Version, "dev"),
		normalized(Commit, "unknown"),
		normalized(BuildDate, "unknown"),
	)
}

func normalized(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

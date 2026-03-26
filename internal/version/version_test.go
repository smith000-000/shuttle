package version

import "testing"

func TestStringFallsBackForBlankMetadata(t *testing.T) {
	previousVersion := Version
	previousCommit := Commit
	previousBuildDate := BuildDate
	t.Cleanup(func() {
		Version = previousVersion
		Commit = previousCommit
		BuildDate = previousBuildDate
	})

	Version = ""
	Commit = ""
	BuildDate = ""

	if got := String(); got != "shuttle dev (commit unknown, built unknown)" {
		t.Fatalf("unexpected version string %q", got)
	}
}

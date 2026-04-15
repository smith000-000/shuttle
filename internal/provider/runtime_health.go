package provider

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const minimumCodexRuntimeVersion = "0.118.0"

var runtimeCommandOutput = func(ctx context.Context, command string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, command, args...).CombinedOutput()
}

var codexRuntimeVersionProbe = detectCodexRuntimeVersion

func ValidateResolvedRuntime(resolved ResolvedRuntime) error {
	switch strings.TrimSpace(resolved.SelectedType) {
	case RuntimeBuiltin, "":
		return nil
	case RuntimeCodexSDK, RuntimeCodexAppServer:
		return validateCodexRuntimeCommand(resolved.Command)
	default:
		return nil
	}
}

func validateCodexRuntimeCommand(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		command = defaultCodexCLICommand
	}
	if _, err := runtimeLookPath(command); err != nil {
		return fmt.Errorf("find codex runtime command %q: %w", command, err)
	}
	version, err := codexRuntimeVersionProbe(command)
	if err != nil {
		return err
	}
	if !versionAtLeast(version, minimumCodexRuntimeVersion) {
		return fmt.Errorf("codex runtime %q is too old: found %s, require %s or newer", command, version, minimumCodexRuntimeVersion)
	}
	return nil
}

func detectCodexRuntimeVersion(command string) (string, error) {
	output, err := runtimeCommandOutput(context.Background(), command, "--version")
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("probe codex runtime version: %s", message)
	}
	version := parseSemverLike(string(output))
	if version == "" {
		return "", fmt.Errorf("probe codex runtime version: could not parse version from %q", strings.TrimSpace(string(output)))
	}
	return version, nil
}

var semverLikePattern = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)`)

func parseSemverLike(value string) string {
	match := semverLikePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 4 {
		return ""
	}
	return strings.Join(match[1:], ".")
}

func versionAtLeast(found string, minimum string) bool {
	foundParts := strings.Split(strings.TrimSpace(found), ".")
	minimumParts := strings.Split(strings.TrimSpace(minimum), ".")
	if len(foundParts) != 3 || len(minimumParts) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		foundValue, err := strconv.Atoi(foundParts[i])
		if err != nil {
			return false
		}
		minimumValue, err := strconv.Atoi(minimumParts[i])
		if err != nil {
			return false
		}
		if foundValue > minimumValue {
			return true
		}
		if foundValue < minimumValue {
			return false
		}
	}
	return true
}

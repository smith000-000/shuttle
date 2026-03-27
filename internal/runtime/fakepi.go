package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"time"

	"aiterm/internal/config"
	"aiterm/internal/securefs"
)

const fakePIPackagePath = "./integration/harness/cmd/fakepi"

func fakePIBinaryPath(cfg config.Config) string {
	name := "fake-pi-runtime"
	if stdruntime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(cfg.StateDir, "fakepi", "bin", name)
}

func fakePICommand(cfg config.Config) string {
	command := strings.TrimSpace(cfg.RuntimeCommand)
	if command == "" {
		return fakePIBinaryPath(cfg)
	}
	if filepath.IsAbs(command) {
		return command
	}
	return filepath.Join(cfg.StateDir, command)
}

func fakePIAvailable(cfg config.Config) (bool, string) {
	command := fakePICommand(cfg)
	if fileLooksRunnable(command) {
		return true, ""
	}
	if _, err := lookPath("go"); err != nil {
		return false, "Fake PI requires Go in PATH so Shuttle can build the local helper."
	}
	if _, err := findFakePIRepoRoot(cfg.StartDir); err != nil {
		return false, "Fake PI is only available from a Shuttle checkout that includes integration/harness/cmd/fakepi."
	}
	return true, ""
}

func ensureFakePIBinary(cfg config.Config) (string, error) {
	command := fakePICommand(cfg)
	if fileLooksRunnable(command) {
		return command, nil
	}
	if _, err := lookPath("go"); err != nil {
		return "", fmt.Errorf("find go tool for fake PI: %w", err)
	}
	root, err := findFakePIRepoRoot(cfg.StartDir)
	if err != nil {
		return "", err
	}
	if err := securefs.EnsurePrivateDir(filepath.Dir(command)); err != nil {
		return "", fmt.Errorf("prepare fake PI runtime dir: %w", err)
	}
	cacheDir := filepath.Join(cfg.StateDir, "fakepi", "gocache")
	tmpDir := filepath.Join(cfg.StateDir, "fakepi", "gotmp")
	if err := securefs.EnsurePrivateDir(cacheDir); err != nil {
		return "", fmt.Errorf("prepare fake PI go cache: %w", err)
	}
	if err := securefs.EnsurePrivateDir(tmpDir); err != nil {
		return "", fmt.Errorf("prepare fake PI go temp dir: %w", err)
	}

	if needsBuild, err := fakePIBinaryNeedsBuild(command, root); err == nil && !needsBuild {
		return command, nil
	}

	build := exec.Command("go", "build", "-o", command, fakePIPackagePath)
	build.Dir = root
	build.Env = append(os.Environ(),
		"GOCACHE="+cacheDir,
		"GOTMPDIR="+tmpDir,
	)
	output, err := build.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("build fake PI runtime: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return command, nil
}

func fakePIBinaryNeedsBuild(binaryPath string, repoRoot string) (bool, error) {
	binaryInfo, err := os.Stat(binaryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return true, err
	}
	sourceInfo, err := os.Stat(filepath.Join(repoRoot, "integration", "harness", "cmd", "fakepi", "main.go"))
	if err != nil {
		return true, err
	}
	return binaryInfo.ModTime().Before(sourceInfo.ModTime().Add(50 * time.Millisecond)), nil
}

func findFakePIRepoRoot(startDir string) (string, error) {
	current := strings.TrimSpace(startDir)
	if current == "" {
		current = "."
	}
	current, err := filepath.Abs(current)
	if err != nil {
		return "", fmt.Errorf("resolve fake PI start dir: %w", err)
	}
	for {
		helper := filepath.Join(current, "integration", "harness", "cmd", "fakepi", "main.go")
		mod := filepath.Join(current, "go.mod")
		if fileExists(helper) && fileExists(mod) {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", fmt.Errorf("fake PI helper source not found from %s", startDir)
}

func fileExists(path string) bool {
	info, err := os.Stat(strings.TrimSpace(path))
	return err == nil && !info.IsDir()
}

func fileLooksRunnable(path string) bool {
	info, err := os.Stat(strings.TrimSpace(path))
	if err != nil || info.IsDir() {
		return false
	}
	return true
}

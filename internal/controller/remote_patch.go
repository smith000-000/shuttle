package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aiterm/internal/patchapply"
	"aiterm/internal/shell"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

const remotePatchCommandTimeout = 20 * time.Second
const remotePatchPayloadChunkSize = 1024

type remotePatchCapabilities struct {
	Git         bool
	Python3     bool
	Base64      bool
	Mktemp      bool
	ShellFamily string
	System      string
	OSRelease   string
}

type remoteFileSnapshot struct {
	Exists bool
	Mode   fs.FileMode
	Data   []byte
}

type remoteReadPayload struct {
	Exists bool   `json:"exists"`
	Mode   uint32 `json:"mode"`
	Data   string `json:"data"`
	Error  string `json:"error"`
}

const (
	remoteReadMarker    = "__SHUTTLE_REMOTE_READ__"
	remoteReadDataBegin = "__SHUTTLE_REMOTE_DATA_BEGIN__"
	remoteReadDataEnd   = "__SHUTTLE_REMOTE_DATA_END__"
)

func (c *LocalController) applyRemotePatch(ctx context.Context, patch string) ([]TranscriptEvent, error) {
	trackedShell := c.syncTrackedShellTarget(ctx)
	c.refreshUserShellContextForTarget(ctx, trackedShell, false)

	c.mu.Lock()
	runner := c.runner
	currentShell := c.session.CurrentShell
	currentLocation := c.session.CurrentShellLocation
	c.mu.Unlock()

	targetLabel := strings.TrimSpace(remotePatchTargetLabel(currentShell, trackedShell))
	if runner == nil {
		return c.recordPatchApplyFailure("", PatchTargetRemoteShell, targetLabel, "remote patch runner is not configured")
	}
	if !isRemoteShellLocation(currentLocation, currentShell) {
		return c.recordPatchApplyFailure("", PatchTargetRemoteShell, targetLabel, "remote patch target is ambiguous or not currently active")
	}

	files, err := parseRemotePatchFiles(patch)
	if err != nil {
		return c.recordPatchApplyFailure("", PatchTargetRemoteShell, targetLabel, err.Error())
	}

	tempDir, err := os.MkdirTemp("", "shuttle-remote-patch-*")
	if err != nil {
		return c.recordPatchApplyFailure("", PatchTargetRemoteShell, targetLabel, "create remote patch temp dir: "+err.Error())
	}
	defer os.RemoveAll(tempDir)

	caps, capabilitySource, err := c.resolveRemotePatchCapabilities(ctx, runner, trackedShell, currentShell)
	if err != nil {
		return c.recordPatchApplyFailure("", PatchTargetRemoteShell, targetLabel, err.Error())
	}

	paths := remotePatchPaths(files)
	for _, rel := range paths {
		snapshot, err := c.readRemoteFile(ctx, runner, trackedShell, caps, rel)
		if err != nil {
			return c.recordPatchApplyFailure("", PatchTargetRemoteShell, targetLabel, err.Error())
		}
		if !snapshot.Exists {
			continue
		}
		abs := filepath.Join(tempDir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return c.recordPatchApplyFailure("", PatchTargetRemoteShell, targetLabel, "prepare remote patch temp tree: "+err.Error())
		}
		if err := os.WriteFile(abs, snapshot.Data, snapshot.Mode); err != nil {
			return c.recordPatchApplyFailure("", PatchTargetRemoteShell, targetLabel, "stage remote patch snapshot: "+err.Error())
		}
	}

	applier, err := patchapply.New(tempDir)
	if err != nil {
		return c.recordPatchApplyFailure("", PatchTargetRemoteShell, targetLabel, err.Error())
	}
	result, err := applier.Apply(ctx, patch)
	if err != nil {
		return c.recordPatchApplyFailure(result.WorkspaceRoot, PatchTargetRemoteShell, targetLabel, err.Error())
	}

	transport, caps, capabilitySource, err := c.selectRemotePatchTransport(ctx, runner, trackedShell, currentShell, caps, capabilitySource, patch)
	if err != nil {
		return c.recordPatchApplyFailure("", PatchTargetRemoteShell, targetLabel, err.Error())
	}
	if err := c.commitRemotePatchResult(ctx, runner, trackedShell, caps, tempDir, result.Files, patch, transport); err != nil {
		return c.recordPatchApplyFailure("", PatchTargetRemoteShell, targetLabel, err.Error())
	}
	if err := c.verifyRemotePatchState(ctx, runner, trackedShell, caps, tempDir, result.Files); err != nil {
		if transport == PatchTransportGit {
			_ = reverseRemoteGitPatch(ctx, runner, trackedShell, caps, patch)
		}
		return c.recordPatchApplyFailure("", PatchTargetRemoteShell, targetLabel, err.Error())
	}

	result.WorkspaceRoot = ""
	summary := patchApplySummaryFromResult(result, true, "", PatchTargetRemoteShell, targetLabel, transport, capabilitySource)

	c.mu.Lock()
	c.task.PatchRepairCount = 0
	c.task.LastPatchApplyResult = &summary
	if c.remoteCaps != nil {
		c.remoteCaps.markTransport(c.session.CurrentShellLocation, currentShell, transport)
		c.refreshRemoteCapabilityHintLocked()
	}
	event := c.newEvent(EventPatchApplyResult, summary)
	c.appendEvents(event)
	c.mu.Unlock()

	return []TranscriptEvent{event}, nil
}

func remotePatchTargetLabel(currentShell *shell.PromptContext, trackedShell TrackedShellTarget) string {
	if currentShell != nil && strings.TrimSpace(currentShell.PromptLine()) != "" {
		return currentShell.PromptLine()
	}
	if trackedShell.PaneID != "" {
		return trackedShell.PaneID
	}
	return "remote shell"
}

func parseRemotePatchFiles(patch string) ([]patchapply.FileChange, error) {
	if strings.TrimSpace(patch) == "" {
		return nil, errors.New("patch is empty")
	}
	files, preamble, err := gitdiff.Parse(strings.NewReader(patch + "\n"))
	if err != nil {
		return nil, fmt.Errorf("parse patch: %w", err)
	}
	if strings.TrimSpace(preamble) != "" {
		return nil, errors.New("patch contains unsupported preamble before the first diff")
	}
	changes := make([]patchapply.FileChange, 0, len(files))
	for _, file := range files {
		if file == nil {
			return nil, errors.New("patch contains a nil file entry")
		}
		change, err := remotePatchFileChange(file)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func remotePatchFileChange(file *gitdiff.File) (patchapply.FileChange, error) {
	if file.IsBinary {
		return patchapply.FileChange{}, errors.New("binary patches are not supported")
	}
	if file.IsCopy {
		return patchapply.FileChange{}, errors.New("copy patches are not supported")
	}
	if !remotePatchModesSupported(file.OldMode) || !remotePatchModesSupported(file.NewMode) {
		return patchapply.FileChange{}, errors.New("only regular text-file patches are supported")
	}
	var change patchapply.FileChange
	switch {
	case file.IsRename:
		oldPath, err := sanitizeRemotePatchPath(file.OldName)
		if err != nil {
			return patchapply.FileChange{}, err
		}
		newPath, err := sanitizeRemotePatchPath(file.NewName)
		if err != nil {
			return patchapply.FileChange{}, err
		}
		change = patchapply.FileChange{Operation: patchapply.OperationRename, OldPath: oldPath, NewPath: newPath}
	case file.IsNew:
		newPath, err := sanitizeRemotePatchPath(file.NewName)
		if err != nil {
			return patchapply.FileChange{}, err
		}
		change = patchapply.FileChange{Operation: patchapply.OperationCreate, NewPath: newPath}
	case file.IsDelete:
		oldPath, err := sanitizeRemotePatchPath(file.OldName)
		if err != nil {
			return patchapply.FileChange{}, err
		}
		change = patchapply.FileChange{Operation: patchapply.OperationDelete, OldPath: oldPath}
	default:
		oldPath, err := sanitizeRemotePatchPath(file.OldName)
		if err != nil {
			return patchapply.FileChange{}, err
		}
		newPath, err := sanitizeRemotePatchPath(file.NewName)
		if err != nil {
			return patchapply.FileChange{}, err
		}
		change = patchapply.FileChange{Operation: patchapply.OperationUpdate, OldPath: oldPath, NewPath: newPath}
	}
	if !file.IsRename && !file.IsNew && !file.IsDelete && len(file.TextFragments) == 0 {
		return patchapply.FileChange{}, errors.New("mode-only patches are not supported")
	}
	for _, fragment := range file.TextFragments {
		if fragment == nil {
			return patchapply.FileChange{}, errors.New("patch contains a nil text fragment")
		}
		if err := fragment.Validate(); err != nil {
			return patchapply.FileChange{}, fmt.Errorf("validate fragment for %s: %w", preferredRemotePatchPath(change), err)
		}
	}
	return change, nil
}

func preferredRemotePatchPath(change patchapply.FileChange) string {
	if strings.TrimSpace(change.NewPath) != "" {
		return change.NewPath
	}
	return change.OldPath
}

func remotePatchModesSupported(mode fs.FileMode) bool {
	if mode == 0 {
		return true
	}
	switch mode & 0o170000 {
	case 0, 0o100000:
		return true
	default:
		return false
	}
}

func sanitizeRemotePatchPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	switch {
	case path == "", path == ".":
		return "", errors.New("patch contains an empty path")
	case path == "/dev/null":
		return "", errors.New("patch contains an unexpected /dev/null path")
	case strings.HasPrefix(path, "a/"), strings.HasPrefix(path, "b/"):
		path = path[2:]
	}
	path = filepath.Clean(filepath.FromSlash(path))
	if path == "" || path == "." {
		return "", errors.New("patch contains an empty path")
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute path %q is not allowed", path)
	}
	if path == ".." || strings.HasPrefix(path, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the target root", path)
	}
	return path, nil
}

func remotePatchPaths(changes []patchapply.FileChange) []string {
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(changes)*2)
	for _, change := range changes {
		for _, candidate := range []string{change.OldPath, change.NewPath} {
			if candidate == "" {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			paths = append(paths, candidate)
		}
	}
	return paths
}

func (c *LocalController) loadRemotePatchCapabilities(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, currentShell *shell.PromptContext) (remotePatchCapabilities, string, error) {
	c.mu.Lock()
	currentLocation := c.session.CurrentShellLocation
	c.mu.Unlock()
	if c.remoteCaps != nil {
		if record, ok := c.remoteCaps.recordForShell(currentLocation, currentShell); ok {
			return capabilitiesFromRecord(record), "cached", validateRemotePatchCapabilities(capabilitiesFromRecord(record))
		}
	}
	caps, err := probeRemotePatchCapabilities(ctx, runner, trackedShell)
	if err != nil {
		return remotePatchCapabilities{}, "", err
	}
	if c.remoteCaps != nil && isRemoteShellLocation(currentLocation, currentShell) {
		c.storeRemotePatchCapabilities(currentLocation, currentShell, caps)
	}
	return caps, "probed", validateRemotePatchCapabilities(caps)
}

func (c *LocalController) storeRemotePatchCapabilities(currentLocation *shell.ShellLocation, currentShell *shell.PromptContext, caps remotePatchCapabilities) {
	key := remoteCapabilityKeyForShell(currentLocation, currentShell)
	if c.remoteCaps == nil || key == "" {
		return
	}
	user := ""
	host := ""
	if currentLocation != nil {
		user = strings.TrimSpace(currentLocation.User)
		host = strings.TrimSpace(currentLocation.Host)
	}
	if user == "" && currentShell != nil {
		user = strings.TrimSpace(currentShell.User)
	}
	if host == "" && currentShell != nil {
		host = strings.TrimSpace(currentShell.Host)
	}
	c.remoteCaps.saveRecord(remoteCapabilityRecord{
		Key:                key,
		User:               user,
		Host:               host,
		TargetKind:         "remote_shell",
		System:             caps.System,
		OSRelease:          caps.OSRelease,
		ShellFamily:        caps.ShellFamily,
		Git:                caps.Git,
		Python3:            caps.Python3,
		Base64:             caps.Base64,
		Mktemp:             caps.Mktemp,
		LastProbeSucceeded: true,
		LastValidated:      time.Now().UTC(),
	})
	c.mu.Lock()
	c.refreshRemoteCapabilityHintLocked()
	c.mu.Unlock()
}

func capabilitiesFromRecord(record remoteCapabilityRecord) remotePatchCapabilities {
	return remotePatchCapabilities{
		Git:         record.Git,
		Python3:     record.Python3,
		Base64:      record.Base64,
		Mktemp:      record.Mktemp,
		ShellFamily: record.ShellFamily,
		System:      record.System,
		OSRelease:   record.OSRelease,
	}
}

func validateRemotePatchCapabilities(caps remotePatchCapabilities) error {
	if caps.Python3 || caps.Base64 {
		return nil
	}
	return errors.New("remote patching requires python3 or base64 to read remote file snapshots")
}

func (c *LocalController) resolveRemotePatchCapabilities(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, currentShell *shell.PromptContext) (remotePatchCapabilities, string, error) {
	caps, source, err := c.loadRemotePatchCapabilities(ctx, runner, trackedShell, currentShell)
	if err != nil {
		return remotePatchCapabilities{}, source, err
	}
	if source == "cached" && (!caps.Git || !caps.Python3) {
		probed, probeErr := probeRemotePatchCapabilities(ctx, runner, trackedShell)
		if probeErr == nil {
			caps = probed
			source = "reprobed"
			c.mu.Lock()
			currentLocation := c.session.CurrentShellLocation
			c.mu.Unlock()
			c.storeRemotePatchCapabilities(currentLocation, currentShell, caps)
		}
	}
	return caps, source, validateRemotePatchCapabilities(caps)
}

func probeRemotePatchCapabilities(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget) (remotePatchCapabilities, error) {
	command := strings.Join([]string{
		"printf 'git=%s\\n' \"$(command -v git >/dev/null 2>&1 && echo 1 || echo 0)\"",
		"printf 'python3=%s\\n' \"$(command -v python3 >/dev/null 2>&1 && echo 1 || echo 0)\"",
		"printf 'base64=%s\\n' \"$(command -v base64 >/dev/null 2>&1 && echo 1 || echo 0)\"",
		"printf 'mktemp=%s\\n' \"$(command -v mktemp >/dev/null 2>&1 && echo 1 || echo 0)\"",
		"printf 'shell=%s\\n' \"${SHELL##*/}\"",
		"printf 'system=%s\\n' \"$(uname -srm 2>/dev/null | tr '\\n' ' ')\"",
		"if [ -r /etc/os-release ]; then . /etc/os-release; printf 'os_release=%s %s\\n' \"$ID\" \"$VERSION_ID\"; else printf 'os_release=\\n'; fi",
	}, "; ")
	result, err := runRemotePatchCommand(ctx, runner, trackedShell, command, "probe remote patch capabilities", true)
	if err != nil {
		return remotePatchCapabilities{}, err
	}
	caps := remotePatchCapabilities{}
	for _, line := range strings.Split(strings.TrimSpace(result.Captured), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "git":
			caps.Git = value == "1"
		case "python3":
			caps.Python3 = value == "1"
		case "base64":
			caps.Base64 = value == "1"
		case "mktemp":
			caps.Mktemp = value == "1"
		case "shell":
			caps.ShellFamily = strings.TrimSpace(value)
		case "system":
			caps.System = strings.TrimSpace(value)
		case "os_release":
			caps.OSRelease = strings.TrimSpace(value)
		}
	}
	return caps, nil
}

func (c *LocalController) selectRemotePatchTransport(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, currentShell *shell.PromptContext, caps remotePatchCapabilities, source string, patch string) (PatchTransport, remotePatchCapabilities, string, error) {
	if caps.Git {
		ok, err := canUseRemoteGitTransport(ctx, runner, trackedShell, caps, patch)
		if err != nil {
			if source == "cached" && looksLikeRemoteCapabilityContradiction(err) {
				if reprobed, probeErr := probeRemotePatchCapabilities(ctx, runner, trackedShell); probeErr == nil {
					caps = reprobed
					source = "reprobed"
					c.mu.Lock()
					currentLocation := c.session.CurrentShellLocation
					c.mu.Unlock()
					c.storeRemotePatchCapabilities(currentLocation, currentShell, caps)
					ok, err = canUseRemoteGitTransport(ctx, runner, trackedShell, caps, patch)
				}
			}
			if err != nil {
				return PatchTransportNone, remotePatchCapabilities{}, source, err
			}
		}
		if ok {
			return PatchTransportGit, caps, source, nil
		}
	}
	if caps.Python3 {
		return PatchTransportPython, caps, source, nil
	}
	if caps.Base64 && caps.Mktemp {
		return PatchTransportShell, caps, source, nil
	}
	return PatchTransportNone, remotePatchCapabilities{}, source, errors.New("remote patching requires git in the current repo, python3, or both base64 and mktemp for shell fallback")
}

func canUseRemoteGitTransport(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, caps remotePatchCapabilities, patch string) (bool, error) {
	if !caps.Git {
		return false, nil
	}
	repoCheck, err := runRemotePatchCommand(ctx, runner, trackedShell, "git rev-parse --is-inside-work-tree >/dev/null 2>&1", "check remote git worktree", false)
	if err != nil {
		return false, err
	}
	if repoCheck.ExitCode != 0 {
		return false, nil
	}
	result, err := runRemoteGitApply(ctx, runner, trackedShell, caps, patch, "--check", "--verbose")
	if err != nil {
		return false, err
	}
	return result.ExitCode == 0, nil
}

func (c *LocalController) readRemoteFile(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, caps remotePatchCapabilities, rel string) (remoteFileSnapshot, error) {
	if caps.Python3 {
		return readRemoteFilePython(ctx, runner, trackedShell, rel)
	}
	if caps.Base64 {
		return readRemoteFileShell(ctx, runner, trackedShell, rel)
	}
	return remoteFileSnapshot{}, fmt.Errorf("read remote file %q: no supported remote snapshot transport", rel)
}

func readRemoteFilePython(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, rel string) (remoteFileSnapshot, error) {
	command := "python3 - <<'PY'\n" +
		"import base64, os, stat, sys\n" +
		"path = " + pythonStringLiteral(rel) + "\n" +
		"if not os.path.exists(path):\n" +
		"    print('" + remoteReadMarker + " missing')\n" +
		"elif not os.path.isfile(path):\n" +
		"    print('" + remoteReadMarker + " error not_a_regular_file')\n" +
		"else:\n" +
		"    data = open(path, 'rb').read()\n" +
		"    mode = stat.S_IMODE(os.stat(path).st_mode)\n" +
		"    print('" + remoteReadMarker + " exists %d' % mode)\n" +
		"    print('" + remoteReadDataBegin + "')\n" +
		"    sys.stdout.write(base64.encodebytes(data).decode('ascii'))\n" +
		"    if len(data) == 0 or not base64.encodebytes(data).decode('ascii').endswith('\\n'):\n" +
		"        sys.stdout.write('\\n')\n" +
		"    print('" + remoteReadDataEnd + "')\n" +
		"PY"
	result, err := runRemotePatchCommand(ctx, runner, trackedShell, command, fmt.Sprintf("read remote file %q", rel), true)
	if err != nil {
		return remoteFileSnapshot{}, err
	}
	payload, err := parseRemoteReadPayload(result.Captured)
	if err != nil {
		return remoteFileSnapshot{}, fmt.Errorf("decode remote file payload for %q: %w", rel, err)
	}
	if strings.TrimSpace(payload.Error) != "" {
		return remoteFileSnapshot{}, fmt.Errorf("read remote file %q: %s", rel, strings.TrimSpace(payload.Error))
	}
	if !payload.Exists {
		return remoteFileSnapshot{}, nil
	}
	data, err := base64.StdEncoding.DecodeString(payload.Data)
	if err != nil {
		return remoteFileSnapshot{}, fmt.Errorf("decode remote file data for %q: %w", rel, err)
	}
	mode := fs.FileMode(payload.Mode)
	if mode == 0 {
		mode = 0o644
	}
	return remoteFileSnapshot{Exists: true, Mode: mode, Data: data}, nil
}

func readRemoteFileShell(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, rel string) (remoteFileSnapshot, error) {
	quoted := shellQuote(rel)
	command := "if [ -e " + quoted + " ]; then " +
		"if [ ! -f " + quoted + " ]; then printf '" + remoteReadMarker + " error not_a_regular_file\\n'; exit 0; fi; " +
		"mode_raw=$(stat -c '%a' " + quoted + " 2>/dev/null || stat -f '%Lp' " + quoted + " 2>/dev/null || printf '644'); " +
		"mode_dec=$(printf '%d' \"0${mode_raw}\" 2>/dev/null || printf '420'); " +
		"printf '" + remoteReadMarker + " exists %s\\n' \"$mode_dec\"; printf '" + remoteReadDataBegin + "\\n'; base64 < " + quoted + "; printf '\\n" + remoteReadDataEnd + "\\n'; " +
		"else printf '" + remoteReadMarker + " missing\\n'; fi"
	result, err := runRemotePatchCommand(ctx, runner, trackedShell, command, fmt.Sprintf("read remote file %q", rel), true)
	if err != nil {
		return remoteFileSnapshot{}, err
	}
	payload, err := parseRemoteReadPayload(result.Captured)
	if err != nil {
		return remoteFileSnapshot{}, fmt.Errorf("decode remote file payload for %q: %w", rel, err)
	}
	if strings.TrimSpace(payload.Error) != "" {
		return remoteFileSnapshot{}, fmt.Errorf("read remote file %q: %s", rel, strings.TrimSpace(payload.Error))
	}
	if !payload.Exists {
		return remoteFileSnapshot{}, nil
	}
	data, err := base64.StdEncoding.DecodeString(payload.Data)
	if err != nil {
		return remoteFileSnapshot{}, fmt.Errorf("decode remote file data for %q: %w", rel, err)
	}
	mode := fs.FileMode(payload.Mode)
	if mode == 0 {
		mode = 0o644
	}
	return remoteFileSnapshot{Exists: true, Mode: mode, Data: data}, nil
}

func parseRemoteReadPayload(captured string) (remoteReadPayload, error) {
	trimmed := strings.TrimSpace(captured)
	if trimmed == "" {
		return remoteReadPayload{}, errors.New("empty response")
	}

	if strings.HasPrefix(trimmed, "{") {
		var payload remoteReadPayload
		if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
			return remoteReadPayload{}, err
		}
		return payload, nil
	}

	lines := strings.Split(trimmed, "\n")
	metaIndex := -1
	for i, raw := range lines {
		if strings.HasPrefix(strings.TrimSpace(raw), remoteReadMarker) {
			metaIndex = i
			break
		}
	}
	if metaIndex < 0 {
		return remoteReadPayload{}, fmt.Errorf("missing %s marker", remoteReadMarker)
	}

	metaFields := strings.Fields(strings.TrimSpace(lines[metaIndex]))
	if len(metaFields) < 2 {
		return remoteReadPayload{}, fmt.Errorf("invalid %s marker", remoteReadMarker)
	}

	switch metaFields[1] {
	case "missing":
		return remoteReadPayload{Exists: false}, nil
	case "error":
		message := strings.TrimSpace(strings.Join(metaFields[2:], " "))
		if message == "" {
			message = "remote read failed"
		}
		message = strings.ReplaceAll(message, "_", " ")
		return remoteReadPayload{Exists: true, Error: message}, nil
	case "exists":
		payload := remoteReadPayload{Exists: true}
		if len(metaFields) >= 3 {
			mode, err := strconv.ParseUint(strings.TrimSpace(metaFields[2]), 10, 32)
			if err != nil {
				return remoteReadPayload{}, fmt.Errorf("invalid file mode %q", metaFields[2])
			}
			payload.Mode = uint32(mode)
		}
		beginIndex := -1
		endIndex := -1
		for i := metaIndex + 1; i < len(lines); i++ {
			line := strings.TrimSpace(lines[i])
			if line == remoteReadDataBegin {
				beginIndex = i
				continue
			}
			if line == remoteReadDataEnd {
				endIndex = i
				break
			}
		}
		if beginIndex < 0 || endIndex < 0 || endIndex < beginIndex {
			return remoteReadPayload{}, fmt.Errorf("missing remote data markers")
		}
		var builder strings.Builder
		for _, raw := range lines[beginIndex+1 : endIndex] {
			builder.WriteString(strings.TrimSpace(raw))
		}
		payload.Data = builder.String()
		return payload, nil
	default:
		return remoteReadPayload{}, fmt.Errorf("unknown %s state %q", remoteReadMarker, metaFields[1])
	}
}

func (c *LocalController) commitRemotePatchResult(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, caps remotePatchCapabilities, tempDir string, files []patchapply.FileChange, patch string, transport PatchTransport) error {
	switch transport {
	case PatchTransportGit:
		return applyRemoteGitPatch(ctx, runner, trackedShell, caps, patch)
	case PatchTransportPython, PatchTransportShell:
		for _, change := range files {
			switch change.Operation {
			case patchapply.OperationDelete:
				if err := removeRemoteFile(ctx, runner, trackedShell, transport, change.OldPath); err != nil {
					return err
				}
			case patchapply.OperationCreate, patchapply.OperationUpdate:
				data, mode, err := readStagedRemoteFile(tempDir, change.NewPath)
				if err != nil {
					return err
				}
				if err := writeRemoteFile(ctx, runner, trackedShell, transport, change.NewPath, data, mode); err != nil {
					return err
				}
			case patchapply.OperationRename:
				data, mode, err := readStagedRemoteFile(tempDir, change.NewPath)
				if err != nil {
					return err
				}
				if err := writeRemoteFile(ctx, runner, trackedShell, transport, change.NewPath, data, mode); err != nil {
					return err
				}
				if err := removeRemoteFile(ctx, runner, trackedShell, transport, change.OldPath); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupported remote patch operation %q", change.Operation)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported remote patch transport %q", transport)
	}
}

func readStagedRemoteFile(root string, rel string) ([]byte, fs.FileMode, error) {
	abs := filepath.Join(root, rel)
	info, err := os.Stat(abs)
	if err != nil {
		return nil, 0, fmt.Errorf("read staged remote file %q: %w", rel, err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, 0, fmt.Errorf("read staged remote file %q: %w", rel, err)
	}
	return data, info.Mode().Perm(), nil
}

func (c *LocalController) verifyRemotePatchState(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, caps remotePatchCapabilities, tempDir string, files []patchapply.FileChange) error {
	for _, change := range files {
		switch change.Operation {
		case patchapply.OperationDelete:
			snapshot, err := c.readRemoteFile(ctx, runner, trackedShell, caps, change.OldPath)
			if err != nil {
				return err
			}
			if snapshot.Exists {
				return fmt.Errorf("verify remote file %q: file still exists after delete", change.OldPath)
			}
		case patchapply.OperationCreate, patchapply.OperationUpdate, patchapply.OperationRename:
			expected, expectedMode, err := readStagedRemoteFile(tempDir, change.NewPath)
			if err != nil {
				return err
			}
			snapshot, err := c.readRemoteFile(ctx, runner, trackedShell, caps, change.NewPath)
			if err != nil {
				return err
			}
			if !snapshot.Exists {
				return fmt.Errorf("verify remote file %q: file is missing after apply", change.NewPath)
			}
			if string(snapshot.Data) != string(expected) {
				return fmt.Errorf("verify remote file %q: file contents do not match staged patch result", change.NewPath)
			}
			if snapshot.Mode.Perm() != expectedMode.Perm() {
				return fmt.Errorf("verify remote file %q: file mode %03o does not match expected mode %03o", change.NewPath, snapshot.Mode.Perm(), expectedMode.Perm())
			}
			if change.Operation == patchapply.OperationRename {
				oldSnapshot, err := c.readRemoteFile(ctx, runner, trackedShell, caps, change.OldPath)
				if err != nil {
					return err
				}
				if oldSnapshot.Exists {
					return fmt.Errorf("verify remote file %q: old path still exists after rename", change.OldPath)
				}
			}
		}
	}
	return nil
}

func writeRemoteFile(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, transport PatchTransport, rel string, data []byte, mode fs.FileMode) error {
	switch transport {
	case PatchTransportPython:
		return writeRemoteFilePython(ctx, runner, trackedShell, rel, data, mode)
	case PatchTransportShell:
		return writeRemoteFileShell(ctx, runner, trackedShell, rel, data, mode)
	default:
		return fmt.Errorf("write remote file %q: unsupported transport %q", rel, transport)
	}
}

func writeRemoteFilePython(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, rel string, data []byte, mode fs.FileMode) error {
	payloadPath, cleanup, err := stageRemotePayload(ctx, runner, trackedShell, remotePatchCapabilities{Python3: true, Base64: true, Mktemp: true}, data, ".b64", "stage remote python payload")
	if err != nil {
		return err
	}
	defer cleanup()
	command := "python3 - <<'PY'\n" +
		"import base64, os, shutil, stat, tempfile\n" +
		"path = " + pythonStringLiteral(rel) + "\n" +
		"payload_path = " + pythonStringLiteral(payloadPath) + "\n" +
		"with open(payload_path, 'rb') as payload_handle:\n" +
		"    data = base64.b64decode(payload_handle.read())\n" +
		"mode = " + fmt.Sprintf("%d", mode) + "\n" +
		"dirname = os.path.dirname(path) or '.'\n" +
		"os.makedirs(dirname, exist_ok=True)\n" +
		"backup = path + '.shuttle.bak'\n" +
		"tmp_fd, tmp_path = tempfile.mkstemp(prefix='.shuttle-patch-', dir=dirname)\n" +
		"os.close(tmp_fd)\n" +
		"had_original = os.path.exists(path)\n" +
		"try:\n" +
		"    if had_original:\n" +
		"        shutil.copy2(path, backup)\n" +
		"    with open(tmp_path, 'wb') as handle:\n" +
		"        handle.write(data)\n" +
		"    os.chmod(tmp_path, mode)\n" +
		"    with open(tmp_path, 'rb') as handle:\n" +
		"        if handle.read() != data:\n" +
		"            raise RuntimeError('temp verification failed')\n" +
		"    os.replace(tmp_path, path)\n" +
		"    with open(path, 'rb') as handle:\n" +
		"        if handle.read() != data:\n" +
		"            raise RuntimeError('final verification failed')\n" +
		"    if stat.S_IMODE(os.stat(path).st_mode) != mode:\n" +
		"        raise RuntimeError('final mode verification failed')\n" +
		"    if os.path.exists(backup):\n" +
		"        os.remove(backup)\n" +
		"except Exception:\n" +
		"    if os.path.exists(tmp_path):\n" +
		"        os.remove(tmp_path)\n" +
		"    if os.path.exists(backup):\n" +
		"        os.replace(backup, path)\n" +
		"    raise\n" +
		"finally:\n" +
		"    if os.path.exists(payload_path):\n" +
		"        os.remove(payload_path)\n" +
		"PY"
	_, err = runRemotePatchCommand(ctx, runner, trackedShell, command, fmt.Sprintf("write remote file %q", rel), true)
	return err
}

func writeRemoteFileShell(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, rel string, data []byte, mode fs.FileMode) error {
	payloadPath, cleanup, err := stageRemotePayload(ctx, runner, trackedShell, remotePatchCapabilities{Base64: true, Mktemp: true}, data, ".b64", "stage remote shell payload")
	if err != nil {
		return err
	}
	defer cleanup()
	path := shellQuote(rel)
	command := "set -eu\n" +
		"path=" + path + "\n" +
		"dir=$(dirname \"$path\")\n" +
		"mkdir -p \"$dir\"\n" +
		"tmp=$(mktemp \"$dir/.shuttle-patch.XXXXXX\")\n" +
		"bak=\"$path.shuttle.bak\"\n" +
		"payload=" + shellQuote(payloadPath) + "\n" +
		"cleanup() { rm -f \"$tmp\" \"$payload\"; }\n" +
		"trap cleanup EXIT\n" +
		"if [ -e \"$path\" ]; then cp \"$path\" \"$bak\"; fi\n" +
		"base64 -d \"$payload\" > \"$tmp\"\n" +
		"chmod " + fmt.Sprintf("%03o", mode.Perm()) + " \"$tmp\"\n" +
		"test \"$(wc -c < \"$tmp\" | tr -d ' ')\" = \"" + fmt.Sprintf("%d", len(data)) + "\"\n" +
		"mv \"$tmp\" \"$path\"\n" +
		"mode_raw=$(stat -c '%a' \"$path\" 2>/dev/null || stat -f '%Lp' \"$path\" 2>/dev/null || printf '000')\n" +
		"test \"$(wc -c < \"$path\" | tr -d ' ')\" = \"" + fmt.Sprintf("%d", len(data)) + "\" || { [ -e \"$bak\" ] && mv \"$bak\" \"$path\"; exit 19; }\n" +
		"test \"$mode_raw\" = \"" + fmt.Sprintf("%03o", mode.Perm()) + "\" || { [ -e \"$bak\" ] && mv \"$bak\" \"$path\"; exit 19; }\n" +
		"rm -f \"$bak\""
	_, err = runRemotePatchCommand(ctx, runner, trackedShell, command, fmt.Sprintf("write remote file %q", rel), true)
	return err
}

func removeRemoteFile(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, transport PatchTransport, rel string) error {
	switch transport {
	case PatchTransportPython:
		command := "python3 - <<'PY'\n" +
			"import os, shutil\n" +
			"path = " + pythonStringLiteral(rel) + "\n" +
			"backup = path + '.shuttle.bak'\n" +
			"if os.path.exists(path):\n" +
			"    if not os.path.isfile(path):\n" +
			"        raise RuntimeError('not a regular file')\n" +
			"    shutil.copy2(path, backup)\n" +
			"    os.remove(path)\n" +
			"    if os.path.exists(path):\n" +
			"        os.replace(backup, path)\n" +
			"        raise RuntimeError('delete verification failed')\n" +
			"    os.remove(backup)\n" +
			"PY"
		_, err := runRemotePatchCommand(ctx, runner, trackedShell, command, fmt.Sprintf("remove remote file %q", rel), true)
		return err
	case PatchTransportShell:
		command := "path=" + shellQuote(rel) + "\n" +
			"bak=\"$path.shuttle.bak\"\n" +
			"if [ -e \"$path\" ]; then cp \"$path\" \"$bak\"; rm -f \"$path\"; test ! -e \"$path\" || { mv \"$bak\" \"$path\"; exit 19; }; rm -f \"$bak\"; fi"
		_, err := runRemotePatchCommand(ctx, runner, trackedShell, command, fmt.Sprintf("remove remote file %q", rel), true)
		return err
	default:
		return fmt.Errorf("remove remote file %q: unsupported transport %q", rel, transport)
	}
}

func applyRemoteGitPatch(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, caps remotePatchCapabilities, patch string) error {
	result, err := runRemoteGitApply(ctx, runner, trackedShell, caps, patch, "--whitespace=nowarn")
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("apply remote git patch: %s", remotePatchExitMessage(result, "git apply exited non-zero"))
	}
	return nil
}

func reverseRemoteGitPatch(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, caps remotePatchCapabilities, patch string) error {
	result, err := runRemoteGitApply(ctx, runner, trackedShell, caps, patch, "-R", "--whitespace=nowarn")
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("reverse remote git patch: %s", remotePatchExitMessage(result, "git apply -R exited non-zero"))
	}
	return nil
}

func runRemoteGitApply(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, caps remotePatchCapabilities, patch string, args ...string) (shell.TrackedExecution, error) {
	command, cleanup, err := gitApplyCommand(ctx, runner, trackedShell, caps, patch, args...)
	if err != nil {
		return shell.TrackedExecution{}, err
	}
	defer cleanup()
	return runRemotePatchCommand(ctx, runner, trackedShell, command, "run remote git apply", false)
}

func gitApplyCommand(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, caps remotePatchCapabilities, patch string, args ...string) (string, func(), error) {
	command := "git apply " + strings.Join(args, " ")
	payloadPath, cleanup, err := stageRemotePayload(ctx, runner, trackedShell, caps, []byte(patch), ".diff.b64", "stage remote git patch")
	if err != nil {
		return "", nil, err
	}
	diffPath, diffCleanup, err := createRemoteTempPath(ctx, runner, trackedShell, caps, ".diff", "create remote git diff path")
	if err != nil {
		cleanup()
		return "", nil, err
	}
	finalCleanup := func() {
		diffCleanup()
		cleanup()
	}
	decodeCommand, err := decodeRemotePayloadCommand(caps, payloadPath, diffPath, len([]byte(patch)))
	if err != nil {
		finalCleanup()
		return "", nil, err
	}
	return decodeCommand + "\n" + command + " " + shellQuote(diffPath), finalCleanup, nil
}

func runRemotePatchCommand(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, command string, action string, requireZeroExit bool) (shell.TrackedExecution, error) {
	result, err := runner.RunTrackedCommand(ctx, trackedShell.PaneID, command, remotePatchCommandTimeout)
	if err != nil {
		return shell.TrackedExecution{}, fmt.Errorf("%s: %w", action, err)
	}
	if requireZeroExit && result.ExitCode != 0 {
		return shell.TrackedExecution{}, fmt.Errorf("%s: %s", action, remotePatchExitMessage(result, "remote command exited non-zero"))
	}
	return result, nil
}

func remotePatchExitMessage(result shell.TrackedExecution, fallback string) string {
	if output := strings.TrimSpace(result.Captured); output != "" {
		return clipRemotePatchError(output)
	}
	if result.ExitCode != 0 {
		return "exit code " + strconv.Itoa(result.ExitCode)
	}
	return fallback
}

func clipRemotePatchError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 240 {
		return value
	}
	return value[:240] + "...(truncated)"
}

func pythonStringLiteral(value string) string {
	return fmt.Sprintf("%q", value)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func looksLikeRemoteCapabilityContradiction(err error) bool {
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "command not found") || strings.Contains(text, "not found") || strings.Contains(text, "no such file or directory")
}

func stageRemotePayload(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, caps remotePatchCapabilities, payload []byte, suffix string, action string) (string, func(), error) {
	path, cleanup, err := createRemoteTempPath(ctx, runner, trackedShell, caps, suffix, action)
	if err != nil {
		return "", nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(payload)
	if _, err := runRemotePatchCommand(ctx, runner, trackedShell, ": > "+shellQuote(path), action+": initialize payload file", true); err != nil {
		cleanup()
		return "", nil, err
	}
	for offset := 0; offset < len(encoded); offset += remotePatchPayloadChunkSize {
		end := offset + remotePatchPayloadChunkSize
		if end > len(encoded) {
			end = len(encoded)
		}
		chunk := encoded[offset:end]
		if _, err := runRemotePatchCommand(ctx, runner, trackedShell, "printf '%s' "+shellQuote(chunk)+" >> "+shellQuote(path), action+": append payload chunk", true); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	verify := "test \"$(wc -c < " + shellQuote(path) + " | tr -d ' ')\" = \"" + strconv.Itoa(len(encoded)) + "\""
	if _, err := runRemotePatchCommand(ctx, runner, trackedShell, verify, action+": verify staged payload", true); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func createRemoteTempPath(ctx context.Context, runner ShellRunner, trackedShell TrackedShellTarget, caps remotePatchCapabilities, suffix string, action string) (string, func(), error) {
	var command string
	if caps.Python3 {
		command = "python3 - <<'PY'\n" +
			"import os, tempfile\n" +
			"fd, path = tempfile.mkstemp(prefix='.shuttle-remote-', suffix=" + pythonStringLiteral(suffix) + ")\n" +
			"os.close(fd)\n" +
			"print(path)\n" +
			"PY"
	} else if caps.Mktemp {
		command = "mktemp \"${TMPDIR:-/tmp}/.shuttle-remote.XXXXXX" + suffix + "\""
	} else {
		return "", nil, errors.New("remote payload staging requires python3 or mktemp")
	}
	result, err := runRemotePatchCommand(ctx, runner, trackedShell, command, action, true)
	if err != nil {
		return "", nil, err
	}
	path := strings.TrimSpace(result.Captured)
	if path == "" {
		return "", nil, fmt.Errorf("%s: empty remote temp path", action)
	}
	cleanup := func() {
		_, _ = runRemotePatchCommand(context.Background(), runner, trackedShell, "rm -f "+shellQuote(path), action+": cleanup remote temp path", false)
	}
	return path, cleanup, nil
}

func decodeRemotePayloadCommand(caps remotePatchCapabilities, payloadPath string, rawPath string, decodedLen int) (string, error) {
	if caps.Python3 {
		return "python3 - <<'PY'\n" +
			"import base64\n" +
			"payload_path = " + pythonStringLiteral(payloadPath) + "\n" +
			"raw_path = " + pythonStringLiteral(rawPath) + "\n" +
			"expected = " + strconv.Itoa(decodedLen) + "\n" +
			"with open(payload_path, 'rb') as payload_handle:\n" +
			"    data = base64.b64decode(payload_handle.read())\n" +
			"with open(raw_path, 'wb') as raw_handle:\n" +
			"    raw_handle.write(data)\n" +
			"if len(data) != expected:\n" +
			"    raise RuntimeError('decoded payload size mismatch')\n" +
			"PY", nil
	}
	if caps.Base64 {
		return "base64 -d " + shellQuote(payloadPath) + " > " + shellQuote(rawPath) + "\n" +
			"test \"$(wc -c < " + shellQuote(rawPath) + " | tr -d ' ')\" = \"" + strconv.Itoa(decodedLen) + "\"", nil
	}
	return "", errors.New("remote payload decoding requires python3 or base64")
}

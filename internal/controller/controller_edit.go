package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (c *LocalController) synthesizeStructuredEditResponse(ctx context.Context, response AgentResponse) (AgentResponse, error) {
	if response.Proposal == nil || response.Proposal.Kind != ProposalEdit || response.Proposal.Edit == nil {
		return response, nil
	}

	proposal, note, err := c.proposalFromStructuredEdit(ctx, *response.Proposal)
	if err != nil {
		return response, err
	}
	response.Proposal = &proposal
	if strings.TrimSpace(note) != "" {
		if strings.TrimSpace(response.Message) == "" {
			response.Message = strings.TrimSpace(note)
		} else {
			response.Message = strings.TrimSpace(response.Message) + "\n\n" + strings.TrimSpace(note)
		}
	}
	return response, nil
}

func (c *LocalController) proposalFromStructuredEdit(ctx context.Context, original Proposal) (Proposal, string, error) {
	intent := original.Edit
	if intent == nil {
		return Proposal{}, "Structured edit request was missing edit details.", nil
	}

	target := intent.Target
	if target == "" {
		target = original.PatchTarget
	}
	if target == "" {
		target = PatchTargetLocalWorkspace
	}

	relPath, err := sanitizeStructuredEditPath(intent.Path)
	if err != nil {
		return Proposal{}, "Structured edit path was invalid: " + err.Error(), nil
	}

	snapshot, err := c.readStructuredEditSnapshot(ctx, target, relPath)
	if err != nil {
		return Proposal{}, "", err
	}
	if !snapshot.Exists {
		return Proposal{}, fmt.Sprintf("Structured edit could not continue because %q does not exist on %s.", relPath, structuredEditTargetLabel(target)), nil
	}

	before := string(snapshot.Data)
	after, editErr := applyStructuredEdit(before, *intent)
	if editErr != nil {
		inspect := Proposal{
			Kind:        ProposalCommand,
			Command:     c.structuredEditInspectionCommand(target, relPath),
			Description: "Inspect the current file contents before retrying the edit.",
		}
		return inspect, "Structured edit synthesis could not find a unique insertion/replacement point: " + editErr.Error(), nil
	}
	if before == after {
		return Proposal{}, "Structured edit produced no file changes.", nil
	}

	patch := synthesizeSingleFileUnifiedDiff(relPath, before, after)
	if err := c.validatePatchPayload(ctx, patch, target); err != nil {
		inspect := Proposal{
			Kind:        ProposalCommand,
			Command:     c.structuredEditInspectionCommand(target, relPath),
			Description: "Inspect the current file contents before retrying the edit.",
		}
		return inspect, "Structured edit synthesis produced a patch that did not validate cleanly: " + err.Error(), nil
	}

	return Proposal{
		Kind:        ProposalPatch,
		Patch:       patch,
		PatchTarget: target,
		Description: original.Description,
	}, "", nil
}

func structuredEditTargetLabel(target PatchTarget) string {
	if target == PatchTargetRemoteShell {
		return "the remote shell target"
	}
	return "the local workspace"
}

func sanitizeStructuredEditPath(path string) (string, error) {
	return sanitizeRemotePatchPath(path)
}

func (c *LocalController) readStructuredEditSnapshot(ctx context.Context, target PatchTarget, relPath string) (remoteFileSnapshot, error) {
	if target == PatchTargetRemoteShell {
		trackedShell := c.syncTrackedShellTarget(ctx)
		c.refreshUserShellContextForTarget(ctx, trackedShell, false)

		c.mu.Lock()
		runner := c.runner
		currentShell := c.session.CurrentShell
		currentLocation := c.session.CurrentShellLocation
		c.mu.Unlock()
		if runner == nil {
			return remoteFileSnapshot{}, fmt.Errorf("remote structured edit runner is not configured")
		}
		if !isRemoteShellLocation(currentLocation, currentShell) {
			return remoteFileSnapshot{}, fmt.Errorf("remote structured edit target is ambiguous or not currently active")
		}
		caps, _, err := c.resolveRemotePatchCapabilities(ctx, runner, trackedShell, currentShell)
		if err != nil {
			return remoteFileSnapshot{}, err
		}
		return c.readRemoteFile(ctx, runner, trackedShell, caps, relPath)
	}

	c.mu.Lock()
	root := c.session.LocalWorkspaceRoot
	c.mu.Unlock()
	if strings.TrimSpace(root) == "" {
		return remoteFileSnapshot{}, fmt.Errorf("local workspace root is not configured")
	}
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return remoteFileSnapshot{}, nil
		}
		return remoteFileSnapshot{}, fmt.Errorf("read local file %q: %w", relPath, err)
	}
	if !info.Mode().IsRegular() {
		return remoteFileSnapshot{}, fmt.Errorf("read local file %q: not a regular file", relPath)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return remoteFileSnapshot{}, fmt.Errorf("read local file %q: %w", relPath, err)
	}
	return remoteFileSnapshot{Exists: true, Mode: info.Mode().Perm(), Data: data}, nil
}

func applyStructuredEdit(before string, intent EditIntent) (string, error) {
	switch intent.Operation {
	case EditInsertBefore:
		index, err := findUniqueEditAnchor(before, intent.AnchorText)
		if err != nil {
			return "", err
		}
		return before[:index] + intent.NewText + before[index:], nil
	case EditInsertAfter:
		index, err := findUniqueEditAnchor(before, intent.AnchorText)
		if err != nil {
			return "", err
		}
		index += len(intent.AnchorText)
		if !strings.HasSuffix(intent.AnchorText, "\n") && index < len(before) && before[index] == '\n' {
			index++
		}
		return before[:index] + intent.NewText + before[index:], nil
	case EditReplaceExact:
		index, err := findUniqueEditAnchor(before, intent.OldText)
		if err != nil {
			return "", err
		}
		return before[:index] + intent.NewText + before[index+len(intent.OldText):], nil
	case EditReplaceRange:
		return replaceStructuredEditRange(before, intent.StartLine, intent.EndLine, intent.NewText)
	default:
		return "", fmt.Errorf("unsupported edit operation %q", intent.Operation)
	}
}

func findUniqueEditAnchor(body string, anchor string) (int, error) {
	if anchor == "" {
		return 0, fmt.Errorf("anchor text is empty")
	}
	first := strings.Index(body, anchor)
	if first < 0 {
		return 0, fmt.Errorf("anchor text was not found")
	}
	if second := strings.Index(body[first+len(anchor):], anchor); second >= 0 {
		return 0, fmt.Errorf("anchor text matched more than once")
	}
	return first, nil
}

func replaceStructuredEditRange(body string, startLine int, endLine int, newText string) (string, error) {
	lines := splitStructuredEditLines(body)
	if startLine <= 0 || endLine < startLine {
		return "", fmt.Errorf("line range %d-%d is invalid", startLine, endLine)
	}
	if endLine > len(lines) {
		return "", fmt.Errorf("line range %d-%d is outside the file", startLine, endLine)
	}
	return strings.Join(lines[:startLine-1], "") + newText + strings.Join(lines[endLine:], ""), nil
}

func splitStructuredEditLines(body string) []string {
	if body == "" {
		return nil
	}
	lines := strings.SplitAfter(body, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func synthesizeSingleFileUnifiedDiff(relPath string, before string, after string) string {
	path := filepath.ToSlash(relPath)
	oldLines := splitDiffBodyLines(before)
	newLines := splitDiffBodyLines(after)

	var builder strings.Builder
	builder.WriteString("diff --git a/")
	builder.WriteString(path)
	builder.WriteString(" b/")
	builder.WriteString(path)
	builder.WriteString("\n--- a/")
	builder.WriteString(path)
	builder.WriteString("\n+++ b/")
	builder.WriteString(path)
	builder.WriteString("\n@@ -")
	builder.WriteString(unifiedRange(1, len(oldLines)))
	builder.WriteString(" +")
	builder.WriteString(unifiedRange(1, len(newLines)))
	builder.WriteString(" @@\n")
	for _, line := range oldLines {
		builder.WriteString("-")
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	for _, line := range newLines {
		builder.WriteString("+")
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	return strings.TrimRight(builder.String(), "\n")
}

func splitDiffBodyLines(body string) []string {
	if body == "" {
		return nil
	}
	parts := strings.Split(body, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func unifiedRange(start int, count int) string {
	if count <= 0 {
		return "0,0"
	}
	if start <= 0 {
		start = 1
	}
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

func (c *LocalController) structuredEditInspectionCommand(target PatchTarget, relPath string) string {
	if target == PatchTargetRemoteShell {
		return "nl -ba " + shellQuote(relPath) + " | sed -n '1,200p'"
	}
	c.mu.Lock()
	root := c.session.LocalWorkspaceRoot
	c.mu.Unlock()
	if strings.TrimSpace(root) == "" {
		return "nl -ba " + shellQuote(relPath) + " | sed -n '1,200p'"
	}
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	return "nl -ba " + shellQuote(abs) + " | sed -n '1,200p'"
}

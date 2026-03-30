package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"aiterm/internal/logging"
	"aiterm/internal/patchapply"
)

func (c *LocalController) ApplyProposedPatch(ctx context.Context, patch string) ([]TranscriptEvent, error) {
	logging.Trace("controller.apply_proposed_patch")
	return c.applyPatch(ctx, patch)
}

func (c *LocalController) ContinueAfterPatchApply(ctx context.Context) ([]TranscriptEvent, error) {
	c.mu.Lock()
	lastResult := c.task.LastPatchApplyResult
	if lastResult == nil {
		errEvent := c.newEvent(EventError, TextPayload{Text: "no patch apply result available for agent continuation"})
		c.appendEvents(errEvent)
		c.mu.Unlock()
		return []TranscriptEvent{errEvent}, nil
	}
	var planEvent *TranscriptEvent
	prompt := continueAfterPatchFailurePrompt
	if lastResult.Applied {
		planEvent = c.advanceActivePlanLocked()
		if planEvent != nil {
			c.appendEvents(*planEvent)
		}
		prompt = continueAfterPatchApplyPrompt
	}
	c.mu.Unlock()

	logging.Trace("controller.continue_after_patch_apply")
	events, err := c.submitAgentTurn(ctx, "", prompt, nil, false)
	if planEvent != nil {
		events = append([]TranscriptEvent{*planEvent}, events...)
	}
	return events, err
}

func (c *LocalController) applyPatch(ctx context.Context, patch string) ([]TranscriptEvent, error) {
	c.mu.Lock()
	applier := c.patches
	patchInitErr := c.patchInitErr
	workspaceRoot := c.session.LocalWorkspaceRoot
	c.mu.Unlock()

	if err := patchInitErr; err != nil {
		return c.recordPatchApplyFailure(workspaceRoot, "patch engine is not ready: "+err.Error())
	}
	if applier == nil {
		return c.recordPatchApplyFailure(workspaceRoot, "patch engine is not configured")
	}

	result, err := applier.Apply(ctx, patch)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		if strings.TrimSpace(result.WorkspaceRoot) == "" {
			result.WorkspaceRoot = workspaceRoot
		}
		return c.recordPatchApplyFailure(result.WorkspaceRoot, err.Error())
	}

	summary := patchApplySummaryFromResult(result, true, "")

	c.mu.Lock()
	c.task.LastPatchApplyResult = &summary
	event := c.newEvent(EventPatchApplyResult, summary)
	c.appendEvents(event)
	c.mu.Unlock()

	logging.Trace(
		"controller.patch_apply.complete",
		"workspace_root", summary.WorkspaceRoot,
		"created", summary.Created,
		"updated", summary.Updated,
		"deleted", summary.Deleted,
		"renamed", summary.Renamed,
	)
	return []TranscriptEvent{event}, nil
}

func (c *LocalController) recordPatchApplyFailure(workspaceRoot string, errText string) ([]TranscriptEvent, error) {
	summary := PatchApplySummary{
		WorkspaceRoot: strings.TrimSpace(workspaceRoot),
		Applied:       false,
		Error:         strings.TrimSpace(errText),
	}

	c.mu.Lock()
	c.task.LastPatchApplyResult = &summary
	resultEvent := c.newEvent(EventPatchApplyResult, summary)
	errEvent := c.newEvent(EventError, TextPayload{Text: fmt.Sprintf("patch apply failed: %s", summary.Error)})
	c.appendEvents(resultEvent, errEvent)
	c.mu.Unlock()

	logging.Trace("controller.patch_apply.failed", "workspace_root", summary.WorkspaceRoot, "error", summary.Error)
	return []TranscriptEvent{resultEvent, errEvent}, nil
}

func patchApplySummaryFromResult(result patchapply.Result, applied bool, errText string) PatchApplySummary {
	summary := PatchApplySummary{
		WorkspaceRoot: strings.TrimSpace(result.WorkspaceRoot),
		Validation:    strings.TrimSpace(result.Validation),
		Applied:       applied,
		Created:       result.Created,
		Updated:       result.Updated,
		Deleted:       result.Deleted,
		Renamed:       result.Renamed,
		Error:         strings.TrimSpace(errText),
	}
	if len(result.Files) == 0 {
		return summary
	}

	summary.Files = make([]PatchApplyFile, 0, len(result.Files))
	for _, file := range result.Files {
		summary.Files = append(summary.Files, PatchApplyFile{
			Operation: string(file.Operation),
			OldPath:   file.OldPath,
			NewPath:   file.NewPath,
		})
	}
	return summary
}

package tui

import (
	"strings"
	"testing"

	"aiterm/internal/controller"
	"aiterm/internal/provider"
	"aiterm/internal/shell"
)

func TestRenderStatusLineUsesCachedSnapshotWithoutControllerReads(t *testing.T) {
	ctrl := &fakeController{
		approvalMode: controller.ApprovalModeAuto,
		contextUsage: controller.ContextWindowUsage{ApproxPromptTokens: 1234},
	}
	model := NewModel(fakeWorkspace(), ctrl).WithProviderOnboarding(
		provider.Profile{
			Preset: provider.PresetOpenAI,
			Name:   "OpenAI Responses",
			Model:  "gpt-test",
			SelectedModel: &provider.ModelOption{
				ID:            "gpt-test",
				ContextWindow: 200000,
			},
		},
		nil,
		nil,
		nil,
		nil,
	).WithShellContext(shell.PromptContext{
		User:      "tester",
		Host:      "local",
		Directory: "/tmp/project",
	})
	model.setMode(AgentMode)
	model.setInput("explain the slowdown")

	ctrl.approvalModeCalls = 0
	ctrl.contextUsageCalls = 0

	line := model.renderStatusLine(120)
	if !strings.Contains(line, "auto") || !strings.Contains(line, "1.2k/200k") {
		t.Fatalf("expected cached status line content, got %q", line)
	}
	if ctrl.approvalModeCalls != 0 {
		t.Fatalf("expected no approval-mode reads during render, got %d", ctrl.approvalModeCalls)
	}
	if ctrl.contextUsageCalls != 0 {
		t.Fatalf("expected no context-usage estimate during render, got %d", ctrl.contextUsageCalls)
	}

	_ = model.renderStatusLine(120)
	if ctrl.approvalModeCalls != 0 || ctrl.contextUsageCalls != 0 {
		t.Fatalf("expected repeated renders to stay controller-free, got approval=%d context=%d", ctrl.approvalModeCalls, ctrl.contextUsageCalls)
	}
}

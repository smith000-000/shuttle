package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"aiterm/internal/app"
	"aiterm/internal/config"
	"aiterm/internal/controller"
	"aiterm/internal/logging"
)

func main() {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(2)
	}

	if cfg.TraceMode == config.TraceModeSensitive && !cfg.TraceConsent {
		fmt.Fprintf(os.Stderr, "config error: sensitive trace captures raw commands, terminal output, key input, prompts, and provider payloads. Re-run with --trace-consent to acknowledge the risk.\n")
		os.Exit(2)
	}

	logger, closeLogger, err := logging.New(cfg.LogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger error: %v\n", err)
		os.Exit(1)
	}
	defer closeLogger()

	closeTrace, err := logging.ConfigureTrace(cfg.TracePath, cfg.TraceMode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "trace logger error: %v\n", err)
		os.Exit(1)
	}
	defer closeTrace()

	if cfg.TraceMode != config.TraceModeOff {
		fmt.Fprintf(os.Stderr, "warning: %s trace enabled; output may contain debugging details in %s\n", cfg.TraceMode, cfg.TracePath)
	}

	logging.Trace(
		"app.start",
		"session", cfg.SessionName,
		"socket", cfg.TmuxSocket,
		"start_dir", cfg.StartDir,
		"state_dir", cfg.StateDir,
		"provider", cfg.ProviderType,
		"auth", cfg.ProviderAuthMethod,
		"model", cfg.ProviderModel,
		"trace_path", cfg.TracePath,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result, err := app.New(cfg, logger).Run(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "shuttle canceled")
			os.Exit(130)
		}

		fmt.Fprintf(os.Stderr, "shuttle error: %v\n", err)
		os.Exit(1)
	}

	if result.Interactive {
		return
	}

	fmt.Printf(
		"session=%s created=%t top_pane=%s bottom_pane=%s\n",
		result.Workspace.SessionName,
		result.Created,
		result.Workspace.TopPane.ID,
		result.Workspace.BottomPane.ID,
	)

	if result.InjectedCommand != "" {
		fmt.Printf("injected=%q target=%s\n", result.InjectedCommand, result.Workspace.TopPane.ID)
	}

	if len(result.AgentEvents) > 0 {
		for _, event := range result.AgentEvents {
			fmt.Println(formatAgentEvent(event))
		}
	}

	if result.Tracked != nil {
		fmt.Printf(
			"tracked command_id=%s exit_code=%d\n%s\n",
			result.Tracked.CommandID,
			result.Tracked.ExitCode,
			result.Tracked.Captured,
		)
	}
}

func formatAgentEvent(event controller.TranscriptEvent) string {
	switch event.Kind {
	case controller.EventUserMessage, controller.EventAgentMessage, controller.EventSystemNotice, controller.EventError:
		payload, _ := event.Payload.(controller.TextPayload)
		return fmt.Sprintf("[%s]\n%s", strings.ToUpper(string(event.Kind)), payload.Text)
	case controller.EventPlan:
		payload, _ := event.Payload.(controller.PlanPayload)
		lines := []string{payload.Summary}
		for index, step := range payload.Steps {
			lines = append(lines, fmt.Sprintf("%d. %s", index+1, step))
		}
		return fmt.Sprintf("[PLAN]\n%s", strings.Join(lines, "\n"))
	case controller.EventProposal:
		payload, _ := event.Payload.(controller.ProposalPayload)
		lines := []string{"kind: " + string(payload.Kind)}
		if payload.Description != "" {
			lines = append(lines, payload.Description)
		}
		if payload.Command != "" {
			lines = append(lines, "command: "+payload.Command)
		}
		if payload.Patch != "" {
			lines = append(lines, "patch:\n"+payload.Patch)
		}
		return fmt.Sprintf("[PROPOSAL]\n%s", strings.Join(lines, "\n"))
	case controller.EventApproval:
		payload, _ := event.Payload.(controller.ApprovalRequest)
		lines := []string{
			"title: " + payload.Title,
			"kind: " + string(payload.Kind),
			"risk: " + string(payload.Risk),
			"summary: " + payload.Summary,
		}
		if payload.Command != "" {
			lines = append(lines, "command: "+payload.Command)
		}
		if payload.Patch != "" {
			lines = append(lines, "patch:\n"+payload.Patch)
		}
		return fmt.Sprintf("[APPROVAL]\n%s", strings.Join(lines, "\n"))
	case controller.EventModelInfo:
		payload, _ := event.Payload.(controller.AgentModelInfo)
		lines := []string{}
		if payload.ProviderPreset != "" {
			lines = append(lines, "provider: "+payload.ProviderPreset)
		}
		if payload.RequestedModel != "" {
			lines = append(lines, "requested: "+payload.RequestedModel)
		}
		if payload.ResponseModel != "" {
			lines = append(lines, "response: "+payload.ResponseModel)
		}
		if payload.ResponseBaseURL != "" {
			lines = append(lines, "base_url: "+payload.ResponseBaseURL)
		}
		return fmt.Sprintf("[MODEL_INFO]\n%s", strings.Join(lines, "\n"))
	case controller.EventPatchApplyResult:
		payload, _ := event.Payload.(controller.PatchApplySummary)
		lines := []string{
			fmt.Sprintf("applied: %t", payload.Applied),
		}
		if payload.WorkspaceRoot != "" {
			lines = append(lines, "workspace_root: "+payload.WorkspaceRoot)
		}
		if payload.Validation != "" {
			lines = append(lines, "validation: "+payload.Validation)
		}
		lines = append(lines,
			fmt.Sprintf("created: %d", payload.Created),
			fmt.Sprintf("updated: %d", payload.Updated),
			fmt.Sprintf("deleted: %d", payload.Deleted),
			fmt.Sprintf("renamed: %d", payload.Renamed),
		)
		if payload.Error != "" {
			lines = append(lines, "error: "+payload.Error)
		}
		return fmt.Sprintf("[PATCH_APPLY_RESULT]\n%s", strings.Join(lines, "\n"))
	default:
		return fmt.Sprintf("[%s]", strings.ToUpper(string(event.Kind)))
	}
}

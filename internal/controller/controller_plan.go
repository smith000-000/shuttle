package controller

import "strings"

func buildActivePlan(plan Plan) ActivePlan {
	steps := make([]PlanStep, 0, len(plan.Steps))
	hasExplicitStatus := false
	for _, step := range plan.Steps {
		status, normalized := parsePlanStep(step)
		if status != "" {
			hasExplicitStatus = true
		}
		step = normalized
		if step == "" {
			continue
		}
		steps = append(steps, PlanStep{
			Text:   step,
			Status: status,
		})
	}
	if !hasExplicitStatus {
		for index := range steps {
			steps[index].Status = PlanStepPending
		}
		if len(steps) > 0 {
			steps[0].Status = PlanStepInProgress
		}
	} else {
		normalizeActivePlanStatuses(steps)
	}

	return ActivePlan{
		Summary: strings.TrimSpace(plan.Summary),
		Steps:   steps,
	}
}

func normalizePlanStepText(step string) string {
	_, normalized := parsePlanStep(step)
	return normalized
}

func parsePlanStep(step string) (PlanStepStatus, string) {
	step = strings.TrimSpace(step)
	if step == "" {
		return "", ""
	}

	lower := strings.ToLower(step)
	for _, candidate := range []struct {
		prefix string
		status PlanStepStatus
	}{
		{prefix: "[done]", status: PlanStepDone},
		{prefix: "[in_progress]", status: PlanStepInProgress},
		{prefix: "[in progress]", status: PlanStepInProgress},
		{prefix: "[pending]", status: PlanStepPending},
		{prefix: "done:", status: PlanStepDone},
		{prefix: "in_progress:", status: PlanStepInProgress},
		{prefix: "in progress:", status: PlanStepInProgress},
		{prefix: "pending:", status: PlanStepPending},
	} {
		if strings.HasPrefix(lower, candidate.prefix) {
			step = strings.TrimSpace(step[len(candidate.prefix):])
			return candidate.status, strings.TrimSpace(step)
		}
	}

	return "", step
}

func normalizeActivePlanStatuses(steps []PlanStep) {
	if len(steps) == 0 {
		return
	}

	foundInProgress := false
	for index := range steps {
		switch steps[index].Status {
		case PlanStepDone:
			continue
		case PlanStepInProgress:
			if !foundInProgress {
				foundInProgress = true
				continue
			}
			steps[index].Status = PlanStepPending
		default:
			steps[index].Status = PlanStepPending
		}
	}
	if foundInProgress {
		return
	}
	for index := range steps {
		if steps[index].Status == PlanStepPending {
			steps[index].Status = PlanStepInProgress
			return
		}
	}
}

func reconcilePlanStatuses(plan ActivePlan, statuses []PlanStepStatus) (ActivePlan, bool) {
	if len(plan.Steps) == 0 || len(statuses) != len(plan.Steps) {
		return ActivePlan{}, false
	}

	updated := ActivePlan{
		Summary: plan.Summary,
		Steps:   append([]PlanStep(nil), plan.Steps...),
	}
	for index, status := range statuses {
		switch status {
		case PlanStepDone, PlanStepInProgress, PlanStepPending:
			updated.Steps[index].Status = status
		default:
			return ActivePlan{}, false
		}
	}
	normalizeActivePlanStatuses(updated.Steps)
	return updated, true
}

func isPlanComplete(plan ActivePlan) bool {
	if len(plan.Steps) == 0 {
		return false
	}
	for _, step := range plan.Steps {
		if step.Status != PlanStepDone {
			return false
		}
	}
	return true
}

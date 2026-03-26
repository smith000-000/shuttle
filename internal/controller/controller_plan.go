package controller

import "strings"

func buildActivePlan(plan Plan) ActivePlan {
	steps := make([]PlanStep, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		step = normalizePlanStepText(step)
		if step == "" {
			continue
		}

		status := PlanStepPending
		if len(steps) == 0 {
			status = PlanStepInProgress
		}
		steps = append(steps, PlanStep{
			Text:   step,
			Status: status,
		})
	}

	return ActivePlan{
		Summary: strings.TrimSpace(plan.Summary),
		Steps:   steps,
	}
}

func (c *LocalController) advanceActivePlanLocked() *TranscriptEvent {
	if c.task.ActivePlan == nil {
		return nil
	}

	plan := ActivePlan{
		Summary: c.task.ActivePlan.Summary,
		Steps:   append([]PlanStep(nil), c.task.ActivePlan.Steps...),
	}

	if len(plan.Steps) == 0 {
		return nil
	}

	current := -1
	for index, step := range plan.Steps {
		if step.Status == PlanStepInProgress {
			current = index
			break
		}
	}
	if current == -1 {
		for index, step := range plan.Steps {
			if step.Status == PlanStepPending {
				current = index
				break
			}
		}
	}
	if current == -1 {
		return nil
	}

	plan.Steps[current].Status = PlanStepDone
	for index := current + 1; index < len(plan.Steps); index++ {
		if plan.Steps[index].Status == PlanStepPending {
			plan.Steps[index].Status = PlanStepInProgress
			break
		}
	}

	if isPlanComplete(plan) {
		c.task.ActivePlan = nil
	} else {
		c.task.ActivePlan = &plan
	}
	event := c.newEvent(EventPlan, plan)
	return &event
}

func normalizePlanStepText(step string) string {
	step = strings.TrimSpace(step)
	if step == "" {
		return ""
	}

	lower := strings.ToLower(step)
	for _, prefix := range []string{"[done]", "[in_progress]", "[pending]", "[in progress]", "done:", "in_progress:", "in progress:", "pending:"} {
		if strings.HasPrefix(lower, prefix) {
			step = strings.TrimSpace(step[len(prefix):])
			lower = strings.ToLower(step)
		}
	}

	return step
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

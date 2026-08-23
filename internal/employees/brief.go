package employees

import (
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// ShiftBrief builds the agent_run goal text for one employee shift.
func ShiftBrief(emp storage.AIEmployee, shiftNumber int) string {
	role := strings.TrimSpace(emp.Role)
	if role == "" {
		role = "AI employee"
	}
	progress := strings.TrimSpace(emp.ProgressSummary)
	if progress == "" {
		progress = "none yet"
	}
	return fmt.Sprintf(
		`You are %s, a Donna AI employee (%s).
Ongoing goal: %s
Progress so far: %s
This is shift #%d. Make concrete progress this shift using tools.
Call report_progress with an updated summary before you wrap up.
Call complete_goal only when the ongoing goal is fully achieved.`,
		strings.TrimSpace(emp.Name),
		role,
		strings.TrimSpace(emp.Goal),
		progress,
		shiftNumber,
	)
}

// DisplayGoal returns a short user-facing goal for the run list.
func DisplayGoal(emp storage.AIEmployee) string {
	name := strings.TrimSpace(emp.Name)
	goal := strings.TrimSpace(emp.Goal)
	if name == "" {
		return goal
	}
	return fmt.Sprintf("[%s] %s", name, goal)
}

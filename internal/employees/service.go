package employees

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/agents"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// Service hires employees and turns them into recurring agent_run shifts.
type Service struct {
	Store   *storage.EmployeesStore
	Agents  *storage.AgentsStore
	Spawner *agents.Spawner
	Jobs    *storage.BackgroundJobs
}

func (s *Service) Hire(ctx context.Context, userID string, in storage.NewEmployeeInput) (storage.AIEmployee, error) {
	if s == nil || s.Store == nil || !s.Store.Enabled {
		return storage.AIEmployee{}, fmt.Errorf("employees_disabled")
	}
	emp, err := s.Store.Create(ctx, userID, in)
	if err != nil {
		return storage.AIEmployee{}, err
	}
	if err := s.StartShift(ctx, emp); err != nil {
		log.Warn("employee first shift failed", map[string]any{
			"employeeId": log.ShortID(emp.ID),
			"error":      err.Error(),
		})
		// Still return the hired employee; scheduler will retry via next_shift_at.
		return emp, nil
	}
	if fresh, err := s.Store.Get(ctx, userID, emp.ID); err == nil {
		return fresh, nil
	}
	return emp, nil
}

func (s *Service) StartShift(ctx context.Context, emp storage.AIEmployee) error {
	if s == nil || s.Store == nil || s.Spawner == nil {
		return fmt.Errorf("employees_unconfigured")
	}
	if emp.Status != storage.EmployeeStatusActive {
		return fmt.Errorf("employee_not_active")
	}
	if emp.CurrentAgentRunID != nil && strings.TrimSpace(*emp.CurrentAgentRunID) != "" {
		return fmt.Errorf("employee_already_working")
	}

	shiftNum := emp.ShiftCount + 1
	allow := emp.ToolAllowlist
	if len(allow) == 0 {
		allow = []string{"orchestration", "memory", "web", "browser", "skills", "commerce", "employee"}
	} else {
		allow = ensureToolset(allow, "employee")
		allow = ensureToolset(allow, "orchestration")
	}

	empID := emp.ID
	run, err := s.Spawner.Spawn(ctx, emp.UserID, agents.SpawnInput{
		Goal:          DisplayGoal(emp),
		GroundedGoal:  ShiftBrief(emp, shiftNum),
		EmployeeID:    &empID,
		ToolAllowlist: allow,
		MaxSteps:      emp.MaxStepsPerShift,
	})
	if err != nil {
		return err
	}

	claimed, err := s.Store.ClaimForShift(ctx, emp, run.ID)
	if err != nil {
		// Another worker claimed; cancel the orphaned run best-effort.
		_, _ = s.Agents.Cancel(ctx, emp.UserID, run.ID)
		return err
	}
	_ = claimed
	return nil
}

func ensureToolset(allow []string, name string) []string {
	for _, a := range allow {
		if a == name {
			return allow
		}
	}
	return append(append([]string{}, allow...), name)
}

// AfterAgentRun resumes the employee lifecycle when a shift ends.
func (s *Service) AfterAgentRun(ctx context.Context, run storage.AgentRun) {
	if s == nil || s.Store == nil || run.EmployeeID == nil || strings.TrimSpace(*run.EmployeeID) == "" {
		return
	}
	if !storage.IsTerminalAgentStatus(run.Status) && run.Status != storage.AgentStatusWaitingForUser {
		return
	}

	empID := *run.EmployeeID
	emp, err := s.Store.Get(ctx, run.UserID, empID)
	if err != nil {
		log.Warn("employee after-run lookup failed", map[string]any{
			"employeeId": log.ShortID(empID),
			"error":      err.Error(),
		})
		return
	}

	// Clear current run pointer when this run was the active shift.
	if emp.CurrentAgentRunID != nil && *emp.CurrentAgentRunID == run.ID {
		_ = s.Store.ClearCurrentRun(ctx, run.UserID, empID, run.ID)
	}

	if run.Status == storage.AgentStatusWaitingForUser {
		// Stay paused on shifts until the user answers; do not schedule next.
		return
	}

	goalDone, summary := parseShiftResult(run)
	patch := map[string]any{}
	if summary != "" {
		patch["progress_summary"] = summary
	}

	if goalDone || runHasCompleteGoal(run) {
		now := time.Now().UTC().Format(time.RFC3339)
		patch["status"] = storage.EmployeeStatusCompleted
		patch["completed_at"] = now
		patch["next_shift_at"] = nil
		patch["current_agent_run_id"] = nil
		if _, err := s.Store.Patch(ctx, run.UserID, empID, patch); err != nil {
			log.Warn("employee complete patch failed", map[string]any{"error": err.Error()})
		}
		return
	}

	if emp.Status != storage.EmployeeStatusActive {
		if len(patch) > 0 {
			_, _ = s.Store.Patch(ctx, run.UserID, empID, patch)
		}
		return
	}

	next := storage.NextShiftAt(emp.CadenceMinutes, time.Now().UTC())
	patch["next_shift_at"] = next.Format(time.RFC3339)
	if _, err := s.Store.Patch(ctx, run.UserID, empID, patch); err != nil {
		log.Warn("employee schedule patch failed", map[string]any{"error": err.Error()})
		return
	}

	// Continuous employees: try to start immediately after cooldown via job.
	if emp.CadenceMinutes <= 0 && s.Jobs != nil && s.Jobs.Enabled {
		_, _ = s.Jobs.Enqueue(ctx, storage.EnqueueJobInput{
			UserID:     run.UserID,
			JobType:    storage.JobTypeEmployeeShift,
			DedupeKey:  fmt.Sprintf("employee_shift:%s:%d", empID, emp.ShiftCount+1),
			Payload:    map[string]any{"employee_id": empID, "user_id": run.UserID},
			TargetKind: storage.TargetKindAIEmployee,
			TargetID:   empID,
			RunAfter:   next,
		})
	}
}

func parseShiftResult(run storage.AgentRun) (goalDone bool, summary string) {
	if len(run.Result) == 0 || string(run.Result) == "null" {
		return false, ""
	}
	var result map[string]any
	if err := json.Unmarshal(run.Result, &result); err != nil {
		return false, ""
	}
	if v, ok := result["employee_goal_complete"].(bool); ok && v {
		goalDone = true
	}
	if v, ok := result["progress_summary"].(string); ok {
		summary = strings.TrimSpace(v)
	}
	if summary == "" {
		if v, ok := result["summary"].(string); ok {
			summary = strings.TrimSpace(v)
		}
	}
	return goalDone, summary
}

func runHasCompleteGoal(run storage.AgentRun) bool {
	done, _ := parseShiftResult(run)
	return done
}

// HandleShiftJob runs one due employee shift from the background queue.
func (s *Service) HandleShiftJob(ctx context.Context, job storage.BackgroundJob) error {
	if s == nil || s.Store == nil {
		return fmt.Errorf("employees_unconfigured")
	}
	var payload struct {
		EmployeeID string `json:"employee_id"`
		UserID     string `json:"user_id"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	id := payload.EmployeeID
	if id == "" && job.TargetID != nil {
		id = *job.TargetID
	}
	if id == "" {
		return fmt.Errorf("employee_id_required")
	}
	var emp storage.AIEmployee
	var err error
	if payload.UserID != "" {
		emp, err = s.Store.Get(ctx, payload.UserID, id)
	} else {
		emp, err = s.Store.GetByID(ctx, id)
	}
	if err != nil {
		return err
	}
	if emp.Status != storage.EmployeeStatusActive {
		return nil
	}
	if emp.CurrentAgentRunID != nil && strings.TrimSpace(*emp.CurrentAgentRunID) != "" {
		return nil
	}
	return s.StartShift(ctx, emp)
}

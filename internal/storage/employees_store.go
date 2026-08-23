package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	EmployeeStatusActive    = "active"
	EmployeeStatusPaused    = "paused"
	EmployeeStatusCompleted = "completed"
	EmployeeStatusArchived  = "archived"

	JobTypeEmployeeShift    = "employee_shift"
	TargetKindAIEmployee    = "ai_employee"
	DefaultEmployeeCadence  = 0
	DefaultEmployeeMaxSteps = 40
	ContinuousShiftCooldown = 1 * time.Minute
)

func IsEmployeeStatus(s string) bool {
	switch s {
	case EmployeeStatusActive, EmployeeStatusPaused, EmployeeStatusCompleted, EmployeeStatusArchived:
		return true
	default:
		return false
	}
}

// AIEmployee is a durable goal-driven worker that spawns agent_run shifts.
type AIEmployee struct {
	ID                 string          `json:"id"`
	UserID             string          `json:"user_id"`
	Name               string          `json:"name"`
	Role               string          `json:"role"`
	Goal               string          `json:"goal"`
	Status             string          `json:"status"`
	CadenceMinutes     int             `json:"cadence_minutes"`
	MaxStepsPerShift   int             `json:"max_steps_per_shift"`
	ToolAllowlist      []string        `json:"tool_allowlist"`
	ProgressSummary    string          `json:"progress_summary"`
	Progress           json.RawMessage `json:"progress"`
	CurrentAgentRunID  *string         `json:"current_agent_run_id,omitempty"`
	ShiftCount         int             `json:"shift_count"`
	LastShiftAt        *string         `json:"last_shift_at,omitempty"`
	NextShiftAt        *string         `json:"next_shift_at,omitempty"`
	CreatedAt          string          `json:"created_at"`
	UpdatedAt          string          `json:"updated_at"`
	CompletedAt        *string         `json:"completed_at,omitempty"`
}

type NewEmployeeInput struct {
	Name             string
	Role             string
	Goal             string
	CadenceMinutes   *int
	MaxStepsPerShift *int
	ToolAllowlist    []string
}

type UpdateEmployeeInput struct {
	Name             *string
	Role             *string
	Goal             *string
	CadenceMinutes   *int
	MaxStepsPerShift *int
	ToolAllowlist    []string
	ProgressSummary  *string
	Status           *string
}

type EmployeesStore struct {
	DB      *Supabase
	Enabled bool
}

func (s *EmployeesStore) selectColumns() string {
	return "id,user_id,name,role,goal,status,cadence_minutes,max_steps_per_shift,tool_allowlist,progress_summary,progress,current_agent_run_id,shift_count,last_shift_at,next_shift_at,created_at,updated_at,completed_at"
}

func (s *EmployeesStore) Create(ctx context.Context, userID string, in NewEmployeeInput) (AIEmployee, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return AIEmployee{}, fmt.Errorf("employees_disabled")
	}
	name := strings.TrimSpace(in.Name)
	goal := strings.TrimSpace(in.Goal)
	if name == "" {
		return AIEmployee{}, fmt.Errorf("name_required")
	}
	if goal == "" {
		return AIEmployee{}, fmt.Errorf("goal_required")
	}
	if len(name) > 80 {
		return AIEmployee{}, fmt.Errorf("name_too_long")
	}
	if len(goal) > 4000 {
		return AIEmployee{}, fmt.Errorf("goal_too_long")
	}
	role := strings.TrimSpace(in.Role)
	if len(role) > 120 {
		return AIEmployee{}, fmt.Errorf("role_too_long")
	}
	cadence := DefaultEmployeeCadence
	if in.CadenceMinutes != nil {
		cadence = *in.CadenceMinutes
	}
	if cadence < 0 || cadence > 10080 {
		return AIEmployee{}, fmt.Errorf("invalid_cadence")
	}
	maxSteps := DefaultEmployeeMaxSteps
	if in.MaxStepsPerShift != nil {
		maxSteps = *in.MaxStepsPerShift
	}
	if maxSteps <= 0 || maxSteps > 200 {
		return AIEmployee{}, fmt.Errorf("invalid_max_steps")
	}
	allow := in.ToolAllowlist
	if allow == nil {
		allow = []string{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	body := map[string]any{
		"user_id":              userID,
		"name":                 name,
		"role":                 role,
		"goal":                 goal,
		"status":               EmployeeStatusActive,
		"cadence_minutes":      cadence,
		"max_steps_per_shift":  maxSteps,
		"tool_allowlist":       allow,
		"progress_summary":     "",
		"progress":             map[string]any{},
		"shift_count":          0,
		"next_shift_at":        now,
		"updated_at":           now,
	}
	var rows []AIEmployee
	if err := s.DB.Insert(ctx, "ai_employees", body, &rows); err != nil {
		return AIEmployee{}, err
	}
	if len(rows) == 0 {
		return AIEmployee{}, fmt.Errorf("employee_create_empty")
	}
	return rows[0], nil
}

func (s *EmployeesStore) Get(ctx context.Context, userID, id string) (AIEmployee, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return AIEmployee{}, fmt.Errorf("employees_disabled")
	}
	q := url.Values{}
	q.Set("select", s.selectColumns())
	q.Set("id", "eq."+id)
	q.Set("user_id", "eq."+userID)
	q.Set("limit", "1")
	var rows []AIEmployee
	if err := s.DB.Get(ctx, "ai_employees", q, &rows); err != nil {
		return AIEmployee{}, err
	}
	if len(rows) == 0 {
		return AIEmployee{}, fmt.Errorf("employee_not_found")
	}
	return rows[0], nil
}

func (s *EmployeesStore) GetByID(ctx context.Context, id string) (AIEmployee, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return AIEmployee{}, fmt.Errorf("employees_disabled")
	}
	q := url.Values{}
	q.Set("select", s.selectColumns())
	q.Set("id", "eq."+id)
	q.Set("limit", "1")
	var rows []AIEmployee
	if err := s.DB.Get(ctx, "ai_employees", q, &rows); err != nil {
		return AIEmployee{}, err
	}
	if len(rows) == 0 {
		return AIEmployee{}, fmt.Errorf("employee_not_found")
	}
	return rows[0], nil
}

func (s *EmployeesStore) List(ctx context.Context, userID, status string, limit, offset int) ([]AIEmployee, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("employees_disabled")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	q := url.Values{}
	q.Set("select", s.selectColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("order", "updated_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("offset", fmt.Sprintf("%d", offset))
	if status = strings.TrimSpace(status); status != "" {
		if !IsEmployeeStatus(status) {
			return nil, fmt.Errorf("invalid_status")
		}
		q.Set("status", "eq."+status)
	}
	var rows []AIEmployee
	if err := s.DB.Get(ctx, "ai_employees", q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []AIEmployee{}
	}
	return rows, nil
}

func (s *EmployeesStore) Patch(ctx context.Context, userID, id string, patch map[string]any) (AIEmployee, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return AIEmployee{}, fmt.Errorf("employees_disabled")
	}
	if patch == nil {
		patch = map[string]any{}
	}
	patch["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	q := url.Values{}
	q.Set("id", "eq."+id)
	q.Set("user_id", "eq."+userID)
	q.Set("select", s.selectColumns())
	var rows []AIEmployee
	if err := s.DB.PatchReturning(ctx, "ai_employees", q, patch, &rows); err != nil {
		return AIEmployee{}, err
	}
	if len(rows) == 0 {
		return AIEmployee{}, fmt.Errorf("employee_not_found")
	}
	return rows[0], nil
}

func (s *EmployeesStore) Update(ctx context.Context, userID, id string, in UpdateEmployeeInput) (AIEmployee, error) {
	patch := map[string]any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return AIEmployee{}, fmt.Errorf("name_required")
		}
		if len(name) > 80 {
			return AIEmployee{}, fmt.Errorf("name_too_long")
		}
		patch["name"] = name
	}
	if in.Role != nil {
		role := strings.TrimSpace(*in.Role)
		if len(role) > 120 {
			return AIEmployee{}, fmt.Errorf("role_too_long")
		}
		patch["role"] = role
	}
	if in.Goal != nil {
		goal := strings.TrimSpace(*in.Goal)
		if goal == "" {
			return AIEmployee{}, fmt.Errorf("goal_required")
		}
		if len(goal) > 4000 {
			return AIEmployee{}, fmt.Errorf("goal_too_long")
		}
		patch["goal"] = goal
	}
	if in.CadenceMinutes != nil {
		if *in.CadenceMinutes < 0 || *in.CadenceMinutes > 10080 {
			return AIEmployee{}, fmt.Errorf("invalid_cadence")
		}
		patch["cadence_minutes"] = *in.CadenceMinutes
	}
	if in.MaxStepsPerShift != nil {
		if *in.MaxStepsPerShift <= 0 || *in.MaxStepsPerShift > 200 {
			return AIEmployee{}, fmt.Errorf("invalid_max_steps")
		}
		patch["max_steps_per_shift"] = *in.MaxStepsPerShift
	}
	if in.ToolAllowlist != nil {
		patch["tool_allowlist"] = in.ToolAllowlist
	}
	if in.ProgressSummary != nil {
		summary := strings.TrimSpace(*in.ProgressSummary)
		if len(summary) > 8000 {
			return AIEmployee{}, fmt.Errorf("progress_too_long")
		}
		patch["progress_summary"] = summary
	}
	if in.Status != nil {
		if !IsEmployeeStatus(*in.Status) {
			return AIEmployee{}, fmt.Errorf("invalid_status")
		}
		patch["status"] = *in.Status
		if *in.Status == EmployeeStatusCompleted {
			now := time.Now().UTC().Format(time.RFC3339)
			patch["completed_at"] = now
			patch["next_shift_at"] = nil
			patch["current_agent_run_id"] = nil
		}
		if *in.Status == EmployeeStatusPaused || *in.Status == EmployeeStatusArchived {
			patch["next_shift_at"] = nil
		}
	}
	if len(patch) == 0 {
		return s.Get(ctx, userID, id)
	}
	return s.Patch(ctx, userID, id, patch)
}

// ListDueActive returns active employees whose next_shift_at is due and who are not mid-shift.
func (s *EmployeesStore) ListDueActive(ctx context.Context, limit int) ([]AIEmployee, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("employees_disabled")
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	now := time.Now().UTC().Format(time.RFC3339)
	q := url.Values{}
	q.Set("select", s.selectColumns())
	q.Set("status", "eq."+EmployeeStatusActive)
	q.Set("next_shift_at", "lte."+now)
	q.Set("current_agent_run_id", "is.null")
	q.Set("order", "next_shift_at.asc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	var rows []AIEmployee
	if err := s.DB.Get(ctx, "ai_employees", q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []AIEmployee{}
	}
	return rows, nil
}

// ClaimForShift atomically marks an employee as starting a shift (optimistic).
func (s *EmployeesStore) ClaimForShift(ctx context.Context, emp AIEmployee, runID string) (AIEmployee, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	patch := map[string]any{
		"current_agent_run_id": runID,
		"shift_count":          emp.ShiftCount + 1,
		"last_shift_at":        now,
		"next_shift_at":        nil,
		"updated_at":           now,
	}
	q := url.Values{}
	q.Set("id", "eq."+emp.ID)
	q.Set("user_id", "eq."+emp.UserID)
	q.Set("status", "eq."+EmployeeStatusActive)
	q.Set("current_agent_run_id", "is.null")
	q.Set("select", s.selectColumns())
	var rows []AIEmployee
	if err := s.DB.PatchReturning(ctx, "ai_employees", q, patch, &rows); err != nil {
		return AIEmployee{}, err
	}
	if len(rows) == 0 {
		return AIEmployee{}, fmt.Errorf("employee_claim_conflict")
	}
	return rows[0], nil
}

func (s *EmployeesStore) ClearCurrentRun(ctx context.Context, userID, employeeID, runID string) error {
	q := url.Values{}
	q.Set("id", "eq."+employeeID)
	q.Set("user_id", "eq."+userID)
	q.Set("current_agent_run_id", "eq."+runID)
	q.Set("select", s.selectColumns())
	var rows []AIEmployee
	return s.DB.PatchReturning(ctx, "ai_employees", q, map[string]any{
		"current_agent_run_id": nil,
		"updated_at":           time.Now().UTC().Format(time.RFC3339),
	}, &rows)
}

func NextShiftAt(cadenceMinutes int, from time.Time) time.Time {
	if cadenceMinutes <= 0 {
		return from.Add(ContinuousShiftCooldown)
	}
	return from.Add(time.Duration(cadenceMinutes) * time.Minute)
}

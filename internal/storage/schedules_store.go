package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	ScheduleStatusActive    = "active"
	ScheduleStatusPaused    = "paused"
	ScheduleStatusCompleted = "completed"
	ScheduleStatusArchived  = "archived"

	DefaultScheduleCadence  = 1440
	DefaultScheduleMaxSteps = 80
	MaxScheduleCadence      = 10080
)

func IsScheduleStatus(s string) bool {
	switch s {
	case ScheduleStatusActive, ScheduleStatusPaused, ScheduleStatusCompleted, ScheduleStatusArchived:
		return true
	default:
		return false
	}
}

// ScheduledAgentGoal is a recurring (or one-shot) agent goal.
type ScheduledAgentGoal struct {
	ID                string   `json:"id"`
	UserID            string   `json:"user_id"`
	Title             string   `json:"title"`
	Goal              string   `json:"goal"`
	Status            string   `json:"status"`
	CadenceMinutes    int      `json:"cadence_minutes"`
	SelectedSkills    []string `json:"selected_skills"`
	LastSummary       string   `json:"last_summary"`
	CurrentAgentRunID *string  `json:"current_agent_run_id,omitempty"`
	RunCount          int      `json:"run_count"`
	LastRunAt         *string  `json:"last_run_at,omitempty"`
	NextRunAt         *string  `json:"next_run_at,omitempty"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
	CompletedAt       *string  `json:"completed_at,omitempty"`
}

type NewScheduleInput struct {
	Title          string
	Goal           string
	CadenceMinutes *int
	SelectedSkills []string
}

type UpdateScheduleInput struct {
	Title          *string
	Goal           *string
	CadenceMinutes *int
	SelectedSkills []string
	LastSummary    *string
	Status         *string
}

type SchedulesStore struct {
	DB      *Supabase
	Enabled bool
}

func (s *SchedulesStore) selectColumns() string {
	return "id,user_id,title,goal,status,cadence_minutes,selected_skills,last_summary,current_agent_run_id,run_count,last_run_at,next_run_at,created_at,updated_at,completed_at"
}

func (s *SchedulesStore) Create(ctx context.Context, userID string, in NewScheduleInput) (ScheduledAgentGoal, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return ScheduledAgentGoal{}, fmt.Errorf("schedules_disabled")
	}
	title := strings.TrimSpace(in.Title)
	goal := strings.TrimSpace(in.Goal)
	if title == "" {
		return ScheduledAgentGoal{}, fmt.Errorf("title_required")
	}
	if goal == "" {
		return ScheduledAgentGoal{}, fmt.Errorf("goal_required")
	}
	if len(title) > 120 {
		return ScheduledAgentGoal{}, fmt.Errorf("title_too_long")
	}
	if len(goal) > 4000 {
		return ScheduledAgentGoal{}, fmt.Errorf("goal_too_long")
	}
	cadence := DefaultScheduleCadence
	if in.CadenceMinutes != nil {
		cadence = *in.CadenceMinutes
	}
	if cadence < 0 || cadence > MaxScheduleCadence {
		return ScheduledAgentGoal{}, fmt.Errorf("invalid_cadence")
	}
	skills := in.SelectedSkills
	if skills == nil {
		skills = []string{}
	}
	if len(skills) > 5 {
		return ScheduledAgentGoal{}, fmt.Errorf("too_many_skills")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	body := map[string]any{
		"user_id":         userID,
		"title":           title,
		"goal":            goal,
		"status":          ScheduleStatusActive,
		"cadence_minutes": cadence,
		"selected_skills": skills,
		"last_summary":    "",
		"run_count":       0,
		"next_run_at":     now,
		"updated_at":      now,
	}
	var rows []ScheduledAgentGoal
	if err := s.DB.Insert(ctx, "scheduled_agent_goals", body, &rows); err != nil {
		return ScheduledAgentGoal{}, err
	}
	if len(rows) == 0 {
		return ScheduledAgentGoal{}, fmt.Errorf("schedule_create_empty")
	}
	return rows[0], nil
}

func (s *SchedulesStore) Get(ctx context.Context, userID, id string) (ScheduledAgentGoal, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return ScheduledAgentGoal{}, fmt.Errorf("schedules_disabled")
	}
	q := url.Values{}
	q.Set("select", s.selectColumns())
	q.Set("id", "eq."+id)
	q.Set("user_id", "eq."+userID)
	q.Set("limit", "1")
	var rows []ScheduledAgentGoal
	if err := s.DB.Get(ctx, "scheduled_agent_goals", q, &rows); err != nil {
		return ScheduledAgentGoal{}, err
	}
	if len(rows) == 0 {
		return ScheduledAgentGoal{}, fmt.Errorf("schedule_not_found")
	}
	return rows[0], nil
}

func (s *SchedulesStore) GetByID(ctx context.Context, id string) (ScheduledAgentGoal, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return ScheduledAgentGoal{}, fmt.Errorf("schedules_disabled")
	}
	q := url.Values{}
	q.Set("select", s.selectColumns())
	q.Set("id", "eq."+id)
	q.Set("limit", "1")
	var rows []ScheduledAgentGoal
	if err := s.DB.Get(ctx, "scheduled_agent_goals", q, &rows); err != nil {
		return ScheduledAgentGoal{}, err
	}
	if len(rows) == 0 {
		return ScheduledAgentGoal{}, fmt.Errorf("schedule_not_found")
	}
	return rows[0], nil
}

func (s *SchedulesStore) List(ctx context.Context, userID, status string, limit, offset int) ([]ScheduledAgentGoal, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("schedules_disabled")
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
		if !IsScheduleStatus(status) {
			return nil, fmt.Errorf("invalid_status")
		}
		q.Set("status", "eq."+status)
	}
	var rows []ScheduledAgentGoal
	if err := s.DB.Get(ctx, "scheduled_agent_goals", q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []ScheduledAgentGoal{}
	}
	return rows, nil
}

func (s *SchedulesStore) Patch(ctx context.Context, userID, id string, patch map[string]any) (ScheduledAgentGoal, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return ScheduledAgentGoal{}, fmt.Errorf("schedules_disabled")
	}
	if patch == nil {
		patch = map[string]any{}
	}
	patch["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	q := url.Values{}
	q.Set("id", "eq."+id)
	q.Set("user_id", "eq."+userID)
	q.Set("select", s.selectColumns())
	var rows []ScheduledAgentGoal
	if err := s.DB.PatchReturning(ctx, "scheduled_agent_goals", q, patch, &rows); err != nil {
		return ScheduledAgentGoal{}, err
	}
	if len(rows) == 0 {
		return ScheduledAgentGoal{}, fmt.Errorf("schedule_not_found")
	}
	return rows[0], nil
}

func (s *SchedulesStore) Update(ctx context.Context, userID, id string, in UpdateScheduleInput) (ScheduledAgentGoal, error) {
	patch := map[string]any{}
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if title == "" {
			return ScheduledAgentGoal{}, fmt.Errorf("title_required")
		}
		if len(title) > 120 {
			return ScheduledAgentGoal{}, fmt.Errorf("title_too_long")
		}
		patch["title"] = title
	}
	if in.Goal != nil {
		goal := strings.TrimSpace(*in.Goal)
		if goal == "" {
			return ScheduledAgentGoal{}, fmt.Errorf("goal_required")
		}
		if len(goal) > 4000 {
			return ScheduledAgentGoal{}, fmt.Errorf("goal_too_long")
		}
		patch["goal"] = goal
	}
	if in.CadenceMinutes != nil {
		if *in.CadenceMinutes < 0 || *in.CadenceMinutes > MaxScheduleCadence {
			return ScheduledAgentGoal{}, fmt.Errorf("invalid_cadence")
		}
		patch["cadence_minutes"] = *in.CadenceMinutes
	}
	if in.SelectedSkills != nil {
		if len(in.SelectedSkills) > 5 {
			return ScheduledAgentGoal{}, fmt.Errorf("too_many_skills")
		}
		patch["selected_skills"] = in.SelectedSkills
	}
	if in.LastSummary != nil {
		summary := strings.TrimSpace(*in.LastSummary)
		if len(summary) > 8000 {
			return ScheduledAgentGoal{}, fmt.Errorf("summary_too_long")
		}
		patch["last_summary"] = summary
	}
	if in.Status != nil {
		if !IsScheduleStatus(*in.Status) {
			return ScheduledAgentGoal{}, fmt.Errorf("invalid_status")
		}
		patch["status"] = *in.Status
		if *in.Status == ScheduleStatusCompleted {
			now := time.Now().UTC().Format(time.RFC3339)
			patch["completed_at"] = now
			patch["next_run_at"] = nil
			patch["current_agent_run_id"] = nil
		}
		if *in.Status == ScheduleStatusPaused || *in.Status == ScheduleStatusArchived {
			patch["next_run_at"] = nil
		}
	}
	if len(patch) == 0 {
		return s.Get(ctx, userID, id)
	}
	return s.Patch(ctx, userID, id, patch)
}

// ListDueActive returns active schedules whose next_run_at is due and who are not mid-run.
func (s *SchedulesStore) ListDueActive(ctx context.Context, limit int) ([]ScheduledAgentGoal, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("schedules_disabled")
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
	q.Set("status", "eq."+ScheduleStatusActive)
	q.Set("next_run_at", "lte."+now)
	q.Set("current_agent_run_id", "is.null")
	q.Set("order", "next_run_at.asc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	var rows []ScheduledAgentGoal
	if err := s.DB.Get(ctx, "scheduled_agent_goals", q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []ScheduledAgentGoal{}
	}
	return rows, nil
}

// ClaimForRun atomically marks a schedule as starting a run.
func (s *SchedulesStore) ClaimForRun(ctx context.Context, sch ScheduledAgentGoal, runID string) (ScheduledAgentGoal, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	patch := map[string]any{
		"current_agent_run_id": runID,
		"run_count":            sch.RunCount + 1,
		"last_run_at":          now,
		"next_run_at":          nil,
		"updated_at":           now,
	}
	q := url.Values{}
	q.Set("id", "eq."+sch.ID)
	q.Set("user_id", "eq."+sch.UserID)
	q.Set("status", "eq."+ScheduleStatusActive)
	q.Set("current_agent_run_id", "is.null")
	q.Set("select", s.selectColumns())
	var rows []ScheduledAgentGoal
	if err := s.DB.PatchReturning(ctx, "scheduled_agent_goals", q, patch, &rows); err != nil {
		return ScheduledAgentGoal{}, err
	}
	if len(rows) == 0 {
		return ScheduledAgentGoal{}, fmt.Errorf("schedule_claim_conflict")
	}
	return rows[0], nil
}

func (s *SchedulesStore) ClearCurrentRun(ctx context.Context, userID, scheduleID, runID string) error {
	q := url.Values{}
	q.Set("id", "eq."+scheduleID)
	q.Set("user_id", "eq."+userID)
	q.Set("current_agent_run_id", "eq."+runID)
	q.Set("select", s.selectColumns())
	var rows []ScheduledAgentGoal
	return s.DB.PatchReturning(ctx, "scheduled_agent_goals", q, map[string]any{
		"current_agent_run_id": nil,
		"updated_at":           time.Now().UTC().Format(time.RFC3339),
	}, &rows)
}

// NextScheduleAt returns when a recurring schedule should fire again.
// Cadence 0 means one-shot (caller should complete instead of rescheduling).
func NextScheduleAt(cadenceMinutes int, from time.Time) (time.Time, bool) {
	if cadenceMinutes <= 0 {
		return time.Time{}, false
	}
	return from.Add(time.Duration(cadenceMinutes) * time.Minute), true
}

package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	ReminderStatusScheduled  = "scheduled"
	ReminderStatusFired      = "fired"
	ReminderStatusDismissed  = "dismissed"
	ReminderStatusCancelled  = "cancelled"
)

func IsReminderStatus(s string) bool {
	switch s {
	case ReminderStatusScheduled, ReminderStatusFired, ReminderStatusDismissed, ReminderStatusCancelled:
		return true
	default:
		return false
	}
}

// Reminder is a timed alert for a user.
type Reminder struct {
	ID          string  `json:"id"`
	UserID      string  `json:"user_id"`
	Title       string  `json:"title"`
	Notes       string  `json:"notes"`
	DueAt       string  `json:"due_at"`
	Timezone    string  `json:"timezone"`
	Status      string  `json:"status"`
	ActionRunID *string `json:"action_run_id,omitempty"`
	FiredAt     *string `json:"fired_at,omitempty"`
	DismissedAt *string `json:"dismissed_at,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type NewReminderInput struct {
	Title       string
	Notes       string
	DueAt       time.Time
	Timezone    string
	ActionRunID *string
}

type UpdateReminderInput struct {
	Title    *string
	Notes    *string
	DueAt    *time.Time
	Timezone *string
}

type RemindersStore struct {
	DB      *Supabase
	Enabled bool
}

func (s *RemindersStore) selectColumns() string {
	return "id,user_id,title,notes,due_at,timezone,status,action_run_id,fired_at,dismissed_at,created_at,updated_at"
}

func (s *RemindersStore) Create(ctx context.Context, userID string, in NewReminderInput) (Reminder, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return Reminder{}, fmt.Errorf("reminders_disabled")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return Reminder{}, fmt.Errorf("title_required")
	}
	if len(title) > 200 {
		return Reminder{}, fmt.Errorf("title_too_long")
	}
	notes := strings.TrimSpace(in.Notes)
	if len(notes) > 4000 {
		return Reminder{}, fmt.Errorf("notes_too_long")
	}
	if in.DueAt.IsZero() {
		return Reminder{}, fmt.Errorf("due_at_required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	body := map[string]any{
		"user_id":  userID,
		"title":    title,
		"notes":    notes,
		"due_at":   in.DueAt.UTC().Format(time.RFC3339),
		"timezone": strings.TrimSpace(in.Timezone),
		"status":   ReminderStatusScheduled,
		"updated_at": now,
	}
	if in.ActionRunID != nil && strings.TrimSpace(*in.ActionRunID) != "" {
		body["action_run_id"] = strings.TrimSpace(*in.ActionRunID)
	}
	var rows []Reminder
	if err := s.DB.Insert(ctx, "reminders", body, &rows); err != nil {
		return Reminder{}, err
	}
	if len(rows) == 0 {
		return Reminder{}, fmt.Errorf("reminder_create_empty")
	}
	return rows[0], nil
}

func (s *RemindersStore) Get(ctx context.Context, userID, id string) (Reminder, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return Reminder{}, fmt.Errorf("reminders_disabled")
	}
	q := url.Values{}
	q.Set("select", s.selectColumns())
	q.Set("id", "eq."+id)
	q.Set("user_id", "eq."+userID)
	q.Set("limit", "1")
	var rows []Reminder
	if err := s.DB.Get(ctx, "reminders", q, &rows); err != nil {
		return Reminder{}, err
	}
	if len(rows) == 0 {
		return Reminder{}, fmt.Errorf("reminder_not_found")
	}
	return rows[0], nil
}

func (s *RemindersStore) List(ctx context.Context, userID, status string, limit, offset int) ([]Reminder, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("reminders_disabled")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	q := url.Values{}
	q.Set("select", s.selectColumns())
	q.Set("user_id", "eq."+userID)
	status = strings.TrimSpace(strings.ToLower(status))
	switch status {
	case "", "open":
		q.Set("status", "in.(scheduled,fired)")
	case "all":
		// no filter
	default:
		parts := strings.Split(status, ",")
		cleaned := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if IsReminderStatus(p) {
				cleaned = append(cleaned, p)
			}
		}
		if len(cleaned) == 1 {
			q.Set("status", "eq."+cleaned[0])
		} else if len(cleaned) > 1 {
			q.Set("status", "in.("+strings.Join(cleaned, ",")+")")
		} else {
			return nil, fmt.Errorf("invalid_status")
		}
	}
	q.Set("order", "due_at.asc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("offset", fmt.Sprintf("%d", offset))
	var rows []Reminder
	if err := s.DB.Get(ctx, "reminders", q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []Reminder{}
	}
	return rows, nil
}

func (s *RemindersStore) Patch(ctx context.Context, userID, id string, patch map[string]any) (Reminder, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return Reminder{}, fmt.Errorf("reminders_disabled")
	}
	if patch == nil {
		patch = map[string]any{}
	}
	patch["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	q := url.Values{}
	q.Set("id", "eq."+id)
	q.Set("user_id", "eq."+userID)
	q.Set("select", s.selectColumns())
	var rows []Reminder
	if err := s.DB.PatchReturning(ctx, "reminders", q, patch, &rows); err != nil {
		return Reminder{}, err
	}
	if len(rows) == 0 {
		return Reminder{}, fmt.Errorf("reminder_not_found")
	}
	return rows[0], nil
}

func (s *RemindersStore) Update(ctx context.Context, userID, id string, in UpdateReminderInput) (Reminder, error) {
	existing, err := s.Get(ctx, userID, id)
	if err != nil {
		return Reminder{}, err
	}
	if existing.Status != ReminderStatusScheduled {
		return Reminder{}, fmt.Errorf("reminder_not_editable")
	}
	patch := map[string]any{}
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if title == "" {
			return Reminder{}, fmt.Errorf("title_required")
		}
		if len(title) > 200 {
			return Reminder{}, fmt.Errorf("title_too_long")
		}
		patch["title"] = title
	}
	if in.Notes != nil {
		notes := strings.TrimSpace(*in.Notes)
		if len(notes) > 4000 {
			return Reminder{}, fmt.Errorf("notes_too_long")
		}
		patch["notes"] = notes
	}
	if in.DueAt != nil {
		if in.DueAt.IsZero() {
			return Reminder{}, fmt.Errorf("due_at_required")
		}
		patch["due_at"] = in.DueAt.UTC().Format(time.RFC3339)
	}
	if in.Timezone != nil {
		patch["timezone"] = strings.TrimSpace(*in.Timezone)
	}
	if len(patch) == 0 {
		return existing, nil
	}
	return s.Patch(ctx, userID, id, patch)
}

func (s *RemindersStore) SetStatus(ctx context.Context, userID, id, status string) (Reminder, error) {
	if !IsReminderStatus(status) {
		return Reminder{}, fmt.Errorf("invalid_status")
	}
	existing, err := s.Get(ctx, userID, id)
	if err != nil {
		return Reminder{}, err
	}
	if existing.Status == status {
		return existing, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	patch := map[string]any{"status": status}
	switch status {
	case ReminderStatusCancelled:
		if existing.Status != ReminderStatusScheduled {
			return Reminder{}, fmt.Errorf("reminder_not_cancellable")
		}
	case ReminderStatusDismissed:
		if existing.Status != ReminderStatusFired && existing.Status != ReminderStatusScheduled {
			return Reminder{}, fmt.Errorf("reminder_not_dismissable")
		}
		patch["dismissed_at"] = now
	case ReminderStatusFired:
		if existing.Status != ReminderStatusScheduled {
			return Reminder{}, fmt.Errorf("reminder_not_firable")
		}
		patch["fired_at"] = now
	default:
		return Reminder{}, fmt.Errorf("invalid_status_transition")
	}
	return s.Patch(ctx, userID, id, patch)
}

// ListDueScheduled returns scheduled reminders whose due_at is now or past.
func (s *RemindersStore) ListDueScheduled(ctx context.Context, limit int) ([]Reminder, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("reminders_disabled")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	now := time.Now().UTC().Format(time.RFC3339)
	q := url.Values{}
	q.Set("select", s.selectColumns())
	q.Set("status", "eq."+ReminderStatusScheduled)
	q.Set("due_at", "lte."+now)
	q.Set("order", "due_at.asc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	var rows []Reminder
	if err := s.DB.Get(ctx, "reminders", q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []Reminder{}
	}
	return rows, nil
}

// ClaimFired marks a scheduled reminder as fired if it is still scheduled.
func (s *RemindersStore) ClaimFired(ctx context.Context, rem Reminder) (Reminder, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return Reminder{}, fmt.Errorf("reminders_disabled")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	q := url.Values{}
	q.Set("id", "eq."+rem.ID)
	q.Set("user_id", "eq."+rem.UserID)
	q.Set("status", "eq."+ReminderStatusScheduled)
	q.Set("select", s.selectColumns())
	var rows []Reminder
	if err := s.DB.PatchReturning(ctx, "reminders", q, map[string]any{
		"status":     ReminderStatusFired,
		"fired_at":   now,
		"updated_at": now,
	}, &rows); err != nil {
		return Reminder{}, err
	}
	if len(rows) == 0 {
		return Reminder{}, fmt.Errorf("reminder_claim_conflict")
	}
	return rows[0], nil
}

// FindSimilar returns a scheduled reminder with the same title due near dueAt.
func (s *RemindersStore) FindSimilar(ctx context.Context, userID, title string, dueAt time.Time, window time.Duration) (*Reminder, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("reminders_disabled")
	}
	title = strings.TrimSpace(title)
	if title == "" || dueAt.IsZero() {
		return nil, nil
	}
	if window <= 0 {
		window = 2 * time.Minute
	}
	start := dueAt.UTC().Add(-window).Format(time.RFC3339)
	end := dueAt.UTC().Add(window).Format(time.RFC3339)
	q := url.Values{}
	q.Set("select", s.selectColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("status", "eq."+ReminderStatusScheduled)
	q.Set("title", "eq."+title)
	q.Set("due_at", "gte."+start)
	q.Add("due_at", "lte."+end)
	q.Set("order", "due_at.asc")
	q.Set("limit", "1")
	var rows []Reminder
	if err := s.DB.Get(ctx, "reminders", q, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

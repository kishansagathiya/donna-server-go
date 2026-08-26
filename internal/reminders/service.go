package reminders

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/connectors/google"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const defaultLead = time.Hour

// Service creates and updates timed reminders.
type Service struct {
	Store       *storage.RemindersStore
	Preferences *storage.Preferences
}

func (s *Service) Create(ctx context.Context, userID string, in CreateInput) (storage.Reminder, error) {
	if s == nil || s.Store == nil || !s.Store.Enabled {
		return storage.Reminder{}, fmt.Errorf("reminders_disabled")
	}
	resolved, err := s.Resolve(ctx, userID, in)
	if err != nil {
		return storage.Reminder{}, err
	}
	if similar, findErr := s.Store.FindSimilar(ctx, userID, resolved.Title, resolved.DueAt, 2*time.Minute); findErr == nil && similar != nil {
		return *similar, nil
	}
	return s.Store.Create(ctx, userID, resolved)
}

// SetReminder implements agents.ReminderCreator.
func (s *Service) SetReminder(ctx context.Context, userID, title, when, notes, timezone string) (string, string, error) {
	rem, err := s.Create(ctx, userID, CreateInput{
		Title:    title,
		When:     when,
		Notes:    notes,
		Timezone: timezone,
	})
	if err != nil {
		return "", "", err
	}
	return rem.ID, rem.DueAt, nil
}

// CreateFromAction persists a confirmed propose_reminder builtin.
func (s *Service) CreateFromAction(ctx context.Context, userID, actionRunID string, input map[string]any) (map[string]any, error) {
	in := CreateInput{
		Title:    stringSlot(input, "title"),
		Notes:    firstNonEmpty(stringSlot(input, "notes"), stringSlot(input, "body")),
		When:     firstNonEmpty(stringSlot(input, "when"), stringSlot(input, "start")),
		Timezone: firstNonEmpty(stringSlot(input, "timezone"), stringSlot(input, "time_zone")),
	}
	if in.Title == "" {
		in.Title = stringSlot(input, "summary")
	}
	if actionRunID != "" {
		in.ActionRunID = &actionRunID
	}
	rem, err := s.Create(ctx, userID, in)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"type":        "propose_reminder",
		"scheduled":   true,
		"reminder_id": rem.ID,
		"title":       rem.Title,
		"notes":       rem.Notes,
		"when":        in.When,
		"due_at":      rem.DueAt,
		"timezone":    rem.Timezone,
	}, nil
}

type CreateInput struct {
	Title       string
	Notes       string
	When        string
	DueAt       *time.Time
	Timezone    string
	ActionRunID *string
}

func (s *Service) Resolve(ctx context.Context, userID string, in CreateInput) (storage.NewReminderInput, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return storage.NewReminderInput{}, fmt.Errorf("title_required")
	}
	tzName, loc, err := s.resolveTimezone(ctx, userID, in.Timezone)
	if err != nil {
		return storage.NewReminderInput{}, err
	}
	now := time.Now().In(loc)
	due, err := resolveDueAt(in, now, loc)
	if err != nil {
		return storage.NewReminderInput{}, err
	}
	return storage.NewReminderInput{
		Title:       title,
		Notes:       strings.TrimSpace(in.Notes),
		DueAt:       due,
		Timezone:    tzName,
		ActionRunID: in.ActionRunID,
	}, nil
}

func (s *Service) resolveTimezone(ctx context.Context, userID, explicit string) (string, *time.Location, error) {
	tzName := strings.TrimSpace(explicit)
	if tzName == "" && s.Preferences != nil {
		if pref, err := s.Preferences.GetTimezone(ctx, userID); err == nil {
			tzName = strings.TrimSpace(pref)
		}
	}
	if tzName == "" {
		return "", nil, fmt.Errorf("timezone_required")
	}
	loc, err := google.LoadTZ(tzName)
	if err != nil {
		return "", nil, err
	}
	return tzName, loc, nil
}

func resolveDueAt(in CreateInput, now time.Time, loc *time.Location) (time.Time, error) {
	if in.DueAt != nil && !in.DueAt.IsZero() {
		return in.DueAt.In(loc), nil
	}
	raw := strings.TrimSpace(in.When)
	if raw == "" {
		return now.Add(defaultLead), nil
	}
	parsed, ok := google.ParseWhen(raw, now, loc)
	if !ok {
		return time.Time{}, fmt.Errorf("unparseable_when:%s", raw)
	}
	return parsed, nil
}

func stringSlot(input map[string]any, key string) string {
	if input == nil {
		return ""
	}
	v, ok := input[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

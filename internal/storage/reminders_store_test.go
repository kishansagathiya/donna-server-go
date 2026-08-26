package storage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type reminderTable struct {
	mu   sync.Mutex
	rows []Reminder
}

func (t *reminderTable) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/reminders") {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			t.mu.Lock()
			defer t.mu.Unlock()
			matched := t.filterLocked(r)
			_ = json.NewEncoder(w).Encode(matched)
		case http.MethodPost:
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			row := reminderFromMap(body)
			if row.ID == "" {
				row.ID = "rem-1"
			}
			t.mu.Lock()
			t.rows = append(t.rows, row)
			t.mu.Unlock()
			_ = json.NewEncoder(w).Encode([]Reminder{row})
		case http.MethodPatch:
			raw, _ := io.ReadAll(r.Body)
			var patch map[string]any
			_ = json.Unmarshal(raw, &patch)
			t.mu.Lock()
			defer t.mu.Unlock()
			var updated []Reminder
			for i := range t.rows {
				if !matchReminder(t.rows[i], r) {
					continue
				}
				applyReminderPatch(&t.rows[i], patch)
				updated = append(updated, t.rows[i])
			}
			_ = json.NewEncoder(w).Encode(updated)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	})
}

func (t *reminderTable) filterLocked(r *http.Request) []Reminder {
	var out []Reminder
	for _, row := range t.rows {
		if matchReminder(row, r) {
			out = append(out, row)
		}
	}
	return out
}

func reminderFromMap(body map[string]any) Reminder {
	str := func(k string) string {
		v, _ := body[k].(string)
		return v
	}
	row := Reminder{
		ID:        str("id"),
		UserID:    str("user_id"),
		Title:     str("title"),
		Notes:     str("notes"),
		DueAt:     str("due_at"),
		Timezone:  str("timezone"),
		Status:    str("status"),
		CreatedAt: str("created_at"),
		UpdatedAt: str("updated_at"),
	}
	if v, ok := body["action_run_id"].(string); ok && v != "" {
		row.ActionRunID = &v
	}
	if row.Status == "" {
		row.Status = ReminderStatusScheduled
	}
	if row.CreatedAt == "" {
		row.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return row
}

func applyReminderPatch(row *Reminder, patch map[string]any) {
	if v, ok := patch["title"].(string); ok {
		row.Title = v
	}
	if v, ok := patch["notes"].(string); ok {
		row.Notes = v
	}
	if v, ok := patch["due_at"].(string); ok {
		row.DueAt = v
	}
	if v, ok := patch["timezone"].(string); ok {
		row.Timezone = v
	}
	if v, ok := patch["status"].(string); ok {
		row.Status = v
	}
	if v, ok := patch["updated_at"].(string); ok {
		row.UpdatedAt = v
	}
	if v, ok := patch["fired_at"].(string); ok {
		row.FiredAt = &v
	}
	if v, ok := patch["dismissed_at"].(string); ok {
		row.DismissedAt = &v
	}
}

func matchReminder(row Reminder, r *http.Request) bool {
	q := r.URL.Query()
	for key, vals := range q {
		switch key {
		case "select", "order", "limit", "offset":
			continue
		}
		for _, val := range vals {
			field := reminderField(row, key)
			switch {
			case strings.HasPrefix(val, "eq."):
				if field != strings.TrimPrefix(val, "eq.") {
					return false
				}
			case strings.HasPrefix(val, "in.("):
				inner := strings.TrimSuffix(strings.TrimPrefix(val, "in.("), ")")
				ok := false
				for _, p := range strings.Split(inner, ",") {
					if field == strings.TrimSpace(p) {
						ok = true
						break
					}
				}
				if !ok {
					return false
				}
			case strings.HasPrefix(val, "lte."):
				if field > strings.TrimPrefix(val, "lte.") {
					return false
				}
			case strings.HasPrefix(val, "gte."):
				if field < strings.TrimPrefix(val, "gte.") {
					return false
				}
			}
		}
	}
	return true
}

func reminderField(row Reminder, key string) string {
	switch key {
	case "id":
		return row.ID
	case "user_id":
		return row.UserID
	case "title":
		return row.Title
	case "status":
		return row.Status
	case "due_at":
		return row.DueAt
	default:
		return ""
	}
}

func testRemindersStore(t *testing.T, seed []Reminder) *RemindersStore {
	t.Helper()
	table := &reminderTable{rows: append([]Reminder(nil), seed...)}
	srv := httptest.NewServer(table.handler())
	t.Cleanup(srv.Close)
	return &RemindersStore{DB: NewSupabase(srv.URL, "test-key"), Enabled: true}
}

func sampleDue(t time.Time) Reminder {
	now := time.Now().UTC().Format(time.RFC3339)
	return Reminder{
		ID:        "rem-1",
		UserID:    "user-1",
		Title:     "Call Mom",
		Notes:     "landline",
		DueAt:     t.UTC().Format(time.RFC3339),
		Timezone:  "UTC",
		Status:    ReminderStatusScheduled,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestIsReminderStatus(t *testing.T) {
	if !IsReminderStatus(ReminderStatusScheduled) || !IsReminderStatus(ReminderStatusFired) {
		t.Fatal("expected known statuses")
	}
	if IsReminderStatus("open") || IsReminderStatus("") {
		t.Fatal("open/empty should not be a status")
	}
}

func TestRemindersStoreDisabled(t *testing.T) {
	s := &RemindersStore{}
	ctx := context.Background()
	due := time.Now().Add(time.Hour)
	if _, err := s.Create(ctx, "u", NewReminderInput{Title: "x", DueAt: due}); err == nil {
		t.Fatal("expected disabled")
	}
	if _, err := s.Get(ctx, "u", "id"); err == nil {
		t.Fatal("expected disabled")
	}
	if _, err := s.List(ctx, "u", "", 10, 0); err == nil {
		t.Fatal("expected disabled")
	}
	if _, err := s.ListDueScheduled(ctx, 5); err == nil {
		t.Fatal("expected disabled")
	}
	if _, err := s.ClaimFired(ctx, Reminder{ID: "x"}); err == nil {
		t.Fatal("expected disabled")
	}
	if _, err := s.FindSimilar(ctx, "u", "x", due, time.Minute); err == nil {
		t.Fatal("expected disabled")
	}
}

func TestRemindersStoreCreateValidation(t *testing.T) {
	s := testRemindersStore(t, nil)
	ctx := context.Background()
	if _, err := s.Create(ctx, "u", NewReminderInput{}); err == nil {
		t.Fatal("expected title_required")
	}
	if _, err := s.Create(ctx, "u", NewReminderInput{Title: strings.Repeat("a", 201), DueAt: time.Now()}); err == nil {
		t.Fatal("expected title_too_long")
	}
	if _, err := s.Create(ctx, "u", NewReminderInput{Title: "ok", Notes: strings.Repeat("n", 4001), DueAt: time.Now()}); err == nil {
		t.Fatal("expected notes_too_long")
	}
	if _, err := s.Create(ctx, "u", NewReminderInput{Title: "ok"}); err == nil {
		t.Fatal("expected due_at_required")
	}
}

func TestRemindersStoreCreateGetList(t *testing.T) {
	due := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	actionID := "act-1"
	s := testRemindersStore(t, nil)
	ctx := context.Background()
	created, err := s.Create(ctx, "user-1", NewReminderInput{
		Title:       "  Call Mom  ",
		Notes:       " landline ",
		DueAt:       due,
		Timezone:    "UTC",
		ActionRunID: &actionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Title != "Call Mom" || created.Notes != "landline" || created.Status != ReminderStatusScheduled {
		t.Fatalf("created: %#v", created)
	}

	got, err := s.Get(ctx, "user-1", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Call Mom" {
		t.Fatalf("get: %#v", got)
	}
	if _, err := s.Get(ctx, "user-1", "missing"); err == nil {
		t.Fatal("expected not_found")
	}

	open, err := s.List(ctx, "user-1", "open", 0, -1)
	if err != nil || len(open) != 1 {
		t.Fatalf("open list: %v %#v", err, open)
	}
	all, err := s.List(ctx, "user-1", "all", 200, 0)
	if err != nil || len(all) != 1 {
		t.Fatalf("all list: %v %#v", err, all)
	}
	scheduled, err := s.List(ctx, "user-1", "scheduled", 10, 0)
	if err != nil || len(scheduled) != 1 {
		t.Fatalf("scheduled: %v %#v", err, scheduled)
	}
	multi, err := s.List(ctx, "user-1", "scheduled,fired", 10, 0)
	if err != nil || len(multi) != 1 {
		t.Fatalf("multi: %v %#v", err, multi)
	}
	if _, err := s.List(ctx, "user-1", "nope", 10, 0); err == nil {
		t.Fatal("expected invalid_status")
	}
}

func TestRemindersStoreUpdateAndStatus(t *testing.T) {
	due := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	seed := sampleDue(due)
	s := testRemindersStore(t, []Reminder{seed})
	ctx := context.Background()

	title := "Pickup"
	notes := "door"
	tz := "America/Los_Angeles"
	later := due.Add(30 * time.Minute)
	updated, err := s.Update(ctx, "user-1", "rem-1", UpdateReminderInput{
		Title:    &title,
		Notes:    &notes,
		DueAt:    &later,
		Timezone: &tz,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Pickup" || updated.Timezone != tz {
		t.Fatalf("update: %#v", updated)
	}

	empty := ""
	if _, err := s.Update(ctx, "user-1", "rem-1", UpdateReminderInput{Title: &empty}); err == nil {
		t.Fatal("expected title_required")
	}
	long := strings.Repeat("x", 201)
	if _, err := s.Update(ctx, "user-1", "rem-1", UpdateReminderInput{Title: &long}); err == nil {
		t.Fatal("expected title_too_long")
	}
	longNotes := strings.Repeat("n", 4001)
	if _, err := s.Update(ctx, "user-1", "rem-1", UpdateReminderInput{Notes: &longNotes}); err == nil {
		t.Fatal("expected notes_too_long")
	}
	zero := time.Time{}
	if _, err := s.Update(ctx, "user-1", "rem-1", UpdateReminderInput{DueAt: &zero}); err == nil {
		t.Fatal("expected due_at_required")
	}
	noop, err := s.Update(ctx, "user-1", "rem-1", UpdateReminderInput{})
	if err != nil || noop.Title != "Pickup" {
		t.Fatalf("noop: %v %#v", err, noop)
	}

	if _, err := s.SetStatus(ctx, "user-1", "rem-1", "nope"); err == nil {
		t.Fatal("expected invalid_status")
	}
	same, err := s.SetStatus(ctx, "user-1", "rem-1", ReminderStatusScheduled)
	if err != nil || same.Status != ReminderStatusScheduled {
		t.Fatalf("same status: %v %#v", err, same)
	}
	cancelled, err := s.SetStatus(ctx, "user-1", "rem-1", ReminderStatusCancelled)
	if err != nil || cancelled.Status != ReminderStatusCancelled {
		t.Fatalf("cancel: %v %#v", err, cancelled)
	}
	if _, err := s.Update(ctx, "user-1", "rem-1", UpdateReminderInput{Notes: &notes}); err == nil {
		t.Fatal("expected not_editable")
	}
	if _, err := s.SetStatus(ctx, "user-1", "rem-1", ReminderStatusFired); err == nil {
		t.Fatal("expected not_firable")
	}
}

func TestRemindersStoreDismissAndFire(t *testing.T) {
	due := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	seed := sampleDue(due)
	s := testRemindersStore(t, []Reminder{seed})
	ctx := context.Background()

	fired, err := s.SetStatus(ctx, "user-1", "rem-1", ReminderStatusFired)
	if err != nil || fired.Status != ReminderStatusFired || fired.FiredAt == nil {
		t.Fatalf("fire: %v %#v", err, fired)
	}
	dismissed, err := s.SetStatus(ctx, "user-1", "rem-1", ReminderStatusDismissed)
	if err != nil || dismissed.Status != ReminderStatusDismissed || dismissed.DismissedAt == nil {
		t.Fatalf("dismiss: %v %#v", err, dismissed)
	}
	if _, err := s.SetStatus(ctx, "user-1", "rem-1", ReminderStatusCancelled); err == nil {
		t.Fatal("expected not_cancellable")
	}

	s2 := testRemindersStore(t, []Reminder{func() Reminder {
		r := sampleDue(due)
		r.Status = ReminderStatusCancelled
		return r
	}()})
	if _, err := s2.SetStatus(ctx, "user-1", "rem-1", ReminderStatusDismissed); err == nil {
		t.Fatal("expected not_dismissable")
	}
}

func TestRemindersStoreDueClaimSimilar(t *testing.T) {
	past := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	future := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	s := testRemindersStore(t, []Reminder{sampleDue(past), func() Reminder {
		r := sampleDue(future)
		r.ID = "rem-2"
		r.Title = "Later"
		return r
	}()})
	ctx := context.Background()

	due, err := s.ListDueScheduled(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != "rem-1" {
		t.Fatalf("due: %#v", due)
	}

	claimed, err := s.ClaimFired(ctx, due[0])
	if err != nil || claimed.Status != ReminderStatusFired {
		t.Fatalf("claim: %v %#v", err, claimed)
	}
	if _, err := s.ClaimFired(ctx, due[0]); err == nil {
		t.Fatal("expected claim conflict")
	}

	hit, err := s.FindSimilar(ctx, "user-1", "Later", future, 2*time.Minute)
	if err != nil || hit == nil || hit.ID != "rem-2" {
		t.Fatalf("similar: %v %#v", err, hit)
	}
	miss, err := s.FindSimilar(ctx, "user-1", "Later", future.Add(time.Hour), time.Minute)
	if err != nil || miss != nil {
		t.Fatalf("miss: %v %#v", err, miss)
	}
	none, err := s.FindSimilar(ctx, "user-1", "  ", future, 0)
	if err != nil || none != nil {
		t.Fatalf("empty title: %v %#v", err, none)
	}
}

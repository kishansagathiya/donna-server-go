package reminders

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type reminderTable struct {
	mu   sync.Mutex
	rows []storage.Reminder
}

func sampleReminder(due time.Time) storage.Reminder {
	now := time.Now().UTC().Format(time.RFC3339)
	return storage.Reminder{
		ID:        "rem-1",
		UserID:    "user-1",
		Title:     "Call Mom",
		Notes:     "landline",
		DueAt:     due.UTC().Format(time.RFC3339),
		Timezone:  "UTC",
		Status:    storage.ReminderStatusScheduled,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func testStore(t *testing.T, seed []storage.Reminder) *storage.RemindersStore {
	t.Helper()
	table := &reminderTable{rows: append([]storage.Reminder(nil), seed...)}
	srv := httptest.NewServer(table.handler())
	t.Cleanup(srv.Close)
	return &storage.RemindersStore{DB: storage.NewSupabase(srv.URL, "test-key"), Enabled: true}
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
			var matched []storage.Reminder
			for _, row := range t.rows {
				if matchReminder(row, r) {
					matched = append(matched, row)
				}
			}
			_ = json.NewEncoder(w).Encode(matched)
		case http.MethodPost:
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			row := reminderFromMap(body)
			if row.ID == "" {
				row.ID = "rem-new"
			}
			t.mu.Lock()
			t.rows = append(t.rows, row)
			t.mu.Unlock()
			_ = json.NewEncoder(w).Encode([]storage.Reminder{row})
		case http.MethodPatch:
			raw, _ := io.ReadAll(r.Body)
			var patch map[string]any
			_ = json.Unmarshal(raw, &patch)
			t.mu.Lock()
			defer t.mu.Unlock()
			var updated []storage.Reminder
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

func reminderFromMap(body map[string]any) storage.Reminder {
	str := func(k string) string {
		v, _ := body[k].(string)
		return v
	}
	row := storage.Reminder{
		UserID:    str("user_id"),
		Title:     str("title"),
		Notes:     str("notes"),
		DueAt:     str("due_at"),
		Timezone:  str("timezone"),
		Status:    str("status"),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: str("updated_at"),
	}
	if row.Status == "" {
		row.Status = storage.ReminderStatusScheduled
	}
	return row
}

func applyReminderPatch(row *storage.Reminder, patch map[string]any) {
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
	if v, ok := patch["fired_at"].(string); ok {
		row.FiredAt = &v
	}
	if v, ok := patch["dismissed_at"].(string); ok {
		row.DismissedAt = &v
	}
}

func matchReminder(row storage.Reminder, r *http.Request) bool {
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

func reminderField(row storage.Reminder, key string) string {
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

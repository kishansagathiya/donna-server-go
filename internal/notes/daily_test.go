package notes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestDailyChecker_fallbackBriefing(t *testing.T) {
	today := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	notes := []storage.NoteSummary{
		{ID: "1", Title: "Ship feature", IsUrgent: true, IsImportant: true, NoteDate: today.Format(time.RFC3339)},
		{ID: "2", Title: "Plan Q3", IsImportant: true, NoteDate: today.Format(time.RFC3339)},
		{ID: "3", Title: "Reply email", IsUrgent: true, NoteDate: today.Format(time.RFC3339)},
		{ID: "4", Title: "Old note", NoteDate: today.AddDate(0, 0, -45).Format(time.RFC3339)},
	}

	dc := &DailyChecker{}
	briefing := dc.fallbackBriefing(notes, today)

	if len(briefing.Tasks) != 3 {
		t.Fatalf("tasks = %d, want 3", len(briefing.Tasks))
	}
	if briefing.Tasks[0].Priority != "do_first" {
		t.Fatalf("first task priority = %q, want do_first", briefing.Tasks[0].Priority)
	}
	if len(briefing.Outdated) != 1 || briefing.Outdated[0].NoteID != "4" {
		t.Fatalf("outdated = %#v, want note 4", briefing.Outdated)
	}
	if briefing.Summary == "" {
		t.Fatal("expected non-empty summary")
	}
}

func TestDailyChecker_Check_emptyNotes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "notes") {
			_ = json.NewEncoder(w).Encode([]storage.NoteSummary{})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	dc := &DailyChecker{
		Store: &storage.Notes{
			DB:      storage.NewSupabase(srv.URL, "test-key"),
			Enabled: true,
		},
	}

	briefing, err := dc.Check(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(briefing.Tasks) != 0 || len(briefing.Outdated) != 0 {
		t.Fatalf("expected empty briefing, got %#v", briefing)
	}
	if briefing.Summary == "" {
		t.Fatal("expected summary for empty notes")
	}
}

func TestHandler_DailyCheck_missingToken(t *testing.T) {
	h := &Handler{Daily: &DailyChecker{Store: &storage.Notes{Enabled: true}}}
	req := httptest.NewRequest(http.MethodPost, "/notes/daily-check", nil)
	rec := httptest.NewRecorder()

	h.DailyCheck(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestNormalizePriority(t *testing.T) {
	cases := map[string]string{
		"do_first": "do_first",
		"Do-First": "do_first",
		"schedule": "schedule",
		"delegate": "delegate",
		"unknown":  "schedule",
	}
	for input, want := range cases {
		if got := normalizePriority(input); got != want {
			t.Fatalf("normalizePriority(%q) = %q, want %q", input, got, want)
		}
	}
}

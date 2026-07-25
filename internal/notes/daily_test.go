package notes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestTasksFromNotes_priorityOrder(t *testing.T) {
	doFirst := []storage.NoteSummary{
		{ID: "1", Title: "Ship feature", IsUrgent: true, IsImportant: true},
	}
	schedule := []storage.NoteSummary{
		{ID: "2", Title: "Plan Q3", IsImportant: true},
	}
	delegate := []storage.NoteSummary{
		{ID: "3", Title: "Reply email", IsUrgent: true},
	}
	later := []storage.NoteSummary{
		{ID: "4", Title: "Nice to have"},
	}

	tasks := append(
		tasksFromNotes(doFirst, PriorityDoFirst, "do first"),
		tasksFromNotes(schedule, PrioritySchedule, "schedule")...,
	)
	tasks = append(tasks, tasksFromNotes(delegate, PriorityDelegate, "delegate")...)
	tasks = append(tasks, tasksFromNotes(later, PriorityLater, "later")...)

	if len(tasks) != 4 {
		t.Fatalf("tasks = %d, want 4", len(tasks))
	}
	want := []string{PriorityDoFirst, PrioritySchedule, PriorityDelegate, PriorityLater}
	for i, p := range want {
		if tasks[i].Priority != p {
			t.Fatalf("tasks[%d].Priority = %q, want %q", i, tasks[i].Priority, p)
		}
	}
}

func TestTodaySummary(t *testing.T) {
	got := todaySummary(2, 1, 0, 3)
	if !strings.Contains(got, "2 to do first") || !strings.Contains(got, "1 to schedule") || !strings.Contains(got, "3 for later") {
		t.Fatalf("unexpected summary: %q", got)
	}
	if todaySummary(0, 0, 0, 0) != "Nothing on your list today." {
		t.Fatalf("empty summary unexpected: %q", todaySummary(0, 0, 0, 0))
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

func TestDailyChecker_Check_ordersByEisenhower(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q, _ := url.ParseQuery(r.URL.RawQuery)
		urgent := q.Get("is_urgent")
		important := q.Get("is_important")

		var notes []storage.NoteSummary
		switch {
		case urgent == "eq.true" && important == "eq.true":
			notes = []storage.NoteSummary{{ID: "do", Title: "Do first", IsUrgent: true, IsImportant: true}}
		case urgent == "eq.false" && important == "eq.true":
			notes = []storage.NoteSummary{{ID: "sched", Title: "Schedule", IsImportant: true}}
		case urgent == "eq.true" && important == "eq.false":
			notes = []storage.NoteSummary{{ID: "del", Title: "Delegate", IsUrgent: true}}
		case urgent == "eq.false" && important == "eq.false":
			notes = []storage.NoteSummary{{ID: "later", Title: "Later"}}
		}
		_ = json.NewEncoder(w).Encode(notes)
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
	if len(briefing.Tasks) != 3 {
		t.Fatalf("tasks = %d, want 3 (later notes are excluded from Today)", len(briefing.Tasks))
	}
	want := []string{PriorityDoFirst, PrioritySchedule, PriorityDelegate}
	for i, p := range want {
		if briefing.Tasks[i].Priority != p {
			t.Fatalf("tasks[%d].Priority = %q, want %q", i, briefing.Tasks[i].Priority, p)
		}
	}
	if briefing.Outdated == nil || len(briefing.Outdated) != 0 {
		t.Fatalf("outdated should be empty, got %#v", briefing.Outdated)
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
		"do_first":  PriorityDoFirst,
		"Do-First":  PriorityDoFirst,
		"schedule":  PrioritySchedule,
		"delegate":  PriorityDelegate,
		"later":     PriorityLater,
		"eliminate": PriorityLater,
		"unknown":   PrioritySchedule,
	}
	for input, want := range cases {
		if got := normalizePriority(input); got != want {
			t.Fatalf("normalizePriority(%q) = %q, want %q", input, got, want)
		}
	}
}

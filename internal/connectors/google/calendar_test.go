package google

import (
	"testing"
	"time"
)

func TestResolveEventWindowParsesRFC3339(t *testing.T) {
	start, end, note, err := resolveEventWindow(map[string]any{
		"start": "2026-08-01T15:00:00Z",
		"end":   "2026-08-01T16:30:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Fatalf("unexpected note: %q", note)
	}
	if !start.Equal(time.Date(2026, 8, 1, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("start: %s", start)
	}
	if !end.Equal(time.Date(2026, 8, 1, 16, 30, 0, 0, time.UTC)) {
		t.Fatalf("end: %s", end)
	}
}

func TestResolveEventWindowFreeTextKeepsNote(t *testing.T) {
	start, end, note, err := resolveEventWindow(map[string]any{
		"when": "tomorrow afternoon",
	})
	if err != nil {
		t.Fatal(err)
	}
	if note != "tomorrow afternoon" {
		t.Fatalf("note: %q", note)
	}
	if !end.After(start) {
		t.Fatalf("end should be after start: %s %s", start, end)
	}
}

func TestParseAttendees(t *testing.T) {
	got := parseAttendees(map[string]any{
		"attendees": "a@example.com, b@example.com",
	})
	if len(got) != 2 || got[0].Email != "a@example.com" || got[1].Email != "b@example.com" {
		t.Fatalf("got %#v", got)
	}
}

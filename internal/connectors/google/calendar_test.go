package google

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestResolveEventWindowParsesRFC3339(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, loc)
	start, end, note, err := resolveEventWindow(map[string]any{
		"start": "2026-08-01T15:00:00Z",
		"end":   "2026-08-01T16:30:00Z",
	}, now, loc)
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

func TestResolveEventWindowParsesZonelessInLocation(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, loc)
	start, end, note, err := resolveEventWindow(map[string]any{
		"when": "2026-08-01 15:00",
	}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Fatalf("unexpected note: %q", note)
	}
	want := time.Date(2026, 8, 1, 15, 0, 0, 0, loc)
	if !start.Equal(want) {
		t.Fatalf("start: got %s want %s", start, want)
	}
	if !end.Equal(want.Add(time.Hour)) {
		t.Fatalf("end: %s", end)
	}
}

func TestResolveEventWindowParsesTomorrowAfternoon(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, loc) // Monday
	start, _, note, err := resolveEventWindow(map[string]any{
		"when": "tomorrow afternoon",
	}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Fatalf("unexpected note: %q", note)
	}
	want := time.Date(2026, 7, 28, 15, 0, 0, 0, loc)
	if !start.Equal(want) {
		t.Fatalf("start: got %s want %s", start, want)
	}
}

func TestResolveEventWindowParsesTomorrowAt3PM(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, loc)
	start, _, note, err := resolveEventWindow(map[string]any{
		"when": "tomorrow at 3pm",
	}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Fatalf("unexpected note: %q", note)
	}
	want := time.Date(2026, 7, 28, 15, 0, 0, 0, loc)
	if !start.Equal(want) {
		t.Fatalf("start: got %s want %s", start, want)
	}
}

func TestResolveEventWindowUnknownKeepsNote(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 7, 27, 12, 30, 0, 0, loc)
	start, end, note, err := resolveEventWindow(map[string]any{
		"when": "whenever works for the team",
	}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	if note != "whenever works for the team" {
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

	got = parseAttendees(map[string]any{
		"guests": "Alice <alice@example.com>; bob@example.com",
	})
	if len(got) != 2 || got[0].Email != "alice@example.com" || got[1].Email != "bob@example.com" {
		t.Fatalf("guests: %#v", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCreateEventSendsInvitesAndUsesTimezone(t *testing.T) {
	locName := "America/Los_Angeles"
	var createReq *http.Request
	var createBody []byte

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/calendar/v3/calendars/primary":
			body := `{"timeZone":"America/Los_Angeles"}`
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(body)),
				Header:     make(http.Header),
			}, nil
		case r.Method == http.MethodPost && r.URL.Path == "/calendar/v3/calendars/primary/events":
			createReq = r
			b, _ := io.ReadAll(r.Body)
			createBody = b
			body := `{"id":"evt_1","htmlLink":"https://calendar.google.com/event?eid=1","status":"confirmed"}`
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(body)),
				Header:     make(http.Header),
			}, nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
			return nil, nil
		}
	})}

	a := &Adapter{HTTPClient: client}
	out, err := a.createEvent(context.Background(), "token", map[string]any{
		"title":     "Sync with Alex",
		"when":      "tomorrow at 3pm",
		"attendees": "alex@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if createReq == nil {
		t.Fatal("expected create request")
	}
	if createReq.URL.Query().Get("sendUpdates") != "all" {
		t.Fatalf("expected sendUpdates=all, got %q", createReq.URL.RawQuery)
	}
	var payload calendarEventRequest
	if err := json.Unmarshal(createBody, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Start.TimeZone != locName {
		t.Fatalf("timezone: %q", payload.Start.TimeZone)
	}
	if len(payload.Attendees) != 1 || payload.Attendees[0].Email != "alex@example.com" {
		t.Fatalf("attendees: %#v", payload.Attendees)
	}
	if out["invites"] != true {
		t.Fatalf("expected invites=true, got %#v", out["invites"])
	}
	start, ok := out["start"].(string)
	if !ok || start == "" {
		t.Fatalf("missing start in output: %#v", out)
	}
	parsed, err := time.Parse(time.RFC3339, start)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Hour() != 15 {
		t.Fatalf("expected 3pm local, got %s", start)
	}
}

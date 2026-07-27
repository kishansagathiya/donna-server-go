package google

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestResolveEventWindowParsesRFC3339(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, loc)
	start, end, err := resolveEventWindow(map[string]any{
		"start": "2026-08-01T15:00:00Z",
		"end":   "2026-08-01T16:30:00Z",
	}, now, loc)
	if err != nil {
		t.Fatal(err)
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
	start, end, err := resolveEventWindow(map[string]any{
		"when": "2026-08-01 15:00",
	}, now, loc)
	if err != nil {
		t.Fatal(err)
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
	start, _, err := resolveEventWindow(map[string]any{
		"when": "tomorrow afternoon",
	}, now, loc)
	if err != nil {
		t.Fatal(err)
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
	start, _, err := resolveEventWindow(map[string]any{
		"when": "tomorrow at 3pm",
	}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 28, 15, 0, 0, 0, loc)
	if !start.Equal(want) {
		t.Fatalf("start: got %s want %s", start, want)
	}
}

func TestResolveEventWindowParsesJuly28At4PM(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 11, 23, 0, 0, loc)
	start, _, err := resolveEventWindow(map[string]any{
		"when": "July 28, 2026 at 4PM",
	}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 28, 16, 0, 0, 0, loc)
	if !start.Equal(want) {
		t.Fatalf("start: got %s want %s", start, want)
	}
}

func TestResolveEventWindowParsesTomorrow4PM(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 11, 23, 0, 0, loc)
	start, _, err := resolveEventWindow(map[string]any{
		"when": "tomorrow 4pm",
	}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 28, 16, 0, 0, 0, loc)
	if !start.Equal(want) {
		t.Fatalf("start: got %s want %s", start, want)
	}
}

func TestResolveEventWindowPrefersWhenOverBadStart(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 11, 23, 0, 0, loc)
	start, _, err := resolveEventWindow(map[string]any{
		"start": "2026-07-27T14:00:00Z",
		"when":  "tomorrow 4pm",
	}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 28, 16, 0, 0, 0, loc)
	if !start.Equal(want) {
		t.Fatalf("got %s want %s (should prefer relative when)", start, want)
	}
}

func TestResolveEventWindowParsesTimeRange(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 11, 0, 0, 0, loc)
	start, end, err := resolveEventWindow(map[string]any{
		"when": "tomorrow 1:00 PM - 2:00 PM",
	}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	wantStart := time.Date(2026, 7, 28, 13, 0, 0, 0, loc)
	wantEnd := time.Date(2026, 7, 28, 14, 0, 0, 0, loc)
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Fatalf("got %s-%s want %s-%s", start, end, wantStart, wantEnd)
	}
}

func TestResolveEventWindowUsesSummaryWhenWhenIsClockOnly(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 11, 30, 0, 0, loc)
	start, _, err := resolveEventWindow(map[string]any{
		"when":    "4pm",
		"summary": "I need to meet Radhika on 28 July, 2026 on 4PM",
	}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 28, 16, 0, 0, 0, loc)
	if !start.Equal(want) {
		t.Fatalf("got %s want %s", start, want)
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
	locName := "Asia/Kolkata"
	var createReq *http.Request
	var createBody []byte

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/users/me/settings/timezone"):
			body := `{"id":"timezone","value":"Asia/Kolkata"}`
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(body)),
				Header:     make(http.Header),
			}, nil
		case r.Method == http.MethodGet && r.URL.Path == "/calendar/v3/calendars/primary":
			body := `{"timeZone":"UTC"}`
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
		"when":      "July 28, 2026 at 4PM",
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
	if payload.Start.DateTime != "2026-07-28T16:00:00" {
		t.Fatalf("expected floating local datetime, got %q", payload.Start.DateTime)
	}
	if strings.Contains(payload.Start.DateTime, "Z") || strings.Contains(payload.Start.DateTime, "+") {
		t.Fatalf("dateTime should not include offset: %q", payload.Start.DateTime)
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
	if parsed.Day() != 28 || parsed.Hour() != 16 {
		t.Fatalf("expected July 28 4pm local, got %s", start)
	}
}

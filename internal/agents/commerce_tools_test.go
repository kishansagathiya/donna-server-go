package agents

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type fakeFactWriter struct {
	last storage.NewFactInput
	err  error
}

func (f *fakeFactWriter) InsertFactReturning(ctx context.Context, userID string, input storage.NewFactInput) (storage.KbFact, error) {
	if f.err != nil {
		return storage.KbFact{}, f.err
	}
	f.last = input
	return storage.KbFact{ID: "fact-1", UserID: userID, Fact: input.Fact, Active: true}, nil
}

type fakeCalendar struct {
	last  CalendarEventProposal
	err   error
	calls int
}

func (f *fakeCalendar) Propose(ctx context.Context, userID, agentRunID string, event CalendarEventProposal) (string, string, error) {
	f.calls++
	f.last = event
	if f.err != nil {
		return "", "", f.err
	}
	return "ar-1", "in-1", nil
}

func TestSearchFlightsUnconfigured(t *testing.T) {
	tool := searchFlightsTool()
	res, err := tool.Handle(context.Background(), &RunContext{UserID: "u1"}, `{"origin":"sfo","destination":"lis","depart_date":"2026-09-01"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "No live flight-search partner") {
		t.Fatalf("content: %s", res.Content)
	}
	if res.Meta["provider"] != "unconfigured" {
		t.Fatalf("meta: %#v", res.Meta)
	}
	if _, ok := DefaultToolsets(nil, nil, "", nil, nil).Get("search_flights"); !ok {
		t.Fatal("search_flights should register by default")
	}
}

func TestWriteMemoryFactRejectsCardLike(t *testing.T) {
	w := &fakeFactWriter{}
	tool := writeMemoryFactTool(w)
	res, err := tool.Handle(context.Background(), &RunContext{UserID: "u1"}, `{"fact":"card 4111111111111111"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "Refused") {
		t.Fatalf("expected refuse, got %s", res.Content)
	}
	if w.last.Fact != "" {
		t.Fatal("should not persist")
	}
}

func TestWriteMemoryFactSaves(t *testing.T) {
	w := &fakeFactWriter{}
	tool := writeMemoryFactTool(w)
	res, err := tool.Handle(context.Background(), &RunContext{UserID: "u1"}, `{"fact":"Prefers United, aisle seat","topic":"travel"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "Saved memory fact") {
		t.Fatalf("content: %s", res.Content)
	}
	if w.last.Fact != "Prefers United, aisle seat" {
		t.Fatalf("stored: %#v", w.last)
	}
}

func TestProposeCalendarEvent(t *testing.T) {
	cal := &fakeCalendar{}
	tool := proposeCalendarEventTool(cal)
	res, err := tool.Handle(context.Background(), &RunContext{UserID: "u1", RunID: "run-1"}, `{"title":"SFO to LIS","when":"2026-09-01 10:15"}`)
	if err != nil {
		t.Fatal(err)
	}
	if cal.calls != 1 || cal.last.Title != "SFO to LIS" {
		t.Fatalf("propose: %#v", cal.last)
	}
	if !strings.Contains(res.Content, "Actions") {
		t.Fatalf("content: %s", res.Content)
	}
}

type fakeReminders struct {
	title string
	when  string
	err   error
}

func (f *fakeReminders) SetReminder(ctx context.Context, userID, title, when, notes, timezone string) (string, string, error) {
	f.title = title
	f.when = when
	if f.err != nil {
		return "", "", f.err
	}
	return "rem-1", "2026-08-26T18:00:00Z", nil
}

func TestSetReminderTool(t *testing.T) {
	fake := &fakeReminders{}
	reg := DefaultToolsets(nil, nil, "", nil, nil, Phase3Tools{Reminders: fake})
	tool, ok := reg.Get("set_reminder")
	if !ok {
		t.Fatal("expected set_reminder tool")
	}
	res, err := tool.Handle(context.Background(), &RunContext{UserID: "u1"}, `{"title":"Call Mom","when":"tomorrow 4pm"}`)
	if err != nil {
		t.Fatal(err)
	}
	if fake.title != "Call Mom" || fake.when != "tomorrow 4pm" {
		t.Fatalf("got title=%q when=%q", fake.title, fake.when)
	}
	if !strings.Contains(res.Content, "Call Mom") {
		t.Fatalf("content: %s", res.Content)
	}
}

func TestNextScheduleAt(t *testing.T) {
	from := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	if _, ok := storage.NextScheduleAt(0, from); ok {
		t.Fatal("once should not reschedule")
	}
	next, ok := storage.NextScheduleAt(1440, from)
	if !ok || !next.Equal(from.Add(24*time.Hour)) {
		t.Fatalf("daily: %v %v", next, ok)
	}
}

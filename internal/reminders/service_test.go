package reminders

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/connectors/google"
)

func TestResolveDueAtParsesRelativeAndEnglish(t *testing.T) {
	loc, err := google.LoadTZ("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, loc)

	got, err := resolveDueAt(CreateInput{When: "in 10 minutes"}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := now.Add(10 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("in 10 minutes: got %s want %s", got, want)
	}

	got, err = resolveDueAt(CreateInput{When: "tomorrow 4pm"}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	want = time.Date(2026, 8, 27, 16, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("tomorrow 4pm: got %s want %s", got, want)
	}

	got, err = resolveDueAt(CreateInput{}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("default: got %s want %s", got, now.Add(time.Hour))
	}
}

func TestResolveDueAtRejectsGarbage(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, loc)
	if _, err := resolveDueAt(CreateInput{When: "whenever"}, now, loc); err == nil {
		t.Fatal("expected unparseable_when")
	}
}

func TestResolveDueAtUsesExplicitDueAt(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, loc)
	due := time.Date(2026, 9, 1, 9, 0, 0, 0, loc)
	got, err := resolveDueAt(CreateInput{DueAt: &due, When: "tomorrow"}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(due) {
		t.Fatalf("got %s want %s", got, due)
	}
}

func TestServiceResolveRequiresTitleAndTimezone(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	if _, err := s.Resolve(ctx, "user-1", CreateInput{Timezone: "UTC"}); err == nil || !strings.Contains(err.Error(), "title_required") {
		t.Fatalf("title: %v", err)
	}
	if _, err := s.Resolve(ctx, "user-1", CreateInput{Title: "x"}); err == nil || !strings.Contains(err.Error(), "timezone_required") {
		t.Fatalf("tz: %v", err)
	}
	got, err := s.Resolve(ctx, "user-1", CreateInput{Title: " Stretch ", Notes: " now ", When: "in 10 minutes", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Stretch" || got.Notes != "now" || got.Timezone != "UTC" || got.DueAt.IsZero() {
		t.Fatalf("resolved: %#v", got)
	}
}

func TestServiceCreateDisabledAndFromAction(t *testing.T) {
	ctx := context.Background()
	s := &Service{}
	if _, err := s.Create(ctx, "user-1", CreateInput{Title: "x"}); err == nil {
		t.Fatal("expected disabled")
	}

	store := testStore(t, nil)
	s = &Service{Store: store}
	runID := "act-1"
	out, err := s.CreateFromAction(ctx, "user-1", runID, map[string]any{
		"summary":  "Pickup dry cleaning",
		"body":     "ticket 12",
		"start":    "in 10 minutes",
		"timezone": "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["scheduled"] != true || out["title"] != "Pickup dry cleaning" {
		t.Fatalf("action: %#v", out)
	}

	id, dueAt, err := s.SetReminder(ctx, "user-1", "Water plants", "in 10 minutes", "", "UTC")
	if err != nil || id == "" || dueAt == "" {
		t.Fatalf("set: %q %q %v", id, dueAt, err)
	}

	// Duplicate within 2 minutes returns the existing row.
	again, err := s.Create(ctx, "user-1", CreateInput{Title: "Water plants", When: "in 10 minutes", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != id {
		t.Fatalf("expected similar reuse, got %s want %s", again.ID, id)
	}

	if _, _, err := s.SetReminder(ctx, "user-1", "", "in 10 minutes", "", "UTC"); err == nil {
		t.Fatal("expected title_required")
	}
}

func TestStringSlotAndFirstNonEmpty(t *testing.T) {
	if stringSlot(nil, "x") != "" {
		t.Fatal("nil map")
	}
	if stringSlot(map[string]any{"n": 3}, "n") != "3" {
		t.Fatal("non-string")
	}
	if firstNonEmpty("", "  b  ", "c") != "b" {
		t.Fatal("firstNonEmpty")
	}
	if firstNonEmpty("", "  ") != "" {
		t.Fatal("all empty")
	}
}

package reminders

import (
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

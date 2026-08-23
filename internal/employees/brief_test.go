package employees

import (
	"testing"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestShiftBrief(t *testing.T) {
	brief := ShiftBrief(storage.AIEmployee{
		Name:            "Alex",
		Role:            "Researcher",
		Goal:            "Map YC competitors",
		ProgressSummary: "Listed 3 firms",
	}, 2)
	if !containsAll(brief, []string{"Alex", "Researcher", "Map YC competitors", "Listed 3 firms", "shift #2", "report_progress", "complete_goal"}) {
		t.Fatalf("brief missing expected content:\n%s", brief)
	}
}

func TestDisplayGoal(t *testing.T) {
	got := DisplayGoal(storage.AIEmployee{Name: "Alex", Goal: "Find leads"})
	if got != "[Alex] Find leads" {
		t.Fatalf("got %q", got)
	}
}

func TestNextShiftAtContinuous(t *testing.T) {
	from := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	next := storage.NextShiftAt(0, from)
	if next.Sub(from) != storage.ContinuousShiftCooldown {
		t.Fatalf("cooldown=%s", next.Sub(from))
	}
	hourly := storage.NextShiftAt(60, from)
	if hourly.Sub(from) != time.Hour {
		t.Fatalf("hourly=%s", hourly.Sub(from))
	}
}

func containsAll(s string, parts []string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || stringIndex(s, sub) >= 0)
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

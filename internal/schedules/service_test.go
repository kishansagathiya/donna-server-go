package schedules

import (
	"strings"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestRunBriefIncludesLastSummary(t *testing.T) {
	sch := storage.ScheduledAgentGoal{
		Title:          "Monday brief",
		Goal:           "Catch me up",
		CadenceMinutes: 10080,
		LastSummary:    "Covered Q3 pipeline.",
	}
	brief := RunBrief(sch)
	if !strings.Contains(brief, "every 10080 minutes") {
		t.Fatalf("cadence: %s", brief)
	}
	if !strings.Contains(brief, "Covered Q3 pipeline.") {
		t.Fatalf("last summary: %s", brief)
	}
	if !strings.Contains(DisplayGoal(sch), "Monday brief") {
		t.Fatalf("display: %s", DisplayGoal(sch))
	}
}

func TestRunBriefOnce(t *testing.T) {
	brief := RunBrief(storage.ScheduledAgentGoal{
		Title: "Watch fares",
		Goal:  "Alert if SFO-LIS drops under $600",
	})
	if !strings.Contains(brief, "one-shot") {
		t.Fatalf("once: %s", brief)
	}
}

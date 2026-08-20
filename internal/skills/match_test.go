package skills

import "testing"

func TestMatchRanksRelevantSkillFirst(t *testing.T) {
	candidates := []Skill{
		{Name: "memory-first", Description: "Prefer the user's Donna memory and notes for anything personal"},
		{Name: "web-research", Description: "How Donna researches open-ended questions on the web"},
		{Name: "flight-booking-prefs", Description: "Airline, seat, and budget preferences for booking flights"},
	}
	got := Match("book me a flight to SFO next week using my usual airline preferences", candidates, 5)
	if len(got) == 0 {
		t.Fatal("expected matches")
	}
	if got[0].Skill.Name != "flight-booking-prefs" {
		t.Fatalf("top match: %s (score %.1f)", got[0].Skill.Name, got[0].Score)
	}
}

func TestMatchHyphenNameTokenized(t *testing.T) {
	candidates := []Skill{
		{Name: "web-research", Description: "research procedure"},
		{Name: "other", Description: "unrelated"},
	}
	got := Match("deep research on this topic", candidates, 5)
	if len(got) == 0 || got[0].Skill.Name != "web-research" {
		t.Fatalf("expected web-research via part-token, got %#v", got)
	}
}

func TestMatchFullSkillNameInGoalScoresHighest(t *testing.T) {
	candidates := []Skill{
		{Name: "web-research", Description: "research"},
		{Name: "research-protocol", Description: "research"},
	}
	got := Match("use web-research for this", candidates, 5)
	if len(got) == 0 || got[0].Skill.Name != "web-research" {
		t.Fatalf("exact name should outrank, got %#v", got)
	}
}

func TestMatchNoOverlapNoMatches(t *testing.T) {
	candidates := []Skill{
		{Name: "web-research", Description: "researching the web"},
	}
	if got := Match("cook pasta tonight", candidates, 5); len(got) != 0 {
		t.Fatalf("expected no matches, got %#v", got)
	}
	if got := Match("", candidates, 5); len(got) != 0 {
		t.Fatalf("empty goal should match nothing, got %#v", got)
	}
}

func TestMatchRespectsLimit(t *testing.T) {
	candidates := []Skill{
		{Name: "alpha", Description: "topic"},
		{Name: "beta", Description: "topic"},
		{Name: "gamma", Description: "topic"},
	}
	if got := Match("topic research", candidates, 2); len(got) != 2 {
		t.Fatalf("limit not respected: %d", len(got))
	}
}

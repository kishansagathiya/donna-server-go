package storage

import "testing"

func TestNormalizeMemoryKindForUI(t *testing.T) {
	if got := NormalizeMemoryKindForUI("habit"); got != MemoryKindRoutine {
		t.Fatalf("habit -> %q", got)
	}
	if got := NormalizeMemoryKindForUI("constraint"); got != MemoryKindConstraint {
		t.Fatalf("constraint -> %q", got)
	}
	if got := NormalizeMemoryKindForUI("fact"); got != MemoryKindOther {
		t.Fatalf("fact -> %q", got)
	}
}

func TestProjectProfileSummary(t *testing.T) {
	identity := "identity"
	pref := "preference"
	goal := "goal"
	facts := []MemoryFact{
		{Active: true, ReviewStatus: ReviewActive, MemoryKind: &identity, Fact: "Name is Alex."},
		{Active: true, ReviewStatus: ReviewActive, MemoryKind: &pref, Fact: "Prefers tea."},
		{Active: true, ReviewStatus: ReviewActive, MemoryKind: &goal, Fact: "Ship Notes V2."},
		{Active: false, ReviewStatus: ReviewActive, MemoryKind: &pref, Fact: "Inactive."},
		{Active: true, ReviewStatus: ReviewPendingReview, MemoryKind: &pref, Fact: "Pending."},
	}
	got := ProjectProfileSummary(facts)
	want := "Name is Alex. Prefers tea. Ship Notes V2."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

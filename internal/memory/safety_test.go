package memory

import (
	"testing"
)

func TestRejectUnsafe_credentials(t *testing.T) {
	cases := []Candidate{
		{Fact: "My password is: hunter2", Explicit: true, Confidence: 0.99},
		{Fact: "api_key=sk-abcdefghijklmnopqrstuvwxyz", Explicit: true, Confidence: 0.99},
		{Fact: "store this ghp_abcdefghijklmnopqrstuv", Explicit: true, Confidence: 0.99},
		{Fact: "credit card 4111", Explicit: true, Confidence: 0.99, Predicate: "payment"},
	}
	for _, c := range cases {
		if got := RejectUnsafe(c); got != RejectCredential {
			t.Errorf("RejectUnsafe(%q)=%q want credential", c.Fact, got)
		}
	}
}

func TestRejectUnsafe_inferredProtectedTraits(t *testing.T) {
	c := Candidate{
		Fact:       "User appears to be Christian based on context",
		Predicate:  "religion",
		Explicit:   false,
		Confidence: 0.95,
	}
	if got := RejectUnsafe(c); got != RejectProtectedTrait {
		t.Fatalf("got %q want protected trait", got)
	}
	// Explicit self-description still rejected by predicate when not explicit flag —
	// only explicit=true + non-inferred path allows through RejectUnsafe.
	c.Explicit = true
	if got := RejectUnsafe(c); got != RejectNone {
		t.Fatalf("explicit protected self-description should pass hard filter, got %q", got)
	}
}

func TestConfidenceGating(t *testing.T) {
	high := Candidate{
		Kind: "preference", Predicate: "likes", Fact: "User likes oat milk",
		Explicit: true, Sensitivity: "normal", Confidence: 0.95,
	}
	if !CanAutoActivate(high, false) {
		t.Fatal("expected auto-activate for high-confidence explicit normal")
	}
	if NeedsReview(high, false) {
		t.Fatal("high confidence should not need review")
	}

	mid := high
	mid.Confidence = 0.75
	if CanAutoActivate(mid, false) {
		t.Fatal("mid confidence must not auto-activate")
	}
	if !NeedsReview(mid, false) {
		t.Fatal("mid confidence should need review")
	}

	low := high
	low.Confidence = 0.4
	if ShouldDiscard(low) != RejectLowConfidence {
		t.Fatal("low confidence should discard")
	}
	if CanAutoActivate(low, false) || NeedsReview(low, false) {
		t.Fatal("discarded candidates must not activate or review")
	}

	sensitive := high
	sensitive.Sensitivity = "sensitive"
	if CanAutoActivate(sensitive, false) {
		t.Fatal("sensitive must not auto-activate")
	}
	if !NeedsReview(sensitive, false) {
		t.Fatal("sensitive should need review")
	}

	conflict := high
	if CanAutoActivate(conflict, true) {
		t.Fatal("conflicting must not auto-activate")
	}
	if !NeedsReview(conflict, true) {
		t.Fatal("conflicting should need review")
	}

	ephemeral := high
	ephemeral.Ephemeral = true
	if ShouldDiscard(ephemeral) != RejectEphemeral {
		t.Fatal("ephemeral should discard")
	}
}

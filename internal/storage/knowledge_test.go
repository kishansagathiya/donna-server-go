package storage

import (
	"testing"
)

func TestExtractSearchTerms_stripsStopWords(t *testing.T) {
	got := extractSearchTerms("tell me about my dog Max")
	want := []string{"dog", "max"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestExtractSearchTerms_deduplicates(t *testing.T) {
	got := extractSearchTerms("dog dog cat cat")
	want := []string{"dog", "cat"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestExtractSearchTerms_maxTwelve(t *testing.T) {
	transcript := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi"
	got := extractSearchTerms(transcript)
	if len(got) > 12 {
		t.Fatalf("expected at most 12 terms, got %d: %v", len(got), got)
	}
}

func TestExtractSearchTerms_shortWordsSkipped(t *testing.T) {
	got := extractSearchTerms("go to ox")
	if len(got) != 0 {
		t.Fatalf("expected no terms, got %v", got)
	}
}

func TestFormatFactRows_withEntityAndTopic(t *testing.T) {
	entity := "Kishan"
	rows := []factRow{{Fact: "likes coffee", EntityName: &entity}}
	got := formatFactRows(rows)
	if len(got) != 1 || got[0] != "Kishan: likes coffee" {
		t.Fatalf("unexpected format: %#v", got)
	}
}

func TestFormatFactRows_factOnly(t *testing.T) {
	rows := []factRow{{Fact: "plain fact"}}
	got := formatFactRows(rows)
	if len(got) != 1 || got[0] != "plain fact" {
		t.Fatalf("unexpected format: %#v", got)
	}
}

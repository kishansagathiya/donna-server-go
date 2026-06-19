package knowledge

import "testing"

func TestExtractObviousFactsFromTranscript(t *testing.T) {
	facts := ExtractObviousFacts([]SourceSlice{{Content: "User: My name is Kishan"}})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Fact != "User's name is Kishan" {
		t.Fatalf("unexpected fact: %q", facts[0].Fact)
	}
	if facts[0].EntityName == nil || *facts[0].EntityName != "Kishan" {
		t.Fatalf("unexpected entity name: %v", facts[0].EntityName)
	}
}

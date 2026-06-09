package knowledge

import "testing"

func TestMergeNameIntoProfile(t *testing.T) {
	tests := []struct {
		existing string
		name     string
		want     string
	}{
		{"", "Kishan", "The user's name is Kishan."},
		{"Enjoys hiking.", "Kishan", "The user's name is Kishan. Enjoys hiking."},
		{"The user's name is Kishan. Enjoys hiking.", "Kishan", "The user's name is Kishan. Enjoys hiking."},
	}
	for _, tt := range tests {
		got := mergeNameIntoProfile(tt.existing, tt.name)
		if got != tt.want {
			t.Fatalf("mergeNameIntoProfile(%q, %q) = %q, want %q", tt.existing, tt.name, got, tt.want)
		}
	}
}

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

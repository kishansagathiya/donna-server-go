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

func TestExtractObviousFacts_nameVariants(t *testing.T) {
	tests := []struct {
		content string
		want    string
	}{
		{"I'm Sarah", "User's name is Sarah"},
		{"call me Alex", "User's name is Alex"},
		{"I am Jordan Lee", "User's name is Jordan Lee"},
	}

	for _, tt := range tests {
		facts := ExtractObviousFacts([]SourceSlice{{Content: tt.content}})
		if len(facts) != 1 {
			t.Fatalf("content %q: expected 1 fact, got %d", tt.content, len(facts))
		}
		if facts[0].Fact != tt.want {
			t.Fatalf("content %q: got fact %q, want %q", tt.content, facts[0].Fact, tt.want)
		}
	}
}

func TestExtractObviousFacts_urlExtraction(t *testing.T) {
	facts := ExtractObviousFacts([]SourceSlice{
		{Content: "Check out https://example.com/page."},
	})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	want := "User shared link: https://example.com/page"
	if facts[0].Fact != want {
		t.Fatalf("got %q, want %q", facts[0].Fact, want)
	}
}

func TestExtractObviousFacts_deduplicatesAcrossSources(t *testing.T) {
	facts := ExtractObviousFacts([]SourceSlice{
		{Content: "My name is Kishan"},
		{Content: "I'm Kishan"},
	})
	if len(facts) != 1 {
		t.Fatalf("expected deduplicated single fact, got %d", len(facts))
	}
}

func TestExtractObviousFacts_noFactsFromUnrelatedText(t *testing.T) {
	facts := ExtractObviousFacts([]SourceSlice{{Content: "The weather is nice today"}})
	if len(facts) != 0 {
		t.Fatalf("expected no facts, got %#v", facts)
	}
}

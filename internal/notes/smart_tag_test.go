package notes

import (
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestNormalizeTagUnicode(t *testing.T) {
	got := storage.NormalizeTagUnicode("  Hello   World  ")
	if got != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestProposeLexicalConfidenceBuckets(t *testing.T) {
	e := &SmartTagEnricher{}
	pinned := true
	taxonomy := []storage.TaxonomyTag{
		{Name: "work", Count: 5, Pinned: pinned},
		{Name: "ideas", Count: 1},
	}
	proposals := e.proposeLexical("Today at work I had ideas", taxonomy)
	if len(proposals) == 0 {
		t.Fatal("expected proposals")
	}
	foundAuto := false
	foundSuggest := false
	for _, p := range proposals {
		if p.Tag == "work" && p.Confidence >= smartTagAutoMin {
			foundAuto = true
		}
		if p.Tag == "ideas" && p.Confidence >= smartTagSuggestMin && p.Confidence < smartTagAutoMin {
			foundSuggest = true
		}
	}
	if !foundAuto {
		t.Fatal("expected work to auto-apply")
	}
	if !foundSuggest {
		t.Fatal("expected ideas to be suggestion-range")
	}
}

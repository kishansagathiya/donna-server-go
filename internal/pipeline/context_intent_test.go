package pipeline

import "testing"

func TestNeedsUserContext(t *testing.T) {
	tests := []struct {
		transcript string
		want       bool
	}{
		{"how are great founders hiring ai savvy founders?", false},
		{"what is a CRDT?", false},
		{"explain reinforcement learning", false},
		{"what's the best way to build PM agents?", false},
		{"thanks!", false},
		{"hey", false},
		{"should I join YC co-founder matching?", true},
		{"what do you remember about my project?", true},
		{"remind me about the deadline", true},
		{"what do you know?", true},
		{"tell me about my coffee preferences", true},
		{"how should I find a co-founder?", true},
		{"When is Sarah's birthday?", true},
		{"", false},
	}

	for _, tt := range tests {
		got := NeedsUserContext(tt.transcript)
		if got != tt.want {
			t.Errorf("NeedsUserContext(%q) = %v, want %v", tt.transcript, got, tt.want)
		}
	}
}

func TestMemoryPlanFor_genericNoEmbed(t *testing.T) {
	p := MemoryPlanFor("what is a CRDT?")
	if p.ShouldRetrieve || p.NeedsEmbed {
		t.Fatalf("generic prompt must not retrieve/embed: %+v", p)
	}
}

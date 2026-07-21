package memory

import (
	"strings"
	"testing"
)

func TestPlanMemory_genericNoEmbed(t *testing.T) {
	cases := []string{
		"what is a CRDT?",
		"explain reinforcement learning",
		"how are great founders hiring ai savvy founders?",
		"what's the best way to build PM agents?",
	}
	for _, c := range cases {
		p := PlanMemory(c)
		if p.ShouldRetrieve || p.NeedsEmbed {
			t.Errorf("PlanMemory(%q)=%+v want no retrieve/embed", c, p)
		}
	}
}

func TestPlanMemory_birthdayStyleRouting(t *testing.T) {
	p := PlanMemory("When is Sarah's birthday?")
	if !p.ShouldRetrieve {
		t.Fatal("expected retrieve for birthday-style query")
	}
	if !p.NeedsEmbed {
		t.Fatal("expected embed for birthday-style query")
	}
	if !p.Temporal {
		t.Fatal("expected temporal cue")
	}
	foundSarah := false
	for _, e := range p.Entities {
		if strings.EqualFold(e, "Sarah") {
			foundSarah = true
		}
	}
	if !foundSarah {
		t.Fatalf("expected Sarah entity, got %v", p.Entities)
	}
	if !hasRoute(p, RouteEpisodic) && !hasRoute(p, RouteIdentityPrefs) {
		t.Fatalf("expected episodic or identity route, got %v", p.Routes)
	}
}

func TestPlanMemory_personalAndChitchat(t *testing.T) {
	if !PlanMemory("tell me about my coffee preferences").ShouldRetrieve {
		t.Fatal("expected retrieve for preference question")
	}
	if PlanMemory("thanks!").ShouldRetrieve {
		t.Fatal("chitchat should not retrieve")
	}
	if !PlanMemory("what do you remember about my project?").ShouldRetrieve {
		t.Fatal("explicit memory should retrieve")
	}
}

func TestRankAndCap_limits(t *testing.T) {
	hits := make([]Hit, 0, 20)
	for i := 0; i < 20; i++ {
		hits = append(hits, Hit{Source: "fact", ID: string(rune('a'+i)), Text: "memory item text here", Score: 0.9 - float64(i)*0.01})
	}
	got := rankAndCap(hits, 0.35, 8, 1200*4)
	if len(got) > 8 {
		t.Fatalf("cap exceeded: %d", len(got))
	}
}

func TestDetectConflicts(t *testing.T) {
	clar := detectConflicts([]Hit{
		{Source: "fact", Predicate: "birthday", EntityName: "Sarah", Text: "Sarah's birthday is March 1", Confidence: 0.9, Score: 0.8},
		{Source: "fact", Predicate: "birthday", EntityName: "Sarah", Text: "Sarah's birthday is April 2", Confidence: 0.85, Score: 0.75},
	})
	if clar == "" {
		t.Fatal("expected clarification for conflicting birthdays")
	}
}

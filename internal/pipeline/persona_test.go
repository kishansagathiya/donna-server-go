package pipeline

import (
	"strings"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestApplyPersona_eachPersonaHasDocumentedBehavior(t *testing.T) {
	for id, behavior := range DocumentedPersonaBehaviors {
		if strings.TrimSpace(behavior) == "" {
			t.Fatalf("persona %q missing documented behavior", id)
		}
	}
	for id, preamble := range PersonaPreambles {
		if id == "companion" {
			if preamble == "" {
				t.Fatal("companion should have an explicit preamble")
			}
			continue
		}
		if preamble == "" {
			t.Fatalf("persona %q has empty preamble", id)
		}
	}
}

func TestApplyPersona_includesSharedQualityPolicy(t *testing.T) {
	got := applyPersona("Base prompt.", "boss", "")
	if !strings.Contains(got, "You are Donna, acting as the user's direct, no-nonsense boss.") {
		t.Fatalf("missing boss identity: %q", got)
	}
	if !strings.Contains(got, "Base prompt.") {
		t.Fatalf("missing base prompt: %q", got)
	}
	if !strings.Contains(got, SharedQualityPolicy) {
		t.Fatalf("missing shared quality policy: %q", got)
	}
	if !strings.Contains(got, "clarifying questions") {
		t.Fatalf("missing clarify guidance: %q", got)
	}
	if !strings.Contains(got, "Next actions") {
		t.Fatalf("missing structured-output guidance: %q", got)
	}
}

func TestApplyPersona_companionIsExplicit(t *testing.T) {
	got := applyPersona("Base.", "companion", "")
	if !strings.Contains(got, "second-brain companion") {
		t.Fatalf("companion should set identity: %q", got)
	}
	if !strings.Contains(got, SharedQualityPolicy) {
		t.Fatalf("companion should still get quality policy: %q", got)
	}
}

func TestApplyPersona_customUsesUserText(t *testing.T) {
	got := applyPersona("Base.", "custom", "Speak like a pirate quartermaster.")
	if !strings.HasPrefix(got, "Speak like a pirate quartermaster.") {
		t.Fatalf("custom text should lead: %q", got)
	}
	if !strings.Contains(got, SharedQualityPolicy) {
		t.Fatalf("custom should still get quality policy: %q", got)
	}
}

func TestApplyPersona_personasDiffer(t *testing.T) {
	boss := applyPersona("Base.", "boss", "")
	coach := applyPersona("Base.", "coach", "")
	therapist := applyPersona("Base.", "therapist", "")
	if boss == coach || boss == therapist || coach == therapist {
		t.Fatal("persona prompts should differ")
	}
}

func TestResolveSystemPromptWithPreferences_preservesPersona(t *testing.T) {
	e := &Engine{Config: &config.Config{SystemPrompt: "Base."}}
	got := e.resolveSystemPromptWithPreferences(storage.PrefsRow{Persona: "boss"})
	if !strings.Contains(got, "direct, no-nonsense boss") {
		t.Fatalf("missing selected persona: %q", got)
	}
	if !strings.Contains(got, "Base.") {
		t.Fatalf("missing base prompt: %q", got)
	}
}

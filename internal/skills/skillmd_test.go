package skills

import (
	"strings"
	"testing"
)

func TestParseSkillMDRoundTrip(t *testing.T) {
	s := Skill{
		Name:        "flight-booking-prefs",
		Description: "How to book flights for the user",
		Content:     "# Procedure\n\n1. Search aisle seats.",
	}
	raw := RenderSkillMD(s)
	parsed, err := ParseSkillMD(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Name != s.Name || parsed.Description != s.Description {
		t.Fatalf("round trip mismatch: %#v", parsed)
	}
	if !strings.Contains(parsed.Content, "Search aisle seats") {
		t.Fatalf("content lost: %q", parsed.Content)
	}
}

func TestParseSkillMDMultilineDescription(t *testing.T) {
	raw := "---\nname: trip-planning\ndescription: >\n  Plans trips end to end.\n  Uses loyalty numbers.\n---\n\nBody here."
	parsed, err := ParseSkillMD(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Name != "trip-planning" {
		t.Fatalf("name: %q", parsed.Name)
	}
	if !strings.Contains(parsed.Description, "Plans trips end to end.") {
		t.Fatalf("description: %q", parsed.Description)
	}
	if strings.TrimSpace(parsed.Content) != "Body here." {
		t.Fatalf("content: %q", parsed.Content)
	}
}

func TestParseSkillMDRequiresName(t *testing.T) {
	if _, err := ParseSkillMD("just a body, no frontmatter"); err == nil {
		t.Fatal("expected name_required error")
	}
	if _, err := ParseSkillMD("---\ndescription: no name\n---\n\nbody"); err == nil {
		t.Fatal("expected name_required for missing name")
	}
	if _, err := ParseSkillMD(""); err == nil {
		t.Fatal("expected empty_skill error")
	}
}

func TestSkillValidate(t *testing.T) {
	cases := []struct {
		skill Skill
		want  string
	}{
		{Skill{Name: "ok-name", Description: "d", Content: "c"}, ""},
		{Skill{Name: "Bad_Name"}, "invalid_name"},
		{Skill{Name: "-leading"}, "invalid_name"},
		{Skill{Name: "UPPER"}, "invalid_name"},
		{Skill{Name: strings.Repeat("a", MaxNameLength+1)}, "name_too_long"},
		{Skill{Name: "ok", Description: strings.Repeat("d", MaxDescriptionLength+1)}, "description_too_long"},
		{Skill{Name: "ok", Content: strings.Repeat("c", MaxContentLength+1)}, "content_too_long"},
		{Skill{Name: "  "}, "name_required"},
	}
	for _, c := range cases {
		err := c.skill.Validate()
		if c.want == "" && err != nil {
			t.Fatalf("unexpected error for %#v: %v", c.skill.Name, err)
		}
		if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Fatalf("want %s, got %v (%s)", c.want, err, c.skill.Name)
		}
	}
}

func TestBundledSkillsValid(t *testing.T) {
	all := Bundled()
	if len(all) < 2 {
		t.Fatalf("expected bundled skills, got %d", len(all))
	}
	for _, s := range all {
		if s.Source != SourceSystem {
			t.Fatalf("source: %s", s.Source)
		}
		if err := s.Validate(); err != nil {
			t.Fatalf("bundled skill %s invalid: %v", s.Name, err)
		}
		if strings.TrimSpace(s.Content) == "" {
			t.Fatalf("bundled skill %s empty content", s.Name)
		}
	}
	if _, ok := System("web-research"); !ok {
		t.Fatal("expected web-research bundled skill")
	}
	if _, ok := System("booking-proposal"); !ok {
		t.Fatal("expected booking-proposal bundled skill")
	}
	if _, ok := System("nope"); ok {
		t.Fatal("unknown system skill should miss")
	}
}

func TestProviderNilStoreReturnsBundled(t *testing.T) {
	p := &Provider{}
	all, err := p.List(t.Context(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 2 {
		t.Fatalf("expected bundled skills, got %d", len(all))
	}
	s, err := p.GetByName(t.Context(), "user-1", "memory-first")
	if err != nil || s.Source != SourceSystem {
		t.Fatalf("system fallback failed: %v", err)
	}
	if _, err := p.GetByName(t.Context(), "user-1", "unknown"); err == nil {
		t.Fatal("expected skill_not_found")
	}
}

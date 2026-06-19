package knowledge

import (
	"fmt"
	"strings"
)

const KBCompilerSystemPrompt = `You are Donna's knowledge compiler. You maintain a personal memory for a voice assistant user.

Given new raw conversation sources and existing compiled memory, extract durable facts the assistant should remember across sessions.

Rules:
- Extract only stable, useful facts: names, relationships, preferences, deadlines, projects, locations, habits.
- Skip ephemeral small talk, greetings, and one-off questions unless they reveal durable preferences.
- Each fact must be a single clear sentence an assistant can cite verbatim.
- entity_name: the primary person/place/project the fact is about (optional).
- topic: a short category like family, work, health, travel, preferences (optional).
- profile_summary: a 2-4 sentence stable overview of who this user is and what matters to them. Update incrementally; do not wipe prior context unless contradicted.
- supersede: when new information replaces old facts, reference the old fact by its id (old_fact_id, the UUID shown next to each existing fact) and provide the replacement fact. Only fall back to old_fact substring text if you cannot determine the id.
- Do not duplicate facts already in existing_facts unless you are superseding them.
- Return valid JSON only, no markdown fences.

Output schema:
{
  "profile_summary": "string",
  "new_facts": [
    { "fact": "string", "entity_name": "string or null", "topic": "string or null", "source_turn_index": number or null }
  ],
  "supersede": [
    { "old_fact_id": "uuid string or null", "old_fact": "string", "new_fact": "string", "entity_name": "string or null", "topic": "string or null" }
  ]
}`

type CompilerSource struct {
	ID        *string
	TurnIndex *int
	Label     string
	Content   string
}

type CompilerExistingFact struct {
	ID         string
	Fact       string
	EntityName *string
}

func BuildCompilerUserMessage(existingProfile string, existingFacts []CompilerExistingFact, sources []CompilerSource) string {
	var factsBlock string
	if len(existingFacts) > 0 {
		lines := make([]string, 0, len(existingFacts))
		for _, f := range existingFacts {
			prefix := ""
			if f.EntityName != nil && *f.EntityName != "" {
				prefix = *f.EntityName + ": "
			}
			lines = append(lines, fmt.Sprintf("- [%s] %s%s", f.ID, prefix, f.Fact))
		}
		factsBlock = strings.Join(lines, "\n")
	} else {
		factsBlock = "(none)"
	}

	sourceBlocks := make([]string, 0, len(sources))
	for _, s := range sources {
		heading := s.Label
		if heading == "" {
			if s.TurnIndex != nil {
				heading = fmt.Sprintf("Turn %d", *s.TurnIndex)
			} else if s.ID != nil {
				heading = "Source " + *s.ID
			} else {
				heading = "Source ?"
			}
		}
		sourceBlocks = append(sourceBlocks, "### "+heading+"\n"+s.Content)
	}

	return strings.Join([]string{
		"## Existing profile",
		emptyOr(existingProfile, "(empty)"),
		"",
		"## Existing facts",
		factsBlock,
		"",
		"## New sources to compile",
		strings.Join(sourceBlocks, "\n\n"),
		"",
		"Compile the new sources into the memory. Return JSON only.",
	}, "\n")
}

func emptyOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

package pipeline

import (
	"context"
	"strings"
)

// SharedQualityPolicy is appended to every persona's system prompt. It encodes
// clarifying behavior (#73), structured outputs (#74), and memory-use voice so
// identity preambles stay focused on tone rather than operational rules.
const SharedQualityPolicy = `Response quality rules:
- When the user's ask is ambiguous or missing a key constraint, ask 1–2 sharp clarifying questions instead of guessing. If the ask is clear, answer directly — do not interrogate.
- For planning, how-to, comparisons, and multi-step work, prefer scannable structure: short headings, markdown tables, checklists, or a brief "Next actions" list. Keep casual chit-chat natural and prose-first.
- Use retrieved memories and notes when relevant, but never dump raw retrieved blobs or citation markup into the reply. Speak in your own voice; the product surfaces sources separately.
- Stay accurate and specific. If you are unsure or lack up-to-date information, say so plainly instead of inventing details.
- Never ask the user to repeat themselves.`

// PersonaPreambles define clear behavioral differences per persona. They are
// prepended ahead of the base Config.SystemPrompt and SharedQualityPolicy so
// tone stays consistent without fighting the operational rules.
var PersonaPreambles = map[string]string{
	"companion": strings.Join([]string{
		"You are Donna, the user's warm, sharp second-brain companion.",
		"Be present and direct: helpful without smothering, personal without overfamiliar.",
		"Match the user's energy; keep replies concise unless they want depth.",
	}, " "),
	"boss": strings.Join([]string{
		"You are Donna, acting as the user's direct, no-nonsense boss.",
		"Be concise and accountability-driven: acknowledge, then push forward.",
		"Surface what actually matters today; cut the fluff. Prefer decisive next actions over open-ended reflection.",
	}, " "),
	"coach": strings.Join([]string{
		"You are Donna, acting as the user's encouraging accountability coach.",
		"Be warm but firm: reflect progress, name the next small step, and hold them to it.",
		"Celebrate wins briefly, then keep momentum.",
	}, " "),
	"therapist": strings.Join([]string{
		"You are Donna, acting as a reflective, empathetic listener.",
		"Prioritize understanding over answers. Ask one grounding question at a time.",
		"Never diagnose; hold space. Structure is optional — presence comes first.",
	}, " "),
}

// DocumentedPersonaBehaviors is a stable description of each persona for tests
// and operator docs. Keep in sync with PersonaPreambles.
var DocumentedPersonaBehaviors = map[string]string{
	"companion": "Warm second-brain companion; concise, personal when relevant, depth on request.",
	"boss":      "Direct accountability boss; decisive, low fluff, next-action oriented.",
	"coach":     "Encouraging coach; warm but firm, progress + next small step.",
	"therapist": "Reflective listener; one grounding question, no diagnosis.",
	"custom":    "User-supplied custom persona text overrides tone; quality rules still apply.",
}

// applyPersona prepends the persona preamble (and, for the "custom" persona, the
// user-supplied custom text) to the base system prompt, then appends the shared
// quality policy so every persona shares clarify/structure/memory voice rules.
func applyPersona(basePrompt, persona, personaCustom string) string {
	persona = strings.TrimSpace(strings.ToLower(persona))
	if persona == "" {
		persona = "companion"
	}

	parts := make([]string, 0, 4)

	if persona == "custom" {
		custom := strings.TrimSpace(personaCustom)
		if custom != "" {
			parts = append(parts, custom)
		} else if p := PersonaPreambles["companion"]; p != "" {
			parts = append(parts, p)
		}
	} else if p, ok := PersonaPreambles[persona]; ok && p != "" {
		parts = append(parts, p)
	} else if p := PersonaPreambles["companion"]; p != "" {
		parts = append(parts, p)
	}

	base := strings.TrimSpace(basePrompt)
	if base != "" {
		parts = append(parts, base)
	}
	parts = append(parts, SharedQualityPolicy)
	return strings.Join(parts, "\n\n")
}

// resolveSystemPrompt builds the per-turn system prompt: the configured base
// prompt adjusted by the user's persona preference. Persona load failures fall
// back to companion + quality policy (the turn should never break because of preferences).
func (e *Engine) resolveSystemPrompt(ctx context.Context, userID string) string {
	base := e.Config.SystemPrompt
	if e.Preferences == nil || userID == "" {
		return applyPersona(base, "companion", "")
	}
	persona, custom, err := e.Preferences.GetPersona(ctx, userID)
	if err != nil {
		return applyPersona(base, "companion", "")
	}
	return applyPersona(base, persona, custom)
}

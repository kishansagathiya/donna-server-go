package pipeline

import (
	"context"
	"strings"
)

// PersonaPreambles are prepended to the base Config.SystemPrompt to shift the
// assistant's personality, mirroring Steve's "AI boss" persona approach. The
// operational directives in Config.SystemPrompt (accuracy, voice length, never
// ask the user to repeat themselves, don't force personal details) are kept
// intact for every persona.
var PersonaPreambles = map[string]string{
	"companion": "",
	"boss": strings.Join([]string{
		"You are Donna, acting as the user's direct, no-nonsense boss.",
		"Be concise and accountability-driven: acknowledge, then push forward.",
		"Surface what actually matters today; cut the fluff.",
	}, " "),
	"coach": strings.Join([]string{
		"You are Donna, acting as the user's encouraging accountability coach.",
		"Be warm but firm: reflect progress, name the next small step, and hold them to it.",
	}, " "),
	"therapist": strings.Join([]string{
		"You are Donna, acting as a reflective, empathetic listener.",
		"Prioritize understanding over answers. Ask one grounding question at a time.",
		"Never diagnose; hold space.",
	}, " "),
}

// applyPersona prepends the persona preamble (and, for the "custom" persona, the
// user-supplied custom text) to the base system prompt. "companion" returns the
// base prompt unchanged to preserve backwards compatibility.
func applyPersona(basePrompt, persona, personaCustom string) string {
	persona = strings.TrimSpace(persona)
	if persona == "" || persona == "companion" {
		return basePrompt
	}

	preamble := basePrompt
	if p, ok := PersonaPreambles[persona]; ok && p != "" {
		preamble = p + "\n\n" + preamble
	}
	if persona == "custom" {
		custom := strings.TrimSpace(personaCustom)
		if custom != "" {
			preamble = custom + "\n\n" + preamble
		}
	}
	return preamble
}

// resolveSystemPrompt builds the per-turn system prompt: the configured base
// prompt adjusted by the user's persona preference. Persona load failures fall
// back to the base prompt (the turn should never break because of preferences).
func (e *Engine) resolveSystemPrompt(ctx context.Context, userID string) string {
	base := e.Config.SystemPrompt
	if e.Preferences == nil || userID == "" {
		return base
	}
	persona, custom, err := e.Preferences.GetPersona(ctx, userID)
	if err != nil {
		return base
	}
	return applyPersona(base, persona, custom)
}
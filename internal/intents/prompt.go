package intents

import (
	"fmt"
	"strings"
)

const ExtractorSystemPrompt = `You are Donna's intent extractor. You read user-authored notes and conversation turns and extract actionable intents.

Rules:
- Extract only clear, actionable wishes: remind, follow_up, schedule, draft_message, open_url, or other short snake_case kinds.
- Skip greetings, pure questions, journaling, and non-actionable reflection.
- Each intent must have a short human summary and optional slots (JSON object of string values).
- Prefer fewer high-confidence intents over speculative ones.
- Never invent a create_note intent. Notes are user-authored only.
- Return valid JSON only, no markdown fences.

Output schema:
{
  "intents": [
    {
      "kind": "remind|follow_up|schedule|draft_message|open_url|string",
      "summary": "string",
      "slots": { "key": "string value" },
      "confidence": 0.0
    }
  ]
}

Examples of slots:
- remind / propose_reminder: title, when, notes
- follow_up / draft_message: recipient, body, channel, subject
- open_url: url, label
- schedule: title, when, location`

type ExtractedIntent struct {
	Kind       string            `json:"kind"`
	Summary    string            `json:"summary"`
	Slots      map[string]string `json:"slots"`
	Confidence *float64          `json:"confidence"`
}

type extractorOutput struct {
	Intents []ExtractedIntent `json:"intents"`
}

func BuildExtractorUserMessage(sourceType, sourceLabel, content string) string {
	return strings.Join([]string{
		"## Source",
		fmt.Sprintf("type: %s", sourceType),
		fmt.Sprintf("label: %s", emptyOr(sourceLabel, "(none)")),
		"",
		"## Content",
		strings.TrimSpace(content),
	}, "\n")
}

func emptyOr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

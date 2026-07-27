package intents

import (
	"fmt"
	"strings"
	"time"
)

const ExtractorSystemPrompt = `You are Donna's intent extractor. You read user-authored notes and conversation turns and extract actionable intents.

Rules:
- Extract only clear, actionable wishes: remind, follow_up, schedule, draft_message, open_url, or other short snake_case kinds.
- Skip greetings, pure questions, journaling, and non-actionable reflection.
- Each intent must have a short human summary and optional slots (JSON object of string values).
- Prefer fewer high-confidence intents over speculative ones.
- Never invent a create_note intent. Notes are user-authored only.
- Return valid JSON only, no markdown fences.

For schedule intents:
- Always include title (short meeting name).
- Prefer start as RFC3339 with numeric offset when the time can be resolved from Context (example: 2026-07-28T15:00:00-07:00).
- If you cannot resolve an absolute time, put the user's phrasing in when (example: "tomorrow at 3pm").
- Include end when a duration or end time is stated; otherwise omit end (default 1 hour).
- Include attendees as a comma-separated list of email addresses whenever emails are present.
- Include location when stated.
- Do not invent email addresses or times that were not implied.

Examples of slots:
- remind / propose_reminder: title, when, notes
- follow_up / draft_message: recipient, body, channel, subject
- open_url: url, label
- schedule: title, start, end, when, attendees, location, notes`

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
	return BuildExtractorUserMessageAt(sourceType, sourceLabel, content, time.Now().UTC())
}

func BuildExtractorUserMessageAt(sourceType, sourceLabel, content string, now time.Time) string {
	return strings.Join([]string{
		"## Context",
		fmt.Sprintf("current_time_utc: %s", now.UTC().Format(time.RFC3339)),
		"",
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

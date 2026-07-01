package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type DailyTask struct {
	NoteID   string `json:"note_id"`
	Title    string `json:"title"`
	Preview  string `json:"preview"`
	Priority string `json:"priority"`
	Reason   string `json:"reason"`
	IsUrgent bool   `json:"is_urgent"`
	IsImportant bool `json:"is_important"`
}

type OutdatedNote struct {
	NoteID  string `json:"note_id"`
	Title   string `json:"title"`
	Preview string `json:"preview"`
	Reason  string `json:"reason"`
}

type DailyBriefing struct {
	Date     string         `json:"date"`
	Summary  string         `json:"summary"`
	Tasks    []DailyTask    `json:"tasks"`
	Outdated []OutdatedNote `json:"outdated"`
}

type DailyChecker struct {
	Store *storage.Notes
	LLM   *providers.LLM
}

func (dc *DailyChecker) Check(ctx context.Context, userID string) (DailyBriefing, error) {
	if dc.Store == nil || !dc.Store.Enabled {
		return DailyBriefing{}, fmt.Errorf("notes disabled")
	}

	today := time.Now().UTC()
	briefing := DailyBriefing{
		Date:     today.Format("2006-01-02"),
		Tasks:    []DailyTask{},
		Outdated: []OutdatedNote{},
	}

	notes, err := dc.Store.ListForDailyReview(ctx, userID, 50)
	if err != nil {
		return briefing, err
	}

	if len(notes) == 0 {
		briefing.Summary = "No notes yet. Add context from chat, links, or documents and Donna will build your daily list."
		return briefing, nil
	}

	if dc.LLM != nil && dc.LLM.APIKey != "" {
		if llmBriefing, err := dc.checkWithLLM(ctx, notes, today); err == nil {
			return llmBriefing, nil
		} else {
			log.Warn("daily notes LLM check failed, using fallback", map[string]any{
				"error": err.Error(),
			})
		}
	}

	return dc.fallbackBriefing(notes, today), nil
}

func (dc *DailyChecker) checkWithLLM(ctx context.Context, notes []storage.NoteSummary, today time.Time) (DailyBriefing, error) {
	todayStr := today.Format("Monday, January 2, 2006")
	noteLines := make([]string, 0, len(notes))
	noteByID := make(map[string]storage.NoteSummary, len(notes))
	for _, note := range notes {
		noteByID[note.ID] = note
		flags := []string{}
		if note.IsUrgent {
			flags = append(flags, "urgent")
		}
		if note.IsImportant {
			flags = append(flags, "important")
		}
		flagStr := "none"
		if len(flags) > 0 {
			flagStr = strings.Join(flags, ", ")
		}
		noteLines = append(noteLines, fmt.Sprintf(
			"- id=%s date=%s flags=%s title=%q content=%q",
			note.ID,
			note.NoteDate,
			flagStr,
			note.Title,
			truncateForLLM(notePreviewText(note), 400),
		))
	}

	systemPrompt := strings.Join([]string{
		"You are Donna, a personal AI assistant that reviews notes and builds a daily action plan.",
		"Today is " + todayStr + ".",
		"Analyze the user's notes and return strict JSON with keys:",
		"- summary: string (1-3 sentences, friendly boss-style briefing for today)",
		"- tasks: array of {note_id, priority, reason} where priority is do_first, schedule, or delegate",
		"- outdated: array of {note_id, reason} for notes that are no longer relevant (past events, completed items, stale reminders)",
		"Rules:",
		"- Include only notes that imply actionable work in tasks (max 10 tasks)",
		"- do_first = urgent and important, schedule = important but not urgent, delegate = urgent but less important",
		"- Prefer notes marked urgent/important; still include unflagged notes if clearly actionable today",
		"- Mark outdated notes that reference past dates, finished projects, or expired context",
		"- A note can appear in tasks OR outdated, not both",
		"- Use exact note_id values from the input",
	}, "\n")

	messages := []providers.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "Notes to review:\n" + strings.Join(noteLines, "\n")},
	}

	raw, err := dc.LLM.CompleteOnce(ctx, messages)
	if err != nil {
		return DailyBriefing{}, err
	}

	var parsed struct {
		Summary  string `json:"summary"`
		Tasks    []struct {
			NoteID   string `json:"note_id"`
			Priority string `json:"priority"`
			Reason   string `json:"reason"`
		} `json:"tasks"`
		Outdated []struct {
			NoteID string `json:"note_id"`
			Reason string `json:"reason"`
		} `json:"outdated"`
	}
	if err := parseLLMJSON(raw, &parsed); err != nil {
		return DailyBriefing{}, err
	}

	briefing := DailyBriefing{
		Date:     today.Format("2006-01-02"),
		Summary:  strings.TrimSpace(parsed.Summary),
		Tasks:    []DailyTask{},
		Outdated: []OutdatedNote{},
	}

	for _, task := range parsed.Tasks {
		note, ok := noteByID[task.NoteID]
		if !ok {
			continue
		}
		priority := normalizePriority(task.Priority)
		briefing.Tasks = append(briefing.Tasks, DailyTask{
			NoteID:      note.ID,
			Title:       note.Title,
			Preview:     note.Preview,
			Priority:    priority,
			Reason:      strings.TrimSpace(task.Reason),
			IsUrgent:    note.IsUrgent,
			IsImportant: note.IsImportant,
		})
	}

	for _, item := range parsed.Outdated {
		note, ok := noteByID[item.NoteID]
		if !ok {
			continue
		}
		briefing.Outdated = append(briefing.Outdated, OutdatedNote{
			NoteID:  note.ID,
			Title:   note.Title,
			Preview: note.Preview,
			Reason:  strings.TrimSpace(item.Reason),
		})
	}

	if briefing.Summary == "" {
		briefing.Summary = defaultSummary(len(briefing.Tasks), len(briefing.Outdated))
	}

	return briefing, nil
}

func (dc *DailyChecker) fallbackBriefing(notes []storage.NoteSummary, today time.Time) DailyBriefing {
	briefing := DailyBriefing{
		Date:     today.Format("2006-01-02"),
		Tasks:    []DailyTask{},
		Outdated: []OutdatedNote{},
	}

	cutoff := today.AddDate(0, 0, -30)
	for _, note := range notes {
		noteTime, err := time.Parse(time.RFC3339, note.NoteDate)
		if err != nil {
			noteTime = today
		}

		if note.IsUrgent && note.IsImportant {
			briefing.Tasks = append(briefing.Tasks, DailyTask{
				NoteID:      note.ID,
				Title:       note.Title,
				Preview:     note.Preview,
				Priority:    "do_first",
				Reason:      "Marked urgent and important",
				IsUrgent:    true,
				IsImportant: true,
			})
			continue
		}

		if note.IsImportant {
			briefing.Tasks = append(briefing.Tasks, DailyTask{
				NoteID:      note.ID,
				Title:       note.Title,
				Preview:     note.Preview,
				Priority:    "schedule",
				Reason:      "Marked important — plan time for this",
				IsUrgent:    note.IsUrgent,
				IsImportant: true,
			})
			continue
		}

		if note.IsUrgent {
			briefing.Tasks = append(briefing.Tasks, DailyTask{
				NoteID:      note.ID,
				Title:       note.Title,
				Preview:     note.Preview,
				Priority:    "delegate",
				Reason:      "Marked urgent",
				IsUrgent:    true,
				IsImportant: false,
			})
			continue
		}

		if noteTime.Before(cutoff) {
			briefing.Outdated = append(briefing.Outdated, OutdatedNote{
				NoteID:  note.ID,
				Title:   note.Title,
				Preview: note.Preview,
				Reason:  "Older than 30 days with no priority flags",
			})
		}
	}

	if len(briefing.Tasks) > 10 {
		briefing.Tasks = briefing.Tasks[:10]
	}

	briefing.Summary = defaultSummary(len(briefing.Tasks), len(briefing.Outdated))
	return briefing
}

func parseLLMJSON(raw string, dest any) error {
	trimmed := strings.TrimSpace(raw)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start == -1 || end == -1 {
		return fmt.Errorf("no JSON object in LLM response")
	}
	return json.Unmarshal([]byte(trimmed[start:end+1]), dest)
}

func normalizePriority(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "do_first", "do-first", "dofirst":
		return "do_first"
	case "schedule", "important":
		return "schedule"
	case "delegate":
		return "delegate"
	default:
		return "schedule"
	}
}

func defaultSummary(taskCount, outdatedCount int) string {
	if taskCount == 0 && outdatedCount == 0 {
		return "Nothing urgent on your list today. Review your notes or add new context when something comes up."
	}
	if taskCount == 0 {
		return fmt.Sprintf("No action items for today. %d note(s) may be outdated and worth archiving.", outdatedCount)
	}
	if outdatedCount == 0 {
		return fmt.Sprintf("You have %d thing(s) to focus on today.", taskCount)
	}
	return fmt.Sprintf("You have %d thing(s) to focus on today, and %d note(s) that may be outdated.", taskCount, outdatedCount)
}

func notePreviewText(note storage.NoteSummary) string {
	if note.Preview != "" {
		return note.Preview
	}
	return note.Title
}

func truncateForLLM(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

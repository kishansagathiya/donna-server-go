package notes

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// Eisenhower priorities for Today's focus list.
const (
	PriorityDoFirst  = "do_first" // urgent + important
	PrioritySchedule = "schedule" // important, not urgent
	PriorityDelegate = "delegate" // urgent, not important
	PriorityLater    = "later"    // neither — good to have, last
)

type DailyTask struct {
	NoteID      string `json:"note_id"`
	Title       string `json:"title"`
	Preview     string `json:"preview"`
	Content     string `json:"content,omitempty"`
	Priority    string `json:"priority"`
	Reason      string `json:"reason"`
	IsUrgent    bool   `json:"is_urgent"`
	IsImportant bool   `json:"is_important"`
}

// OutdatedNote is kept for API compatibility; Today no longer computes outdated notes.
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
}

// quadrant limits keep Today snappy while covering what matters most.
const (
	doFirstLimit  = 25
	scheduleLimit = 25
	delegateLimit = 15
)

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

	// Fast path: read notes by urgent/important flags in parallel — no LLM.
	// Order: do first → schedule → delegate. Unflagged "later" notes stay in Notes.
	var (
		doFirst, schedule, delegate []storage.NoteSummary
		errDo, errSched, errDel     error
		wg                          sync.WaitGroup
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		doFirst, errDo = dc.Store.ListQuadrant(ctx, userID, true, true, doFirstLimit)
	}()
	go func() {
		defer wg.Done()
		schedule, errSched = dc.Store.ListQuadrant(ctx, userID, false, true, scheduleLimit)
	}()
	go func() {
		defer wg.Done()
		delegate, errDel = dc.Store.ListQuadrant(ctx, userID, true, false, delegateLimit)
	}()
	wg.Wait()

	for _, err := range []error{errDo, errSched, errDel} {
		if err != nil {
			return briefing, err
		}
	}

	briefing.Tasks = append(briefing.Tasks, tasksFromNotes(doFirst, PriorityDoFirst, "Urgent and important — do first")...)
	briefing.Tasks = append(briefing.Tasks, tasksFromNotes(schedule, PrioritySchedule, "Important — schedule time for this")...)
	briefing.Tasks = append(briefing.Tasks, tasksFromNotes(delegate, PriorityDelegate, "Urgent — consider delegating")...)

	if len(briefing.Tasks) == 0 {
		briefing.Summary = "Nothing marked for today. Flag notes as urgent or important and they will show up here."
		return briefing, nil
	}

	briefing.Summary = todaySummary(len(doFirst), len(schedule), len(delegate), 0)
	return briefing, nil
}

func tasksFromNotes(notes []storage.NoteSummary, priority, reason string) []DailyTask {
	out := make([]DailyTask, 0, len(notes))
	for _, note := range notes {
		out = append(out, DailyTask{
			NoteID:      note.ID,
			Title:       note.Title,
			Preview:     note.Preview,
			Content:     note.Content,
			Priority:    priority,
			Reason:      reason,
			IsUrgent:    note.IsUrgent,
			IsImportant: note.IsImportant,
		})
	}
	return out
}

func todaySummary(doFirst, schedule, delegate, later int) string {
	parts := make([]string, 0, 4)
	if doFirst > 0 {
		parts = append(parts, fmt.Sprintf("%d to do first", doFirst))
	}
	if schedule > 0 {
		parts = append(parts, fmt.Sprintf("%d to schedule", schedule))
	}
	if delegate > 0 {
		parts = append(parts, fmt.Sprintf("%d to delegate", delegate))
	}
	if later > 0 {
		parts = append(parts, fmt.Sprintf("%d for later", later))
	}
	if len(parts) == 0 {
		return "Nothing on your list today."
	}
	return "Today: " + strings.Join(parts, ", ") + "."
}

func normalizePriority(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "do_first", "do-first", "dofirst":
		return PriorityDoFirst
	case "schedule", "important":
		return PrioritySchedule
	case "delegate":
		return PriorityDelegate
	case "later", "eliminate":
		return PriorityLater
	default:
		return PrioritySchedule
	}
}

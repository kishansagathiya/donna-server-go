package schedules

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/agents"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// Service creates scheduled goals and turns due ticks into agent_runs.
type Service struct {
	Store   *storage.SchedulesStore
	Agents  *storage.AgentsStore
	Spawner *agents.Spawner
	Intents *storage.ActionsStore
}

func (s *Service) Create(ctx context.Context, userID string, in storage.NewScheduleInput) (storage.ScheduledAgentGoal, error) {
	if s == nil || s.Store == nil || !s.Store.Enabled {
		return storage.ScheduledAgentGoal{}, fmt.Errorf("schedules_disabled")
	}
	sch, err := s.Store.Create(ctx, userID, in)
	if err != nil {
		return storage.ScheduledAgentGoal{}, err
	}
	if err := s.StartRun(ctx, sch); err != nil {
		log.Warn("schedule first run failed", map[string]any{
			"scheduleId": log.ShortID(sch.ID),
			"error":      err.Error(),
		})
		return sch, nil
	}
	if fresh, err := s.Store.Get(ctx, userID, sch.ID); err == nil {
		return fresh, nil
	}
	return sch, nil
}

func (s *Service) StartRun(ctx context.Context, sch storage.ScheduledAgentGoal) error {
	if s == nil || s.Store == nil || s.Spawner == nil {
		return fmt.Errorf("schedules_unconfigured")
	}
	if sch.Status != storage.ScheduleStatusActive {
		return fmt.Errorf("schedule_not_active")
	}
	if sch.CurrentAgentRunID != nil && strings.TrimSpace(*sch.CurrentAgentRunID) != "" {
		return fmt.Errorf("schedule_already_running")
	}

	schID := sch.ID
	run, err := s.Spawner.Spawn(ctx, sch.UserID, agents.SpawnInput{
		Goal:           DisplayGoal(sch),
		GroundedGoal:   RunBrief(sch),
		ScheduleID:     &schID,
		SelectedSkills: sch.SelectedSkills,
		ToolAllowlist:  agents.DefaultParentToolAllowlist(),
		MaxSteps:       storage.DefaultScheduleMaxSteps,
	})
	if err != nil {
		return err
	}

	if _, err := s.Store.ClaimForRun(ctx, sch, run.ID); err != nil {
		_, _ = s.Agents.Cancel(ctx, sch.UserID, run.ID)
		return err
	}
	return nil
}

func DisplayGoal(sch storage.ScheduledAgentGoal) string {
	title := strings.TrimSpace(sch.Title)
	if title == "" {
		return strings.TrimSpace(sch.Goal)
	}
	return title + " — " + strings.TrimSpace(sch.Goal)
}

func RunBrief(sch storage.ScheduledAgentGoal) string {
	var b strings.Builder
	b.WriteString("This is a scheduled agent goal")
	if sch.CadenceMinutes <= 0 {
		b.WriteString(" (one-shot).")
	} else {
		fmt.Fprintf(&b, " (repeats every %d minutes).", sch.CadenceMinutes)
	}
	b.WriteString("\n\nTitle: ")
	b.WriteString(strings.TrimSpace(sch.Title))
	b.WriteString("\n\nGoal:\n")
	b.WriteString(strings.TrimSpace(sch.Goal))
	if strings.TrimSpace(sch.LastSummary) != "" {
		b.WriteString("\n\nLast run summary:\n")
		b.WriteString(strings.TrimSpace(sch.LastSummary))
	}
	b.WriteString("\n\nDeliver a concise result the user can read later. Do not wait for them to stay online.")
	return b.String()
}

// AfterAgentRun resumes the schedule lifecycle when a tick ends.
func (s *Service) AfterAgentRun(ctx context.Context, run storage.AgentRun) {
	if s == nil || s.Store == nil || run.ScheduleID == nil || strings.TrimSpace(*run.ScheduleID) == "" {
		return
	}
	if !storage.IsTerminalAgentStatus(run.Status) && run.Status != storage.AgentStatusWaitingForUser {
		return
	}

	schID := *run.ScheduleID
	sch, err := s.Store.Get(ctx, run.UserID, schID)
	if err != nil {
		log.Warn("schedule after-run lookup failed", map[string]any{
			"scheduleId": log.ShortID(schID),
			"error":      err.Error(),
		})
		return
	}

	if run.Status == storage.AgentStatusWaitingForUser {
		// Keep current_agent_run_id so the user can open and reply.
		return
	}

	if sch.CurrentAgentRunID != nil && *sch.CurrentAgentRunID == run.ID {
		_ = s.Store.ClearCurrentRun(ctx, run.UserID, schID, run.ID)
	}

	if run.Status == storage.AgentStatusSucceeded {
		s.deliverResult(ctx, run)
	}

	summary := runSummary(run)
	patch := map[string]any{}
	if summary != "" {
		patch["last_summary"] = summary
	}

	if sch.Status != storage.ScheduleStatusActive {
		if len(patch) > 0 {
			_, _ = s.Store.Patch(ctx, run.UserID, schID, patch)
		}
		return
	}

	next, ok := storage.NextScheduleAt(sch.CadenceMinutes, time.Now().UTC())
	if !ok {
		now := time.Now().UTC().Format(time.RFC3339)
		patch["status"] = storage.ScheduleStatusCompleted
		patch["completed_at"] = now
		patch["next_run_at"] = nil
		patch["current_agent_run_id"] = nil
		if _, err := s.Store.Patch(ctx, run.UserID, schID, patch); err != nil {
			log.Warn("schedule complete patch failed", map[string]any{"error": err.Error()})
		}
		return
	}

	patch["next_run_at"] = next.Format(time.RFC3339)
	if _, err := s.Store.Patch(ctx, run.UserID, schID, patch); err != nil {
		log.Warn("schedule next-run patch failed", map[string]any{"error": err.Error()})
	}
}

func runSummary(run storage.AgentRun) string {
	if len(run.Result) == 0 || string(run.Result) == "null" {
		return ""
	}
	var result map[string]any
	if err := json.Unmarshal(run.Result, &result); err != nil {
		return ""
	}
	for _, key := range []string{"summary", "question"} {
		if v, ok := result[key].(string); ok {
			if s := strings.TrimSpace(v); s != "" {
				if len(s) > 8000 {
					return s[:8000]
				}
				return s
			}
		}
	}
	return ""
}

func (s *Service) deliverResult(ctx context.Context, run storage.AgentRun) {
	if s == nil || s.Intents == nil || !s.Intents.Enabled {
		return
	}
	summary := runSummary(run)
	if summary == "" {
		summary = "Scheduled task finished."
	}
	goal := strings.TrimSpace(run.Goal)
	if goal != "" {
		summary = goal + "\n\n" + summary
	}
	if len(summary) > 2000 {
		summary = summary[:2000]
	}
	sourceID := run.ID
	if _, _, err := s.Intents.UpsertOpenIntent(ctx, run.UserID, storage.NewIntentInput{
		Kind:       "agent_result",
		Summary:    summary,
		SourceType: "agent_run",
		SourceID:   &sourceID,
	}); err != nil {
		log.Warn("schedule result intent failed", map[string]any{
			"runId": log.ShortID(run.ID),
			"error": err.Error(),
		})
	}
}

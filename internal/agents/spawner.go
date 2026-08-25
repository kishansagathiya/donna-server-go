package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/memory"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// MemoryBridge adapts Donna memory.Retriever to agents.MemorySearcher.
type MemoryBridge struct {
	Retriever *memory.Retriever
	MinScore  float64
}

func (m *MemoryBridge) Search(ctx context.Context, userID, query string, limit int) ([]MemoryHit, error) {
	if m == nil || m.Retriever == nil {
		return nil, fmt.Errorf("memory_unavailable")
	}
	min := m.MinScore
	if min <= 0 {
		min = 0.35
	}
	res := m.Retriever.Retrieve(ctx, userID, "", query, min)
	out := make([]MemoryHit, 0, len(res.Hits))
	for i, h := range res.Hits {
		if limit > 0 && i >= limit {
			break
		}
		out = append(out, MemoryHit{
			Source: h.Source,
			ID:     h.ID,
			Text:   h.Text,
			Score:  h.Score,
		})
	}
	return out, nil
}

// NotesBridge adapts storage.Notes to agents.NoteSearcher.
type NotesBridge struct {
	Notes *storage.Notes
}

func (n *NotesBridge) Search(ctx context.Context, userID, query string, limit int) ([]NoteHit, error) {
	if n == nil || n.Notes == nil || !n.Notes.Enabled {
		return nil, fmt.Errorf("notes_unavailable")
	}
	rows, err := n.Notes.SearchNotes(ctx, userID, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]NoteHit, 0, len(rows))
	for _, row := range rows {
		out = append(out, NoteHit{ID: row.ID, Title: row.Title, Preview: row.Preview})
	}
	return out, nil
}

// Spawner creates agent_runs and enqueues background work.
type Spawner struct {
	Store  *storage.AgentsStore
	Jobs   *storage.BackgroundJobs
	Mem    MemorySearcher
	Skills SkillProvider
}

type SpawnInput struct {
	Goal           string
	GroundedGoal   string // optional LLM-facing goal (attachments grounded); falls back to Goal
	IntentID       *string
	EmployeeID     *string
	ScheduleID     *string
	ToolAllowlist  []string
	SelectedSkills []string
	MaxSteps       int
}

func (s *Spawner) Spawn(ctx context.Context, userID string, in SpawnInput) (storage.AgentRun, error) {
	if s == nil || s.Store == nil || !s.Store.Enabled {
		return storage.AgentRun{}, fmt.Errorf("agents_disabled")
	}
	goal := strings.TrimSpace(in.Goal)
	if goal == "" {
		return storage.AgentRun{}, fmt.Errorf("goal_required")
	}
	grounded := strings.TrimSpace(in.GroundedGoal)
	if grounded == "" {
		grounded = goal
	}
	allow := in.ToolAllowlist
	if len(allow) == 0 {
		allow = []string{"orchestration", "memory", "web", "browser", "skills", "commerce"}
	}

	// Validate user-selected skills (names must resolve; user skills shadow
	// bundled system skills).
	var selected []string
	for _, name := range in.SelectedSkills {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if s.Skills != nil {
			if _, err := s.Skills.GetByName(ctx, userID, name); err != nil {
				return storage.AgentRun{}, fmt.Errorf("skill_not_found: %s", name)
			}
		}
		if len(selected) >= 5 {
			return storage.AgentRun{}, fmt.Errorf("too_many_skills")
		}
		selected = append(selected, name)
	}
	if selected == nil {
		selected = []string{}
	}

	snapshot := map[string]any{}
	if grounded != goal {
		snapshot["grounded_goal"] = grounded
	}
	if s.Mem != nil {
		hits, err := s.Mem.Search(ctx, userID, goal, 6)
		if err != nil {
			log.Warn("agent memory snapshot failed", map[string]any{"error": err.Error()})
		} else if len(hits) > 0 {
			snapshot["hits"] = hits
		}
	}
	if s.Skills != nil {
		matches := s.Skills.Match(ctx, userID, goal, 5)
		skillsSnapshot := make([]map[string]any, 0, len(matches)+len(selected))
		selectedSet := map[string]struct{}{}
		for _, name := range selected {
			selectedSet[name] = struct{}{}
		}
		for _, m := range matches {
			_, isSelected := selectedSet[m.Skill.Name]
			entry := map[string]any{
				"name":        m.Skill.Name,
				"description": m.Skill.Description,
				"source":      m.Skill.Source,
				"kind":        map[bool]string{true: "selected", false: "matched"}[isSelected],
			}
			if isSelected {
				entry["content"] = m.Skill.Content
			}
			skillsSnapshot = append(skillsSnapshot, entry)
		}
		// Selected skills that matched nothing still go in as selected.
		for _, name := range selected {
			found := false
			for _, m := range matches {
				if m.Skill.Name == name {
					found = true
					break
				}
			}
			if found {
				continue
			}
			if sk, err := s.Skills.GetByName(ctx, userID, name); err == nil {
				skillsSnapshot = append(skillsSnapshot, map[string]any{
					"name":        sk.Name,
					"description": sk.Description,
					"source":      sk.Source,
					"kind":        "selected",
					"content":     sk.Content,
				})
			}
		}
		if len(skillsSnapshot) > 0 {
			snapshot["skills"] = skillsSnapshot
		}
	}
	raw, _ := json.Marshal(snapshot)

	run, err := s.Store.Create(ctx, userID, storage.NewAgentRunInput{
		IntentID:       in.IntentID,
		EmployeeID:     in.EmployeeID,
		ScheduleID:     in.ScheduleID,
		Goal:           goal,
		ToolAllowlist:  allow,
		SelectedSkills: selected,
		MaxSteps:       in.MaxSteps,
		MemorySnapshot: raw,
	})
	if err != nil {
		return storage.AgentRun{}, err
	}

	if s.Jobs != nil && s.Jobs.Enabled {
		_, err := s.Jobs.Enqueue(ctx, storage.EnqueueJobInput{
			UserID:     userID,
			JobType:    storage.JobTypeAgentRun,
			DedupeKey:  "agent_run:" + run.ID,
			Payload:    map[string]any{"agent_run_id": run.ID, "user_id": userID},
			TargetKind: storage.TargetKindAgentRun,
			TargetID:   run.ID,
		})
		if err != nil {
			log.Warn("agent job enqueue failed", map[string]any{"runId": log.ShortID(run.ID), "error": err.Error()})
		}
	}
	return run, nil
}

// Worker handles background_jobs of type agent_run.
type Worker struct {
	Store    *storage.AgentsStore
	Harness  *Harness
	AfterRun func(ctx context.Context, run storage.AgentRun)
}

func (w *Worker) HandleJob(ctx context.Context, job storage.BackgroundJob) error {
	if w == nil || w.Store == nil || w.Harness == nil {
		return fmt.Errorf("agent_worker_unconfigured")
	}
	var payload struct {
		AgentRunID string `json:"agent_run_id"`
		UserID     string `json:"user_id"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	runID := payload.AgentRunID
	if runID == "" && job.TargetID != nil {
		runID = *job.TargetID
	}
	if runID == "" {
		return fmt.Errorf("agent_run_id_required")
	}

	var run storage.AgentRun
	var err error
	if payload.UserID != "" {
		run, err = w.Store.Get(ctx, payload.UserID, runID)
	} else {
		run, err = w.Store.GetByID(ctx, runID)
	}
	if err != nil {
		return err
	}
	switch run.Status {
	case storage.AgentStatusSucceeded, storage.AgentStatusFailed, storage.AgentStatusCancelled, storage.AgentStatusExpired, storage.AgentStatusWaitingForUser:
		if w.AfterRun != nil {
			w.AfterRun(ctx, run)
		}
		return nil
	}
	runErr := w.Harness.Run(ctx, run)
	if w.AfterRun != nil {
		fresh, getErr := w.Store.GetByID(ctx, run.ID)
		if getErr == nil {
			w.AfterRun(ctx, fresh)
		} else {
			w.AfterRun(ctx, run)
		}
	}
	return runErr
}

// ResumeAfterApproval requeues a waiting/finished agent and enqueues a job.
func ResumeAfterApproval(ctx context.Context, store *storage.AgentsStore, jobs *storage.BackgroundJobs, userID, runID, note string) (storage.AgentRun, error) {
	if store == nil || !store.Enabled {
		return storage.AgentRun{}, fmt.Errorf("agents_disabled")
	}
	run, err := store.Get(ctx, userID, runID)
	if err != nil {
		return storage.AgentRun{}, err
	}
	switch run.Status {
	case storage.AgentStatusWaitingForUser, storage.AgentStatusQueued, storage.AgentStatusSucceeded, storage.AgentStatusFailed:
		// ok
	case storage.AgentStatusRunning:
		// Already running; redirect_pending will be picked up mid-loop.
		return run, nil
	default:
		return storage.AgentRun{}, fmt.Errorf("run_not_resumable")
	}
	if strings.TrimSpace(note) != "" {
		_, _ = store.SetRedirect(ctx, userID, runID, note)
	}
	run, err = store.Requeue(ctx, userID, runID)
	if err != nil {
		return storage.AgentRun{}, err
	}
	if jobs != nil && jobs.Enabled {
		_, _ = jobs.Enqueue(ctx, storage.EnqueueJobInput{
			UserID:     userID,
			JobType:    storage.JobTypeAgentRun,
			DedupeKey:  fmt.Sprintf("agent_run_resume:%s:%d", run.ID, run.StepCount),
			Payload:    map[string]any{"agent_run_id": run.ID, "user_id": userID},
			TargetKind: storage.TargetKindAgentRun,
			TargetID:   run.ID,
		})
	}
	return run, nil
}

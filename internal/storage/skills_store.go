package storage

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ErrSkillNotFound is returned when a skill lookup misses.
var ErrSkillNotFound = errors.New("skill_not_found")

const (
	SkillSourceUser  = "user"
	SkillSourceAgent = "agent"

	skillSelect = "id,user_id,name,description,content,source,agent_run_id,version,created_at,updated_at"
)

// Skill is a per-user agent skill (agentskills.io-compatible SKILL.md content).
// Bundled system skills are NOT rows here — they live in internal/skills/bundled.
type Skill struct {
	ID          string  `json:"id"`
	UserID      string  `json:"user_id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Content     string  `json:"content"`
	Source      string  `json:"source"`
	AgentRunID  *string `json:"agent_run_id,omitempty"`
	Version     int     `json:"version"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type NewSkillInput struct {
	Name        string
	Description string
	Content     string
	Source      string
	AgentRunID  *string
}

type UpdateSkillInput struct {
	Name        *string
	Description *string
	Content     *string
}

type SkillsStore struct {
	DB      *Supabase
	Enabled bool
}

func (s *SkillsStore) Create(ctx context.Context, userID string, in NewSkillInput) (Skill, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return Skill{}, fmt.Errorf("skills_disabled")
	}
	body, err := s.skillBody(in)
	if err != nil {
		return Skill{}, err
	}
	body["user_id"] = userID
	var rows []Skill
	if err := s.DB.Insert(ctx, "agent_skills", body, &rows); err != nil {
		return Skill{}, err
	}
	if len(rows) == 0 {
		return Skill{}, fmt.Errorf("skill_create_empty")
	}
	return rows[0], nil
}

// UpsertByName inserts or updates a skill keyed on (user_id, name), bumping
// version on update. Used by the agent save_skill tool.
func (s *SkillsStore) UpsertByName(ctx context.Context, userID string, in NewSkillInput) (Skill, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return Skill{}, fmt.Errorf("skills_disabled")
	}
	body, err := s.skillBody(in)
	if err != nil {
		return Skill{}, err
	}
	body["user_id"] = userID
	existing, getErr := s.GetByName(ctx, userID, body["name"].(string))
	if getErr == nil {
		patch := map[string]any{
			"description": body["description"],
			"content":     body["content"],
			"source":      body["source"],
			"version":     existing.Version + 1,
			"updated_at":  body["updated_at"],
		}
		if runID, ok := body["agent_run_id"]; ok {
			patch["agent_run_id"] = runID
		}
		q := url.Values{}
		q.Set("id", "eq."+existing.ID)
		q.Set("user_id", "eq."+userID)
		var rows []Skill
		if err := s.DB.PatchReturning(ctx, "agent_skills", q, patch, &rows); err != nil {
			return Skill{}, err
		}
		if len(rows) == 0 {
			return Skill{}, fmt.Errorf("skill_upsert_empty")
		}
		return rows[0], nil
	}
	if !errors.Is(getErr, ErrSkillNotFound) {
		return Skill{}, getErr
	}
	var rows []Skill
	if err := s.DB.Insert(ctx, "agent_skills", body, &rows); err != nil {
		return Skill{}, err
	}
	if len(rows) == 0 {
		return Skill{}, fmt.Errorf("skill_upsert_empty")
	}
	return rows[0], nil
}

func (s *SkillsStore) skillBody(in NewSkillInput) (map[string]any, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("name_required")
	}
	source := in.Source
	if source == "" {
		source = SkillSourceUser
	}
	if source != SkillSourceUser && source != SkillSourceAgent {
		return nil, fmt.Errorf("invalid_source")
	}
	body := map[string]any{
		"name":        name,
		"description": strings.TrimSpace(in.Description),
		"content":     strings.TrimSpace(in.Content),
		"source":      source,
		"updated_at":  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if in.AgentRunID != nil && *in.AgentRunID != "" {
		body["agent_run_id"] = *in.AgentRunID
	}
	return body, nil
}

func (s *SkillsStore) Get(ctx context.Context, userID, skillID string) (Skill, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return Skill{}, fmt.Errorf("skills_disabled")
	}
	q := url.Values{}
	q.Set("select", skillSelect)
	q.Set("id", "eq."+skillID)
	q.Set("user_id", "eq."+userID)
	q.Set("limit", "1")
	var rows []Skill
	if err := s.DB.Get(ctx, "agent_skills", q, &rows); err != nil {
		return Skill{}, err
	}
	if len(rows) == 0 {
		return Skill{}, ErrSkillNotFound
	}
	return rows[0], nil
}

func (s *SkillsStore) GetByName(ctx context.Context, userID, name string) (Skill, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return Skill{}, fmt.Errorf("skills_disabled")
	}
	q := url.Values{}
	q.Set("select", skillSelect)
	q.Set("user_id", "eq."+userID)
	q.Set("name", "eq."+strings.TrimSpace(name))
	q.Set("limit", "1")
	var rows []Skill
	if err := s.DB.Get(ctx, "agent_skills", q, &rows); err != nil {
		return Skill{}, err
	}
	if len(rows) == 0 {
		return Skill{}, ErrSkillNotFound
	}
	return rows[0], nil
}

func (s *SkillsStore) List(ctx context.Context, userID string, limit, offset int) ([]Skill, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("skills_disabled")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	q := url.Values{}
	q.Set("select", skillSelect)
	q.Set("user_id", "eq."+userID)
	q.Set("order", "updated_at.desc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("offset", fmt.Sprintf("%d", offset))
	var rows []Skill
	if err := s.DB.Get(ctx, "agent_skills", q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []Skill{}
	}
	return rows, nil
}

func (s *SkillsStore) Update(ctx context.Context, userID, skillID string, in UpdateSkillInput) (Skill, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return Skill{}, fmt.Errorf("skills_disabled")
	}
	patch := map[string]any{"updated_at": time.Now().UTC().Format(time.RFC3339Nano)}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return Skill{}, fmt.Errorf("name_required")
		}
		patch["name"] = name
	}
	if in.Description != nil {
		patch["description"] = strings.TrimSpace(*in.Description)
	}
	if in.Content != nil {
		patch["content"] = strings.TrimSpace(*in.Content)
	}
	current, err := s.Get(ctx, userID, skillID)
	if err != nil {
		return Skill{}, err
	}
	patch["version"] = current.Version + 1
	q := url.Values{}
	q.Set("id", "eq."+skillID)
	q.Set("user_id", "eq."+userID)
	var rows []Skill
	if err := s.DB.PatchReturning(ctx, "agent_skills", q, patch, &rows); err != nil {
		return Skill{}, err
	}
	if len(rows) == 0 {
		return Skill{}, ErrSkillNotFound
	}
	return rows[0], nil
}

func (s *SkillsStore) Delete(ctx context.Context, userID, skillID string) error {
	if s == nil || !s.Enabled || s.DB == nil {
		return fmt.Errorf("skills_disabled")
	}
	q := url.Values{}
	q.Set("id", "eq."+skillID)
	q.Set("user_id", "eq."+userID)
	return s.DB.Delete(ctx, "agent_skills", q)
}

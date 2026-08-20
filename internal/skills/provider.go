package skills

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// Provider merges bundled system skills with the user's agent_skills rows.
// A user skill with the same name as a bundled skill shadows it.
type Provider struct {
	Store *storage.SkillsStore
}

// List returns the user's skills plus bundled system skills (shadowed names
// excluded), ordered user-first then bundled.
func (p *Provider) List(ctx context.Context, userID string) ([]Skill, error) {
	system := Bundled()
	if p == nil || p.Store == nil || !p.Store.Enabled {
		return system, nil
	}
	rows, err := p.Store.List(ctx, userID, 200, 0)
	if err != nil {
		log.Warn("skills list failed", map[string]any{"error": err.Error()})
		return system, nil
	}
	out := make([]Skill, 0, len(rows)+len(system))
	shadowed := map[string]struct{}{}
	for _, r := range rows {
		shadowed[r.Name] = struct{}{}
		out = append(out, rowToSkill(r))
	}
	for _, s := range system {
		if _, ok := shadowed[s.Name]; !ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// GetByName resolves a skill by name: user skills shadow bundled system skills.
func (p *Provider) GetByName(ctx context.Context, userID, name string) (Skill, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Skill{}, fmt.Errorf("name_required")
	}
	if p != nil && p.Store != nil && p.Store.Enabled {
		row, err := p.Store.GetByName(ctx, userID, name)
		if err == nil {
			return rowToSkill(row), nil
		}
		if !errors.Is(err, storage.ErrSkillNotFound) {
			return Skill{}, err
		}
	}
	if s, ok := System(name); ok {
		return s, nil
	}
	return Skill{}, fmt.Errorf("skill_not_found: %s", name)
}

// Match ranks all visible skills (user + system) against a goal.
func (p *Provider) Match(ctx context.Context, userID, goal string, limit int) []MatchScore {
	all, err := p.List(ctx, userID)
	if err != nil {
		all = Bundled()
	}
	return Match(goal, all, limit)
}

// SaveUser creates or updates a user skill (used by save_skill tool).
func (p *Provider) SaveUser(ctx context.Context, userID string, in NewSkillInput) (Skill, error) {
	if p == nil || p.Store == nil || !p.Store.Enabled {
		return Skill{}, fmt.Errorf("skills_disabled")
	}
	row, err := p.Store.UpsertByName(ctx, userID, storage.NewSkillInput(in))
	if err != nil {
		return Skill{}, err
	}
	return rowToSkill(row), nil
}

type NewSkillInput = storage.NewSkillInput

func rowToSkill(r storage.Skill) Skill {
	return Skill{
		ID:          r.ID,
		UserID:      r.UserID,
		Name:        r.Name,
		Description: r.Description,
		Content:     r.Content,
		Source:      r.Source,
		AgentRunID:  r.AgentRunID,
		Version:     r.Version,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

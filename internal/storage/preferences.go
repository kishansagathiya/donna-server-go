package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Preferences struct {
	DB      *Supabase
	Enabled bool

	mu    sync.Mutex
	cache map[string]prefsCacheEntry
}

type prefsCacheEntry struct {
	model       string
	persona     string
	personaCust string
	timezone    string
	expiresAt   time.Time
}

const prefsCacheTTL = 60 * time.Second

type PrefsRow struct {
	LLMModel    string `json:"llm_model"`
	Persona     string `json:"persona"`
	PersonaCust string `json:"persona_custom"`
	Timezone    string `json:"timezone"`
}

// GetChatPreferences returns the single preferences row used to configure a
// chat turn. Callers that need both persona and model should use this instead
// of resolving those fields separately so a cold cache only waits once.
func (p *Preferences) GetChatPreferences(ctx context.Context, userID string) (PrefsRow, error) {
	return p.loadRow(ctx, userID)
}

// loadRow fetches the user's preferences row with a 60s per-user cache.
func (p *Preferences) loadRow(ctx context.Context, userID string) (PrefsRow, error) {
	if p == nil || !p.Enabled || p.DB == nil {
		return PrefsRow{}, nil
	}

	now := time.Now()
	p.mu.Lock()
	if p.cache != nil {
		if entry, ok := p.cache[userID]; ok && entry.expiresAt.After(now) {
			p.mu.Unlock()
			return PrefsRow{LLMModel: entry.model, Persona: entry.persona, PersonaCust: entry.personaCust, Timezone: entry.timezone}, nil
		}
	}
	p.mu.Unlock()

	q := url.Values{}
	q.Set("select", "llm_model,persona,persona_custom,timezone")
	q.Set("user_id", "eq."+userID)

	var rows []PrefsRow
	if err := p.DB.Get(ctx, "user_preferences", q, &rows); err != nil {
		return PrefsRow{}, err
	}
	row := PrefsRow{}
	if len(rows) > 0 {
		row = rows[0]
	}

	p.mu.Lock()
	if p.cache == nil {
		p.cache = make(map[string]prefsCacheEntry)
	}
	p.cache[userID] = prefsCacheEntry{
		model:       row.LLMModel,
		persona:     row.Persona,
		personaCust: row.PersonaCust,
		timezone:    row.Timezone,
		expiresAt:   now.Add(prefsCacheTTL),
	}
	p.mu.Unlock()

	return row, nil
}

func (p *Preferences) GetLLMModel(ctx context.Context, userID string) (string, error) {
	row, err := p.loadRow(ctx, userID)
	if err != nil {
		return "", err
	}
	return row.LLMModel, nil
}

func (p *Preferences) SetLLMModel(ctx context.Context, userID, model string) error {
	if p == nil || !p.Enabled || p.DB == nil {
		return fmt.Errorf("preferences unavailable")
	}
	body := map[string]any{
		"user_id":    userID,
		"llm_model":  model,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	if err := p.DB.Upsert(ctx, "user_preferences", "user_id", body, nil); err != nil {
		return err
	}
	p.invalidate(userID)
	return nil
}

// GetPersona returns the user's persona id and optional custom prompt text.
// Missing/empty values yield ("companion", "") as defaults.
func (p *Preferences) GetPersona(ctx context.Context, userID string) (persona, personaCustom string, err error) {
	row, err := p.loadRow(ctx, userID)
	if err != nil {
		return "companion", "", err
	}
	persona = strings.TrimSpace(row.Persona)
	if persona == "" {
		persona = "companion"
	}
	personaCustom = strings.TrimSpace(row.PersonaCust)
	return persona, personaCustom, nil
}

func (p *Preferences) SetPersona(ctx context.Context, userID, persona, personaCustom string) error {
	if p == nil || !p.Enabled || p.DB == nil {
		return fmt.Errorf("preferences unavailable")
	}
	body := map[string]any{
		"user_id":        userID,
		"persona":        persona,
		"persona_custom": personaCustom,
		"updated_at":     time.Now().UTC().Format(time.RFC3339),
	}
	if err := p.DB.Upsert(ctx, "user_preferences", "user_id", body, nil); err != nil {
		return err
	}
	p.invalidate(userID)
	return nil
}

// GetTimezone returns the user's IANA timezone preference (e.g. Asia/Kolkata).
func (p *Preferences) GetTimezone(ctx context.Context, userID string) (string, error) {
	row, err := p.loadRow(ctx, userID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(row.Timezone), nil
}

func (p *Preferences) SetTimezone(ctx context.Context, userID, timezone string) error {
	if p == nil || !p.Enabled || p.DB == nil {
		return fmt.Errorf("preferences unavailable")
	}
	timezone = strings.TrimSpace(timezone)
	if timezone != "" {
		if _, err := time.LoadLocation(timezone); err != nil {
			return fmt.Errorf("invalid_timezone")
		}
	}
	body := map[string]any{
		"user_id":    userID,
		"timezone":   timezone,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	if err := p.DB.Upsert(ctx, "user_preferences", "user_id", body, nil); err != nil {
		return err
	}
	p.invalidate(userID)
	return nil
}

func (p *Preferences) invalidate(userID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cache != nil {
		delete(p.cache, userID)
	}
}

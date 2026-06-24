package storage

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

type Preferences struct {
	DB      *Supabase
	Enabled bool
}

func (p *Preferences) GetLLMModel(ctx context.Context, userID string) (string, error) {
	if p == nil || !p.Enabled || p.DB == nil {
		return "", nil
	}

	q := url.Values{}
	q.Set("select", "llm_model")
	q.Set("user_id", "eq."+userID)

	var rows []struct {
		LLMModel string `json:"llm_model"`
	}
	if err := p.DB.Get(ctx, "user_preferences", q, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].LLMModel, nil
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
	return p.DB.Upsert(ctx, "user_preferences", "user_id", body, nil)
}

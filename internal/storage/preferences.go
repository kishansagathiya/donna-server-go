package storage

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"
)

type Preferences struct {
	DB      *Supabase
	Enabled bool

	mu         sync.Mutex
	modelCache map[string]modelCacheEntry
}

type modelCacheEntry struct {
	value     string
	expiresAt time.Time
}

const llmModelCacheTTL = 60 * time.Second

func (p *Preferences) GetLLMModel(ctx context.Context, userID string) (string, error) {
	if p == nil || !p.Enabled || p.DB == nil {
		return "", nil
	}

	now := time.Now()
	p.mu.Lock()
	if p.modelCache != nil {
		if entry, ok := p.modelCache[userID]; ok && entry.expiresAt.After(now) {
			p.mu.Unlock()
			return entry.value, nil
		}
	}
	p.mu.Unlock()

	q := url.Values{}
	q.Set("select", "llm_model")
	q.Set("user_id", "eq."+userID)

	var rows []struct {
		LLMModel string `json:"llm_model"`
	}
	if err := p.DB.Get(ctx, "user_preferences", q, &rows); err != nil {
		return "", err
	}
	value := ""
	if len(rows) > 0 {
		value = rows[0].LLMModel
	}

	p.mu.Lock()
	if p.modelCache == nil {
		p.modelCache = make(map[string]modelCacheEntry)
	}
	p.modelCache[userID] = modelCacheEntry{value: value, expiresAt: now.Add(llmModelCacheTTL)}
	p.mu.Unlock()

	return value, nil
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
	p.invalidateLLMModelCache(userID)
	return nil
}

func (p *Preferences) invalidateLLMModelCache(userID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.modelCache != nil {
		delete(p.modelCache, userID)
	}
}

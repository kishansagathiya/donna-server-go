package pipeline

import (
	"context"
	"strings"
	"unicode"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
)

// RouteDecision records how a model was chosen for observability.
type RouteDecision struct {
	Model  string `json:"model"`
	Route  string `json:"route"` // "user" | "fast" | "strong" | "default"
	Reason string `json:"reason"`
}

// resolveLLM picks the LLM for a turn. Explicit user model selection always
// wins. When auto-routing is enabled and the user has no override, simple
// prompts take the fast model and harder prompts take the strong/default model.
func (e *Engine) resolveLLM(ctx context.Context, userID, userMessage string) (*providers.LLM, RouteDecision) {
	decision := RouteDecision{
		Model:  e.LLM.Model,
		Route:  "default",
		Reason: "configured_default",
	}

	if e.Preferences != nil && userID != "" {
		model, err := e.Preferences.GetLLMModel(ctx, userID)
		if err == nil && strings.TrimSpace(model) != "" {
			model = strings.TrimSpace(model)
			if e.isAllowedModel(model) {
				decision = RouteDecision{
					Model:  model,
					Route:  "user",
					Reason: "explicit_user_preference",
				}
				logRoute(userID, decision)
				return e.LLM.WithModel(model), decision
			}
			decision.Reason = "user_model_not_allowlisted"
		}
	}

	if e.Config == nil || !e.Config.AutoRouteEnabled {
		logRoute(userID, decision)
		return e.LLM, decision
	}

	// Empty message means caller wants preference/default only (no heuristic).
	if strings.TrimSpace(userMessage) == "" {
		logRoute(userID, decision)
		return e.LLM, decision
	}

	fast := strings.TrimSpace(e.Config.LLMFastModel)
	strong := strings.TrimSpace(e.Config.LLMModel)
	if fast == "" || !e.isAllowedModel(fast) || fast == strong {
		decision.Reason = "auto_route_unavailable"
		logRoute(userID, decision)
		return e.LLM, decision
	}

	if shouldUseFastModel(userMessage) {
		decision = RouteDecision{
			Model:  fast,
			Route:  "fast",
			Reason: "heuristic_simple_prompt",
		}
		logRoute(userID, decision)
		return e.LLM.WithModel(fast), decision
	}

	decision = RouteDecision{
		Model:  strong,
		Route:  "strong",
		Reason: "heuristic_complex_prompt",
	}
	logRoute(userID, decision)
	return e.LLM.WithModel(strong), decision
}

func (e *Engine) isAllowedModel(model string) bool {
	if e.Config == nil {
		return false
	}
	for _, allowed := range e.Config.LLMModels {
		if model == allowed {
			return true
		}
	}
	return model == e.Config.LLMModel
}

// shouldUseFastModel is a lightweight heuristic: short, non-memory, non-planning
// prompts can take the fast path. Everything else uses the strong model.
func shouldUseFastModel(message string) bool {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return true
	}
	if NeedsUserContext(msg) {
		return false
	}
	if looksLikeChitchat(msg) {
		return true
	}

	lower := strings.ToLower(msg)
	complexHints := []string{
		"compare", "analysis", "analyze", "analyse", "plan", "roadmap",
		"strategy", "architecture", "debug", "implement", "refactor",
		"write a", "draft", "explain in detail", "step by step", "pros and cons",
		"tradeoff", "trade-off", "research", "summarize the following",
		"code review", "design",
	}
	for _, hint := range complexHints {
		if strings.Contains(lower, hint) {
			return false
		}
	}

	words := countWords(msg)
	if words <= 12 && len(msg) <= 100 {
		return true
	}
	return false
}

func countWords(s string) int {
	n := 0
	inWord := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			inWord = false
			continue
		}
		if !inWord {
			n++
			inWord = true
		}
	}
	return n
}

func logRoute(userID string, decision RouteDecision) {
	log.Print("llm route", map[string]any{
		"userId": log.ShortID(userID),
		"model":  decision.Model,
		"route":  decision.Route,
		"reason": decision.Reason,
	})
}

package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// kindToActionSlug maps extracted intent kinds to system action slugs.
var kindToActionSlug = map[string]string{
	"remind":         "propose_reminder",
	"reminder":       "propose_reminder",
	"propose_reminder": "propose_reminder",
	"follow_up":      "draft_message",
	"followup":       "draft_message",
	"draft_message":  "draft_message",
	"message":        "draft_message",
	"email":          "draft_message",
	"schedule":       "propose_reminder",
	"open_url":       "open_url",
	"url":            "open_url",
}

type Matcher struct {
	Store    *storage.ActionsStore
	Executor *Executor
	// AutoInternal, when true, auto-confirms and executes risk=internal builtins.
	AutoInternal bool
}

func (m *Matcher) MatchIntent(ctx context.Context, userID string, intent storage.Intent) error {
	if m == nil || m.Store == nil || !m.Store.Enabled {
		return nil
	}
	if intent.Status != "open" {
		return nil
	}
	if _, err := m.Store.FindActiveRunForIntent(ctx, userID, intent.ID); err == nil {
		return nil
	}

	slug := kindToActionSlug[strings.ToLower(strings.TrimSpace(intent.Kind))]
	if slug == "" {
		log.Print("no action match for intent kind", map[string]any{
			"kind":     intent.Kind,
			"intentId": log.ShortID(intent.ID),
		})
		return nil
	}
	if slug == "create_note" {
		return fmt.Errorf("create_note_forbidden")
	}

	action, err := m.Store.GetSystemActionBySlug(ctx, slug)
	if err != nil {
		return err
	}

	input := buildRunInput(intent, action.Slug)
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return err
	}
	intentID := intent.ID
	run, err := m.Store.CreateActionRun(ctx, userID, storage.NewActionRunInput{
		IntentID: &intentID,
		ActionID: action.ID,
		Status:   "proposed",
		Input:    inputJSON,
	})
	if err != nil {
		return err
	}

	if m.AutoInternal && action.Risk == "internal" && m.Executor != nil {
		if _, err := m.Executor.ConfirmAndExecute(ctx, userID, run.ID); err != nil {
			log.Warn("auto-execute internal action failed", map[string]any{
				"runId": log.ShortID(run.ID),
				"error": err.Error(),
			})
		}
	}
	return nil
}

func buildRunInput(intent storage.Intent, actionSlug string) map[string]any {
	slots := SlotsToInput(intent.Slots)
	out := map[string]any{}
	for k, v := range slots {
		out[k] = v
	}
	if _, ok := out["title"]; !ok && (actionSlug == "propose_reminder") {
		out["title"] = intent.Summary
	}
	if _, ok := out["body"]; !ok && actionSlug == "draft_message" {
		out["body"] = intent.Summary
	}
	if _, ok := out["summary"]; !ok {
		out["summary"] = intent.Summary
	}
	out["intent_kind"] = intent.Kind
	return out
}

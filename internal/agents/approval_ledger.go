package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// ApprovalRecorder writes request_approval to the action_runs ledger.
type ApprovalRecorder interface {
	RecordRequest(ctx context.Context, userID, agentRunID string, payload map[string]any) (actionRunID string, err error)
}

// SessionCloser destroys an interactive browser session for a finished run.
type SessionCloser interface {
	CloseSession(ctx context.Context, sessionID string) error
}

// ActionApprovalLedger creates proposed agent_approval action_runs (no inbox intent).
type ActionApprovalLedger struct {
	Store *storage.ActionsStore
}

func (l *ActionApprovalLedger) RecordRequest(ctx context.Context, userID, agentRunID string, payload map[string]any) (string, error) {
	if l == nil || l.Store == nil || !l.Store.Enabled {
		return "", nil
	}
	action, err := l.Store.GetSystemActionBySlug(ctx, "agent_approval")
	if err != nil {
		return "", err
	}
	kind := ""
	if args, ok := payload["args"].(map[string]any); ok {
		kind = trimAny(args["kind"])
	}
	if kind == "" || kind == "request_approval" {
		if k := trimAny(payload["kind"]); k != "" && k != "request_approval" {
			kind = k
		}
	}
	if kind == "" {
		kind = "request_approval"
	}
	summary := trimAny(payload["summary"])
	if summary == "" {
		summary = trimAny(payload["question"])
	}
	if summary == "" {
		summary = "Donna needs approval to continue."
	}
	input := map[string]any{
		"kind":         kind,
		"summary":      summary,
		"intent_kind":  "agent_approval",
		"agent_run_id": agentRunID,
	}
	if details := payloadDetails(payload); details != nil {
		input["details"] = details
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	runID := strings.TrimSpace(agentRunID)
	run, err := l.Store.CreateActionRun(ctx, userID, storage.NewActionRunInput{
		ActionID:     action.ID,
		Status:       "proposed",
		Input:        raw,
		AgentRunID:   &runID,
		ApprovalKind: kind,
	})
	if err != nil {
		return "", err
	}
	return run.ID, nil
}

func payloadDetails(payload map[string]any) any {
	if payload == nil {
		return nil
	}
	if d, ok := payload["details"]; ok && d != nil {
		return d
	}
	if args, ok := payload["args"].(map[string]any); ok {
		if d, ok := args["details"]; ok && d != nil {
			return d
		}
	}
	return nil
}

func trimAny(v any) string {
	if v == nil {
		return ""
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "" || s == "<nil>" {
		return ""
	}
	return s
}

func logApprovalLedger(err error, runID string) {
	if err == nil {
		return
	}
	log.Warn("agent approval ledger failed", map[string]any{
		"runId": log.ShortID(runID),
		"error": err.Error(),
	})
}

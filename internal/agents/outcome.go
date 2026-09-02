package agents

import (
	"encoding/json"
	"strings"
)

// ToolOutcome is persisted on tool_result (and optional on ToolResult).
type ToolOutcome string

const (
	OutcomeSucceeded ToolOutcome = "succeeded"
	OutcomeFailed    ToolOutcome = "failed"
	OutcomeBlocked   ToolOutcome = "blocked"
)

func (o ToolOutcome) Valid() bool {
	switch o {
	case OutcomeSucceeded, OutcomeFailed, OutcomeBlocked:
		return true
	default:
		return false
	}
}

// ResolveOutcome fills in an omitted ToolResult outcome.
// A handler error or nonzero shell exit is failed, content starting with
// "Error:" is failed, "Refused:" is blocked, and everything else succeeded.
func ResolveOutcome(result ToolResult, handlerErr error) ToolOutcome {
	if result.Outcome.Valid() {
		return result.Outcome
	}
	if handlerErr != nil {
		return OutcomeFailed
	}
	if exit, ok := metaExit(result.Meta); ok && exit != 0 {
		return OutcomeFailed
	}
	content := strings.TrimSpace(result.Content)
	if strings.HasPrefix(content, "Error:") {
		return OutcomeFailed
	}
	if strings.HasPrefix(content, "Refused:") {
		return OutcomeBlocked
	}
	return OutcomeSucceeded
}

func metaExit(meta map[string]any) (int, bool) {
	if meta == nil {
		return 0, false
	}
	switch v := meta["exit"].(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

func outcomeError(result ToolResult, handlerErr error, outcome ToolOutcome) string {
	if outcome != OutcomeFailed && outcome != OutcomeBlocked {
		return ""
	}
	if strings.TrimSpace(result.Error) != "" {
		return result.Error
	}
	if handlerErr != nil {
		return handlerErr.Error()
	}
	return strings.TrimSpace(result.Content)
}

func attachRecovery(payload map[string]any, pending *[]string) {
	if payload == nil || pending == nil || len(*pending) == 0 {
		return
	}
	payload["recovery_from"] = append([]string(nil), (*pending)...)
	*pending = nil
}

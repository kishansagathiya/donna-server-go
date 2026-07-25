package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// BuiltinName identifies a first-party runner. create_note is intentionally absent.
type BuiltinName string

const (
	BuiltinDraftMessage        BuiltinName = "draft_message"
	BuiltinProposeReminder     BuiltinName = "propose_reminder"
	BuiltinOpenURL             BuiltinName = "open_url"
	BuiltinCreateCalendarEvent BuiltinName = "create_calendar_event"
	BuiltinSendEmail           BuiltinName = "send_email"
)

type BuiltinResult struct {
	Output map[string]any
}

type BuiltinRunner struct{}

func (r *BuiltinRunner) Run(ctx context.Context, name BuiltinName, input map[string]any) (BuiltinResult, error) {
	_ = ctx
	switch name {
	case BuiltinDraftMessage:
		return r.draftMessage(input)
	case BuiltinProposeReminder:
		return r.proposeReminder(input)
	case BuiltinOpenURL:
		return r.openURL(input)
	case BuiltinCreateCalendarEvent, BuiltinSendEmail:
		// Side-effecting integrations are handled by Executor.Integrations.
		return BuiltinResult{}, fmt.Errorf("integration_builtin:%s", name)
	default:
		return BuiltinResult{}, fmt.Errorf("unknown_builtin:%s", name)
	}
}

// IsIntegrationBuiltin reports builtins that require a connected provider.
func IsIntegrationBuiltin(name BuiltinName) bool {
	return name == BuiltinCreateCalendarEvent || name == BuiltinSendEmail
}

func (r *BuiltinRunner) draftMessage(input map[string]any) (BuiltinResult, error) {
	body := stringSlot(input, "body")
	if body == "" {
		return BuiltinResult{}, fmt.Errorf("body_required")
	}
	return BuiltinResult{Output: map[string]any{
		"type":      "draft_message",
		"recipient": stringSlot(input, "recipient"),
		"subject":   stringSlot(input, "subject"),
		"channel":   defaultString(stringSlot(input, "channel"), "message"),
		"body":      body,
		"drafted_at": time.Now().UTC().Format(time.RFC3339),
		"sent":      false,
	}}, nil
}

func (r *BuiltinRunner) proposeReminder(input map[string]any) (BuiltinResult, error) {
	title := stringSlot(input, "title")
	if title == "" {
		return BuiltinResult{}, fmt.Errorf("title_required")
	}
	return BuiltinResult{Output: map[string]any{
		"type":       "propose_reminder",
		"title":      title,
		"when":       stringSlot(input, "when"),
		"notes":      stringSlot(input, "notes"),
		"proposed_at": time.Now().UTC().Format(time.RFC3339),
		"scheduled":  false,
	}}, nil
}

func (r *BuiltinRunner) openURL(input map[string]any) (BuiltinResult, error) {
	rawURL := stringSlot(input, "url")
	if rawURL == "" {
		return BuiltinResult{}, fmt.Errorf("url_required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return BuiltinResult{}, fmt.Errorf("invalid_url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return BuiltinResult{}, fmt.Errorf("unsupported_url_scheme")
	}
	return BuiltinResult{Output: map[string]any{
		"type":      "open_url",
		"url":       parsed.String(),
		"label":     defaultString(stringSlot(input, "label"), parsed.Host),
		"opened":    false,
		"ready_at":  time.Now().UTC().Format(time.RFC3339),
	}}, nil
}

func BuiltinFromConfig(config json.RawMessage) (BuiltinName, error) {
	if len(config) == 0 {
		return "", fmt.Errorf("missing_config")
	}
	var cfg struct {
		Builtin string `json:"builtin"`
	}
	if err := json.Unmarshal(config, &cfg); err != nil {
		return "", err
	}
	name := BuiltinName(strings.TrimSpace(cfg.Builtin))
	switch name {
	case BuiltinDraftMessage, BuiltinProposeReminder, BuiltinOpenURL,
		BuiltinCreateCalendarEvent, BuiltinSendEmail:
		return name, nil
	default:
		return "", fmt.Errorf("unknown_builtin:%s", name)
	}
}

func stringSlot(input map[string]any, key string) string {
	if input == nil {
		return ""
	}
	v, ok := input[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func InputMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

func SlotsToInput(slots json.RawMessage) map[string]any {
	return InputMap(slots)
}

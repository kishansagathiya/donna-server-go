package reminders

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
)

const ChatToolPrompt = `You can set timed reminders with set_reminder when the user asks to be reminded (now, in N minutes, tomorrow 4pm, etc.). Create the reminder immediately and confirm the time back to them. Do not invent a time if they did not give one — ask, or omit when to default to one hour from now.`

// SetReminderChatTool creates a reminder immediately for this user.
func SetReminderChatTool(userID string, svc *Service) tools.RegisteredTool {
	return tools.RegisteredTool{
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "set_reminder",
				Description: "Set a timed reminder for this user. Use when they ask to be reminded about something at a time (e.g. in 10 minutes, tomorrow 4pm). Creates the reminder immediately. Confirm the scheduled time in your reply.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":    map[string]any{"type": "string", "description": "Short reminder text"},
						"when":     map[string]any{"type": "string", "description": "When to fire, e.g. in 10 minutes, tomorrow 4pm, Saturday morning"},
						"notes":    map[string]any{"type": "string"},
						"timezone": map[string]any{"type": "string", "description": "IANA timezone if known"},
					},
					"required": []string{"title"},
				},
			},
		},
		Handle: func(ctx context.Context, argsJSON string) (tools.Result, error) {
			var args struct {
				Title    string `json:"title"`
				When     string `json:"when"`
				Notes    string `json:"notes"`
				Timezone string `json:"timezone"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
				return tools.Result{Content: "Error: invalid arguments"}, nil
			}
			if svc == nil {
				return tools.Result{Content: "Error: reminders_unavailable"}, nil
			}
			if strings.TrimSpace(userID) == "" {
				return tools.Result{Content: "Error: missing_user"}, nil
			}
			rem, err := svc.Create(ctx, userID, CreateInput{
				Title:    args.Title,
				When:     args.When,
				Notes:    args.Notes,
				Timezone: args.Timezone,
			})
			if err != nil {
				return tools.Result{Content: "Error: " + err.Error()}, nil
			}
			when := strings.TrimSpace(args.When)
			if when == "" {
				when = rem.DueAt
			}
			return tools.Result{
				Content: fmt.Sprintf("Reminder set: %q at %s (id %s). Tell the user it is scheduled.", rem.Title, rem.DueAt, rem.ID),
			}, nil
		},
	}
}

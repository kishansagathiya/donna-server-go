package granola

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/connectors"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
)

// Donna-owned allowlisted tool names (map to Granola MCP tools).
const (
	ToolQueryMeetings = "granola_query_meetings"
	ToolGetTranscript = "granola_get_transcript"
)

func (a *Adapter) buildLiveTools(conn connectors.Connection) []tools.RegisteredTool {
	var out []tools.RegisteredTool
	if conn.Capabilities.LiveQueryMeetings || conn.Capabilities.ListMeetings || conn.Status == connectors.StatusConnected {
		out = append(out, tools.RegisteredTool{
			Definition: providers.ToolDefinition{
				Type: "function",
				Function: providers.ToolFunctionSchema{
					Name:        ToolQueryMeetings,
					Description: "Ask a read-only question about the user's Granola meeting notes (summaries, decisions, attendees).",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query": map[string]any{
								"type":        "string",
								"description": "Natural language question about meetings",
							},
						},
						"required": []string{"query"},
					},
				},
			},
			Handle: a.handleQueryMeetings(conn),
		})
	}
	if conn.Capabilities.LiveGetTranscript || conn.Capabilities.Transcripts {
		out = append(out, tools.RegisteredTool{
			Definition: providers.ToolDefinition{
				Type: "function",
				Function: providers.ToolFunctionSchema{
					Name:        ToolGetTranscript,
					Description: "Fetch a read-only Granola meeting transcript by meeting id (paid plans). Attribute quotes to Granola.",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"meeting_id": map[string]any{
								"type":        "string",
								"description": "Granola meeting id",
							},
						},
						"required": []string{"meeting_id"},
					},
				},
			},
			Handle: a.handleGetTranscript(conn),
		})
	}
	return out
}

func (a *Adapter) handleQueryMeetings(conn connectors.Connection) tools.Handler {
	return func(ctx context.Context, argsJSON string) (tools.Result, error) {
		var args struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil || strings.TrimSpace(args.Query) == "" {
			return tools.Result{Content: "Error: query is required"}, nil
		}
		caller, err := a.OpenMCP(ctx, conn)
		if err != nil {
			return tools.Result{Content: "Error: Granola unavailable"}, nil
		}
		defer caller.Close()

		raw, err := caller.CallTool(ctx, "query_granola_meetings", map[string]any{"query": args.Query})
		if err != nil {
			// Capability fallback: list + get meetings.
			raw, err = a.fallbackQuery(ctx, caller, args.Query)
			if err != nil {
				return tools.Result{Content: "Error: " + err.Error()}, nil
			}
		}
		wrapped := connectors.WrapUntrustedMCPResult(connectors.ProviderGranola, ToolQueryMeetings, raw)
		return tools.Result{
			Content: wrapped,
			Phase:   "generating",
			Citations: []tools.Citation{{
				Title:   "Granola",
				Content: truncate(raw, 160),
				Source:  "granola",
			}},
		}, nil
	}
}

func (a *Adapter) handleGetTranscript(conn connectors.Connection) tools.Handler {
	return func(ctx context.Context, argsJSON string) (tools.Result, error) {
		var args struct {
			MeetingID string `json:"meeting_id"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil || strings.TrimSpace(args.MeetingID) == "" {
			return tools.Result{Content: "Error: meeting_id is required"}, nil
		}
		caller, err := a.OpenMCP(ctx, conn)
		if err != nil {
			return tools.Result{Content: "Error: Granola unavailable"}, nil
		}
		defer caller.Close()

		raw, err := caller.CallTool(ctx, "get_meeting_transcript", map[string]any{
			"meeting_id": args.MeetingID,
		})
		if err != nil {
			return tools.Result{Content: "Error: transcript unavailable (may require a paid Granola plan)"}, nil
		}
		wrapped := connectors.WrapUntrustedMCPResult(connectors.ProviderGranola, ToolGetTranscript, raw)
		return tools.Result{
			Content: wrapped,
			Citations: []tools.Citation{{
				Title:   "Granola transcript",
				Content: truncate(raw, 160),
				Source:  "granola",
			}},
		}, nil
	}
}

func (a *Adapter) fallbackQuery(ctx context.Context, caller MCPCaller, query string) (string, error) {
	listed, err := caller.CallTool(ctx, "list_meetings", map[string]any{})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Query: %s\n\nAccessible meetings:\n%s", query, listed), nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

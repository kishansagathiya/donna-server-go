package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
)

// DefaultToolsets builds the agent tool registry.
// When browserURL is set (donna-browser sidecar), browse_page is registered.
// When prov is non-nil (skills enabled), skills tools are registered.
// When employees is non-nil, report_progress / complete_goal are registered.
func DefaultToolsets(mem MemorySearcher, notes NoteSearcher, browserURL string, prov SkillProvider, employees EmployeeProgressWriter) *Registry {
	reg := NewRegistry()
	reg.Register(todoTool())
	reg.Register(askUserTool())
	reg.Register(requestApprovalTool())
	if mem != nil {
		reg.Register(memorySearchTool(mem))
	}
	if notes != nil {
		reg.Register(searchNotesTool(notes))
	}
	reg.Register(fetchURLTool())
	if client := tools.NewBrowserClient(browserURL); client != nil {
		reg.Register(browsePageTool(client))
	}
	reg.Register(sessionSearchTool())
	if prov != nil {
		reg.Register(loadSkillTool(prov))
		reg.Register(saveSkillTool(prov))
		reg.Register(listSkillsTool(prov))
	}
	if employees != nil {
		for _, t := range employeeTools(employees) {
			reg.Register(t)
		}
	}
	return reg
}

func todoTool() RegisteredTool {
	return RegisteredTool{
		Toolset: "orchestration",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "todo",
				Description: "Create or update a short plan checklist for this agent run. Keep 3–8 items.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"items": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"id":      map[string]any{"type": "string"},
									"content": map[string]any{"type": "string"},
									"status":  map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "done"}},
								},
								"required": []string{"id", "content", "status"},
							},
						},
					},
					"required": []string{"items"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			args, err := ParseArgs[struct {
				Items []TodoItem `json:"items"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			if runCtx != nil && runCtx.SetPlan != nil {
				runCtx.SetPlan(args.Items)
			}
			raw, _ := json.Marshal(args.Items)
			return ToolResult{Content: "Plan updated: " + string(raw), Meta: map[string]any{"plan": args.Items}}, nil
		},
	}
}

func askUserTool() RegisteredTool {
	return RegisteredTool{
		Toolset: "orchestration",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "ask_user",
				Description: "Pause and ask the user a clarifying question. Prefer multiple-choice options so the user can tap choices instead of typing. Do not ask in plain text and stop — call this tool so they get a Reply UI.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"question": map[string]any{
							"type":        "string",
							"description": "Clear question for the user (markdown ok)",
						},
						"context": map[string]any{
							"type":        "string",
							"description": "Optional short context for why you need this",
						},
						"options": map[string]any{
							"type":        "array",
							"description": "Choices the user can tap. Use whenever the answer is one of a few discrete options (airports, dates, yes/no, airlines, etc.).",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"id":    map[string]any{"type": "string", "description": "Stable id, e.g. sfo"},
									"label": map[string]any{"type": "string", "description": "Button label shown to the user"},
								},
								"required": []string{"id", "label"},
							},
						},
						"allow_multiple": map[string]any{
							"type":        "boolean",
							"description": "If true, user may select more than one option. Default false.",
						},
					},
					"required": []string{"question"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			return ToolResult{Content: "Waiting for user reply."}, nil
		},
	}
}

func requestApprovalTool() RegisteredTool {
	return RegisteredTool{
		Toolset: "orchestration",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "request_approval",
				Description: "Pause the agent and ask the user to approve an irreversible step (payment, booking, send). Provide a clear summary.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind":    map[string]any{"type": "string", "description": "e.g. book_flight, pay, send_email"},
						"summary": map[string]any{"type": "string"},
						"details": map[string]any{"type": "object"},
					},
					"required": []string{"kind", "summary"},
				},
			},
		},
		// Handled specially in the harness (pauses run). This handler is a fallback.
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			return ToolResult{Content: "Approval requested. Waiting for user."}, nil
		},
	}
}

func memorySearchTool(mem MemorySearcher) RegisteredTool {
	return RegisteredTool{
		Toolset: "memory",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "memory_search",
				Description: "Search the user's Donna memory (facts, notes snippets, integrations) for personal context relevant to the goal.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
						"limit": map[string]any{"type": "integer"},
					},
					"required": []string{"query"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			args, err := ParseArgs[struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			limit := args.Limit
			if limit <= 0 {
				limit = 8
			}
			hits, err := mem.Search(ctx, runCtx.UserID, args.Query, limit)
			if err != nil {
				return ToolResult{Content: "Error: " + err.Error()}, nil
			}
			if len(hits) == 0 {
				return ToolResult{Content: "No memory hits.", Meta: map[string]any{"hits": []any{}}}, nil
			}
			var b strings.Builder
			for i, h := range hits {
				fmt.Fprintf(&b, "%d. [%s score=%.2f id=%s] %s\n", i+1, h.Source, h.Score, h.ID, truncate(h.Text, 500))
			}
			return ToolResult{Content: b.String(), Meta: map[string]any{"hits": hits}}, nil
		},
	}
}

func searchNotesTool(notes NoteSearcher) RegisteredTool {
	return RegisteredTool{
		Toolset: "memory",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "search_notes",
				Description: "Full-text search the user's notes for titles and previews matching a query.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
						"limit": map[string]any{"type": "integer"},
					},
					"required": []string{"query"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			args, err := ParseArgs[struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			limit := args.Limit
			if limit <= 0 {
				limit = 10
			}
			hits, err := notes.Search(ctx, runCtx.UserID, args.Query, limit)
			if err != nil {
				return ToolResult{Content: "Error: " + err.Error()}, nil
			}
			if len(hits) == 0 {
				return ToolResult{Content: "No notes found."}, nil
			}
			var b strings.Builder
			for i, h := range hits {
				title := h.Title
				if title == "" {
					title = "(untitled)"
				}
				fmt.Fprintf(&b, "%d. %s [%s]\n%s\n\n", i+1, title, h.ID, truncate(h.Preview, 400))
			}
			return ToolResult{Content: b.String(), Meta: map[string]any{"notes": hits}}, nil
		},
	}
}

func fetchURLTool() RegisteredTool {
	inner := tools.NewFetchURLHandler()
	return RegisteredTool{
		Toolset:    "web",
		Definition: tools.FetchURLDefinition(),
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			res, err := inner(ctx, argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			return ToolResult{Content: res.Content, Meta: map[string]any{"host": res.Host}}, nil
		},
	}
}

func browsePageTool(client *tools.BrowserClient) RegisteredTool {
	inner := tools.NewBrowsePageHandler(client)
	return RegisteredTool{
		Toolset:    "browser",
		Definition: tools.BrowsePageDefinition(),
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			res, err := inner(ctx, argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			meta := map[string]any{}
			if res.Host != "" {
				meta["host"] = res.Host
			}
			if res.Phase != "" {
				meta["phase"] = res.Phase
			}
			return ToolResult{Content: res.Content, Meta: meta}, nil
		},
	}
}

func sessionSearchTool() RegisteredTool {
	return RegisteredTool{
		Toolset: "memory",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "session_search",
				Description: "Search this agent run's own prior step log (tool results and thoughts) by keyword.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
					},
					"required": []string{"query"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			store, _ := runCtx.Extra["store"].(RunStore)
			if store == nil {
				return ToolResult{Content: "session_search unavailable"}, nil
			}
			args, err := ParseArgs[struct {
				Query string `json:"query"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			q := strings.ToLower(strings.TrimSpace(args.Query))
			steps, err := store.ListSteps(ctx, runCtx.UserID, runCtx.RunID, 0, 500)
			if err != nil {
				return ToolResult{Content: "Error: " + err.Error()}, nil
			}
			var b strings.Builder
			n := 0
			for _, st := range steps {
				payload := strings.ToLower(string(st.Payload))
				if q != "" && !strings.Contains(payload, q) && !strings.Contains(st.Kind, q) {
					continue
				}
				fmt.Fprintf(&b, "seq=%d kind=%s %s\n", st.Seq, st.Kind, truncate(string(st.Payload), 300))
				n++
				if n >= 20 {
					break
				}
			}
			if n == 0 {
				return ToolResult{Content: "No matching steps."}, nil
			}
			return ToolResult{Content: b.String()}, nil
		},
	}
}

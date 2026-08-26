package agents

import (
	"context"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
)

func interactiveBrowserTools(client *tools.BrowserClient) []RegisteredTool {
	if client == nil {
		return nil
	}
	return []RegisteredTool{
		browserNavigateTool(client),
		browserSnapshotTool(client),
		browserClickTool(client),
		browserTypeTool(client),
	}
}

func browserNavigateTool(client *tools.BrowserClient) RegisteredTool {
	return RegisteredTool{
		Toolset: "browser",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "browser_navigate",
				Description: "Open a public URL in this agent run's browser session (cookies persist until the run finishes). Prefer this over browse_page when you will click or type next. Never open localhost or private IPs.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"url":     map[string]any{"type": "string"},
						"wait_ms": map[string]any{"type": "integer", "description": "Optional extra wait after load (ms)"},
					},
					"required": []string{"url"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			args, err := ParseArgs[struct {
				URL    string `json:"url"`
				WaitMs int    `json:"wait_ms"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			snap, err := client.Navigate(ctx, sessionID(runCtx), args.URL, args.WaitMs)
			return snapshotResult(snap, err)
		},
	}
}

func browserSnapshotTool(client *tools.BrowserClient) RegisteredTool {
	return RegisteredTool{
		Toolset: "browser",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "browser_snapshot",
				Description: "List interactive elements on the current page (ref, tag, name) plus visible text. Call this before click/type, and again after navigation.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			snap, err := client.Snapshot(ctx, sessionID(runCtx))
			return snapshotResult(snap, err)
		},
	}
}

func browserClickTool(client *tools.BrowserClient) RegisteredTool {
	return RegisteredTool{
		Toolset: "browser",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "browser_click",
				Description: "Click an element from the latest browser_snapshot by ref (e.g. e3). Do not click Pay / Place order / Submit payment — call request_approval first.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ref": map[string]any{"type": "string", "description": "Element ref from snapshot, like e1"},
					},
					"required": []string{"ref"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			args, err := ParseArgs[struct {
				Ref string `json:"ref"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			ref := strings.TrimSpace(args.Ref)
			if ref == "" {
				return ToolResult{Content: "Error: ref is required"}, nil
			}
			snap, err := client.Click(ctx, sessionID(runCtx), ref)
			return snapshotResult(snap, err)
		},
	}
}

func browserTypeTool(client *tools.BrowserClient) RegisteredTool {
	return RegisteredTool{
		Toolset: "browser",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "browser_type",
				Description: "Type into an input from the latest snapshot by ref. Never type card numbers, CVV, passwords, or OTPs. Set submit true only to press Enter on search fields — not to pay.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ref":    map[string]any{"type": "string"},
						"text":   map[string]any{"type": "string"},
						"submit": map[string]any{"type": "boolean", "description": "Press Enter after typing (search boxes only)"},
					},
					"required": []string{"ref", "text"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			args, err := ParseArgs[struct {
				Ref    string `json:"ref"`
				Text   string `json:"text"`
				Submit bool   `json:"submit"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			ref := strings.TrimSpace(args.Ref)
			if ref == "" {
				return ToolResult{Content: "Error: ref is required"}, nil
			}
			if looksLikeSecret(args.Text) {
				return ToolResult{Content: "Refused: do not type payment card numbers, CVV, or passwords into pages."}, nil
			}
			snap, err := client.Type(ctx, sessionID(runCtx), ref, args.Text, args.Submit)
			return snapshotResult(snap, err)
		},
	}
}

func sessionID(runCtx *RunContext) string {
	if runCtx == nil {
		return ""
	}
	return strings.TrimSpace(runCtx.RunID)
}

func snapshotResult(snap tools.BrowserSnapshot, err error) (ToolResult, error) {
	if err != nil {
		return ToolResult{Content: "Error: " + err.Error()}, nil
	}
	host := ""
	if snap.URL != "" {
		host = snap.URL
	}
	return ToolResult{
		Content: tools.FormatBrowserSnapshot(snap),
		Meta: map[string]any{
			"url":      snap.URL,
			"title":    snap.Title,
			"host":     host,
			"elements": snap.Elements,
		},
	}, nil
}

func looksLikeSecret(text string) bool {
	s := strings.TrimSpace(text)
	if s == "" {
		return false
	}
	if longDigitRun.MatchString(s) {
		return true
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, "cvv") || strings.Contains(lower, "cvc")
}

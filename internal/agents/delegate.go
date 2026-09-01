package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const defaultDelegateTimeout = 3 * time.Minute

// Delegator spawns a child agent run (used by delegate_task).
type Delegator interface {
	Spawn(ctx context.Context, userID string, in SpawnInput) (storage.AgentRun, error)
	WaitUntilDone(ctx context.Context, userID, runID string, timeout time.Duration) (storage.AgentRun, error)
}

func DefaultParentToolAllowlist() []string {
	return []string{"orchestration", "memory", "web", "browser", "skills", "commerce", "delegation"}
}

func DefaultLocalToolAllowlist(hasWorkspace bool) []string {
	allow := []string{"orchestration", "memory", "web", "browser", "skills", "commerce", "delegation"}
	if hasWorkspace {
		allow = append(allow, "workspace", "process")
	}
	return allow
}

func childToolAllowlist() []string {
	return []string{"orchestration", "memory", "web", "browser", "skills"}
}

// DelegateTaskTool registers as toolset "delegation" so children (no that
// toolset) cannot nest further even if they have orchestration.
func DelegateTaskTool(d Delegator) RegisteredTool {
	return RegisteredTool{
		Toolset: "delegation",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "delegate_task",
				Description: "Spawn a child agent for a parallel workstream (research, page extract). Children cannot delegate or use commerce. wait=true (default) blocks until the child finishes, waits for the user, or 3 minutes. Never use this for payment.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"goal": map[string]any{"type": "string", "description": "Self-contained goal for the child"},
						"wait": map[string]any{"type": "boolean", "description": "Wait for the child to finish (default true)"},
					},
					"required": []string{"goal"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			if d == nil {
				return ToolResult{Content: "Error: delegate_task is unavailable"}, nil
			}
			if runCtx == nil || strings.TrimSpace(runCtx.RunID) == "" {
				return ToolResult{Content: "Error: missing agent run"}, nil
			}
			args, err := ParseArgs[struct {
				Goal string `json:"goal"`
				Wait *bool  `json:"wait"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			goal := strings.TrimSpace(args.Goal)
			if goal == "" {
				return ToolResult{Content: "Error: goal is required"}, nil
			}
			if store, _ := runCtx.Extra["store"].(RunStore); store != nil {
				parent, err := store.Get(ctx, runCtx.UserID, runCtx.RunID)
				if err != nil {
					return ToolResult{Content: "Error: " + err.Error()}, nil
				}
				if parent.ParentRunID != nil && strings.TrimSpace(*parent.ParentRunID) != "" {
					return ToolResult{Content: "Error: nested_delegate_forbidden — this run is already a child"}, nil
				}
			}
			parentID := runCtx.RunID
			child, err := d.Spawn(ctx, runCtx.UserID, SpawnInput{
				Goal:          goal,
				ParentRunID:   &parentID,
				ToolAllowlist: childToolAllowlist(),
				MaxSteps:      40,
			})
			if err != nil {
				return ToolResult{Content: "Error: " + err.Error()}, nil
			}
			wait := true
			if args.Wait != nil {
				wait = *args.Wait
			}
			if !wait {
				return ToolResult{
					Content: fmt.Sprintf("Started child agent %s (not waiting).", child.ID),
					Meta:    map[string]any{"child_run_id": child.ID, "status": child.Status, "waited": false},
				}, nil
			}
			done, err := d.WaitUntilDone(ctx, runCtx.UserID, child.ID, defaultDelegateTimeout)
			if err != nil && !strings.Contains(err.Error(), "delegate_timeout") {
				return ToolResult{Content: "Error: " + err.Error(), Meta: map[string]any{"child_run_id": child.ID}}, nil
			}
			summary := childSummary(done)
			if err != nil {
				summary = "Child still running after 3 minutes. " + summary
			}
			return ToolResult{
				Content: summary,
				Meta: map[string]any{
					"child_run_id": done.ID,
					"status":       done.Status,
					"waited":       true,
				},
			}, nil
		},
	}
}

func childSummary(run storage.AgentRun) string {
	status := strings.TrimSpace(run.Status)
	text := ""
	if len(run.Result) > 0 && string(run.Result) != "null" {
		var result map[string]any
		if err := json.Unmarshal(run.Result, &result); err == nil {
			if s, ok := result["summary"].(string); ok {
				text = strings.TrimSpace(s)
			}
			if text == "" {
				if s, ok := result["question"].(string); ok {
					text = strings.TrimSpace(s)
				}
			}
		}
	}
	if text == "" && run.Error != nil {
		text = strings.TrimSpace(*run.Error)
	}
	if text == "" {
		text = "(no summary)"
	}
	if len(text) > 2000 {
		text = text[:2000] + "…"
	}
	return fmt.Sprintf("Child agent %s status=%s\n%s", run.ID, status, text)
}

package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// EmployeeProgressWriter updates an AI employee's progress summary.
type EmployeeProgressWriter interface {
	Patch(ctx context.Context, userID, id string, patch map[string]any) (storage.AIEmployee, error)
}

func employeeTools(writer EmployeeProgressWriter) []RegisteredTool {
	return []RegisteredTool{
		reportProgressTool(writer),
		completeGoalTool(writer),
	}
}

func reportProgressTool(writer EmployeeProgressWriter) RegisteredTool {
	return RegisteredTool{
		Toolset: "employee",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "report_progress",
				Description: "Update the AI employee's durable progress summary for future shifts. Call this before wrapping up a shift.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"summary": map[string]any{
							"type":        "string",
							"description": "Concise cumulative progress toward the ongoing goal (what was done, what's left, key findings).",
						},
					},
					"required": []string{"summary"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			args, err := ParseArgs[struct {
				Summary string `json:"summary"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			summary := strings.TrimSpace(args.Summary)
			if summary == "" {
				return ToolResult{}, fmt.Errorf("summary_required")
			}
			if len(summary) > 8000 {
				summary = summary[:8000]
			}
			if runCtx == nil || strings.TrimSpace(runCtx.EmployeeID) == "" {
				return ToolResult{Content: "Not an employee shift; progress noted for this run only: " + summary}, nil
			}
			if writer == nil {
				return ToolResult{}, fmt.Errorf("employees_unavailable")
			}
			if _, err := writer.Patch(ctx, runCtx.UserID, runCtx.EmployeeID, map[string]any{
				"progress_summary": summary,
			}); err != nil {
				return ToolResult{}, err
			}
			return ToolResult{
				Content: "Progress saved for future shifts.",
				Meta:    map[string]any{"progress_summary": summary},
			}, nil
		},
	}
}

func completeGoalTool(writer EmployeeProgressWriter) RegisteredTool {
	return RegisteredTool{
		Toolset: "employee",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "complete_goal",
				Description: "Mark the AI employee's ongoing goal as fully achieved and end this shift. Only call when the goal is truly done.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"summary": map[string]any{
							"type":        "string",
							"description": "Final outcome summary for the user.",
						},
					},
					"required": []string{"summary"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			args, err := ParseArgs[struct {
				Summary string `json:"summary"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			summary := strings.TrimSpace(args.Summary)
			if summary == "" {
				return ToolResult{}, fmt.Errorf("summary_required")
			}
			if runCtx != nil && strings.TrimSpace(runCtx.EmployeeID) != "" && writer != nil {
				_, _ = writer.Patch(ctx, runCtx.UserID, runCtx.EmployeeID, map[string]any{
					"progress_summary": summary,
				})
			}
			return ToolResult{
				Content: summary,
				Meta: map[string]any{
					"employee_goal_complete": true,
					"progress_summary":       summary,
				},
				Finish: true,
				FinishResult: map[string]any{
					"employee_goal_complete": true,
					"progress_summary":       summary,
					"summary":                summary,
				},
			}, nil
		},
	}
}

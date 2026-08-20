package agents

import (
	"context"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/skills"
)

// SkillProvider is the slice of skills.Provider the agent tools need.
type SkillProvider interface {
	GetByName(ctx context.Context, userID, name string) (skills.Skill, error)
	SaveUser(ctx context.Context, userID string, in skills.NewSkillInput) (skills.Skill, error)
	List(ctx context.Context, userID string) ([]skills.Skill, error)
	Match(ctx context.Context, userID, goal string, limit int) []skills.MatchScore
}

func loadSkillTool(prov SkillProvider) RegisteredTool {
	return RegisteredTool{
		Toolset: "skills",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "load_skill",
				Description: "Load the full instructions of a skill by name. Skills listed in the system prompt are available; load one before following it.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string", "description": "Skill name exactly as listed"},
					},
					"required": []string{"name"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			args, err := ParseArgs[struct {
				Name string `json:"name"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			skill, err := prov.GetByName(ctx, runCtx.UserID, args.Name)
			if err != nil {
				return ToolResult{Content: "Error: " + err.Error()}, nil
			}
			var b strings.Builder
			b.WriteString("Skill: " + skill.Name + "\n")
			if skill.Description != "" {
				b.WriteString("Description: " + skill.Description + "\n")
			}
			b.WriteString("\n" + skill.Content)
			return ToolResult{Content: b.String(), Meta: map[string]any{"skill": skill.Name, "source": skill.Source}}, nil
		},
	}
}

func saveSkillTool(prov SkillProvider) RegisteredTool {
	return RegisteredTool{
		Toolset: "skills",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "save_skill",
				Description: "Save a reusable procedure as a skill after a successful complex run, or when the user states a repeatable workflow. Content is markdown instructions for a future agent, not a log of this run.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":        map[string]any{"type": "string", "description": "kebab-case slug, e.g. flight-booking-prefs"},
						"description": map[string]any{"type": "string", "description": "One line: when this skill applies"},
						"content":     map[string]any{"type": "string", "description": "Markdown procedure body"},
					},
					"required": []string{"name", "description", "content"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			args, err := ParseArgs[struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Content     string `json:"content"`
			}](argsJSON)
			if err != nil {
				return ToolResult{}, err
			}
			in := skills.NewSkillInput{
				Name:        args.Name,
				Description: args.Description,
				Content:     args.Content,
				Source:      "agent",
			}
			if runCtx != nil && runCtx.RunID != "" {
				runID := runCtx.RunID
				in.AgentRunID = &runID
			}
			skill, err := prov.SaveUser(ctx, runCtx.UserID, in)
			if err != nil {
				return ToolResult{Content: "Error: " + err.Error()}, nil
			}
			return ToolResult{Content: "Skill saved: " + skill.Name, Meta: map[string]any{"skill": skill.Name, "version": skill.Version}}, nil
		},
	}
}

func listSkillsTool(prov SkillProvider) RegisteredTool {
	return RegisteredTool{
		Toolset: "skills",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "list_skills",
				Description: "List available skills (yours and Donna's) with their descriptions, to decide which to load.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			all, err := prov.List(ctx, runCtx.UserID)
			if err != nil {
				return ToolResult{Content: "Error: " + err.Error()}, nil
			}
			if len(all) == 0 {
				return ToolResult{Content: "No skills available."}, nil
			}
			var b strings.Builder
			for _, s := range all {
				line := "- " + s.Name + " [" + s.Source + "]"
				if s.Description != "" {
					line += ": " + s.Description
				}
				b.WriteString(line + "\n")
			}
			return ToolResult{Content: b.String(), Meta: map[string]any{"skills": all}}, nil
		},
	}
}

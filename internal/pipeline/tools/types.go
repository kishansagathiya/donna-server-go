package tools

import (
	"context"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
)

// Citation is a source discovered while running a tool.
type Citation struct {
	URL     string
	Title   string
	Content string
	// Source is the citation bucket for clients ("web", "granola", …). Empty means "web".
	Source string
}

// Result is what a tool returns to the model (and optionally to citation UI).
type Result struct {
	Content   string
	Citations []Citation
	// Phase hints the chat UI (fetching / browsing). Empty keeps generating.
	Phase string
	Host  string
}

// Handler executes one tool call.
type Handler func(ctx context.Context, argsJSON string) (Result, error)

// RegisteredTool pairs an OpenAI-style definition with its handler.
type RegisteredTool struct {
	Definition providers.ToolDefinition
	Handle     Handler
}

// Call is a normalized tool invocation from the model.
type Call struct {
	ID        string
	Name      string
	Arguments string
}

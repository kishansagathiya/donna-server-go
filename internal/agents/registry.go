package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
)

// ToolResult is returned from a tool to the model (+ optional UI metadata).
type ToolResult struct {
	Content string
	Meta    map[string]any
	// Finish ends the agent run successfully after this tool result is logged.
	Finish       bool
	FinishResult map[string]any
}

// ToolHandler executes one tool call for an agent run.
type ToolHandler func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error)

// RegisteredTool pairs an OpenAI-style schema with a handler and toolset name.
type RegisteredTool struct {
	Toolset    string
	Definition providers.ToolDefinition
	Handle     ToolHandler
}

// Registry is a self-registering agent tool registry (Hermes-style).
type Registry struct {
	mu    sync.RWMutex
	tools map[string]RegisteredTool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]RegisteredTool{}}
}

func (r *Registry) Register(tool RegisteredTool) {
	name := tool.Definition.Function.Name
	if name == "" || tool.Handle == nil {
		return
	}
	if tool.Toolset == "" {
		tool.Toolset = "default"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[name] = tool
}

func (r *Registry) Get(name string) (RegisteredTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Definitions(allowlist []string) []providers.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	allow := map[string]struct{}{}
	for _, a := range allowlist {
		allow[a] = struct{}{}
	}
	names := make([]string, 0, len(r.tools))
	for name, tool := range r.tools {
		if len(allow) > 0 {
			if _, ok := allow[name]; !ok {
				if _, ok := allow[tool.Toolset]; !ok {
					continue
				}
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]providers.ToolDefinition, 0, len(names))
	for _, name := range names {
		out = append(out, r.tools[name].Definition)
	}
	return out
}

func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// RunContext is passed into every tool invocation.
type RunContext struct {
	UserID     string
	RunID      string
	EmployeeID string
	Goal       string
	Plan       []TodoItem
	SetPlan    func([]TodoItem)
	Extra      map[string]any
}

type TodoItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"` // pending | in_progress | done
}

func ParseArgs[T any](argsJSON string) (T, error) {
	var out T
	if err := json.Unmarshal([]byte(argsJSON), &out); err != nil {
		return out, fmt.Errorf("invalid arguments: %w", err)
	}
	return out, nil
}

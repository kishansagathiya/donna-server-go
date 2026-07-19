package tools

import (
	"fmt"
	"sort"
	"sync"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
)

// Registry holds chat tools Donna can call mid-turn.
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
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[name] = tool
}

func (r *Registry) Definitions() []providers.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]providers.ToolDefinition, 0, len(names))
	for _, name := range names {
		out = append(out, r.tools[name].Definition)
	}
	return out
}

func (r *Registry) Get(name string) (RegisteredTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// DefaultRegistry builds the standard chat tool set.
// browse_page is only registered when a browser sidecar URL is configured.
func DefaultRegistry(browserURL string) *Registry {
	reg := NewRegistry()
	reg.Register(RegisteredTool{
		Definition: FetchURLDefinition(),
		Handle:     NewFetchURLHandler(),
	})
	if client := NewBrowserClient(browserURL); client != nil {
		reg.Register(RegisteredTool{
			Definition: BrowsePageDefinition(),
			Handle:     NewBrowsePageHandler(client),
		})
	}
	return reg
}

func (r *Registry) MustGet(name string) RegisteredTool {
	tool, ok := r.Get(name)
	if !ok {
		panic(fmt.Sprintf("tool not registered: %s", name))
	}
	return tool
}

package connectors

import (
	"fmt"
	"sync"
)

// Registry maps Donna-owned provider IDs to adapters. V1 only allows known providers.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]ConnectorAdapter
}

func NewRegistry() *Registry {
	return &Registry{adapters: map[string]ConnectorAdapter{}}
}

func (r *Registry) Register(adapter ConnectorAdapter) {
	if adapter == nil || adapter.Provider() == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[adapter.Provider()] = adapter
}

func (r *Registry) Get(provider string) (ConnectorAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[provider]
	return a, ok
}

func (r *Registry) MustGet(provider string) ConnectorAdapter {
	a, ok := r.Get(provider)
	if !ok {
		panic(fmt.Sprintf("connector provider not registered: %s", provider))
	}
	return a
}

func (r *Registry) Providers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.adapters))
	for p := range r.adapters {
		out = append(out, p)
	}
	return out
}

func (r *Registry) All() []ConnectorAdapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ConnectorAdapter, 0, len(r.adapters))
	for _, a := range r.adapters {
		out = append(out, a)
	}
	return out
}

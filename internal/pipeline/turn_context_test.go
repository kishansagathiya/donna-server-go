package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestLoadTurnContext_skipsGenericQuestion(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	e := &Engine{
		Config: &config.Config{MemoryMinScore: 0.35},
		KB: &storage.Knowledge{
			DB:       storage.NewSupabase(srv.URL, "test-key"),
			Enabled:  true,
			Embedder: &fakeEmbedder{enabled: true},
		},
	}

	aug, profile := e.loadTurnContext(
		context.Background(),
		"how are great founders hiring ai savvy founders?",
		"user-1",
		"sess-1",
	)
	if calls != 0 {
		t.Fatalf("expected zero DB calls for generic question, got %d", calls)
	}
	if profile != "" {
		t.Fatalf("expected empty profile, got %q", profile)
	}
	if len(aug.Retrieved) != 0 {
		t.Fatalf("expected no retrieved items, got %#v", aug.Retrieved)
	}
}

func TestLoadTurnContext_loadsForPersonalQuestion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "kb_user_profiles") {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"summary": "builder profile", "identity_facts": []string{}},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	e := &Engine{
		Config: &config.Config{MemoryMinScore: 0.35},
		KB: &storage.Knowledge{
			DB:       storage.NewSupabase(srv.URL, "test-key"),
			Enabled:  true,
			Embedder: &fakeEmbedder{enabled: false},
		},
	}

	_, profile := e.loadTurnContext(
		context.Background(),
		"what do you remember about my project?",
		"user-1",
		"sess-1",
	)
	if profile == "" {
		t.Fatal("expected profile summary for personal question")
	}
}

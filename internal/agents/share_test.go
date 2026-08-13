package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func authCtx(userID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), appauth.UserIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func TestCreateShare_success(t *testing.T) {
	var inserted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/agent_runs":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":      "run-1",
				"user_id": "user-1",
				"goal":    "Find cafes",
				"status":  "succeeded",
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/agent_run_shares":
			_ = json.NewEncoder(w).Encode([]any{})
		case r.Method == http.MethodPost && r.URL.Path == "/rest/v1/agent_run_shares":
			inserted = true
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":           "share-1",
				"agent_run_id": "run-1",
				"user_id":      "user-1",
				"token":        "tok_abc1234567890xyz",
				"created_at":   "2026-08-13T10:00:00Z",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	db := storage.NewSupabase(srv.URL, "test-key")
	h := &Handler{
		Store:      &storage.AgentsStore{DB: db, Enabled: true},
		WebAppBase: "https://donnadoesit.com",
	}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(http.MethodPost, "/agent-runs/run-1/share", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want %d, body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !inserted {
		t.Fatal("expected share insert")
	}

	var body shareResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.URL != "https://donnadoesit.com/share/agent/tok_abc1234567890xyz" {
		t.Fatalf("url %q", body.URL)
	}
}

func TestCreateShare_idempotentExisting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/agent_runs":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "run-1", "user_id": "user-1"}})
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/agent_run_shares":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":           "share-1",
				"agent_run_id": "run-1",
				"token":        "existing-token-abcdef",
				"created_at":   "2026-08-01T10:00:00Z",
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/rest/v1/agent_run_shares":
			t.Fatal("should not insert when active share exists")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	db := storage.NewSupabase(srv.URL, "test-key")
	h := &Handler{
		Store:      &storage.AgentsStore{DB: db, Enabled: true},
		WebAppBase: "https://example.com/",
	}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(http.MethodPost, "/agent-runs/run-1/share", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var body shareResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.URL != "https://example.com/share/agent/existing-token-abcdef" {
		t.Fatalf("url %q", body.URL)
	}
}

func TestGetPublicShare_successOmitsMemoryAndSteps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/agent_run_shares":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":           "share-1",
				"agent_run_id": "run-1",
				"user_id":      "user-1",
				"token":        "public-token-xyz",
				"created_at":   "2026-08-13T10:00:00Z",
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/agent_runs":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":              "run-1",
				"user_id":         "user-1",
				"goal":            "Find cafes",
				"status":          "succeeded",
				"memory_snapshot": map[string]any{"hits": []any{map[string]any{"content": "SECRET_MEMORY"}}},
				"result":          map[string]any{"summary": "Try third wave.", "plan": []string{"search"}},
				"created_at":      "2026-08-13T09:00:00Z",
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/agent_steps":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":      "s1",
				"seq":     1,
				"kind":    "thought",
				"payload": map[string]any{"text": "SECRET_THOUGHT"},
			}, {
				"id":      "s2",
				"seq":     2,
				"kind":    "tool_result",
				"payload": map[string]any{"name": "memory_search", "content": "SECRET_TOOL"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	db := storage.NewSupabase(srv.URL, "test-key")
	h := &Handler{Store: &storage.AgentsStore{DB: db, Enabled: true}}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(http.MethodGet, "/share/agent/public-token-xyz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var body storage.PublicSharedAgentRun
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Goal != "Find cafes" {
		t.Fatalf("goal %q", body.Goal)
	}
	if len(body.Turns) != 1 || body.Turns[0].Output.Text != "Try third wave." {
		t.Fatalf("turns %+v", body.Turns)
	}
	raw := rec.Body.String()
	for _, leak := range []string{"SECRET_MEMORY", "SECRET_THOUGHT", "SECRET_TOOL", "user_id", "memory_snapshot", "tool_result"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("leaked %q in public share: %s", leak, raw)
		}
	}
}

func TestGetPublicShare_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/v1/agent_run_shares" {
			_ = json.NewEncoder(w).Encode([]any{})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	db := storage.NewSupabase(srv.URL, "test-key")
	h := &Handler{Store: &storage.AgentsStore{DB: db, Enabled: true}}
	r := chi.NewRouter()
	RegisterRoutes(r, func(next http.Handler) http.Handler { return next }, h)

	req := httptest.NewRequest(http.MethodGet, "/share/agent/missing", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

func TestRevokeShare_success(t *testing.T) {
	var patched bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/agent_runs":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "run-1", "user_id": "user-1"}})
		case r.Method == http.MethodPatch && r.URL.Path == "/rest/v1/agent_run_shares":
			patched = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	db := storage.NewSupabase(srv.URL, "test-key")
	h := &Handler{Store: &storage.AgentsStore{DB: db, Enabled: true}}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(http.MethodDelete, "/agent-runs/run-1/share", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	if !patched {
		t.Fatal("expected revoke patch")
	}
}

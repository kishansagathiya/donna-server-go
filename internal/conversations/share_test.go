package conversations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestCreateShare_success(t *testing.T) {
	var inserted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/conversations":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "conv-1"}})
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/conversation_shares":
			_ = json.NewEncoder(w).Encode([]any{})
		case r.Method == http.MethodPost && r.URL.Path == "/rest/v1/conversation_shares":
			inserted = true
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":              "share-1",
				"conversation_id": "conv-1",
				"user_id":         "user-1",
				"token":           "tok_abc1234567890xyz",
				"created_at":      "2026-08-02T10:00:00Z",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	db := storage.NewSupabase(srv.URL, "test-key")
	h := &Handler{
		Store:      &storage.Conversations{DB: db, Enabled: true},
		WebAppBase: "https://donnadoesit.com",
	}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(http.MethodPost, "/conversations/conv-1/share", nil)
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
	if body.URL != "https://donnadoesit.com/share/tok_abc1234567890xyz" {
		t.Fatalf("url %q", body.URL)
	}
	if body.Token != "tok_abc1234567890xyz" {
		t.Fatalf("token %q", body.Token)
	}
}

func TestCreateShare_idempotentExisting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/conversations":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "conv-1"}})
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/conversation_shares":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":              "share-1",
				"conversation_id": "conv-1",
				"token":           "existing-token-abcdef",
				"created_at":      "2026-08-01T10:00:00Z",
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/rest/v1/conversation_shares":
			t.Fatal("should not insert when active share exists")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	db := storage.NewSupabase(srv.URL, "test-key")
	h := &Handler{
		Store:      &storage.Conversations{DB: db, Enabled: true},
		WebAppBase: "https://example.com/",
	}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(http.MethodPost, "/conversations/conv-1/share", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var body shareResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.URL != "https://example.com/share/existing-token-abcdef" {
		t.Fatalf("url %q", body.URL)
	}
}

func TestGetPublicShare_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/conversation_shares":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":              "share-1",
				"conversation_id": "conv-1",
				"user_id":         "user-1",
				"token":           "public-token-xyz",
				"created_at":      "2026-08-02T10:00:00Z",
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/conversations":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":           "conv-1",
				"channel":      "text",
				"title":        "Shared chat",
				"title_source": "user",
				"created_at":   "2026-08-01T09:00:00Z",
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/conversation_turns":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"turn_index":               0,
				"user_transcript":          "Hello",
				"user_grounded_transcript": "SECRET GROUNDING",
				"assistant_transcript":     "Hi there",
				"created_at":               "2026-08-01T09:00:01Z",
				"attachments":              nil,
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	db := storage.NewSupabase(srv.URL, "test-key")
	h := &Handler{Store: &storage.Conversations{DB: db, Enabled: true}}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(http.MethodGet, "/share/public-token-xyz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var body storage.PublicSharedConversation
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Title != "Shared chat" {
		t.Fatalf("title %q", body.Title)
	}
	if len(body.Turns) != 1 || body.Turns[0].UserTranscript != "Hello" {
		t.Fatalf("turns %+v", body.Turns)
	}
	// Grounded transcript must never appear in the public payload.
	if strings.Contains(rec.Body.String(), "SECRET GROUNDING") {
		t.Fatal("grounded transcript leaked in public share response")
	}
	if strings.Contains(rec.Body.String(), "user_id") {
		t.Fatal("user_id leaked in public share response")
	}
}

func TestGetPublicShare_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/v1/conversation_shares" {
			_ = json.NewEncoder(w).Encode([]any{})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	db := storage.NewSupabase(srv.URL, "test-key")
	h := &Handler{Store: &storage.Conversations{DB: db, Enabled: true}}
	r := chi.NewRouter()
	RegisterRoutes(r, func(next http.Handler) http.Handler { return next }, h)

	req := httptest.NewRequest(http.MethodGet, "/share/missing", nil)
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
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/conversations":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "conv-1"}})
		case r.Method == http.MethodPatch && r.URL.Path == "/rest/v1/conversation_shares":
			patched = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	db := storage.NewSupabase(srv.URL, "test-key")
	h := &Handler{Store: &storage.Conversations{DB: db, Enabled: true}}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(http.MethodDelete, "/conversations/conv-1/share", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	if !patched {
		t.Fatal("expected revoke patch")
	}
}

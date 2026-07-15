package conversations

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

func TestList_unauthorized(t *testing.T) {
	h := &Handler{Store: &storage.Conversations{Enabled: true}}
	r := chi.NewRouter()
	RegisterRoutes(r, func(next http.Handler) http.Handler { return next }, h)

	req := httptest.NewRequest(http.MethodGet, "/conversations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestList_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/v1/conversations":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id":                "conv-1",
				"channel":           "text",
				"title":             "Hello Donna",
				"client_session_id": "sess-1",
				"created_at":        "2026-06-01T10:00:00Z",
			}})
		case r.URL.Path == "/rest/v1/conversation_turns":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"conversation_id":      "conv-1",
				"turn_index":           0,
				"user_transcript":      "Hello Donna",
				"assistant_transcript": "Hi there",
				"created_at":           "2026-06-01T10:00:01Z",
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

	req := httptest.NewRequest(http.MethodGet, "/conversations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want %d, body %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body struct {
		Conversations []storage.ConversationSummary `json:"conversations"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Conversations) != 1 {
		t.Fatalf("got %d conversations, want 1", len(body.Conversations))
	}
	if body.Conversations[0].Title != "Hello Donna" {
		t.Fatalf("title %q, want %q", body.Conversations[0].Title, "Hello Donna")
	}
}

func TestTruncateTurns_success(t *testing.T) {
	var deletedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/v1/conversations":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "conv-1"}})
		case r.Method == http.MethodDelete && r.URL.Path == "/rest/v1/conversation_turns":
			deletedQuery = r.URL.RawQuery
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

	req := httptest.NewRequest(http.MethodDelete, "/conversations/session/sess-1/turns?from_index=1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want %d, body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if deletedQuery == "" {
		t.Fatal("expected delete request")
	}
}

func TestUpsertFeedback_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	defer srv.Close()

	db := storage.NewSupabase(srv.URL, "test-key")
	h := &Handler{Store: &storage.Conversations{DB: db, Enabled: true}}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(
		http.MethodPut,
		"/conversations/session/sess-missing/turns/0/feedback",
		strings.NewReader(`{"rating":"up"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want %d, body %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}


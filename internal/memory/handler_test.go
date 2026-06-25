package memory

import (
	"bytes"
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

func TestGetProfile_unauthorized(t *testing.T) {
	h := &Handler{KB: &storage.Knowledge{Enabled: true}}
	r := chi.NewRouter()
	RegisterRoutes(r, func(next http.Handler) http.Handler { return next }, h)

	req := httptest.NewRequest(http.MethodGet, "/memory/profile", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestGetProfile_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/v1/kb_user_profiles" {
			_ = json.NewEncoder(w).Encode([]storage.UserProfile{{
				UserID:        "user-1",
				Summary:       "Likes coffee",
				IdentityFacts: []string{"Name is Alex"},
				UpdatedAt:     "2026-01-01T00:00:00Z",
			}})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	kb := &storage.Knowledge{
		DB:      storage.NewSupabase(srv.URL, "test-key"),
		Enabled: true,
	}
	h := &Handler{KB: kb}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(http.MethodGet, "/memory/profile", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var profile storage.UserProfile
	if err := json.NewDecoder(rec.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}
	if profile.Summary != "Likes coffee" {
		t.Fatalf("summary %q", profile.Summary)
	}
	if len(profile.IdentityFacts) != 1 {
		t.Fatalf("identity_facts %v", profile.IdentityFacts)
	}
}

func TestUpdateFact_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/v1/kb_facts" && r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]storage.KbFact{})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	kb := &storage.Knowledge{
		DB:      storage.NewSupabase(srv.URL, "test-key"),
		Enabled: true,
	}
	h := &Handler{KB: kb}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	body := bytes.NewBufferString(`{"fact":"updated"}`)
	req := httptest.NewRequest(http.MethodPatch, "/memory/facts/missing-id", body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestListFacts_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/v1/kb_facts" && strings.Contains(r.URL.RawQuery, "user_id") {
			_ = json.NewEncoder(w).Encode([]storage.KbFact{{
				ID:     "fact-1",
				UserID: "user-1",
				Fact:   "Prefers tea",
				Active: true,
			}})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	kb := &storage.Knowledge{
		DB:      storage.NewSupabase(srv.URL, "test-key"),
		Enabled: true,
	}
	h := &Handler{KB: kb}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(http.MethodGet, "/memory/facts", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var facts []storage.KbFact
	if err := json.NewDecoder(rec.Body).Decode(&facts); err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Fact != "Prefers tea" {
		t.Fatalf("facts %+v", facts)
	}
}

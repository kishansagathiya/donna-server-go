package notes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/featureflags"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestFromVoiceSources_NoOp(t *testing.T) {
	s := &Sync{Store: &storage.Notes{Enabled: true}}
	sources := []storage.KbSource{{
		ID:         "src-1",
		SourceType: "voice_turn",
		Content:    "User: hello\nDonna: hi",
	}}
	if err := s.FromVoiceSources(t.Context(), "user-1", sources); err != nil {
		t.Fatalf("FromVoiceSources should be a no-op, got %v", err)
	}
}

func TestHandler_Feed_flagDisabled(t *testing.T) {
	h := &Handler{
		Store: &storage.Notes{Enabled: true},
		Flags: &featureflags.Resolver{Defaults: &config.Config{NotesV2Feed: false}},
	}
	req := httptest.NewRequest(http.MethodGet, "/notes/feed", nil)
	req = req.WithContext(contextWithUser("user-1"))
	rec := httptest.NewRecorder()
	h.Feed(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandler_Feed_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/notes") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":                 "11111111-1111-1111-1111-111111111111",
					"title":              "Coffee",
					"preview":            "espresso",
					"note_date":          "2026-07-20T10:00:00Z",
					"is_important":       false,
					"is_urgent":          false,
					"source_type":        "manual",
					"keywords":           []string{},
					"content_version":    1,
					"enrichment_status":  "idle",
					"enrichment_version": 0,
				},
			})
		case strings.Contains(r.URL.Path, "/note_tags"):
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case strings.Contains(r.URL.Path, "/tags"):
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"name": "work", "count": 2, "normalized_name": "work", "pinned": false},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	h := &Handler{
		Store: &storage.Notes{
			DB:      storage.NewSupabase(srv.URL, "test-key"),
			Enabled: true,
		},
		Flags: &featureflags.Resolver{Defaults: &config.Config{NotesV2Feed: true}},
	}
	req := httptest.NewRequest(http.MethodGet, "/notes/feed?curated=true", nil)
	req = req.WithContext(contextWithUser("user-1"))
	rec := httptest.NewRecorder()
	h.Feed(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var feed storage.NotesFeed
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != 1 || feed.Items[0].Title != "Coffee" {
		t.Fatalf("unexpected feed items: %#v", feed.Items)
	}
	if len(feed.Facets.Tags) != 1 || feed.Facets.Tags[0].Canonical != "work" {
		t.Fatalf("unexpected facets: %#v", feed.Facets)
	}
}

func TestHandler_Create_idempotencyConflict(t *testing.T) {
	existing := storage.Note{
		ID:      "11111111-1111-1111-1111-111111111111",
		UserID:  "user-1",
		Content: "original",
		Title:   "original",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/notes") {
			_ = json.NewEncoder(w).Encode([]storage.Note{existing})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	h := &Handler{
		Sync: &Sync{
			Store: &storage.Notes{
				DB:      storage.NewSupabase(srv.URL, "test-key"),
				Enabled: true,
			},
		},
	}
	body := `{"id":"11111111-1111-1111-1111-111111111111","content":"different"}`
	req := httptest.NewRequest(http.MethodPost, "/notes/", strings.NewReader(body))
	req = req.WithContext(contextWithUser("user-1"))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Update_versionConflict(t *testing.T) {
	existing := storage.Note{
		ID:             "11111111-1111-1111-1111-111111111111",
		UserID:         "user-1",
		Content:        "v1",
		SourceType:     "manual",
		ContentVersion: 3,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode([]storage.Note{existing})
		case http.MethodPatch:
			_ = json.NewEncoder(w).Encode([]storage.Note{})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	h := &Handler{
		Store: &storage.Notes{
			DB:      storage.NewSupabase(srv.URL, "test-key"),
			Enabled: true,
		},
	}
	r := chi.NewRouter()
	r.Patch("/notes/{id}", h.Update)
	body := `{"content":"v2","content_version":2}`
	req := httptest.NewRequest(http.MethodPatch, "/notes/"+existing.ID, strings.NewReader(body))
	req = req.WithContext(contextWithUser("user-1"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

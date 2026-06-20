package notes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestHandler_Search_missingToken(t *testing.T) {
	h := &Handler{Store: &storage.Notes{Enabled: true}}
	req := httptest.NewRequest(http.MethodGet, "/notes/search?q=test", nil)
	rec := httptest.NewRecorder()

	h.Search(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandler_Search_notesDisabled(t *testing.T) {
	h := &Handler{Store: &storage.Notes{Enabled: false}}
	req := httptest.NewRequest(http.MethodGet, "/notes/search?q=test", nil)
	req = req.WithContext(contextWithUser("user-1"))
	rec := httptest.NewRecorder()

	h.Search(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestHandler_Search_qRequired(t *testing.T) {
	h := &Handler{Store: &storage.Notes{Enabled: true}}
	req := httptest.NewRequest(http.MethodGet, "/notes/search", nil)
	req = req.WithContext(contextWithUser("user-1"))
	rec := httptest.NewRecorder()

	h.Search(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_Search_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "notes") {
			_ = json.NewEncoder(w).Encode([]storage.NoteSummary{
				{ID: "note-1", Title: "Coffee", Preview: "likes espresso"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	h := &Handler{
		Store: &storage.Notes{
			DB:      storage.NewSupabase(srv.URL, "test-key"),
			Enabled: true,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/notes/search?q=coffee", nil)
	req = req.WithContext(contextWithUser("user-1"))
	rec := httptest.NewRecorder()
	h.Search(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var notes []storage.NoteSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &notes); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Title != "Coffee" {
		t.Fatalf("unexpected notes: %#v", notes)
	}
}

func contextWithUser(userID string) context.Context {
	return context.WithValue(context.Background(), appauth.UserIDKey, userID)
}

package reminders

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
)

func TestCreateRequiresAuth(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/reminders", strings.NewReader(`{"title":"x"}`))
	w := httptest.NewRecorder()
	h.Create(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
}

func TestCreateDisabled(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/reminders", strings.NewReader(`{"title":"x"}`))
	ctx := context.WithValue(req.Context(), appauth.UserIDKey, "user-1")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.Create(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestRegisterRoutes(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutes(r, func(next http.Handler) http.Handler { return next }, &Handler{})
	req := httptest.NewRequest(http.MethodGet, "/reminders", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}

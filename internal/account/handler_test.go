package account

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
)

func TestHandler_missingToken(t *testing.T) {
	h := &Handler{Deleter: &Deleter{}}
	req := httptest.NewRequest(http.MethodDelete, "/account", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandler_methodNotAllowed(t *testing.T) {
	h := &Handler{Deleter: &Deleter{}}
	req := httptest.NewRequest(http.MethodGet, "/account", nil)
	req = req.WithContext(contextWithUser("user-1"))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func contextWithUser(userID string) context.Context {
	return context.WithValue(context.Background(), appauth.UserIDKey, userID)
}

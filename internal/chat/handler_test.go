package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline"
)

func TestHandler_unauthorized(t *testing.T) {
	h := &Handler{Engine: &pipeline.Engine{}}
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandler_emptyMessage(t *testing.T) {
	h := &Handler{Engine: &pipeline.Engine{}}
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message":"  "}`))
	req = req.WithContext(contextWithUser("user-1"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestHandler_methodNotAllowed(t *testing.T) {
	h := &Handler{Engine: &pipeline.Engine{}}
	req := httptest.NewRequest(http.MethodGet, "/chat", nil)
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

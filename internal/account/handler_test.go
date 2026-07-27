package account

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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
	req := httptest.NewRequest(http.MethodPost, "/account", nil)
	req = req.WithContext(contextWithUser("user-1"))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestHandler_getPreferencesUsesDefault(t *testing.T) {
	h := &Handler{
		Models:       []string{"provider/default", "provider/other"},
		DefaultModel: "provider/default",
	}
	req := httptest.NewRequest(http.MethodGet, "/account", nil)
	req = req.WithContext(contextWithUser("user-1"))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "{\"available_models\":[\"provider/default\",\"provider/other\"],\"available_personas\":null,\"llm_model\":\"provider/default\",\"persona\":\"companion\",\"persona_custom\":\"\",\"timezone\":\"\"}\n" {
		t.Fatalf("body = %s", got)
	}
}

func TestHandler_rejectsUnknownModel(t *testing.T) {
	h := &Handler{
		Models:       []string{"provider/default"},
		DefaultModel: "provider/default",
	}
	req := httptest.NewRequest(http.MethodPatch, "/account", strings.NewReader(`{"llm_model":"provider/expensive"}`))
	req = req.WithContext(contextWithUser("user-1"))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func contextWithUser(userID string) context.Context {
	return context.WithValue(context.Background(), appauth.UserIDKey, userID)
}

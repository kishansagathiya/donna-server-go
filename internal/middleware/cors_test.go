package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORSEchoesOrigin(t *testing.T) {
	h := CORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/agent-runs", nil)
	req.Header.Set("Origin", "https://donnadoesit.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://donnadoesit.com" {
		t.Fatalf("Allow-Origin=%q, want echoed origin", got)
	}
	if rec.Header().Get("Vary") != "Origin" {
		t.Fatalf("expected Vary: Origin")
	}
}

func TestCORSPreflight(t *testing.T) {
	h := CORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("preflight should not reach the next handler")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/agent-runs", nil)
	req.Header.Set("Origin", "https://donnadoesit.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://donnadoesit.com" {
		t.Fatalf("Allow-Origin=%q", got)
	}
	allow := rec.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(strings.ToLower(allow), "authorization") {
		t.Fatalf("Allow-Headers=%q, want Authorization", allow)
	}
}

func TestCORSWildcardWithoutOrigin(t *testing.T) {
	h := CORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Allow-Origin=%q, want *", got)
	}
}

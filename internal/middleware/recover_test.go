package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

func TestRecoverWithErrorLogReportsPanic(t *testing.T) {
	type report struct {
		message string
		fields  map[string]any
	}
	reports := make(chan report, 1)
	log.SetErrorHook(func(message string, fields map[string]any) {
		reports <- report{message, fields}
	})
	defer log.SetErrorHook(nil)

	h := RecoverWithErrorLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/chat", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	select {
	case rep := <-reports:
		if rep.message != "panic recovered" {
			t.Fatalf("unexpected message: %q", rep.message)
		}
		if rep.fields["error"] != "kaboom" {
			t.Fatalf("unexpected error field: %v", rep.fields["error"])
		}
		if rep.fields["path"] != "/chat" {
			t.Fatalf("unexpected path field: %v", rep.fields["path"])
		}
		stack, _ := rep.fields["stack"].(string)
		if !strings.Contains(stack, "recover_test.go") {
			t.Fatal("stack trace missing panic site")
		}
	default:
		t.Fatal("expected panic to be reported via error hook")
	}
}

func TestRecoverWithErrorLogPassesThrough(t *testing.T) {
	h := RecoverWithErrorLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

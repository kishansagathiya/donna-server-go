package errreport

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// collectReporter captures reports instead of calling GitHub.
func newCollectReporter() (*Reporter, chan Report) {
	ch := make(chan Report, 16)
	r := &Reporter{
		cfg:   Config{Enabled: true, Token: "x", Repo: "o/r"},
		queue: ch,
	}
	return r, ch
}

func post(t *testing.T, h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postFrom(t, h, "203.0.113.1:1234", body)
}

func postFrom(t *testing.T, h http.HandlerFunc, remoteAddr, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/errors", strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerAcceptsValidReport(t *testing.T) {
	r, ch := newCollectReporter()
	h := NewHandler(r)

	rec := post(t, h, `{"source":"ios","message":"boom","stack":"trace","route":"/chat","appVersion":"1.0.6","context":{"fatal":"true"}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", rec.Code, rec.Body.String())
	}

	select {
	case rep := <-ch:
		if rep.Source != "ios" || rep.Message != "boom" || rep.Stack != "trace" || rep.Route != "/chat" || rep.AppVersion != "1.0.6" {
			t.Fatalf("unexpected report: %+v", rep)
		}
		if rep.Context["fatal"] != "true" {
			t.Fatalf("context not passed through: %+v", rep.Context)
		}
	default:
		t.Fatal("expected report to be enqueued")
	}
}

func TestHandlerRejectsInvalidPayloads(t *testing.T) {
	r, _ := newCollectReporter()
	h := NewHandler(r)

	cases := map[string]string{
		"malformed json":  `{`,
		"missing source":  `{"message":"boom"}`,
		"server source":   `{"source":"server","message":"boom"}`,
		"unknown source":  `{"source":"android","message":"boom"}`,
		"missing message": `{"source":"web"}`,
		"blank message":   `{"source":"web","message":"  "}`,
	}
	i := 0
	for name, body := range cases {
		// Distinct IP per case so per-IP rate limiting doesn't mask the 400s.
		i++
		addr := fmt.Sprintf("203.0.113.%d:1234", i)
		if rec := postFrom(t, h, addr, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", name, rec.Code)
		}
	}
}

func TestHandlerRejectsWrongMethod(t *testing.T) {
	r, _ := newCollectReporter()
	h := NewHandler(r)
	req := httptest.NewRequest(http.MethodGet, "/errors", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandlerRateLimitsPerIP(t *testing.T) {
	r, ch := newCollectReporter()
	h := NewHandler(r)

	var last *httptest.ResponseRecorder
	for range reportsPerMinute + 2 {
		last = post(t, h, `{"source":"web","message":"flood"}`)
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after %d reports, got %d", reportsPerMinute, last.Code)
	}
	if len(ch) != reportsPerMinute {
		t.Fatalf("expected %d enqueued reports, got %d", reportsPerMinute, len(ch))
	}
}

func TestHandlerDisabledReporterStillAccepts(t *testing.T) {
	r := &Reporter{cfg: Config{Enabled: false}}
	h := NewHandler(r)
	rec := post(t, h, `{"source":"web","message":"dropped silently"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 (drop), got %d", rec.Code)
	}
}

func TestClientIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/errors", nil)
	req.RemoteAddr = "192.0.2.7:9999"
	if got := clientIP(req); got != "192.0.2.7" {
		t.Fatalf("expected host without port, got %q", got)
	}
}

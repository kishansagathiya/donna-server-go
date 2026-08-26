package reminders

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func authReq(method, path, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	return r.WithContext(context.WithValue(r.Context(), appauth.UserIDKey, "user-1"))
}

func testHandler(t *testing.T, seed []storage.Reminder) *Handler {
	t.Helper()
	store := testStore(t, seed)
	return &Handler{Service: &Service{Store: store}, Store: store}
}

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
	req := authReq(http.MethodPost, "/reminders", `{"title":"x"}`)
	w := httptest.NewRecorder()
	h.Create(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestCreateInvalidJSONAndDueAt(t *testing.T) {
	h := testHandler(t, nil)
	w := httptest.NewRecorder()
	h.Create(w, authReq(http.MethodPost, "/reminders", `{`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("json status %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.Create(w, authReq(http.MethodPost, "/reminders", `{"title":"x","due_at":"not-a-time","timezone":"UTC"}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("due_at status %d %s", w.Code, w.Body.String())
	}
}

func TestCreateListGetUpdateCancelDismiss(t *testing.T) {
	h := testHandler(t, nil)
	r := chi.NewRouter()
	RegisterRoutes(r, func(next http.Handler) http.Handler { return next }, h)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodPost, "/reminders", `{"title":"Call Mom","when":"in 10 minutes","notes":"landline","timezone":"UTC"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var created storage.Reminder
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("decode: %v %#v", err, created)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodGet, "/reminders?status=open&limit=10&offset=0", ""))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Call Mom") {
		t.Fatalf("list %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodGet, "/reminders/"+created.ID, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("get %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodPatch, "/reminders/"+created.ID, `{"title":"Call Dad","when":"in 20 minutes"}`))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Call Dad") {
		t.Fatalf("update %d %s", w.Code, w.Body.String())
	}

	due := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodPatch, "/reminders/"+created.ID, `{"due_at":"`+due+`","timezone":"UTC"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("update due %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodPost, "/reminders/"+created.ID+"/cancel", ""))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "cancelled") {
		t.Fatalf("cancel %d %s", w.Code, w.Body.String())
	}
}

func TestHandlerDismissFired(t *testing.T) {
	past := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	seed := sampleReminder(past)
	seed.Status = storage.ReminderStatusFired
	h := testHandler(t, []storage.Reminder{seed})
	r := chi.NewRouter()
	RegisterRoutes(r, func(next http.Handler) http.Handler { return next }, h)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodPost, "/reminders/rem-1/dismiss", ""))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "dismissed") {
		t.Fatalf("dismiss %d %s", w.Code, w.Body.String())
	}
}

func TestHandlerAuthAndDisabledOnReadPaths(t *testing.T) {
	h := &Handler{}
	for _, fn := range []func(http.ResponseWriter, *http.Request){h.List, h.Get, h.Update, h.Cancel, h.Dismiss} {
		w := httptest.NewRecorder()
		fn(w, httptest.NewRequest(http.MethodGet, "/reminders", nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauth %d", w.Code)
		}
		w = httptest.NewRecorder()
		fn(w, authReq(http.MethodGet, "/reminders", ""))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("disabled %d", w.Code)
		}
	}
}

func TestCreateTimezoneError(t *testing.T) {
	h := testHandler(t, nil)
	w := httptest.NewRecorder()
	h.Create(w, authReq(http.MethodPost, "/reminders", `{"title":"x"}`))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "timezone_required") {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}

func TestUpdateInvalidJSONAndDueAt(t *testing.T) {
	due := time.Now().UTC().Add(time.Hour)
	h := testHandler(t, []storage.Reminder{sampleReminder(due)})
	r := chi.NewRouter()
	RegisterRoutes(r, func(next http.Handler) http.Handler { return next }, h)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodPatch, "/reminders/rem-1", `{`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("json %d", w.Code)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodPatch, "/reminders/rem-1", `{"due_at":"nope"}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("due %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodGet, "/reminders/missing", ""))
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing %d %s", w.Code, w.Body.String())
	}
}

func TestStatusForErrAndQueryHelpers(t *testing.T) {
	if statusForErr(nil) != http.StatusOK {
		t.Fatal("nil")
	}
	cases := map[string]int{
		"reminder_not_found":       http.StatusNotFound,
		"reminders_disabled":       http.StatusServiceUnavailable,
		"timezone_required":        http.StatusBadRequest,
		"invalid_timezone":         http.StatusBadRequest,
		"unparseable_when:foo":     http.StatusBadRequest,
		"reminder_not_editable":    http.StatusConflict,
		"reminder_not_cancellable": http.StatusConflict,
		"reminder_not_dismissable": http.StatusConflict,
		"reminder_not_firable":     http.StatusConflict,
		"title_required":           http.StatusBadRequest,
	}
	for msg, want := range cases {
		if got := statusForErr(errString(msg)); got != want {
			t.Fatalf("%s: got %d want %d", msg, got, want)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/reminders?limit=abc&offset=-1", nil)
	if queryLimit(req, 50) != 50 {
		t.Fatal("bad limit")
	}
	if queryOffset(req) != 0 {
		t.Fatal("bad offset")
	}
	req = httptest.NewRequest(http.MethodGet, "/reminders?limit=200&offset=3", nil)
	if queryLimit(req, 50) != 100 {
		t.Fatal("clamp")
	}
	if queryOffset(req) != 3 {
		t.Fatal("offset")
	}
	req = httptest.NewRequest(http.MethodGet, "/reminders?limit=5", nil)
	if queryLimit(req, 50) != 5 {
		t.Fatal("limit")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

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

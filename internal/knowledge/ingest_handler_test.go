package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestIngestHandler_knowledgeDisabled(t *testing.T) {
	h := &IngestHandler{KB: &storage.Knowledge{Enabled: false}}
	req := httptest.NewRequest(http.MethodPost, "/knowledge/ingest", strings.NewReader(`{"text":"hi"}`))
	req = req.WithContext(contextWithUser("user-1"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestIngestHandler_missingToken(t *testing.T) {
	h := &IngestHandler{KB: &storage.Knowledge{Enabled: true}}
	req := httptest.NewRequest(http.MethodPost, "/knowledge/ingest", strings.NewReader(`{"text":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestIngestHandler_invalidBody(t *testing.T) {
	h := &IngestHandler{KB: &storage.Knowledge{Enabled: true}}
	req := httptest.NewRequest(http.MethodPost, "/knowledge/ingest", strings.NewReader(`{}`))
	req = req.WithContext(contextWithUser("user-1"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "invalid_body" {
		t.Fatalf("error = %q, want invalid_body", body["error"])
	}
}

func TestIngestHandler_missingFile(t *testing.T) {
	h := &IngestHandler{KB: &storage.Knowledge{Enabled: true}}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/knowledge/ingest", body)
	req = req.WithContext(contextWithUser("user-1"))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["error"] != "missing_file" {
		t.Fatalf("error = %q, want missing_file", resp["error"])
	}
}

func contextWithUser(userID string) context.Context {
	return context.WithValue(context.Background(), appauth.UserIDKey, userID)
}

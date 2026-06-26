package account

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestSanitizeFilename(t *testing.T) {
	if got := sanitizeFilename("my/report.pdf"); got != "my_report.pdf" {
		t.Fatalf("sanitizeFilename = %q", got)
	}
	if got := sanitizeFilename(""); got != "asset" {
		t.Fatalf("empty sanitizeFilename = %q", got)
	}
}

func TestAssetFilename_prefersOriginalFilename(t *testing.T) {
	source := storage.KbSource{
		Metadata: map[string]any{
			"original_filename": "notes.pdf",
			"title":             "Ignored",
		},
	}
	if got := assetFilename(source); got != "notes.pdf" {
		t.Fatalf("assetFilename = %q", got)
	}
}

func TestPreCheck_rateLimit(t *testing.T) {
	exporter := &Exporter{
		DB: storage.NewSupabase("http://example.test", "service-key"),
	}
	if err := exporter.PreCheck("user-1"); err != nil {
		t.Fatalf("first PreCheck: %v", err)
	}
	if err := exporter.PreCheck("user-1"); err == nil {
		t.Fatal("expected rate limit error")
	}
}

func TestExportUser_buildsZip(t *testing.T) {
	server := newExportTestServer(t)
	defer server.Close()

	exporter := &Exporter{DB: storage.NewSupabase(server.URL, "service-key")}
	exporter.DB.Client = server.Client()

	var buf bytes.Buffer
	if err := exporter.ExportUser(context.Background(), "user-1", &buf); err != nil {
		t.Fatalf("ExportUser: %v", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip reader: %v", err)
	}

	names := make(map[string]bool)
	for _, file := range reader.File {
		names[file.Name] = true
	}

	for _, required := range []string{
		"manifest.json",
		"profile.json",
		"preferences.json",
		"notes.json",
		"compile_log.json",
		"README.txt",
		"knowledge/sources.json",
		"knowledge/facts.json",
		"conversations/conv-1.json",
		"conversations/conv-1/audio/0/user.wav",
	} {
		if !names[required] {
			t.Fatalf("missing zip entry %q; got %v", required, names)
		}
	}

	manifestFile, err := reader.Open("manifest.json")
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer manifestFile.Close()

	var manifest exportManifest
	if err := json.NewDecoder(manifestFile).Decode(&manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.ExportVersion != exportVersion {
		t.Fatalf("export version = %d", manifest.ExportVersion)
	}
	if manifest.Counts.Conversations != 1 || manifest.Counts.Turns != 1 {
		t.Fatalf("counts = %+v", manifest.Counts)
	}
}

func newExportTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/rest/v1/kb_user_profiles"):
			writeJSONResponse(w, []map[string]any{{
				"user_id": "user-1", "summary": "hello", "identity_facts": []string{}, "updated_at": time.Now().UTC().Format(time.RFC3339),
			}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/rest/v1/user_preferences"):
			writeJSONResponse(w, []map[string]any{{"user_id": "user-1", "llm_model": "provider/default"}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/rest/v1/conversations"):
			writeJSONResponse(w, []map[string]any{{
				"id": "conv-1", "user_id": "user-1", "channel": "voice", "voice_session_id": "ws-1", "created_at": time.Now().UTC().Format(time.RFC3339),
			}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/rest/v1/conversation_turns"):
			writeJSONResponse(w, []map[string]any{{
				"turn_index": 0, "user_transcript": "hi", "assistant_transcript": "hello",
				"user_audio_path": "user-1/conv-1/0/user.wav", "created_at": time.Now().UTC().Format(time.RFC3339),
			}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/rest/v1/kb_sources"):
			writeJSONResponse(w, []storage.KbSource{{
				ID: "source-1", UserID: "user-1", SourceType: "document", Content: "doc",
				Metadata: map[string]any{"storage_path": "user-1/file.txt", "original_filename": "file.txt"},
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/rest/v1/kb_facts"):
			writeJSONResponse(w, []exportFact{{ID: "fact-1", UserID: "user-1", Fact: "likes tea", Active: true}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/rest/v1/notes"):
			writeJSONResponse(w, []exportNote{{ID: "note-1", UserID: "user-1", SourceType: "manual", Title: "Note", Content: "Body"}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/rest/v1/kb_compile_log"):
			writeJSONResponse(w, []map[string]any{})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/storage/v1/object/conversation-audio/"):
			_, _ = w.Write([]byte("RIFF"))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/storage/v1/object/knowledge-assets/"):
			_, _ = w.Write([]byte("file contents"))
		default:
			t.Logf("unhandled request: %s %s", r.Method, r.URL.String())
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func writeJSONResponse(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func TestHandler_exportUnavailable(t *testing.T) {
	h := &Handler{Exporter: nil}
	req := httptest.NewRequest(http.MethodGet, "/account/export", nil)
	req = req.WithContext(contextWithUser("user-1"))
	rec := httptest.NewRecorder()

	h.Export(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestHandler_exportRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONResponse(w, []map[string]any{})
	}))
	defer server.Close()

	exporter := &Exporter{DB: storage.NewSupabase(server.URL, "service-key")}
	exporter.DB.Client = server.Client()
	_ = exporter.PreCheck("user-1")

	h := &Handler{Exporter: exporter}
	req := httptest.NewRequest(http.MethodGet, "/account/export", nil)
	req = req.WithContext(contextWithUser("user-1"))
	rec := httptest.NewRecorder()

	h.Export(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
}

func TestDownloadStorage_pathEncoding(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	db := storage.NewSupabase(server.URL, "service-key")
	db.Client = server.Client()

	data, err := db.DownloadStorage(context.Background(), "conversation-audio", "user/conv/0/user.wav")
	if err != nil {
		t.Fatalf("DownloadStorage: %v", err)
	}
	if string(data) != "ok" {
		t.Fatalf("data = %q", data)
	}
	if !strings.Contains(gotPath, "user/conv/0/user.wav") {
		t.Fatalf("path = %q", gotPath)
	}
	_, _ = url.Parse(gotPath)
}

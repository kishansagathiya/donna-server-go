package chatgpt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestPublicImportShape(t *testing.T) {
	errMsg := "boom"
	bytes := int64(12)
	imp := publicImport(storage.ChatGPTImport{
		ID:                     "imp-1",
		Status:                 storage.ChatGPTImportCompleted,
		ConversationsTotal:     2,
		ConversationsProcessed: 2,
		MemoriesImported:       1,
		Bytes:                  &bytes,
		Error:                  &errMsg,
		CreatedAt:              "2026-01-01T00:00:00Z",
		UpdatedAt:              "2026-01-01T00:00:00Z",
	})
	raw, err := json.Marshal(imp)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "status", "conversations_total", "conversations_processed", "bytes", "error"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("missing key %s in %s", key, string(raw))
		}
	}
}

func TestCreateUnavailableWithoutStore(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/imports/chatgpt", nil)
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestLatestEmptyWithoutStore(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/imports/chatgpt", nil)
	rec := httptest.NewRecorder()
	h.Latest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["import"] != nil {
		t.Fatalf("expected null import, got %#v", body["import"])
	}
}

func TestJobPayloadRoundTrip(t *testing.T) {
	p := jobPayload{ImportID: "abc", Cursor: 7}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var out jobPayload
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.ImportID != "abc" || out.Cursor != 7 {
		t.Fatalf("unexpected payload: %+v", out)
	}
}

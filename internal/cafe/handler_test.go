package cafe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPresenceHeartbeatAndExpiry(t *testing.T) {
	h := NewHandler()
	h.ttl = 50 * time.Millisecond

	post := func(id string) int {
		body, _ := json.Marshal(map[string]string{"clientId": id})
		req := httptest.NewRequest(http.MethodPost, "/cafe/presence", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.PostPresence(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST status %d body %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Count int `json:"count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Count
	}

	if got := post("client-aaa-1"); got != 1 {
		t.Fatalf("count=%d want 1", got)
	}
	if got := post("client-bbb-2"); got != 2 {
		t.Fatalf("count=%d want 2", got)
	}

	time.Sleep(80 * time.Millisecond)
	if got := post("client-ccc-3"); got != 1 {
		t.Fatalf("after expiry count=%d want 1", got)
	}
}

func TestPresenceRejectsBadClientID(t *testing.T) {
	h := NewHandler()
	body, _ := json.Marshal(map[string]string{"clientId": "bad"})
	req := httptest.NewRequest(http.MethodPost, "/cafe/presence", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PostPresence(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d want 400", rec.Code)
	}
}

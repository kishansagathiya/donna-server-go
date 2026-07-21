package memory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestListGrouped_empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/v1/kb_facts" {
			_ = json.NewEncoder(w).Encode([]storage.MemoryFact{})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	kb := &storage.Knowledge{DB: storage.NewSupabase(srv.URL, "k"), Enabled: true}
	h := &Handler{KB: kb}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(http.MethodGet, "/memory/items/grouped", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Groups []storage.MemoryGroup `json:"groups"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Groups) < len(storage.MemoryUIGroupOrder) {
		t.Fatalf("expected %d groups, got %d", len(storage.MemoryUIGroupOrder), len(body.Groups))
	}
}

func TestAcceptSuggestion_createsFact(t *testing.T) {
	var insertedFact map[string]any
	var patchedSuggestion bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/v1/memory_suggestions" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]storage.MemorySuggestion{{
				ID:             "sug-1",
				UserID:         "user-1",
				SuggestionKind: storage.SuggestionKindMemory,
				Status:         storage.SuggestionPending,
				Payload: map[string]any{
					"fact":        "Prefers oat milk",
					"kind":        "preference",
					"predicate":   "prefers",
					"sensitivity": "normal",
					"excerpt":     "I prefer oat milk",
					"source_kind": "note",
					"source_id":   "note-1",
				},
				Confidence: floatPtr(0.92),
			}})
		case r.URL.Path == "/rest/v1/kb_facts" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&insertedFact)
			_ = json.NewEncoder(w).Encode([]storage.MemoryFact{{
				ID:           "fact-1",
				UserID:       "user-1",
				Fact:         "Prefers oat milk",
				Active:       true,
				ReviewStatus: storage.ReviewActive,
				Sensitivity:  "normal",
			}})
		case r.URL.Path == "/rest/v1/kb_memory_evidence" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode([]storage.MemoryEvidence{{ID: "ev-1", FactID: "fact-1", Excerpt: "I prefer oat milk"}})
		case r.URL.Path == "/rest/v1/memory_suggestions" && r.Method == http.MethodPatch:
			patchedSuggestion = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
		case r.URL.Path == "/rest/v1/memory_feedback" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode([]storage.MemoryFeedback{{ID: "fb-1", Action: storage.FeedbackAccept}})
		case r.URL.Path == "/rest/v1/kb_facts" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]storage.MemoryFact{{
				ID: "fact-1", UserID: "user-1", Fact: "Prefers oat milk", Active: true, ReviewStatus: storage.ReviewActive,
			}})
		case r.URL.Path == "/rest/v1/kb_user_profiles":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	kb := &storage.Knowledge{DB: storage.NewSupabase(srv.URL, "k"), Enabled: true}
	h := &Handler{KB: kb}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(http.MethodPost, "/memory/suggestions/sug-1/accept", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if insertedFact["fact"] != "Prefers oat milk" {
		t.Fatalf("inserted fact %#v", insertedFact)
	}
	if !patchedSuggestion {
		t.Fatal("expected suggestion status patch")
	}
}

func TestPostFeedback_notRelevant(t *testing.T) {
	var feedbackAction string
	var reviewStatus string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/v1/memory_feedback" && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			feedbackAction, _ = body["action"].(string)
			_ = json.NewEncoder(w).Encode([]storage.MemoryFeedback{{ID: "fb-1", Action: feedbackAction}})
		case r.URL.Path == "/rest/v1/kb_facts" && r.Method == http.MethodPatch:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			reviewStatus, _ = body["review_status"].(string)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
		case r.URL.Path == "/rest/v1/kb_facts" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]storage.MemoryFact{{
				ID: "fact-1", UserID: "user-1", Fact: "x", Active: false, ReviewStatus: storage.ReviewRejected,
			}})
		case r.URL.Path == "/rest/v1/kb_user_profiles":
			_, _ = w.Write([]byte("[]"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	kb := &storage.Knowledge{DB: storage.NewSupabase(srv.URL, "k"), Enabled: true}
	h := &Handler{KB: kb}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(http.MethodPost, "/memory/feedback", strings.NewReader(`{"fact_id":"fact-1","action":"not_relevant"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if feedbackAction != storage.FeedbackNotRelevant {
		t.Fatalf("action %q", feedbackAction)
	}
	if reviewStatus != storage.ReviewRejected {
		t.Fatalf("review_status %q", reviewStatus)
	}
}

func TestListItems_conflicting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/v1/memory_suggestions" {
			_ = json.NewEncoder(w).Encode([]storage.MemorySuggestion{
				{
					ID: "s1", UserID: "user-1", SuggestionKind: "memory", Status: "pending",
					Payload: map[string]any{"fact": "Born in 1990", "conflicting": true, "kind": "identity"},
				},
				{
					ID: "s2", UserID: "user-1", SuggestionKind: "memory", Status: "pending",
					Payload: map[string]any{"fact": "Likes tea", "conflicting": false, "kind": "preference"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	kb := &storage.Knowledge{DB: storage.NewSupabase(srv.URL, "k"), Enabled: true}
	h := &Handler{KB: kb}
	r := chi.NewRouter()
	RegisterRoutes(r, authCtx("user-1"), h)

	req := httptest.NewRequest(http.MethodGet, "/memory/items?status=conflicting", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var items []storage.MemoryItem
	if err := json.NewDecoder(rec.Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].Conflicting {
		t.Fatalf("items %#v", items)
	}
}

func floatPtr(v float64) *float64 { return &v }

package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestFormatAugmentedUserMessage_withRetrievedAndSession(t *testing.T) {
	got := FormatAugmentedUserMessage(TranscriptAugmentation{
		Transcript:   "hello",
		Retrieved:    []string{"fact one", "fact two"},
		SessionNotes: "remember milk",
	})

	if !strings.Contains(got, "[Retrieved: fact one | fact two]") {
		t.Fatalf("missing retrieved block: %q", got)
	}
	if !strings.Contains(got, "[Session: remember milk]") {
		t.Fatalf("missing session block: %q", got)
	}
	if !strings.Contains(got, `User said: "hello"`) {
		t.Fatalf("missing user quote: %q", got)
	}
}

func TestFormatAugmentedUserMessage_transcriptOnly(t *testing.T) {
	got := FormatAugmentedUserMessage(TranscriptAugmentation{Transcript: "hello"})
	want := `User said: "hello"`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestApplyTokenBudget_dropsTailItems(t *testing.T) {
	hits := []storage.MemoryHit{
		{Text: "aaaa", Score: 3},
		{Text: "bbbb", Score: 2},
		{Text: "cccc", Score: 1},
	}
	got := applyTokenBudget(hits, 12)
	if len(got) != 2 || got[0] != "aaaa" || got[1] != "bbbb" {
		t.Fatalf("unexpected budget trim: %#v", got)
	}
}

func TestApplyTokenBudget_keepsFirstWhenOverBudget(t *testing.T) {
	hits := []storage.MemoryHit{{Text: strings.Repeat("x", 100), Score: 1}}
	got := applyTokenBudget(hits, 10)
	if len(got) != 1 || got[0] != hits[0].Text {
		t.Fatalf("expected first hit kept: %#v", got)
	}
}

func TestApplyTokenBudget_skipsEmptyText(t *testing.T) {
	hits := []storage.MemoryHit{
		{Text: "", Score: 2},
		{Text: "valid", Score: 1},
	}
	got := applyTokenBudget(hits, 100)
	if len(got) != 1 || got[0] != "valid" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

type fakeEmbedder struct {
	enabled bool
	err     error
}

func (f *fakeEmbedder) Embed(_ context.Context, _ []string) ([][]float32, error) {
	return nil, f.err
}

func (f *fakeEmbedder) EmbedOne(_ context.Context, _ string) ([]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []float32{0.1, 0.2, 0.3}, nil
}

func (f *fakeEmbedder) Enabled() bool { return f.enabled }

func newTestKnowledge(t *testing.T, handler http.HandlerFunc) *storage.Knowledge {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &storage.Knowledge{
		DB:       storage.NewSupabase(srv.URL, "test-key"),
		Enabled:  true,
		Embedder: &fakeEmbedder{enabled: true},
	}
}

func TestDefaultAugment_hybridPath(t *testing.T) {
	kb := newTestKnowledge(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "rpc/match_memory") {
			_ = json.NewEncoder(w).Encode([]storage.MemoryHit{
				{Text: "hybrid memory hit", Score: 0.9},
			})
			return
		}
		http.NotFound(w, r)
	})

	got := DefaultAugment(context.Background(), kb, nil, "what do you know?", "user-1", "sess-1")
	if len(got.Retrieved) != 1 || got.Retrieved[0] != "hybrid memory hit" {
		t.Fatalf("unexpected retrieved: %#v", got.Retrieved)
	}
	if !strings.Contains(got.Text, "hybrid memory hit") {
		t.Fatalf("text missing hybrid hit: %q", got.Text)
	}
}

func TestDefaultAugment_fallbackOnRPCError(t *testing.T) {
	kb := newTestKnowledge(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "rpc/match_memory") {
			http.Error(w, "rpc failed", http.StatusInternalServerError)
			return
		}
		if strings.Contains(r.URL.Path, "kb_facts") {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"fact": "User likes coffee", "entity_name": nil, "topic": nil},
			})
			return
		}
		http.NotFound(w, r)
	})

	got := DefaultAugment(context.Background(), kb, nil, "coffee preferences", "user-1", "sess-1")
	if len(got.Retrieved) == 0 {
		t.Fatal("expected legacy facts in fallback path")
	}
	if !strings.Contains(got.Retrieved[0], "coffee") {
		t.Fatalf("unexpected retrieved: %#v", got.Retrieved)
	}
}

func TestDefaultAugment_deduplicatesLegacyHits(t *testing.T) {
	kb := &storage.Knowledge{
		Enabled: true,
		Embedder: &fakeEmbedder{enabled: false},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "notes") {
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"title": "", "preview": "", "content": "same fact"},
			})
			return
		}
		if strings.Contains(r.URL.Path, "kb_facts") {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"fact": "same fact", "entity_name": nil, "topic": nil},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	kb.DB = storage.NewSupabase(srv.URL, "test-key")

	notes := &storage.Notes{DB: kb.DB, Enabled: true}

	got := DefaultAugment(context.Background(), kb, notes, "same fact details", "user-1", "sess-1")
	if len(got.Retrieved) != 1 {
		t.Fatalf("expected deduplicated single hit, got %#v", got.Retrieved)
	}
}

func TestDefaultAugment_capsLegacyAt10(t *testing.T) {
	kb := &storage.Knowledge{
		Enabled:  true,
		Embedder: &fakeEmbedder{enabled: false},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "notes") {
			var rows []map[string]string
			for i := 0; i < 6; i++ {
				rows = append(rows, map[string]string{
					"title":   "N",
					"preview": "note-" + string(rune('a'+i)),
					"content": "note-" + string(rune('a'+i)),
				})
			}
			_ = json.NewEncoder(w).Encode(rows)
			return
		}
		if strings.Contains(r.URL.Path, "kb_facts") {
			var rows []map[string]any
			for i := 0; i < 10; i++ {
				rows = append(rows, map[string]any{
					"fact": "fact-" + string(rune('a'+i)), "entity_name": nil, "topic": nil,
				})
			}
			_ = json.NewEncoder(w).Encode(rows)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	kb.DB = storage.NewSupabase(srv.URL, "test-key")
	notes := &storage.Notes{DB: kb.DB, Enabled: true}

	got := DefaultAugment(context.Background(), kb, notes, "note fact mix", "user-1", "sess-1")
	if len(got.Retrieved) != 10 {
		t.Fatalf("expected cap of 10, got %d: %#v", len(got.Retrieved), got.Retrieved)
	}
}

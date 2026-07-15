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
	got := applyTokenBudget(hits, 12, 0)
	if len(got) != 2 || got[0].Text != "aaaa" || got[1].Text != "bbbb" {
		t.Fatalf("unexpected budget trim: %#v", got)
	}
}

func TestApplyTokenBudget_keepsFirstWhenOverBudget(t *testing.T) {
	hits := []storage.MemoryHit{{Text: strings.Repeat("x", 100), Score: 1}}
	got := applyTokenBudget(hits, 10, 0)
	if len(got) != 1 || got[0].Text != hits[0].Text {
		t.Fatalf("expected first hit kept: %#v", got)
	}
}

func TestApplyTokenBudget_skipsEmptyText(t *testing.T) {
	hits := []storage.MemoryHit{
		{Text: "", Score: 2},
		{Text: "valid", Score: 1},
	}
	got := applyTokenBudget(hits, 100, 0)
	if len(got) != 1 || got[0].Text != "valid" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestToCitations_truncatesAndKeepsIDs(t *testing.T) {
	long := strings.Repeat("a", citationTextMax+40)
	hits := []storage.MemoryHit{
		{Source: "note", ID: "n1", Text: long, Score: 0.9},
		{Source: "fact", ID: "f1", Text: "short", Score: 0.8},
	}
	cites := toCitations(hits)
	if len(cites) != 2 {
		t.Fatalf("expected 2 citations, got %#v", cites)
	}
	if cites[0].ID != "n1" || cites[0].Source != "note" {
		t.Fatalf("unexpected first citation: %#v", cites[0])
	}
	if !strings.HasSuffix(cites[0].Text, "…") || len(cites[0].Text) >= len(long) {
		t.Fatalf("citation text not truncated: %q (%d)", cites[0].Text, len(cites[0].Text))
	}
	if cites[1].Text != "short" {
		t.Fatalf("short citation changed: %#v", cites[1])
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

	got := DefaultAugment(context.Background(), kb, nil, "what do you know?", "user-1", "sess-1", 0.35)
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

	got := DefaultAugment(context.Background(), kb, nil, "coffee preferences", "user-1", "sess-1", 0.35)
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

	got := DefaultAugment(context.Background(), kb, notes, "same fact details", "user-1", "sess-1", 0.35)
	if len(got.Retrieved) != 1 {
		t.Fatalf("expected deduplicated single hit, got %#v", got.Retrieved)
	}
}

func TestDefaultAugment_skipsRetrievalForChitchat(t *testing.T) {
	calls := 0
	kb := &storage.Knowledge{
		Enabled:  true,
		Embedder: &fakeEmbedder{enabled: false},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	kb.DB = storage.NewSupabase(srv.URL, "test-key")
	notes := &storage.Notes{DB: kb.DB, Enabled: true}

	got := DefaultAugment(context.Background(), kb, notes, "thanks!", "user-1", "sess-1", 0.35)
	if calls != 0 {
		t.Fatalf("expected zero DB calls for chitchat, got %d", calls)
	}
	if len(got.Retrieved) != 0 {
		t.Fatalf("expected no retrieved items for chitchat, got %#v", got.Retrieved)
	}
	if !strings.Contains(got.Text, `User said: "thanks!"`) {
		t.Fatalf("expected user quote preserved, got %q", got.Text)
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

	got := DefaultAugment(context.Background(), kb, notes, "note fact mix", "user-1", "sess-1", 0.35)
	if len(got.Retrieved) != 10 {
		t.Fatalf("expected cap of 10, got %d: %#v", len(got.Retrieved), got.Retrieved)
	}
}

func TestApplyTokenBudget_skipsLowScore(t *testing.T) {
	hits := []storage.MemoryHit{
		{Text: "relevant", Score: 0.9},
		{Text: "weak", Score: 0.1},
	}
	got := applyTokenBudget(hits, 1000, 0.35)
	if len(got) != 1 || got[0].Text != "relevant" {
		t.Fatalf("expected only high-score hit, got %#v", got)
	}
}

func TestDefaultAugment_skipsHybridForChitchat(t *testing.T) {
	calls := 0
	kb := newTestKnowledge(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	})

	got := DefaultAugment(context.Background(), kb, nil, "thanks!", "user-1", "sess-1", 0.35)
	if calls != 0 {
		t.Fatalf("expected zero DB calls for chitchat on hybrid path, got %d", calls)
	}
	if len(got.Retrieved) != 0 {
		t.Fatalf("expected no retrieved items, got %#v", got.Retrieved)
	}
}

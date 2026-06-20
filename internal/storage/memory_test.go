package storage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubEmbedder struct {
	enabled   bool
	embedding []float32
	err       error
}

func (s *stubEmbedder) Embed(_ context.Context, _ []string) ([][]float32, error) {
	return nil, s.err
}

func (s *stubEmbedder) EmbedOne(_ context.Context, _ string) ([]float32, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.embedding, nil
}

func (s *stubEmbedder) Enabled() bool { return s.enabled }

func TestRetrieveMemory_disabled(t *testing.T) {
	kb := &Knowledge{Enabled: false}
	got, err := kb.RetrieveMemory(context.Background(), "user-1", "hello", 10)
	if err != nil || got != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
	}
}

func TestRetrieveMemory_noEmbedder(t *testing.T) {
	kb := &Knowledge{Enabled: true}
	_, err := kb.RetrieveMemory(context.Background(), "user-1", "hello", 10)
	if err == nil {
		t.Fatal("expected embedder unavailable error")
	}
}

func TestRetrieveMemory_emptyTranscript(t *testing.T) {
	kb := &Knowledge{
		Enabled:  true,
		Embedder: &stubEmbedder{enabled: true, embedding: []float32{0.1}},
	}
	got, err := kb.RetrieveMemory(context.Background(), "user-1", "  ", 10)
	if err != nil || got != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
	}
}

func TestRetrieveMemory_success(t *testing.T) {
	var rpcBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "rpc/match_memory") {
			_ = json.NewDecoder(r.Body).Decode(&rpcBody)
			_ = json.NewEncoder(w).Encode([]MemoryHit{
				{Source: "fact", ID: "id-1", Text: "memory hit", Score: 0.8},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	kb := &Knowledge{
		DB:       NewSupabase(srv.URL, "test-key"),
		Enabled:  true,
		Embedder: &stubEmbedder{enabled: true, embedding: []float32{0.1, 0.2}},
	}

	got, err := kb.RetrieveMemory(context.Background(), "user-1", "hello donna", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "memory hit" {
		t.Fatalf("unexpected hits: %#v", got)
	}
	if rpcBody["p_user_id"] != "user-1" || rpcBody["p_query_text"] != "hello donna" {
		t.Fatalf("unexpected RPC body: %#v", rpcBody)
	}
}

func TestRetrieveMemory_embeddingFailure(t *testing.T) {
	kb := &Knowledge{
		Enabled:  true,
		Embedder: &stubEmbedder{enabled: true, err: errors.New("embed failed")},
	}
	_, err := kb.RetrieveMemory(context.Background(), "user-1", "hello", 10)
	if err == nil {
		t.Fatal("expected embedding error")
	}
}

func TestAppendIdentityFacts_prependsProfile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "kb_user_profiles") {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"identity_facts": []string{"User's name is Kishan"}},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	kb := &Knowledge{
		DB:      NewSupabase(srv.URL, "test-key"),
		Enabled: true,
	}

	got := kb.appendIdentityFacts(context.Background(), "user-1", "likes coffee")
	want := "User's name is Kishan likes coffee"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

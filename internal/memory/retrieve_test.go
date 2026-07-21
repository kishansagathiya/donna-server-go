package memory

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type countingEmbedder struct {
	calls atomic.Int64
}

func (c *countingEmbedder) Embed(_ context.Context, _ []string) ([][]float32, error) {
	c.calls.Add(1)
	return [][]float32{{0.1}}, nil
}
func (c *countingEmbedder) EmbedOne(_ context.Context, _ string) ([]float32, error) {
	c.calls.Add(1)
	return []float32{0.1}, nil
}
func (c *countingEmbedder) Enabled() bool { return true }

func TestRetrieve_genericMakesNoEmbedRequest(t *testing.T) {
	emb := &countingEmbedder{}
	r := &Retriever{
		KB: &storage.Knowledge{Enabled: true, Embedder: emb},
	}
	got := r.Retrieve(context.Background(), "user-1", "sess", "what is a CRDT?", 0.35)
	if got.UsedEmbed || emb.calls.Load() != 0 {
		t.Fatalf("generic prompt must not embed: used=%v calls=%d plan=%+v", got.UsedEmbed, emb.calls.Load(), got.Plan)
	}
	if len(got.Hits) != 0 {
		t.Fatalf("expected no hits, got %#v", got.Hits)
	}
}

func TestRetrieve_birthdayPlansEmbed(t *testing.T) {
	plan := PlanMemory("When is Sarah's birthday?")
	if !plan.NeedsEmbed || !plan.ShouldRetrieve {
		t.Fatalf("birthday query should plan embed/retrieve: %+v", plan)
	}
}

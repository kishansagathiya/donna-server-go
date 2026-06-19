package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// Backfill embeddings for existing kb_facts and notes rows that lack them.
// Idempotent: safe to re-run. Run once locally against prod Supabase:
//
//	go run ./cmd/backfill
//
// Requires OPENAI_API_KEY, SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY in env/.env.
func main() {
	loadEnv()

	supaURL := strings.TrimSuffix(os.Getenv("SUPABASE_URL"), "/")
	supaKey := os.Getenv("SUPABASE_SERVICE_ROLE_KEY")
	openAIKey := os.Getenv("OPENAI_API_KEY")
	embeddingModel := strings.TrimSpace(os.Getenv("DONNA_EMBEDDING_MODEL"))
	if embeddingModel == "" {
		embeddingModel = "text-embedding-3-small"
	}

	if supaURL == "" || supaKey == "" {
		die("missing SUPABASE_URL or SUPABASE_SERVICE_ROLE_KEY")
	}
	if openAIKey == "" {
		die("missing OPENAI_API_KEY")
	}

	supa := storage.NewSupabase(supaURL, supaKey)
	emb := providers.NewEmbeddings(openAIKey, embeddingModel)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	factRows, err := backfillFacts(ctx, supa, emb)
	if err != nil {
		die(fmt.Sprintf("fact backfill failed: %v", err))
	}
	noteRows, err := backfillNotes(ctx, supa, emb)
	if err != nil {
		die(fmt.Sprintf("note backfill failed: %v", err))
	}

	fmt.Printf("backfill complete: %d facts, %d notes embedded\n", factRows, noteRows)
}

const backfillBatch = 100

type factBackfillRow struct {
	ID         string  `json:"id"`
	Fact       string  `json:"fact"`
	EntityName *string `json:"entity_name"`
	Topic      *string `json:"topic"`
}

func backfillFacts(ctx context.Context, supa *storage.Supabase, emb *providers.Embeddings) (int, error) {
	total := 0
	for offset := 0; ; offset += backfillBatch {
		q := url.Values{}
		q.Set("select", "id,fact,entity_name,topic")
		q.Set("active", "eq.true")
		q.Set("embedding", "is.null")
		q.Set("order", "created_at.asc")
		q.Set("limit", fmt.Sprintf("%d", backfillBatch))
		q.Set("offset", fmt.Sprintf("%d", offset))

		var rows []factBackfillRow
		if err := supa.Get(ctx, "kb_facts", q, &rows); err != nil {
			return total, err
		}
		if len(rows) == 0 {
			break
		}

		inputs := make([]string, len(rows))
		for i, r := range rows {
			inputs[i] = factEmbedInput(r.EntityName, r.Topic, r.Fact)
		}
		vecs, err := emb.Embed(ctx, inputs)
		if err != nil {
			return total, err
		}
		for i, r := range rows {
			if err := patchEmbedding(ctx, supa, "kb_facts", r.ID, vecs[i]); err != nil {
				return total, err
			}
		}
		total += len(rows)
		fmt.Printf("facts: embedded %d (running total %d)\n", len(rows), total)
		if len(rows) < backfillBatch {
			break
		}
	}
	return total, nil
}

type noteBackfillRow struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
	Preview string `json:"preview"`
}

func backfillNotes(ctx context.Context, supa *storage.Supabase, emb *providers.Embeddings) (int, error) {
	total := 0
	for offset := 0; ; offset += backfillBatch {
		q := url.Values{}
		q.Set("select", "id,title,content,preview")
		q.Set("embedding", "is.null")
		q.Set("order", "created_at.asc")
		q.Set("limit", fmt.Sprintf("%d", backfillBatch))
		q.Set("offset", fmt.Sprintf("%d", offset))

		var rows []noteBackfillRow
		if err := supa.Get(ctx, "notes", q, &rows); err != nil {
			return total, err
		}
		if len(rows) == 0 {
			break
		}

		inputs := make([]string, len(rows))
		for i, r := range rows {
			text := strings.TrimSpace(r.Title + "\n" + r.Content)
			if text == "" {
				text = strings.TrimSpace(r.Preview)
			}
			inputs[i] = text
		}
		vecs, err := emb.Embed(ctx, inputs)
		if err != nil {
			return total, err
		}
		for i, r := range rows {
			if err := patchEmbedding(ctx, supa, "notes", r.ID, vecs[i]); err != nil {
				return total, err
			}
		}
		total += len(rows)
		fmt.Printf("notes: embedded %d (running total %d)\n", len(rows), total)
		if len(rows) < backfillBatch {
			break
		}
	}
	return total, nil
}

func patchEmbedding(ctx context.Context, supa *storage.Supabase, table, id string, vec []float32) error {
	if vec == nil {
		return nil
	}
	q := url.Values{}
	q.Set("id", "eq."+id)
	return supa.Patch(ctx, table, q, map[string]any{"embedding": vec})
}

func factEmbedInput(entityName, topic *string, fact string) string {
	parts := make([]string, 0, 3)
	if entityName != nil && strings.TrimSpace(*entityName) != "" {
		parts = append(parts, *entityName)
	}
	if topic != nil && strings.TrimSpace(*topic) != "" {
		parts = append(parts, *topic)
	}
	parts = append(parts, fact)
	return strings.Join(parts, " ")
}

func loadEnv() {
	candidates := []string{
		filepath.Join("..", ".env"),
		filepath.Join("..", "..", ".env"),
		".env",
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			_ = godotenv.Load(path)
			return
		}
	}
}

func die(msg string) {
	fmt.Fprintf(os.Stderr, "backfill: %s\n", msg)
	os.Exit(1)
}

// One-off integration check: persists a name fact and verifies Supabase read-back.
// Usage: go run ./scripts/test_live_facts
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/knowledge"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}
	if !cfg.PersistKnowledge {
		fmt.Fprintln(os.Stderr, "knowledge base disabled — set SUPABASE_URL and SUPABASE_SERVICE_ROLE_KEY")
		os.Exit(1)
	}

	kb := &storage.Knowledge{
		DB:      storage.NewSupabase(cfg.SupabaseURL, cfg.SupabaseServiceRoleKey),
		Enabled: true,
	}

	ctx := context.Background()
	userID := uuid.NewString()
	transcript := "My name is Kishan"

	knowledge.PersistLiveFactsFromTranscript(ctx, kb, knowledge.LiveFactsInput{
		UserID:     userID,
		Transcript: transcript,
	})

	summary, err := kb.GetUserProfileSummary(ctx, userID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "profile read failed: %v\n", err)
		os.Exit(1)
	}
	facts, err := kb.GetActiveFacts(ctx, userID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "facts read failed: %v\n", err)
		os.Exit(1)
	}

	var nameFact string
	for _, f := range facts {
		if strings.Contains(strings.ToLower(f.Fact), "kishan") {
			nameFact = f.Fact
			break
		}
	}

	if nameFact == "" {
		fmt.Fprintln(os.Stderr, "FAIL: no name fact found in kb_facts")
		os.Exit(1)
	}
	if !strings.Contains(strings.ToLower(summary), "kishan") {
		fmt.Fprintf(os.Stderr, "FAIL: profile summary missing name: %q\n", summary)
		os.Exit(1)
	}

	fmt.Printf("OK: live fact persisted for user %s\n", userID[:8])
	fmt.Printf("  fact: %s\n", nameFact)
	fmt.Printf("  profile: %s\n", summary)
}

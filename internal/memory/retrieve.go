package memory

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const (
	retrieveBudget     = 500 * time.Millisecond
	retrieveItemCap    = 8
	retrieveTokenBudget = 1200 // approximate tokens
	charsPerToken       = 4
)

// Hit is a ranked memory item for prompt augmentation.
type Hit struct {
	Source     string
	ID         string
	Text       string
	Score      float64
	Kind       string
	Predicate  string
	EntityName string
	Confidence float64
}

// Result is the output of intent-aware hybrid retrieval.
type Result struct {
	Hits           []Hit
	Clarification  string
	Plan           Plan
	UsedEmbed      bool
	LatencyMs      int
	FallbackLexical bool
}

// Retriever performs intent-aware hybrid memory retrieval.
type Retriever struct {
	KB    *storage.Knowledge
	Notes *storage.Notes
}

// Retrieve runs planning + hybrid lexical/entity (+ optional embedding) under a
// 500ms budget. Generic prompts make no embedding request.
func (r *Retriever) Retrieve(ctx context.Context, userID, sessionID, transcript string, minScore float64) Result {
	start := time.Now()
	plan := PlanMemory(transcript)
	out := Result{Plan: plan}
	if !plan.ShouldRetrieve || r == nil || r.KB == nil || !r.KB.Enabled {
		out.LatencyMs = int(time.Since(start).Milliseconds())
		return out
	}
	if minScore <= 0 {
		minScore = 0.35
	}

	budgetCtx, cancel := context.WithTimeout(ctx, retrieveBudget)
	defer cancel()

	var (
		lexical   []Hit
		vector    []Hit
		notes     []Hit
		usedEmbed bool
		wg        sync.WaitGroup
		mu        sync.Mutex
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		lexical = r.lexicalAndEntity(budgetCtx, userID, transcript, plan)
	}()

	if plan.NeedsEmbed {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hits, used := r.embedPath(budgetCtx, userID, transcript)
			mu.Lock()
			usedEmbed = used
			vector = hits
			mu.Unlock()
		}()
	}

	// Episodic route also pulls note snippets concurrently (lexical).
	if hasRoute(plan, RouteEpisodic) && r.Notes != nil && r.Notes.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snippets, err := r.Notes.RetrieveNoteSnippets(budgetCtx, userID, transcript, 6)
			if err != nil || len(snippets) == 0 {
				return
			}
			local := make([]Hit, 0, len(snippets))
			for i, s := range snippets {
				local = append(local, Hit{
					Source: "note",
					Text:   s,
					Score:  0.55 - float64(i)*0.02,
				})
			}
			mu.Lock()
			notes = local
			mu.Unlock()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-budgetCtx.Done():
		out.FallbackLexical = true
	}

	mu.Lock()
	merged := mergeHits(lexical, vector, notes)
	out.UsedEmbed = usedEmbed
	mu.Unlock()
	merged = filterValid(merged)
	merged = collapseSuperseded(merged)
	clarification := detectConflicts(merged)
	ranked := rankAndCap(merged, minScore, retrieveItemCap, retrieveTokenBudget*charsPerToken)

	out.Hits = ranked
	out.Clarification = clarification
	out.LatencyMs = int(time.Since(start).Milliseconds())
	out.FallbackLexical = out.FallbackLexical || (!out.UsedEmbed && plan.NeedsEmbed)

	// Best-effort telemetry; never block the turn.
	go func() {
		_ = r.KB.InsertMemoryRetrievalEvent(context.Background(), userID, sessionID, transcript, map[string]any{
			"should_retrieve": plan.ShouldRetrieve,
			"needs_embed":     plan.NeedsEmbed,
			"routes":          plan.Routes,
			"entities":        plan.Entities,
			"temporal":        plan.Temporal,
			"source_recall":   plan.SourceRecall,
			"cues":            plan.Cues,
		}, map[string]any{
			"hit_count":       len(out.Hits),
			"used_embed":      out.UsedEmbed,
			"fallback":        out.FallbackLexical,
			"clarification":   out.Clarification != "",
		}, out.LatencyMs)
	}()

	return out
}

func (r *Retriever) lexicalAndEntity(ctx context.Context, userID, transcript string, plan Plan) []Hit {
	var out []Hit
	seen := map[string]struct{}{}

	addFacts := func(facts []storage.MemoryFact, base float64) {
		for i, f := range facts {
			if _, ok := seen[f.ID]; ok {
				continue
			}
			seen[f.ID] = struct{}{}
			out = append(out, factToHit(f, base-float64(i)*0.01))
		}
	}

	// Kind-routed pulls.
	for _, route := range plan.Routes {
		kinds := KindsForRoute(route)
		if len(kinds) == 0 {
			continue
		}
		facts, err := r.KB.ListActiveMemoryFactsByKinds(ctx, userID, kinds, 20)
		if err != nil {
			log.Warn("memory route list failed", map[string]any{"error": err.Error()})
			continue
		}
		// Prefer entity matches within the route.
		if len(plan.Entities) > 0 {
			filtered := filterByEntities(facts, plan.Entities)
			if len(filtered) > 0 {
				addFacts(filtered, 0.8)
				continue
			}
		}
		addFacts(facts, 0.6)
	}

	// Lexical FTS across facts.
	fts, err := r.KB.SearchMemoryFactsLexical(ctx, userID, transcript, 20)
	if err == nil {
		addFacts(fts, 0.7)
	}

	// Entity name substring scan if FTS missed.
	if len(plan.Entities) > 0 {
		all, err := r.KB.ListActiveMemoryFacts(ctx, userID, 100)
		if err == nil {
			addFacts(filterByEntities(all, plan.Entities), 0.85)
		}
	}
	return out
}

func (r *Retriever) embedPath(ctx context.Context, userID, transcript string) ([]Hit, bool) {
	hits, err := r.KB.RetrieveMemory(ctx, userID, transcript, 20)
	if err != nil || len(hits) == 0 {
		return nil, err == nil && r.KB.Embedder != nil && r.KB.Embedder.Enabled()
	}
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		out = append(out, Hit{
			Source: h.Source,
			ID:     h.ID,
			Text:   h.Text,
			Score:  h.Score,
		})
	}
	return out, true
}

func factToHit(f storage.MemoryFact, score float64) Hit {
	kind := ""
	if f.MemoryKind != nil {
		kind = *f.MemoryKind
	}
	pred := ""
	if f.Predicate != nil {
		pred = *f.Predicate
	}
	entity := ""
	if f.EntityName != nil {
		entity = *f.EntityName
	}
	conf := 0.0
	if f.Confidence != nil {
		conf = *f.Confidence
	}
	text := f.Fact
	if entity != "" && !strings.Contains(strings.ToLower(text), strings.ToLower(entity)) {
		text = entity + ": " + text
	}
	return Hit{
		Source:     "fact",
		ID:         f.ID,
		Text:       text,
		Score:      score,
		Kind:       kind,
		Predicate:  pred,
		EntityName: entity,
		Confidence: conf,
	}
}

func filterByEntities(facts []storage.MemoryFact, entities []string) []storage.MemoryFact {
	if len(entities) == 0 {
		return nil
	}
	out := make([]storage.MemoryFact, 0)
	for _, f := range facts {
		blob := strings.ToLower(f.Fact)
		if f.EntityName != nil {
			blob += " " + strings.ToLower(*f.EntityName)
		}
		for _, e := range entities {
			if strings.Contains(blob, strings.ToLower(e)) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

func filterValid(hits []Hit) []Hit {
	// Validity is enforced at storage list time for V2; keep all for now.
	// Placeholder for valid_until filtering when hits carry timestamps.
	return hits
}

func collapseSuperseded(hits []Hit) []Hit {
	// Prefer higher score per ID; drop empties.
	seen := map[string]Hit{}
	order := make([]string, 0, len(hits))
	for _, h := range hits {
		key := h.ID
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(h.Text))
		}
		if key == "" {
			continue
		}
		if prev, ok := seen[key]; ok {
			if h.Score > prev.Score {
				seen[key] = h
			}
			continue
		}
		seen[key] = h
		order = append(order, key)
	}
	out := make([]Hit, 0, len(order))
	for _, k := range order {
		out = append(out, seen[k])
	}
	return out
}

func detectConflicts(hits []Hit) string {
	type key struct{ entity, pred string }
	best := map[key]Hit{}
	for _, h := range hits {
		if h.Source != "fact" || h.Predicate == "" {
			continue
		}
		k := key{entity: strings.ToLower(h.EntityName), pred: strings.ToLower(h.Predicate)}
		if prev, ok := best[k]; ok {
			if !strings.EqualFold(strings.TrimSpace(prev.Text), strings.TrimSpace(h.Text)) &&
				prev.Confidence >= 0.7 && h.Confidence >= 0.7 {
				return "I have conflicting memories about " + displayEntityPred(h) +
					": \"" + truncate(prev.Text, 80) + "\" vs \"" + truncate(h.Text, 80) +
					"\". Which is current?"
			}
			if h.Score > prev.Score {
				best[k] = h
			}
			continue
		}
		best[k] = h
	}
	return ""
}

func displayEntityPred(h Hit) string {
	if h.EntityName != "" && h.Predicate != "" {
		return h.EntityName + " / " + h.Predicate
	}
	if h.Predicate != "" {
		return h.Predicate
	}
	return "this"
}

func rankAndCap(hits []Hit, minScore float64, itemCap, charBudget int) []Hit {
	// Simple score sort (desc) with source diversity: alternate fact/note when possible.
	sorted := make([]Hit, 0, len(hits))
	for _, h := range hits {
		if strings.TrimSpace(h.Text) == "" || h.Score < minScore {
			continue
		}
		sorted = append(sorted, h)
	}
	for i := 0; i < len(sorted); i++ {
		best := i
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Score > sorted[best].Score {
				best = j
			}
		}
		sorted[i], sorted[best] = sorted[best], sorted[i]
	}

	out := make([]Hit, 0, itemCap)
	used := 0
	sources := map[string]int{}
	for _, h := range sorted {
		if len(out) >= itemCap {
			break
		}
		// Source diversity: avoid flooding with a single source beyond 5.
		if sources[h.Source] >= 5 && len(sorted) > itemCap {
			continue
		}
		cost := utf8.RuneCountInString(h.Text) + 3
		if used+cost > charBudget && len(out) > 0 {
			break
		}
		out = append(out, h)
		used += cost
		sources[h.Source]++
	}
	return out
}

func mergeHits(parts ...[]Hit) []Hit {
	var out []Hit
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func hasRoute(p Plan, r Route) bool {
	for _, x := range p.Routes {
		if x == r {
			return true
		}
	}
	return false
}

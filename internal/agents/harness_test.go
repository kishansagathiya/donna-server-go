package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestRegistryAllowlistByToolset(t *testing.T) {
	reg := NewRegistry()
	reg.Register(todoTool())
	reg.Register(requestApprovalTool())
	reg.Register(fetchURLTool())
	defs := reg.Definitions([]string{"orchestration"})
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Function.Name] = true
	}
	if !names["todo"] || !names["request_approval"] {
		t.Fatalf("expected orchestration tools, got %#v", names)
	}
	if names["fetch_url"] {
		t.Fatal("fetch_url should be excluded by toolset allowlist")
	}
}

func TestCompressIfNeeded(t *testing.T) {
	msgs := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "goal"},
	}
	// Exceed DefaultMaxTokens*4 character budget.
	for i := 0; i < 60; i++ {
		msgs = append(msgs, providers.ChatMessage{Role: "assistant", Content: strings.Repeat("x", 4000)})
	}
	out := compressIfNeeded(msgs, "find photo")
	if len(out) >= len(msgs) {
		t.Fatalf("expected compression, before=%d after=%d", len(msgs), len(out))
	}
	joined := ""
	for _, m := range out {
		joined += m.Content
	}
	if !strings.Contains(joined, "Compressed earlier steps") {
		t.Fatalf("missing compress marker")
	}
}

func TestTodoToolUpdatesPlan(t *testing.T) {
	tool := todoTool()
	rc := &RunContext{}
	var got []TodoItem
	rc.SetPlan = func(items []TodoItem) { got = items }
	res, err := tool.Handle(context.Background(), rc, `{"items":[{"id":"1","content":"search","status":"pending"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Content != "search" {
		t.Fatalf("plan not set: %#v", got)
	}
	if !strings.Contains(res.Content, "Plan updated") {
		t.Fatalf("content: %s", res.Content)
	}
}

type scriptedLLM struct {
	calls  int
	script []providers.ChatCompletionMetadata
}

func (s *scriptedLLM) CompleteOnceWithOptions(ctx context.Context, messages []providers.ChatMessage, options providers.ChatCompletionOptions) (providers.ChatCompletionMetadata, error) {
	if s.calls >= len(s.script) {
		return providers.ChatCompletionMetadata{Content: "fallback done"}, nil
	}
	m := s.script[s.calls]
	s.calls++
	return m, nil
}

type memRunStore struct {
	mu    sync.Mutex
	runs  map[string]*storage.AgentRun
	steps map[string][]storage.AgentStep
}

func newMemRunStore(run storage.AgentRun) *memRunStore {
	r := run
	return &memRunStore{
		runs:  map[string]*storage.AgentRun{run.ID: &r},
		steps: map[string][]storage.AgentStep{},
	}
}

func (m *memRunStore) Get(ctx context.Context, userID, runID string) (storage.AgentRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[runID]
	if !ok || r.UserID != userID {
		return storage.AgentRun{}, fmt.Errorf("agent_run_not_found")
	}
	return *r, nil
}

func (m *memRunStore) Patch(ctx context.Context, userID, runID string, patch map[string]any) (storage.AgentRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[runID]
	if !ok || r.UserID != userID {
		return storage.AgentRun{}, fmt.Errorf("agent_run_not_found")
	}
	if v, ok := patch["status"].(string); ok {
		r.Status = v
	}
	if v, ok := patch["redirect_pending"]; ok {
		if v == nil {
			r.RedirectPending = nil
		} else if s, ok := v.(string); ok {
			r.RedirectPending = &s
		}
	}
	if v, ok := patch["plan"].(json.RawMessage); ok {
		r.Plan = v
	}
	if v, ok := patch["step_count"].(int); ok {
		r.StepCount = v
	}
	if v, ok := patch["result"].(json.RawMessage); ok {
		r.Result = v
	}
	if v, ok := patch["error"]; ok {
		if v == nil {
			r.Error = nil
		} else if s, ok := v.(string); ok {
			r.Error = &s
		}
	}
	if v, ok := patch["finished_at"]; ok {
		if v == nil {
			r.FinishedAt = nil
		} else if s, ok := v.(string); ok {
			r.FinishedAt = &s
		}
	}
	return *r, nil
}

func (m *memRunStore) Heartbeat(ctx context.Context, userID, runID, workerID string, lease time.Duration) (storage.AgentRun, error) {
	return m.Patch(ctx, userID, runID, map[string]any{
		"status":      storage.AgentStatusRunning,
		"lease_owner": workerID,
	})
}

func (m *memRunStore) AppendStep(ctx context.Context, userID, runID string, seq int, kind string, payload map[string]any) (storage.AgentStep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, _ := json.Marshal(payload)
	st := storage.AgentStep{
		ID:         fmt.Sprintf("step-%d", seq),
		AgentRunID: runID,
		UserID:     userID,
		Seq:        seq,
		Kind:       kind,
		Payload:    raw,
	}
	m.steps[runID] = append(m.steps[runID], st)
	if r, ok := m.runs[runID]; ok {
		r.StepCount = seq
	}
	return st, nil
}

func (m *memRunStore) ListSteps(ctx context.Context, userID, runID string, afterSeq, limit int) ([]storage.AgentStep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.AgentStep
	for _, st := range m.steps[runID] {
		if st.Seq > afterSeq {
			out = append(out, st)
		}
	}
	return out, nil
}

func (m *memRunStore) Finish(ctx context.Context, userID, runID, status string, result map[string]any, errText string) (storage.AgentRun, error) {
	patch := map[string]any{"status": status, "finished_at": time.Now().UTC().Format(time.RFC3339)}
	if result != nil {
		raw, _ := json.Marshal(result)
		patch["result"] = json.RawMessage(raw)
	}
	if errText != "" {
		patch["error"] = errText
	} else {
		patch["error"] = nil
	}
	return m.Patch(ctx, userID, runID, patch)
}

func (m *memRunStore) WaitForUser(ctx context.Context, userID, runID string, approvalPayload map[string]any) (storage.AgentRun, error) {
	raw, _ := json.Marshal(approvalPayload)
	return m.Patch(ctx, userID, runID, map[string]any{
		"status":      storage.AgentStatusWaitingForUser,
		"result":      json.RawMessage(raw),
		"finished_at": nil,
		"error":       nil,
	})
}

func TestHarnessFinalAnswer(t *testing.T) {
	run := storage.AgentRun{
		ID:            "run-1",
		UserID:        "user-1",
		Goal:          "Find Lisbon photo",
		Status:        storage.AgentStatusQueued,
		Plan:          json.RawMessage(`[]`),
		MemorySnapshot: json.RawMessage(`{}`),
		MaxSteps:      5,
		ToolAllowlist: []string{"orchestration"},
	}
	store := newMemRunStore(run)
	llm := &scriptedLLM{script: []providers.ChatCompletionMetadata{
		{Content: "Found it in your July notes."},
	}}
	reg := NewRegistry()
	reg.Register(todoTool())
	h := &Harness{
		Store:    store,
		LLM:      llm,
		Registry: reg,
		WorkerID: "test",
		Budgets:  Budgets{MaxSteps: 5, WallClock: time.Minute, Lease: time.Minute},
	}
	if err := h.Run(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(context.Background(), "user-1", "run-1")
	if got.Status != storage.AgentStatusSucceeded {
		t.Fatalf("status=%s error=%v", got.Status, got.Error)
	}
	if !strings.Contains(string(got.Result), "Found it") {
		t.Fatalf("result=%s", string(got.Result))
	}
}

func TestHarnessAskUserPauses(t *testing.T) {
	run := storage.AgentRun{
		ID:             "run-ask",
		UserID:         "user-1",
		Goal:           "Book flight",
		Status:         storage.AgentStatusQueued,
		Plan:           json.RawMessage(`[]`),
		MemorySnapshot: json.RawMessage(`{}`),
		MaxSteps:       5,
		ToolAllowlist:  []string{"orchestration"},
	}
	store := newMemRunStore(run)
	args, _ := json.Marshal(map[string]any{"question": "Which airport — SFO or SJC?"})
	llm := &scriptedLLM{script: []providers.ChatCompletionMetadata{
		{
			Content: "Need your preference.",
			ToolCalls: []providers.ToolCall{{
				ID:       "call-ask",
				Type:     "function",
				Function: providers.ToolFunction{Name: "ask_user", Arguments: string(args)},
			}},
		},
	}}
	reg := NewRegistry()
	reg.Register(todoTool())
	reg.Register(askUserTool())
	h := &Harness{
		Store:    store,
		LLM:      llm,
		Registry: reg,
		WorkerID: "test",
		Budgets:  Budgets{MaxSteps: 5, WallClock: time.Minute, Lease: time.Minute},
	}
	if err := h.Run(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(context.Background(), "user-1", "run-ask")
	if got.Status != storage.AgentStatusWaitingForUser {
		t.Fatalf("status=%s", got.Status)
	}
	if !strings.Contains(string(got.Result), "SFO or SJC") {
		t.Fatalf("result=%s", string(got.Result))
	}
}

func TestLooksLikeClarifyingQuestion(t *testing.T) {
	if !looksLikeClarifyingQuestion("Which airport do you prefer — SFO or SJC?") {
		t.Fatal("expected question")
	}
	if !looksLikeClarifyingQuestion("I need more details before I continue. Could you share the travel dates?") {
		t.Fatal("expected clarifying cue")
	}
	if looksLikeClarifyingQuestion("Found three matching notes about Lisbon.") {
		t.Fatal("should not treat summary as question")
	}
}

func TestHarnessClarifyingFinalPauses(t *testing.T) {
	run := storage.AgentRun{
		ID:             "run-q",
		UserID:         "user-1",
		Goal:           "Book flight",
		Status:         storage.AgentStatusQueued,
		Plan:           json.RawMessage(`[]`),
		MemorySnapshot: json.RawMessage(`{}`),
		MaxSteps:       5,
		ToolAllowlist:  []string{"orchestration"},
	}
	store := newMemRunStore(run)
	llm := &scriptedLLM{script: []providers.ChatCompletionMetadata{
		{Content: "Which dates should I search for the flight?"},
	}}
	reg := NewRegistry()
	reg.Register(todoTool())
	h := &Harness{
		Store:    store,
		LLM:      llm,
		Registry: reg,
		WorkerID: "test",
		Budgets:  Budgets{MaxSteps: 5, WallClock: time.Minute, Lease: time.Minute},
	}
	if err := h.Run(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(context.Background(), "user-1", "run-q")
	if got.Status != storage.AgentStatusWaitingForUser {
		t.Fatalf("status=%s result=%s", got.Status, string(got.Result))
	}
}

func TestHarnessRequestApprovalPauses(t *testing.T) {
	run := storage.AgentRun{
		ID:             "run-2",
		UserID:         "user-1",
		Goal:           "Book flight",
		Status:         storage.AgentStatusQueued,
		Plan:           json.RawMessage(`[]`),
		MemorySnapshot: json.RawMessage(`{}`),
		MaxSteps:       5,
		ToolAllowlist:  []string{"orchestration"},
	}
	store := newMemRunStore(run)
	args, _ := json.Marshal(map[string]any{"kind": "book_flight", "summary": "SFO $499"})
	llm := &scriptedLLM{script: []providers.ChatCompletionMetadata{
		{
			Content: "Ready to book.",
			ToolCalls: []providers.ToolCall{{
				ID:       "call-1",
				Type:     "function",
				Function: providers.ToolFunction{Name: "request_approval", Arguments: string(args)},
			}},
		},
	}}
	reg := NewRegistry()
	reg.Register(todoTool())
	reg.Register(requestApprovalTool())
	h := &Harness{
		Store:    store,
		LLM:      llm,
		Registry: reg,
		WorkerID: "test",
		Budgets:  Budgets{MaxSteps: 5, WallClock: time.Minute, Lease: time.Minute},
	}
	if err := h.Run(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(context.Background(), "user-1", "run-2")
	if got.Status != storage.AgentStatusWaitingForUser {
		t.Fatalf("status=%s", got.Status)
	}
}

func TestHarnessCancelMidRun(t *testing.T) {
	run := storage.AgentRun{
		ID:            "run-3",
		UserID:        "user-1",
		Goal:          "Long task",
		Status:        storage.AgentStatusQueued,
		Plan:          json.RawMessage(`[]`),
		MemorySnapshot: json.RawMessage(`{}`),
		MaxSteps:      10,
		ToolAllowlist: []string{"orchestration"},
	}
	store := newMemRunStore(run)
	llm := &scriptedLLM{script: []providers.ChatCompletionMetadata{
		{Content: "still working", ToolCalls: []providers.ToolCall{{
			ID: "c1", Type: "function",
			Function: providers.ToolFunction{Name: "todo", Arguments: `{"items":[{"id":"1","content":"a","status":"pending"}]}`},
		}}},
	}}
	// After first tool round, mark cancelled before next LLM call by wrapping Get.
	reg := NewRegistry()
	reg.Register(todoTool())

	cancelStore := &cancelAfterToolStore{memRunStore: store, afterSteps: 3}
	h := &Harness{
		Store:    cancelStore,
		LLM:      llm,
		Registry: reg,
		WorkerID: "test",
		Budgets:  Budgets{MaxSteps: 10, WallClock: time.Minute, Lease: time.Minute},
	}
	if err := h.Run(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(context.Background(), "user-1", "run-3")
	if got.Status != storage.AgentStatusCancelled {
		// cancel store flips status on Get; harness returns nil when cancelled
		if got.Status == storage.AgentStatusRunning || got.Status == storage.AgentStatusSucceeded {
			t.Fatalf("expected cancel path, status=%s", got.Status)
		}
	}
}

type cancelAfterToolStore struct {
	*memRunStore
	afterSteps int
}

func (c *cancelAfterToolStore) Get(ctx context.Context, userID, runID string) (storage.AgentRun, error) {
	run, err := c.memRunStore.Get(ctx, userID, runID)
	if err != nil {
		return run, err
	}
	c.mu.Lock()
	n := len(c.steps[runID])
	c.mu.Unlock()
	if n >= c.afterSteps {
		run.Status = storage.AgentStatusCancelled
		c.mu.Lock()
		if r := c.runs[runID]; r != nil {
			r.Status = storage.AgentStatusCancelled
		}
		c.mu.Unlock()
	}
	return run, nil
}

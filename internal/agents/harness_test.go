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
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestRegistryAllowlistByToolset(t *testing.T) {
	reg := NewRegistry()
	reg.Register(todoTool())
	reg.Register(requestApprovalTool())
	reg.Register(fetchURLTool())
	reg.Register(browsePageTool(tools.NewBrowserClient("http://127.0.0.1:9229")))
	defs := reg.Definitions([]string{"orchestration"})
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Function.Name] = true
	}
	if !names["todo"] || !names["request_approval"] {
		t.Fatalf("expected orchestration tools, got %#v", names)
	}
	if names["fetch_url"] || names["browse_page"] {
		t.Fatal("web/browser tools should be excluded by orchestration allowlist")
	}

	webDefs := reg.Definitions([]string{"web", "browser"})
	webNames := map[string]bool{}
	for _, d := range webDefs {
		webNames[d.Function.Name] = true
	}
	if !webNames["fetch_url"] || !webNames["browse_page"] {
		t.Fatalf("expected web+browser tools, got %#v", webNames)
	}
}

func TestDefaultToolsetsRegistersBrowseWhenConfigured(t *testing.T) {
	without := DefaultToolsets(nil, nil, "", nil, nil)
	with := DefaultToolsets(nil, nil, "http://127.0.0.1:9229", nil, nil)
	if _, ok := without.Get("browse_page"); ok {
		t.Fatal("browse_page must not register without browser URL")
	}
	if _, ok := with.Get("browse_page"); !ok {
		t.Fatal("browse_page must register when browser URL is set")
	}
	if _, ok := with.Get("fetch_url"); !ok {
		t.Fatal("fetch_url should always register")
	}
	if _, ok := with.Get("fetch_image"); !ok {
		t.Fatal("fetch_image should always register")
	}
}

func TestImageURLFromToolContent(t *testing.T) {
	got := imageURLFromToolContent("Verified public image.\n\nURL: https://example.com/a.png\nMIME: image/png\n")
	if got != "https://example.com/a.png" {
		t.Fatalf("got %q", got)
	}
	if imageURLFromToolContent("no url here") != "" {
		t.Fatal("expected empty")
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

type fakeApprovalLedger struct {
	calls    int
	lastKind string
}

func (f *fakeApprovalLedger) RecordRequest(ctx context.Context, userID, agentRunID string, payload map[string]any) (string, error) {
	f.calls++
	f.lastKind = trimAny(payload["kind"])
	if args, ok := payload["args"].(map[string]any); ok {
		if k := trimAny(args["kind"]); k != "" {
			f.lastKind = k
		}
	}
	return "ar-test", nil
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
	current, err := m.Get(ctx, userID, runID)
	if err != nil {
		return storage.AgentRun{}, err
	}
	if storage.IsTerminalAgentStatus(current.Status) {
		return current, nil
	}
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
	current, err := m.Get(ctx, userID, runID)
	if err != nil {
		return storage.AgentRun{}, err
	}
	if storage.IsTerminalAgentStatus(current.Status) {
		return current, nil
	}
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
	current, err := m.Get(ctx, userID, runID)
	if err != nil {
		return storage.AgentRun{}, err
	}
	if storage.IsTerminalAgentStatus(current.Status) {
		return current, nil
	}
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
		ID:             "run-1",
		UserID:         "user-1",
		Goal:           "Find Lisbon photo",
		Status:         storage.AgentStatusQueued,
		Plan:           json.RawMessage(`[]`),
		MemorySnapshot: json.RawMessage(`{}`),
		MaxSteps:       5,
		ToolAllowlist:  []string{"orchestration"},
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

func TestHarnessRespectsMarkFinishedDuringLLM(t *testing.T) {
	run := storage.AgentRun{
		ID:             "run-finish",
		UserID:         "user-1",
		Goal:           "Find Lisbon photo",
		Status:         storage.AgentStatusQueued,
		Plan:           json.RawMessage(`[]`),
		MemorySnapshot: json.RawMessage(`{}`),
		MaxSteps:       5,
		ToolAllowlist:  []string{"orchestration"},
	}
	store := newMemRunStore(run)
	llm := &finishDuringCallLLM{store: store, run: run}
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
	got, _ := store.Get(context.Background(), "user-1", "run-finish")
	if got.Status != storage.AgentStatusSucceeded {
		t.Fatalf("status=%s", got.Status)
	}
	if !strings.Contains(string(got.Result), "closed_by_user") {
		t.Fatalf("user finish was overwritten: %s", string(got.Result))
	}
	if strings.Contains(string(got.Result), "I found the photo") {
		t.Fatalf("harness result replaced mark-finished: %s", string(got.Result))
	}
}

type finishDuringCallLLM struct {
	store *memRunStore
	run   storage.AgentRun
}

func (s *finishDuringCallLLM) CompleteOnceWithOptions(ctx context.Context, messages []providers.ChatMessage, options providers.ChatCompletionOptions) (providers.ChatCompletionMetadata, error) {
	_, err := s.store.Finish(ctx, s.run.UserID, s.run.ID, storage.AgentStatusSucceeded, map[string]any{
		"closed_by_user": true,
		"summary":        "Marked finished by user.",
	}, "")
	if err != nil {
		return providers.ChatCompletionMetadata{}, err
	}
	return providers.ChatCompletionMetadata{Content: "I found the photo"}, nil
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
	ledger := &fakeApprovalLedger{}
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
		Store:     store,
		LLM:       llm,
		Registry:  reg,
		WorkerID:  "test",
		Budgets:   Budgets{MaxSteps: 5, WallClock: time.Minute, Lease: time.Minute},
		Approvals: ledger,
	}
	if err := h.Run(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(context.Background(), "user-1", "run-2")
	if got.Status != storage.AgentStatusWaitingForUser {
		t.Fatalf("status=%s", got.Status)
	}
	if ledger.calls != 1 || ledger.lastKind != "book_flight" {
		t.Fatalf("ledger calls=%d kind=%s", ledger.calls, ledger.lastKind)
	}
}

func TestHarnessCancelMidRun(t *testing.T) {
	run := storage.AgentRun{
		ID:             "run-3",
		UserID:         "user-1",
		Goal:           "Long task",
		Status:         storage.AgentStatusQueued,
		Plan:           json.RawMessage(`[]`),
		MemorySnapshot: json.RawMessage(`{}`),
		MaxSteps:       10,
		ToolAllowlist:  []string{"orchestration"},
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

func TestNormalizeAskOptions(t *testing.T) {
	got := normalizeAskOptions([]any{
		map[string]any{"id": "sfo", "label": "San Francisco"},
		"Direct flight",
		map[string]any{"text": "One stop"},
		map[string]any{"id": "skip", "label": "   "},
	})
	if len(got) != 3 {
		t.Fatalf("expected 3 options, got %#v", got)
	}
	if got[0]["id"] != "sfo" || got[0]["label"] != "San Francisco" {
		t.Fatalf("first option: %#v", got[0])
	}
	if got[1]["id"] != "opt_2" || got[1]["label"] != "Direct flight" {
		t.Fatalf("string option: %#v", got[1])
	}
	if got[2]["label"] != "One stop" {
		t.Fatalf("text option: %#v", got[2])
	}
}

func TestExtractOptionsFromText(t *testing.T) {
	text := "Which airport?\n- SFO\n- OAK\n- SJC\n"
	got := extractOptionsFromText(text)
	if len(got) != 3 {
		t.Fatalf("expected 3, got %#v", got)
	}
	if got[0]["label"] != "SFO" || got[2]["label"] != "SJC" {
		t.Fatalf("labels: %#v", got)
	}
	if extractOptionsFromText("Just one\n- Only") != nil {
		t.Fatal("need at least two list items")
	}
}

func scriptTool(name, content string, outcome ToolOutcome, handlerErr error, meta map[string]any) RegisteredTool {
	return RegisteredTool{
		Toolset: "orchestration",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        name,
				Description: name,
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
		Handle: func(ctx context.Context, runCtx *RunContext, argsJSON string) (ToolResult, error) {
			if handlerErr != nil {
				return ToolResult{}, handlerErr
			}
			return ToolResult{Content: content, Outcome: outcome, Meta: meta}, nil
		},
	}
}

func toolCall(id, name string) providers.ToolCall {
	return providers.ToolCall{
		ID:   id,
		Type: "function",
		Function: providers.ToolFunction{
			Name:      name,
			Arguments: `{}`,
		},
	}
}

func runHarness(t *testing.T, store *memRunStore, run storage.AgentRun, llm Completer, tools ...RegisteredTool) {
	t.Helper()
	reg := NewRegistry()
	for _, tool := range tools {
		reg.Register(tool)
	}
	h := &Harness{
		Store:    store,
		LLM:      llm,
		Registry: reg,
		WorkerID: "test",
		Budgets:  Budgets{MaxSteps: 8, WallClock: time.Minute, Lease: time.Minute},
	}
	if err := h.Run(context.Background(), run); err != nil {
		t.Fatal(err)
	}
}

func queuedRun(id, goal string) storage.AgentRun {
	return storage.AgentRun{
		ID:             id,
		UserID:         "user-1",
		Goal:           goal,
		Status:         storage.AgentStatusQueued,
		Plan:           json.RawMessage(`[]`),
		MemorySnapshot: json.RawMessage(`{}`),
		MaxSteps:       8,
		ToolAllowlist:  []string{"orchestration"},
	}
}

func stepMaps(t *testing.T, store *memRunStore, runID, kind string) []map[string]any {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	var out []map[string]any
	for _, st := range store.steps[runID] {
		if kind != "" && st.Kind != kind {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(st.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		payload["_kind"] = st.Kind
		payload["_seq"] = st.Seq
		out = append(out, payload)
	}
	return out
}

func TestHarnessToolOutcomes(t *testing.T) {
	run := queuedRun("run-outcomes", "test outcomes")
	store := newMemRunStore(run)
	llm := &scriptedLLM{script: []providers.ChatCompletionMetadata{
		{ToolCalls: []providers.ToolCall{
			toolCall("ok-1", "ok_tool"),
			toolCall("fail-1", "fail_tool"),
			toolCall("block-1", "block_tool"),
			toolCall("exit-1", "exit_tool"),
			toolCall("herr-1", "handler_err_tool"),
		}},
		{Content: "Wrapped up."},
	}}
	runHarness(t, store, run, llm,
		scriptTool("ok_tool", "all good", "", nil, nil),
		scriptTool("fail_tool", "Error: boom", "", nil, nil),
		scriptTool("block_tool", "Refused: policy", "", nil, nil),
		scriptTool("exit_tool", "exit=2", "", nil, map[string]any{"exit": 2}),
		scriptTool("handler_err_tool", "", "", fmt.Errorf("handler exploded"), nil),
	)

	results := stepMaps(t, store, run.ID, storage.AgentStepToolResult)
	if len(results) != 5 {
		t.Fatalf("results=%d", len(results))
	}
	want := map[string]string{
		"ok-1":    "succeeded",
		"fail-1":  "failed",
		"block-1": "blocked",
		"exit-1":  "failed",
		"herr-1":  "failed",
	}
	for _, payload := range results {
		id, _ := payload["id"].(string)
		got, _ := payload["outcome"].(string)
		if want[id] != got {
			t.Fatalf("id %s outcome=%s want %s payload=%v", id, got, want[id], payload)
		}
		if _, ok := payload["duration_ms"]; !ok {
			t.Fatalf("missing duration_ms on %s", id)
		}
		if id != "ok-1" {
			if _, ok := payload["error"]; !ok {
				t.Fatalf("missing error on %s", id)
			}
		}
	}
}

func TestHarnessApprovalCallID(t *testing.T) {
	run := queuedRun("run-callid", "ask")
	store := newMemRunStore(run)
	args, _ := json.Marshal(map[string]any{"question": "Which airport?"})
	llm := &scriptedLLM{script: []providers.ChatCompletionMetadata{
		{
			Content: "Need a choice.",
			ToolCalls: []providers.ToolCall{{
				ID:       "call-ask",
				Type:     "function",
				Function: providers.ToolFunction{Name: "ask_user", Arguments: string(args)},
			}},
		},
	}}
	reg := NewRegistry()
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
	approvals := stepMaps(t, store, run.ID, storage.AgentStepApprovalRequest)
	if len(approvals) != 1 {
		t.Fatalf("approvals=%d", len(approvals))
	}
	if approvals[0]["call_id"] != "call-ask" {
		t.Fatalf("call_id=%v", approvals[0]["call_id"])
	}
}

func TestHarnessRecoveryLinks(t *testing.T) {
	run := queuedRun("run-recovery", "retry after fail")
	store := newMemRunStore(run)
	llm := &scriptedLLM{script: []providers.ChatCompletionMetadata{
		{ToolCalls: []providers.ToolCall{toolCall("c1", "fail_tool")}},
		{Content: "Trying a different approach.", ToolCalls: []providers.ToolCall{toolCall("c2", "ok_tool")}},
		{Content: "Done after recovery."},
	}}
	runHarness(t, store, run, llm,
		scriptTool("fail_tool", "Error: first try", "", nil, nil),
		scriptTool("ok_tool", "recovered", "", nil, nil),
	)

	var recovered []map[string]any
	for _, payload := range stepMaps(t, store, run.ID, "") {
		if payload["recovery_from"] != nil {
			recovered = append(recovered, payload)
		}
	}
	if len(recovered) != 1 {
		t.Fatalf("expected one recovery event, got %d: %#v", len(recovered), recovered)
	}
	ids, _ := recovered[0]["recovery_from"].([]any)
	if len(ids) != 1 || ids[0] != "c1" {
		t.Fatalf("recovery_from=%v", recovered[0]["recovery_from"])
	}
}

func TestHarnessConsecutiveFailures(t *testing.T) {
	run := queuedRun("run-consec", "fail twice")
	store := newMemRunStore(run)
	llm := &scriptedLLM{script: []providers.ChatCompletionMetadata{
		{ToolCalls: []providers.ToolCall{toolCall("a1", "fail_tool")}},
		{ToolCalls: []providers.ToolCall{toolCall("a2", "fail_tool")}},
		{Content: "Gave up? No — succeeded.", ToolCalls: []providers.ToolCall{toolCall("a3", "ok_tool")}},
		{Content: "Finished."},
	}}
	runHarness(t, store, run, llm,
		scriptTool("fail_tool", "Error: still broken", "", nil, nil),
		scriptTool("ok_tool", "ok", "", nil, nil),
	)

	var recovered []map[string]any
	for _, payload := range stepMaps(t, store, run.ID, "") {
		if payload["recovery_from"] != nil {
			recovered = append(recovered, payload)
		}
	}
	if len(recovered) != 2 {
		t.Fatalf("expected two recovery events, got %d: %#v", len(recovered), recovered)
	}
	first, _ := recovered[0]["recovery_from"].([]any)
	second, _ := recovered[1]["recovery_from"].([]any)
	if len(first) != 1 || first[0] != "a1" {
		t.Fatalf("first recovery=%v", recovered[0]["recovery_from"])
	}
	if len(second) != 1 || second[0] != "a2" {
		t.Fatalf("second recovery=%v", recovered[1]["recovery_from"])
	}
}

func TestHarnessNoRecoveryWithinSameResponse(t *testing.T) {
	run := queuedRun("run-same", "parallel calls")
	store := newMemRunStore(run)
	llm := &scriptedLLM{script: []providers.ChatCompletionMetadata{
		{ToolCalls: []providers.ToolCall{
			toolCall("p1", "fail_tool"),
			toolCall("p2", "ok_tool"),
		}},
		{Content: "Noted the failure."},
	}}
	runHarness(t, store, run, llm,
		scriptTool("fail_tool", "Error: no", "", nil, nil),
		scriptTool("ok_tool", "yes", "", nil, nil),
	)

	for _, payload := range stepMaps(t, store, run.ID, storage.AgentStepToolCall) {
		if payload["recovery_from"] != nil {
			t.Fatalf("same-response tool_call should not recover: %#v", payload)
		}
	}
	thoughts := stepMaps(t, store, run.ID, storage.AgentStepThought)
	var found bool
	for _, payload := range thoughts {
		ids, _ := payload["recovery_from"].([]any)
		if len(ids) == 1 && ids[0] == "p1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected next-turn thought to recover from p1, thoughts=%#v", thoughts)
	}
}

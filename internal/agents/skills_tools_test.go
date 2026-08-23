package agents

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/skills"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type fakeSkillProvider struct {
	mu     sync.Mutex
	user   map[string]skills.Skill
	system map[string]skills.Skill
	saved  []skills.NewSkillInput
}

func newFakeSkillProvider(user ...skills.Skill) *fakeSkillProvider {
	p := &fakeSkillProvider{
		user:   map[string]skills.Skill{},
		system: map[string]skills.Skill{},
	}
	for _, s := range user {
		p.user[s.Name] = s
	}
	for _, s := range skills.Bundled() {
		p.system[s.Name] = s
	}
	return p
}

func (f *fakeSkillProvider) GetByName(ctx context.Context, userID, name string) (skills.Skill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.user[name]; ok {
		return s, nil
	}
	if s, ok := f.system[name]; ok {
		return s, nil
	}
	return skills.Skill{}, storage.ErrSkillNotFound
}

func (f *fakeSkillProvider) SaveUser(ctx context.Context, userID string, in skills.NewSkillInput) (skills.Skill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, in)
	in.Name = strings.TrimSpace(in.Name)
	f.user[in.Name] = skills.Skill{Name: in.Name, Description: in.Description, Content: in.Content, Source: "agent"}
	return f.user[in.Name], nil
}

func (f *fakeSkillProvider) List(ctx context.Context, userID string) ([]skills.Skill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]skills.Skill, 0, len(f.user)+len(f.system))
	for _, s := range f.user {
		out = append(out, s)
	}
	for _, s := range f.system {
		if _, shadowed := f.user[s.Name]; !shadowed {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeSkillProvider) Match(ctx context.Context, userID, goal string, limit int) []skills.MatchScore {
	all, _ := f.List(ctx, userID)
	return skills.Match(goal, all, limit)
}

func TestDefaultToolsetsRegistersSkillsTools(t *testing.T) {
	without := DefaultToolsets(nil, nil, "", nil, nil)
	if _, ok := without.Get("load_skill"); ok {
		t.Fatal("load_skill must not register without provider")
	}
	with := DefaultToolsets(nil, nil, "", newFakeSkillProvider(), nil)
	for _, name := range []string{"load_skill", "save_skill", "list_skills"} {
		if _, ok := with.Get(name); !ok {
			t.Fatalf("%s should register with provider", name)
		}
	}
}

func TestLoadSkillTool(t *testing.T) {
	prov := newFakeSkillProvider(skills.Skill{
		Name:        "trip-planning",
		Description: "plan trips",
		Content:     "1. Ask dates.",
		Source:      "user",
	})
	tool := loadSkillTool(prov)
	res, err := tool.Handle(context.Background(), &RunContext{UserID: "u1"}, `{"name":"trip-planning"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "1. Ask dates.") {
		t.Fatalf("content: %s", res.Content)
	}
	if res.Meta["skill"] != "trip-planning" {
		t.Fatalf("meta: %#v", res.Meta)
	}

	res, err = tool.Handle(context.Background(), &RunContext{UserID: "u1"}, `{"name":"missing"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "Error:") {
		t.Fatalf("missing skill should return error content, got: %s", res.Content)
	}
}

func TestLoadSkillToolShadowsSystemWithUser(t *testing.T) {
	prov := newFakeSkillProvider(skills.Skill{
		Name:        "web-research",
		Description: "user override",
		Content:     "USER OVERRIDE",
		Source:      "user",
	})
	tool := loadSkillTool(prov)
	res, err := tool.Handle(context.Background(), &RunContext{UserID: "u1"}, `{"name":"web-research"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "USER OVERRIDE") {
		t.Fatalf("user skill should shadow system, got: %s", res.Content)
	}
}

func TestSaveSkillTool(t *testing.T) {
	prov := newFakeSkillProvider()
	tool := saveSkillTool(prov)
	rc := &RunContext{UserID: "u1", RunID: "run-9"}
	res, err := tool.Handle(context.Background(), rc, `{"name":"flight-prefs","description":"airline prefs","content":"Prefer United."}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "Skill saved: flight-prefs") {
		t.Fatalf("content: %s", res.Content)
	}
	if len(prov.saved) != 1 {
		t.Fatalf("saved: %#v", prov.saved)
	}
	in := prov.saved[0]
	if in.Source != "agent" || in.AgentRunID == nil || *in.AgentRunID != "run-9" {
		t.Fatalf("input: %#v", in)
	}
}

func TestListSkillsTool(t *testing.T) {
	prov := newFakeSkillProvider()
	tool := listSkillsTool(prov)
	res, err := tool.Handle(context.Background(), &RunContext{UserID: "u1"}, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "web-research") || !strings.Contains(res.Content, "[system]") {
		t.Fatalf("content: %s", res.Content)
	}
}

func TestRenderSkillsBlock(t *testing.T) {
	raw := []any{
		map[string]any{"name": "flight-prefs", "description": "airline prefs", "kind": "selected", "content": "Always United, aisle."},
		map[string]any{"name": "web-research", "description": "web procedure", "kind": "matched"},
	}
	out := renderSkillsBlock(raw)
	if !strings.Contains(out, "User-selected skills") {
		t.Fatalf("missing selected header: %q", out)
	}
	if !strings.Contains(out, "Always United, aisle.") {
		t.Fatalf("selected full content missing: %q", out)
	}
	if !strings.Contains(out, "load_skill(name)") {
		t.Fatalf("matched hint missing: %q", out)
	}
	if !strings.Contains(out, "web-research — web procedure") {
		t.Fatalf("matched entry missing: %q", out)
	}
	if renderSkillsBlock(nil) != "" || renderSkillsBlock([]any{}) != "" {
		t.Fatal("empty input should render empty")
	}
}

func TestHarnessBootstrapsSkillsIntoSystemPrompt(t *testing.T) {
	snapshot := map[string]any{
		"skills": []map[string]any{
			{"name": "flight-prefs", "description": "airline prefs", "kind": "selected", "content": "Always United, aisle."},
			{"name": "web-research", "description": "web procedure", "kind": "matched"},
		},
	}
	raw, _ := json.Marshal(snapshot)
	run := storage.AgentRun{
		ID:             "run-s",
		UserID:         "user-1",
		Goal:           "Book a flight",
		Status:         storage.AgentStatusQueued,
		Plan:           json.RawMessage(`[]`),
		MemorySnapshot: raw,
		MaxSteps:       5,
		ToolAllowlist:  []string{"orchestration"},
	}
	store := newMemRunStore(run)
	llm := &recordingLLM{scripted: &scriptedLLM{script: []providers.ChatCompletionMetadata{
		{Content: "Done. Flight preferences noted."},
	}}}
	h := &Harness{Store: store, LLM: llm, Registry: NewRegistry()}
	_ = h
	messages, _, _, err := h.bootstrapMessages(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	sys := messages[0].Content
	if !strings.Contains(sys, "User-selected skills") || !strings.Contains(sys, "Always United, aisle.") {
		t.Fatalf("selected skill not injected: %q", sys)
	}
	if !strings.Contains(sys, "web-research — web procedure") {
		t.Fatalf("matched skill not listed: %q", sys)
	}
}

type recordingLLM struct {
	scripted *scriptedLLM
	messages []providers.ChatMessage
}

func (r *recordingLLM) CompleteOnceWithOptions(ctx context.Context, messages []providers.ChatMessage, options providers.ChatCompletionOptions) (providers.ChatCompletionMetadata, error) {
	r.messages = messages
	return r.scripted.CompleteOnceWithOptions(ctx, messages, options)
}

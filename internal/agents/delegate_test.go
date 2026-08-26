package agents

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type fakeDelegator struct {
	in       SpawnInput
	child    storage.AgentRun
	waited   bool
	spawnErr error
	waitErr  error
}

func (f *fakeDelegator) Spawn(ctx context.Context, userID string, in SpawnInput) (storage.AgentRun, error) {
	f.in = in
	if f.spawnErr != nil {
		return storage.AgentRun{}, f.spawnErr
	}
	return f.child, nil
}

func (f *fakeDelegator) WaitUntilDone(ctx context.Context, userID, runID string, timeout time.Duration) (storage.AgentRun, error) {
	f.waited = true
	if f.waitErr != nil {
		return f.child, f.waitErr
	}
	return f.child, nil
}

func TestDelegateTaskSpawnsChildAndWaits(t *testing.T) {
	d := &fakeDelegator{child: storage.AgentRun{
		ID:     "child-1",
		Status: storage.AgentStatusSucceeded,
		Result: []byte(`{"summary":"Found three options"}`),
	}}
	parentID := "parent-1"
	store := newMemRunStore(storage.AgentRun{ID: parentID, UserID: "u1", Status: storage.AgentStatusRunning})
	tool := DelegateTaskTool(d)
	res, err := tool.Handle(context.Background(), &RunContext{
		UserID: "u1",
		RunID:  parentID,
		Extra:  map[string]any{"store": store},
	}, `{"goal":"Search United vs Delta"}`)
	if err != nil {
		t.Fatal(err)
	}
	if d.in.ParentRunID == nil || *d.in.ParentRunID != parentID {
		t.Fatalf("parent=%v", d.in.ParentRunID)
	}
	if !d.waited {
		t.Fatal("expected wait")
	}
	if !strings.Contains(res.Content, "Found three options") {
		t.Fatalf("content=%s", res.Content)
	}
	for _, set := range d.in.ToolAllowlist {
		if set == "commerce" || set == "delegation" {
			t.Fatalf("child should not have %s", set)
		}
	}
}

func TestDelegateTaskRejectsNested(t *testing.T) {
	grand := "grand-1"
	parent := "parent-1"
	store := newMemRunStore(storage.AgentRun{
		ID:          parent,
		UserID:      "u1",
		ParentRunID: &grand,
		Status:      storage.AgentStatusRunning,
	})
	d := &fakeDelegator{child: storage.AgentRun{ID: "nope"}}
	tool := DelegateTaskTool(d)
	res, err := tool.Handle(context.Background(), &RunContext{
		UserID: "u1",
		RunID:  parent,
		Extra:  map[string]any{"store": store},
	}, `{"goal":"nested"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "nested_delegate_forbidden") {
		t.Fatalf("content=%s", res.Content)
	}
	if d.in.Goal != "" {
		t.Fatal("should not spawn")
	}
}

func TestDelegateTaskWaitFalse(t *testing.T) {
	d := &fakeDelegator{child: storage.AgentRun{ID: "child-2", Status: storage.AgentStatusQueued}}
	store := newMemRunStore(storage.AgentRun{ID: "p", UserID: "u1"})
	tool := DelegateTaskTool(d)
	res, err := tool.Handle(context.Background(), &RunContext{
		UserID: "u1",
		RunID:  "p",
		Extra:  map[string]any{"store": store},
	}, `{"goal":"Look up calendar", "wait": false}`)
	if err != nil {
		t.Fatal(err)
	}
	if d.waited {
		t.Fatal("should not wait")
	}
	if !strings.Contains(res.Content, "child-2") {
		t.Fatalf("content=%s", res.Content)
	}
}

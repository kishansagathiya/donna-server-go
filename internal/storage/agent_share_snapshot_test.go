package storage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildPublicSharedAgentRun_stripsStepsAndMemory(t *testing.T) {
	errText := "failed to book"
	run := AgentRun{
		ID:     "run-1",
		UserID: "user-secret",
		Goal:   "Find cafes in Indiranagar",
		Status: AgentStatusSucceeded,
		MemorySnapshot: json.RawMessage(`{
			"hits": [{"content": "SECRET_MEMORY"}],
			"grounded_goal": "SECRET_GROUNDED"
		}`),
		Result:    json.RawMessage(`{"summary": "Try third wave coffee.", "plan": ["search memory", "browse"]}`),
		CreatedAt: "2026-08-13T10:00:00Z",
		Error:     &errText,
	}
	steps := []AgentStep{
		{
			ID:      "s1",
			Seq:     1,
			Kind:    AgentStepThought,
			Payload: json.RawMessage(`{"text":"SECRET_THOUGHT"}`),
		},
		{
			ID:      "s2",
			Seq:     2,
			Kind:    AgentStepToolCall,
			Payload: json.RawMessage(`{"name":"memory_search","args":{"q":"SECRET_QUERY"}}`),
		},
		{
			ID:      "s3",
			Seq:     3,
			Kind:    AgentStepToolResult,
			Payload: json.RawMessage(`{"name":"memory_search","content":"SECRET_TOOL_RESULT"}`),
		},
		{
			ID:      "s4",
			Seq:     4,
			Kind:    AgentStepMemoryRetrieve,
			Payload: json.RawMessage(`{"content":"SECRET_RETRIEVE"}`),
		},
	}

	out := BuildPublicSharedAgentRun(run, steps)
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)

	if out.Goal != "Find cafes in Indiranagar" {
		t.Fatalf("goal %q", out.Goal)
	}
	if len(out.Turns) != 1 {
		t.Fatalf("turns %d", len(out.Turns))
	}
	if out.Turns[0].Output.Kind != "summary" || out.Turns[0].Output.Text != "Try third wave coffee." {
		t.Fatalf("output %+v", out.Turns[0].Output)
	}

	leaks := []string{
		"SECRET_MEMORY",
		"SECRET_GROUNDED",
		"SECRET_THOUGHT",
		"SECRET_QUERY",
		"SECRET_TOOL_RESULT",
		"SECRET_RETRIEVE",
		"user-secret",
		"memory_snapshot",
		"tool_call",
		"user_id",
		"run-1",
		"search memory",
	}
	for _, leak := range leaks {
		if strings.Contains(body, leak) {
			t.Fatalf("public snapshot leaked %q: %s", leak, body)
		}
	}
}

func TestBuildPublicSharedAgentRun_multiTurnPromptsAndQuestions(t *testing.T) {
	run := AgentRun{
		Goal:   "Find cafes",
		Status: AgentStatusSucceeded,
		Result: json.RawMessage(`{"summary":"Here are three cafes."}`),
	}
	steps := []AgentStep{
		{
			Seq:     1,
			Kind:    AgentStepApprovalRequest,
			Payload: json.RawMessage(`{"question":"Which neighborhood?"}`),
		},
		{
			Seq:     2,
			Kind:    AgentStepUserMessage,
			Payload: json.RawMessage(`{"message":"Indiranagar"}`),
		},
		{
			Seq:     3,
			Kind:    AgentStepThought,
			Payload: json.RawMessage(`{"text":"SECRET_THOUGHT"}`),
		},
	}

	out := BuildPublicSharedAgentRun(run, steps)
	if len(out.Turns) != 2 {
		t.Fatalf("turns %d", len(out.Turns))
	}
	if out.Turns[0].Prompt != "Find cafes" {
		t.Fatalf("turn0 prompt %q", out.Turns[0].Prompt)
	}
	if out.Turns[0].Output.Kind != "question" || out.Turns[0].Output.Text != "Which neighborhood?" {
		t.Fatalf("turn0 output %+v", out.Turns[0].Output)
	}
	if out.Turns[1].Prompt != "Indiranagar" {
		t.Fatalf("turn1 prompt %q", out.Turns[1].Prompt)
	}
	if out.Turns[1].Output.Kind != "summary" || out.Turns[1].Output.Text != "Here are three cafes." {
		t.Fatalf("turn1 output %+v", out.Turns[1].Output)
	}

	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "SECRET_THOUGHT") {
		t.Fatalf("thought leaked: %s", raw)
	}
}

func TestBuildPublicSharedAgentRun_waitingUsesQuestionNotThoughts(t *testing.T) {
	run := AgentRun{
		Goal:   "Plan a trip",
		Status: AgentStatusWaitingForUser,
		Result: json.RawMessage(`{"kind":"ask_user","question":"What dates?"}`),
	}
	steps := []AgentStep{
		{
			Seq:     1,
			Kind:    AgentStepThought,
			Payload: json.RawMessage(`{"text":"SECRET_THOUGHT"}`),
		},
	}
	out := BuildPublicSharedAgentRun(run, steps)
	if out.Turns[0].Output.Kind != "question" || out.Turns[0].Output.Text != "What dates?" {
		t.Fatalf("output %+v", out.Turns[0].Output)
	}
}

func TestBuildPublicSharedAgentRun_runningHasNoProvisionalThoughts(t *testing.T) {
	run := AgentRun{
		Goal:   "Still working",
		Status: AgentStatusRunning,
	}
	steps := []AgentStep{
		{
			Seq:     1,
			Kind:    AgentStepThought,
			Payload: json.RawMessage(`{"text":"SECRET_THOUGHT"}`),
		},
	}
	out := BuildPublicSharedAgentRun(run, steps)
	if out.Turns[0].Output.Kind != "none" {
		t.Fatalf("running share must not surface thoughts: %+v", out.Turns[0].Output)
	}
}

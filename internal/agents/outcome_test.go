package agents

import (
	"fmt"
	"testing"
)

func TestResolveOutcome(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		result     ToolResult
		handlerErr error
		want       ToolOutcome
	}{
		{name: "explicit succeeded", result: ToolResult{Outcome: OutcomeSucceeded, Content: "Error: ignored"}, want: OutcomeSucceeded},
		{name: "explicit failed", result: ToolResult{Outcome: OutcomeFailed, Content: "ok"}, want: OutcomeFailed},
		{name: "explicit blocked", result: ToolResult{Outcome: OutcomeBlocked, Content: "ok"}, want: OutcomeBlocked},
		{name: "handler error", result: ToolResult{Content: "ok"}, handlerErr: fmt.Errorf("boom"), want: OutcomeFailed},
		{name: "shell exit nonzero", result: ToolResult{Content: "exit=1", Meta: map[string]any{"exit": 1}}, want: OutcomeFailed},
		{name: "shell exit float", result: ToolResult{Content: "exit=2", Meta: map[string]any{"exit": 2.0}}, want: OutcomeFailed},
		{name: "shell exit zero", result: ToolResult{Content: "ok", Meta: map[string]any{"exit": 0}}, want: OutcomeSucceeded},
		{name: "error prefix", result: ToolResult{Content: "Error: unknown tool"}, want: OutcomeFailed},
		{name: "refused prefix", result: ToolResult{Content: "Refused: policy"}, want: OutcomeBlocked},
		{name: "plain success", result: ToolResult{Content: "Plan updated"}, want: OutcomeSucceeded},
		{name: "invalid outcome ignored", result: ToolResult{Outcome: "maybe", Content: "ok"}, want: OutcomeSucceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveOutcome(tc.result, tc.handlerErr)
			if got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

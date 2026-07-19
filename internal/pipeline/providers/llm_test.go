package providers

import (
	"encoding/json"
	"testing"
)

func TestChatRequestBody_addsWebPluginWhenRequested(t *testing.T) {
	llm := NewLLM("test-key", "provider/model", "")

	body, err := llm.chatRequestBody([]ChatMessage{
		{Role: "user", Content: "what happened today?"},
	}, true, ChatCompletionOptions{WebSearch: true, WebSearchMaxResults: 3})
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	plugins, ok := got["plugins"].([]any)
	if !ok || len(plugins) != 1 {
		t.Fatalf("expected one plugin, got %#v", got["plugins"])
	}
	plugin, ok := plugins[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected plugin shape: %#v", plugins[0])
	}
	if plugin["id"] != "web" {
		t.Fatalf("plugin id = %#v, want web", plugin["id"])
	}
	if plugin["max_results"] != float64(3) {
		t.Fatalf("max_results = %#v, want 3", plugin["max_results"])
	}
}

func TestChatRequestBody_omitsWebPluginByDefault(t *testing.T) {
	llm := NewLLM("test-key", "provider/model", "")

	body, err := llm.chatRequestBody([]ChatMessage{
		{Role: "user", Content: "hello"},
	}, true, ChatCompletionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["plugins"]; ok {
		t.Fatalf("plugins should be omitted by default: %#v", got["plugins"])
	}
}

func TestChatRequestBody_includesTools(t *testing.T) {
	llm := NewLLM("test-key", "provider/model", "")
	body, err := llm.chatRequestBody([]ChatMessage{
		{Role: "user", Content: "hello"},
	}, false, ChatCompletionOptions{
		Tools: []ToolDefinition{{
			Type: "function",
			Function: ToolFunctionSchema{
				Name:        "fetch_url",
				Description: "Fetch a page",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	tools, ok := got["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", got["tools"])
	}
	if got["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %#v", got["tool_choice"])
	}
}

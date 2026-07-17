package providers

import (
	"encoding/json"
	"testing"
)

func TestChatRequestBody_addsWebPluginWhenRequested(t *testing.T) {
	llm := NewLLM("test-key", "provider/model", "")

	body, err := llm.chatRequestBody([]ChatMessage{
		{Role: "user", Content: "what happened today?"},
	}, true, ChatCompletionOptions{WebSearch: true, WebSearchMaxResults: 3, ExcludeReasoning: true})
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	if got["max_tokens"] != float64(defaultChatMaxTokens) {
		t.Fatalf("max_tokens = %#v, want %d", got["max_tokens"], defaultChatMaxTokens)
	}
	reasoning, ok := got["reasoning"].(map[string]any)
	if !ok || reasoning["exclude"] != true {
		t.Fatalf("reasoning = %#v, want exclude:true", got["reasoning"])
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
	if _, ok := got["reasoning"]; ok {
		t.Fatalf("reasoning should be omitted unless ExcludeReasoning: %#v", got["reasoning"])
	}
	if got["max_tokens"] != float64(defaultChatMaxTokens) {
		t.Fatalf("max_tokens = %#v, want %d", got["max_tokens"], defaultChatMaxTokens)
	}
}

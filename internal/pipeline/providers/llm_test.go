package providers

import (
	"encoding/json"
	"testing"
	"time"
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

func TestChatRequestBody_normalizesOnlineSuffixForKimi(t *testing.T) {
	llm := NewLLM("test-key", "moonshotai/kimi-k3:online", "")

	body, err := llm.chatRequestBody([]ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}, true, ChatCompletionOptions{WebSearch: true})
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "moonshotai/kimi-k3" {
		t.Fatalf("model = %#v, want moonshotai/kimi-k3", got["model"])
	}
	if _, ok := got["plugins"]; ok {
		t.Fatalf("kimi-k3 must not enable web plugin, got %#v", got["plugins"])
	}

	msgs, ok := got["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages = %#v", got["messages"])
	}
	assistant, ok := msgs[1].(map[string]any)
	if !ok {
		t.Fatalf("assistant message = %#v", msgs[1])
	}
	if _, ok := assistant["reasoning_content"]; !ok {
		t.Fatalf("assistant history missing reasoning_content: %#v", assistant)
	}
}

func TestChatRequestBody_onlineSuffixEnablesWebForNonKimi(t *testing.T) {
	llm := NewLLM("test-key", "deepseek/deepseek-v4-pro:online", "")
	body, err := llm.chatRequestBody([]ChatMessage{
		{Role: "user", Content: "hi"},
	}, true, ChatCompletionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "deepseek/deepseek-v4-pro" {
		t.Fatalf("model = %#v", got["model"])
	}
	plugins, ok := got["plugins"].([]any)
	if !ok || len(plugins) != 1 {
		t.Fatalf("expected web plugin, got %#v", got["plugins"])
	}
}

func TestNewLLM_usesLongTimeoutForReasoningModels(t *testing.T) {
	llm := NewLLM("test-key", "moonshotai/kimi-k3", "")
	if llm.Client == nil || llm.Client.Timeout != 5*time.Minute {
		t.Fatalf("Client.Timeout = %v, want 5m", llm.Client.Timeout)
	}
}

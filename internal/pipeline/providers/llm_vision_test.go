package providers

import "testing"

func TestNewLLMUsesDedicatedVisionModel(t *testing.T) {
	llm := NewLLM("key", "deepseek/deepseek-v4-pro", "z-ai/glm-4.6v")
	if got := llm.visionModel(); got != "z-ai/glm-4.6v" {
		t.Fatalf("visionModel() = %q, want z-ai/glm-4.6v", got)
	}
	if llm.Model != "deepseek/deepseek-v4-pro" {
		t.Fatalf("Model = %q, want deepseek/deepseek-v4-pro", llm.Model)
	}
}

func TestNewLLMFallsBackVisionToChatModel(t *testing.T) {
	llm := NewLLM("key", "deepseek/deepseek-v4-pro", "")
	if got := llm.visionModel(); got != "deepseek/deepseek-v4-pro" {
		t.Fatalf("visionModel() = %q, want deepseek/deepseek-v4-pro", got)
	}
}

func TestWithModelPreservesVisionModel(t *testing.T) {
	llm := NewLLM("key", "deepseek/deepseek-v4-pro", "z-ai/glm-4.6v")
	switched := llm.WithModel("moonshotai/kimi-k2.6")
	if switched.Model != "moonshotai/kimi-k2.6" {
		t.Fatalf("Model = %q, want moonshotai/kimi-k2.6", switched.Model)
	}
	if got := switched.visionModel(); got != "z-ai/glm-4.6v" {
		t.Fatalf("visionModel() = %q, want z-ai/glm-4.6v", got)
	}
}

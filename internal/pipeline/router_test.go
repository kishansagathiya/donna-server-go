package pipeline

import (
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
)

func TestShouldUseFastModel_chitchatAndShort(t *testing.T) {
	if !shouldUseFastModel("hey") {
		t.Fatal("expected chitchat on fast path")
	}
	if !shouldUseFastModel("What time is it?") {
		t.Fatal("expected short prompt on fast path")
	}
}

func TestShouldUseFastModel_complexAndMemory(t *testing.T) {
	if shouldUseFastModel("Compare the pros and cons of these three roadmaps in detail") {
		t.Fatal("expected complex prompt on strong path")
	}
	if shouldUseFastModel("What do you remember about my coffee preferences?") {
		t.Fatal("expected memory prompt on strong path")
	}
}

func TestCountWords(t *testing.T) {
	if got := countWords("one two  three"); got != 3 {
		t.Fatalf("got %d want 3", got)
	}
}

func TestResolveLLMWithPreference_preservesExplicitModelSelection(t *testing.T) {
	e := &Engine{
		Config: &config.Config{
			LLMModel:  "default-model",
			LLMModels: []string{"default-model", "preferred-model"},
		},
		LLM: &providers.LLM{Model: "default-model"},
	}

	llm, route := e.resolveLLMWithPreference("user-1", "hello", " preferred-model ")
	if llm.Model != "preferred-model" {
		t.Fatalf("model = %q, want preferred-model", llm.Model)
	}
	if route.Route != "user" || route.Reason != "explicit_user_preference" {
		t.Fatalf("route = %#v", route)
	}
}

func TestResolveLLMWithPreference_rejectsUnallowlistedModel(t *testing.T) {
	e := &Engine{
		Config: &config.Config{
			LLMModel:  "default-model",
			LLMModels: []string{"default-model"},
		},
		LLM: &providers.LLM{Model: "default-model"},
	}

	llm, route := e.resolveLLMWithPreference("user-1", "hello", "unknown-model")
	if llm.Model != "default-model" {
		t.Fatalf("model = %q, want default-model", llm.Model)
	}
	if route.Route != "default" || route.Reason != "user_model_not_allowlisted" {
		t.Fatalf("route = %#v", route)
	}
}

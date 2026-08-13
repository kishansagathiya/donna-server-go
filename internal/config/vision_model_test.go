package config

import (
	"testing"
)

func TestLoadDefaultsVisionModel(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("DONNA_LLM_MODEL", "deepseek/deepseek-v4-pro")
	t.Setenv("DONNA_VISION_MODEL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.VisionModel != "z-ai/glm-4.6v" {
		t.Fatalf("VisionModel = %q, want z-ai/glm-4.6v", cfg.VisionModel)
	}
}

func TestLoadDefaultsAgentModel(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("DONNA_LLM_MODEL", "z-ai/glm-5.2")
	t.Setenv("DONNA_AGENT_MODEL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.LLMModel != "z-ai/glm-5.2" {
		t.Fatalf("LLMModel = %q, want z-ai/glm-5.2", cfg.LLMModel)
	}
	if cfg.AgentModel != "z-ai/glm-5.2" {
		t.Fatalf("AgentModel = %q, want z-ai/glm-5.2", cfg.AgentModel)
	}
}

func TestLoadRespectsAgentModelEnv(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("DONNA_LLM_MODEL", "z-ai/glm-5.2")
	t.Setenv("DONNA_AGENT_MODEL", "moonshotai/kimi-k3")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.AgentModel != "moonshotai/kimi-k3" {
		t.Fatalf("AgentModel = %q, want moonshotai/kimi-k3", cfg.AgentModel)
	}
}

func TestLoadRespectsVisionModelEnv(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("DONNA_LLM_MODEL", "deepseek/deepseek-v4-pro")
	t.Setenv("DONNA_VISION_MODEL", "moonshotai/kimi-k2.6")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.VisionModel != "moonshotai/kimi-k2.6" {
		t.Fatalf("VisionModel = %q, want moonshotai/kimi-k2.6", cfg.VisionModel)
	}
}

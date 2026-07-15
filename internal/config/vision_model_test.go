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

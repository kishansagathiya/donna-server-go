package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Host                   string
	Port                   int
	SupabaseURL            string
	SupabaseServiceRoleKey string
	JWTAudience            string
	RequireAuth            bool
	PersistConversations   bool
	PersistKnowledge       bool
	OpenRouterAPIKey       string
	OpenAIAPIKey           string
	CartesiaAPIKey         string
	ElevenLabsAPIKey       string
	LLMModel               string
	STTModel               string
	SystemPrompt           string
	MaxHistoryMessages     int
}

func Load() (*Config, error) {
	loadEnv()

	openRouterKey := os.Getenv("OPENROUTER_API_KEY")
	if openRouterKey == "" {
		return nil, fmt.Errorf("missing required env var: OPENROUTER_API_KEY")
	}

	supabaseURL := strings.TrimSuffix(os.Getenv("SUPABASE_URL"), "/")
	supabaseServiceRoleKey := os.Getenv("SUPABASE_SERVICE_ROLE_KEY")

	port := 8787
	if p := os.Getenv("DONNA_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid DONNA_PORT: %w", err)
		}
		port = n
	}

	host := os.Getenv("DONNA_HOST")
	if host == "" {
		host = "0.0.0.0"
	}

	jwtAudience := os.Getenv("JWT_AUDIENCE")
	if jwtAudience == "" {
		jwtAudience = "authenticated"
	}

	llmModel := os.Getenv("DONNA_LLM_MODEL")
	if llmModel == "" {
		llmModel = "deepseek/deepseek-v4-pro"
	}

	sttModel := os.Getenv("DONNA_STT_MODEL")
	if sttModel == "" {
		sttModel = "mistralai/voxtral-mini-transcribe"
	}

	systemPrompt := os.Getenv("DONNA_SYSTEM_PROMPT")
	if systemPrompt == "" {
		systemPrompt = "You are Donna, a warm and concise voice assistant. Keep replies short and conversational — one or two sentences unless the user asks for detail. Never ask the user to repeat themselves. If input is unclear, give your best short guess or a brief helpful reply."
	}

	return &Config{
		Host:                   host,
		Port:                   port,
		SupabaseURL:            supabaseURL,
		SupabaseServiceRoleKey: supabaseServiceRoleKey,
		JWTAudience:            jwtAudience,
		RequireAuth:            supabaseURL != "",
		PersistConversations:   supabaseURL != "" && supabaseServiceRoleKey != "",
		PersistKnowledge:       supabaseURL != "" && supabaseServiceRoleKey != "",
		OpenRouterAPIKey:       openRouterKey,
		OpenAIAPIKey:           os.Getenv("OPENAI_API_KEY"),
		CartesiaAPIKey:         os.Getenv("CARTESIA_API_KEY"),
		ElevenLabsAPIKey:       os.Getenv("ELEVENLABS_API_KEY"),
		LLMModel:               llmModel,
		STTModel:               sttModel,
		SystemPrompt:           systemPrompt,
		MaxHistoryMessages:     20,
	}, nil
}

func loadEnv() {
	candidates := []string{
		filepath.Join("..", ".env"),
		".env",
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			_ = godotenv.Load(path)
			return
		}
	}
}

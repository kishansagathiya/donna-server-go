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
	// AgentModel is the OpenRouter slug for cloud agent runs. Chat uses LLMModel.
	AgentModel         string
	LLMFastModel       string
	LLMModels          []string
	AutoRouteEnabled   bool
	VisionModel        string
	STTModel           string
	EmbeddingModel     string
	SystemPrompt       string
	MemoryMinScore     float64
	MaxHistoryMessages int
	Personas           []string
	// BrowserURL is the donna-browser Playwright sidecar base URL (e.g. http://127.0.0.1:9229).
	// When empty, browse_page is not registered; fetch_url still works.
	BrowserURL string
	// ChatToolsEnabled controls mid-turn tools (fetch_url / browse_page). Default true.
	ChatToolsEnabled bool

	// Integrations feature flags (Granola + Google connectors).
	IntegrationsEnabled bool
	GranolaEnabled      bool
	GoogleEnabled       bool
	// Google OAuth web client credentials (Calendar write). Required when GoogleEnabled.
	GoogleOAuthClientID     string
	GoogleOAuthClientSecret string
	// ConnectorEncryptionKey is base64 of 32 bytes for AES-256-GCM. Required to enable connectors.
	ConnectorEncryptionKey string
	// PublicAPIBase is the externally reachable API origin used for OAuth redirect_uri.
	PublicAPIBase string
	// WebAppBase is the web app origin for post-OAuth redirects (https://donnadoesit.com).
	WebAppBase string

	// Notes & Memory V2 rollout flags (defaults; per-user overrides in user_preferences).
	NotesV2Feed         bool
	NotesV2SmartTagging bool
	MemoryV2Extraction  bool
	MemoryV2Retrieval   bool
	// BackgroundJobsEnabled runs the durable background_jobs poller.
	BackgroundJobsEnabled bool
	// CloudAgentsEnabled enables long-running per-user agent harness + /agent-runs APIs.
	CloudAgentsEnabled bool
	// AgentSkillsEnabled enables agent skills: /skills CRUD + load_skill/save_skill tools.
	AgentSkillsEnabled bool

	// ErrorReportsEnabled turns server/client errors into GitHub issues.
	ErrorReportsEnabled bool
	// GitHubToken is a fine-grained PAT with Issues: Read & Write on GitHubIssueRepo.
	GitHubToken string
	// GitHubIssueRepo is "owner/name" of the repo issues are filed in.
	GitHubIssueRepo string

	// Gemini Live (realtime Voice harness). Optional — /voice/live requires GeminiAPIKey.
	GeminiAPIKey  string
	LiveModel     string
	LiveVoiceName string

	// ChatGPT import object storage (Railway Buckets / S3-compatible).
	// When unset, ChatGPT ZIP import is disabled.
	ChatGPTImportS3Bucket          string
	ChatGPTImportS3Endpoint        string
	ChatGPTImportS3Region          string
	ChatGPTImportS3AccessKeyID     string
	ChatGPTImportS3SecretAccessKey string
	ChatGPTImportS3UsePathStyle    bool
}

const (
	defaultWebAppBase = "https://donnadoesit.com"
	// legacyRailwayWebAppBase was the temporary Railway hostname before the custom domain.
	legacyRailwayWebAppBase = "https://donna-web-production-3d4a.up.railway.app"
)

func Load() (*Config, error) {
	loadEnv()

	openRouterKey := os.Getenv("OPENROUTER_API_KEY")
	if openRouterKey == "" {
		return nil, fmt.Errorf("missing required env var: OPENROUTER_API_KEY")
	}

	supabaseURL := strings.TrimSuffix(os.Getenv("SUPABASE_URL"), "/")
	supabaseServiceRoleKey := os.Getenv("SUPABASE_SERVICE_ROLE_KEY")

	port := 8787
	// Platform PORT (Railway/Heroku) always wins; DONNA_PORT is for local dev only.
	if p := os.Getenv("PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid PORT: %w", err)
		}
		port = n
	} else if p := os.Getenv("DONNA_PORT"); p != "" {
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

	llmModel := strings.TrimSpace(os.Getenv("DONNA_LLM_MODEL"))
	if llmModel == "" {
		return nil, fmt.Errorf("missing required env var: DONNA_LLM_MODEL")
	}
	agentModel := strings.TrimSpace(os.Getenv("DONNA_AGENT_MODEL"))
	if agentModel == "" {
		agentModel = "z-ai/glm-5.2"
	}
	llmModels := parseModelList(os.Getenv("DONNA_LLM_MODELS"))
	if !containsString(llmModels, llmModel) {
		llmModels = append([]string{llmModel}, llmModels...)
	}

	visionModel := strings.TrimSpace(os.Getenv("DONNA_VISION_MODEL"))
	if visionModel == "" {
		visionModel = "z-ai/glm-4.6v"
	}

	sttModel := os.Getenv("DONNA_STT_MODEL")
	if sttModel == "" {
		sttModel = "mistralai/voxtral-mini-transcribe"
	}

	embeddingModel := strings.TrimSpace(os.Getenv("DONNA_EMBEDDING_MODEL"))
	if embeddingModel == "" {
		embeddingModel = "text-embedding-3-small"
	}

	systemPrompt := os.Getenv("DONNA_SYSTEM_PROMPT")
	if systemPrompt == "" {
		systemPrompt = "You are Donna, a sharp and thoughtful second-brain companion for text and voice. Give the best answer you can — accurate, specific, and genuinely useful. Default to 2–4 sentences for simple questions; go longer when the topic needs it or the user asks you to explain, compare, or go deeper. For spoken replies, prefer 1–2 short sentences unless the user asks for depth. Be warm and direct, not robotic. Use what you know about this user when it's relevant; don't force personal details into every reply."
	}

	memoryMinScore := 0.35
	if raw := strings.TrimSpace(os.Getenv("DONNA_MEMORY_MIN_SCORE")); raw != "" {
		if n, err := strconv.ParseFloat(raw, 64); err == nil && n >= 0 {
			memoryMinScore = n
		}
	}

	maxHistoryMessages := 20
	if raw := strings.TrimSpace(os.Getenv("DONNA_MAX_HISTORY_MESSAGES")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			maxHistoryMessages = n
		}
	}

	llmFastModel := strings.TrimSpace(os.Getenv("DONNA_LLM_FAST_MODEL"))
	autoRoute := false
	if raw := strings.TrimSpace(os.Getenv("DONNA_LLM_AUTO_ROUTE")); raw != "" {
		autoRoute = parseBool(raw)
	}
	if llmFastModel != "" && !containsString(llmModels, llmFastModel) {
		llmModels = append(llmModels, llmFastModel)
	}

	personas := parseModelList(os.Getenv("DONNA_PERSONAS"))
	if len(personas) == 0 {
		personas = []string{"companion", "boss", "coach", "listener", "custom"}
	}
	if !containsString(personas, "companion") {
		personas = append([]string{"companion"}, personas...)
	}

	browserURL := strings.TrimSpace(os.Getenv("DONNA_BROWSER_URL"))
	chatToolsEnabled := true
	if raw := strings.TrimSpace(os.Getenv("DONNA_CHAT_TOOLS")); raw != "" {
		chatToolsEnabled = parseBool(raw)
	}

	integrationsEnabled := parseBool(os.Getenv("DONNA_INTEGRATIONS_ENABLED"))
	granolaEnabled := parseBool(os.Getenv("DONNA_GRANOLA_ENABLED"))
	googleEnabled := parseBool(os.Getenv("DONNA_GOOGLE_ENABLED"))
	googleClientID := strings.TrimSpace(os.Getenv("DONNA_GOOGLE_OAUTH_CLIENT_ID"))
	googleClientSecret := strings.TrimSpace(os.Getenv("DONNA_GOOGLE_OAUTH_CLIENT_SECRET"))
	connectorKey := strings.TrimSpace(os.Getenv("DONNA_CONNECTOR_ENCRYPTION_KEY"))
	publicAPIBase := strings.TrimSpace(os.Getenv("DONNA_PUBLIC_API_BASE"))
	if publicAPIBase == "" {
		publicAPIBase = strings.TrimSpace(os.Getenv("DONNA_API_BASE"))
	}
	webAppBase := resolveWebAppBase(os.Getenv("DONNA_WEB_APP_BASE"))

	notesV2Feed := parseBool(os.Getenv("DONNA_NOTES_V2_FEED"))
	notesV2SmartTagging := parseBool(os.Getenv("DONNA_NOTES_V2_SMART_TAGGING"))
	// Memory V2 extract/retrieve + background jobs default on when unset.
	memoryV2Extraction := parseBoolDefault(os.Getenv("DONNA_MEMORY_V2_EXTRACTION"), true)
	memoryV2Retrieval := parseBoolDefault(os.Getenv("DONNA_MEMORY_V2_RETRIEVAL"), true)
	backgroundJobsEnabled := parseBoolDefault(os.Getenv("DONNA_BACKGROUND_JOBS"), true)
	cloudAgentsEnabled := parseBoolDefault(os.Getenv("DONNA_CLOUD_AGENTS"), true)
	agentSkillsEnabled := parseBoolDefault(os.Getenv("DONNA_AGENT_SKILLS"), true)

	errorReportsEnabled := parseBool(os.Getenv("DONNA_ERROR_REPORTS_ENABLED"))
	githubIssueRepo := strings.TrimSpace(os.Getenv("DONNA_GITHUB_ISSUE_REPO"))
	if githubIssueRepo == "" {
		githubIssueRepo = "kishansagathiya/donna"
	}

	liveModel := strings.TrimSpace(os.Getenv("DONNA_LIVE_MODEL"))
	if liveModel == "" {
		liveModel = "gemini-2.5-flash-native-audio-preview-12-2025"
	}
	liveVoice := strings.TrimSpace(os.Getenv("DONNA_LIVE_VOICE_NAME"))
	if liveVoice == "" {
		liveVoice = "Aoede"
	}

	chatgptS3Bucket := strings.TrimSpace(os.Getenv("CHATGPT_IMPORT_S3_BUCKET"))
	chatgptS3Endpoint := firstNonEmpty(
		os.Getenv("CHATGPT_IMPORT_S3_ENDPOINT"),
		os.Getenv("AWS_ENDPOINT_URL"),
		"https://storage.railway.app",
	)
	chatgptS3Region := firstNonEmpty(
		os.Getenv("CHATGPT_IMPORT_S3_REGION"),
		os.Getenv("AWS_REGION"),
		"auto",
	)
	chatgptS3AccessKey := firstNonEmpty(
		os.Getenv("CHATGPT_IMPORT_S3_ACCESS_KEY_ID"),
		os.Getenv("AWS_ACCESS_KEY_ID"),
	)
	chatgptS3Secret := firstNonEmpty(
		os.Getenv("CHATGPT_IMPORT_S3_SECRET_ACCESS_KEY"),
		os.Getenv("AWS_SECRET_ACCESS_KEY"),
	)
	chatgptS3PathStyle := parseBool(os.Getenv("CHATGPT_IMPORT_S3_USE_PATH_STYLE"))

	return &Config{
		Host:                           host,
		Port:                           port,
		SupabaseURL:                    supabaseURL,
		SupabaseServiceRoleKey:         supabaseServiceRoleKey,
		JWTAudience:                    jwtAudience,
		RequireAuth:                    supabaseURL != "",
		PersistConversations:           supabaseURL != "" && supabaseServiceRoleKey != "",
		PersistKnowledge:               supabaseURL != "" && supabaseServiceRoleKey != "",
		OpenRouterAPIKey:               openRouterKey,
		OpenAIAPIKey:                   os.Getenv("OPENAI_API_KEY"),
		CartesiaAPIKey:                 os.Getenv("CARTESIA_API_KEY"),
		ElevenLabsAPIKey:               os.Getenv("ELEVENLABS_API_KEY"),
		LLMModel:                       llmModel,
		AgentModel:                     agentModel,
		LLMFastModel:                   llmFastModel,
		LLMModels:                      llmModels,
		AutoRouteEnabled:               autoRoute,
		VisionModel:                    visionModel,
		STTModel:                       sttModel,
		EmbeddingModel:                 embeddingModel,
		SystemPrompt:                   systemPrompt,
		MemoryMinScore:                 memoryMinScore,
		MaxHistoryMessages:             maxHistoryMessages,
		Personas:                       personas,
		BrowserURL:                     browserURL,
		ChatToolsEnabled:               chatToolsEnabled,
		IntegrationsEnabled:            integrationsEnabled,
		GranolaEnabled:                 granolaEnabled,
		GoogleEnabled:                  googleEnabled,
		GoogleOAuthClientID:            googleClientID,
		GoogleOAuthClientSecret:        googleClientSecret,
		ConnectorEncryptionKey:         connectorKey,
		PublicAPIBase:                  publicAPIBase,
		WebAppBase:                     webAppBase,
		NotesV2Feed:                    notesV2Feed,
		NotesV2SmartTagging:            notesV2SmartTagging,
		MemoryV2Extraction:             memoryV2Extraction,
		MemoryV2Retrieval:              memoryV2Retrieval,
		BackgroundJobsEnabled:          backgroundJobsEnabled,
		CloudAgentsEnabled:             cloudAgentsEnabled,
		AgentSkillsEnabled:             agentSkillsEnabled,
		ErrorReportsEnabled:            errorReportsEnabled,
		GitHubToken:                    strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
		GitHubIssueRepo:                githubIssueRepo,
		GeminiAPIKey:                   strings.TrimSpace(os.Getenv("GEMINI_API_KEY")),
		LiveModel:                      liveModel,
		LiveVoiceName:                  liveVoice,
		ChatGPTImportS3Bucket:          chatgptS3Bucket,
		ChatGPTImportS3Endpoint:        chatgptS3Endpoint,
		ChatGPTImportS3Region:          chatgptS3Region,
		ChatGPTImportS3AccessKeyID:     chatgptS3AccessKey,
		ChatGPTImportS3SecretAccessKey: chatgptS3Secret,
		ChatGPTImportS3UsePathStyle:    chatgptS3PathStyle,
	}, nil
}

func parseBool(raw string) bool {
	return parseBoolDefault(raw, false)
}

// parseBoolDefault treats empty/unset as def; explicit false/0/no/off disable.
func parseBoolDefault(raw string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// resolveWebAppBase returns the origin used after Granola (and future) OAuth.
// Empty or legacy Railway web hosts fall back to the public DonnaDoesIt.com domain
// so users are not bounced to *.up.railway.app after approving an integration.
func resolveWebAppBase(raw string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	if base == "" {
		return defaultWebAppBase
	}
	lower := strings.ToLower(base)
	if lower == legacyRailwayWebAppBase ||
		(strings.HasSuffix(lower, ".up.railway.app") && strings.Contains(lower, "donna-web")) {
		return defaultWebAppBase
	}
	return base
}

func parseModelList(raw string) []string {
	var models []string
	for _, value := range strings.Split(raw, ",") {
		model := strings.TrimSpace(value)
		if model != "" && !containsString(models, model) {
			models = append(models, model)
		}
	}
	return models
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
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

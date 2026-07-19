package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/kishansagathiya/donna/donna-server-go/internal/account"
	"github.com/kishansagathiya/donna/donna-server-go/internal/actions"
	"github.com/kishansagathiya/donna/donna-server-go/internal/apidocs"
	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/chat"
	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/conversations"
	"github.com/kishansagathiya/donna/donna-server-go/internal/intents"
	"github.com/kishansagathiya/donna/donna-server-go/internal/knowledge"
	ingestpkg "github.com/kishansagathiya/donna/donna-server-go/internal/knowledge/ingest"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/memory"
	appmiddleware "github.com/kishansagathiya/donna/donna-server-go/internal/middleware"
	"github.com/kishansagathiya/donna/donna-server-go/internal/notes"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
	"github.com/kishansagathiya/donna/donna-server-go/internal/voice"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	authCfg := appauth.Config{
		SupabaseURL: cfg.SupabaseURL,
		JWTAudience: cfg.JWTAudience,
	}

	supa := storage.NewSupabase(cfg.SupabaseURL, cfg.SupabaseServiceRoleKey)
	embeddings := providers.NewEmbeddings(cfg.OpenAIAPIKey, cfg.EmbeddingModel)
	kbStore := &storage.Knowledge{DB: supa, Enabled: cfg.PersistKnowledge, Embedder: embeddings}
	notesStore := &storage.Notes{DB: supa, Enabled: cfg.PersistKnowledge, Embedder: embeddings}
	convStore := &storage.Conversations{DB: supa, Enabled: cfg.PersistConversations}
	preferencesStore := &storage.Preferences{DB: supa, Enabled: supa.Enabled()}
	actionsStore := &storage.ActionsStore{DB: supa, Enabled: supa.Enabled()}

	stt := providers.NewSTT(cfg.OpenRouterAPIKey, cfg.STTModel)
	llm := providers.NewLLM(cfg.OpenRouterAPIKey, cfg.LLMModel, cfg.VisionModel)
	convStore.TitleGen = &conversations.LLMTitleGenerator{LLM: llm}
	ingestpkg.InitExtractors(ingestpkg.Services{STT: stt, LLM: llm})

	noteIndexer := &notes.Indexer{Store: notesStore, LLM: llm}
	noteIndexQueue := notes.NewIndexQueue(noteIndexer)

	actionExecutor := &actions.Executor{Store: actionsStore, Builtin: &actions.BuiltinRunner{}}
	actionMatcher := &actions.Matcher{Store: actionsStore, Executor: actionExecutor, AutoInternal: false}
	intentExtractor := &intents.Extractor{Store: actionsStore, LLM: llm, Matcher: actionMatcher}
	intentQueue := intents.NewQueue(intentExtractor)

	noteSync := &notes.Sync{Store: notesStore, Queue: noteIndexQueue, Intents: intentQueue}
	convStore.OnTurnPersisted = func(input storage.SaveTurnInput) {
		intentQueue.EnqueueConversationTurn(input.UserID, input.ConversationID, input.TurnIndex, input.UserTranscript)
	}

	compiler := &knowledge.Compiler{KB: kbStore, LLM: llm, Notes: noteSync}
	compileQueue := knowledge.NewQueue(kbStore, compiler)

	chatTools := tools.DefaultRegistry(cfg.BrowserURL)
	if cfg.ChatToolsEnabled {
		log.Print("chat tools enabled", map[string]any{
			"count":      chatTools.Len(),
			"browserUrl": cfg.BrowserURL != "",
		})
	}

	engine := &pipeline.Engine{
		Config:      cfg,
		STT:         stt,
		LLM:         llm,
		TTS:         providers.NewTTS(cfg.OpenAIAPIKey, cfg.CartesiaAPIKey, cfg.ElevenLabsAPIKey),
		KB:          kbStore,
		Notes:       notesStore,
		Preferences: preferencesStore,
		Tools:       chatTools,
	}

	r := chi.NewRouter()
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Logger)
	r.Use(appmiddleware.CORS)

	apidocs.Register(r)

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"service":      "donna-server-go",
			"authRequired": cfg.RequireAuth,
		})
	})

	r.Get("/knowledge/formats", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, knowledge.SupportedFormats())
	})

	ingestHandler := &knowledge.IngestHandler{KB: kbStore, Queue: compileQueue, Notes: noteSync}
	r.With(appauth.RequireAuth(appauth.MiddlewareConfig{
		RequireAuth: cfg.RequireAuth,
		Auth:        authCfg,
	})).Post("/knowledge/ingest", ingestHandler.ServeHTTP)

	dailyChecker := &notes.DailyChecker{Store: notesStore, LLM: llm, Conversations: convStore}
	notesHandler := &notes.Handler{Store: notesStore, Sync: noteSync, Daily: dailyChecker, KB: kbStore, Intents: intentQueue}
	authMiddleware := appauth.RequireAuth(appauth.MiddlewareConfig{
		RequireAuth: cfg.RequireAuth,
		Auth:        authCfg,
	})
	notes.RegisterRoutes(r, authMiddleware, notesHandler)

	actionsHandler := &actions.Handler{Store: actionsStore, Executor: actionExecutor}
	actions.RegisterRoutes(r, authMiddleware, actionsHandler)

	memoryHandler := &memory.Handler{KB: kbStore}
	memory.RegisterRoutes(r, authMiddleware, memoryHandler)

	accountHandler := &account.Handler{
		Deleter:      &account.Deleter{DB: supa},
		Exporter:     &account.Exporter{DB: supa},
		Preferences:  preferencesStore,
		Models:       cfg.LLMModels,
		DefaultModel: cfg.LLMModel,
		Personas:     cfg.Personas,
	}
	accountRoutes := r.With(appauth.RequireAuth(appauth.MiddlewareConfig{
		RequireAuth: cfg.RequireAuth,
		Auth:        authCfg,
	}))
	accountRoutes.Get("/account", accountHandler.ServeHTTP)
	accountRoutes.Patch("/account", accountHandler.ServeHTTP)
	accountRoutes.Delete("/account", accountHandler.ServeHTTP)
	accountRoutes.Get("/account/export", accountHandler.Export)

	chatHandler := &chat.Handler{Engine: engine, Conversations: convStore, Notes: noteSync}
	r.With(appauth.RequireAuth(appauth.MiddlewareConfig{
		RequireAuth: cfg.RequireAuth,
		Auth:        authCfg,
	})).Post("/chat", chatHandler.ServeHTTP)

	conversationsHandler := &conversations.Handler{Store: convStore}
	conversations.RegisterRoutes(r, authMiddleware, conversationsHandler)

	voiceHandler := &voice.Handler{
		Config:        cfg,
		Auth:          authCfg,
		Engine:        engine,
		Conversations: convStore,
		Queue:         compileQueue,
		Notes:         noteSync,
	}
	r.Get("/voice", voiceHandler.ServeHTTP)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	server := &http.Server{Addr: addr, Handler: r}

	log.Print(fmt.Sprintf("listening on http://%s", addr), nil)
	log.Print(fmt.Sprintf("health: http://127.0.0.1:%d/health", cfg.Port), nil)
	if os.Getenv("PORT") != "" {
		log.Print(fmt.Sprintf("using PORT=%s for HTTP listener", os.Getenv("PORT")), nil)
	}
	log.Print(fmt.Sprintf("api docs: http://127.0.0.1:%d/docs", cfg.Port), nil)
	if cfg.RequireAuth {
		log.Print("voice auth: required (Supabase JWT)", nil)
	} else {
		log.Print("voice auth: disabled", nil)
	}
	if cfg.PersistConversations {
		log.Print("conversation persistence: enabled", nil)
	} else {
		log.Print("conversation persistence: disabled", nil)
	}
	if cfg.PersistKnowledge {
		log.Print("knowledge base: enabled", nil)
	} else {
		log.Print("knowledge base: disabled", nil)
	}
	log.Print("knowledge ingest: POST /knowledge/ingest, GET /knowledge/formats", nil)
	log.Print("notes: GET /notes/search, POST /notes/daily-check, web-only CRUD at /notes/*", nil)
	log.Print("intents: GET /intents, POST /intents/{id}/dismiss", nil)
	log.Print("action-runs: GET /action-runs, POST /action-runs/{id}/confirm|cancel", nil)
	log.Print("memory: GET/PATCH /memory/profile, CRUD at /memory/facts", nil)
	log.Print("chat: POST /chat (text, optional ?stream=1 for SSE)", nil)
	log.Print("conversations: GET /conversations, GET /conversations/{id}", nil)
	log.Print("account: GET/PATCH/DELETE /account, GET /account/export", nil)
	log.Print(fmt.Sprintf("llm model: %s", cfg.LLMModel), nil)
	log.Print(fmt.Sprintf("vision model: %s", cfg.VisionModel), nil)
	log.Print(fmt.Sprintf("stt model: %s", cfg.STTModel), nil)
	log.Print(fmt.Sprintf("voice (simulator): ws://127.0.0.1:%d/voice", cfg.Port), nil)
	for _, ip := range lanAddresses() {
		log.Print(fmt.Sprintf("voice (physical device): ws://%s:%d/voice", ip, cfg.Port), nil)
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func lanAddresses() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var addrs []string
	for _, iface := range ifaces {
		inetAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range inetAddrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil || ipNet.IP.IsLoopback() {
				continue
			}
			addrs = append(addrs, ipNet.IP.String())
		}
	}
	return addrs
}

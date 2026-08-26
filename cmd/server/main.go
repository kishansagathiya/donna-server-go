package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	_ "time/tzdata" // embed IANA zones for LoadLocation in Alpine/scratch images

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/kishansagathiya/donna/donna-server-go/internal/account"
	"github.com/kishansagathiya/donna/donna-server-go/internal/actions"
	"github.com/kishansagathiya/donna/donna-server-go/internal/agents"
	"github.com/kishansagathiya/donna/donna-server-go/internal/apidocs"
	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/cafe"
	"github.com/kishansagathiya/donna/donna-server-go/internal/chat"
	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/connectors"
	"github.com/kishansagathiya/donna/donna-server-go/internal/connectors/google"
	"github.com/kishansagathiya/donna/donna-server-go/internal/connectors/granola"
	"github.com/kishansagathiya/donna/donna-server-go/internal/conversations"
	"github.com/kishansagathiya/donna/donna-server-go/internal/employees"
	"github.com/kishansagathiya/donna/donna-server-go/internal/errreport"
	"github.com/kishansagathiya/donna/donna-server-go/internal/featureflags"
	chatgptimport "github.com/kishansagathiya/donna/donna-server-go/internal/imports/chatgpt"
	"github.com/kishansagathiya/donna/donna-server-go/internal/intents"
	"github.com/kishansagathiya/donna/donna-server-go/internal/jobs"
	"github.com/kishansagathiya/donna/donna-server-go/internal/knowledge"
	ingestpkg "github.com/kishansagathiya/donna/donna-server-go/internal/knowledge/ingest"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/memory"
	appmiddleware "github.com/kishansagathiya/donna/donna-server-go/internal/middleware"
	"github.com/kishansagathiya/donna/donna-server-go/internal/notes"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
	"github.com/kishansagathiya/donna/donna-server-go/internal/reminders"
	"github.com/kishansagathiya/donna/donna-server-go/internal/schedules"
	"github.com/kishansagathiya/donna/donna-server-go/internal/skills"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
	ttspkg "github.com/kishansagathiya/donna/donna-server-go/internal/tts"
	"github.com/kishansagathiya/donna/donna-server-go/internal/voice"
	"github.com/kishansagathiya/donna/donna-server-go/internal/voicelive"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	reporter := errreport.New(errreport.Config{
		Enabled: cfg.ErrorReportsEnabled,
		Token:   cfg.GitHubToken,
		Repo:    cfg.GitHubIssueRepo,
	})
	log.SetErrorHook(func(message string, fields map[string]any) {
		report := errreport.Report{Source: "server", Context: map[string]string{}}
		for k, v := range fields {
			s := fmt.Sprint(v)
			switch k {
			case "error":
				message += ": " + s
			case "stack":
				report.Stack = s
			case "path":
				report.Route = s
			default:
				report.Context[k] = s
			}
		}
		report.Message = message
		reporter.Report(report)
	})

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
	agentsStore := &storage.AgentsStore{DB: supa, Enabled: cfg.CloudAgentsEnabled && supa.Enabled()}
	jobStore := &storage.BackgroundJobs{DB: supa, Enabled: cfg.BackgroundJobsEnabled && supa.Enabled()}
	chatgptImportStore := &storage.ChatGPTImports{DB: supa, Enabled: supa.Enabled()}
	var chatgptBlobs storage.ImportBlobStore
	if cfg.ChatGPTImportS3Bucket != "" && cfg.ChatGPTImportS3AccessKeyID != "" && cfg.ChatGPTImportS3SecretAccessKey != "" {
		store, err := storage.NewS3BlobStore(storage.S3BlobStoreConfig{
			Bucket:          cfg.ChatGPTImportS3Bucket,
			Endpoint:        cfg.ChatGPTImportS3Endpoint,
			Region:          cfg.ChatGPTImportS3Region,
			AccessKeyID:     cfg.ChatGPTImportS3AccessKeyID,
			SecretAccessKey: cfg.ChatGPTImportS3SecretAccessKey,
			UsePathStyle:    cfg.ChatGPTImportS3UsePathStyle,
		})
		if err != nil {
			log.Warn("chatgpt import s3 store init failed", map[string]any{"error": err.Error()})
		} else {
			chatgptBlobs = store
			log.Print("chatgpt import: Railway/S3 blob store enabled", map[string]any{
				"bucket":   store.Bucket(),
				"endpoint": cfg.ChatGPTImportS3Endpoint,
			})
		}
	} else {
		log.Print("chatgpt import: blob store not configured (set CHATGPT_IMPORT_S3_BUCKET + credentials)", nil)
	}

	var bgWorker *jobs.Worker
	flagResolver := &featureflags.Resolver{
		Defaults: cfg,
		Store:    &storage.FeatureFlags{DB: supa, Enabled: supa.Enabled()},
	}
	log.Print("notes & memory v2 defaults", map[string]any{
		"notesFeed":        cfg.NotesV2Feed,
		"smartTagging":     cfg.NotesV2SmartTagging,
		"memoryExtraction": cfg.MemoryV2Extraction,
		"memoryRetrieval":  cfg.MemoryV2Retrieval,
		"backgroundJobs":   cfg.BackgroundJobsEnabled,
		"cloudAgents":      cfg.CloudAgentsEnabled,
	})

	stt := providers.NewSTT(cfg.OpenRouterAPIKey, cfg.STTModel)
	llm := providers.NewLLM(cfg.OpenRouterAPIKey, cfg.LLMModel, cfg.VisionModel)
	memoryEnqueuer := &memory.Enqueuer{Jobs: jobStore, Flags: flagResolver}
	memoryExtractor := &memory.Extractor{KB: kbStore, Notes: notesStore, LLM: llm, Flags: flagResolver}
	noteIndexer := &notes.Indexer{Store: notesStore, LLM: llm}
	noteIndexQueue := notes.NewIndexQueue(noteIndexer)
	chatgptImportWorker := &chatgptimport.Worker{
		Imports: chatgptImportStore,
		KB:      kbStore,
		Jobs:    jobStore,
		Memory:  memoryEnqueuer,
		Blobs:   chatgptBlobs,
	}

	memBridge := &agents.MemoryBridge{
		Retriever: &memory.Retriever{KB: kbStore, Notes: notesStore},
		MinScore:  cfg.MemoryMinScore,
	}
	notesBridge := &agents.NotesBridge{Notes: notesStore}
	skillsStore := &storage.SkillsStore{DB: supa, Enabled: cfg.AgentSkillsEnabled && cfg.CloudAgentsEnabled && supa.Enabled()}
	var skillProvider *skills.Provider
	if skillsStore.Enabled {
		skillProvider = &skills.Provider{Store: skillsStore}
	}
	var agentSkillProv agents.SkillProvider
	if skillProvider != nil {
		agentSkillProv = skillProvider
	}
	employeesStore := &storage.EmployeesStore{DB: supa, Enabled: cfg.CloudAgentsEnabled && supa.Enabled()}
	schedulesStore := &storage.SchedulesStore{DB: supa, Enabled: cfg.CloudAgentsEnabled && supa.Enabled()}
	remindersStore := &storage.RemindersStore{DB: supa, Enabled: supa.Enabled()}
	reminderService := &reminders.Service{Store: remindersStore, Preferences: preferencesStore}
	var employeeWriter agents.EmployeeProgressWriter
	if employeesStore.Enabled {
		employeeWriter = employeesStore
	}
	var calendarProposer agents.CalendarProposer
	if actionsStore.Enabled {
		calendarProposer = &agents.ActionsCalendarProposer{Store: actionsStore}
	}
	var factWriter agents.FactWriter
	if kbStore.Enabled {
		factWriter = kbStore
	}
	var reminderCreator agents.ReminderCreator
	if remindersStore.Enabled {
		reminderCreator = reminderService
	}
	agentRegistry := agents.DefaultToolsets(memBridge, notesBridge, cfg.BrowserURL, agentSkillProv, employeeWriter, agents.Phase3Tools{
		Facts:     factWriter,
		Calendar:  calendarProposer,
		Reminders: reminderCreator,
	})
	if cfg.CloudAgentsEnabled {
		log.Print("cloud agents tools", map[string]any{
			"count":      agentRegistry.Len(),
			"browserUrl": cfg.BrowserURL != "",
			"skills":     skillProvider != nil,
			"employees":  employeesStore.Enabled,
			"schedules":  schedulesStore.Enabled,
			"facts":      factWriter != nil,
			"calendar":   calendarProposer != nil,
		})
	}
	agentHarness := &agents.Harness{
		Store:    agentsStore,
		LLM:      llm.WithModel(cfg.AgentModel),
		Registry: agentRegistry,
		WorkerID: "donna-server",
	}
	agentSpawner := &agents.Spawner{Store: agentsStore, Jobs: jobStore, Mem: memBridge, Skills: agentSkillProv}
	agentRegistry.Register(agents.DelegateTaskTool(agentSpawner))
	employeeService := &employees.Service{
		Store:   employeesStore,
		Agents:  agentsStore,
		Spawner: agentSpawner,
		Jobs:    jobStore,
	}
	scheduleService := &schedules.Service{
		Store:   schedulesStore,
		Agents:  agentsStore,
		Spawner: agentSpawner,
	}
	agentWorker := &agents.Worker{
		Store:   agentsStore,
		Harness: agentHarness,
		AfterRun: func(ctx context.Context, run storage.AgentRun) {
			employeeService.AfterAgentRun(ctx, run)
			scheduleService.AfterAgentRun(ctx, run)
		},
	}
	employeeScheduler := &employees.Scheduler{Service: employeeService}
	scheduleScheduler := &schedules.Scheduler{Service: scheduleService}

	if cfg.BackgroundJobsEnabled {
		enricher := &notes.SmartTagEnricher{Store: notesStore, LLM: llm, Flags: flagResolver}
		handlers := map[string]jobs.Handler{
			storage.JobTypeNoteEnrich:          noteIndexer.HandleJob,
			storage.JobTypeSmartTagEnrich:      enricher.HandleJob,
			storage.JobTypeMemoryExtract:       memoryExtractor.HandleJob,
			storage.JobTypeChatGPTExportImport: chatgptImportWorker.HandleJob,
		}
		if cfg.CloudAgentsEnabled {
			handlers[storage.JobTypeAgentRun] = agentWorker.HandleJob
			handlers[storage.JobTypeEmployeeShift] = employeeService.HandleShiftJob
		}
		bgWorker = &jobs.Worker{
			Store:    jobStore,
			Handlers: handlers,
		}
	}

	convStore.TitleGen = &conversations.LLMTitleGenerator{LLM: llm}
	ingestpkg.InitExtractors(ingestpkg.Services{STT: stt, LLM: llm})
	ingestpkg.SetBrowserBaseURL(cfg.BrowserURL)

	actionExecutor := &actions.Executor{Store: actionsStore, Builtin: &actions.BuiltinRunner{}, Reminders: reminderService}
	actionExecutor.ResumeAgent = func(ctx context.Context, userID, runID, note string) error {
		_, err := agents.ResumeAfterApproval(ctx, agentsStore, jobStore, userID, runID, note)
		return err
	}
	actionMatcher := &actions.Matcher{Store: actionsStore, Executor: actionExecutor, Preferences: preferencesStore, AutoInternal: false, Agents: agentSpawner}
	intentExtractor := &intents.Extractor{Store: actionsStore, LLM: llm, Matcher: actionMatcher}
	intentQueue := intents.NewQueue(intentExtractor)
	agentHarness.Approvals = &agents.ActionApprovalLedger{Store: actionsStore}
	if browser := tools.NewBrowserClient(cfg.BrowserURL); browser != nil {
		agentHarness.Browser = browser
	}
	scheduleService.Intents = actionsStore

	noteSync := &notes.Sync{
		Store:   notesStore,
		Queue:   noteIndexQueue,
		Jobs:    jobStore,
		Intents: intentQueue,
		Flags:   flagResolver,
		Memory:  memoryEnqueuer,
	}
	if bgWorker != nil {
		if bgWorker.Handlers == nil {
			bgWorker.Handlers = map[string]jobs.Handler{}
		}
		bgWorker.Handlers[storage.JobTypeNoteLinkExpand] = (&notes.LinkExpander{Sync: noteSync}).HandleJob
		bgWorker.Start()
		log.Print("background jobs worker enabled", map[string]any{"cloudAgents": cfg.CloudAgentsEnabled})
	}
	if cfg.CloudAgentsEnabled && employeesStore.Enabled {
		employeeScheduler.Start()
	}
	if cfg.CloudAgentsEnabled && schedulesStore.Enabled {
		scheduleScheduler.Start()
	}
	reminderScheduler := &reminders.Scheduler{Store: reminderService}
	if remindersStore.Enabled {
		reminderScheduler.Start()
	}
	chatgptImportWorker.Notes = noteSync
	convStore.OnTurnPersisted = func(input storage.SaveTurnInput) {
		intentQueue.EnqueueConversationTurn(input.UserID, input.ConversationID, input.TurnIndex, input.UserTranscript)
		memoryEnqueuer.EnqueueFromConversationTurn(
			context.Background(),
			input.UserID,
			input.ConversationID,
			input.TurnIndex,
			input.UserTranscript,
		)
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

	var connectorSvc *connectors.Service
	var syncWorker *connectors.HourlySyncWorker
	if cfg.IntegrationsEnabled {
		encKey, err := connectors.ParseEncryptionKey(cfg.ConnectorEncryptionKey)
		if err != nil {
			log.Warn("integrations requested but encryption key invalid; connectors disabled", map[string]any{
				"error": err.Error(),
			})
		} else {
			store := &connectors.Store{DB: supa, Key: encKey}
			registry := connectors.NewRegistry()
			granolaAdapter := &granola.Adapter{
				Store: store,
				Notes: notesStore,
				KB:    kbStore,
			}
			registry.Register(granolaAdapter)
			googleEnabled := cfg.GoogleEnabled &&
				strings.TrimSpace(cfg.GoogleOAuthClientID) != "" &&
				strings.TrimSpace(cfg.GoogleOAuthClientSecret) != ""
			if googleEnabled {
				registry.Register(&google.Adapter{
					Store:        store,
					ClientID:     cfg.GoogleOAuthClientID,
					ClientSecret: cfg.GoogleOAuthClientSecret,
				})
			} else if cfg.GoogleEnabled {
				log.Warn("google integration requested but OAuth client credentials missing", nil)
			}
			connectorSvc = &connectors.Service{
				Registry:            registry,
				Store:               store,
				Notes:               notesStore,
				KB:                  kbStore,
				Preferences:         preferencesStore,
				IntegrationsEnabled: true,
				GranolaEnabled:      cfg.GranolaEnabled,
				GoogleEnabled:       googleEnabled,
				PublicAPIBase:       cfg.PublicAPIBase,
				WebAppBase:          cfg.WebAppBase,
			}
			actionExecutor.Integrations = connectorSvc
			syncWorker = &connectors.HourlySyncWorker{Service: connectorSvc}
			syncWorker.Start()
			log.Print("integrations enabled", map[string]any{
				"granola": cfg.GranolaEnabled,
				"google":  googleEnabled,
			})
		}
	}

	engine := &pipeline.Engine{
		Config:      cfg,
		STT:         stt,
		LLM:         llm,
		KB:          kbStore,
		Notes:       notesStore,
		Preferences: preferencesStore,
		Flags:       flagResolver,
		Tools:       chatTools,
	}
	engine.ConnectorTools = func(ctx context.Context, userID string, base *tools.Registry) *tools.Registry {
		extra := []tools.RegisteredTool{}
		if reminderService != nil && remindersStore.Enabled {
			extra = append(extra, reminders.SetReminderChatTool(userID, reminderService))
		}
		if connectorSvc != nil {
			extra = append(extra, connectors.LoadLiveToolsForUser(ctx, connectorSvc, userID)...)
		}
		if len(extra) == 0 {
			return base
		}
		return connectors.MergeUserTools(base, extra)
	}
	if connectorSvc != nil {
		engine.ConnectorPrompt = connectors.ConnectorToolsPrompt
	}
	if remindersStore.Enabled {
		engine.ExtraToolsPrompt = reminders.ChatToolPrompt
	}

	r := chi.NewRouter()
	r.Use(appmiddleware.RecoverWithErrorLog)
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

	// Client error reports → GitHub issues. Public by design (must work when
	// auth is broken); abuse bounded by per-IP and global rate limits.
	if reporter.Enabled() {
		r.Post("/errors", errreport.NewHandler(reporter))
	}

	// Donna Cafe live "N online" counter — public HTTP heartbeats, no PartyKit.
	cafe.RegisterRoutes(r, cafe.NewHandler())

	ingestHandler := &knowledge.IngestHandler{KB: kbStore, Queue: compileQueue, Notes: noteSync, Memory: memoryEnqueuer}
	r.With(appauth.RequireAuth(appauth.MiddlewareConfig{
		RequireAuth: cfg.RequireAuth,
		Auth:        authCfg,
	})).Post("/knowledge/ingest", ingestHandler.ServeHTTP)

	dailyChecker := &notes.DailyChecker{Store: notesStore}
	notesHandler := &notes.Handler{
		Store:   notesStore,
		Sync:    noteSync,
		Daily:   dailyChecker,
		KB:      kbStore,
		Intents: intentQueue,
		Flags:   flagResolver,
	}
	authMiddleware := appauth.RequireAuth(appauth.MiddlewareConfig{
		RequireAuth: cfg.RequireAuth,
		Auth:        authCfg,
	})
	notes.RegisterRoutes(r, authMiddleware, notesHandler)

	actionsHandler := &actions.Handler{Store: actionsStore, Executor: actionExecutor}
	actions.RegisterRoutes(r, authMiddleware, actionsHandler)

	if remindersStore.Enabled {
		remindersHandler := &reminders.Handler{Service: reminderService, Store: remindersStore}
		reminders.RegisterRoutes(r, authMiddleware, remindersHandler)
		log.Print("reminders: GET/POST /reminders, PATCH /reminders/{id}, POST /reminders/{id}/cancel|dismiss", nil)
	}

	if cfg.CloudAgentsEnabled {
		agentsHandler := &agents.Handler{Store: agentsStore, Spawner: agentSpawner, Jobs: jobStore, WebAppBase: cfg.WebAppBase, Actions: actionsStore}
		agents.RegisterRoutes(r, authMiddleware, agentsHandler)
		log.Print("cloud agents: /agent-runs enabled", map[string]any{"tools": agentRegistry.Len()})

		employeesHandler := &employees.Handler{Service: employeeService, Store: employeesStore, Agents: agentsStore}
		employees.RegisterRoutes(r, authMiddleware, employeesHandler)
		log.Print("ai employees: /employees enabled", nil)

		schedulesHandler := &schedules.Handler{Service: scheduleService, Store: schedulesStore, Agents: agentsStore}
		schedules.RegisterRoutes(r, authMiddleware, schedulesHandler)
		log.Print("scheduled goals: /schedules enabled", nil)
	}

	if skillsStore.Enabled {
		skillsHandler := &skills.Handler{Provider: skillProvider, Store: skillsStore}
		skills.RegisterRoutes(r, authMiddleware, skillsHandler)
		log.Print("agent skills: /skills enabled", map[string]any{"bundled": skills.FormatBundled()})
	}

	if connectorSvc != nil {
		connectors.RegisterRoutes(r, authMiddleware, &connectors.Handler{Service: connectorSvc})
		log.Print("integrations: GET /integrations, Granola OAuth/sync/disconnect routes", nil)
	}

	memoryHandler := &memory.Handler{KB: kbStore}
	memory.RegisterRoutes(r, authMiddleware, memoryHandler)

	chatgptimport.RegisterRoutes(r, authMiddleware, &chatgptimport.Handler{
		Imports: chatgptImportStore,
		Blobs:   chatgptBlobs,
		Jobs:    jobStore,
	})
	log.Print("chatgpt import: POST/GET /imports/chatgpt", nil)

	accountHandler := &account.Handler{
		Deleter:      &account.Deleter{DB: supa},
		Exporter:     &account.Exporter{DB: supa},
		Preferences:  preferencesStore,
		Flags:        flagResolver,
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

	chatHandler := &chat.Handler{Engine: engine, Conversations: convStore, Notes: noteSync, Ingest: ingestHandler}
	r.With(appauth.RequireAuth(appauth.MiddlewareConfig{
		RequireAuth: cfg.RequireAuth,
		Auth:        authCfg,
	})).Post("/chat", chatHandler.ServeHTTP)

	ttsProvider := providers.NewTTS(cfg.OpenAIAPIKey, cfg.CartesiaAPIKey, cfg.ElevenLabsAPIKey)
	ttsHandler := &ttspkg.Handler{TTS: ttsProvider, Store: supa}
	r.With(appauth.RequireAuth(appauth.MiddlewareConfig{
		RequireAuth: cfg.RequireAuth,
		Auth:        authCfg,
	})).Post("/tts", ttsHandler.ServeHTTP)

	conversationsHandler := &conversations.Handler{
		Store:      convStore,
		WebAppBase: cfg.WebAppBase,
	}
	conversations.RegisterRoutes(r, authMiddleware, conversationsHandler)

	voiceHandler := &voice.Handler{
		Config: cfg,
		Auth:   authCfg,
		Engine: engine,
		Notes:  noteSync,
	}
	r.Get("/voice", voiceHandler.ServeHTTP)

	liveHandler := &voicelive.Handler{
		Config: cfg,
		Auth:   authCfg,
		Retriever: &memory.Retriever{
			KB:    kbStore,
			Notes: notesStore,
		},
		Convs: convStore,
	}
	r.Get("/voice/live", liveHandler.ServeHTTP)

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
	if reporter.Enabled() {
		log.Print(fmt.Sprintf("error reports: POST /errors → github issues (%s)", cfg.GitHubIssueRepo), nil)
	} else {
		log.Print("error reports: disabled (set DONNA_ERROR_REPORTS_ENABLED + GITHUB_TOKEN)", nil)
	}
	log.Print("notes: GET /notes/search, POST /notes/daily-check, web-only CRUD at /notes/*", nil)
	log.Print("intents: GET /intents, POST /intents/{id}/dismiss", nil)
	log.Print("action-runs: GET /action-runs, POST /action-runs/{id}/confirm|cancel", nil)
	log.Print("reminders: GET/POST /reminders", nil)
	log.Print("memory: GET/PATCH /memory/profile, CRUD /memory/facts, review /memory/items|suggestions|feedback", nil)
	log.Print("chat: POST /chat (text, optional ?stream=1 for SSE)", nil)
	log.Print("tts: POST /tts (synthesize assistant reply audio, cached in conversation-audio)", nil)
	log.Print("conversations: GET /conversations, GET /conversations/{id}, POST/GET/DELETE /conversations/{id}/share", nil)
	log.Print("share: GET /share/{token} (public)", nil)
	log.Print("account: GET/PATCH/DELETE /account, GET /account/export", nil)
	log.Print(fmt.Sprintf("llm model: %s", cfg.LLMModel), nil)
	log.Print(fmt.Sprintf("agent model: %s", cfg.AgentModel), nil)
	log.Print(fmt.Sprintf("vision model: %s", cfg.VisionModel), nil)
	log.Print(fmt.Sprintf("stt model: %s", cfg.STTModel), nil)
	log.Print(fmt.Sprintf("voice (simulator): ws://127.0.0.1:%d/voice", cfg.Port), nil)
	log.Print(fmt.Sprintf("voice live (Gemini): ws://127.0.0.1:%d/voice/live", cfg.Port), nil)
	if cfg.GeminiAPIKey == "" {
		log.Print("voice live: disabled (set GEMINI_API_KEY)", nil)
	} else {
		log.Print(fmt.Sprintf("voice live model: %s", cfg.LiveModel), nil)
	}
	for _, ip := range lanAddresses() {
		log.Print(fmt.Sprintf("voice (physical device): ws://%s:%d/voice", ip, cfg.Port), nil)
		log.Print(fmt.Sprintf("voice live (physical device): ws://%s:%d/voice/live", ip, cfg.Port), nil)
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

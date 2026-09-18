package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/terenzif/ibis-assistant/internal/ai"
	"github.com/terenzif/ibis-assistant/internal/auth"
	"github.com/terenzif/ibis-assistant/internal/cli"
	"github.com/terenzif/ibis-assistant/internal/config"
	"github.com/terenzif/ibis-assistant/internal/db"
	"github.com/terenzif/ibis-assistant/internal/discovery"
	"github.com/terenzif/ibis-assistant/internal/ingest/code"
	"github.com/terenzif/ibis-assistant/internal/ingest/dynamic"
	"github.com/terenzif/ibis-assistant/internal/ingest/git"
	"github.com/terenzif/ibis-assistant/internal/ingest/logs"
	"github.com/terenzif/ibis-assistant/internal/logger"
	"github.com/terenzif/ibis-assistant/internal/optimization"
	"github.com/terenzif/ibis-assistant/internal/repopr"
	repoprado "github.com/terenzif/ibis-assistant/internal/repopr/providers/azuredevops"
	"github.com/terenzif/ibis-assistant/internal/schema"
	"github.com/terenzif/ibis-assistant/internal/search"
	"github.com/terenzif/ibis-assistant/internal/ticketing"
	ticketado "github.com/terenzif/ibis-assistant/internal/ticketing/providers/azuredevops"
	ticketjira "github.com/terenzif/ibis-assistant/internal/ticketing/providers/jira"
	ticketredmine "github.com/terenzif/ibis-assistant/internal/ticketing/providers/redmine"
	"github.com/terenzif/ibis-assistant/internal/gitrepo"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// AuthMiddleware injects ticketing and repo-provider auth headers into request context.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if key := strings.TrimSpace(r.Header.Get("X-Redmine-API-Key")); key != "" {
			ctx = context.WithValue(ctx, auth.RedmineKeyContextKey, key)
		}
		if email := strings.TrimSpace(r.Header.Get("X-Jira-Email")); email != "" {
			ctx = context.WithValue(ctx, auth.JiraEmailContextKey, email)
		}
		if token := strings.TrimSpace(r.Header.Get("X-Jira-API-Token")); token != "" {
			ctx = context.WithValue(ctx, auth.JiraAPITokenContextKey, token)
		}
		if pat := strings.TrimSpace(r.Header.Get("X-Azure-DevOps-PAT")); pat != "" {
			ctx = context.WithValue(ctx, auth.AzureDevOpsPATContextKey, pat)
		}
		if org := strings.TrimSpace(r.Header.Get("X-Azure-DevOps-Org")); org != "" {
			ctx = context.WithValue(ctx, auth.AzureDevOpsOrgContextKey, org)
		}
		if project := strings.TrimSpace(r.Header.Get("X-Azure-DevOps-Project")); project != "" {
			ctx = context.WithValue(ctx, auth.AzureDevOpsProjectContextKey, project)
		}
		if repo := strings.TrimSpace(r.Header.Get("X-Azure-DevOps-Repo")); repo != "" {
			ctx = context.WithValue(ctx, auth.AzureDevOpsRepoContextKey, repo)
		}
		if gitToken := strings.TrimSpace(r.Header.Get("X-Git-Token")); gitToken != "" {
			ctx = context.WithValue(ctx, auth.GitTokenContextKey, gitToken)
		} else if gitPat := strings.TrimSpace(r.Header.Get("X-Git-PAT")); gitPat != "" {
			ctx = context.WithValue(ctx, auth.GitTokenContextKey, gitPat)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func main() {
	// Se la configurazione non esiste e non stiamo usando opzioni di help o installazione,
	// avviamo automaticamente il wizard interattivo.
	if !configExists() {
		if len(os.Args) < 2 || (os.Args[1] != "help" && os.Args[1] != "-h" && os.Args[1] != "--help" && os.Args[1] != "install" && os.Args[1] != "uninstall" && os.Args[1] != "/install" && os.Args[1] != "/uninstall") {
			if err := config.RunWizard(); err != nil {
				fmt.Fprintf(os.Stderr, "Errore durante il wizard di configurazione: %v\n", err)
				os.Exit(1)
			}
			// Una volta completato il wizard, se l'utente ha creato il file di configurazione,
			// forziamo l'avvio del server in modalità interattiva.
			os.Args = []string{os.Args[0], "run"}
		}
	}

	if len(os.Args) < 2 {
		printHelp()
		return
	}

	cmd := os.Args[1]
	switch cmd {
	case "/run":
		// Service run mode: must use the service library to communicate with SCM
		if len(os.Args) > 1 {
			os.Args = append(os.Args[:1], os.Args[2:]...)
		}
		handleService("/run")
	case "run", "-run", "--run":
		// Normal interactive run
		if len(os.Args) > 2 {
			os.Args = append(os.Args[:1], os.Args[2:]...)
		} else {
			os.Args = os.Args[:1]
		}
		runServer(context.Background())
	case "/install", "install", "-install", "--install":
		handleService("/install")
	case "/uninstall", "uninstall", "-uninstall", "--uninstall":
		handleService("/uninstall")
	case "config":
		if err := config.RunWizard(); err != nil {
			fmt.Fprintf(os.Stderr, "Errore durante il wizard: %v\n", err)
			os.Exit(1)
		}
	case "start":
		startBackgroundServer()
	case "stop":
		stopBackgroundServer()
	default:
		// Se è una flag (inizia con - o --), avvia il server
		if strings.HasPrefix(cmd, "-") {
			runServer(context.Background())
		} else {
			// Altrimenti, delega al client CLI per l'inoltro delle richieste
			cli.ExecuteCLI(os.Args[1:])
		}
	}
}

func printHelp() {
	binName := filepath.Base(os.Args[0])
	fmt.Println("Ibis Assistant - MCP Knowledge Graph & Search Engine")
	fmt.Println("\nUso:")
	fmt.Printf("  %s <comando> [opzioni]\n", binName)
	fmt.Println("\nComandi del Server:")
	fmt.Println("  run         Avvia il server in modalità interattiva (comportamento standard)")
	fmt.Println("  start       Avvia il server in background come demone")
	fmt.Println("  stop        Ferma il server avviato in background")
	fmt.Println("  config      Avvia il wizard interattivo di configurazione")
	fmt.Println("  install     Installa Ibis Assistant come Servizio Windows ('ibis-assistant')")
	fmt.Println("  uninstall   Disinstalla il Servizio Windows")
	fmt.Println("\nComandi del Client CLI:")
	fmt.Println("  ask          Invia una domanda di reasoning sul codice")
	fmt.Println("  ingest       Sincronizza e indicizza codice o Git")
	fmt.Println("  ticket       Gestisce i ticket su Redmine/Jira/Azure DevOps")
	fmt.Println("  pr           Gestisce Pull Request")
	fmt.Println("  logs         Analizza file di log")
	fmt.Println("  credentials  Configura credenziali Git persistenti")
	fmt.Println("  memory       Gestisce memorie collaborative del progetto")
	fmt.Println("  outcome      Gestisce outcome e deduizioni di reasoning")
	fmt.Println("  optimize     Ottimizza la knowledge base tramite RAFT")
	fmt.Println("\nEsempio:")
	fmt.Printf("  %s run -port 3030\n", binName)
}

func getStringArg(args map[string]interface{}, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func getIntArg(args map[string]interface{}, key string) (int, error) {
	v, ok := args[key]
	if !ok || v == nil {
		return 0, nil
	}

	switch val := v.(type) {
	case float64:
		return int(val), nil
	case int:
		return val, nil
	case int32:
		return int(val), nil
	case int64:
		return int(val), nil
	case string:
		if strings.TrimSpace(val) == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil {
			return 0, fmt.Errorf("%s must be a number", key)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
}

func runServer(ctx context.Context) {
	// 1. Initial check for custom config path in raw args
	configPath := ""
	for i, arg := range os.Args {
		if (arg == "-config" || arg == "--config") && i+1 < len(os.Args) {
			configPath = os.Args[i+1]
			break
		}
	}

	// 2. Load Config (Defaults + config.json + Env)
	var cfg *config.Config
	if configPath != "" {
		cfg = config.Load(configPath)
	} else {
		cfg = config.Load()
	}

	// 3. Flags (Values from config are used as defaults)
	// This ensures that CLI flags override Config File values.
	fs := flag.NewFlagSet("server", flag.ExitOnError)

	portFlag := fs.Int("port", cfg.Port, "Port to listen on for SSE")
	modeFlag := fs.String("mode", cfg.Mode, "Mode: 'sse' or 'stdio'")
	scanFlag := fs.Bool("scan", cfg.AutoScan, "Discover git repositories in current/root directory")
	rootFlag := fs.String("root", cfg.DiscoveryRoot, "Root directory for discovery")
	logFileFlag := fs.String("log-file", cfg.LogFile, "Log file path")
	logLevelFlag := fs.String("log-level", cfg.LogLevel, "Log level: DEBUG, INFO, WARN, ERROR")
	// Add config flag just for help/documentation visibility
	_ = fs.String("config", "", "Path to config.json")

	fs.Parse(os.Args[1:])
	logger.Info("[INIT] Flags parsed. Mode: %s, Port: %d", *modeFlag, *portFlag)

	// 4. Apply overrides back to cfg
	cfg.Port = *portFlag
	cfg.Mode = *modeFlag
	cfg.AutoScan = *scanFlag
	cfg.DiscoveryRoot = *rootFlag
	cfg.LogFile = *logFileFlag
	cfg.LogLevel = *logLevelFlag

	// Initialize Logger
	if err := logger.Init(cfg.LogFile, cfg.LogLevel); err != nil {
		fmt.Printf("Error initializing logger: %v\n", err)
		os.Exit(1)
	}

	if cfg.ConfigLoaded {
		logger.Info("Configuration loaded from: %s", cfg.ConfigPath)
	} else {
		logger.Warn("No config file found, using defaults and environment variables.")
	}

	// 3. Discovery Logic
	logger.Info("Starting Ibis Assistant (Mode: %s)...", cfg.Mode)
	var activeRepos []string

	if cfg.AutoScan {
		logger.Info("Scanning for repositories in %s...", cfg.DiscoveryRoot)
		scanner := discovery.NewScanner(cfg.DiscoveryRoot)
		repos, err := scanner.Scan()
		if err != nil {
			logger.Warn("Discovery failed: %v", err)
		} else {
			activeRepos = append(activeRepos, repos...)
			logger.Info("Discovered %d repositories.", len(repos))
			for _, r := range repos {
				logger.Debug("Discovered repo: %s", r)
			}
		}
	}
	if len(cfg.GitRepos) > 0 {
		activeRepos = append(activeRepos, cfg.GitRepos...)
		logger.Debug("Added %d manually configured repositories", len(cfg.GitRepos))
	}

	// 4. Initialize MCP Server
	s := &mcpServerWrapper{server.NewMCPServer(
		"Knowledge Graph MCP",
		"1.1.0",
		server.WithLogging(),
	)}

	// --- [NEW] Start Embedded DB and Sidecars ---
	if err := code.EnsureAstGrep(cfg.DBAutoUpdate); err != nil {
		logger.Warn("Could not ensure ast-grep binary: %v", err)
	}

	var dbProcess *db.ProcessManager
	// Extract port from DBUrl
	dbPort := 8000
	if parts := strings.Split(cfg.DBUrl, ":"); len(parts) > 1 {
		fmt.Sscanf(parts[len(parts)-1], "%d", &dbPort)
	}

	logger.Debug("Attempting to start embedded database on port %d...", dbPort)
	proc, err := db.StartEmbedded(cfg.DBUser, cfg.DBPassword, cfg.DBDataPath, dbPort, cfg.DBAutoUpdate)
	if err != nil {
		logger.Info("Note: Could not start embedded database (or it is already running): %v", err)
	} else {
		dbProcess = proc
		logger.Info("Embedded SurrealDB started successfully.")
	}

	// 5. Connect DB
	logger.Info("Connecting to SurrealDB at %s...", cfg.DBUrl)
	var dbClient db.Executor // Use interface type directly
	concreteClient, err := db.NewClient(cfg.DBUrl, cfg.DBNamespace, cfg.DBDatabase, cfg.DBUser, cfg.DBPassword)
	if err != nil {
		logger.Error("CRITICAL: Failed to connect to SurrealDB: %v", err)
		// dbClient remains nil (interface nil)
	} else {
		dbClient = concreteClient
		if cfg.DBTimeout > 0 {
			concreteClient.SetTimeout(time.Duration(cfg.DBTimeout) * time.Second)
		}
		defer dbClient.Close()
		logger.Info("Successfully connected to SurrealDB.")

		// --- [NEW] Initialize Schema ---
		if err := db.InitSchema(ctx, dbClient); err != nil {
			logger.Warn("Database schema initialization warning: %v", err)
		}
	}

	// 6. Initialize Providers & Clients
	var embProvider ai.EmbeddingProvider
	var reasProvider ai.ReasoningProvider
	var ollamaRunner *ai.OllamaRunner

	// --- 6a. Initialize Embedding Provider ---
	switch cfg.AI.Embedding.Provider {
	case "ollama":
		cmd, err := ai.EnsureOllama(ctx, cfg.AI.Embedding.URL, cfg.AI.Embedding.Model, cfg.AI.Embedding.AutoStart, cfg.AI.Embedding.AutoUpdate)
		if err != nil {
			logger.Warn("Ollama initialization failed/skipped: %v. Embedding provider might be non-functional.", err)
		} else if cmd != nil {
			ollamaRunner = &ai.OllamaRunner{Cmd: cmd}
		}
		embProvider = ai.NewOllamaProvider(cfg.AI.Embedding.Model, cfg.AI.Embedding.URL)

	case "gemini":
		var aiKeys []ai.KeyConfig
		for _, k := range cfg.AI.Reasoning.Keys {
			rpm := k.RPM
			if rpm <= 0 {
				rpm = cfg.GeminiDefaultRPM
			}
			aiKeys = append(aiKeys, ai.KeyConfig{
				Key:          k.Key,
				RPM:          rpm,
				TPM:          k.TPM,
				RPD:          k.RPD,
				Owner:        k.Owner,
				AllowOverage: k.AllowOverage,
			})
		}
		embProvider = ai.NewGeminiProvider(aiKeys, dbClient)

	default:
		logger.Warn("Unknown embedding provider: %s. Defaulting to Ollama.", cfg.AI.Embedding.Provider)
		embProvider = ai.NewOllamaProvider(cfg.AI.Embedding.Model, cfg.AI.Embedding.URL)
	}

	// --- 6b. Initialize Reasoning Provider ---
	switch cfg.AI.Reasoning.Provider {
	case "gemini":
		var aiKeys []ai.KeyConfig
		for _, k := range cfg.AI.Reasoning.Keys {
			rpm := k.RPM
			if rpm <= 0 {
				rpm = cfg.GeminiDefaultRPM
			}
			aiKeys = append(aiKeys, ai.KeyConfig{
				Key:          k.Key,
				RPM:          rpm,
				TPM:          k.TPM,
				RPD:          k.RPD,
				Owner:        k.Owner,
				AllowOverage: k.AllowOverage,
			})
		}
		reasProvider = ai.NewGeminiProvider(aiKeys, dbClient)

	default:
		logger.Warn("Unknown reasoning provider: %s. Reasoning provider might be non-functional.", cfg.AI.Reasoning.Provider)
	}

	// --- 6c. Unified AI Client Orchestrator ---
	aiClient := ai.NewClient(embProvider, reasProvider, ollamaRunner)
	defer aiClient.Stop()

	// Start Batch Manager
	var batchManager *ai.BatchManager
	if dbClient != nil {
		batchManager = ai.NewBatchManager(dbClient, aiClient, cfg.DiscoveryRoot, cfg.MaxDeltaSize)
		batchManager.Start()
		defer batchManager.Stop()
	}

	ticketCfg, ticketCfgErr := ticketing.LoadConfig()
	if ticketCfgErr != nil {
		logger.Warn("Ticketing config not found or invalid (%v). Falling back to defaults.", ticketCfgErr)
		ticketCfg = ticketing.NewDefaultConfig()
	}
	ticketService := ticketing.NewService(ticketCfg, dbClient)
	if ticketCfg.Providers.Redmine.BaseURL != "" {
		logger.Info("Initializing ticket provider: redmine (%s)", ticketCfg.Providers.Redmine.BaseURL)
		ticketService.RegisterProvider(ticketredmine.New(ticketCfg.Providers.Redmine))
	}
	if ticketCfg.Providers.Jira.BaseURL != "" {
		logger.Info("Initializing ticket provider: jira (%s)", ticketCfg.Providers.Jira.BaseURL)
		ticketService.RegisterProvider(ticketjira.New(ticketCfg.Providers.Jira))
	}
	if ticketCfg.Providers.AzureDevOps.OrganizationURL != "" {
		logger.Info("Initializing ticket provider: azure_devops (%s)", ticketCfg.Providers.AzureDevOps.OrganizationURL)
		ticketService.RegisterProvider(ticketado.New(ticketCfg.Providers.AzureDevOps))
	}
	if dbClient != nil {
		if err := ticketing.MigrateLegacyIssues(ctx, dbClient); err != nil {
			logger.Warn("Legacy issue migration warning: %v", err)
		}
	}

	projectProviderMap := map[string]repopr.ProviderName{}
	for project, provider := range ticketCfg.ProjectProviderMap {
		projectProviderMap[project] = repopr.ProviderName(provider)
	}
	prService := repopr.NewService(repopr.Config{
		DefaultProvider:     repopr.ProviderName(ticketCfg.DefaultProvider),
		ProjectProviderMap:  projectProviderMap,
		DefaultTargetBranch: ticketCfg.PR.DefaultTargetBranch,
		ProjectTargetBranch: ticketCfg.PR.ProjectTargetBranch,
	})
	if ticketCfg.Providers.AzureDevOps.OrganizationURL != "" {
		prService.RegisterProvider(repoprado.New(repoprado.Config{
			OrganizationURL: ticketCfg.Providers.AzureDevOps.OrganizationURL,
			Project:         ticketCfg.Providers.AzureDevOps.Project,
			Repository:      ticketCfg.Providers.AzureDevOps.Repository,
			PAT:             ticketCfg.Providers.AzureDevOps.PAT,
		}))
	}

	searchService := &search.Service{DB: dbClient, AI: aiClient}
	optimizer := optimization.NewOptimizer(dbClient, aiClient, searchService)

	// --- Check Connections ---
	if !aiClient.IsFunctional() {
		logger.Warn("⚠️  AI vectorization is DISABLED (no Gemini API keys found).")
		logger.Warn("   - Add keys to 'config.json' (gemini_keys: [\"...\"])")
		logger.Warn("   - Or set 'GEMINI_API_KEY' environment variable.")
	}
	if len(ticketService.Capabilities()["providers"].([]string)) == 0 {
		logger.Warn("⚠️  No ticketing providers configured. ticket_* tools will return configuration errors.")
	}

	// --- [NEW] Background Indexing ---

	// Context for graceful shutdown of other components if needed
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var bgWg sync.WaitGroup

	// Start Log Watcher
	if cfg.LogsRoot != "" && dbClient != nil {
		logWatcher := logs.NewWatcher(cfg, dbClient, aiClient)
		if err := logWatcher.Start(ctx); err != nil {
			logger.Error("Failed to start Log Watcher: %v", err)
		} else {
			bgWg.Add(1)
			go func() {
				defer bgWg.Done()
				<-ctx.Done()
				logWatcher.Stop()
			}()
		}
	}

	// Start Background SMTP Notification Aggregator
	if cfg.SMTP.Enabled && dbClient != nil {
		window := cfg.SMTP.AggregationWindow
		if window == "" {
			window = "1h"
		}
		duration, err := time.ParseDuration(window)
		if err != nil {
			logger.Warn("Invalid aggregation window duration '%s', defaulting to 1 hour: %v", window, err)
			duration = 1 * time.Hour
		}

		logger.Info("Starting notification aggregator worker (interval: %v)...", duration)
		bgWg.Add(1)
		go func() {
			defer bgWg.Done()
			ticker := time.NewTicker(duration)
			defer ticker.Stop()

			notifier := logs.NewNotifier(cfg, dbClient)

			for {
				select {
				case <-ticker.C:
					logger.Debug("Aggregator tick: processing pending notifications...")
					if err := notifier.ProcessPendingNotifications(ctx); err != nil {
						logger.Error("Aggregator: failed to process pending notifications: %v", err)
					}
				case <-ctx.Done():
					logger.Info("Aggregator: context cancelled, flushing final pending notifications...")
					flushCtx, flushCancel := context.WithTimeout(context.Background(), 10*time.Second)
					if err := notifier.ProcessPendingNotifications(flushCtx); err != nil {
						logger.Error("Aggregator: final flush failed: %v", err)
					}
					flushCancel()
					return
				}
			}
		}()
	}

	// Start Log Polling Manager
	if dbClient != nil {
		pollingMgr := logs.NewPollingManager(cfg, dbClient, aiClient)
		pollingMgr.Start(ctx)
		bgWg.Add(1)
		go func() {
			defer bgWg.Done()
			<-ctx.Done()
			pollingMgr.Stop()
		}()
	}

	if len(activeRepos) > 0 && dbClient != nil {
		logger.Info("Triggering background indexing for %d repositories...", len(activeRepos))
		bgWg.Add(1)
		go func() {
			defer bgWg.Done()

			var repoWg sync.WaitGroup

			for _, r := range activeRepos {
				r := r

				// 1. Git Ingestion
				repoWg.Add(1)
				go func() {
					defer repoWg.Done()
					// Check for cancellation
					if ctx.Err() != nil {
						return
					}

					// Calculate a canonical Name for the repository relative to discovery root
					repoName, err := filepath.Rel(cfg.DiscoveryRoot, r)
					if err != nil {
						repoName = filepath.Base(r) // Fallback
					}

					logger.Info("Background: Indexing Git history for %s...", repoName)
					patterns := ticketService.ReferencePatternsForProject(repoName)
					if err := git.IngestRepo(ctx, dbClient, ticketService, r, repoName, cfg.RedmineConcurrency, patterns...); err != nil {
						logger.Error("Background: Git ingestion error for %s: %v", repoName, err)
					}
				}()

				// 2. Code Ingestion
				repoWg.Add(1)
				go func() {
					defer repoWg.Done()
					// Check for cancellation
					if ctx.Err() != nil {
						return
					}

					// Calculate a canonical Name for the repository relative to discovery root
					repoName, err := filepath.Rel(cfg.DiscoveryRoot, r)
					if err != nil {
						repoName = filepath.Base(r) // Fallback
					}

					logger.Info("Background: Vectorizing codebase for %s...", repoName)
					if err := code.IngestCodebase(ctx, dbClient, aiClient, r, repoName, cfg); err != nil {
						logger.Error("Background: Code ingestion error for %s: %v", repoName, err)
					}
				}()
			}

			repoWg.Wait()
			if ctx.Err() == nil {
				logger.Info("Background: Initial indexing complete.")
			} else {
				logger.Info("Background: Indexing interrupted.")
			}
		}()
	}

	// Start Dynamic Ingestion Manager
	ingestionManager := dynamic.NewProjectIngestionManager(cfg, dbClient)
	if dbClient != nil {
		ingestionManager.ProcessJob = func(jobCtx context.Context, job dynamic.IngestionJob, repoPath string) error {
			repoName := job.ProjectName
			logger.Info("Background: Indexing Git history for %s...", repoName)
			patterns := ticketService.ReferencePatternsForProject(repoName)
			if err := git.IngestRepo(jobCtx, dbClient, ticketService, repoPath, repoName, cfg.RedmineConcurrency, patterns...); err != nil {
				return fmt.Errorf("git ingestion error for %s: %w", repoName, err)
			}
			logger.Info("Background: Vectorizing codebase for %s...", repoName)
			if err := code.IngestCodebase(jobCtx, dbClient, aiClient, repoPath, repoName, cfg); err != nil {
				return fmt.Errorf("code ingestion error for %s: %w", repoName, err)
			}
			return nil
		}
	}

	type repoSessionContext struct {
		ProjectName string
		OriginURL   string
		Branch      string
		Commit      string
	}
	var sessionMu sync.RWMutex
	projectSessions := map[string]repoSessionContext{}
	setSession := func(s repoSessionContext) {
		if strings.TrimSpace(s.ProjectName) == "" {
			return
		}
		sessionMu.Lock()
		projectSessions[s.ProjectName] = s
		sessionMu.Unlock()
	}
	getSession := func(projectName string) (repoSessionContext, bool) {
		sessionMu.RLock()
		defer sessionMu.RUnlock()
		val, ok := projectSessions[projectName]
		return val, ok
	}

	// 7. Register Tools
	s.AddTool(mcp.NewTool("init_project",
		mcp.WithDescription("Initialize a repository session. Checks out the specific branch/commit and triggers ingestion if necessary."),
		mcp.WithString("project_name", mcp.Description("Name of the project")),
		mcp.WithString("origin_url", mcp.Description("Git origin URL")),
		mcp.WithString("branch", mcp.Description("Target branch")),
		mcp.WithString("commit", mcp.Description("Target commit hash (optional, takes precedence)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: init_project")
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Invalid arguments"), nil
		}

		syncDone := make(chan dynamic.IngestionResult, 1)
		projectName := getStringArg(args, "project_name")
		job := dynamic.IngestionJob{
			ProjectName: projectName,
			OriginURL:   getStringArg(args, "origin_url"),
			Branch:      getStringArg(args, "branch"),
			Commit:      getStringArg(args, "commit"),
			OnSyncDone: func(res dynamic.IngestionResult) {
				syncDone <- res
			},
			OnComplete: func(err error) {
				name := projectName
				if name == "" {
					name = "(unnamed)"
				}
				msg := fmt.Sprintf("Ingestion completed for %s", name)
				if err != nil {
					msg = fmt.Sprintf("Ingestion failed for %s: %v", name, err)
				}
				logger.Info(msg)
			},
		}
		setSession(repoSessionContext{
			ProjectName: job.ProjectName,
			OriginURL:   job.OriginURL,
			Branch:      job.Branch,
			Commit:      job.Commit,
		})
		if dbClient != nil && job.ProjectName != "" {
			projectID := db.FormatRecordID(schema.TableProject, db.SanitizeID(job.ProjectName))
			commit := job.Commit
			branch := job.Branch
			source := "session"
			if commit == "" && branch != "" {
				source = "session_branch"
			}
			_, _ = dbClient.Execute(ctx, fmt.Sprintf(
				"UPSERT %s SET name = '%s', baseline_commit = '%s', baseline_branch = '%s', baseline_source = '%s';",
				projectID, db.EscapeSQL(job.ProjectName), db.EscapeSQL(commit), db.EscapeSQL(branch), source))
		}

		ingestionManager.Enqueue(job)

		select {
		case res := <-syncDone:
			if res.Error != nil {
				var credErr *gitrepo.CredentialsRequiredError
				if errors.As(res.Error, &credErr) {
					jsonPayload := fmt.Sprintf(`{"status": "credentials_required", "provider": "%s", "target": "%s", "message": "%s"}`,
						credErr.Provider, credErr.Target, strings.ReplaceAll(credErr.Message, `"`, `\"`))
					return mcp.NewToolResultText(jsonPayload), nil
				}
				return mcp.NewToolResultError(fmt.Sprintf("Failed to sync: %v", res.Error)), nil
			}
			if dbClient != nil && projectName != "" && res.ActualCommit != "" {
				projectID := db.FormatRecordID(schema.TableProject, db.SanitizeID(projectName))
				_, _ = dbClient.Execute(ctx, fmt.Sprintf(
					"UPDATE %s SET baseline_commit = '%s', baseline_branch = '%s', baseline_source = 'init_project';",
					projectID, db.EscapeSQL(res.ActualCommit), db.EscapeSQL(job.Branch)))
				setSession(repoSessionContext{
					ProjectName: projectName,
					OriginURL:   job.OriginURL,
					Branch:      job.Branch,
					Commit:      res.ActualCommit,
				})
			}
			if !res.IsAligned {
				return mcp.NewToolResultText(fmt.Sprintf(`{"status": "requires_patch", "closest_known_commit": "%s", "message": "Commit not found on remote. Please use sync_local_patch for perfect alignment or continue with the closest commit."}`, res.ActualCommit)), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf(`{"status": "aligned", "actual_commit": "%s", "message": "Ingestion started in background"}`, res.ActualCommit)), nil
		case <-time.After(3 * time.Minute):
			return mcp.NewToolResultError("Timeout waiting for git sync"), nil
		}
	})

	s.AddTool(mcp.NewTool("update_project_status",
		mcp.WithDescription("Update project status after local commits/pushes to trigger incremental ingestion."),
		mcp.WithString("project_name", mcp.Description("Name of the project")),
		mcp.WithString("origin_url", mcp.Description("Git origin URL")),
		mcp.WithString("branch", mcp.Description("Current branch")),
		mcp.WithString("commit", mcp.Description("Current commit hash")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: update_project_status")
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Invalid arguments"), nil
		}
		job := dynamic.IngestionJob{
			ProjectName: getStringArg(args, "project_name"),
			OriginURL:   getStringArg(args, "origin_url"),
			Branch:      getStringArg(args, "branch"),
			Commit:      getStringArg(args, "commit"),
		}
		setSession(repoSessionContext{
			ProjectName: job.ProjectName,
			OriginURL:   job.OriginURL,
			Branch:      job.Branch,
			Commit:      job.Commit,
		})
		ingestionManager.Enqueue(job)
		return mcp.NewToolResultText("Aggiornamento accodato con successo."), nil
	})

	s.AddTool(mcp.NewTool("sync_local_patch",
		mcp.WithDescription("Apply a local unpushed unified diff to the project workspace after init_project returned requires_patch, then re-index the patched tree."),
		mcp.WithString("project_name", mcp.Description("Name of the project (same as init_project)")),
		mcp.WithString("patch", mcp.Description("Unified diff text (git format) to apply with git apply")),
		mcp.WithString("commit", mcp.Description("Optional local commit hash for session tracking after patch")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: sync_local_patch")
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Invalid arguments"), nil
		}
		projectName := getStringArg(args, "project_name")
		patchText := getStringArg(args, "patch")
		commit := getStringArg(args, "commit")
		if projectName == "" || patchText == "" {
			return mcp.NewToolResultError("project_name and patch are required"), nil
		}
		// Reject path traversal in project name
		if strings.Contains(projectName, "..") || strings.ContainsAny(projectName, `/\`) {
			return mcp.NewToolResultError("invalid project_name"), nil
		}

		patchText = strings.ReplaceAll(patchText, "\r\n", "\n")
		if !strings.HasSuffix(patchText, "\n") {
			patchText += "\n"
		}

		repoPath := filepath.Join(cfg.DiscoveryRoot, "dynamic", projectName)
		if st, err := os.Stat(repoPath); err != nil || !st.IsDir() {
			return mcp.NewToolResultError(fmt.Sprintf("workspace not found for project %q; run init_project first", projectName)), nil
		}

		patchPath := filepath.Join(repoPath, "ibis-local.patch")
		if err := os.WriteFile(patchPath, []byte(patchText), 0644); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to write patch: %v", err)), nil
		}
		defer os.Remove(patchPath)

		runner := gitrepo.NewRunner(nil)
		// Use relative patch path for Windows git compatibility
		out, err := runner.Run(ctx, repoPath, "apply", "--whitespace=nowarn", "ibis-local.patch")
		if err != nil {
			out2, err2 := runner.Run(ctx, repoPath, "apply", "--3way", "--whitespace=nowarn", "ibis-local.patch")
			if err2 != nil {
				// Keep failed patch for diagnosis
				_ = os.WriteFile(filepath.Join(repoPath, "ibis-local.failed.patch"), []byte(patchText), 0644)
				return mcp.NewToolResultError(fmt.Sprintf("git apply failed: %v (%s); retry: %v (%s)", err, string(out), err2, string(out2))), nil
			}
		}

		if sess, ok := getSession(projectName); ok {
			if commit != "" {
				sess.Commit = commit
			}
			setSession(sess)
		} else {
			setSession(repoSessionContext{ProjectName: projectName, Commit: commit})
		}

		if dbClient != nil && aiClient != nil && aiClient.IsEmbeddingFunctional() {
			if err := code.IngestCodebase(ctx, dbClient, aiClient, repoPath, projectName, cfg); err != nil {
				logger.Warn("sync_local_patch: ingest after patch failed: %v", err)
			}
		}

		head := ""
		if outRev, errRev := runner.Run(ctx, repoPath, "rev-parse", "HEAD"); errRev == nil {
			head = strings.TrimSpace(string(outRev))
		}
		payload := fmt.Sprintf(`{"status":"patched","project_name":"%s","workspace_commit":"%s","requested_commit":"%s","message":"Local patch applied and workspace re-indexed"}`,
			projectName, head, commit)
		return mcp.NewToolResultText(payload), nil
	})

	s.AddTool(mcp.NewTool("git_configure_credentials",
		mcp.WithDescription("Configure persistent Git credentials on the server for a specific domain or repository URL."),
		mcp.WithString("target", mcp.Description("Domain (e.g. github.com) or repository URL")),
		mcp.WithString("provider", mcp.Description("Git provider name: github, gitlab, azure_devops, or generic")),
		mcp.WithString("auth_type", mcp.Description("Authentication type: token, basic, or ssh")),
		mcp.WithString("token", mcp.Description("PAT, Token, or Password (required for token/basic)")),
		mcp.WithString("username", mcp.Description("Username (optional, for basic auth)")),
		mcp.WithString("ssh_private_key", mcp.Description("SSH Private Key (optional, for ssh auth)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: git_configure_credentials")
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Invalid arguments"), nil
		}

		target := getStringArg(args, "target")
		provider := getStringArg(args, "provider")
		authTypeStr := getStringArg(args, "auth_type")
		token := getStringArg(args, "token")
		username := getStringArg(args, "username")
		sshKey := getStringArg(args, "ssh_private_key")

		if target == "" {
			return mcp.NewToolResultError("Argument 'target' is required"), nil
		}
		if authTypeStr == "" {
			return mcp.NewToolResultError("Argument 'auth_type' is required"), nil
		}

		var authType gitrepo.AuthType
		switch strings.ToLower(authTypeStr) {
		case "token":
			authType = gitrepo.AuthTypeToken
			if token == "" {
				return mcp.NewToolResultError("Argument 'token' is required for token auth"), nil
			}
		case "basic":
			authType = gitrepo.AuthTypeBasic
			if token == "" {
				return mcp.NewToolResultError("Argument 'token' is required for basic auth (as password)"), nil
			}
		case "ssh":
			authType = gitrepo.AuthTypeSSH
			if sshKey == "" {
				return mcp.NewToolResultError("Argument 'ssh_private_key' is required for ssh auth"), nil
			}
		default:
			return mcp.NewToolResultError("Invalid auth_type, must be: token, basic, or ssh"), nil
		}

		cred := gitrepo.Credential{
			Target:        target,
			Provider:      provider,
			AuthType:      authType,
			Token:         token,
			Username:      username,
			SSHPrivateKey: sshKey,
		}

		store := gitrepo.NewCredentialStore(dbClient)
		if err := store.SaveCredential(ctx, cred); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to save credentials: %v", err)), nil
		}

		return mcp.NewToolResultText(`{"status": "configured", "message": "Git credentials saved successfully"}`), nil
	})

	s.AddTool(mcp.NewTool("provide_collaborative_memory",
		mcp.WithDescription("Inject collaborative memory/context for the project. Optional embeddings array can be provided to skip server-side AI embedding generation."),
		mcp.WithString("project_name", mcp.Description("Name of the project")),
		mcp.WithString("memory_text", mcp.Description("The textual content of the memory/insight")),
		mcp.WithString("embedding", mcp.Description("Optional pre-computed vector embedding (JSON array of floats)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: provide_collaborative_memory")
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Invalid arguments"), nil
		}

		projectName := getStringArg(args, "project_name")
		memoryText := getStringArg(args, "memory_text")
		var embedding []float64
		if embStr, ok := args["embedding"].(string); ok && embStr != "" {
			if err := json.Unmarshal([]byte(embStr), &embedding); err != nil {
				logger.Warn("Failed to unmarshal embedding JSON: %v", err)
			}
		}

		err := searchService.AddCollaborativeMemory(ctx, projectName, memoryText, embedding)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to add memory: %v", err)), nil
		}

		return mcp.NewToolResultText("Collaborative memory successfully integrated into the knowledge graph."), nil
	})

	s.AddTool(mcp.NewTool("save_reasoning_outcome",
		mcp.WithDescription("Save a semantic deduction/outcome for the project and reinforce the paths that led to it. Replaces reinforce_path."),
		mcp.WithString("project_name", mcp.Description("Name of the project")),
		mcp.WithString("question", mcp.Description("The original question or problem statement")),
		mcp.WithString("outcome_text", mcp.Description("The deduced reasoning outcome or solution")),
		mcp.WithString("useful_sources", mcp.Description("Comma-separated list of source IDs (commits, issues, files) that were helpful in reaching the outcome")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: save_reasoning_outcome")
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Invalid arguments"), nil
		}

		projectName := getStringArg(args, "project_name")
		question := getStringArg(args, "question")
		outcomeText := getStringArg(args, "outcome_text")

		var sources []string
		if srcStr, ok := args["useful_sources"].(string); ok && srcStr != "" {
			parts := strings.Split(srcStr, ",")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					sources = append(sources, p)
				}
			}
		}

		err := searchService.SaveReasoningOutcome(ctx, projectName, question, outcomeText, sources)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to save reasoning outcome: %v", err)), nil
		}

		return mcp.NewToolResultText("Reasoning outcome saved and structural reinforcement applied."), nil
	})

	s.AddTool(mcp.NewTool("ingest_code",
		mcp.WithDescription("Trigger codebase vectorization (Delta RAG). If no path provided, scans all repos."),
		mcp.WithString("path", mcp.Description("Optional specific repo path")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ingest_code")
		if dbClient == nil {
			return mcp.NewToolResultError("Database not connected"), nil
		}
		if len(cfg.GeminiKeys) == 0 {
			return mcp.NewToolResultError("No AI Keys configured"), nil
		}

		targets := activeRepos
		args, ok := request.Params.Arguments.(map[string]interface{})
		if ok {
			if p, ok := args["path"].(string); ok && p != "" {
				targets = []string{p}
			}
		}

		var output strings.Builder
		for _, r := range targets {
			repoName, _ := filepath.Rel(cfg.DiscoveryRoot, r)
			if err := code.IngestCodebase(ctx, dbClient, aiClient, r, repoName, cfg); err != nil {
				output.WriteString(fmt.Sprintf("Error scanning %s: %v\n", r, err))
			} else {
				output.WriteString(fmt.Sprintf("Successfully scanned %s\n", r))
			}
		}
		return mcp.NewToolResultText(output.String()), nil
	})

	s.AddTool(mcp.NewTool("analyze_blast_radius",
		mcp.WithDescription("Analyze the blast radius of changing a specific function or symbol."),
		mcp.WithString("repo_name", mcp.Description("Name of the repository")),
		mcp.WithString("file_path", mcp.Description("Relative path to the file containing the symbol")),
		mcp.WithString("symbol_name", mcp.Description("Name of the function or symbol")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: analyze_blast_radius")
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Invalid arguments"), nil
		}

		repoName := getStringArg(args, "repo_name")
		filePath := getStringArg(args, "file_path")
		symbolName := getStringArg(args, "symbol_name")

		if repoName == "" || filePath == "" || symbolName == "" {
			return mcp.NewToolResultError("Missing required arguments"), nil
		}

		res, err := search.AnalyzeBlastRadius(ctx, dbClient, repoName, filePath, symbolName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Blast radius analysis failed: %v", err)), nil
		}

		bytes, _ := json.MarshalIndent(res, "", "  ")
		return mcp.NewToolResultText(string(bytes)), nil
	})

	s.AddTool(mcp.NewTool("find_dead_code",
		mcp.WithDescription("Find potential dead code (functions with no callers) in a repository."),
		mcp.WithString("repo_name", mcp.Description("Name of the repository to scan")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: find_dead_code")
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Invalid arguments"), nil
		}

		repoName := getStringArg(args, "repo_name")
		if repoName == "" {
			return mcp.NewToolResultError("Missing required argument: repo_name"), nil
		}

		res, err := search.FindDeadCode(ctx, dbClient, repoName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Dead code analysis failed: %v", err)), nil
		}

		if len(res) == 0 {
			return mcp.NewToolResultText("No dead code found."), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Found %d potentially dead symbols:\n- %s", len(res), strings.Join(res, "\n- "))), nil
	})

	s.AddTool(mcp.NewTool("ask_project",
		mcp.WithDescription("Ask a natural language question about the project history and code. Uses Agentic reasoning."),
		mcp.WithString("query", mcp.Description("The question (e.g., 'Why was login changed?')")),
		mcp.WithString("branch_or_commit", mcp.Description("Optional current branch or commit to provide topological context to the AI")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ask_project")
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Invalid arguments"), nil
		}
		query, _ := args["query"].(string)
		branchContext, _ := args["branch_or_commit"].(string)

		result, err := searchService.AskProjectAgentic(ctx, query, branchContext)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
		}

		bytes, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(bytes)), nil
	})

	s.AddTool(mcp.NewTool("optimize_knowledge",
		mcp.WithDescription("Trigger the RAFT self-optimization loop to improve knowledge graph weights."),
		mcp.WithNumber("iterations", mcp.Description("Number of chunks to process (default 10)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: optimize_knowledge")
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Invalid arguments"), nil
		}

		iter := 10
		if v, ok := args["iterations"].(float64); ok {
			iter = int(v)
		}

		// Detach from request context: the HTTP/MCP call returns immediately and would cancel ctx.
		go func() {
			if err := optimizer.OptimizeLoop(context.Background(), iter); err != nil {
				logger.Error("Optimization failed: %v", err)
			} else {
				logger.Info("Optimization completed for %d iterations.", iter)
			}
		}()

		return mcp.NewToolResultText(fmt.Sprintf("Optimization started for %d iterations.", iter)), nil
	})

	// reinforce_path tool has been removed as it is now integrated into save_reasoning_outcome.

	// --- Ticketing Tools (provider-agnostic) ---

	s.AddTool(mcp.NewTool("ticket_get_capabilities",
		mcp.WithDescription("Return ticketing capabilities, configured providers, header hints, and client env hints."),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_get_capabilities")
		out, err := json.MarshalIndent(ticketService.Capabilities(), "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	})

	s.AddTool(mcp.NewTool("ticket_search",
		mcp.WithDescription("Search issues/work items across configured ticketing providers."),
		mcp.WithString("provider", mcp.Description("Optional provider override: redmine|jira|azure_devops")),
		mcp.WithString("query", mcp.Description("Optional search query")),
		mcp.WithString("project_key", mcp.Description("Optional project key for routing/filtering")),
		mcp.WithString("status", mcp.Description("Optional status filter")),
		mcp.WithString("type", mcp.Description("Optional issue/work-item type filter")),
		mcp.WithString("assignee", mcp.Description("Optional assignee filter")),
		mcp.WithString("author", mcp.Description("Optional author filter")),
		mcp.WithString("priority", mcp.Description("Optional priority filter")),
		mcp.WithString("updated_from", mcp.Description("Optional lower bound date")),
		mcp.WithString("updated_to", mcp.Description("Optional upper bound date")),
		mcp.WithNumber("limit", mcp.Description("Optional max results (default 20)")),
		mcp.WithNumber("offset", mcp.Description("Optional pagination offset")),
		mcp.WithString("sort", mcp.Description("Optional sort expression")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_search")
		args, _ := request.Params.Arguments.(map[string]interface{})
		params, err := buildTicketSearchParamsFromArgs(args)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %v", err)), nil
		}
		result, err := ticketService.Search(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ticketing error: %v", err)), nil
		}
		out, err := formatTicketSearchResponse(result, params, "Ticket search results")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(out), nil
	})

	s.AddTool(mcp.NewTool("ticket_search_my",
		mcp.WithDescription("Search issues/work items assigned to the current user."),
		mcp.WithString("provider", mcp.Description("Optional provider override: redmine|jira|azure_devops")),
		mcp.WithString("query", mcp.Description("Optional search query")),
		mcp.WithString("project_key", mcp.Description("Optional project key")),
		mcp.WithString("status", mcp.Description("Optional status filter")),
		mcp.WithString("type", mcp.Description("Optional issue/work-item type filter")),
		mcp.WithString("priority", mcp.Description("Optional priority filter")),
		mcp.WithString("updated_from", mcp.Description("Optional lower bound date")),
		mcp.WithString("updated_to", mcp.Description("Optional upper bound date")),
		mcp.WithNumber("limit", mcp.Description("Optional max results (default 20)")),
		mcp.WithNumber("offset", mcp.Description("Optional pagination offset")),
		mcp.WithString("sort", mcp.Description("Optional sort expression")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_search_my")
		args, _ := request.Params.Arguments.(map[string]interface{})
		params, err := buildTicketSearchParamsFromArgs(args)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %v", err)), nil
		}
		result, err := ticketService.SearchMy(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ticketing error: %v", err)), nil
		}
		out, err := formatTicketSearchResponse(result, params, "Ticket my-issues search results")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(out), nil
	})

	s.AddTool(mcp.NewTool("ticket_get",
		mcp.WithDescription("Get a ticket/work-item by ID or external key."),
		mcp.WithString("provider", mcp.Description("Optional provider override: redmine|jira|azure_devops")),
		mcp.WithString("id", mcp.Description("Ticket/work-item ID or key")),
		mcp.WithString("project_key", mcp.Description("Optional project key for routing")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_get")
		args, _ := request.Params.Arguments.(map[string]interface{})
		id := getStringArg(args, "id")
		if id == "" {
			return mcp.NewToolResultError("Invalid arguments: id is required"), nil
		}
		issue, err := ticketService.Get(ctx, getStringArg(args, "provider"), id, getStringArg(args, "project_key"))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ticketing error: %v", err)), nil
		}
		if issue == nil {
			return mcp.NewToolResultError("Ticket not found"), nil
		}
		out, err := json.MarshalIndent(issue, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	})

	s.AddTool(mcp.NewTool("ticket_create",
		mcp.WithDescription("Create a new ticket/work-item on the selected provider."),
		mcp.WithString("provider", mcp.Description("Optional provider override: redmine|jira|azure_devops")),
		mcp.WithString("project_key", mcp.Description("Project key or identifier")),
		mcp.WithString("title", mcp.Description("Ticket title/summary")),
		mcp.WithString("description", mcp.Description("Optional description")),
		mcp.WithString("type", mcp.Description("Optional issue/work-item type")),
		mcp.WithString("assignee", mcp.Description("Optional assignee")),
		mcp.WithString("priority", mcp.Description("Optional priority")),
		mcp.WithString("provider_fields_json", mcp.Description("Optional provider-specific JSON fields")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_create")
		args, _ := request.Params.Arguments.(map[string]interface{})
		params := ticketing.CreateParams{
			Provider:           getStringArg(args, "provider"),
			ProjectKey:         getStringArg(args, "project_key"),
			Title:              getStringArg(args, "title"),
			Description:        getStringArg(args, "description"),
			Type:               getStringArg(args, "type"),
			Assignee:           getStringArg(args, "assignee"),
			Priority:           getStringArg(args, "priority"),
			ProviderFieldsJSON: getStringArg(args, "provider_fields_json"),
		}
		issue, err := ticketService.Create(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ticketing error: %v", err)), nil
		}
		out, err := json.MarshalIndent(issue, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	})

	s.AddTool(mcp.NewTool("ticket_update",
		mcp.WithDescription("Update an existing ticket/work-item."),
		mcp.WithString("provider", mcp.Description("Optional provider override: redmine|jira|azure_devops")),
		mcp.WithString("id", mcp.Description("Ticket/work-item ID or key")),
		mcp.WithString("project_key", mcp.Description("Optional project key for routing")),
		mcp.WithString("notes", mcp.Description("Optional notes/comment")),
		mcp.WithString("status", mcp.Description("Optional status")),
		mcp.WithString("type", mcp.Description("Optional issue/work-item type")),
		mcp.WithString("assignee", mcp.Description("Optional assignee")),
		mcp.WithString("priority", mcp.Description("Optional priority")),
		mcp.WithString("workflow_action", mcp.Description("Optional semantic action: resolve|close|reopen")),
		mcp.WithString("fixed_version", mcp.Description("Optional fixed/target version")),
		mcp.WithString("provider_fields_json", mcp.Description("Optional provider-specific JSON fields")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_update")
		args, _ := request.Params.Arguments.(map[string]interface{})
		id := getStringArg(args, "id")
		if id == "" {
			return mcp.NewToolResultError("Invalid arguments: id is required"), nil
		}
		params := ticketing.UpdateParams{
			Provider:           getStringArg(args, "provider"),
			Notes:              getStringArg(args, "notes"),
			Status:             getStringArg(args, "status"),
			Type:               getStringArg(args, "type"),
			Assignee:           getStringArg(args, "assignee"),
			Priority:           getStringArg(args, "priority"),
			WorkflowAction:     getStringArg(args, "workflow_action"),
			FixedVersion:       getStringArg(args, "fixed_version"),
			ProviderFieldsJSON: getStringArg(args, "provider_fields_json"),
		}
		issue, err := ticketService.Update(ctx, id, getStringArg(args, "project_key"), params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ticketing error: %v", err)), nil
		}
		out, err := json.MarshalIndent(issue, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	})

	s.AddTool(mcp.NewTool("ticket_add_comment",
		mcp.WithDescription("Add a comment to a ticket/work-item."),
		mcp.WithString("provider", mcp.Description("Optional provider override")),
		mcp.WithString("id", mcp.Description("Ticket/work-item ID or key")),
		mcp.WithString("project_key", mcp.Description("Optional project key")),
		mcp.WithString("comment", mcp.Description("Comment text")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_add_comment")
		args, _ := request.Params.Arguments.(map[string]interface{})
		id := getStringArg(args, "id")
		comment := getStringArg(args, "comment")
		if id == "" || comment == "" {
			return mcp.NewToolResultError("Invalid arguments: id and comment are required"), nil
		}
		if err := ticketService.AddComment(ctx, getStringArg(args, "provider"), id, getStringArg(args, "project_key"), comment); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ticketing error: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Comment added to ticket %s.", id)), nil
	})

	s.AddTool(mcp.NewTool("ticket_assign",
		mcp.WithDescription("Assign a ticket/work-item to a user."),
		mcp.WithString("provider", mcp.Description("Optional provider override")),
		mcp.WithString("id", mcp.Description("Ticket/work-item ID or key")),
		mcp.WithString("project_key", mcp.Description("Optional project key")),
		mcp.WithString("assignee", mcp.Description("Assignee identifier")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_assign")
		args, _ := request.Params.Arguments.(map[string]interface{})
		id := getStringArg(args, "id")
		assignee := getStringArg(args, "assignee")
		if id == "" || assignee == "" {
			return mcp.NewToolResultError("Invalid arguments: id and assignee are required"), nil
		}
		if err := ticketService.Assign(ctx, getStringArg(args, "provider"), id, getStringArg(args, "project_key"), assignee); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ticketing error: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Ticket %s assigned to %s.", id, assignee)), nil
	})

	s.AddTool(mcp.NewTool("ticket_transition",
		mcp.WithDescription("Transition a ticket/work-item to a specific state/status."),
		mcp.WithString("provider", mcp.Description("Optional provider override")),
		mcp.WithString("id", mcp.Description("Ticket/work-item ID or key")),
		mcp.WithString("project_key", mcp.Description("Optional project key")),
		mcp.WithString("transition", mcp.Description("Target transition/status id or name")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_transition")
		args, _ := request.Params.Arguments.(map[string]interface{})
		id := getStringArg(args, "id")
		transition := getStringArg(args, "transition")
		if id == "" || transition == "" {
			return mcp.NewToolResultError("Invalid arguments: id and transition are required"), nil
		}
		if err := ticketService.Transition(ctx, getStringArg(args, "provider"), id, getStringArg(args, "project_key"), transition); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ticketing error: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Ticket %s transitioned to %s.", id, transition)), nil
	})

	s.AddTool(mcp.NewTool("ticket_list_statuses",
		mcp.WithDescription("List statuses/states for a provider/project and optional issue type."),
		mcp.WithString("provider", mcp.Description("Optional provider override")),
		mcp.WithString("project_key", mcp.Description("Optional project key")),
		mcp.WithString("issue_type", mcp.Description("Optional issue/work-item type")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_list_statuses")
		args, _ := request.Params.Arguments.(map[string]interface{})
		statuses, err := ticketService.ListStatuses(ctx, getStringArg(args, "provider"), getStringArg(args, "project_key"), getStringArg(args, "issue_type"))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ticketing error: %v", err)), nil
		}
		out, err := json.MarshalIndent(statuses, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	})

	s.AddTool(mcp.NewTool("ticket_search_users",
		mcp.WithDescription("Search users in ticketing provider for assignment/filtering."),
		mcp.WithString("provider", mcp.Description("Optional provider override")),
		mcp.WithString("project_key", mcp.Description("Optional project key")),
		mcp.WithString("query", mcp.Description("User search query")),
		mcp.WithNumber("limit", mcp.Description("Optional max results (default 20)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_search_users")
		args, _ := request.Params.Arguments.(map[string]interface{})
		limit, _ := getIntArg(args, "limit")
		if limit <= 0 {
			limit = 20
		}
		users, err := ticketService.SearchUsers(ctx, getStringArg(args, "provider"), getStringArg(args, "project_key"), getStringArg(args, "query"), limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ticketing error: %v", err)), nil
		}
		out, err := json.MarshalIndent(users, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	})

	s.AddTool(mcp.NewTool("ticket_list_projects",
		mcp.WithDescription("List projects available in the selected ticketing provider."),
		mcp.WithString("provider", mcp.Description("Optional provider override")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_list_projects")
		args, _ := request.Params.Arguments.(map[string]interface{})
		projects, err := ticketService.ListProjects(ctx, getStringArg(args, "provider"))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ticketing error: %v", err)), nil
		}
		out, err := json.MarshalIndent(projects, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	})

	s.AddTool(mcp.NewTool("ticket_mark_resolved",
		mcp.WithDescription("Move a ticket/work-item to resolved state using provider/project workflow mapping."),
		mcp.WithString("provider", mcp.Description("Optional provider override")),
		mcp.WithString("id", mcp.Description("Ticket/work-item ID or key")),
		mcp.WithString("project_key", mcp.Description("Optional project key")),
		mcp.WithString("issue_type", mcp.Description("Optional issue/work-item type")),
		mcp.WithString("notes", mcp.Description("Optional comment to append after transition")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_mark_resolved")
		args, _ := request.Params.Arguments.(map[string]interface{})
		id := getStringArg(args, "id")
		if id == "" {
			return mcp.NewToolResultError("Invalid arguments: id is required"), nil
		}
		res, err := ticketService.MarkResolved(ctx, getStringArg(args, "provider"), id, getStringArg(args, "project_key"), getStringArg(args, "issue_type"))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ticketing error: %v", err)), nil
		}
		if notes := getStringArg(args, "notes"); notes != "" {
			_ = ticketService.AddComment(ctx, getStringArg(args, "provider"), id, getStringArg(args, "project_key"), notes)
		}
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	})

	s.AddTool(mcp.NewTool("ticket_mark_closed",
		mcp.WithDescription("Move a ticket/work-item to closed state using provider/project workflow mapping."),
		mcp.WithString("provider", mcp.Description("Optional provider override")),
		mcp.WithString("id", mcp.Description("Ticket/work-item ID or key")),
		mcp.WithString("project_key", mcp.Description("Optional project key")),
		mcp.WithString("issue_type", mcp.Description("Optional issue/work-item type")),
		mcp.WithString("notes", mcp.Description("Optional comment to append after transition")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_mark_closed")
		args, _ := request.Params.Arguments.(map[string]interface{})
		id := getStringArg(args, "id")
		if id == "" {
			return mcp.NewToolResultError("Invalid arguments: id is required"), nil
		}
		res, err := ticketService.MarkClosed(ctx, getStringArg(args, "provider"), id, getStringArg(args, "project_key"), getStringArg(args, "issue_type"))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ticketing error: %v", err)), nil
		}
		if notes := getStringArg(args, "notes"); notes != "" {
			_ = ticketService.AddComment(ctx, getStringArg(args, "provider"), id, getStringArg(args, "project_key"), notes)
		}
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	})

	s.AddTool(mcp.NewTool("ticket_reopen",
		mcp.WithDescription("Reopen a ticket/work-item using provider/project workflow mapping."),
		mcp.WithString("provider", mcp.Description("Optional provider override")),
		mcp.WithString("id", mcp.Description("Ticket/work-item ID or key")),
		mcp.WithString("project_key", mcp.Description("Optional project key")),
		mcp.WithString("issue_type", mcp.Description("Optional issue/work-item type")),
		mcp.WithString("notes", mcp.Description("Optional comment to append after transition")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ticket_reopen")
		args, _ := request.Params.Arguments.(map[string]interface{})
		id := getStringArg(args, "id")
		if id == "" {
			return mcp.NewToolResultError("Invalid arguments: id is required"), nil
		}
		res, err := ticketService.Reopen(ctx, getStringArg(args, "provider"), id, getStringArg(args, "project_key"), getStringArg(args, "issue_type"))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Ticketing error: %v", err)), nil
		}
		if notes := getStringArg(args, "notes"); notes != "" {
			_ = ticketService.AddComment(ctx, getStringArg(args, "provider"), id, getStringArg(args, "project_key"), notes)
		}
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	})

	// --- Repository Pull Request Tools ---

	s.AddTool(mcp.NewTool("repo_pr_create",
		mcp.WithDescription("Create a pull request using configured SCM provider (Azure DevOps in v1)."),
		mcp.WithString("provider", mcp.Description("Optional provider override: azure_devops")),
		mcp.WithString("project_name", mcp.Description("Optional project name (fallback to init_project session)")),
		mcp.WithString("origin_url", mcp.Description("Optional repository origin URL (fallback to session)")),
		mcp.WithString("repository", mcp.Description("Optional repository id/name (fallback derive from origin_url/session)")),
		mcp.WithString("source_branch", mcp.Description("Source branch (fallback to current init_project branch)")),
		mcp.WithString("target_branch", mcp.Description("Optional target branch (fallback project/default config)")),
		mcp.WithString("title", mcp.Description("Optional PR title (auto-generated if omitted)")),
		mcp.WithString("description", mcp.Description("Optional PR description (auto-generated if omitted)")),
		mcp.WithString("ticket_ids", mcp.Description("Optional comma-separated ticket ids/keys")),
		mcp.WithString("reviewer_ids", mcp.Description("Optional comma-separated reviewer ids")),
		mcp.WithString("auto_complete", mcp.Description("Optional true/false to request auto-complete")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: repo_pr_create")
		args, _ := request.Params.Arguments.(map[string]interface{})
		projectName := getStringArg(args, "project_name")
		originURL := getStringArg(args, "origin_url")
		sourceBranch := getStringArg(args, "source_branch")
		repository := getStringArg(args, "repository")

		if projectName != "" {
			if sess, ok := getSession(projectName); ok {
				if originURL == "" {
					originURL = sess.OriginURL
				}
				if sourceBranch == "" {
					sourceBranch = sess.Branch
				}
			}
		}
		if repository == "" {
			repository = deriveRepositoryFromOrigin(originURL)
		}
		if sourceBranch == "" {
			return mcp.NewToolResultError("Invalid arguments: source_branch is required (or initialize project session via init_project)."), nil
		}

		params := repopr.CreateParams{
			Provider:     getStringArg(args, "provider"),
			ProjectName:  projectName,
			OriginURL:    originURL,
			Repository:   repository,
			SourceBranch: sourceBranch,
			TargetBranch: getStringArg(args, "target_branch"),
			Title:        getStringArg(args, "title"),
			Description:  getStringArg(args, "description"),
			TicketIDs:    getStringSliceArg(args, "ticket_ids"),
			ReviewerIDs:  getStringSliceArg(args, "reviewer_ids"),
			AutoComplete: getBoolArg(args, "auto_complete"),
		}
		res, err := prService.CreatePR(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("PR error: %v", err)), nil
		}
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	})

	s.AddTool(mcp.NewTool("repo_pr_complete",
		mcp.WithDescription("Complete/merge an existing pull request."),
		mcp.WithString("provider", mcp.Description("Optional provider override: azure_devops")),
		mcp.WithString("project_name", mcp.Description("Optional project name")),
		mcp.WithString("repository", mcp.Description("Optional repository id/name")),
		mcp.WithString("pr_id", mcp.Description("Pull request id")),
		mcp.WithString("delete_source_branch", mcp.Description("Optional true/false")),
		mcp.WithString("squash", mcp.Description("Optional true/false")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: repo_pr_complete")
		args, _ := request.Params.Arguments.(map[string]interface{})
		projectName := getStringArg(args, "project_name")
		repository := getStringArg(args, "repository")
		if projectName != "" && repository == "" {
			if sess, ok := getSession(projectName); ok {
				repository = deriveRepositoryFromOrigin(sess.OriginURL)
			}
		}
		params := repopr.CompleteParams{
			Provider:           getStringArg(args, "provider"),
			ProjectName:        projectName,
			Repository:         repository,
			PRID:               getStringArg(args, "pr_id"),
			DeleteSourceBranch: getBoolArg(args, "delete_source_branch"),
			Squash:             getBoolArg(args, "squash"),
		}
		if params.PRID == "" {
			return mcp.NewToolResultError("Invalid arguments: pr_id is required"), nil
		}
		res, err := prService.CompletePR(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("PR error: %v", err)), nil
		}
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	})

	s.AddTool(mcp.NewTool("analyze_logs",
		mcp.WithDescription("Analyze log entries/text synchronously and return detected errors or anomalies."),
		mcp.WithString("project_name", mcp.Description("Name of the project")),
		mcp.WithString("log_file", mcp.Description("Optional log file name")),
		mcp.WithString("log_text", mcp.Description("Raw log text content to analyze (entries separated by newlines)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: analyze_logs")
		args, _ := request.Params.Arguments.(map[string]interface{})
		projectName := getStringArg(args, "project_name")
		logFile := getStringArg(args, "log_file")
		logText := getStringArg(args, "log_text")

		if projectName == "" {
			return mcp.NewToolResultError("project_name is required"), nil
		}
		if logText == "" {
			return mcp.NewToolResultError("log_text is required"), nil
		}
		if logFile == "" {
			logFile = "mcp_tool.log"
		}

		lines := strings.Split(logText, "\n")
		var cleanLines []string
		for _, l := range lines {
			trimmed := strings.TrimSpace(l)
			if trimmed != "" {
				cleanLines = append(cleanLines, trimmed)
			}
		}

		if len(cleanLines) == 0 {
			return mcp.NewToolResultText(`[]`), nil
		}

		path := filepath.Join(cfg.LogsRoot, projectName, logFile)
		analyzer := logs.NewLogAnalyzerWithProject(ctx, path, projectName, cfg, dbClient, aiClient)

		anomalies, err := analyzer.ProcessBatchSync(ctx, cleanLines)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Analysis failed: %v", err)), nil
		}

		if len(anomalies) > 0 {
			notifier := logs.NewNotifier(cfg, dbClient)
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("MCP Synchronous Log Report for Project: %s (File: %s)\r\n\r\n", projectName, logFile))
			sb.WriteString(fmt.Sprintf("Detected %d errors/anomalies:\r\n\r\n", len(anomalies)))
			maxSeverity := 0
			for _, e := range anomalies {
				if e.Severity > maxSeverity {
					maxSeverity = e.Severity
				}
				sb.WriteString(fmt.Sprintf("Category: %s\r\n", e.Category))
				sb.WriteString(fmt.Sprintf("Severity: %d/10\r\n", e.Severity))
				if e.File != "" {
					sb.WriteString(fmt.Sprintf("File: %s\r\n", e.File))
				}
				if e.Cause != "" {
					sb.WriteString(fmt.Sprintf("Cause: %s\r\n", e.Cause))
				}
				sb.WriteString("Stack Trace:\r\n")
				sb.WriteString(e.StackTrace + "\r\n\r\n")
			}
			notifier.QueueNotification(ctx, projectName, logFile, len(anomalies), maxSeverity, sb.String())
		}

		out, marshalErr := json.MarshalIndent(anomalies, "", "  ")
		if marshalErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", marshalErr)), nil
		}

		return mcp.NewToolResultText(string(out)), nil
	})

	// 8. Start Server

	// Handle Signals in Main Thread
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	if cfg.Mode == "sse" {
		logger.Info("Starting SSE/Streamable HTTP server on port %d...", cfg.Port)
		sseServer := server.NewSSEServer(s.MCPServer)
		streamableServer := server.NewStreamableHTTPServer(s.MCPServer)

		// Standard HTTP server with graceful shutdown
		mux := http.NewServeMux()

		// Endpoint per l'auto-discovery (HTML e metadata JSON)
		mux.HandleFunc("/", autoDiscoveryHandler(cfg))

		// Bridge HTTP POST per inoltrare i comandi CLI locali
		mux.HandleFunc("/api/v1/cli/call", cliCallHandler())

		// Legacy SSE endpoints
		mux.Handle("/sse", sseServer.SSEHandler())
		mux.Handle("/message", sseServer.MessageHandler())

		// Streamable HTTP endpoints (MCP standard)
		mux.Handle("/mcp/", streamableServer)
		mux.Handle("/mcp", streamableServer)

		// New HTTP Log Upload endpoint
		mux.HandleFunc("/api/v1/logs/upload", logUploadHandler(cfg, dbClient, aiClient))

		// Wrap the entire mux with AuthMiddleware
		handler := AuthMiddleware(mux)

		// Store server instance for graceful shutdown
		httpSrv := &http.Server{
			Addr:    fmt.Sprintf(":%d", cfg.Port),
			Handler: handler,
		}

		go func() {
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("Server error: %v", err)
				sigChan <- syscall.SIGTERM
			}
		}()

		// Block until a shutdown signal or context cancellation is received.
		logger.Info("Ibis Assistant is operational. Press Ctrl+C to stop.")
		select {
		case <-sigChan:
			logger.Info("Shutdown signal received.")
		case <-ctx.Done():
			logger.Info("Cancellation signal received.")
		}

		// Graceful HTTP Shutdown
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("HTTP server graceful shutdown failed: %v", err)
		} else {
			logger.Info("HTTP server stopped gracefully.")
		}

	} else {
		logger.Info("Starting STDIO server...")
		// STDIO usually blocks until stdin closes
		go func() {
			if err := server.ServeStdio(s.MCPServer); err != nil {
				logger.Error("Server error: %v", err)
			}
			// If stdio interaction ends, we assume done
			sigChan <- syscall.SIGTERM
		}()

		select {
		case <-sigChan:
			logger.Info("Received signal, shutting down...")
		case <-ctx.Done():
			logger.Info("Context cancelled, shutting down...")
		}
	}

	// GRACEFUL SHUTDOWN SEQUENCE
	// 1. Cancel background context (stops ingestion loops)
	cancel()

	// Wait for background tasks to finish (prevents panic in AI client)
	logger.Info("Waiting for background tasks to finish...")
	bgWg.Wait()

	// 2. Stop AI Workers (waits for them to finish current job)
	logger.Info("Stopping AI workers...")
	aiClient.Stop()
	// Stop Batch Manager
	if batchManager != nil {
		logger.Info("Stopping Batch Manager...")
		batchManager.Stop()
	}

	// Stop Ingestion Manager
	ingestionManager.Stop()

	// 3. Close DB Connection (now safe as no workers are using it)
	if dbClient != nil {
		logger.Info("Closing database connection...")
		dbClient.Close()
	}

	// Give the websocket connection time to close properly before killing the DB process
	time.Sleep(1 * time.Second)

	// 4. Stop DB Process
	if dbProcess != nil {
		logger.Info("Stopping embedded database process...")
		if err := dbProcess.Stop(); err != nil {
			logger.Error("Error stopping database: %v", err)
		}
	}
}

func logUploadHandler(cfg *config.Config, dbClient db.Executor, aiClient *ai.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if !cfg.LogIngestion.HTTP.Enabled {
			http.Error(w, "HTTP Log Ingestion is disabled in configuration", http.StatusForbidden)
			return
		}

		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" || apiKey != cfg.LogIngestion.HTTP.APIKey {
			http.Error(w, "Unauthorized: invalid or missing API key", http.StatusUnauthorized)
			return
		}

		// Cap body size; ProcessBatchSync further chunks lines for AI.
		r.Body = http.MaxBytesReader(w, r.Body, logs.MaxLogIngestBytes)

		var project string
		var fileName string
		var lines []string
		var truncated bool

		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			type LogUploadRequest struct {
				Project  string   `json:"project"`
				FileName string   `json:"file_name"`
				Lines    []string `json:"lines"`
				Text     string   `json:"text"`
			}
			var req LogUploadRequest
			dec := json.NewDecoder(r.Body)
			if err := dec.Decode(&req); err != nil {
				if err.Error() == "http: request body too large" || strings.Contains(err.Error(), "request body too large") {
					http.Error(w, fmt.Sprintf("Request body exceeds %d byte limit", logs.MaxLogIngestBytes), http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, fmt.Sprintf("Failed to parse JSON body: %v", err), http.StatusBadRequest)
				return
			}
			project = req.Project
			fileName = req.FileName
			if req.Text != "" {
				lines = strings.Split(req.Text, "\n")
			} else {
				lines = req.Lines
			}
		} else {
			project = r.URL.Query().Get("project")
			fileName = r.URL.Query().Get("file_name")
			bodyBytes, trunc, err := logs.ReadCapped(r.Body, logs.MaxLogIngestBytes)
			if err != nil {
				if strings.Contains(err.Error(), "request body too large") {
					http.Error(w, fmt.Sprintf("Request body exceeds %d byte limit", logs.MaxLogIngestBytes), http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, "Failed to read body", http.StatusInternalServerError)
				return
			}
			truncated = trunc
			if truncated {
				logger.Warn("HTTP log upload truncated at %d bytes (project=%s file=%s)", logs.MaxLogIngestBytes, project, fileName)
			}
			lines = strings.Split(string(bodyBytes), "\n")
		}

		if project == "" {
			project = "HTTPUpload"
		}
		if fileName == "" {
			fileName = "uploaded.log"
		}

		var cleanLines []string
		for _, l := range lines {
			trimmed := strings.TrimSpace(l)
			if trimmed != "" {
				cleanLines = append(cleanLines, trimmed)
			}
		}

		if len(cleanLines) == 0 {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":    "processed",
				"project":   project,
				"file_name": fileName,
				"anomalies": []logs.SemanticError{},
			})
			return
		}

		path := filepath.Join(cfg.LogsRoot, project, fileName)
		analyzer := logs.NewLogAnalyzerWithProject(r.Context(), path, project, cfg, dbClient, aiClient)

		anomalies, err := analyzer.ProcessBatchSync(r.Context(), cleanLines)
		if err != nil {
			logger.Error("HTTP log upload analysis error: %v", err)
			http.Error(w, fmt.Sprintf("Analysis failed: %v", err), http.StatusInternalServerError)
			return
		}

		if len(anomalies) > 0 {
			notifier := logs.NewNotifier(cfg, dbClient)
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("HTTP Upload Log Report for Project: %s (File: %s)\r\n\r\n", project, fileName))
			sb.WriteString(fmt.Sprintf("Detected %d errors/anomalies:\r\n\r\n", len(anomalies)))
			maxSeverity := 0
			for _, e := range anomalies {
				if e.Severity > maxSeverity {
					maxSeverity = e.Severity
				}
				sb.WriteString(fmt.Sprintf("Category: %s\r\n", e.Category))
				sb.WriteString(fmt.Sprintf("Severity: %d/10\r\n", e.Severity))
				if e.File != "" {
					sb.WriteString(fmt.Sprintf("File: %s\r\n", e.File))
				}
				if e.Cause != "" {
					sb.WriteString(fmt.Sprintf("Cause: %s\r\n", e.Cause))
				}
				sb.WriteString("Stack Trace:\r\n")
				sb.WriteString(e.StackTrace + "\r\n\r\n")
			}
			notifier.QueueNotification(r.Context(), project, fileName, len(anomalies), maxSeverity, sb.String())
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"status":    "processed",
			"project":   project,
			"file_name": fileName,
			"anomalies": anomalies,
		}
		if truncated {
			resp["truncated"] = true
			resp["warning"] = fmt.Sprintf("Body truncated at %d bytes; analysis covers the capped prefix only", logs.MaxLogIngestBytes)
		}
		json.NewEncoder(w).Encode(resp)
	}
}

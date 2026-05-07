package main

import (
	"context"
	"encoding/json"
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

	"github.com/deckonline/knowledge_mcp/internal/ai"
	"github.com/deckonline/knowledge_mcp/internal/auth"
	"github.com/deckonline/knowledge_mcp/internal/config"
	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/discovery"
	"github.com/deckonline/knowledge_mcp/internal/ingest/code"
	"github.com/deckonline/knowledge_mcp/internal/ingest/dynamic"
	"github.com/deckonline/knowledge_mcp/internal/ingest/git"
	"github.com/deckonline/knowledge_mcp/internal/ingest/logs"
	"github.com/deckonline/knowledge_mcp/internal/ingest/redmine"
	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/deckonline/knowledge_mcp/internal/optimization"
	"github.com/deckonline/knowledge_mcp/internal/search"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// AuthMiddleware injects the X-Redmine-API-Key into the request context for downstream services.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Redmine-API-Key")
		if key != "" {
			ctx := context.WithValue(r.Context(), auth.RedmineKeyContextKey, key)
			next.ServeHTTP(w, r.WithContext(ctx))
		} else {
			next.ServeHTTP(w, r)
		}
	})
}

func main() {
	if len(os.Args) < 2 {
		printHelp()
		return
	}

	cmd := os.Args[1]
	switch cmd {
	case "/run":
		// Service run mode: must use the service library to communicate with SCM
		// We shift the arguments to skip the command for flag parsing later in runServer
		if len(os.Args) > 1 {
			os.Args = append(os.Args[:1], os.Args[2:]...)
		}
		handleService("/run")
	case "run", "-run", "--run":
		// Normal interactive run
		// We shift the arguments to skip the command for flag parsing
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
	default:
		// If it's not a known command, it might be a flag or just wrong.
		// If it starts with - or --, we assume they want to run with flags directly?
		// But the request says "without params it will print a syntetic --help guide"
		// And add 3 commands.
		if strings.HasPrefix(cmd, "-") {
			runServer(context.Background())
		} else {
			fmt.Printf("Unknown command: %s\n", cmd)
			printHelp()
		}
	}
}

func printHelp() {
	binName := filepath.Base(os.Args[0])
	fmt.Println("Knowledge Server - MCP Knowledge Graph & Search")
	fmt.Println("\nUsage:")
	fmt.Printf("  %s <command> [options]\n", binName)
	fmt.Println("\nCommands:")
	fmt.Println("  run         Run the server interactively (standard MCP behavior)")
	fmt.Println("  install     Install as Windows Service 'knowledge-server'")
	fmt.Println("  uninstall   Uninstall the Windows Service")
	fmt.Println("\nOptions (used with run or as flags):")
	fmt.Println("  -port int        Port to listen on for SSE (default 8080)")
	fmt.Println("  -mode string     Mode: 'sse' or 'stdio' (default 'sse')")
	fmt.Println("  -scan            Discover git repositories in current/root directory (default true)")
	fmt.Println("  -root string     Root directory for discovery (default '.')")
	fmt.Println("  -log-file string Log file path")
	fmt.Println("  -log-level string Log level: DEBUG, INFO, WARN, ERROR (default 'INFO')")
	fmt.Println("  -config string   Path to config.json (searches current dir and exe dir by default)")
	fmt.Println("\nConfiguration Precedence:")
	fmt.Println("  1. Command Line Flags (highest)")
	fmt.Println("  2. Environment Variables")
	fmt.Println("  3. Config File (config.json)")
	fmt.Println("  4. Hardcoded Defaults (lowest)")
	fmt.Println("\nExample:")
	fmt.Printf("  %s run -port 9000 -mode sse\n", binName)
}

type redmineSearchToolResponse struct {
	Summary    string                      `json:"summary"`
	Issues     []redmine.Issue             `json:"issues"`
	TotalCount int                         `json:"total_count"`
	Offset     int                         `json:"offset"`
	Limit      int                         `json:"limit"`
	Compact    []string                    `json:"compact"`
	Filters    redmine.SearchIssuesParams  `json:"filters"`
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

func buildSearchParamsFromArgs(args map[string]interface{}) (redmine.SearchIssuesParams, error) {
	limit, err := getIntArg(args, "limit")
	if err != nil {
		return redmine.SearchIssuesParams{}, err
	}
	offset, err := getIntArg(args, "offset")
	if err != nil {
		return redmine.SearchIssuesParams{}, err
	}

	params := redmine.SearchIssuesParams{
		Query:        getStringArg(args, "query"),
		ProjectID:    getStringArg(args, "project_id"),
		StatusID:     getStringArg(args, "status_id"),
		TrackerID:    getStringArg(args, "tracker_id"),
		AssignedToID: getStringArg(args, "assigned_to_id"),
		AuthorID:     getStringArg(args, "author_id"),
		PriorityID:   getStringArg(args, "priority_id"),
		UpdatedFrom:  getStringArg(args, "updated_from"),
		UpdatedTo:    getStringArg(args, "updated_to"),
		Limit:        limit,
		Offset:       offset,
		Sort:         getStringArg(args, "sort"),
	}

	if err := redmine.ValidateSearchParams(params); err != nil {
		return redmine.SearchIssuesParams{}, err
	}

	return params, nil
}

func formatRedmineSearchResponse(result *redmine.SearchIssuesResult, params redmine.SearchIssuesParams, label string) (string, error) {
	compact := make([]string, 0, len(result.Issues))
	for _, idx := range result.Issues {
		compact = append(compact, fmt.Sprintf("[%d] %s (%s) - %s", idx.ID, idx.Subject, idx.Status.Name, idx.Author.Name))
	}

	payload := redmineSearchToolResponse{
		Summary:    label,
		Issues:     result.Issues,
		TotalCount: result.TotalCount,
		Offset:     result.Offset,
		Limit:      result.Limit,
		Compact:    compact,
		Filters:    params,
	}

	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}

	return string(b), nil
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
	logger.Info("Starting Knowledge Server (Mode: %s)...", cfg.Mode)
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
	s := server.NewMCPServer(
		"Knowledge Graph MCP",
		"1.1.0",
		server.WithLogging(),
	)

	// --- [NEW] Start Embedded DB ---
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

	// 6. Initialize Clients
	logger.Info("Initializing Gemini AI Client with %d keys...", len(cfg.GeminiKeys))
	var aiKeys []ai.KeyConfig
	for _, k := range cfg.GeminiKeys {
		// Use specific RPM if set, otherwise default
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
	aiClient := ai.NewClient(aiKeys, dbClient)

	// Start Batch Manager
	var batchManager *ai.BatchManager
	if dbClient != nil {
		batchManager = ai.NewBatchManager(dbClient, aiClient, cfg.DiscoveryRoot, cfg.MaxDeltaSize)
		batchManager.Start()
		defer batchManager.Stop()
	}

	logger.Info("Initializing Redmine Client at %s...", cfg.RedmineURL)
	redmineClient := redmine.NewClient(cfg.RedmineURL, cfg.RedmineKey)

	searchService := &search.Service{DB: dbClient, AI: aiClient}
	optimizer := optimization.NewOptimizer(dbClient, aiClient, searchService)

	// --- Check Connections ---
	if !aiClient.IsFunctional() {
		logger.Warn("⚠️  AI vectorization is DISABLED (no Gemini API keys found).")
		logger.Warn("   - Add keys to 'config.json' (gemini_keys: [\"...\"])")
		logger.Warn("   - Or set 'GEMINI_API_KEY' environment variable.")
	}
	if cfg.RedmineURL == "" {
		logger.Warn("⚠️  Redmine URL not configured. Issue tracking features will be limited.")
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
					if err := git.IngestRepo(ctx, dbClient, redmineClient, r, repoName, cfg.RedmineConcurrency); err != nil {
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
	ingestionManager := dynamic.NewProjectIngestionManager(cfg.DiscoveryRoot)
	if dbClient != nil {
		ingestionManager.ProcessJob = func(jobCtx context.Context, job dynamic.IngestionJob, repoPath string) error {
			repoName := job.ProjectName
			logger.Info("Background: Indexing Git history for %s...", repoName)
			if err := git.IngestRepo(jobCtx, dbClient, redmineClient, repoPath, repoName, cfg.RedmineConcurrency); err != nil {
				return fmt.Errorf("git ingestion error for %s: %w", repoName, err)
			}
			logger.Info("Background: Vectorizing codebase for %s...", repoName)
			if err := code.IngestCodebase(jobCtx, dbClient, aiClient, repoPath, repoName, cfg); err != nil {
				return fmt.Errorf("code ingestion error for %s: %w", repoName, err)
			}
			return nil
		}
	}

	// 7. Register Tools
	s.AddTool(mcp.NewTool("init_project",
		mcp.WithDescription("Initialize a repository session. Checks out the specific branch/commit and triggers ingestion if necessary."),
		mcp.WithString("project_name", mcp.Description("Name of the project (e.g. DeckOnLine)")),
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
		job := dynamic.IngestionJob{
			ProjectName: getStringArg(args, "project_name"),
			OriginURL:   getStringArg(args, "origin_url"),
			Branch:      getStringArg(args, "branch"),
			Commit:      getStringArg(args, "commit"),
			OnSyncDone: func(res dynamic.IngestionResult) {
				syncDone <- res
			},
			OnComplete: func(err error) {
				msg := fmt.Sprintf("Ingestion completed for %s", args["project_name"])
				if err != nil {
					msg = fmt.Sprintf("Ingestion failed for %s: %v", args["project_name"], err)
				}
				logger.Info(msg)
			},
		}

		ingestionManager.Enqueue(job)

		select {
		case res := <-syncDone:
			if res.Error != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Failed to sync: %v", res.Error)), nil
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
		ingestionManager.Enqueue(job)
		return mcp.NewToolResultText("Aggiornamento accodato con successo."), nil
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

		go func() {
			if err := optimizer.OptimizeLoop(ctx, iter); err != nil {
				logger.Error("Optimization failed: %v", err)
			}
		}()

		return mcp.NewToolResultText(fmt.Sprintf("Optimization started for %d iterations.", iter)), nil
	})

	// reinforce_path tool has been removed as it is now integrated into save_reasoning_outcome.

	// --- Redmine Direct Tools ---

	s.AddTool(mcp.NewTool("redmine_search_issues",
		mcp.WithDescription("Search matching issues in Redmine by text/subject or structured filters (status, assignee, etc)."),
		mcp.WithString("query", mcp.Description("Optional subject text query")),
		mcp.WithString("project_id", mcp.Description("Optional Redmine project id or identifier")),
		mcp.WithString("status_id", mcp.Description("Optional status filter (e.g. open, closed, *)")),
		mcp.WithString("tracker_id", mcp.Description("Optional tracker id")),
		mcp.WithString("assigned_to_id", mcp.Description("Optional assignee id (e.g. me, 15)")),
		mcp.WithString("author_id", mcp.Description("Optional author id")),
		mcp.WithString("priority_id", mcp.Description("Optional priority id")),
		mcp.WithString("updated_from", mcp.Description("Optional lower bound date (YYYY-MM-DD or RFC3339)")),
		mcp.WithString("updated_to", mcp.Description("Optional upper bound date (YYYY-MM-DD or RFC3339)")),
		mcp.WithNumber("limit", mcp.Description("Optional max results (1..100), default 20")),
		mcp.WithNumber("offset", mcp.Description("Optional pagination offset, default 0")),
		mcp.WithString("sort", mcp.Description("Optional sort (e.g. updated_on:desc)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: redmine_search_issues")
		if cfg.RedmineURL == "" {
			return mcp.NewToolResultError("Redmine not configured"), nil
		}
		args, _ := request.Params.Arguments.(map[string]interface{})
		params, err := buildSearchParamsFromArgs(args)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %v", err)), nil
		}
		if params.Limit == 0 {
			params.Limit = 20
		}
		if params.Sort == "" {
			params.Sort = "updated_on:desc"
		}

		// Pass context to use User Key if available
		result, err := redmineClient.SearchIssuesAdvanced(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Redmine error: %v", err)), nil
		}

		out, err := formatRedmineSearchResponse(result, params, "Redmine search results")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(out), nil
	})


	s.AddTool(mcp.NewTool("redmine_search_my_issues",
		mcp.WithDescription("Search Redmine issues assigned to the current user (assigned_to_id=me)."),
		mcp.WithString("query", mcp.Description("Optional subject text query")),
		mcp.WithString("status_id", mcp.Description("Optional status filter")),
		mcp.WithString("project_id", mcp.Description("Optional project filter")),
		mcp.WithString("tracker_id", mcp.Description("Optional tracker filter")),
		mcp.WithString("priority_id", mcp.Description("Optional priority filter")),
		mcp.WithString("updated_from", mcp.Description("Optional lower bound date (YYYY-MM-DD or RFC3339)")),
		mcp.WithString("updated_to", mcp.Description("Optional upper bound date (YYYY-MM-DD or RFC3339)")),
		mcp.WithNumber("limit", mcp.Description("Optional max results (1..100), default 20")),
		mcp.WithNumber("offset", mcp.Description("Optional pagination offset, default 0")),
		mcp.WithString("sort", mcp.Description("Optional sort, default updated_on:desc")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: redmine_search_my_issues")
		if cfg.RedmineURL == "" {
			return mcp.NewToolResultError("Redmine not configured"), nil
		}

		args, _ := request.Params.Arguments.(map[string]interface{})
		params, err := buildSearchParamsFromArgs(args)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %v", err)), nil
		}

		result, err := redmineClient.SearchMyIssues(ctx, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Redmine error: %v", err)), nil
		}

		params.AssignedToID = "me"
		if params.Sort == "" {
			params.Sort = "updated_on:desc"
		}
		if params.Limit == 0 {
			params.Limit = 20
		}

		out, err := formatRedmineSearchResponse(result, params, "Redmine my-issues search results")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}

		return mcp.NewToolResultText(out), nil
	})

	s.AddTool(mcp.NewTool("redmine_get_issue",
		mcp.WithDescription("Get detailed information for a specific Redmine issue."),
		mcp.WithString("id", mcp.Description("Issue ID")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: redmine_get_issue")
		if cfg.RedmineURL == "" {
			return mcp.NewToolResultError("Redmine not configured"), nil
		}
		args := request.Params.Arguments.(map[string]interface{})
		id, _ := args["id"].(string)

		// Pass context to use User Key if available
		issue, err := redmineClient.GetIssue(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Redmine error: %v", err)), nil
		}
		if issue == nil {
			return mcp.NewToolResultError("Issue not found"), nil
		}

		out := fmt.Sprintf("ID: %d\nSubject: %s\nStatus: %s\nTracker: %s\nAuthor: %s\nCreated: %s\n\n%s",
			issue.ID, issue.Subject, issue.Status.Name, issue.Tracker.Name, issue.Author.Name, issue.CreatedOn, issue.Description)
		return mcp.NewToolResultText(out), nil
	})

	s.AddTool(mcp.NewTool("redmine_update_issue",
		mcp.WithDescription("Update a Redmine issue (notes and selected fields)."),
		mcp.WithString("id", mcp.Description("Issue ID")),
		mcp.WithString("notes", mcp.Description("Optional notes/comment to add")),
		mcp.WithNumber("status_id", mcp.Description("Optional status ID")),
		mcp.WithNumber("priority_id", mcp.Description("Optional priority ID")),
		mcp.WithNumber("assigned_to_id", mcp.Description("Optional assignee user ID")),
		mcp.WithNumber("fixed_version_id", mcp.Description("Optional target version ID")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: redmine_update_issue")
		if cfg.RedmineURL == "" {
			return mcp.NewToolResultError("Redmine not configured"), nil
		}
		args, _ := request.Params.Arguments.(map[string]interface{})
		id := getStringArg(args, "id")
		if id == "" {
			return mcp.NewToolResultError("Invalid arguments: id is required"), nil
		}

		statusID, err := getIntArg(args, "status_id")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %v", err)), nil
		}
		priorityID, err := getIntArg(args, "priority_id")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %v", err)), nil
		}
		assignedToID, err := getIntArg(args, "assigned_to_id")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %v", err)), nil
		}
		fixedVersionID, err := getIntArg(args, "fixed_version_id")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid arguments: %v", err)), nil
		}

		update := redmine.UpdateIssueParams{
			Notes:          getStringArg(args, "notes"),
			StatusID:       statusID,
			PriorityID:     priorityID,
			AssignedToID:   assignedToID,
			FixedVersionID: fixedVersionID,
		}

		// ENFORCE: Update requires User Key
		if k, ok := ctx.Value(auth.RedmineKeyContextKey).(string); !ok || k == "" {
			return mcp.NewToolResultError("Permission denied: You must provide a valid X-Redmine-API-Key header to update issues."), nil
		}

		err = redmineClient.UpdateIssue(ctx, id, update)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Redmine error: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Issue #%s updated successfully.", id)), nil
	})

	s.AddTool(mcp.NewTool("redmine_search_users",
		mcp.WithDescription("Search Redmine users by name to find their IDs for assignment or filtering."),
		mcp.WithString("name", mcp.Description("Name to search for")),
		mcp.WithNumber("limit", mcp.Description("Optional limit (default 100)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: redmine_search_users")
		if cfg.RedmineURL == "" {
			return mcp.NewToolResultError("Redmine not configured"), nil
		}
		args, _ := request.Params.Arguments.(map[string]interface{})
		name := getStringArg(args, "name")
		limit, _ := getIntArg(args, "limit")
		if limit == 0 {
			limit = 100
		}

		res, err := redmineClient.GetUsers(ctx, name, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Redmine error: %v", err)), nil
		}
		
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("JSON marshal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(b)), nil
	})

	// 8. Start Server

	// Handle Signals in Main Thread
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	if cfg.Mode == "sse" {
		logger.Info("Starting SSE/Streamable HTTP server on port %d...", cfg.Port)
		sseServer := server.NewSSEServer(s)
		streamableServer := server.NewStreamableHTTPServer(s)

		// Standard HTTP server with graceful shutdown
		mux := http.NewServeMux()
		
		// Legacy SSE endpoints
		mux.Handle("/sse", sseServer.SSEHandler())
		mux.Handle("/message", sseServer.MessageHandler())

		// Streamable HTTP endpoints (MCP standard)
		mux.Handle("/mcp/", streamableServer)
		mux.Handle("/mcp", streamableServer)

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
		logger.Info("Knowledge Server is operational. Press Ctrl+C to stop.")
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
			if err := server.ServeStdio(s); err != nil {
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

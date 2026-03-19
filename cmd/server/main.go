package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	"github.com/deckonline/knowledge_mcp/internal/ingest/git"
	"github.com/deckonline/knowledge_mcp/internal/ingest/logs"
	"github.com/deckonline/knowledge_mcp/internal/ingest/redmine"
	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/deckonline/knowledge_mcp/internal/optimization"
	"github.com/deckonline/knowledge_mcp/internal/search"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// AuthMiddleware extracts the X-Redmine-API-Key header and puts it in the context
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
	case "/run", "run", "-run", "--run":
		// Normal interactive run or service run
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

	// Configure structured logging (used by dependencies like mcp-go) to use TextHandler (Console friendly)
	// We map it to the same output writer if possible, but for now stdout/stderr is fine.
	// mcp-go uses slog.Default().
	var logHandler slog.Handler
	logHandler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(logHandler))

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
	dbPort := 8000

	logger.Debug("Attempting to start embedded database...")
	proc, err := db.StartEmbedded(cfg.DBUser, cfg.DBPassword, "project.db", dbPort, cfg.DBAutoUpdate)
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
		if err := db.InitSchema(dbClient); err != nil {
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
			Key:   k.Key,
			RPM:   rpm,
			TPM:   k.TPM,
			RPD:   k.RPD,
			Owner: k.Owner,
		})
	}
	aiClient := ai.NewClient(aiKeys, dbClient)

	// Start Batch Manager
	var batchManager *ai.BatchManager
	if dbClient != nil {
		batchManager = ai.NewBatchManager(dbClient, aiClient)
		batchManager.Start()
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

					logger.Info("Background: Indexing Git history for %s...", r)
					if err := git.IngestRepo(dbClient, redmineClient, r, cfg.RedmineConcurrency); err != nil {
						logger.Error("Background: Git ingestion error for %s: %v", r, err)
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

					logger.Info("Background: Vectorizing codebase for %s...", r)
					if err := code.IngestCodebase(ctx, dbClient, aiClient, r, cfg); err != nil {
						logger.Error("Background: Code ingestion error for %s: %v", r, err)
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

	// 7. Register Tools
	s.AddTool(mcp.NewTool("ingest_git",
		mcp.WithDescription("Trigger git ingestion for repositories. If no path is provided, ingest all discovered/configured repos."),
		mcp.WithString("path", mcp.Description("Optional specific repo path to ingest")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ingest_git")
		if dbClient == nil {
			return mcp.NewToolResultError("Database not connected"), nil
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
			if err := git.IngestRepo(dbClient, redmineClient, r, cfg.RedmineConcurrency); err != nil {
				output.WriteString(fmt.Sprintf("Error ingesting %s: %v\n", r, err))
			} else {
				output.WriteString(fmt.Sprintf("Successfully ingested %s\n", r))
			}
		}
		return mcp.NewToolResultText(output.String()), nil
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
			if err := code.IngestCodebase(ctx, dbClient, aiClient, r, cfg); err != nil {
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
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ask_project")
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Invalid arguments"), nil
		}
		query, _ := args["query"].(string)

		result, err := searchService.AskProjectAgentic(ctx, query)
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
			if err := optimizer.OptimizeLoop(context.Background(), iter); err != nil {
				logger.Error("Optimization failed: %v", err)
			}
		}()

		return mcp.NewToolResultText(fmt.Sprintf("Optimization started for %d iterations.", iter)), nil
	})

	s.AddTool(mcp.NewTool("reinforce_path",
		mcp.WithDescription("Reinforce a specific connection in the knowledge graph based on user feedback."),
		mcp.WithString("source", mcp.Description("ID of the source node (e.g., commit:xyz)")),
		mcp.WithString("target", mcp.Description("ID of the target node (e.g., issue:123)")),
		mcp.WithNumber("score", mcp.Description("Feedback score (positive to reinforce, negative to decay)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: reinforce_path")
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Invalid arguments"), nil
		}

		source, _ := args["source"].(string)
		target, _ := args["target"].(string)
		score, _ := args["score"].(float64)

		err := searchService.ReinforcePath(source, target, score)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Reinforcement failed: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Successfully reinforced path %s -> %s (Delta: %.2f)", source, target, score)), nil
	})

	// --- Redmine Direct Tools ---

	s.AddTool(mcp.NewTool("redmine_search_issues",
		mcp.WithDescription("Search matching issues in Redmine by text/subject."),
		mcp.WithString("query", mcp.Description("Text to search for")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: redmine_search_issues")
		if cfg.RedmineURL == "" {
			return mcp.NewToolResultError("Redmine not configured"), nil
		}
		args := request.Params.Arguments.(map[string]interface{})
		query, _ := args["query"].(string)

		// Pass context to use User Key if available
		issues, err := redmineClient.SearchIssues(ctx, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Redmine error: %v", err)), nil
		}

		var out strings.Builder
		for _, idx := range issues {
			out.WriteString(fmt.Sprintf("[%d] %s (%s) - %s\n", idx.ID, idx.Subject, idx.Status.Name, idx.Author.Name))
		}
		return mcp.NewToolResultText(out.String()), nil
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
		mcp.WithDescription("Update a Redmine issue (e.g. add notes)."),
		mcp.WithString("id", mcp.Description("Issue ID")),
		mcp.WithString("notes", mcp.Description("Notes/Comment to add")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: redmine_update_issue")
		if cfg.RedmineURL == "" {
			return mcp.NewToolResultError("Redmine not configured"), nil
		}
		args := request.Params.Arguments.(map[string]interface{})
		id, _ := args["id"].(string)
		notes, _ := args["notes"].(string)

		// ENFORCE: Update requires User Key
		if k, ok := ctx.Value(auth.RedmineKeyContextKey).(string); !ok || k == "" {
			return mcp.NewToolResultError("Permission denied: You must provide a valid X-Redmine-API-Key header to update issues."), nil
		}

		err := redmineClient.UpdateIssue(ctx, id, notes)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Redmine error: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Issue #%s updated successfully.", id)), nil
	})

	// 8. Start Server

	// Handle Signals in Main Thread
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	if cfg.Mode == "sse" {
		logger.Info("Starting SSE server on port %d...", cfg.Port)
		sseServer := server.NewSSEServer(s)

		// Run Server in Goroutine
		go func() {
			mux := http.NewServeMux()
			mux.Handle("/sse", sseServer.SSEHandler())
			mux.Handle("/message", sseServer.MessageHandler())

			// Wrap the entire mux with AuthMiddleware
			handler := AuthMiddleware(mux)

			server := &http.Server{
				Addr:    fmt.Sprintf(":%d", cfg.Port),
				Handler: handler,
			}

			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("Server error: %v", err)
				sigChan <- syscall.SIGTERM
			}
		}()

		// Wait for signal or context cancellation
		select {
		case <-sigChan:
			logger.Info("Received signal, shutting down...")
		case <-ctx.Done():
			logger.Info("Context cancelled, shutting down...")
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

	// 3. Close DB Connection (now safe as no workers are using it)
	if dbClient != nil {
		logger.Info("Closing database connection...")
		dbClient.Close()
	}

	// Give the websocket connection time to close properly before killing the DB process
	time.Sleep(500 * time.Millisecond)

	// 4. Stop DB Process
	if dbProcess != nil {
		logger.Info("Stopping embedded database process...")
		if err := dbProcess.Stop(); err != nil {
			logger.Error("Error stopping database: %v", err)
		}
	}
}

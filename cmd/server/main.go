package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/deckonline/knowledge_mcp/internal/ai"
	"github.com/deckonline/knowledge_mcp/internal/auth"
	"github.com/deckonline/knowledge_mcp/internal/config"
	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/discovery"
	"github.com/deckonline/knowledge_mcp/internal/ingest/code"
	"github.com/deckonline/knowledge_mcp/internal/ingest/git"
	"github.com/deckonline/knowledge_mcp/internal/ingest/redmine"
	"github.com/deckonline/knowledge_mcp/internal/logger"
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
	// 1. Load Config (Defaults + Env)
	cfg := config.Load()

	// 2. Parse Flags (Overrides)
	portFlag := flag.Int("port", cfg.Port, "Port to listen on for SSE")
	modeFlag := flag.String("mode", cfg.Mode, "Mode: 'sse' or 'stdio'")
	scanFlag := flag.Bool("scan", cfg.AutoScan, "Discover git repositories in current/root directory")
	rootFlag := flag.String("root", cfg.DiscoveryRoot, "Root directory for discovery")
	logFileFlag := flag.String("log-file", cfg.LogFile, "Log file path")
	logLevelFlag := flag.String("log-level", cfg.LogLevel, "Log level: DEBUG, INFO, WARN, ERROR")
	
	flag.Parse()

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
	proc, err := db.StartEmbedded(cfg.DBUser, cfg.DBPassword, "project.db", dbPort)
	if err != nil {
		logger.Info("Note: Could not start embedded database (or it is already running): %v", err)
	} else {
		dbProcess = proc
		logger.Info("Embedded SurrealDB started successfully.")
	}

	// 5. Connect DB
	logger.Info("Connecting to SurrealDB at %s...", cfg.DBUrl)
	dbClient, err := db.NewClient(cfg.DBUrl, cfg.DBNamespace, cfg.DBDatabase, cfg.DBUser, cfg.DBPassword)
	if err != nil {
		logger.Error("CRITICAL: Failed to connect to SurrealDB: %v", err)
	} else {
		defer dbClient.Close()
		logger.Info("Successfully connected to SurrealDB.")
	}

	// 6. Initialize Clients
	logger.Info("Initializing Gemini AI Client with %d keys...", len(cfg.GeminiKeys))
	aiClient := ai.NewClient(cfg.GeminiKeys, cfg.GeminiRPM)
	
	logger.Info("Initializing Redmine Client at %s...", cfg.RedmineURL)
	redmineClient := redmine.NewClient(cfg.RedmineURL, cfg.RedmineKey)
	
	searchService := &search.Service{DB: dbClient, AI: aiClient}

	// --- Check Connections ---
	if len(cfg.GeminiKeys) == 0 {
		logger.Warn("No GEMINI_API_KEY provided. AI features will be disabled.")
	}
	if cfg.RedmineURL == "" {
		logger.Warn("No REDMINE_URL provided. Issue tracking features will be limited.")
	}

	// --- [NEW] Background Indexing ---
	if len(activeRepos) > 0 {
		logger.Info("Triggering background indexing for %d repositories...", len(activeRepos))
		go func() {
			for _, r := range activeRepos {
				logger.Info("Background: Indexing Git history for %s...", r)
				if err := git.IngestRepo(dbClient, redmineClient, r); err != nil {
					logger.Error("Background: Git ingestion error for %s: %v", r, err)
				}
				
				logger.Info("Background: Vectorizing codebase for %s...", r)
				if err := code.IngestCodebase(dbClient, aiClient, r); err != nil {
					logger.Error("Background: Code ingestion error for %s: %v", r, err)
				}
			}
			logger.Info("Background: Initial indexing complete.")
		}()
	}

	// 7. Register Tools
	s.AddTool(mcp.NewTool("ingest_git",
		mcp.WithDescription("Trigger git ingestion for repositories. If no path is provided, ingest all discovered/configured repos."),
		mcp.WithString("path", mcp.Description("Optional specific repo path to ingest")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ingest_git")
		if dbClient == nil { return mcp.NewToolResultError("Database not connected"), nil }
		
		targets := activeRepos
		args, ok := request.Params.Arguments.(map[string]interface{})
		if ok {
			if p, ok := args["path"].(string); ok && p != "" {
				targets = []string{p}
			}
		}

		var output strings.Builder
		for _, r := range targets {
			if err := git.IngestRepo(dbClient, redmineClient, r); err != nil {
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
		if dbClient == nil { return mcp.NewToolResultError("Database not connected"), nil }
		if len(cfg.GeminiKeys) == 0 { return mcp.NewToolResultError("No AI Keys configured"), nil }
		
		targets := activeRepos
		args, ok := request.Params.Arguments.(map[string]interface{})
		if ok {
			if p, ok := args["path"].(string); ok && p != "" {
				targets = []string{p}
			}
		}
		
		var output strings.Builder
		for _, r := range targets {
			if err := code.IngestCodebase(dbClient, aiClient, r); err != nil {
				output.WriteString(fmt.Sprintf("Error scanning %s: %v\n", r, err))
			} else {
				output.WriteString(fmt.Sprintf("Successfully scanned %s\n", r))
			}
		}
		return mcp.NewToolResultText(output.String()), nil
	})

	s.AddTool(mcp.NewTool("ask_project",
		mcp.WithDescription("Ask a natural language question about the project history and code."),
		mcp.WithString("query", mcp.Description("The question (e.g., 'Why was login changed?')")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: ask_project")
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok { return mcp.NewToolResultError("Invalid arguments"), nil }
		query, _ := args["query"].(string)

		results, err := searchService.AskProject(query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
		}

		var out strings.Builder
		out.WriteString(fmt.Sprintf("Found %d results for '%s':\n\n", len(results), query))
		for i, r := range results {
			out.WriteString(fmt.Sprintf("%d. [%s] %s (Score: %.2f)\n%s\n\n", i+1, r.Type, r.ID, r.Score, r.Content))
		}
		return mcp.NewToolResultText(out.String()), nil
	})

	// --- Redmine Direct Tools ---

	s.AddTool(mcp.NewTool("redmine_search_issues",
		mcp.WithDescription("Search matching issues in Redmine by text/subject."),
		mcp.WithString("query", mcp.Description("Text to search for")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("MCP Tool Call: redmine_search_issues")
		if cfg.RedmineURL == "" { return mcp.NewToolResultError("Redmine not configured"), nil }
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
		if cfg.RedmineURL == "" { return mcp.NewToolResultError("Redmine not configured"), nil }
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
		if cfg.RedmineURL == "" { return mcp.NewToolResultError("Redmine not configured"), nil }
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
	
	// Ensure DB is stopped on exit (even if via signal)
	defer func() {
		if dbProcess != nil {
			logger.Info("Cleaning up embedded database...")
			if err := dbProcess.Stop(); err != nil {
				logger.Error("Error stopping database: %v", err)
			}
		}
	}()

	// Context for graceful shutdown of other components if needed
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

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

		// Wait for signal
		<-sigChan
		logger.Info("Shutting down...")

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
		
		<-sigChan
		logger.Info("Shutting down...")
	}
}

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/deckonline/knowledge_mcp/internal/ai"
	"github.com/deckonline/knowledge_mcp/internal/config"
	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/discovery"
	"github.com/deckonline/knowledge_mcp/internal/ingest/code"
	"github.com/deckonline/knowledge_mcp/internal/ingest/git"
	"github.com/deckonline/knowledge_mcp/internal/ingest/redmine"
	"github.com/deckonline/knowledge_mcp/internal/search"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	// 1. Load Config (Defaults + Env)
	cfg := config.Load()

	// 2. Parse Flags (Overrides)
	portFlag := flag.Int("port", cfg.Port, "Port to listen on for SSE")
	modeFlag := flag.String("mode", cfg.Mode, "Mode: 'sse' or 'stdio'")
	scanFlag := flag.Bool("scan", cfg.AutoScan, "Discover git repositories in current/root directory")
	rootFlag := flag.String("root", cfg.DiscoveryRoot, "Root directory for discovery")
	
	flag.Parse()

	cfg.Port = *portFlag
	cfg.Mode = *modeFlag
	cfg.AutoScan = *scanFlag
	cfg.DiscoveryRoot = *rootFlag
	
	// 3. Discovery Logic
	log.Printf("Starting Knowledge Server (Mode: %s)...", cfg.Mode)
	var activeRepos []string
	
	if cfg.AutoScan {
		log.Printf("Scanning for repositories in %s...", cfg.DiscoveryRoot)
		scanner := discovery.NewScanner(cfg.DiscoveryRoot)
		repos, err := scanner.Scan()
		if err != nil {
			log.Printf("Warning: Discovery failed: %v", err)
		} else {
			activeRepos = append(activeRepos, repos...)
			log.Printf("Discovered %d repositories.", len(repos))
		}
	}
	// Append any manually configured repos (e.g. from Env CSV if we added that, currently only code/flags)
	if len(cfg.GitRepos) > 0 {
		activeRepos = append(activeRepos, cfg.GitRepos...)
	}

	// 4. Initialize MCP Server
	s := server.NewMCPServer(
		"Knowledge Graph MCP",
		"1.1.0",
		server.WithLogging(),
	)

	// --- [NEW] Start Embedded DB ---
	// We attempt to start ./surreal.exe if it exists.
	// We assume port 8000 for the DB based on default config.
	var dbProcess *db.ProcessManager
	dbPort := 8000 // Default SurrealDB port
	
	// Check if we should auto-start (simple check: does binary exist?)
	// We use the configured User/Pass for startup as well.
	proc, err := db.StartEmbedded(cfg.DBUser, cfg.DBPassword, "project.db", dbPort)
	if err != nil {
		// Log but don't fatal, maybe it's already running external to us?
		// But if it failed because implicit binary was missing, that's fine too.
		log.Printf("Note: Could not start embedded database (or it is already running): %v", err)
	} else {
		dbProcess = proc
		log.Println("Embedded SurrealDB started successfully.")
	}

	// 5. Connect DB
	dbClient, err := db.NewClient(cfg.DBUrl, cfg.DBNamespace, cfg.DBDatabase, cfg.DBUser, cfg.DBPassword)
	if err != nil {
		log.Printf("Warning: Failed to connect to SurrealDB: %v", err)
	} else {
		defer dbClient.Close()
		log.Println("Connected to SurrealDB.")
	}

	// 6. Initialize Clients
	aiClient := ai.NewClient(cfg.GeminiKeys, cfg.GeminiRPM)
	redmineClient := redmine.NewClient(cfg.RedmineURL, cfg.RedmineKey)
	searchService := &search.Service{DB: dbClient, AI: aiClient}

	// --- Check Connections ---
	if len(cfg.GeminiKeys) == 0 {
		log.Println("Warning: No GEMINI_API_KEY provided. AI features will be disabled.")
	}
	if cfg.RedmineURL == "" {
		log.Println("Warning: No REDMINE_URL provided. Issue tracking features will be limited.")
	}

	// 7. Register Tools
	// ... (Tools registration code remains here, collapsed for brevity in this view) ...
	// RE-INSERTING TOOLS REGISTRATION LOGIC TO ENSURE CONTINUITY
	// (Since replace_file_content replaces a block, I must ensure I don't lose the tools if I targeted a large block.
	// However, I am only replacing a small chunk around step 5. 
	// Wait, the previous view showed lines 63-253. I must be careful not to delete the tools.)
	// CHECKING TARGET CONTENT AGAIN.

	// I will refine the target content to be safer/smaller scope or use multi_replace.


	// 7. Register Tools

	// --- Knowledge Tools ---

	s.AddTool(mcp.NewTool("ingest_git",
		mcp.WithDescription("Trigger git ingestion for repositories. If no path is provided, ingest all discovered/configured repos."),
		mcp.WithString("path", mcp.Description("Optional specific repo path to ingest")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		if cfg.RedmineURL == "" { return mcp.NewToolResultError("Redmine not configured"), nil }
		args := request.Params.Arguments.(map[string]interface{})
		query, _ := args["query"].(string)

		issues, err := redmineClient.SearchIssues(query)
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
		if cfg.RedmineURL == "" { return mcp.NewToolResultError("Redmine not configured"), nil }
		args := request.Params.Arguments.(map[string]interface{})
		id, _ := args["id"].(string)

		issue, err := redmineClient.GetIssue(id)
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
		if cfg.RedmineURL == "" { return mcp.NewToolResultError("Redmine not configured"), nil }
		args := request.Params.Arguments.(map[string]interface{})
		id, _ := args["id"].(string)
		notes, _ := args["notes"].(string)

		err := redmineClient.UpdateIssue(id, notes)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Redmine error: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Issue #%s updated successfully.", id)), nil
	})


	// 8. Start Server
	
	// Ensure DB is stopped on exit (even if via signal)
	defer func() {
		if dbProcess != nil {
			log.Println("Cleaning up embedded database...")
			if err := dbProcess.Stop(); err != nil {
				log.Printf("Error stopping database: %v", err)
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
		log.Printf("Starting SSE server on port %d...", cfg.Port)
		sseServer := server.NewSSEServer(s) 
		
		// Run Server in Goroutine
		go func() {
			if err := sseServer.Start(fmt.Sprintf(":%d", cfg.Port)); err != nil {
				log.Printf("Server error: %v", err)
				// If server fails to start, we should exit
				sigChan <- syscall.SIGTERM
			}
		}()

		// Wait for signal
		<-sigChan
		log.Println("Shutting down...")
		// Since mcp-go doesn't easily expose Shutdown(), we just rely on main exiting 
		// which closes the listener. The crucial part is falling through to 'defer' above.

	} else {
		log.Println("Starting STDIO server...")
		// STDIO usually blocks until stdin closes
		go func() {
			if err := server.ServeStdio(s); err != nil {
				log.Printf("Server error: %v", err)
			}
			// If stdio interaction ends, we assume done
			sigChan <- syscall.SIGTERM
		}()
		
		<-sigChan
		log.Println("Shutting down...")
	}
}

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

	// "time" // Removed if not used

	"github.com/deckonline/knowledge_mcp/internal/ai"
	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/ingest/code"
	"github.com/deckonline/knowledge_mcp/internal/ingest/git"
	"github.com/deckonline/knowledge_mcp/internal/ingest/redmine"
	"github.com/deckonline/knowledge_mcp/internal/search"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var (
	port      = flag.Int("port", 3030, "Port to listen on for SSE")
	mode      = flag.String("mode", "sse", "Mode: 'sse' or 'stdio'")
	repos     = flag.String("repos", ".", "Comma-separated list of git repositories to analyze")
	rpm       = flag.Int("rpm", 60, "Rate limit (Requests Per Minute) for Gemini API")
)

func main() {
	flag.Parse()

	// 1. Initialize MCP Server
	s := server.NewMCPServer(
		"Knowledge Graph MCP",
		"1.0.0",
		server.WithLogging(),
	)

	// --- Custom Logic ---
	// TODO: Load from env/flags
	dbClient, err := db.NewClient("ws://localhost:8000/rpc", "deckonline", "analysis", "root", "root")
	if err != nil {
		log.Printf("Warning: Failed to connect to SurrealDB: %v", err)
	} else {
		defer dbClient.Close()
		log.Println("Connected to SurrealDB.")
		
		// Init Schema
		// For simplicity, we run the init SQL directly
		// In prod, check if migration needed
		// _, err = dbClient.Execute(schema.GenerateInitSQL())
		// if err != nil {
		// 	 log.Printf("Schema init error: %v", err)
		// }
	}

	// 2. Register Tools
	
	// Init AI Client
	apiKey := os.Getenv("GEMINI_API_KEY")
	aiClient := ai.NewClient(apiKey, *rpm)

	// Init Redmine Client
	redmineURL := os.Getenv("REDMINE_URL")
	redmineKey := os.Getenv("REDMINE_API_KEY")
	redmineClient := redmine.NewClient(redmineURL, redmineKey)

	// Tool: Ingest Git
	s.AddTool(mcp.NewTool("ingest_git",
		mcp.WithDescription("Trigger git ingestion for configured repositories"),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if dbClient == nil {
			return mcp.NewToolResultError("Database not connected"), nil
		}
		
		repoList := strings.Split(*repos, ",")
		var output strings.Builder
		
		for _, r := range repoList {
			r = strings.TrimSpace(r)
			if r == "" { continue }
			
			if err := git.IngestRepo(dbClient, r); err != nil {
				output.WriteString(fmt.Sprintf("Error ingesting %s: %v\n", r, err))
			} else {
				output.WriteString(fmt.Sprintf("Successfully ingested %s\n", r))
			}
		}
		
		return mcp.NewToolResultText(output.String()), nil
	})

	// Tool: Ingest Codebase (RAG)
	s.AddTool(mcp.NewTool("ingest_code",
		mcp.WithDescription("Trigger codebase vectorization (Delta RAG)"),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if dbClient == nil {
			return mcp.NewToolResultError("Database not connected"), nil
		}
		if apiKey == "" {
			return mcp.NewToolResultError("GEMINI_API_KEY not set"), nil
		}
		
		repoList := strings.Split(*repos, ",")
		var output strings.Builder
		
		for _, r := range repoList {
			r = strings.TrimSpace(r)
			if r == "" { continue }
			
			if err := code.IngestCodebase(dbClient, aiClient, r); err != nil {
				output.WriteString(fmt.Sprintf("Error scanning %s: %v\n", r, err))
			} else {
				output.WriteString(fmt.Sprintf("Successfully scanned %s\n", r))
			}
		}
		
		return mcp.NewToolResultText(output.String()), nil
	})

	// Tool: Ingest Redmine (Intent)
	s.AddTool(mcp.NewTool("ingest_redmine",
		mcp.WithDescription("Trigger Redmine issues ingestion (Intent Layer)"),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if dbClient == nil {
			return mcp.NewToolResultError("Database not connected"), nil
		}
		if redmineURL == "" {
			return mcp.NewToolResultError("REDMINE_URL not set"), nil
		}
		
		if err := redmineClient.IngestIssues(dbClient); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error ingesting issues: %v", err)), nil
		}
		
		return mcp.NewToolResultText("Successfully ingested Redmine issues."), nil
	})

	// Init Search Service
	searchService := &search.Service{DB: dbClient, AI: aiClient}

	// Tool: Ask Project (Unified Interface)
	s.AddTool(mcp.NewTool("ask_project",
		mcp.WithDescription("Ask a natural language question about the project history and code."),
		mcp.WithString("query", mcp.Description("The question (e.g., 'Why was login changed?')")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Invalid arguments"), nil
		}
		query, _ := args["query"].(string)

		results, err := searchService.AskProject(query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
		}

		// Format output as text (since MCP handles text best for now)
		var out strings.Builder
		out.WriteString(fmt.Sprintf("Found %d results for '%s':\n\n", len(results), query))
		for i, r := range results {
			out.WriteString(fmt.Sprintf("%d. [%s] %s (Score: %.2f)\n%s\n\n", i+1, r.Type, r.ID, r.Score, r.Content))
		}
		
		return mcp.NewToolResultText(out.String()), nil
	})

	s.AddTool(mcp.NewTool("ping",
		mcp.WithDescription("Check if server is alive"),
	), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("pong"), nil
	})

	// 3. Start Server
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Shutting down...")
		cancel()
	}()

	if *mode == "sse" {
		log.Printf("Starting SSE server on port %d...", *port)
		// NewSSEServer(s, options...) - Removing URL string arg causing error
		sseServer := server.NewSSEServer(s) 
		if err := sseServer.Start(fmt.Sprintf(":%d", *port)); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	} else {
		log.Println("Starting STDIO server...")
		if err := server.ServeStdio(s); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}
	
	log.Println("Server stopped.")
}

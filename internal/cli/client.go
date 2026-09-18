package cli

import (
	"flag"
	"fmt"
	"os"
)

// ToolCallPayload is the MCP tools/call body built by CLI subcommands.
type ToolCallPayload struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ExecuteCLI dispatches terminal subcommands to MCP tools.
func ExecuteCLI(args []string) {
	if len(args) == 0 {
		PrintCLIHelp()
		return
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "help", "-h", "--help":
		PrintCLIHelp()
	case "ask":
		handleAsk(subArgs)
	case "ingest":
		handleIngest(subArgs)
	case "ticket":
		handleTicket(subArgs)
	case "pr":
		handlePR(subArgs)
	case "logs":
		handleLogs(subArgs)
	case "credentials":
		handleCredentials(subArgs)
	case "memory":
		handleMemory(subArgs)
	case "outcome":
		handleOutcome(subArgs)
	case "optimize":
		handleOptimize(subArgs)
	default:
		fmt.Printf("Unknown command: %s\n", sub)
		PrintCLIHelp()
		os.Exit(1)
	}
}

// PrintCLIHelp prints CLI usage.
func PrintCLIHelp() {
	fmt.Println("Ibis Assistant CLI (MCP Streamable HTTP POST /mcp):")
	fmt.Println("  ibis-assistant <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  ask          Ask a reasoning question about indexed code")
	fmt.Println("  ingest       Sync/index code or Git history")
	fmt.Println("  ticket       Tickets on Redmine / Jira / Azure DevOps")
	fmt.Println("  pr           Pull requests")
	fmt.Println("  logs         Analyze log text")
	fmt.Println("  credentials  Persist Git credentials")
	fmt.Println("  memory       Collaborative project memories")
	fmt.Println("  outcome      Save reasoning outcomes")
	fmt.Println("  optimize     RAFT knowledge-base optimization")
	fmt.Println("\nUse 'ibis-assistant <command> --help' for per-command flags.")
}

func handleAsk(args []string) {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	branch := fs.String("branch", "", "Context branch or commit")
	fs.Parse(args)

	if len(fs.Args()) == 0 {
		fmt.Println("Error: provide a question.")
		fmt.Println("Usage: ibis-assistant ask \"your question\" [--branch <branch>]")
		os.Exit(1)
	}

	query := fs.Arg(0)
	payload := ToolCallPayload{
		Name: "ask_project",
		Arguments: map[string]interface{}{
			"query": query,
		},
	}
	if *branch != "" {
		payload.Arguments["branch_or_commit"] = *branch
	}

	callServerTool(payload)
}

func handleIngest(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: specify what to index (code or git).")
		fmt.Println("Usage:")
		fmt.Println("  ibis-assistant ingest code [--path <path>]")
		fmt.Println("  ibis-assistant ingest git --name <project> --url <git_url> --branch <branch> [--commit <hash>]")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "code":
		fs := flag.NewFlagSet("ingest code", flag.ExitOnError)
		path := fs.String("path", "", "Local repository path")
		fs.Parse(subArgs)

		payload := ToolCallPayload{
			Name:      "ingest_code",
			Arguments: map[string]interface{}{},
		}
		if *path != "" {
			payload.Arguments["path"] = *path
		}
		callServerTool(payload)

	case "git":
		fs := flag.NewFlagSet("ingest git", flag.ExitOnError)
		name := fs.String("name", "", "Project name (required)")
		url := fs.String("url", "", "Git origin URL (required)")
		branch := fs.String("branch", "", "Branch to track (required)")
		commit := fs.String("commit", "", "Specific commit (optional)")
		fs.Parse(subArgs)

		if *name == "" || *url == "" || *branch == "" {
			fmt.Println("Error: --name, --url, and --branch are required for Git ingest.")
			os.Exit(1)
		}

		payload := ToolCallPayload{
			Name: "init_project",
			Arguments: map[string]interface{}{
				"project_name": *name,
				"origin_url":   *url,
				"branch":       *branch,
			},
		}
		if *commit != "" {
			payload.Arguments["commit"] = *commit
		}
		callServerTool(payload)

	default:
		fmt.Printf("Unknown ingest type: %s\n", sub)
		os.Exit(1)
	}
}

func handleTicket(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: specify a ticket action (search, my-issues, get, create, update, comment, assign, transition, resolve, close, reopen, list-statuses, list-projects).")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "search":
		fs := flag.NewFlagSet("ticket search", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider (redmine/jira/azure_devops)")
		query := fs.String("query", "", "Text search query")
		projectKey := fs.String("project", "", "Project key")
		status := fs.String("status", "", "Status")
		limit := fs.Int("limit", 20, "Result limit")
		fs.Parse(subArgs)

		payload := ToolCallPayload{
			Name: "ticket_search",
			Arguments: map[string]interface{}{
				"limit": float64(*limit),
			},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		if *query != "" {
			payload.Arguments["query"] = *query
		}
		if *projectKey != "" {
			payload.Arguments["project_key"] = *projectKey
		}
		if *status != "" {
			payload.Arguments["status"] = *status
		}
		callServerTool(payload)

	case "my-issues":
		fs := flag.NewFlagSet("ticket my-issues", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider (redmine/jira/azure_devops)")
		projectKey := fs.String("project", "", "Project key")
		fs.Parse(subArgs)

		payload := ToolCallPayload{
			Name:      "ticket_search_my",
			Arguments: map[string]interface{}{},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		if *projectKey != "" {
			payload.Arguments["project_key"] = *projectKey
		}
		callServerTool(payload)

	case "get":
		if len(subArgs) == 0 {
			fmt.Println("Error: ticket ID is required.")
			os.Exit(1)
		}
		id := subArgs[0]
		fs := flag.NewFlagSet("ticket get", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Project key")
		fs.Parse(subArgs[1:])

		payload := ToolCallPayload{
			Name: "ticket_get",
			Arguments: map[string]interface{}{
				"id": id,
			},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		if *projectKey != "" {
			payload.Arguments["project_key"] = *projectKey
		}
		callServerTool(payload)

	case "create":
		fs := flag.NewFlagSet("ticket create", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Project key (required)")
		title := fs.String("title", "", "Ticket title (required)")
		desc := fs.String("desc", "", "Description")
		priority := fs.String("priority", "", "Priority")
		fs.Parse(subArgs)

		if *projectKey == "" || *title == "" {
			fmt.Println("Error: --project and --title are required.")
			os.Exit(1)
		}

		payload := ToolCallPayload{
			Name: "ticket_create",
			Arguments: map[string]interface{}{
				"project_key": *projectKey,
				"title":       *title,
			},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		if *desc != "" {
			payload.Arguments["description"] = *desc
		}
		if *priority != "" {
			payload.Arguments["priority"] = *priority
		}
		callServerTool(payload)

	case "update":
		if len(subArgs) == 0 {
			fmt.Println("Error: ticket ID is required.")
			os.Exit(1)
		}
		id := subArgs[0]
		fs := flag.NewFlagSet("ticket update", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Project key")
		notes := fs.String("notes", "", "Comment or notes")
		status := fs.String("status", "", "Status")
		assignee := fs.String("assignee", "", "Assignee")
		fs.Parse(subArgs[1:])

		payload := ToolCallPayload{
			Name: "ticket_update",
			Arguments: map[string]interface{}{
				"id": id,
			},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		if *projectKey != "" {
			payload.Arguments["project_key"] = *projectKey
		}
		if *notes != "" {
			payload.Arguments["notes"] = *notes
		}
		if *status != "" {
			payload.Arguments["status"] = *status
		}
		if *assignee != "" {
			payload.Arguments["assignee"] = *assignee
		}
		callServerTool(payload)

	case "comment":
		if len(subArgs) < 2 {
			fmt.Println("Error: ticket ID and comment are required.")
			fmt.Println("Usage: ibis-assistant ticket comment <id> \"comment\" [--provider <p>] [--project <k>]")
			os.Exit(1)
		}
		id := subArgs[0]
		comment := subArgs[1]
		fs := flag.NewFlagSet("ticket comment", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Project key")
		fs.Parse(subArgs[2:])

		payload := ToolCallPayload{
			Name: "ticket_add_comment",
			Arguments: map[string]interface{}{
				"id":      id,
				"comment": comment,
			},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		if *projectKey != "" {
			payload.Arguments["project_key"] = *projectKey
		}
		callServerTool(payload)

	case "assign":
		if len(subArgs) < 2 {
			fmt.Println("Error: ticket ID and assignee are required.")
			fmt.Println("Usage: ibis-assistant ticket assign <id> <assignee> [--provider <p>] [--project <k>]")
			os.Exit(1)
		}
		id := subArgs[0]
		assignee := subArgs[1]
		fs := flag.NewFlagSet("ticket assign", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Project key")
		fs.Parse(subArgs[2:])

		payload := ToolCallPayload{
			Name: "ticket_assign",
			Arguments: map[string]interface{}{
				"id":       id,
				"assignee": assignee,
			},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		if *projectKey != "" {
			payload.Arguments["project_key"] = *projectKey
		}
		callServerTool(payload)

	case "transition":
		if len(subArgs) < 2 {
			fmt.Println("Error: ticket ID and transition are required.")
			fmt.Println("Usage: ibis-assistant ticket transition <id> <transition> [--provider <p>] [--project <k>]")
			os.Exit(1)
		}
		id := subArgs[0]
		trans := subArgs[1]
		fs := flag.NewFlagSet("ticket transition", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Project key")
		fs.Parse(subArgs[2:])

		payload := ToolCallPayload{
			Name: "ticket_transition",
			Arguments: map[string]interface{}{
				"id":         id,
				"transition": trans,
			},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		if *projectKey != "" {
			payload.Arguments["project_key"] = *projectKey
		}
		callServerTool(payload)

	case "resolve":
		if len(subArgs) == 0 {
			fmt.Println("Error: ticket ID is required.")
			os.Exit(1)
		}
		id := subArgs[0]
		fs := flag.NewFlagSet("ticket resolve", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Project key")
		notes := fs.String("notes", "", "Comment")
		fs.Parse(subArgs[1:])

		payload := ToolCallPayload{
			Name: "ticket_mark_resolved",
			Arguments: map[string]interface{}{
				"id": id,
			},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		if *projectKey != "" {
			payload.Arguments["project_key"] = *projectKey
		}
		if *notes != "" {
			payload.Arguments["notes"] = *notes
		}
		callServerTool(payload)

	case "close":
		if len(subArgs) == 0 {
			fmt.Println("Error: ticket ID is required.")
			os.Exit(1)
		}
		id := subArgs[0]
		fs := flag.NewFlagSet("ticket close", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Project key")
		notes := fs.String("notes", "", "Comment")
		fs.Parse(subArgs[1:])

		payload := ToolCallPayload{
			Name: "ticket_mark_closed",
			Arguments: map[string]interface{}{
				"id": id,
			},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		if *projectKey != "" {
			payload.Arguments["project_key"] = *projectKey
		}
		if *notes != "" {
			payload.Arguments["notes"] = *notes
		}
		callServerTool(payload)

	case "reopen":
		if len(subArgs) == 0 {
			fmt.Println("Error: ticket ID is required.")
			os.Exit(1)
		}
		id := subArgs[0]
		fs := flag.NewFlagSet("ticket reopen", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Project key")
		notes := fs.String("notes", "", "Comment")
		fs.Parse(subArgs[1:])

		payload := ToolCallPayload{
			Name: "ticket_reopen",
			Arguments: map[string]interface{}{
				"id": id,
			},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		if *projectKey != "" {
			payload.Arguments["project_key"] = *projectKey
		}
		if *notes != "" {
			payload.Arguments["notes"] = *notes
		}
		callServerTool(payload)

	case "list-statuses":
		fs := flag.NewFlagSet("ticket list-statuses", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Project key")
		fs.Parse(subArgs)

		payload := ToolCallPayload{
			Name:      "ticket_list_statuses",
			Arguments: map[string]interface{}{},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		if *projectKey != "" {
			payload.Arguments["project_key"] = *projectKey
		}
		callServerTool(payload)

	case "list-projects":
		fs := flag.NewFlagSet("ticket list-projects", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		fs.Parse(subArgs)

		payload := ToolCallPayload{
			Name:      "ticket_list_projects",
			Arguments: map[string]interface{}{},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		callServerTool(payload)

	default:
		fmt.Printf("Unknown ticket action: %s\n", sub)
		os.Exit(1)
	}
}

func handlePR(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: specify a PR action (create or complete).")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "create":
		fs := flag.NewFlagSet("pr create", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider (azure_devops)")
		projectName := fs.String("project", "", "Project name")
		originURL := fs.String("url", "", "Git origin URL")
		repository := fs.String("repo", "", "Repository name")
		source := fs.String("source", "", "Source branch (required)")
		target := fs.String("target", "", "Target branch")
		title := fs.String("title", "", "PR title")
		desc := fs.String("desc", "", "PR description")
		fs.Parse(subArgs)

		if *source == "" {
			fmt.Println("Error: --source is required.")
			os.Exit(1)
		}

		payload := ToolCallPayload{
			Name: "repo_pr_create",
			Arguments: map[string]interface{}{
				"source_branch": *source,
			},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		if *projectName != "" {
			payload.Arguments["project_name"] = *projectName
		}
		if *originURL != "" {
			payload.Arguments["origin_url"] = *originURL
		}
		if *repository != "" {
			payload.Arguments["repository"] = *repository
		}
		if *target != "" {
			payload.Arguments["target_branch"] = *target
		}
		if *title != "" {
			payload.Arguments["title"] = *title
		}
		if *desc != "" {
			payload.Arguments["description"] = *desc
		}
		callServerTool(payload)

	case "complete":
		if len(subArgs) == 0 {
			fmt.Println("Error: PR ID is required.")
			os.Exit(1)
		}
		prID := subArgs[0]
		fs := flag.NewFlagSet("pr complete", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectName := fs.String("project", "", "Project name")
		repository := fs.String("repo", "", "Repository name")
		deleteBranch := fs.Bool("delete-branch", false, "Delete the source branch")
		squash := fs.Bool("squash", false, "Squash merge")
		fs.Parse(subArgs[1:])

		payload := ToolCallPayload{
			Name: "repo_pr_complete",
			Arguments: map[string]interface{}{
				"pr_id": prID,
			},
		}
		if *provider != "" {
			payload.Arguments["provider"] = *provider
		}
		if *projectName != "" {
			payload.Arguments["project_name"] = *projectName
		}
		if *repository != "" {
			payload.Arguments["repository"] = *repository
		}
		if *deleteBranch {
			payload.Arguments["delete_source_branch"] = "true"
		}
		if *squash {
			payload.Arguments["squash"] = "true"
		}
		callServerTool(payload)

	default:
		fmt.Printf("Unknown PR action: %s\n", sub)
		os.Exit(1)
	}
}

func handleLogs(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: specify a logs action (analyze).")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	if sub != "analyze" {
		fmt.Printf("Unknown logs action: %s\n", sub)
		os.Exit(1)
	}

	fs := flag.NewFlagSet("logs analyze", flag.ExitOnError)
	project := fs.String("project", "", "Project name (required)")
	file := fs.String("file", "", "Log file name (optional)")
	text := fs.String("text", "", "Log text to analyze (required)")
	fs.Parse(subArgs)

	if *project == "" || *text == "" {
		fmt.Println("Error: --project and --text are required.")
		os.Exit(1)
	}

	payload := ToolCallPayload{
		Name: "analyze_logs",
		Arguments: map[string]interface{}{
			"project_name": *project,
			"log_text":     *text,
		},
	}
	if *file != "" {
		payload.Arguments["log_file"] = *file
	}
	callServerTool(payload)
}

func handleCredentials(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: specify a credentials action (add).")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	if sub != "add" {
		fmt.Printf("Unknown credentials action: %s\n", sub)
		os.Exit(1)
	}

	fs := flag.NewFlagSet("credentials add", flag.ExitOnError)
	target := fs.String("target", "", "Repository domain or URL (required)")
	provider := fs.String("provider", "generic", "Provider: github, gitlab, azure_devops, generic")
	authType := fs.String("auth-type", "token", "Auth type: token, basic, ssh")
	token := fs.String("token", "", "Token / password (required for token/basic)")
	username := fs.String("username", "", "Username (optional)")
	sshKey := fs.String("ssh-key", "", "SSH private key (optional)")
	fs.Parse(subArgs)

	if *target == "" || *token == "" {
		fmt.Println("Error: --target and --token are required.")
		os.Exit(1)
	}

	payload := ToolCallPayload{
		Name: "git_configure_credentials",
		Arguments: map[string]interface{}{
			"target":    *target,
			"provider":  *provider,
			"auth_type": *authType,
			"token":     *token,
		},
	}
	if *username != "" {
		payload.Arguments["username"] = *username
	}
	if *sshKey != "" {
		payload.Arguments["ssh_private_key"] = *sshKey
	}
	callServerTool(payload)
}

func handleMemory(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: specify a memory action (add).")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	if sub != "add" {
		fmt.Printf("Unknown memory action: %s\n", sub)
		os.Exit(1)
	}

	fs := flag.NewFlagSet("memory add", flag.ExitOnError)
	project := fs.String("project", "", "Project name (required)")
	text := fs.String("text", "", "Memory text (required)")
	fs.Parse(subArgs)

	if *project == "" || *text == "" {
		fmt.Println("Error: --project and --text are required.")
		os.Exit(1)
	}

	payload := ToolCallPayload{
		Name: "provide_collaborative_memory",
		Arguments: map[string]interface{}{
			"project_name": *project,
			"memory_text":  *text,
		},
	}
	callServerTool(payload)
}

func handleOutcome(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: specify an outcome action (add).")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	if sub != "add" {
		fmt.Printf("Unknown outcome action: %s\n", sub)
		os.Exit(1)
	}

	fs := flag.NewFlagSet("outcome add", flag.ExitOnError)
	project := fs.String("project", "", "Project name (required)")
	question := fs.String("question", "", "Original question (required)")
	outcome := fs.String("outcome", "", "Outcome / conclusion (required)")
	sources := fs.String("sources", "", "Comma-separated useful source IDs (optional)")
	fs.Parse(subArgs)

	if *project == "" || *question == "" || *outcome == "" {
		fmt.Println("Error: --project, --question, and --outcome are required.")
		os.Exit(1)
	}

	payload := ToolCallPayload{
		Name: "save_reasoning_outcome",
		Arguments: map[string]interface{}{
			"project_name": *project,
			"question":     *question,
			"outcome_text": *outcome,
		},
	}
	if *sources != "" {
		payload.Arguments["useful_sources"] = *sources
	}
	callServerTool(payload)
}

func handleOptimize(args []string) {
	fs := flag.NewFlagSet("optimize", flag.ExitOnError)
	iterations := fs.Int("iterations", 10, "RAFT iterations")
	fs.Parse(args)

	payload := ToolCallPayload{
		Name: "optimize_knowledge",
		Arguments: map[string]interface{}{
			"iterations": float64(*iterations),
		},
	}
	callServerTool(payload)
}

package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// ToolCallPayload definisce il formato della richiesta inviata all'endpoint del server
type ToolCallPayload struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ToolCallResponseContent definisce il formato del contenuto di ritorno dei tool MCP
type ToolCallResponseContent struct {
	Type textOrImage `json:"type"` // "text" o "image"
	Text string      `json:"text,omitempty"`
}

type textOrImage string

// ToolCallResponse definisce il formato della risposta del server
type ToolCallResponse struct {
	Content []ToolCallResponseContent `json:"content"`
	IsError bool                      `json:"isError,omitempty"`
}

// ExecuteCLI gestisce la decodifica dei sotto-comandi da terminale
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
		fmt.Printf("Comando sconosciuto: %s\n", sub)
		PrintCLIHelp()
		os.Exit(1)
	}
}

// PrintCLIHelp stampa la guida all'uso dei comandi CLI
func PrintCLIHelp() {
	fmt.Println("Uso dei comandi CLI di Ibis Assistant:")
	fmt.Println("  ibis-assistant <comando> [opzioni]")
	fmt.Println("\nComandi disponibili:")
	fmt.Println("  ask          Invia una domanda di reasoning sul codice del progetto")
	fmt.Println("  ingest       Sincronizza/indicizza codice o cronologia Git")
	fmt.Println("  ticket       Gestisce i ticket su Redmine / Jira / Azure DevOps")
	fmt.Println("  pr           Gestisce Pull Request")
	fmt.Println("  logs         Analizza stream di log")
	fmt.Println("  credentials  Configura credenziali Git persistenti")
	fmt.Println("  memory       Aggiunge memorie collaborative sul progetto")
	fmt.Println("  outcome      Salva deduzioni/conclusioni di reasoning")
	fmt.Println("  optimize     Avvia il ciclo di auto-ottimizzazione RAFT")
	fmt.Println("\nUsa 'ibis-assistant <comando> --help' per visualizzare i dettagli di ciascun comando.")
}

func handleAsk(args []string) {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	branch := fs.String("branch", "", "Branch o commit di contesto")
	fs.Parse(args)

	if len(fs.Args()) == 0 {
		fmt.Println("Errore: specifica la domanda da porre all'IA.")
		fmt.Println("Uso: ibis-assistant ask \"tua domanda\" [--branch <branch>]")
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
		fmt.Println("Errore: specifica cosa indicizzare (code o git).")
		fmt.Println("Uso:")
		fmt.Println("  ibis-assistant ingest code [--path <percorso>]")
		fmt.Println("  ibis-assistant ingest git --name <progetto> --url <url_git> --branch <branch> [--commit <hash>]")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "code":
		fs := flag.NewFlagSet("ingest code", flag.ExitOnError)
		path := fs.String("path", "", "Percorso specifico del repository locale")
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
		name := fs.String("name", "", "Nome del progetto (richiesto)")
		url := fs.String("url", "", "URL del repository Git (richiesto)")
		branch := fs.String("branch", "", "Branch da tracciare (richiesto)")
		commit := fs.String("commit", "", "Commit specifico (opzionale)")
		fs.Parse(subArgs)

		if *name == "" || *url == "" || *branch == "" {
			fmt.Println("Errore: --name, --url e --branch sono obbligatori per l'ingestion Git.")
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
		fmt.Printf("Tipo di ingestion sconosciuto: %s\n", sub)
		os.Exit(1)
	}
}

func handleTicket(args []string) {
	if len(args) == 0 {
		fmt.Println("Errore: specifica l'operazione sui ticket (search, my-issues, get, create, update, comment, assign, transition, resolve, close, reopen, list-statuses, list-projects).")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "search":
		fs := flag.NewFlagSet("ticket search", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider (redmine/jira/azure_devops)")
		query := fs.String("query", "", "Query di ricerca testuale")
		projectKey := fs.String("project", "", "Chiave progetto")
		status := fs.String("status", "", "Stato")
		limit := fs.Int("limit", 20, "Limite risultati")
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
		projectKey := fs.String("project", "", "Chiave progetto")
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
			fmt.Println("Errore: ID del ticket richiesto.")
			os.Exit(1)
		}
		id := subArgs[0]
		fs := flag.NewFlagSet("ticket get", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Chiave progetto")
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
		projectKey := fs.String("project", "", "Chiave progetto (richiesto)")
		title := fs.String("title", "", "Titolo ticket (richiesto)")
		desc := fs.String("desc", "", "Descrizione")
		priority := fs.String("priority", "", "Priorità")
		fs.Parse(subArgs)

		if *projectKey == "" || *title == "" {
			fmt.Println("Errore: --project e --title sono richiesti.")
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
			fmt.Println("Errore: ID del ticket richiesto.")
			os.Exit(1)
		}
		id := subArgs[0]
		fs := flag.NewFlagSet("ticket update", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Chiave progetto")
		notes := fs.String("notes", "", "Commento o note da aggiungere")
		status := fs.String("status", "", "Stato")
		assignee := fs.String("assignee", "", "Assegnatario")
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
			fmt.Println("Errore: ID ticket e commento richiesti.")
			fmt.Println("Uso: ibis-assistant ticket comment <id> \"commento\" [--provider <p>] [--project <k>]")
			os.Exit(1)
		}
		id := subArgs[0]
		comment := subArgs[1]
		fs := flag.NewFlagSet("ticket comment", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Chiave progetto")
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
			fmt.Println("Errore: ID ticket e assegnatario richiesti.")
			fmt.Println("Uso: ibis-assistant ticket assign <id> <assegnatario> [--provider <p>] [--project <k>]")
			os.Exit(1)
		}
		id := subArgs[0]
		assignee := subArgs[1]
		fs := flag.NewFlagSet("ticket assign", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Chiave progetto")
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
			fmt.Println("Errore: ID ticket e stato/transizione richiesti.")
			fmt.Println("Uso: ibis-assistant ticket transition <id> <transizione> [--provider <p>] [--project <k>]")
			os.Exit(1)
		}
		id := subArgs[0]
		trans := subArgs[1]
		fs := flag.NewFlagSet("ticket transition", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Chiave progetto")
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
			fmt.Println("Errore: ID del ticket richiesto.")
			os.Exit(1)
		}
		id := subArgs[0]
		fs := flag.NewFlagSet("ticket resolve", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Chiave progetto")
		notes := fs.String("notes", "", "Commento")
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
			fmt.Println("Errore: ID del ticket richiesto.")
			os.Exit(1)
		}
		id := subArgs[0]
		fs := flag.NewFlagSet("ticket close", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Chiave progetto")
		notes := fs.String("notes", "", "Commento")
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
			fmt.Println("Errore: ID del ticket richiesto.")
			os.Exit(1)
		}
		id := subArgs[0]
		fs := flag.NewFlagSet("ticket reopen", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectKey := fs.String("project", "", "Chiave progetto")
		notes := fs.String("notes", "", "Commento")
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
		projectKey := fs.String("project", "", "Chiave progetto")
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
		fmt.Printf("Azione ticket sconosciuta: %s\n", sub)
		os.Exit(1)
	}
}

func handlePR(args []string) {
	if len(args) == 0 {
		fmt.Println("Errore: specifica l'operazione sulle PR (create o complete).")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "create":
		fs := flag.NewFlagSet("pr create", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider (azure_devops)")
		projectName := fs.String("project", "", "Nome del progetto")
		originURL := fs.String("url", "", "Git origin URL")
		repository := fs.String("repo", "", "Nome repository")
		source := fs.String("source", "", "Branch di origine (richiesto)")
		target := fs.String("target", "", "Branch di destinazione")
		title := fs.String("title", "", "Titolo della PR")
		desc := fs.String("desc", "", "Descrizione della PR")
		fs.Parse(subArgs)

		if *source == "" {
			fmt.Println("Errore: --source è obbligatorio.")
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
			fmt.Println("Errore: ID della PR richiesto.")
			os.Exit(1)
		}
		prID := subArgs[0]
		fs := flag.NewFlagSet("pr complete", flag.ExitOnError)
		provider := fs.String("provider", "", "Provider")
		projectName := fs.String("project", "", "Nome del progetto")
		repository := fs.String("repo", "", "Nome repository")
		deleteBranch := fs.Bool("delete-branch", false, "Elimina il branch sorgente")
		squash := fs.Bool("squash", false, "Esegui merge con squash")
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
		fmt.Printf("Azione PR sconosciuta: %s\n", sub)
		os.Exit(1)
	}
}

func handleLogs(args []string) {
	if len(args) == 0 {
		fmt.Println("Errore: specifica l'operazione sui logs (analyze).")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	if sub != "analyze" {
		fmt.Printf("Azione logs sconosciuta: %s\n", sub)
		os.Exit(1)
	}

	fs := flag.NewFlagSet("logs analyze", flag.ExitOnError)
	project := fs.String("project", "", "Nome del progetto (richiesto)")
	file := fs.String("file", "", "Nome del file di log fittizio (opzionale)")
	text := fs.String("text", "", "Contenuto dei log da analizzare (richiesto)")
	fs.Parse(subArgs)

	if *project == "" || *text == "" {
		fmt.Println("Errore: --project e --text sono obbligatori.")
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
		fmt.Println("Errore: specifica l'operazione sulle credenziali (add).")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	if sub != "add" {
		fmt.Printf("Azione credenziali sconosciuta: %s\n", sub)
		os.Exit(1)
	}

	fs := flag.NewFlagSet("credentials add", flag.ExitOnError)
	target := fs.String("target", "", "Dominio o URL del repository (richiesto)")
	provider := fs.String("provider", "generic", "Nome provider: github, gitlab, azure_devops, generic")
	authType := fs.String("auth-type", "token", "Tipo auth: token, basic, ssh")
	token := fs.String("token", "", "Token / Password (richiesto per token/basic)")
	username := fs.String("username", "", "Username (opzionale)")
	sshKey := fs.String("ssh-key", "", "Chiave privata SSH (opzionale)")
	fs.Parse(subArgs)

	if *target == "" || *token == "" {
		fmt.Println("Errore: --target e --token sono obbligatori.")
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
		fmt.Println("Errore: specifica l'operazione sulle memorie (add).")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	if sub != "add" {
		fmt.Printf("Azione memoria sconosciuta: %s\n", sub)
		os.Exit(1)
	}

	fs := flag.NewFlagSet("memory add", flag.ExitOnError)
	project := fs.String("project", "", "Nome del progetto (richiesto)")
	text := fs.String("text", "", "Contenuto della memoria (richiesto)")
	fs.Parse(subArgs)

	if *project == "" || *text == "" {
		fmt.Println("Errore: --project e --text sono obbligatori.")
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
		fmt.Println("Errore: specifica l'operazione sui reasoning outcome (add).")
		os.Exit(1)
	}

	sub := args[0]
	subArgs := args[1:]

	if sub != "add" {
		fmt.Printf("Azione outcome sconosciuta: %s\n", sub)
		os.Exit(1)
	}

	fs := flag.NewFlagSet("outcome add", flag.ExitOnError)
	project := fs.String("project", "", "Nome del progetto (richiesto)")
	question := fs.String("question", "", "Domanda o problema originario (richiesto)")
	outcome := fs.String("outcome", "", "Risoluzione / Deduzione logica (richiesto)")
	sources := fs.String("sources", "", "ID delle fonti utili separated da virgola (opzionale)")
	fs.Parse(subArgs)

	if *project == "" || *question == "" || *outcome == "" {
		fmt.Println("Errore: --project, --question e --outcome sono obbligatori.")
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
	iterations := fs.Int("iterations", 10, "Numero di iterazioni RAFT")
	fs.Parse(args)

	payload := ToolCallPayload{
		Name: "optimize_knowledge",
		Arguments: map[string]interface{}{
			"iterations": float64(*iterations),
		},
	}
	callServerTool(payload)
}

// callServerTool inoltra la chiamata all'endpoint del server Ibis Assistant locale
var callServerTool = func(payload ToolCallPayload) {
	// Carica config locale per leggere la porta del server
	serverPort := 3030
	if f, err := os.Open("config.json"); err == nil {
		defer f.Close()
		var cfg struct {
			Port int `json:"port"`
		}
		if err := json.NewDecoder(f).Decode(&cfg); err == nil && cfg.Port > 0 {
			serverPort = cfg.Port
		}
	}

	serverURL := fmt.Sprintf("http://localhost:%d/api/v1/cli/call", serverPort)

	reqBytes, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Errore serializzazione payload: %v\n", err)
		os.Exit(1)
	}

	client := &http.Client{Timeout: 5 * time.Minute} // Timeout lungo per operazioni AI
	resp, err := client.Post(serverURL, "application/json", bytes.NewBuffer(reqBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Errore di connessione al server: %v\nAssicurati che il server sia avviato (es. con 'ibis-assistant start' o 'ibis-assistant run').\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Errore server (Status %d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var toolResp ToolCallResponse
	if err := json.NewDecoder(resp.Body).Decode(&toolResp); err != nil {
		// Se non è il formato ToolCallResponse standard, potrebbe essere un errore di parsing JSON
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Errore di decodifica della risposta: %v\nRaw response: %s\n", err, string(body))
		os.Exit(1)
	}

	if toolResp.IsError {
		fmt.Fprintln(os.Stderr, "Errore durante l'esecuzione dello strumento:")
		for _, content := range toolResp.Content {
			fmt.Fprintln(os.Stderr, content.Text)
		}
		os.Exit(1)
	}

	// Stampa i risultati
	for _, content := range toolResp.Content {
		// Prova a formattare il testo come JSON se sembra esserlo
		var js interface{}
		trimmed := strings.TrimSpace(content.Text)
		if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) || (strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
			if err := json.Unmarshal([]byte(trimmed), &js); err == nil {
				formatted, err2 := json.MarshalIndent(js, "", "  ")
				if err2 == nil {
					fmt.Println(string(formatted))
					continue
				}
			}
		}
		fmt.Println(content.Text)
	}
}

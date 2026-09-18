package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RunWizard avvia il prompt interattivo da terminale per generare config.json e config/ticketing_config.json
func RunWizard() error {
	fmt.Println("\n=== Ibis Assistant - Wizard di Prima Configurazione ===")
	fmt.Println("Questo wizard ti aiuterà a configurare l'applicazione generando il file config.json.")
	fmt.Println("Lasciando il campo vuoto, verrà applicato il valore predefinito mostrato tra parentesi quadre.")

	scanner := bufio.NewScanner(os.Stdin)

	fmt.Print("\nScegli la modalità di configurazione:\n  1) Veloce (Fast) - parametri essenziali (consigliato)\n  2) Dettagliata (Detailed) - tutti i parametri (DB, AI, SMTP, Ticketing)\nScelta (1/2) [1]: ")
	scanner.Scan()
	choice := strings.TrimSpace(scanner.Text())
	if choice == "" {
		choice = "1"
	}

	// 1. Carica i default di base caricandoli da Load(path non esistente) o instanziandoli direttamente
	cfg := NewDefaultConfig()

	var err error
	if choice == "2" {
		err = runDetailedWizard(cfg, scanner)
	} else {
		err = runFastWizard(cfg, scanner)
	}

	if err != nil {
		return fmt.Errorf("wizard interrotto: %w", err)
	}

	// Salva config.json
	configData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("errore serializzazione JSON: %w", err)
	}

	err = os.WriteFile("config.json", configData, 0644)
	if err != nil {
		return fmt.Errorf("errore scrittura config.json: %w", err)
	}
	fmt.Println("\n[OK] File config.json salvato con successo!")

	// Chiedi se si desidera avviare il server ora
	fmt.Print("\nVuoi avviare il server Ibis Assistant adesso? (S/N) [S]: ")
	scanner.Scan()
	startChoice := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if startChoice == "" || startChoice == "s" || startChoice == "si" {
		fmt.Println("Avvio del server...")
		return nil
	}

	fmt.Println("Configurazione completata. Puoi avviare il server lanciando: ibis-assistant run")
	os.Exit(0)
	return nil
}

func runFastWizard(cfg *Config, scanner *bufio.Scanner) error {
	fmt.Println("\n--- Configurazione Veloce ---")

	promptRuntimeAndFolder(cfg, scanner)

	// Porta
	cfg.Port = promptInt("Porta del server", cfg.Port, scanner)

	// AI Reasoning
	useGemini := promptBool("Abilitare AI Reasoning con Gemini", true, scanner)
	if useGemini {
		cfg.AI.Reasoning.Provider = "gemini"
		cfg.AI.Reasoning.Model = "gemini-2.5-pro"

		// Chiave API
		envKey := os.Getenv("GEMINI_API_KEY")
		promptMsg := "Inserisci la tua Gemini API Key"
		if envKey != "" {
			promptMsg += " (lascia vuoto per usare GEMINI_API_KEY da ambiente)"
		}
		fmt.Printf("%s: ", promptMsg)
		scanner.Scan()
		keyInput := strings.TrimSpace(scanner.Text())
		if keyInput == "" && envKey != "" {
			keyInput = envKey
		}

		if keyInput != "" {
			cfg.AI.Reasoning.Keys = []GeminiKeyConfig{
				{
					Key:   keyInput,
					RPM:   cfg.GeminiDefaultRPM,
					Owner: "default",
				},
			}
			cfg.GeminiKeys = cfg.AI.Reasoning.Keys
		} else {
			fmt.Println("⚠️  Nessuna chiave API inserita. Aggiungila in config.json prima di avviare il server.")
		}
	} else {
		cfg.AI.Reasoning.Provider = "none"
	}

	// Cartelle
	cfg.DiscoveryRoot = promptString("Cartella root per la ricerca dei repository Git", cfg.DiscoveryRoot, scanner)
	cfg.LogsRoot = promptString("Cartella root per l'analisi dei file di log", cfg.LogsRoot, scanner)

	// Crea le cartelle se non esistono
	_ = os.MkdirAll(cfg.DiscoveryRoot, 0755)
	_ = os.MkdirAll(cfg.LogsRoot, 0755)

	return nil
}

func runDetailedWizard(cfg *Config, scanner *bufio.Scanner) error {
	fmt.Println("\n--- Configurazione Dettagliata ---")

	promptRuntimeAndFolder(cfg, scanner)

	// 1. Server
	fmt.Println("\n[1] Impostazioni Server")
	cfg.Port = promptInt("Porta del server", cfg.Port, scanner)
	cfg.Mode = promptString("Modalità server (sse/stdio)", cfg.Mode, scanner)

	// 2. Database
	fmt.Println("\n[2] Impostazioni Database SurrealDB")
	fmt.Print("Tipo database:\n  1) Embedded SurrealDB (avviato in locale in automatico) [consigliato]\n  2) SurrealDB Remoto\nScelta (1/2) [1]: ")
	scanner.Scan()
	dbChoice := strings.TrimSpace(scanner.Text())
	if dbChoice == "" {
		dbChoice = "1"
	}

	if dbChoice == "1" {
		cfg.DBUrl = "ws://localhost:8000/rpc"
		cfg.DBDataPath = promptString("Cartella dati del database", cfg.DBDataPath, scanner)
		cfg.DBAutoUpdate = promptBool("Abilitare l'auto-update del database SurrealDB", cfg.DBAutoUpdate, scanner)
	} else {
		cfg.DBUrl = promptString("URL del database (es. ws://localhost:8000/rpc)", cfg.DBUrl, scanner)
		cfg.DBNamespace = promptString("Namespace SurrealDB", cfg.DBNamespace, scanner)
		cfg.DBDatabase = promptString("Database SurrealDB", cfg.DBDatabase, scanner)
		cfg.DBUser = promptString("Username", cfg.DBUser, scanner)
		cfg.DBPassword = promptString("Password", cfg.DBPassword, scanner)
	}

	// 3. AI Settings
	fmt.Println("\n[3] Impostazioni Intelligenza Artificiale (Embeddings & Reasoning)")

	// AI Embedding
	cfg.AI.Embedding.Provider = promptString("Provider per gli Embeddings (ollama/gemini)", cfg.AI.Embedding.Provider, scanner)
	if cfg.AI.Embedding.Provider == "ollama" {
		cfg.AI.Embedding.URL = promptString("URL server Ollama", cfg.AI.Embedding.URL, scanner)
		cfg.AI.Embedding.Model = promptString("Modello per Embeddings", cfg.AI.Embedding.Model, scanner)
		cfg.AI.Embedding.AutoStart = promptBool("Avviare automaticamente Ollama in background", cfg.AI.Embedding.AutoStart, scanner)
		cfg.AI.Embedding.AutoUpdate = promptBool("Scaricare/Aggiornare automaticamente il modello", cfg.AI.Embedding.AutoUpdate, scanner)
	} else if cfg.AI.Embedding.Provider == "gemini" {
		fmt.Println("Gemini verrà utilizzato per gli embeddings.")
	}

	// AI Reasoning
	cfg.AI.Reasoning.Provider = promptString("Provider per Reasoning (gemini/none)", cfg.AI.Reasoning.Provider, scanner)
	if cfg.AI.Reasoning.Provider == "gemini" {
		cfg.AI.Reasoning.Model = promptString("Modello di Reasoning", cfg.AI.Reasoning.Model, scanner)
		cfg.GeminiDefaultRPM = promptInt("Default Request-Per-Minute (RPM) per chiave", cfg.GeminiDefaultRPM, scanner)

		fmt.Print("Inserisci una o più Gemini API Keys (separate da virgola se multiple): ")
		scanner.Scan()
		keysStr := strings.TrimSpace(scanner.Text())
		if keysStr == "" {
			keysStr = os.Getenv("GEMINI_API_KEY")
		}

		if keysStr != "" {
			parts := strings.Split(keysStr, ",")
			var keys []GeminiKeyConfig
			for i, p := range parts {
				cleanKey := strings.TrimSpace(p)
				if cleanKey != "" {
					keys = append(keys, GeminiKeyConfig{
						Key:   cleanKey,
						RPM:   cfg.GeminiDefaultRPM,
						Owner: fmt.Sprintf("key_%d", i+1),
					})
				}
			}
			cfg.AI.Reasoning.Keys = keys
			cfg.GeminiKeys = keys
		} else {
			fmt.Println("⚠️  Nessuna chiave inserita. Verrà usata la variabile d'ambiente GEMINI_API_KEY a runtime.")
		}
	}

	// 4. Discovery
	fmt.Println("\n[4] Impostazioni Repository Discovery")
	cfg.DiscoveryRoot = promptString("Cartella root per la ricerca dei repository Git", cfg.DiscoveryRoot, scanner)
	cfg.AutoScan = promptBool("Abilitare lo scanning automatico all'avvio", cfg.AutoScan, scanner)

	// 5. Logs Ingestion
	fmt.Println("\n[5] Impostazioni Analisi Logs")
	cfg.LogsRoot = promptString("Cartella root per l'analisi dei file di log", cfg.LogsRoot, scanner)
	cfg.LogIngestion.HTTP.Enabled = promptBool("Abilitare l'invio dei log tramite HTTP API (Push)", cfg.LogIngestion.HTTP.Enabled, scanner)
	if cfg.LogIngestion.HTTP.Enabled {
		cfg.LogIngestion.HTTP.APIKey = promptString("API Key di sicurezza per l'endpoint di Push", "secure-api-key-123", scanner)
	}

	// 6. SMTP
	fmt.Println("\n[6] Impostazioni Notifiche SMTP (E-mail)")
	cfg.SMTP.Enabled = promptBool("Abilitare le notifiche e-mail per errori critici", cfg.SMTP.Enabled, scanner)
	if cfg.SMTP.Enabled {
		cfg.SMTP.Host = promptString("SMTP Host", cfg.SMTP.Host, scanner)
		cfg.SMTP.Port = promptInt("SMTP Port", cfg.SMTP.Port, scanner)
		cfg.SMTP.User = promptString("Username SMTP", cfg.SMTP.User, scanner)
		cfg.SMTP.Password = promptString("Password SMTP", cfg.SMTP.Password, scanner)
		cfg.SMTP.From = promptString("Mittente (From)", cfg.SMTP.From, scanner)
		cfg.SMTP.To = promptString("Destinatario (To)", cfg.SMTP.To, scanner)
		cfg.SMTP.Encryption = promptString("Cifratura (ssl_tls/starttls/none)", "ssl_tls", scanner)
		cfg.SMTP.AggregationWindow = promptString("Finestra di aggregazione (es. 1h, 24h)", cfg.SMTP.AggregationWindow, scanner)
		cfg.SMTP.EmergencySeverityThreshold = promptInt("Soglia di gravità per email immediate (1-10)", cfg.SMTP.EmergencySeverityThreshold, scanner)
	}

	// 7. Ticketing (Redmine)
	fmt.Println("\n[7] Integrazione Ticketing (Redmine)")
	setupRedmine := promptBool("Configurare l'integrazione con Redmine", false, scanner)
	if setupRedmine {
		redmineURL := promptString("Indirizzo URL di Redmine", "http://redmine.local", scanner)
		redmineKey := promptString("Redmine API Key", "", scanner)

		cfg.RedmineURL = redmineURL
		cfg.RedmineKey = redmineKey

		// Crea config/ticketing_config.json
		ticketingConfig := map[string]interface{}{
			"default_provider": "redmine",
			"project_provider_map": map[string]string{
				"*": "redmine",
			},
			"providers": map[string]interface{}{
				"redmine": map[string]interface{}{
					"base_url": redmineURL,
					"api_key":  redmineKey,
				},
			},
			"pr": map[string]interface{}{
				"default_target_branch": "master",
			},
		}

		configDir := "config"
		_ = os.MkdirAll(configDir, 0755)
		tCfgPath := filepath.Join(configDir, "ticketing_config.json")
		tData, _ := json.MarshalIndent(ticketingConfig, "", "  ")
		_ = os.WriteFile(tCfgPath, tData, 0644)
		fmt.Printf("File di configurazione ticketing salvato in: %s\n", tCfgPath)
	}

	// Crea le cartelle se non esistono
	_ = os.MkdirAll(cfg.DiscoveryRoot, 0755)
	_ = os.MkdirAll(cfg.LogsRoot, 0755)

	return nil
}

func promptRuntimeAndFolder(cfg *Config, scanner *bufio.Scanner) {
	cfg.RuntimeMode = promptString("Runtime mode (personal/server/plugin)", "personal", scanner)
	if !IsLiveRuntime(cfg.RuntimeMode) {
		return
	}
	cwd, _ := os.Getwd()
	folder := promptString("Local working folder", cwd, scanner)
	if strings.TrimSpace(folder) == "" {
		return
	}
	cfg.Projects = []ProjectConfig{{
		Name:            filepath.Base(folder),
		WorkingRepoPath: folder,
	}}
}

func promptString(prompt, defaultValue string, scanner *bufio.Scanner) string {
	fmt.Printf("%s [%s]: ", prompt, defaultValue)
	scanner.Scan()
	val := strings.TrimSpace(scanner.Text())
	if val == "" {
		return defaultValue
	}
	return val
}

func promptInt(prompt string, defaultValue int, scanner *bufio.Scanner) int {
	fmt.Printf("%s [%d]: ", prompt, defaultValue)
	scanner.Scan()
	val := strings.TrimSpace(scanner.Text())
	if val == "" {
		return defaultValue
	}
	num, err := strconv.Atoi(val)
	if err != nil {
		fmt.Printf("⚠️  Valore non valido, applicato default: %d\n", defaultValue)
		return defaultValue
	}
	return num
}

func promptBool(prompt string, defaultValue bool, scanner *bufio.Scanner) bool {
	defStr := "S"
	if !defaultValue {
		defStr = "N"
	}
	fmt.Printf("%s (S/N) [%s]: ", prompt, defStr)
	scanner.Scan()
	val := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if val == "" {
		return defaultValue
	}
	return val == "s" || val == "si" || val == "y" || val == "yes"
}

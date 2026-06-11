# Guida Operativa in Fasi: Estensione Log Analysis e Riforma SMTP

> [!NOTE]
> **Stato dell'implementazione: PIANIFICATO**
> Questa guida operativa definisce il piano di implementazione suddiviso in **fasi incrementali e sequenziali**. Ciascuna fase rappresenta un traguardo autocontenuto con verifiche di compilazione e test intermedi.

---

## Panoramica delle Fasi
* **Fase 1:** Configurazione & Schema Database (SMTP, HTTP API, Polling)
* **Fase 2:** Riforma Client SMTP (`internal/ingest/logs/notifier.go`)
* **Fase 3:** Throttling e Aggregazione Notifiche (Daily Digest)
* **Fase 4:** Ingestione Sincrona & Endpoint HTTP POST `/api/v1/logs/upload`
* **Fase 5:** Aggiunta del Tool MCP Sincrono `analyze_log_stream`
* **Fase 6:** Scheduler e Polling Client (FTP / SFTP / SMB)
* **Fase 7:** Validazione e Unit Testing

---

## Fase 1: Configurazione & Schema Database
**Obiettivo:** Estendere la struct `config.Config` e lo schema SurrealDB per accogliere le nuove opzioni.

### Passi operativi:
1. Apri `internal/config/config.go` e aggiungi i seguenti campi a `SMTPConfig`:
   ```go
   Encryption                 string `json:"encryption"` // ssl_tls, starttls, none
   AggregationWindow          string `json:"aggregation_window"` // es. "1h", "24h"
   EmergencySeverityThreshold int    `json:"emergency_severity_threshold"`
   ```
2. Definisci le struct per il Polling dei log remoti in `internal/config/config.go`:
   ```go
   type LogIngestionConfig struct {
       HTTP    HTTPIngestionConfig `json:"http"`
       Polling []PollingSource     `json:"polling"`
   }

   type HTTPIngestionConfig struct {
       Enabled bool   `json:"enabled"`
       APIKey  string `json:"api_key"`
   }

   type PollingSource struct {
       Name         string `json:"name"`
       Enabled      bool   `json:"enabled"`
       Protocol     string `json:"protocol"` // ftp, sftp, smb
       Host         string `json:"host"`
       Port         int    `json:"port"`
       User         string `json:"user"`
       Password     string `json:"password"`
       RemoteDir    string `json:"remote_dir"`
       FilePattern  string `json:"file_pattern"`
       ArchiveDir   string `json:"archive_dir"`
       PollInterval string `json:"poll_interval"` // es. "15m"
       ProjectName  string `json:"project_name"`
   }
   ```
   Aggiungi il campo `LogIngestion LogIngestionConfig json:"log_ingestion"` alla struct principale `Config`.
3. In `internal/schema/schema.go`, aggiungi le definizioni delle tabelle necessarie:
   ```go
   TableLogProcessedFile     = "log_processed_file"
   TableLogPendingNotification = "log_pending_notification"
   ```
   Aggiungile alla lista delle definizioni per inizializzare il DB.

---

## Fase 2: Riforma Client SMTP
**Obiettivo:** Supportare connessioni SSL/TLS implicite su porta 465, STARTTLS e l'invio senza credenziali.

### Passi operativi:
1. Apri `internal/ingest/logs/notifier.go` e sostituisci la logica di `SendEmail`.
2. Implementa la scelta del protocollo di cifratura basato su `Cfg.SMTP.Encryption`.
3. Consenti l'invio senza credenziali (auth = nil) se `Cfg.SMTP.User` e `Cfg.SMTP.Password` sono vuoti.

---

## Fase 3: Throttling e Aggregazione Notifiche
**Obiettivo:** Invece di inviare e-mail per ogni errore, accumulare i record su `log_pending_notification` e inviarli periodicamente in un e-mail aggregata (Digest). Gli errori urgenti (severity superiore alla soglia) aggirano l'accumulatore.

### Passi operativi:
1. In `internal/ingest/logs/notifier.go`, implementa una funzione `QueueNotification` che memorizza in SurrealDB l'anomalia.
2. Implementa la funzione `ProcessPendingNotifications` che legge tutte le notifiche pendenti, le raggruppa in un report markdown consolidato e le invia via email, dopodiché svuota la coda.

---

## Fase 4: Ingestione Sincrona & Endpoint HTTP POST
**Obiettivo:** Consentire l'analisi di stream di log testuali direttamente tramite chiamata HTTP protetta da API Key.

### Passi operativi:
1. In `internal/ingest/logs/analyzer.go`, rendi la funzione `ProcessBatch` flessibile per accettare stringhe esterne e restituire gli errori rilevati.
2. In `cmd/server/main.go`, registra la rotta `POST /api/v1/logs/upload`:
   - Verifica l'header `X-API-Key`.
   - Legge il payload JSON o multipart.
   - Chiama sincronicamente `LogAnalyzer.ProcessBatch` e risponde con l'array degli errori e lo stato dell'elaborazione.

---

## Fase 5: Aggiunta del Tool MCP Sincrono `analyze_log_stream`
**Obiettivo:** Aggiungere lo strumento MCP sincrono per consentire ad agenti remoti di analizzare spezzoni di log in tempo reale.

### Passi operativi:
1. Apri `cmd/server/main.go`.
2. Aggiungi il tool MCP `analyze_log_stream`.
3. Gestisci la chiamata eseguendo sincronicamente l'analisi dei log tramite `LogAnalyzer` e restituisci in output un report JSON contenente gli errori identificati.

---

## Fase 6: Scheduler e Polling Client (FTP / SFTP / SMB)
**Obiettivo:** Implementare il polling scheduler periodico che si collega, scarica, analizza e archivia i log remoti.

### Passi operativi:
1. Installa i pacchetti necessari (se non già presenti):
   - `github.com/jlaffaye/ftp`
   - `github.com/pkg/sftp`
   - `github.com/hirochachacha/go-smb2`
2. Crea `internal/ingest/logs/polling.go` con lo scheduler e l'interfaccia `LogClient` per scansionare, scaricare e spostare i file in `/archive`.

---

## Fase 7: Validazione e Unit Testing
**Obiettivo:** Aggiungere test completi per ciascun componente modificato.

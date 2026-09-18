# Phased Implementation Guide: Log Analysis Extension and SMTP Reform

> [!NOTE]
> **Implementation status: COMPLETED** (commit `cf677b3`; MCP tool renamed/shipped as **`analyze_logs`**)
> All original phases have been implemented. September 2026 additions (enrich, LogAlign promote, sidecar reports, AI→ast-grep for logs) are documented in [log_analysis_pipeline.md](log_analysis_pipeline.md) and [changelog_20260918.md](changelog_20260918.md).
>
> **Residual risks identified (post-naming analysis):**
> - Polling pattern matching + file archival in real scenarios (permissions/path of SMB shares on Windows).
> - Determinism of AI JSON parsing on noisy or non-JSON-friendly logs (depends on model quality).
> - Digest behavior under high load: queue growth and post-send delete guarantee to verify in production.

---

## Phase Overview
* **Phase 1:** Configuration & Database Schema (SMTP, HTTP API, Polling)
* **Phase 2:** SMTP Client Reform (`internal/ingest/logs/notifier.go`)
* **Phase 3:** Notification Throttling and Aggregation (Daily Digest)
* **Phase 4:** Synchronous Ingestion & HTTP POST Endpoint `/api/v1/logs/upload`
* **Phase 5:** Add Synchronous MCP Tool `analyze_logs`
* **Phase 6:** Scheduler and Polling Client (FTP / SFTP / SMB)
* **Phase 7:** Validation and Unit Testing

---

## Phase 1: Configuration & Database Schema
**Goal:** Extend the `config.Config` struct and SurrealDB schema to accept the new options.

### Operational steps:
1. Open `internal/config/config.go` and add the following fields to `SMTPConfig`:
   ```go
   Encryption                 string `json:"encryption"` // ssl_tls, starttls, none
   AggregationWindow          string `json:"aggregation_window"` // e.g. "1h", "24h"
   EmergencySeverityThreshold int    `json:"emergency_severity_threshold"`
   ```
2. Define structs for remote log polling in `internal/config/config.go`:
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
       PollInterval string `json:"poll_interval"` // e.g. "15m"
       ProjectName  string `json:"project_name"`
   }
   ```
   Add the field `LogIngestion LogIngestionConfig json:"log_ingestion"` to the main `Config` struct.
3. In `internal/schema/schema.go`, add the required table definitions:
   ```go
   TableLogProcessedFile     = "log_processed_file"
   TableLogPendingNotification = "log_pending_notification"
   ```
   Add them to the definition list to initialize the DB.

---

## Phase 2: SMTP Client Reform
**Goal:** Support implicit SSL/TLS connections on port 465, STARTTLS, and sending without credentials.

### Operational steps:
1. Open `internal/ingest/logs/notifier.go` and replace the `SendEmail` logic.
2. Implement encryption protocol selection based on `Cfg.SMTP.Encryption`.
3. Allow sending without credentials (auth = nil) if `Cfg.SMTP.User` and `Cfg.SMTP.Password` are empty.

---

## Phase 3: Notification Throttling and Aggregation
**Goal:** Instead of sending an email for every error, accumulate records on `log_pending_notification` and send them periodically in an aggregated email (Digest). Urgent errors (severity above the threshold) bypass the accumulator.

### Operational steps:
1. In `internal/ingest/logs/notifier.go`, implement a `QueueNotification` function that stores the anomaly in SurrealDB.
2. Implement `ProcessPendingNotifications` that reads all pending notifications, groups them into a consolidated markdown report, sends them via email, then clears the queue.

---

## Phase 4: Synchronous Ingestion & HTTP POST Endpoint
**Goal:** Allow analysis of textual log streams directly via an HTTP call protected by API Key.

### Operational steps:
1. In `internal/ingest/logs/analyzer.go`, make `ProcessBatch` flexible enough to accept external strings and return detected errors.
2. In `cmd/server/main.go`, register the route `POST /api/v1/logs/upload`:
   - Verify the `X-API-Key` header.
   - Read the JSON or multipart payload.
   - Synchronously call `LogAnalyzer.ProcessBatch` and respond with the error array and processing status.

---

## Phase 5: Add Synchronous MCP Tool `analyze_logs`
**Goal:** Add the synchronous MCP tool so remote agents can analyze log snippets in real time.

### Operational steps:
1. Open `cmd/server/main.go`.
2. Add the MCP tool `analyze_logs`.
3. Handle the call by synchronously running log analysis via `LogAnalyzer` and return a JSON report containing the identified errors.

---

## Phase 6: Scheduler and Polling Client (FTP / SFTP / SMB)
**Goal:** Implement the periodic polling scheduler that connects, downloads, analyzes, and archives remote logs.

### Operational steps:
1. Install the required packages (if not already present):
   - `github.com/jlaffaye/ftp`
   - `github.com/pkg/sftp`
   - `github.com/hirochachacha/go-smb2`
2. Create `internal/ingest/logs/polling.go` with the scheduler and a `LogClient` interface to scan, download, and move files into `/archive`.

---

## Phase 7: Validation and Unit Testing
**Goal:** Add complete tests for each modified component.

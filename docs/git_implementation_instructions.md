# Phased Implementation Guide: Git Access Generalization and Credential Management (Top-Down Methodology)

> [!NOTE]
> **Implementation status: COMPLETED** (commit `69868c2`)  
> All phases have been implemented and verified with green tests. This document is kept as an architectural reference.
>
> **Residual risks identified (post-naming analysis):**
> - SSH edge-case coverage (multi-line keys, temporary file permissions on Windows).
> - Consistency between real Git auth errors and `CredentialsRequiredError` trigger on non-standard providers.
> - Output sanitization for Git messages with URL-encoded tokens or nested base64.

---

## Phase Overview
* **Phase 1:** Database Schema Update (SurrealDB Schema for `git_credential`)
* **Phase 2:** Core Credential Store (`internal/gitrepo` - Types & Store)
* **Phase 3:** Git Command Runner with Log Masking (`internal/gitrepo` - Runner)
* **Phase 4:** Context and Config Integration (Reading `X-Git-Token` header)
* **Phase 5:** Workspace Sync Refactor (`internal/ingest/dynamic/workspace.go`)
* **Phase 6:** MCP Tool Implementation (`git_configure_credentials` & `init_project` handling)
* **Phase 7:** Validation and Unit Testing

---

## Phase 1: Database Schema Update
**Goal:** Define the `git_credential` table and its unique index in SurrealDB.

### Operational steps:
1. Open `internal/schema/schema.go`.
2. Add the `TableGitCredential` constant to identify the table:
   ```go
   TableGitCredential = "git_credential"
   ```
3. Add the following definitions and indexes to the `Definition` array:
   ```go
   fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableGitCredential),
   fmt.Sprintf("DEFINE INDEX credential_target ON TABLE %s COLUMNS target UNIQUE;", TableGitCredential),
   ```
4. Run the build to verify syntax.

---

## Phase 2: Core Credential Store (`internal/gitrepo` - Types & Store)
**Goal:** Create structs to represent credentials and the logic to save/read them from SurrealDB.

### Operational steps:
1. Create `internal/gitrepo/types.go` and declare the `Credential` struct:
   ```go
   package gitrepo

   type ProviderName string

   const (
       ProviderGitHub      ProviderName = "github"
       ProviderGitLab      ProviderName = "gitlab"
       ProviderAzureDevOps ProviderName = "azure_devops"
       ProviderGeneric     ProviderName = "generic"
   )

   type AuthType string

   const (
       AuthTypeToken AuthType = "token"
       AuthTypeBasic AuthType = "basic"
       AuthTypeSSH   AuthType = "ssh"
   )

   type Credential struct {
       Target        string   `json:"target"` // Domain (e.g. github.com) or full repo URL
       Provider      string   `json:"provider"`
       AuthType      AuthType `json:"auth_type"`
       Token         string   `json:"token,omitempty"`
       Username      string   `json:"username,omitempty"`
       SSHPrivateKey string   `json:"ssh_private_key,omitempty"`
   }
   ```
2. Create `internal/gitrepo/store.go` to implement the credential store:
   ```go
   package gitrepo

   import (
       "context"
       "fmt"
       "github.com/terenzif/ibis-server/internal/db"
       "github.com/terenzif/ibis-server/internal/schema"
   )

   type CredentialStore struct {
       db db.Executor
   }

   func NewCredentialStore(db db.Executor) *CredentialStore {
       return &CredentialStore{db: db}
   }

   func (s *CredentialStore) SaveCredential(ctx context.Context, cred Credential) error {
       id := fmt.Sprintf("%s:%s", schema.TableGitCredential, db.SanitizeID(cred.Target))
       query := fmt.Sprintf("UPSERT %s CONTENT $cred;", id)
       vars := map[string]interface{}{
           "cred": cred,
       }
       _, err := s.db.SmartQuery(ctx, query, vars)
       return err
   }

   func (s *CredentialStore) GetCredential(ctx context.Context, target string) (*Credential, error) {
       id := fmt.Sprintf("%s:%s", schema.TableGitCredential, db.SanitizeID(target))
       query := fmt.Sprintf("SELECT * FROM %s;", id)
       res, err := s.db.Execute(ctx, query)
       if err != nil {
           return nil, err
       }
       // Parse the result
       // ...
       return nil, nil // Return credential if found
   }
   ```
3. Write related unit tests in `internal/gitrepo/store_test.go`.

---

## Phase 3: Git Command Runner with Log Masking (`internal/gitrepo` - Runner)
**Goal:** Run Git commands injecting the correct credentials depending on the provider, and mask sensitive tokens in logs/errors.

### Operational steps:
1. Create `internal/gitrepo/runner.go`.
2. Implement `GitCommandRunner` that:
   - Formats Git arguments (e.g. modifying the URL to insert `http.extraHeader` or base64 basic-auth token).
   - Runs `exec.CommandContext`.
   - Intercepts output and removes/replaces sensitive strings such as PATs and tokens so they are not printed in logs or returned in errors.
3. Write related unit tests in `internal/gitrepo/runner_test.go`.

---

## Phase 4: Context and Config Integration (Reading `X-Git-Token` header)
**Goal:** Enable passing runtime tokens from the HTTP client into the execution context of operations.

### Operational steps:
1. Modify `internal/auth/keys.go` adding the `GitTokenContextKey` constant.
2. In `cmd/server/main.go`, modify `AuthMiddleware` to intercept the `X-Git-Token` (or `X-Git-PAT`) header and inject it into the context.
3. Modify the `Config` struct in `internal/config/config.go` so the credential lookup function also queries the new store if present.

---

## Phase 5: Workspace Sync Refactor (`internal/ingest/dynamic/workspace.go`)
**Goal:** Replace raw `exec.CommandContext` calls in `SyncWorkspace` with the new `GitCommandRunner`.

### Operational steps:
1. Open `internal/ingest/dynamic/workspace.go`.
2. Modify the `SyncWorkspace` signature (or add an updated version) that also receives a `db.Executor` to access saved credentials, in addition to request-level ones in `ctx`.
3. Integrate logic to look up credentials first in the context (runtime), then in the database via the `originUrl` target or provider domain, and finally as fallback in `config.json`.
4. If execution fails due to authorization or there are no credentials for a private repository, return a structured ad-hoc error (e.g. `CredentialsRequiredError`).

---

## Phase 6: MCP Tool Implementation (`git_configure_credentials` & `init_project` handling)
**Goal:** Expose the new MCP tools and update error handling in `init_project`.

### Operational steps:
1. Open `cmd/server/main.go`.
2. Add the new MCP tool `git_configure_credentials`:
   - Takes as arguments: `target`, `provider`, `auth_type`, `token`, `username`, `ssh_private_key`.
   - Saves data in SurrealDB via the store created in Phase 2.
3. Modify the `init_project` and `update_project_status` handlers:
   - Catch `CredentialsRequiredError`.
   - Return a structured JSON payload with `"status": "credentials_required"`.

---

## Phase 7: Validation and Unit Testing
**Goal:** Ensure overall system stability.

### Operational steps:
1. Run the global test suite: `go test ./...`.
2. Manually test the flow by simulating the first sync of a private repository and verifying that credential request and persistence work correctly.

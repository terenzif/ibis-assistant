# Guida Operativa in Fasi: Generalizzazione Accesso Git e Gestione Credenziali (Metodologia Top-Down)

> [!NOTE]
> **Stato dell'implementazione: PIANIFICATO**
> Questa guida operativa definisce il piano di implementazione suddiviso in **fasi incrementali e sequenziali**. Ciascuna fase rappresenta un traguardo autocontenuto con verifiche di compilazione e test intermedi, studiato per guidare gli agenti di coding ed evitare allucinazioni o perdite di contesto.

---

## Panoramica delle Fasi
* **Fase 1:** Aggiornamento dello Schema del Database (SurrealDB Schema per `git_credential`)
* **Fase 2:** Core Credential Store (`internal/gitrepo` - Types & Store)
* **Fase 3:** Git Command Runner con Log Masking (`internal/gitrepo` - Runner)
* **Fase 4:** Integrazione Context e Config (Lettura header `X-Git-Token`)
* **Fase 5:** Rifattorizzazione Sincronizzazione Workspace (`internal/ingest/dynamic/workspace.go`)
* **Fase 6:** Implementazione Tool MCP (`git_configure_credentials` & Gestione `init_project`)
* **Fase 7:** Validazione e Unit Testing

---

## Fase 1: Aggiornamento dello Schema del Database
**Obiettivo:** Definire la tabella `git_credential` e il suo indice unico in SurrealDB.

### Passi operativi:
1. Apri `internal/schema/schema.go`.
2. Aggiungi la costante `TableGitCredential` per identificare la tabella:
   ```go
   TableGitCredential = "git_credential"
   ```
3. Aggiungi le seguenti definizioni e indici all'array `Definition`:
   ```go
   fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableGitCredential),
   fmt.Sprintf("DEFINE INDEX credential_target ON TABLE %s COLUMNS target UNIQUE;", TableGitCredential),
   ```
4. Esegui la build per verificare la sintassi.

---

## Fase 2: Core Credential Store (`internal/gitrepo` - Types & Store)
**Obiettivo:** Creare le struct per rappresentare le credenziali e la logica per salvarle/leggerle da SurrealDB.

### Passi operativi:
1. Crea `internal/gitrepo/types.go` e dichiara la struct `Credential`:
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
       Target        string   `json:"target"` // Dominio (es. github.com) o repo URL completo
       Provider      string   `json:"provider"`
       AuthType      AuthType `json:"auth_type"`
       Token         string   `json:"token,omitempty"`
       Username      string   `json:"username,omitempty"`
       SSHPrivateKey string   `json:"ssh_private_key,omitempty"`
   }
   ```
2. Crea `internal/gitrepo/store.go` per implementare lo store delle credenziali:
   ```go
   package gitrepo

   import (
       "context"
       "fmt"
       "github.com/terenzif/ibis-arc/internal/db"
       "github.com/terenzif/ibis-arc/internal/schema"
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
       // Parsing del risultato
       // ...
       return nil, nil // Ritorna credenziale se trovata
   }
   ```
3. Scrivi i relativi test unitari in `internal/gitrepo/store_test.go`.

---

## Fase 3: Git Command Runner con Log Masking (`internal/gitrepo` - Runner)
**Obiettivo:** Eseguire comandi Git iniettando le credenziali corrette a seconda del provider e mascherando i token sensibili nei log/errori.

### Passi operativi:
1. Crea `internal/gitrepo/runner.go`.
2. Implementa `GitCommandRunner` che:
   - Formatta gli argomenti di Git (es. modificando l'URL per inserire `http.extraHeader` o il token base64 basic auth).
   - Esegue `exec.CommandContext`.
   - Intercetta l'output e rimuove/sostituisce stringhe sensibili come PAT e token in modo che non vengano stampate nei log o ritornate negli errori.
3. Scrivi i relativi test unitari in `internal/gitrepo/runner_test.go`.

---

## Fase 4: Integrazione Context e Config (Lettura header `X-Git-Token`)
**Obiettivo:** Abilitare il passaggio di token runtime dal client HTTP al contesto di esecuzione delle operazioni.

### Passi operativi:
1. Modifica `internal/auth/keys.go` aggiungendo la costante `GitTokenContextKey`.
2. In `cmd/server/main.go`, modifica `AuthMiddleware` per intercettare l'header `X-Git-Token` (o `X-Git-PAT`) e iniettarlo nel contesto.
3. Modifica la struct `Config` in `internal/config/config.go` affinché la funzione di ricerca credenziali interroghi anche il nuovo store se presente.

---

## Fase 5: Rifattorizzazione Sincronizzazione Workspace (`internal/ingest/dynamic/workspace.go`)
**Obiettivo:** Sostituire l'uso delle chiamate `exec.CommandContext` grezze in `SyncWorkspace` con il nuovo `GitCommandRunner`.

### Passi operativi:
1. Apri `internal/ingest/dynamic/workspace.go`.
2. Modifica la firma di `SyncWorkspace` (o aggiungi una versione aggiornata) che riceve anche un `db.Executor` per accedere alle credenziali salvate, oltre che a quelle a livello di request nel `ctx`.
3. Integra la logica per cercare prima le credenziali nel contesto (runtime), poi nel database tramite il target `originUrl` o dominio del provider, e infine come fallback in `config.json`.
4. Se l'esecuzione fallisce a causa di autorizzazione o se non vi sono credenziali per un repository privato, ritorna un errore strutturato ad-hoc (es. `CredentialsRequiredError`).

---

## Fase 6: Implementazione Tool MCP (`git_configure_credentials` & Gestione `init_project`)
**Obiettivo:** Esporre i nuovi strumenti MCP e aggiornare la gestione di errori in `init_project`.

### Passi operativi:
1. Apri `cmd/server/main.go`.
2. Aggiungi il nuovo tool MCP `git_configure_credentials`:
   - Prende come argomenti: `target`, `provider`, `auth_type`, `token`, `username`, `ssh_private_key`.
   - Salva i dati in SurrealDB tramite lo store creato in Fase 2.
3. Modifica il gestore di `init_project` e `update_project_status`:
   - Cattura l'errore `CredentialsRequiredError`.
   - Restituisce un payload JSON strutturato con `"status": "credentials_required"`.

---

## Fase 7: Validazione e Unit Testing
**Obiettivo:** Assicurare la stabilità globale del sistema.

### Passi operativi:
1. Esegui la suite di test globale: `go test ./...`.
2. Testa manualmente il flusso simulando la prima sincronizzazione di un repository privato e verificando che la richiesta di credenziali e il salvataggio funzionino correttamente.

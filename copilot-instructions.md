# Copilot Instructions — Template per Client MCP

> Copia/incolla questa sezione nel progetto client che consuma il Knowledge Server.



### Configurazione MCP client

- Connetti il server remoto via SSE: `http://localhost:3333/sse`
- Usa `X-Redmine-API-Key` dell'utente corrente per operazioni ticket personalizzate/scrittura.
- Non chiamare manualmente `/message`: è gestito dal client MCP.

### Tool disponibili (client-side usage)

3. `init_project(project_name, origin_url, branch, commit?)`
   - Entrypoint iniziale obbligatorio all'avvio della sessione per allineare il Knowledge Server al branch e al commit attuale del client.

4. `update_project_status(project_name, origin_url, branch, commit)`
   - Da richiamare dopo commit o push locali per aggiornare l'ingestione vettoriale incrementale in background.

5. `ask_project(query, branch_or_commit?)`
   - Usa questo tool come entrypoint principale per la comprensione del codice e la ricerca di impatti. Fornire sempre il contesto del branch/commit corrente se disponibile.

6. `provide_collaborative_memory(project_name, memory_text, embedding?)`
   - Usa per iniettare contesto di business o regole di progetto che ritieni utili per l'intero team. Se calcoli l'embedding localmente, passalo per ottimizzare i costi server.

7. `save_reasoning_outcome(project_name, question, outcome_text, useful_sources)`
   - Usa per storicizzare una tua deduzione semantica utile (outcome) e rinforzare automaticamente i pesi degli issue o commit (sources) che ti hanno aiutato a rispondere. Questo arricchisce in modalità "Dual-Loop" il grafo aziendale.

8. `redmine_search_issues(query, limit?, offset?, sort?)`
   - Ricerca rapida per subject.
   - Default: `limit=10`, `offset=0`, `sort=updated_on:desc`.

3. `redmine_search_issues_advanced(query?, project_id?, status_id?, tracker_id?, assigned_to_id?, author_id?, priority_id?, updated_from?, updated_to?, limit?, offset?, sort?)`
   - Usa per triage operativo con filtri composti e paginazione.

4. `redmine_search_my_issues(query?, status_id?, project_id?, tracker_id?, priority_id?, updated_from?, updated_to?, limit?, offset?, sort?)`
   - Cerca ticket assegnati all'utente corrente (`assigned_to_id=me`).

5. `redmine_get_issue(id)`
   - Recupera dettagli completi ticket (inclusi journals lato API Redmine).

6. `redmine_update_issue(id, notes?, status_id?, priority_id?, assigned_to_id?, fixed_version_id?)`
   - Aggiorna ticket con note e/o campi autorizzati.
   - Richiede sempre chiave utente valida.

### Playbook consigliato per ticket

1. Cerca: `redmine_search_issues` o `redmine_search_issues_advanced`
2. Verifica: `redmine_get_issue(id)`
3. Aggiorna: `redmine_update_issue(...)`
4. Ri-leggi ticket per confermare stato finale

### Errori comuni

- `Permission denied` su update: manca header `X-Redmine-API-Key` utente.
- Zero risultati: query troppo generica o filtri troppo restrittivi.
- Errore validazione sort/date: usare sort whitelist e date `YYYY-MM-DD` o RFC3339.
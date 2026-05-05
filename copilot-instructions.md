# Copilot Instructions — `knowledge_server` (repo server MCP)

## Scopo di questo file

Queste istruzioni sono per lavorare **sul server MCP** in questo repository (`knowledge_server`), non per i progetti client.

Quando devo fare modifiche qui dentro:

- tratto `cmd/server/main.go` e `internal/ingest/redmine/ingest.go` come source of truth dei tool MCP;
- mantengo retrocompatibilità dei nomi tool già pubblici;
- aggiungo sempre test quando estendo parametri, validazioni o payload Redmine;
- aggiorno documentazione (`README.md`, `docs/*`) e backlog (`docs/project_tasks.md`) insieme al codice.

## Convenzioni operative Redmine in questo repo

- Le chiamate in scrittura richiedono `X-Redmine-API-Key` utente valido nel contesto.
- I filtri di ricerca devono essere validati lato server (range, sort whitelist, date).
- Output tool di ricerca: preferire JSON strutturato + campo compatto leggibile in chat.

---

## Template da copiare nel `copilot-instructions.md` di un CLIENTE MCP

> Copia/incolla questa sezione nel progetto client che consuma il Knowledge Server.

### Configurazione MCP client

- Connetti il server remoto via SSE: `http://localhost:3333/sse`
- Usa `X-Redmine-API-Key` dell'utente corrente per operazioni ticket personalizzate/scrittura.
- Non chiamare manualmente `/message`: è gestito dal client MCP.

### Tool disponibili (client-side usage)

1. `ask_project(query)`
   - Usa questo tool come entrypoint principale per capire codice, storia e impatti.

2. `redmine_search_issues(query, limit?, offset?, sort?)`
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
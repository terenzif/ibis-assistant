# Copilot Instructions — Ticketing + PR + Git MCP Client

> Copia/incolla questa sezione nel progetto client che consuma il Ibis Assistant.

### Configurazione MCP client

- Connetti il server remoto via SSE (es. `http://localhost:3333/sse`)
- Per override credenziali runtime usa gli header provider:
  - `X-Redmine-API-Key`
  - `X-Jira-Email`, `X-Jira-API-Token`
  - `X-Azure-DevOps-PAT`
  - `X-Git-Token`, `X-Git-PAT` (per override runtime di repository/provider Git privati)
- Non chiamare manualmente `/message`: è gestito dal client MCP.

### Tool principali (client-side usage)

1. `init_project(project_name, origin_url, branch, commit?)`
   * Ritorna `{"status": "credentials_required", ...}` se il repository richiede credenziali.
2. `update_project_status(project_name, origin_url, branch, commit)`
3. `ask_project(query, branch_or_commit?)`
4. `provide_collaborative_memory(project_name, memory_text, embedding?)`
5. `save_reasoning_outcome(project_name, question, outcome_text, useful_sources)`
6. `git_configure_credentials(target, provider, auth_type, token, username?, ssh_private_key?)`
   * Configura credenziali persistenti su SurrealDB (domain o URL specifico).

### Tool ticketing (`ticket_*`)

- Discovery:
  - `ticket_get_capabilities()`

- Search & Read:
  - `ticket_search(provider?, query?, project_key?, status?, type?, assignee?, author?, priority?, updated_from?, updated_to?, limit?, offset?, sort?)`
  - `ticket_search_my(provider?, query?, project_key?, status?, type?, priority?, updated_from?, updated_to?, limit?, offset?, sort?)`
  - `ticket_get(provider?, id, project_key?)`
  - `ticket_list_statuses(provider?, project_key?, issue_type?)`
  - `ticket_search_users(provider?, project_key?, query?, limit?)`
  - `ticket_list_projects(provider?)`

- Write & Workflow:
  - `ticket_create(provider?, project_key, title, description?, type?, assignee?, priority?, provider_fields_json?)`
  - `ticket_update(provider?, id, project_key?, notes?, status?, type?, assignee?, priority?, workflow_action?, fixed_version?, provider_fields_json?)`
  - `ticket_add_comment(provider?, id, project_key?, comment)`
  - `ticket_assign(provider?, id, project_key?, assignee)`
  - `ticket_transition(provider?, id, project_key?, transition)`
  - `ticket_mark_resolved(provider?, id, project_key?, issue_type?, notes?)`
  - `ticket_mark_closed(provider?, id, project_key?, issue_type?, notes?)`
  - `ticket_reopen(provider?, id, project_key?, issue_type?, notes?)`

### Tool PR (`repo_pr_*`)

- `repo_pr_create(provider?, project_name?, origin_url?, repository?, source_branch?, target_branch?, title?, description?, ticket_ids?, reviewer_ids?, auto_complete?)`
- `repo_pr_complete(provider?, project_name?, repository?, pr_id, delete_source_branch?, squash?)`

### Playbook operativo (consigliato)

1. Cerca ticket: `ticket_search` o `ticket_search_my`
2. Verifica dettagli: `ticket_get`
3. Dev conclude fix: `ticket_mark_resolved`
4. Crea PR: `repo_pr_create`
5. Tester verifica e chiude: `ticket_mark_closed`

### Errori comuni

- `provider not registered`: provider non configurato in `config/ticketing_config.json`
- `permission denied` / auth errors: header credenziali utente mancanti/invalidi
- `workflow target not found`: mapping status non configurato e fallback euristico sufficiente
- `credentials_required` (in `init_project`): il repository richiede credenziali. L'agente client deve richiedere un PAT all'utente e configurarlo usando `git_configure_credentials` o inserirlo in `X-Git-Token` ad ogni chiamata.


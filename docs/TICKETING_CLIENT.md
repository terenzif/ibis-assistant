# Ticketing + PR Client Guide

Questa guida descrive il nuovo flusso operativo del Ibis Arc per ticketing multi-provider e automazione PR.

## 1) Configurazione server

Il server legge la configurazione ticketing da:

- `config/ticketing_config.json`

Usa `config/ticketing_config.example.json` come riferimento completo.

Parametri principali:

- `default_provider`
- `project_provider_map`
- `providers.redmine|jira|azure_devops`
- `workflow.actions.resolve|close|reopen`
- `reference_patterns`
- `default_create_type`
- `pr.default_target_branch`, `pr.project_target_branch`

## 2) Header runtime client (override credenziali)

Il server usa credenziali di default dal file config, ma puoi fare override per-request via header:

- Redmine: `X-Redmine-API-Key`
- Jira: `X-Jira-Email`, `X-Jira-API-Token`
- Azure DevOps: `X-Azure-DevOps-PAT`

Header ADO opzionali di routing:

- `X-Azure-DevOps-Org`
- `X-Azure-DevOps-Project`
- `X-Azure-DevOps-Repo`

Policy sicurezza:

- credenziali request non persistite
- masking nei log

## 3) Tool MCP Ticketing

### Discovery

- `ticket_get_capabilities`

### Ricerca e lettura

- `ticket_search`
- `ticket_search_my`
- `ticket_get`
- `ticket_list_statuses`
- `ticket_search_users`
- `ticket_list_projects`

### Scrittura e workflow

- `ticket_create`
- `ticket_update` (supporta `workflow_action=resolve|close|reopen`)
- `ticket_add_comment`
- `ticket_assign`
- `ticket_transition`
- `ticket_mark_resolved`
- `ticket_mark_closed`
- `ticket_reopen`

## 4) Flusso operativo consigliato (Dev -> Tester)

### Developer

1. Cerca ticket: `ticket_search` o `ticket_search_my`
2. Esegui fix su branch feature
3. Aggiorna ticket come risolto:
   - `ticket_mark_resolved`
   - opzionale nota tecnica `notes`
4. Crea PR:
   - `repo_pr_create`

### Tester

1. Verifica PR / build
2. Se fix confermato, chiude ticket:
   - `ticket_mark_closed`
3. Se fix non valido, riapre ticket:
   - `ticket_reopen`

## 5) Tool MCP PR Automation

- `repo_pr_create`
- `repo_pr_complete`

Comportamenti principali:

- provider routing come ticketing (`provider > project map > default`)
- fallback a sessione `init_project` per `project_name/origin_url/source_branch`
- `ticket_ids` opzionale + auto-detect da branch/titolo/descrizione
- `title/description` auto-generated con override possibile
- reviewer opzionali in create

## 6) Esempio rapido

### Mark resolved

```json
{
  "provider": "redmine",
  "id": "123",
  "project_key": "core",
  "notes": "Fix applicata, in attesa QA"
}
```

### Create PR

```json
{
  "provider": "azure_devops",
  "project_name": "platform",
  "source_branch": "feature/AB#456-fix-timeout",
  "reviewer_ids": "user-guid-1,user-guid-2",
  "auto_complete": "false"
}
```

## 7) Note di compatibilità

- I vecchi tool `redmine_*` sono stati rimossi.
- Usa esclusivamente i nuovi tool `ticket_*`.
- Modello grafo issue unificato con metadata provider (`provider`, `external_id`, `external_key`).

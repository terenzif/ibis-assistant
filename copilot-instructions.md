# Copilot Instructions — Ticketing + PR + Git MCP Client

> Paste this into a client project that consumes Ibis Assistant.

### MCP client setup

- Connect over SSE (e.g. `http://localhost:3030/sse`)
- Runtime credential overrides via headers:
  - `X-Redmine-API-Key`
  - `X-Jira-Email`, `X-Jira-API-Token`
  - `X-Azure-DevOps-PAT`
  - `X-Git-Token`, `X-Git-PAT` (private Git remotes)
- Do not call `/message` manually; the MCP client owns that.

### Core tools

1. `init_project(project_name, origin_url, branch, commit?)`
   * May return `{"status": "credentials_required", ...}` when auth is missing.
2. `update_project_status(project_name, origin_url, branch, commit)`
3. `ask_project(query, branch_or_commit?)`
4. `provide_collaborative_memory(project_name, memory_text, embedding?)`
5. `save_reasoning_outcome(project_name, question, outcome_text, useful_sources)`
6. `git_configure_credentials(target, provider, auth_type, token, username?, ssh_private_key?)`
   * Persists credentials in SurrealDB (domain or full repo URL).

### Ticketing tools (`ticket_*`)

- Discovery:
  - `ticket_get_capabilities()`

- Search & read:
  - `ticket_search(provider?, query?, project_key?, status?, type?, assignee?, author?, priority?, updated_from?, updated_to?, limit?, offset?, sort?)`
  - `ticket_search_my(provider?, query?, project_key?, status?, type?, priority?, updated_from?, updated_to?, limit?, offset?, sort?)`
  - `ticket_get(provider?, id, project_key?)`
  - `ticket_list_statuses(provider?, project_key?, issue_type?)`
  - `ticket_search_users(provider?, project_key?, query?, limit?)`
  - `ticket_list_projects(provider)`

- Write & workflow:
  - `ticket_create(provider?, project_key, title, description?, type?, assignee?, priority?, provider_fields_json?)`
  - `ticket_update(provider?, id, project_key?, notes?, status?, type?, assignee?, priority?, workflow_action?, fixed_version?, provider_fields_json?)`
  - `ticket_add_comment(provider?, id, project_key?, comment)`
  - `ticket_assign(provider?, id, project_key?, assignee)`
  - `ticket_transition(provider?, id, project_key?, transition)`
  - `ticket_mark_resolved(provider?, id, project_key?, issue_type?, notes?)`
  - `ticket_mark_closed(provider?, id, project_key?, issue_type?, notes?)`
  - `ticket_reopen(provider?, id, project_key?, issue_type?, notes?)`

### PR tools (`repo_pr_*`)

- `repo_pr_create(provider?, project_name?, origin_url?, repository?, source_branch?, target_branch?, title?, description?, ticket_ids?, reviewer_ids?, auto_complete?)`
- `repo_pr_complete(provider?, project_name?, repository?, pr_id, delete_source_branch?, squash?)`

### Suggested playbook

1. Find ticket: `ticket_search` / `ticket_search_my`
2. Inspect: `ticket_get`
3. Dev finishes fix: `ticket_mark_resolved`
4. Open PR: `repo_pr_create`
5. Tester closes: `ticket_mark_closed`

### Common errors

- `provider not registered`: missing entry in `config/ticketing_config.json`
- `permission denied` / auth errors: missing/invalid user credential headers
- `workflow target not found`: status mapping incomplete; heuristic fallback may apply
- `credentials_required` (`init_project`): ask the user for a PAT and call `git_configure_credentials`, or pass `X-Git-Token` on each call

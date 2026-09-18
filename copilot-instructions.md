# Copilot Instructions — Ticketing + PR + Git MCP Client

> Paste this into a client project that consumes Ibis Assistant.

### MCP client setup

- Connect over Streamable HTTP (e.g. `http://localhost:3030/mcp`). Legacy SSE `http://localhost:3030/sse` remains for `mcp-bridge`.
- Personal/plugin clients can instead spawn `ibis-assistant -mode stdio` with `RUNTIME_MODE=plugin` (Cursor plugin).
- After connect, `GET /` (`Accept: application/json`) or MCP `server/discover` + resource `ibis://guide` is enough to attach and show install/usage to a human.
- Runtime credential overrides via headers:
  - `X-Redmine-API-Key`
  - `X-Jira-Email`, `X-Jira-API-Token`
  - `X-Azure-DevOps-PAT`
  - `X-Git-Token`, `X-Git-PAT` (private Git remotes)
- Do not call `/message` manually; the MCP client owns that.

### Core tools

1. `init_project(project_name, origin_url, branch, commit?)`
   * **Blocks** until git sync and ingest finish (progress notifications if you send `progressToken`).
   * Personal/plugin: uses the live working tree (no clone / `reset --hard`).
   * May return `{"status": "credentials_required", ...}` when auth is missing.
   * May return `{"status": "requires_patch", "closest_known_commit": "..."}` on **server-owned clones** when the tip is not on the remote — follow with `sync_local_patch`.
   * May return `{"status": "aligned", ...}` when ingestion completed.
2. `sync_local_patch(project_name, patch, commit?)`
   * `patch` is a unified diff against a **server clone**. On a live personal/plugin tree the tool returns `{"status":"live_tree"}` and does not apply a patch.
3. `update_project_status(project_name, origin_url, branch, commit)`
4. `ask_project(query, branch_or_commit?)`
5. `ingest_code` — AST/vector ingest (ast-grep + `rules/` + optional AI rule synth)
6. `analyze_logs(project_name, log_text, log_file?)` — static → AI → enrich; see `docs/log_analysis_pipeline.md`
7. `analyze_blast_radius(...)` / `find_dead_code(repo_name)`
8. `provide_collaborative_memory(project_name, memory_text, embedding?)`
9. `save_reasoning_outcome(project_name, question, outcome_text, useful_sources)`
10. `optimize_knowledge(iterations?)`
11. `git_configure_credentials(target, provider, auth_type, token, username?, ssh_private_key?)`
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

### Local unpushed commit playbook

1. `init_project(...)` → if `requires_patch`, note `closest_known_commit`
2. Produce a unified diff of local commits not on the remote
3. `sync_local_patch(project_name, patch, commit?)`
4. Continue with `ask_project` / `ingest_code` as needed

### Log triage playbook

1. `analyze_logs(project_name, log_text, log_file?)` (or CLI `logs analyze`)
2. Prefer results that name a source file and cause (enrich path)
3. Optionally re-ingest code if enrich lacked graph context

### Common errors

- `provider not registered`: missing entry in `config/ticketing_config.json`
- `permission denied` / auth errors: missing/invalid user credential headers
- `workflow target not found`: status mapping incomplete; heuristic fallback may apply
- `credentials_required` (`init_project`): ask the user for a PAT and call `git_configure_credentials`, or pass `X-Git-Token` on each call
- `requires_patch` (`init_project`): call `sync_local_patch` with a unified diff; do not invent a remote SHA. On personal/plugin live trees this status is not used for ordinary dirty work.
- `live_tree` (`sync_local_patch`): working tree is already visible; do not expect a cloned workspace.
- `workspace not found` (`sync_local_patch`): run `init_project` first (server clones) or set `projects[].working_repo_path`.

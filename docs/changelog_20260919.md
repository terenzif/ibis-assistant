# Changelog — 19 Sep 2026

## Hybrid AI, multi-cloud, and Settings UX

Shipped hybrid local/cloud reasoning with hardware auto-tier, multi-cloud keys (Gemini, OpenAI-compatible, Claude), CLI wizard `config fast` / `config full`, and a shared Settings UI.

### Operator-facing

- Default `ai.reasoning.provider=hybrid`, `model=auto`
- Embeddings remain Ollama `nomic-embed-text`
- Local chat via Ollama; auto-tier S/M/L/XL (`granite4.1:3b` … `muse-glimmer`)
- Keys entered in wizard/GUI into `config.json` (ENV optional override only)
- `use_cloud_when_no_gpu` (UI: “On CPU-only PCs, use the cloud for hard questions”)
- Personal HTTP: `GET /settings/`, `GET|POST /api/v1/settings`
- MCP: `settings_get`, `settings_apply`, `settings_open_ui`
- Cursor command **Ibis: Open Settings**

### Docs

- Operator guide: [ai_and_settings.md](ai_and_settings.md)
- Design: [superpowers/specs/2026-09-19-hybrid-reasoning-settings-design.md](superpowers/specs/2026-09-19-hybrid-reasoning-settings-design.md)
- Plan: [superpowers/plans/2026-09-19-hybrid-reasoning-settings.md](superpowers/plans/2026-09-19-hybrid-reasoning-settings.md)
- Living install guide (`GET /`, `ibis://guide`) includes AI/Settings section
- Client guides: [CLAUDE_INTEGRATION.md](CLAUDE_INTEGRATION.md), [../copilot-instructions.md](../copilot-instructions.md), [../cursor-plugin/README.md](../cursor-plugin/README.md)

### Prior day

See [changelog_20260918.md](changelog_20260918.md) for runtime modes / Cursor plugin.

# Hybrid Reasoning + Settings UX Implementation Plan

> **For agentic workers:** Implement task-by-task. Steps use checkbox syntax.

**Goal:** Ship hybrid local/cloud reasoning, multi-cloud (Gemini/OpenAI-compat/Claude), hardware auto-tier, SettingsEngine, CLI `config fast|full`, and shared `/settings` UI for personal HTTP + Cursor MCP.

**Architecture:** Extend `config.ReasoningConfig`; add `internal/settings` (probe/recommend/apply), `internal/ai` providers (ollama chat, openai_compat, claude, cloudpool, router); rewrite wizard; serve static settings UI from `internal/httpserver`.

**Tech Stack:** Go, existing mcp-go HTTP mux, Ollama HTTP APIs, Anthropic Messages API, OpenAI chat completions.

## Global Constraints

- English-only UX strings
- Keys in config.json via wizard/GUI; ENV optional override only
- `use_cloud_when_no_gpu` (never `prefer_cloud_on_cpu`)
- Default `reasoning.provider=hybrid`, `model=auto`
- Embeddings remain `nomic-embed-text`
- Spec: `docs/superpowers/specs/2026-09-19-hybrid-reasoning-settings-design.md`

## File map

| Path | Role |
|------|------|
| `internal/config/config.go` | Clouds, routing, defaults, legacy migrate |
| `internal/settings/*.go` | Probe, Recommend, Apply, MaskSecret |
| `internal/config/wizard.go` | `fast` / `full` English wizard |
| `cmd/server/main.go` | CLI config subcommands; AI wiring |
| `internal/ai/ollama_chat.go` | Ollama `/api/chat` |
| `internal/ai/openai_compat.go` | OpenAI-compat chat |
| `internal/ai/claude.go` | Claude Messages API |
| `internal/ai/cloud_pool.go` | Multi-cloud failover |
| `internal/ai/router.go` | Hybrid ReasoningRouter |
| `internal/ai/types.go` | RouteHint on GenerationConfig |
| `internal/httpserver/*` | `/settings` + API |
| `internal/settings/web/*` | Static HTML/JS/CSS |
| MCP tools in `cmd/server` | `settings_get/apply/open_ui` |
| `cursor-plugin/commands/ibis-settings.md` | Open Settings command |

### Task 1: Config model + migrate

- Extend ReasoningConfig with Clouds, Routing, ModelOverrides, ContextOnlyFallback
- Defaults hybrid/auto; migrate legacy keys → clouds.gemini
- Tests for migrate + default

### Task 2: SettingsEngine + hwprobe

- Probe (Ollama/WMI/fallback), tier S/M/L/XL, Recommend/Apply
- Tests for tier table and cloud autoset

### Task 3: CLI wizard fast/full

- English rewrite; `config`, `config fast`, `config full`, `--show`, `--recommend`

### Task 4: Settings UI + MCP

- Embed static UI; `/settings`, `/api/v1/settings`; MCP tools; plugin command

### Task 5: Ollama chat + CloudPool + Router

- Wire in main.go; RouteHint; context-only error sentinel
- Provider unit tests with httptest

### Task 6: Docs touch

- Short README / changelog note pointing at settings UX

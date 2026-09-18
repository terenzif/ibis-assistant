# Naming

**Product brand:** Ibis Assistant  
**Slug:** `ibis-assistant` (binary, Windows service, PID file, MCP client key, Go module path)

| Surface | Name |
|---------|------|
| Display / docs | Ibis Assistant |
| Executable | `ibis-assistant` / `ibis-assistant.exe` |
| Go module | `github.com/terenzif/ibis-assistant` |
| GitHub | `https://github.com/terenzif/ibis-assistant` |
| Future IDE plugin | Ibis Assistant for Cursor (same brand; not scaffolded yet) |

The MCP process is an implementation detail (“the MCP server”), not a separate product name.

## Remotes

- **origin:** `terenzif/ibis-assistant` (canonical)
- **archive:** `terenzif/ibis-server-archive` — historical only; do not rename or push unless explicitly requested

## Local workspace folder

After pulling the rename, prefer renaming the checkout directory to match the slug (e.g. `…/ibis-assistant`) and reopen the editor on that root. The old folder name does not affect the Go module path.

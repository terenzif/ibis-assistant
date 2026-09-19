---
description: Open Ibis Assistant Settings UI (hybrid AI, cloud keys, local models)
---

# Ibis: Open Settings

Open or describe the Ibis Assistant settings UI.

1. Call MCP tool `settings_open_ui` on the `ibis-assistant` server and show the URL to the user.
2. If that fails, call `settings_get` and summarize probe + recommendations.
3. Prefer the browser page at `http://127.0.0.1:<port>/settings/` when personal mode is running.

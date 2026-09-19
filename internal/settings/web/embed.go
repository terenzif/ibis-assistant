package web

import "embed"

// FS holds the settings UI static files.
//
//go:embed index.html app.js style.css
var FS embed.FS

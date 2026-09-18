package code

import (
	"path/filepath"
	"strings"
)

// Built-in extensions that tree-sitter/ast-grep usually parse without languageGlobs.
// Sparse AST on these still may trigger synthesis (novel APIs), but unknown ext
// always prefer synthesis + optional languageGlob mapping.
var knownSGLanguages = map[string]string{
	".go": "go", ".py": "python", ".js": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".ts": "typescript", ".tsx": "typescript", ".jsx": "javascript",
	".java": "java", ".cs": "csharp", ".cpp": "cpp", ".cc": "cpp", ".cxx": "cpp",
	".c": "c", ".h": "c", ".hpp": "cpp",
	".html": "html", ".htm": "html", ".css": "css", ".scss": "css",
	".json": "json", ".yaml": "yaml", ".yml": "yaml", ".rs": "rust",
	".rb": "ruby", ".php": "php", ".kt": "kotlin", ".swift": "swift",
	".sql": "sql",
}

// Template-ish hosts often need languageGlobs → html so embedded script/style inject.
// This is a bootstrap hint list, not an ASPX-specific pipeline; AI may extend it.
var defaultHTMLGlobExts = []string{
	".aspx", ".ascx", ".vue", ".svelte", ".astro", ".cshtml", ".razor",
	".jsp", ".jspx", ".erb", ".ejs", ".hbs", ".mustache", ".njk", ".liquid",
	".twig", ".blade.php",
}

// NeedsRuleSynthesis reports whether ingest should ask the AI for new ast-grep rules.
// Universal: any non-trivial file with empty/weak structural extraction.
func NeedsRuleSynthesis(relPath string, chunks []ASTChunk, calls []CallEdge, content []byte) bool {
	if len(content) < 40 {
		return false
	}
	if len(chunks) > 0 || len(calls) > 0 {
		return false
	}
	ext := strings.ToLower(filepath.Ext(relPath))
	if ext == "" {
		return true // extension-less legacy / DSL
	}
	// Always synthesize when empty for unknown or template-like extensions.
	if _, known := knownSGLanguages[ext]; !known {
		return true
	}
	for _, h := range defaultHTMLGlobExts {
		if ext == h || strings.HasSuffix(strings.ToLower(relPath), h) {
			return true
		}
	}
	// Known language but zero structure on a sizable file → weak coverage.
	return len(content) >= 200
}

// SuggestHostLanguage proposes an initial sg language / languageGlob host for an extension.
func SuggestHostLanguage(relPath string) string {
	ext := strings.ToLower(filepath.Ext(relPath))
	if lang, ok := knownSGLanguages[ext]; ok {
		return lang
	}
	lower := strings.ToLower(relPath)
	for _, h := range defaultHTMLGlobExts {
		if ext == h || strings.HasSuffix(lower, h) {
			return "html"
		}
	}
	return ""
}

// IsHTMLHostExt reports whether we should ensure a languageGlob html mapping for this path.
func IsHTMLHostExt(relPath string) bool {
	return SuggestHostLanguage(relPath) == "html" && knownSGLanguages[strings.ToLower(filepath.Ext(relPath))] != "html"
}

package logs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/terenzif/ibis-assistant/internal/ai"
	"github.com/terenzif/ibis-assistant/internal/db"
	"github.com/terenzif/ibis-assistant/internal/schema"
	"github.com/terenzif/ibis-assistant/internal/search"
)

// TemporalBaseline is the code revision used when correlating log frames to source.
type TemporalBaseline struct {
	Source string // session | workspace_head | db_branch | project_record | unknown
	Commit string
	Branch string
	Repo   string
}

func (b TemporalBaseline) String() string {
	parts := []string{}
	if b.Repo != "" {
		parts = append(parts, "repo="+b.Repo)
	}
	if b.Branch != "" {
		parts = append(parts, "branch="+b.Branch)
	}
	if b.Commit != "" {
		c := b.Commit
		if len(c) > 12 {
			c = c[:12]
		}
		parts = append(parts, "commit="+c)
	}
	if b.Source != "" {
		parts = append(parts, "via="+b.Source)
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, " ")
}

var (
	// Go panic / runtime frames: at pkg.(*T).M (file.go:12) or file.go:12
	reGoParenFrame = regexp.MustCompile(`(?i)\(([^()\s]+\.[a-z0-9]+):(\d+)\)`)
	reGoAtFrame    = regexp.MustCompile(`(?i)(?:^|\s)(?:at\s+)?([\w./\\-]+\.[a-z0-9]+):(\d+)\b`)
	reJavaFrame    = regexp.MustCompile(`(?i)at\s+([\w.$]+)\(([^()]+\.[a-z0-9]+):(\d+)\)`)
	reBarePath     = regexp.MustCompile(`(?i)\b((?:[\w.-]+[\\/])+[\w.-]+\.[a-z0-9]{1,8})\b`)
	reSymbolHint   = regexp.MustCompile(`(?i)(?:\*\.)?([A-Za-z_][\w]*)\s*(?:\(|$)`)
)

// extractStackLocation pulls the most specific file/line/symbol from stack-like text.
func extractStackLocation(text string) (file string, line int, symbol string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", 0, ""
	}

	if m := reJavaFrame.FindStringSubmatch(text); len(m) == 4 {
		symbol = m[1]
		if idx := strings.LastIndex(symbol, "."); idx >= 0 {
			symbol = symbol[idx+1:]
		}
		return normalizeRelPath(m[2]), atoiSafe(m[3]), symbol
	}
	if m := reGoParenFrame.FindStringSubmatch(text); len(m) == 3 {
		sym := ""
		if sm := regexp.MustCompile(`(?i)(?:\*\))?\.?([A-Za-z_][\w]*)\s+\(`).FindStringSubmatch(text); len(sm) == 2 {
			sym = sm[1]
		}
		return normalizeRelPath(m[1]), atoiSafe(m[2]), sym
	}
	if m := reGoAtFrame.FindStringSubmatch(text); len(m) == 3 {
		return normalizeRelPath(m[1]), atoiSafe(m[2]), ""
	}
	if m := reBarePath.FindStringSubmatch(text); len(m) == 2 {
		return normalizeRelPath(m[1]), 0, ""
	}
	return "", 0, ""
}

func normalizeRelPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, `\`, `/`)
	p = strings.TrimPrefix(p, "./")
	return p
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func (a *LogAnalyzer) resolveBaseline(ctx context.Context) TemporalBaseline {
	b := TemporalBaseline{Repo: a.Project, Source: "unknown"}

	// 1) Project record written by init_project / prior analysis
	if a.DB != nil && a.Project != "" {
		projectID := db.FormatRecordID(schema.TableProject, db.SanitizeID(a.Project))
		res, err := a.DB.Execute(ctx, fmt.Sprintf(
			"SELECT baseline_commit, baseline_branch, baseline_source, name FROM %s;", projectID))
		if err == nil {
			if rows, ok := res.([]interface{}); ok && len(rows) > 0 {
				if row, ok := rows[0].(map[string]interface{}); ok {
					if c, _ := row["baseline_commit"].(string); c != "" {
						b.Commit = c
						b.Branch, _ = row["baseline_branch"].(string)
						b.Source, _ = row["baseline_source"].(string)
						if b.Source == "" {
							b.Source = "project_record"
						}
						return b
					}
					if br, _ := row["baseline_branch"].(string); br != "" {
						b.Branch = br
						b.Source = "project_record"
					}
				}
			}
		}
	}

	// 2) Workspace HEAD under discovery_root
	if a.Cfg != nil && a.Cfg.DiscoveryRoot != "" && a.Project != "" {
		candidates := []string{
			filepath.Join(a.Cfg.DiscoveryRoot, "dynamic", a.Project),
			filepath.Join(a.Cfg.DiscoveryRoot, a.Project),
		}
		for _, dir := range candidates {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
				continue
			}
			commit, branch := gitHeadAndBranch(dir)
			if commit != "" {
				b.Commit = commit
				b.Branch = branch
				b.Source = "workspace_head"
				a.persistBaseline(ctx, b)
				return b
			}
		}
	}

	// 3) DB branch tip for a repo named like the project
	if a.DB != nil && a.Project != "" {
		repoID := db.FormatRecordID(schema.TableRepo, db.SanitizeID(a.Project))
		ql := fmt.Sprintf(`
			SELECT name,
				->%s->%s.name as branches,
				->%s->%s->%s->%s.hash as tip_hashes
			FROM %s LIMIT 1;`,
			schema.EdgeContains, schema.TableBranch,
			schema.EdgeContains, schema.TableBranch, schema.EdgePointedTo, schema.TableCommit,
			repoID)
		res, err := a.DB.Execute(ctx, ql)
		if err == nil {
			if rows, ok := res.([]interface{}); ok && len(rows) > 0 {
				if row, ok := rows[0].(map[string]interface{}); ok {
					if hashes := coerceStringSlice(row["tip_hashes"]); len(hashes) > 0 {
						b.Commit = hashes[0]
						b.Source = "db_branch"
						if branches := coerceStringSlice(row["branches"]); len(branches) > 0 {
							b.Branch = branches[0]
						}
						a.persistBaseline(ctx, b)
						return b
					}
				}
			}
		}
	}

	return b
}

func gitHeadAndBranch(repoDir string) (commit, branch string) {
	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", ""
	}
	commit = strings.TrimSpace(string(out))
	bout, err := exec.Command("git", "-C", repoDir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err == nil {
		branch = strings.TrimSpace(string(bout))
		if branch == "HEAD" {
			branch = ""
		}
	}
	return commit, branch
}

func (a *LogAnalyzer) persistBaseline(ctx context.Context, b TemporalBaseline) {
	if a.DB == nil || a.Project == "" {
		return
	}
	projectID := db.FormatRecordID(schema.TableProject, db.SanitizeID(a.Project))
	_, _ = a.DB.Execute(ctx, fmt.Sprintf(
		"UPDATE %s SET baseline_commit = '%s', baseline_branch = '%s', baseline_source = '%s';",
		projectID, db.EscapeSQL(b.Commit), db.EscapeSQL(b.Branch), db.EscapeSQL(b.Source)))
}

func coerceStringSlice(v interface{}) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, x := range t {
			switch s := x.(type) {
			case string:
				if s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	case string:
		if t != "" {
			return []string{t}
		}
	}
	return nil
}

// enrichAnomalies fills File / Line / Symbol / Cause using stack parsing + SurrealDB / search.
func (a *LogAnalyzer) enrichAnomalies(ctx context.Context, errors []SemanticError) []SemanticError {
	if len(errors) == 0 {
		return errors
	}
	baseline := a.resolveBaseline(ctx)
	baselineStr := baseline.String()

	svc := &search.Service{DB: a.DB, AI: a.AI}

	for i := range errors {
		e := &errors[i]
		e.Baseline = baselineStr
		source := "none"

		// 1) Stack / template text
		locText := e.StackTrace
		if locText == "" {
			locText = e.Template
		}
		if f, line, sym := extractStackLocation(locText); f != "" {
			if e.File == "" {
				e.File = f
				source = "stack"
			}
			if e.Line == 0 && line > 0 {
				e.Line = line
			}
			if e.Symbol == "" && sym != "" {
				e.Symbol = sym
			}
		}

		// 2) Resolve basename → repo-relative path (prefer current ingest)
		if e.File != "" && !strings.Contains(e.File, "/") {
			if resolved := a.resolveSourcePath(ctx, e.File); resolved != "" {
				e.File = resolved
				if source == "stack" {
					source = "stack+db"
				} else {
					source = "db_path"
				}
			}
		}

		// 3) Keyword / hybrid search when file still missing or not in the graph
		snippet := ""
		if e.File == "" || !a.sourcePathKnown(ctx, e.File) {
			query := enrichmentQuery(*e)
			path, snip, hit := a.searchCodeContext(ctx, svc, query)
			if hit {
				snippet = snip
				if path != "" && (e.File == "" || !a.sourcePathKnown(ctx, e.File)) {
					e.File = path
					source = "search"
				}
			}
		} else if e.Cause == "" {
			// Still try to fetch a snippet for cause synthesis
			_, snip, hit := a.searchCodeContext(ctx, svc, enrichmentQuery(*e))
			if hit {
				snippet = snip
			}
		}

		// 4) Static log template already set SourceFile — ensure cause
		if e.File != "" && strings.HasPrefix(e.Category, "Static Log:") && e.Cause == "" {
			e.Cause = fmt.Sprintf("Matched AST log template in %s (line %d) against emitted format string.",
				e.File, e.Line)
			if source == "none" {
				source = "static_template"
			}
		}

		// 5) Cause from snippet / AI / heuristic
		if e.Cause == "" {
			e.Cause = a.synthesizeCause(ctx, *e, snippet, baseline)
		}

	}
	return errors
}

func enrichmentQuery(e SemanticError) string {
	parts := []string{}
	if e.Category != "" {
		parts = append(parts, e.Category)
	}
	msg := e.StackTrace
	if msg == "" {
		msg = e.Template
	}
	msg = stripLogNoise(msg)
	if msg != "" {
		parts = append(parts, msg)
	}
	q := strings.Join(parts, " ")
	if len(q) > 400 {
		q = q[:400]
	}
	return q
}

func stripLogNoise(s string) string {
	s = strings.TrimSpace(s)
	// Drop leading slog-ish key=value prefixes commonly seen in this project's logs.
	reKV := regexp.MustCompile(`(?i)^(?:time|level|msg)=("[^"]*"|[^\s]+)\s*`)
	for i := 0; i < 6; i++ {
		ns := reKV.ReplaceAllString(s, "")
		if ns == s {
			break
		}
		s = strings.TrimSpace(ns)
	}
	s = strings.Trim(s, `"'`)
	return s
}

func (a *LogAnalyzer) resolveSourcePath(ctx context.Context, basename string) string {
	if a.DB == nil || basename == "" {
		return ""
	}
	basename = filepath.Base(strings.ReplaceAll(basename, `\`, `/`))
	esc := db.EscapeSQL(basename)
	ql := fmt.Sprintf(`
		SELECT rel_path, path FROM %s
		WHERE rel_path = '%s'
		   OR string::ends_with(rel_path, '/%s')
		   OR string::ends_with(path, '/%s')
		   OR string::ends_with(path, '\\%s')
		LIMIT 8;`, schema.TableFile, esc, esc, esc, esc)
	res, err := a.DB.Execute(ctx, ql)
	if err != nil {
		return ""
	}
	rows, ok := res.([]interface{})
	if !ok {
		return ""
	}
	var fallback string
	for _, raw := range rows {
		row, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		rel, _ := row["rel_path"].(string)
		if rel == "" {
			if p, _ := row["path"].(string); p != "" {
				rel = normalizeRelPath(p)
			}
		}
		if rel == "" {
			continue
		}
		rel = normalizeRelPath(rel)
		if fallback == "" {
			fallback = rel
		}
		// Prefer paths that look like this repo (internal/..., cmd/...)
		if strings.HasPrefix(rel, "internal/") || strings.HasPrefix(rel, "cmd/") {
			return rel
		}
	}
	return fallback
}

func (a *LogAnalyzer) sourcePathKnown(ctx context.Context, path string) bool {
	if a.DB == nil || strings.TrimSpace(path) == "" {
		return false
	}
	path = normalizeRelPath(path)
	base := filepath.Base(path)
	escPath := db.EscapeSQL(path)
	escBase := db.EscapeSQL(base)
	ql := fmt.Sprintf(`
		SELECT count() AS c FROM %s
		WHERE rel_path = '%s'
		   OR path = '%s'
		   OR string::ends_with(rel_path, '/%s')
		   OR string::ends_with(rel_path, '%s')
		GROUP ALL;`, schema.TableFile, escPath, escPath, escBase, escBase)
	res, err := a.DB.Execute(ctx, ql)
	if err != nil {
		return false
	}
	rows, ok := res.([]interface{})
	if !ok || len(rows) == 0 {
		return false
	}
	row, ok := rows[0].(map[string]interface{})
	if !ok {
		return false
	}
	switch c := row["c"].(type) {
	case float64:
		return c > 0
	case int64:
		return c > 0
	case int:
		return c > 0
	}
	return false
}

func (a *LogAnalyzer) searchCodeContext(ctx context.Context, svc *search.Service, query string) (path, snippet string, hit bool) {
	query = strings.TrimSpace(query)
	if query == "" || a.DB == nil {
		return "", "", false
	}

	// A) Lexical search on file_chunk (works without embeddings)
	terms := distinctiveTerms(query, 4)
	for _, term := range terms {
		if len(term) < 4 {
			continue
		}
		esc := db.EscapeSQL(strings.ToLower(term))
		ql := fmt.Sprintf(`
			SELECT file.rel_path as rel_path, file.path as path, content
			FROM %s
			WHERE string::lowercase(content) CONTAINS '%s'
			LIMIT 5;`, schema.TableFileChunk, esc)
		res, err := a.DB.Execute(ctx, ql)
		if err != nil {
			continue
		}
		rows, ok := res.([]interface{})
		if !ok {
			continue
		}
		for _, raw := range rows {
			row, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			p, _ := row["rel_path"].(string)
			if p == "" {
				p, _ = row["path"].(string)
			}
			content, _ := row["content"].(string)
			if p != "" || content != "" {
				return normalizeRelPath(p), trimSnippet(content, 280), true
			}
		}
	}

	// B) Log templates by format_string / regex fragment
	for _, term := range terms {
		if len(term) < 4 {
			continue
		}
		esc := db.EscapeSQL(term)
		ql := fmt.Sprintf(`
			SELECT source_file, source_line, format_string
			FROM %s
			WHERE string::lowercase(format_string) CONTAINS string::lowercase('%s')
			LIMIT 3;`, schema.TableLogTemplate, esc)
		res, err := a.DB.Execute(ctx, ql)
		if err != nil {
			continue
		}
		rows, ok := res.([]interface{})
		if !ok || len(rows) == 0 {
			continue
		}
		if row, ok := rows[0].(map[string]interface{}); ok {
			p, _ := row["source_file"].(string)
			fs, _ := row["format_string"].(string)
			if p != "" {
				return normalizeRelPath(p), trimSnippet(fs, 200), true
			}
		}
	}

	// C) Hybrid vector search when embeddings work
	if svc != nil && a.AI != nil && a.AI.IsFunctional() {
		results, err := svc.AskProject(ctx, query)
		if err == nil {
			for _, r := range results {
				if r.Path == "" && r.Content == "" {
					continue
				}
				// Prefer current HEAD chunks when correlating to baseline
				if !r.IsCurrent {
					continue
				}
				return normalizeRelPath(r.Path), trimSnippet(r.Content, 280), true
			}
			for _, r := range results {
				if r.Path != "" || r.Content != "" {
					return normalizeRelPath(r.Path), trimSnippet(r.Content, 280), true
				}
			}
		}
	}

	return "", "", false
}

func distinctiveTerms(query string, limit int) []string {
	stop := map[string]bool{
		"error": true, "failed": true, "failure": true, "panic": true, "level": true,
		"time": true, "msg": true, "warn": true, "info": true, "debug": true,
		"the": true, "and": true, "for": true, "from": true, "with": true,
		"this": true, "that": true, "into": true, "unable": true, "cannot": true,
		"static": true, "log": true, "project": true,
	}
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	})
	seen := map[string]bool{}
	out := []string{}
	for _, f := range fields {
		if len(f) < 4 || stop[f] || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func trimSnippet(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (a *LogAnalyzer) synthesizeCause(ctx context.Context, e SemanticError, snippet string, baseline TemporalBaseline) string {
	if snippet != "" && e.File != "" {
		base := fmt.Sprintf("Correlated to %s", e.File)
		if e.Line > 0 {
			base += fmt.Sprintf(":%d", e.Line)
		}
		if e.Symbol != "" {
			base += fmt.Sprintf(" (%s)", e.Symbol)
		}
		base += fmt.Sprintf(" using baseline %s. Nearby code: %s", baseline.String(), snippet)
		if a.AI != nil && a.AI.IsFunctional() {
			if refined := a.refineCauseWithAI(ctx, e, snippet, baseline); refined != "" {
				return refined
			}
		}
		return trimSnippet(base, 500)
	}

	if e.File != "" {
		msg := fmt.Sprintf("Located at %s", e.File)
		if e.Line > 0 {
			msg += fmt.Sprintf(":%d", e.Line)
		}
		if e.Symbol != "" {
			msg += fmt.Sprintf(" in %s", e.Symbol)
		}
		msg += fmt.Sprintf(". Category: %s. Baseline: %s.", e.Category, baseline.String())
		return msg
	}

	if a.AI != nil && a.AI.IsFunctional() {
		if refined := a.refineCauseWithAI(ctx, e, snippet, baseline); refined != "" {
			return refined
		}
	}

	if e.Category != "" {
		return fmt.Sprintf("Classified as %q; no matching source location found in the knowledge graph for baseline %s.",
			e.Category, baseline.String())
	}
	return ""
}

func (a *LogAnalyzer) refineCauseWithAI(ctx context.Context, e SemanticError, snippet string, baseline TemporalBaseline) string {
	prompt := fmt.Sprintf(`You are diagnosing a production log anomaly for project %s (code baseline: %s).
Write ONE short root-cause sentence (max 40 words). Ground it in the code context when present. Do not invent APIs.
Category: %s
File: %s
Line: %d
Symbol: %s
Log/stack:
%s
Code context:
%s
Reply with plain text only.`,
		a.Project, baseline.String(), e.Category, e.File, e.Line, e.Symbol,
		trimSnippet(e.StackTrace, 400), trimSnippet(snippet, 400))

	contents := []ai.Content{{Parts: []ai.Part{{Text: prompt}}}}
	cfg := ai.GenerationConfig{Temperature: 0.1}
	cand, err := a.AI.GenerateContent(ctx, contents, cfg)
	if err != nil || len(cand.Content.Parts) == 0 {
		return ""
	}
	out := strings.TrimSpace(cand.Content.Parts[0].Text)
	out = strings.Trim(out, `"'`)
	return trimSnippet(out, 400)
}

// gatherRepoHints builds a compact context block for the AI classification prompt.
func (a *LogAnalyzer) gatherRepoHints(ctx context.Context, lines []string) string {
	if a.DB == nil || len(lines) == 0 {
		return ""
	}
	svc := &search.Service{DB: a.DB, AI: a.AI}
	joined := strings.Join(lines, "\n")
	if len(joined) > 600 {
		joined = joined[:600]
	}
	path, snip, hit := a.searchCodeContext(ctx, svc, joined)
	if !hit {
		return ""
	}
	return fmt.Sprintf("Likely related source: %s\nSnippet:\n%s", path, snip)
}

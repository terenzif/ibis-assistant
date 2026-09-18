package logs

import (
	"regexp"
	"testing"
)

func TestTemplateToFlexibleRegexMatchesPrefixedLine(t *testing.T) {
	tmpl := `2026-09-18 <VAR> ERROR Database timeout for user <VAR>`
	pat, err := templateToFlexibleRegex(tmpl)
	if err != nil {
		t.Fatalf("templateToFlexibleRegex: %v", err)
	}
	re, err := safeCompileLogAlign(pat)
	if err != nil {
		t.Fatalf("safeCompileLogAlign: %v", err)
	}

	line := `2026-09-18 07:50:01 ERROR Database timeout for user alice`
	if !re.MatchString(line) {
		t.Fatalf("expected match; pat=%q line=%q", pat, line)
	}

	tmpl2 := `[ERROR] Failed to connect to SurrealDB: <VAR>`
	pat2, err := templateToFlexibleRegex(tmpl2)
	if err != nil {
		t.Fatal(err)
	}
	re2, err := safeCompileLogAlign(pat2)
	if err != nil {
		t.Fatal(err)
	}
	prefixed := `2026/09/18 07:50:01 main.go:315: [ERROR] Failed to connect to SurrealDB: dial timeout`
	if !re2.MatchString(prefixed) {
		t.Fatalf("expected prefixed match; pat=%q", pat2)
	}
}

func TestLooksDangerousRegex(t *testing.T) {
	if !looksDangerousRegex("(.*)+foo") {
		t.Fatal("expected dangerous")
	}
	if looksDangerousRegex(`Failed to connect: .*?`) {
		t.Fatal("expected safe")
	}
}

func TestSafeCompileRejectsEmpty(t *testing.T) {
	if _, err := safeCompileLogAlign(""); err == nil {
		t.Fatal("expected error")
	}
}

func TestIsUsefulStaticPatternRejectsWeak(t *testing.T) {
	if isUsefulStaticPattern("w", "(.*?)") {
		t.Fatal("single-char format must be rejected")
	}
	if !isUsefulStaticPattern(`"CRITICAL: Failed to connect to SurrealDB: %v"`, `CRITICAL: Failed to connect to SurrealDB: (.*?)`) {
		t.Fatal("real format must be accepted")
	}
}

func TestPromoteSkipsWhenAlreadyCovered(t *testing.T) {
	re := regexp.MustCompile(`(?s)Database timeout`)
	a := &LogAnalyzer{
		staticPatterns: []LogTemplatePattern{{
			ID: "log_template:x", Regex: re, FormatStr: "Database timeout",
		}},
	}
	n := a.PromoteAIErrorsToLogAlign(nil, []SemanticError{{
		Category:   "DB_TIMEOUT",
		Template:   `ERROR Database timeout for <VAR>`,
		StackTrace: `ERROR Database timeout for alice`,
	}})
	if n != 0 {
		t.Fatalf("expected 0 promotions when already covered, got %d", n)
	}
}

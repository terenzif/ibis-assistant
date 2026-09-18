package code

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNeedsRuleSynthesisUniversal(t *testing.T) {
	if !NeedsRuleSynthesis("pages/x.aspx", nil, nil, []byte(strings.Repeat("a", 80))) {
		t.Fatal("aspx with empty AST should need synthesis")
	}
	if !NeedsRuleSynthesis("App.vue", nil, nil, []byte(strings.Repeat("b", 80))) {
		t.Fatal("vue with empty AST should need synthesis")
	}
	if !NeedsRuleSynthesis("legacy.jsp", nil, nil, []byte(strings.Repeat("c", 80))) {
		t.Fatal("jsp with empty AST should need synthesis")
	}
	if !NeedsRuleSynthesis("weird.dsl", nil, nil, []byte(strings.Repeat("d", 80))) {
		t.Fatal("unknown ext should need synthesis")
	}
	if NeedsRuleSynthesis("main.go", []ASTChunk{{Content: "func"}}, nil, []byte("package main")) {
		t.Fatal("go with chunks should not need synthesis")
	}
}

func TestSuggestHostLanguage(t *testing.T) {
	if SuggestHostLanguage("a.vue") != "html" {
		t.Fatal("vue → html")
	}
	if SuggestHostLanguage("a.go") != "go" {
		t.Fatal("go → go")
	}
}

func TestEnsureLanguageGlobIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sgconfig.yml"), []byte("ruleDirs:\n  - rules\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "rules"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IBIS_SG_ROOT", root)
	if err := EnsureLanguageGlob(".foobar", "html"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLanguageGlob(".foobar", "html"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "sgconfig.yml"))
	if !strings.Contains(string(b), "*.foobar") {
		t.Fatalf("glob missing: %s", b)
	}
	if strings.Count(string(b), "*.foobar") != 1 {
		t.Fatalf("glob duplicated: %s", b)
	}
}

func TestPersistGeneratedChunkRule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sgconfig.yml"), []byte("ruleDirs:\n  - rules\nlanguageGlobs:\n  html:\n    - \"*.vue\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "rules"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IBIS_SG_ROOT", root)
	t.Setenv("IBIS_AST_RULE_STRUCTURAL_ONLY", "1")

	yaml := `id: ai-generated-vue-script-chunk
language: html
rule:
  any:
    - kind: script_element
`
	res, err := PersistValidatedRule(context.Background(), yaml, "ai-generated-vue-script-chunk", "html", "", RuleKindChunk)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.Validated || !strings.Contains(res.Path, "ai-generated-") {
		t.Fatalf("unexpected: %+v", res)
	}
	if !strings.HasSuffix(res.RuleID, "-chunk") {
		t.Fatalf("id=%s", res.RuleID)
	}
}

func TestCanonicalizeGeneratedRuleID(t *testing.T) {
	id := canonicalizeGeneratedRuleID("ai-vue-chunk", RuleKindChunk)
	if id != "ai-generated-vue-chunk" {
		t.Fatalf("got %s", id)
	}
}

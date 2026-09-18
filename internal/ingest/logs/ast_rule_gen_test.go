package logs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAIASTRuleJSON(t *testing.T) {
	raw := "```json\n{\"language\":\"go\",\"id\":\"ai-go-zap-logs\",\"yaml\":\"id: ai-go-zap-logs\\nlanguage: go\\nrule:\\n  any:\\n    - pattern: $L.Infow($FORMAT, $$$A)\\n\",\"sample_code\":\"x\",\"rationale\":\"zap\"}\n```"
	resp, err := parseAIASTRuleJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Language != "go" || resp.ID != "ai-go-zap-logs" {
		t.Fatalf("unexpected parse: %+v", resp)
	}
	if !strings.Contains(resp.YAML, "$FORMAT") {
		t.Fatal("yaml missing FORMAT")
	}
}

func TestStructuralValidateASTRule(t *testing.T) {
	okYAML := `id: ai-go-zap-logs
language: go
rule:
  any:
    - pattern: $LOGGER.Infow($FORMAT, $$$ARGS)
    - pattern: $LOGGER.Errorw($FORMAT, $$$ARGS)
`
	id, err := structuralValidateASTRule(okYAML, "", "go")
	if err != nil {
		t.Fatal(err)
	}
	if id != "ai-go-zap-logs" {
		t.Fatalf("id=%s", id)
	}

	if _, err := structuralValidateASTRule(`id: bad
language: go
rule:
  any:
    - pattern: $LOGGER.Info($MSG)
`, "bad", "go"); err == nil {
		t.Fatal("expected missing $FORMAT error")
	}

	if _, err := structuralValidateASTRule(`id: ai-go-zap
language: go
rule:
  any:
    - pattern: $LOGGER.Infow($FORMAT)
`, "ai-go-zap", "go"); err == nil {
		t.Fatal("expected -logs suffix error")
	}
}

func TestInferLanguageFromErrors(t *testing.T) {
	lang := inferLanguageFromErrors([]SemanticError{
		{File: "internal/foo.go"},
		{File: "pkg/bar.go"},
		{File: "web/app.ts"},
	})
	if lang != "go" {
		t.Fatalf("expected go, got %s", lang)
	}
	if inferLanguageFromErrors([]SemanticError{{File: ""}, {Template: "x"}}) != "" {
		t.Fatal("expected empty language without source paths")
	}
}

func TestShouldAttemptASTRuleGenOnce(t *testing.T) {
	a := &LogAnalyzer{astRuleGenUsed: true}
	if a.shouldAttemptASTRuleGen([]SemanticError{{File: "x.go", Template: "err"}}) {
		t.Fatal("expected false when already used")
	}
}

func TestPersistValidatedASTRuleStructuralOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sgconfig.yml"), []byte("ruleDirs:\n  - rules\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "rules"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IBIS_SG_ROOT", root)
	t.Setenv("IBIS_AST_RULE_STRUCTURAL_ONLY", "1")

	yaml := `id: ai-go-zap-logs
language: go
rule:
  any:
    - pattern: $LOGGER.Infow($FORMAT, $$$ARGS)
    - pattern: $LOGGER.Errorw($FORMAT, $$$ARGS)
`
	res, err := PersistValidatedASTRule(context.Background(), yaml, "ai-go-zap-logs", "go", "")
	if err != nil {
		t.Fatalf("PersistValidatedASTRule: %v", err)
	}
	if res == nil || !res.Validated || res.Path == "" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.Path, "ai-generated-") && !strings.Contains(res.Path, "ai-go-zap") {
		t.Fatalf("path=%s", res.Path)
	}
	b, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "$FORMAT") {
		t.Fatalf("written rule missing FORMAT: %s", b)
	}
}

func TestQuarantineOnBadRule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sgconfig.yml"), []byte("ruleDirs:\n  - rules\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IBIS_SG_ROOT", root)
	t.Setenv("IBIS_AST_RULE_STRUCTURAL_ONLY", "1")

	res, err := PersistValidatedASTRule(context.Background(), "id: nope\nlanguage: go\n", "nope", "go", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if res == nil || res.Quarantine == "" {
		t.Fatalf("expected quarantine path, got %+v", res)
	}
	if _, statErr := os.Stat(res.Quarantine); statErr != nil {
		t.Fatalf("quarantine missing: %v", statErr)
	}
}

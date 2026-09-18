package code

import (
	"regexp"
	"strings"
	"testing"
)

func TestParseFormatToRegexFlexibleAnchors(t *testing.T) {
	pat := parseFormatToRegex(`"Starting Ibis Assistant (Mode: %s)..."`)
	if pat == "" {
		t.Fatal("empty pattern")
	}
	if strings.HasPrefix(pat, "^") || strings.HasSuffix(pat, "$") {
		t.Fatalf("expected no line anchors, got %q", pat)
	}
	re, err := regexp.Compile("(?s)" + pat)
	if err != nil {
		t.Fatal(err)
	}
	runtime := `2026/09/18 07:50:01 main.go:260: [INFO] Starting Ibis Assistant (Mode: sse)...`
	if !re.MatchString(runtime) {
		t.Fatalf("expected substring match; pat=%q", pat)
	}
}

func TestParseFormatToRegexEmpty(t *testing.T) {
	if parseFormatToRegex(`""`) != "" {
		t.Fatal("expected empty")
	}
}

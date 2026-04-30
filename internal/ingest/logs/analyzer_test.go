package logs

import (
	"strings"
	"testing"
)

func TestAddKnownPattern(t *testing.T) {
	analyzer := &LogAnalyzer{
		knownPatterns: make([]Pattern, 0),
	}

	category := "DB_TIMEOUT"
	template := `2026-04-30 10:00:00 ERROR Database timeout
at db.Connect()
user <VAR> failed to login from IP <VAR>`

	err := analyzer.AddKnownPattern(category, template)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(analyzer.knownPatterns) != 1 {
		t.Fatalf("Expected 1 pattern, got %d", len(analyzer.knownPatterns))
	}

	regex := analyzer.knownPatterns[0].Regex
	if regex == nil {
		t.Fatalf("Expected regex for %s, got nil", category)
	}

	// Test if the regex matches variations of the template
	testMatch := `2026-04-30 10:00:00 ERROR Database timeout
at db.Connect()
user john_doe failed to login from IP 192.168.1.1`

	if !regex.MatchString(testMatch) {
		t.Errorf("Expected regex to match string with variables replaced, but it did not")
	}

	// Test if it handles multiple lines properly (using the (?s) modifier)
	testMatchMultilineVar := `2026-04-30 10:00:00 ERROR Database timeout
at db.Connect()
user admin
superuser failed to login from IP 10.0.0.1`
	
	if !regex.MatchString(testMatchMultilineVar) {
		t.Errorf("Expected regex to match string with multiline variables replaced, but it did not")
	}
}

func TestFilterKnownErrors(t *testing.T) {
	analyzer := &LogAnalyzer{
		knownPatterns: make([]Pattern, 0),
	}

	analyzer.AddKnownPattern("DB_TIMEOUT", `ERROR Database timeout
at db.Connect()
user <VAR>`)

	analyzer.AddKnownPattern("NULL_PTR", `NullReferenceException
at main.go:10`)

	logBatch := `INFO System starting up...
ERROR Database timeout
at db.Connect()
user alice
INFO System running
NullReferenceException
at main.go:10
INFO Request finished`

	filtered := analyzer.FilterKnownErrors(logBatch)

	if strings.Contains(filtered, "ERROR Database timeout") {
		t.Errorf("Expected DB_TIMEOUT error block to be filtered out")
	}
	if strings.Contains(filtered, "user alice") {
		t.Errorf("Expected DB_TIMEOUT variable parts to be filtered out")
	}
	if strings.Contains(filtered, "NullReferenceException") {
		t.Errorf("Expected NULL_PTR error block to be filtered out")
	}
	
	if !strings.Contains(filtered, "[KNOWN_ERROR: DB_TIMEOUT]") {
		t.Errorf("Expected marker [KNOWN_ERROR: DB_TIMEOUT] to be injected")
	}
	if !strings.Contains(filtered, "[KNOWN_ERROR: NULL_PTR]") {
		t.Errorf("Expected marker [KNOWN_ERROR: NULL_PTR] to be injected")
	}

	expectedRemaining := []string{
		"INFO System starting up...",
		"INFO System running",
		"INFO Request finished",
	}

	for _, expected := range expectedRemaining {
		if !strings.Contains(filtered, expected) {
			t.Errorf("Expected filtered text to retain: %s", expected)
		}
	}
}

func TestFilterKnownErrorsAllMatched(t *testing.T) {
	analyzer := &LogAnalyzer{
		knownPatterns: make([]Pattern, 0),
	}

	analyzer.AddKnownPattern("SPAM_ERROR", `<VAR> SPAM`)

	logBatch := `10:00 SPAM
10:01 SPAM
10:02 SPAM`

	filtered := analyzer.FilterKnownErrors(logBatch)
	
	// Filter should inject markers instead of leaving newlines
	if !strings.Contains(filtered, "[KNOWN_ERROR: SPAM_ERROR]") {
		t.Errorf("Expected markers to be injected, got: %q", filtered)
	}
}

func TestLexerParserOrdering(t *testing.T) {
	analyzer := &LogAnalyzer{
		knownPatterns: make([]Pattern, 0),
	}

	// Add base pattern (Lexer token)
	analyzer.AddKnownPattern("TOKEN_A", `Error A`)

	// Add composite pattern (Parser rule)
	analyzer.AddKnownPattern("CASCADE_C", `[KNOWN_ERROR: TOKEN_A]
Consequence B`)

	logBatch := `Error A
Consequence B`

	filtered := analyzer.FilterKnownErrors(logBatch)
	
	if strings.Contains(filtered, "[KNOWN_ERROR: TOKEN_A]") {
		t.Errorf("Expected TOKEN_A marker to be subsumed by CASCADE_C marker")
	}
	if !strings.Contains(filtered, "[KNOWN_ERROR: CASCADE_C]") {
		t.Errorf("Expected CASCADE_C marker to be present, got: %q", filtered)
	}
}

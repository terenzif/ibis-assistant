package logs

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/terenzif/ibis-server/internal/ai"
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

	testMatch := `2026-04-30 10:00:00 ERROR Database timeout
at db.Connect()
user john_doe failed to login from IP 192.168.1.1`

	if !regex.MatchString(testMatch) {
		t.Errorf("Expected regex to match string with variables replaced, but it did not")
	}

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

	if !strings.Contains(filtered, "[KNOWN_ERROR: SPAM_ERROR]") {
		t.Errorf("Expected markers to be injected, got: %q", filtered)
	}
}

func TestLexerParserOrdering(t *testing.T) {
	analyzer := &LogAnalyzer{
		knownPatterns: make([]Pattern, 0),
	}

	analyzer.AddKnownPattern("TOKEN_A", `Error A`)

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

func TestPrefilterNoiseLines(t *testing.T) {
	lines := []string{
		"2026-09-18 INFO request ok",
		"2026-09-18 DEBUG cache hit",
		"2026-09-18 ERROR connection refused",
		"2026-09-18 INFO failed with exception in handler",
		"at internal/db/client.go:42",
		"2026-09-18 WARN disk almost full",
	}
	kept, dropped := PrefilterNoiseLines(lines)
	if dropped != 2 {
		t.Fatalf("expected 2 dropped noise lines, got %d kept=%v", dropped, kept)
	}
	joined := strings.Join(kept, "\n")
	if !strings.Contains(joined, "ERROR connection refused") {
		t.Error("ERROR line must be kept")
	}
	if !strings.Contains(joined, "exception") {
		t.Error("INFO+exception must be kept")
	}
	if !strings.Contains(joined, "at internal/db/client.go:42") {
		t.Error("stack frame without level must be kept")
	}
	if !strings.Contains(joined, "WARN") {
		t.Error("WARN must be kept")
	}
	if strings.Contains(joined, "cache hit") || strings.Contains(joined, "request ok") {
		t.Errorf("pure INFO/DEBUG should be dropped, got: %v", kept)
	}
}

func TestIsKnownMarkersOnly(t *testing.T) {
	if !isKnownMarkersOnly("[KNOWN_ERROR: A]\n[KNOWN_ERROR: B]\n  \n") {
		t.Error("expected markers-only text to match")
	}
	if isKnownMarkersOnly("[KNOWN_ERROR: A]\nINFO still here\n") {
		t.Error("expected non-marker residue to fail")
	}
}

func TestEarlyExitKnownOnlySkipsAI(t *testing.T) {
	var calls atomic.Int32
	client := newTestAIClient(&countingReasoning{calls: &calls})

	analyzer := &LogAnalyzer{
		Project:       "test",
		AI:            client,
		knownPatterns: make([]Pattern, 0),
	}
	if err := analyzer.AddKnownPattern("CONN_REFUSED", `ERROR connection refused host=<VAR>`); err != nil {
		t.Fatal(err)
	}

	lines := []string{
		"ERROR connection refused host=db1",
		"ERROR connection refused host=db2",
		"ERROR connection refused host=db3",
	}
	out, err := analyzer.ProcessBatchSync(context.Background(), lines)
	if err != nil {
		t.Fatalf("ProcessBatchSync: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("expected 0 AI GenerateContent calls, got %d", calls.Load())
	}
	if len(out) == 0 {
		t.Fatal("expected aggregated known-error results")
	}
	found := false
	for _, e := range out {
		if e.Category == "CONN_REFUSED" && strings.Contains(e.Cause, "AI skipped") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected CONN_REFUSED aggregate with AI skipped, got %#v", out)
	}
}

func TestProcessBatchSyncChunksAndReusesPatterns(t *testing.T) {
	var calls atomic.Int32
	client := newTestAIClient(&countingReasoning{
		calls: &calls,
		reply: `[{"category":"REPEATED_ERR","stack_trace":"ERROR boom id=1","file":"","line":0,"symbol":"","cause":"test","severity":7,"template":"ERROR boom id=<VAR>","supersedes":[]}]`,
	})

	lines := make([]string, 0, 600)
	for i := 0; i < 600; i++ {
		lines = append(lines, fmt.Sprintf("ERROR boom id=%d", i))
	}

	analyzer := &LogAnalyzer{
		Project:       "chunk-test",
		AI:            client,
		knownPatterns: make([]Pattern, 0),
	}
	out, err := analyzer.ProcessBatchSync(context.Background(), lines)
	if err != nil {
		t.Fatalf("ProcessBatchSync chunked: %v", err)
	}
	n := calls.Load()
	if n != 1 {
		t.Fatalf("expected exactly 1 AI call (first chunk learns, rest early-exit), got %d; results=%d", n, len(out))
	}
	if len(analyzer.knownPatterns) == 0 {
		t.Fatal("expected known pattern accumulated after first chunk")
	}
}

func TestReadCapped(t *testing.T) {
	data := strings.Repeat("x", 100)
	got, trunc, err := ReadCapped(strings.NewReader(data), 50)
	if err != nil {
		t.Fatal(err)
	}
	if !trunc || len(got) != 50 {
		t.Fatalf("expected trunc=true len=50, got trunc=%v len=%d", trunc, len(got))
	}
	got, trunc, err = ReadCapped(strings.NewReader("abc"), 50)
	if err != nil || trunc || string(got) != "abc" {
		t.Fatalf("small read failed: trunc=%v got=%q err=%v", trunc, got, err)
	}
}

type stubEmbedding struct{}

func (s *stubEmbedding) Name() string       { return "stub-embed" }
func (s *stubEmbedding) IsFunctional() bool { return true }
func (s *stubEmbedding) Stop()              {}
func (s *stubEmbedding) EmbedText(ctx context.Context, text string) ([]float32, error) {
	return []float32{0.1}, nil
}
func (s *stubEmbedding) BatchEmbedText(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0.1}
	}
	return out, nil
}

type countingReasoning struct {
	calls *atomic.Int32
	reply string
}

func (c *countingReasoning) Name() string       { return "counting-reason" }
func (c *countingReasoning) IsFunctional() bool { return true }
func (c *countingReasoning) Stop()              {}
func (c *countingReasoning) GenerateContent(ctx context.Context, contents []ai.Content, config ai.GenerationConfig) (ai.Candidate, error) {
	c.calls.Add(1)
	reply := c.reply
	if reply == "" {
		reply = `[]`
	}
	return ai.Candidate{Content: ai.Content{Parts: []ai.Part{{Text: reply}}}}, nil
}

func newTestAIClient(r ai.ReasoningProvider) *ai.Client {
	return ai.NewClient(&stubEmbedding{}, r, nil)
}

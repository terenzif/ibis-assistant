package logs

import "testing"

func TestExtractStackLocationGoParen(t *testing.T) {
	text := `at github.com/terenzif/ibis-server/internal/db.(*Client).Query (client.go:142)
at github.com/terenzif/ibis-server/internal/search.(*Engine).Ask (search.go:88)`
	file, line, _ := extractStackLocation(text)
	if file != "client.go" {
		t.Fatalf("file=%q want client.go", file)
	}
	if line != 142 {
		t.Fatalf("line=%d want 142", line)
	}
}

func TestExtractStackLocationJava(t *testing.T) {
	text := `java.lang.NullPointerException
	at com.example.Service.run(Service.java:55)`
	file, line, sym := extractStackLocation(text)
	if file != "Service.java" || line != 55 {
		t.Fatalf("got %s:%d want Service.java:55", file, line)
	}
	if sym != "run" {
		t.Fatalf("symbol=%q want run", sym)
	}
}

func TestDistinctiveTermsSkipsNoise(t *testing.T) {
	terms := distinctiveTerms("ERROR failed connection refused database host", 4)
	joined := ""
	for _, term := range terms {
		joined += " " + term
		if term == "error" || term == "failed" {
			t.Fatalf("unexpected stop word %q in %v", term, terms)
		}
	}
	if len(terms) == 0 {
		t.Fatal("expected some terms")
	}
	if !containsAny(terms, "connection", "refused", "database") {
		t.Fatalf("terms=%v missing expected keywords", terms)
	}
}

func containsAny(hay []string, needles ...string) bool {
	set := map[string]bool{}
	for _, h := range hay {
		set[h] = true
	}
	for _, n := range needles {
		if set[n] {
			return true
		}
	}
	return false
}

func TestEnrichmentQueryUsesCategory(t *testing.T) {
	q := enrichmentQuery(SemanticError{Category: "DB timeout", StackTrace: "time=1 level=ERROR msg=\"boom\""})
	if q == "" {
		t.Fatal("empty query")
	}
}

package ticketing

import "testing"

func TestParseProviderFieldsJSON(t *testing.T) {
	fields, err := ParseProviderFieldsJSON(`{"status_id":3,"custom":"x"}`)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if fields["custom"] != "x" {
		t.Fatalf("expected custom field x, got %v", fields["custom"])
	}
}

func TestParseProviderFieldsJSONEmpty(t *testing.T) {
	fields, err := ParseProviderFieldsJSON("")
	if err != nil {
		t.Fatalf("parse empty failed: %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("expected empty map, got %d", len(fields))
	}
}

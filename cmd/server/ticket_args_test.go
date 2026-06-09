package main

import "testing"

func TestBuildTicketSearchParamsFromArgs_Valid(t *testing.T) {
	args := map[string]interface{}{
		"provider":    "jira",
		"query":       "timeout",
		"project_key": "core",
		"status":      "open",
		"limit":       float64(20),
		"offset":      float64(10),
		"sort":        "updated DESC",
	}

	params, err := buildTicketSearchParamsFromArgs(args)
	if err != nil {
		t.Fatalf("expected valid args, got error: %v", err)
	}
	if params.Provider != "jira" {
		t.Errorf("expected provider jira, got %s", params.Provider)
	}
	if params.Query != "timeout" {
		t.Errorf("expected query timeout, got %s", params.Query)
	}
	if params.Limit != 20 {
		t.Errorf("expected limit 20, got %d", params.Limit)
	}
	if params.Offset != 10 {
		t.Errorf("expected offset 10, got %d", params.Offset)
	}
}

func TestBuildTicketSearchParamsFromArgs_DefaultLimit(t *testing.T) {
	params, err := buildTicketSearchParamsFromArgs(map[string]interface{}{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if params.Limit != 20 {
		t.Fatalf("expected default limit 20, got %d", params.Limit)
	}
}

func TestGetIntArg_StringValue(t *testing.T) {
	args := map[string]interface{}{"limit": "15"}
	limit, err := getIntArg(args, "limit")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if limit != 15 {
		t.Fatalf("expected 15, got %d", limit)
	}
}

func TestGetStringSliceArg_CommaSeparated(t *testing.T) {
	args := map[string]interface{}{"ticket_ids": "#1,ABC-2, AB#3"}
	values := getStringSliceArg(args, "ticket_ids")
	if len(values) != 3 {
		t.Fatalf("expected 3 values, got %d", len(values))
	}
}

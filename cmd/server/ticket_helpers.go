package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/terenzif/ibis-assistant/internal/ticketing"
)

type ticketSearchToolResponse struct {
	Summary    string                 `json:"summary"`
	Issues     []ticketing.Ticket     `json:"issues"`
	TotalCount int                    `json:"total_count"`
	Offset     int                    `json:"offset"`
	Limit      int                    `json:"limit"`
	Compact    []string               `json:"compact"`
	Filters    ticketing.SearchParams `json:"filters"`
}

func buildTicketSearchParamsFromArgs(args map[string]interface{}) (ticketing.SearchParams, error) {
	limit, err := getIntArg(args, "limit")
	if err != nil {
		return ticketing.SearchParams{}, err
	}
	offset, err := getIntArg(args, "offset")
	if err != nil {
		return ticketing.SearchParams{}, err
	}
	params := ticketing.SearchParams{
		Provider:    getStringArg(args, "provider"),
		Query:       getStringArg(args, "query"),
		ProjectKey:  getStringArg(args, "project_key"),
		Status:      getStringArg(args, "status"),
		Type:        getStringArg(args, "type"),
		Assignee:    getStringArg(args, "assignee"),
		Author:      getStringArg(args, "author"),
		Priority:    getStringArg(args, "priority"),
		UpdatedFrom: getStringArg(args, "updated_from"),
		UpdatedTo:   getStringArg(args, "updated_to"),
		Sort:        getStringArg(args, "sort"),
		Limit:       limit,
		Offset:      offset,
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}
	return params, nil
}

func formatTicketSearchResponse(result *ticketing.SearchResult, params ticketing.SearchParams, label string) (string, error) {
	compact := make([]string, 0, len(result.Issues))
	for _, issue := range result.Issues {
		compact = append(compact, fmt.Sprintf("[%s:%s] %s (%s)", issue.Provider, issue.ExternalKey, issue.Title, issue.Status))
	}
	payload := ticketSearchToolResponse{
		Summary:    label,
		Issues:     result.Issues,
		TotalCount: result.TotalCount,
		Offset:     result.Offset,
		Limit:      result.Limit,
		Compact:    compact,
		Filters:    params,
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func getBoolArg(args map[string]interface{}, key string) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		val = strings.TrimSpace(strings.ToLower(val))
		return val == "1" || val == "true" || val == "yes" || val == "on"
	default:
		return false
	}
}

func getStringSliceArg(args map[string]interface{}, key string) []string {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	out := []string{}
	switch val := v.(type) {
	case string:
		for _, part := range strings.Split(val, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	case []string:
		for _, item := range val {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
	case []interface{}:
		for _, item := range val {
			if s, ok := item.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

func deriveRepositoryFromOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}
	base := filepath.Base(origin)
	base = strings.TrimSuffix(base, ".git")
	return strings.TrimSpace(base)
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolContracts_TicketAndPRNamesPresent(t *testing.T) {
	path := filepath.Join("main.go")
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read main.go failed: %v", err)
	}
	content := string(contentBytes)

	required := []string{
		"ticket_get_capabilities",
		"ticket_search",
		"ticket_search_my",
		"ticket_get",
		"ticket_create",
		"ticket_update",
		"ticket_add_comment",
		"ticket_assign",
		"ticket_transition",
		"ticket_list_statuses",
		"ticket_search_users",
		"ticket_list_projects",
		"ticket_mark_resolved",
		"ticket_mark_closed",
		"ticket_reopen",
		"repo_pr_create",
		"repo_pr_complete",
	}

	for _, name := range required {
		if !strings.Contains(content, "\""+name+"\"") {
			t.Fatalf("missing MCP tool registration for %s", name)
		}
	}

	if strings.Contains(content, "\"redmine_search_issues\"") || strings.Contains(content, "\"redmine_update_issue\"") {
		t.Fatalf("legacy redmine_* tools should not be registered")
	}
}

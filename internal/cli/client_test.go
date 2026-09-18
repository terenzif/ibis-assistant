package cli

import (
	"testing"
)

func TestExecuteCLI_Subcommands(t *testing.T) {
	// Salva la funzione originale per ripristinarla alla fine
	origCallServerTool := callServerTool
	defer func() { callServerTool = origCallServerTool }()

	var lastPayload ToolCallPayload
	callServerTool = func(payload ToolCallPayload) {
		lastPayload = payload
	}

	tests := []struct {
		name         string
		args         []string
		expectedTool string
		expectedArgs map[string]interface{}
	}{
		{
			name:         "ask subcommand",
			args:         []string{"ask", "--branch", "main", "How to query DB?"},
			expectedTool: "ask_project",
			expectedArgs: map[string]interface{}{
				"query":            "How to query DB?",
				"branch_or_commit": "main",
			},
		},
		{
			name:         "ingest code subcommand",
			args:         []string{"ingest", "code", "--path", "/src"},
			expectedTool: "ingest_code",
			expectedArgs: map[string]interface{}{
				"path": "/src",
			},
		},
		{
			name:         "ingest git subcommand",
			args:         []string{"ingest", "git", "--name", "MyProj", "--url", "http://github.com", "--branch", "develop", "--commit", "abc"},
			expectedTool: "init_project",
			expectedArgs: map[string]interface{}{
				"project_name": "MyProj",
				"origin_url":   "http://github.com",
				"branch":       "develop",
				"commit":       "abc",
			},
		},
		{
			name:         "ticket search subcommand",
			args:         []string{"ticket", "search", "--provider", "jira", "--query", "bug", "--project", "PRJ", "--status", "open", "--limit", "10"},
			expectedTool: "ticket_search",
			expectedArgs: map[string]interface{}{
				"provider":    "jira",
				"query":       "bug",
				"project_key": "PRJ",
				"status":      "open",
				"limit":       float64(10),
			},
		},
		{
			name:         "pr create subcommand",
			args:         []string{"pr", "create", "--provider", "azure_devops", "--source", "feat/x", "--target", "main", "--title", "Add X"},
			expectedTool: "repo_pr_create",
			expectedArgs: map[string]interface{}{
				"provider":      "azure_devops",
				"source_branch": "feat/x",
				"target_branch": "main",
				"title":         "Add X",
			},
		},
		{
			name:         "logs analyze subcommand",
			args:         []string{"logs", "analyze", "--project", "App", "--text", "Error occurred"},
			expectedTool: "analyze_logs",
			expectedArgs: map[string]interface{}{
				"project_name": "App",
				"log_text":     "Error occurred",
			},
		},
		{
			name:         "credentials add subcommand",
			args:         []string{"credentials", "add", "--target", "github.com", "--token", "secret"},
			expectedTool: "git_configure_credentials",
			expectedArgs: map[string]interface{}{
				"target":    "github.com",
				"token":     "secret",
				"provider":  "generic",
				"auth_type": "token",
			},
		},
		{
			name:         "memory add subcommand",
			args:         []string{"memory", "add", "--project", "App", "--text", "Use Go 1.27"},
			expectedTool: "provide_collaborative_memory",
			expectedArgs: map[string]interface{}{
				"project_name": "App",
				"memory_text":  "Use Go 1.27",
			},
		},
		{
			name:         "outcome add subcommand",
			args:         []string{"outcome", "add", "--project", "App", "--question", "Why?", "--outcome", "Because", "--sources", "doc1"},
			expectedTool: "save_reasoning_outcome",
			expectedArgs: map[string]interface{}{
				"project_name":   "App",
				"question":       "Why?",
				"outcome_text":   "Because",
				"useful_sources": "doc1",
			},
		},
		{
			name:         "optimize subcommand",
			args:         []string{"optimize", "--iterations", "5"},
			expectedTool: "optimize_knowledge",
			expectedArgs: map[string]interface{}{
				"iterations": float64(5),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lastPayload = ToolCallPayload{}
			ExecuteCLI(tt.args)

			if lastPayload.Name != tt.expectedTool {
				t.Errorf("expected tool %s, got %s", tt.expectedTool, lastPayload.Name)
			}

			for k, expectedVal := range tt.expectedArgs {
				gotVal, ok := lastPayload.Arguments[k]
				if !ok {
					t.Errorf("missing argument key %s", k)
					continue
				}
				if gotVal != expectedVal {
					t.Errorf("key %s: expected %v, got %v", k, expectedVal, gotVal)
				}
			}
		})
	}
}

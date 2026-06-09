package git

import (
	"reflect"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/ticketing"
)

func TestExtractIssueRefs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []IssueRef
	}{
		{
			name:     "Redmine hash reference",
			input:    "This fixes #123",
			expected: []IssueRef{{ID: "123", Key: "123", Confidence: 1.0}},
		},
		{
			name:     "Jira reference",
			input:    "Fixes ABC-456",
			expected: []IssueRef{{Provider: ticketing.ProviderJira, ID: "ABC-456", Key: "ABC-456", ProjectKey: "ABC", Confidence: 1.0}},
		},
		{
			name:     "Azure DevOps reference",
			input:    "Resolves AB#789",
			expected: []IssueRef{{Provider: ticketing.ProviderAzureDevOps, ID: "789", Key: "AB#789", ProjectKey: "AB", Confidence: 1.0}},
		},
		{
			name:  "Multiple references",
			input: "Fixes #123 and closes ABC-222",
			expected: []IssueRef{
				{Provider: ticketing.ProviderJira, ID: "ABC-222", Key: "ABC-222", ProjectKey: "ABC", Confidence: 1.0},
				{ID: "123", Key: "123", Confidence: 1.0},
			},
		},
		{
			name:     "No issue",
			input:    "chore: cleanup",
			expected: nil,
		},
		{
			name:     "Custom pattern with provider",
			input:    "refs TASK-77",
			expected: []IssueRef{{Provider: ticketing.ProviderJira, ID: "TASK-77", Key: "TASK-77", ProjectKey: "TASK", Confidence: 0.5}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			custom := []string{}
			if tt.name == "Custom pattern with provider" {
				custom = []string{"jira::(TASK-\\d+)"}
			}
			got := ExtractIssueRefs(tt.input, custom...)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("ExtractIssueRefs() = %#v, want %#v", got, tt.expected)
			}
		})
	}
}

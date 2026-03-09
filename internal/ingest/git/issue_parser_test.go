package git

import (
	"reflect"
	"testing"
)

func TestExtractIssueRefs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []IssueRef
	}{
		{
			name:  "Standard issue reference",
			input: "This fixes #123",
			expected: []IssueRef{
				{ID: "123", Confidence: 1.0},
			},
		},
		{
			name:  "Multiple issues",
			input: "Fixes #123 and closes #456",
			expected: []IssueRef{
				{ID: "123", Confidence: 1.0},
				{ID: "456", Confidence: 1.0},
			},
		},
		{
			name:  "Issue reference without keywords",
			input: "Mentioning #789",
			expected: []IssueRef{
				{ID: "789", Confidence: 0.5},
			},
		},
		{
			name:  "Issue reference with trailing punctuation",
			input: "See #101.",
			expected: []IssueRef{
				{ID: "101", Confidence: 0.5},
			},
		},
		{
			name:  "Issue reference in parentheses",
			input: "(ref #202)",
			expected: []IssueRef{
				{ID: "202", Confidence: 0.5},
			},
		},
		{
			name:  "Issue reference with mixed characters",
			input: "#abc",
			expected: nil,
		},
		{
			name:  "Issue reference with leading characters",
			input: "Ticket#303",
			expected: []IssueRef{
				{ID: "303", Confidence: 0.5},
			},
		},
		{
			name:  "Empty input",
			input: "",
			expected: nil,
		},
		{
			name:  "Just hash",
			input: "#",
			expected: nil,
		},
		{
			name:  "Double hash",
			input: "##404",
			expected: []IssueRef{
				{ID: "404", Confidence: 0.5},
			},
		},
		{
			name:  "Hash then space then digits",
			input: "# 505",
			expected: nil,
		},
		{
			name:  "Duplicates",
			input: "Fixes #123. See #123.",
			expected: []IssueRef{
				{ID: "123", Confidence: 1.0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractIssueRefs(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("ExtractIssueRefs() = %v, want %v", got, tt.expected)
			}
		})
	}
}

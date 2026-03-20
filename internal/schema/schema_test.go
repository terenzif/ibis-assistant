package schema_test

import (
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/schema"
)

func TestSchemaConstants(t *testing.T) {
	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"Repo Table", schema.TableRepo, "repo"},
		{"Commit Table", schema.TableCommit, "commit"},
		{"Author Table", schema.TableAuthor, "author"},
		{"File Table", schema.TableFile, "source_file"},
		{"Issue Table", schema.TableIssue, "issue"},
		{"Project Table", schema.TableProject, "project"},
		{"LogFile Table", schema.TableLogFile, "log_file"},
		{"ErrorType Table", schema.TableErrorType, "error_type"},
		{"Edge Changed", schema.EdgeChanged, "changed"},
		{"Edge ParentOf", schema.EdgeParentOf, "parent_of"},
	}

	for _, tt := range tests {
		if tt.got != tt.expected {
			t.Errorf("%s: got %s, want %s", tt.name, tt.got, tt.expected)
		}
	}
}

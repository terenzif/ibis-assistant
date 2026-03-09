package git

import (
	"strings"
)

// IssueRef represents a reference to an issue within a text
type IssueRef struct {
	ID         string
	Confidence float64
}

// ExtractIssueRefs parses a commit message and returns a list of referenced issues
// with their confidence levels.
// Confidence: 1.0 if "Fixes #123", 0.5 if just "#123".
func ExtractIssueRefs(message string) []IssueRef {
	var refs []IssueRef
	seen := make(map[string]bool)
	msgLen := len(message)
	lowerSub := strings.ToLower(message)

	// Determine confidence based on global keywords (as per existing logic)
	// "Spec: 0.5 if just mentioned. 1.0 if 'Fixes'."
	// Current logic applies this to ALL issues found in the message if any keyword is present.
	confidence := 0.5
	if strings.Contains(lowerSub, "fix") || strings.Contains(lowerSub, "close") || strings.Contains(lowerSub, "resolve") {
		confidence = 1.0
	}

	for i := 0; i < msgLen; i++ {
		if message[i] == '#' {
			// Check if next chars are digits
			start := i + 1
			end := start
			for end < msgLen && message[end] >= '0' && message[end] <= '9' {
				end++
			}
			if end > start {
				// Found an issue ID
				issueIDStr := message[start:end]

				if !seen[issueIDStr] {
					refs = append(refs, IssueRef{
						ID:         issueIDStr,
						Confidence: confidence,
					})
					seen[issueIDStr] = true
				}

				// Advance i to the end of the number (minus 1 because loop does i++)
				// If we don't subtract 1, we skip the character at 'end'
				i = end - 1
			}
		}
	}
	return refs
}

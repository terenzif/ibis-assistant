package git

import (
	"regexp"
	"strings"

	"github.com/deckonline/knowledge_mcp/internal/ticketing"
)

var (
	jiraIssuePattern = regexp.MustCompile(`\b([A-Z][A-Z0-9]+-\d+)\b`)
	adoIssuePattern  = regexp.MustCompile(`\b([A-Z][A-Z0-9]+)#(\d+)\b`)
	hashIssuePattern = regexp.MustCompile(`#(\d+)`)
)

// IssueRef represents a ticket reference extracted from commit text.
type IssueRef struct {
	Provider   ticketing.ProviderName
	ID         string
	Key        string
	ProjectKey string
	Confidence float64
}

// ExtractIssueRefs parses a commit message and returns a list of referenced issues
// with confidence levels. Standard patterns supported:
// - Redmine generic: #123 (provider resolved by routing)
// - Jira: ABC-123
// - Azure DevOps: AB#123
// Custom patterns can be provided as:
// - "<regex>"
// - "<provider>::<regex>"
func ExtractIssueRefs(message string, customPatterns ...string) []IssueRef {
	var refs []IssueRef
	seen := make(map[string]bool)
	occupied := make([][2]int, 0)
	lowerSub := strings.ToLower(message)

	confidence := 0.5
	if strings.Contains(lowerSub, "fix") || strings.Contains(lowerSub, "close") || strings.Contains(lowerSub, "resolve") {
		confidence = 1.0
	}

	record := func(ref IssueRef, spanStart, spanEnd int) {
		if ref.Key == "" {
			ref.Key = ref.ID
		}
		if ref.ID == "" {
			ref.ID = ref.Key
		}
		key := string(ref.Provider) + "|" + ref.Key + "|" + ref.ID
		if ref.ID == "" || seen[key] {
			return
		}
		ref.Confidence = confidence
		refs = append(refs, ref)
		seen[key] = true
		if spanEnd > spanStart {
			occupied = append(occupied, [2]int{spanStart, spanEnd})
		}
	}

	for _, match := range adoIssuePattern.FindAllStringSubmatchIndex(message, -1) {
		if len(match) < 6 {
			continue
		}
		project := message[match[2]:match[3]]
		id := message[match[4]:match[5]]
		key := message[match[0]:match[1]]
		record(IssueRef{Provider: ticketing.ProviderAzureDevOps, ID: id, Key: key, ProjectKey: project}, match[0], match[1])
	}

	for _, match := range jiraIssuePattern.FindAllStringSubmatchIndex(message, -1) {
		if len(match) < 4 {
			continue
		}
		key := message[match[2]:match[3]]
		project := strings.SplitN(key, "-", 2)[0]
		record(IssueRef{Provider: ticketing.ProviderJira, ID: key, Key: key, ProjectKey: project}, match[0], match[1])
	}

	for _, match := range hashIssuePattern.FindAllStringSubmatchIndex(message, -1) {
		if len(match) < 4 {
			continue
		}
		if isOccupied(match[0], match[1], occupied) {
			continue
		}
		id := message[match[2]:match[3]]
		record(IssueRef{ID: id, Key: id}, match[0], match[1])
	}

	for _, raw := range customPatterns {
		provider, expr := parseCustomPattern(raw)
		if expr == "" {
			continue
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			continue
		}
		for _, match := range re.FindAllStringSubmatchIndex(message, -1) {
			if len(match) < 2 {
				continue
			}
			if isOccupied(match[0], match[1], occupied) {
				continue
			}
			value := strings.TrimSpace(message[match[0]:match[1]])
			if len(match) >= 4 {
				value = strings.TrimSpace(message[match[2]:match[3]])
			}
			record(IssueRef{Provider: provider, ID: value, Key: value}, match[0], match[1])
		}
	}

	return refs
}

func parseCustomPattern(raw string) (ticketing.ProviderName, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if strings.Contains(raw, "::") {
		parts := strings.SplitN(raw, "::", 2)
		provider := ticketing.ProviderName(strings.ToLower(strings.TrimSpace(parts[0])))
		return provider, strings.TrimSpace(parts[1])
	}
	return "", raw
}

func isOccupied(start, end int, ranges [][2]int) bool {
	for _, span := range ranges {
		if start < span[1] && end > span[0] {
			return true
		}
	}
	return false
}

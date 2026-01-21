package redmine

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewClient(url, key string) *Client {
	return &Client{
		BaseURL: url,
		APIKey:  key,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Redmine Structs (Simplified)
type IssuesResponse struct {
	Issues []Issue `json:"issues"`
}
type Issue struct {
	ID          int       `json:"id"`
	Subject     string    `json:"subject"`
	Description string    `json:"description"`
	Status      NamedObj  `json:"status"`
	Tracker     NamedObj  `json:"tracker"`
	Author      NamedObj  `json:"author"`
	CreatedOn   string    `json:"created_on"`
	UpdatedOn   string    `json:"updated_on"`
}
type NamedObj struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// IngestIssue fetches a single issue by ID and updates the graph
// This is called "On-Demand" when a commit references an issue.
func (c *Client) IngestIssue(dbClient *db.Client, issueIDStr string) error {
	log.Printf("Fetching Redmine Issue #%s...", issueIDStr)
	
	endpoint := fmt.Sprintf("%s/issues/%s.json?include=journals", c.BaseURL, issueIDStr)
	
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Redmine-API-Key", c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("redmine request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		log.Printf("Issue #%s not found in Redmine. Skipping.", issueIDStr)
		return nil
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("redmine error %d: %s", resp.StatusCode, string(body))
	}

	// Wrapper for single issue response
	type SingleIssueResponse struct {
		Issue Issue `json:"issue"`
	}

	var result SingleIssueResponse
	bodyBytes, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return err
	}
	
	issue := result.Issue

	issueID := fmt.Sprintf("%s:%d", schema.TableIssue, issue.ID)
	trackerID := fmt.Sprintf("%s:%d", schema.TableTracker, issue.Tracker.ID)
	authorID := fmt.Sprintf("%s:%s", schema.TableAuthor, sanitizeID(issue.Author.Name))

	// 1. Create/Update Tracker
	dbClient.Execute(fmt.Sprintf("UPDATE %s SET name = '%s';", trackerID, escapeSQL(issue.Tracker.Name)))

	// 2. Create/Update Author (Redmine user)
	dbClient.Execute(fmt.Sprintf("UPDATE %s SET name = '%s';", authorID, escapeSQL(issue.Author.Name)))

	// 3. Update Issue
	ql := fmt.Sprintf("UPDATE %s SET subject = '%s', description = '%s', status = '%s', updated_on = '%s';", 
		issueID, escapeSQL(issue.Subject), escapeSQL(issue.Description), escapeSQL(issue.Status.Name), issue.UpdatedOn)
	dbClient.Execute(ql)

	// 4. Link Issue -> Tracker
	dbClient.Execute(fmt.Sprintf("RELATE %s->%s->%s;", issueID, schema.EdgePartOf, trackerID))
	
	// 5. Link Issue -> Author (Reported By) -- Optional but good
	dbClient.Execute(fmt.Sprintf("RELATE %s->%s->%s;", authorID, schema.EdgeAuthored, issueID)) // Reusing 'authored' for 'reported' implies somewhat ok
	
	log.Printf("Successfully ingested Issue #%d", issue.ID)
	
	return nil
}


func sanitizeID(s string) string {
	safe := strings.ReplaceAll(s, " ", "_")
	safe = strings.ReplaceAll(safe, "'", "")
	return strings.ToLower(safe)
}

func escapeSQL(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

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

// IngestIssues fetches issues and updates the graph
func (c *Client) IngestIssues(dbClient *db.Client) error {
	log.Println("Starting Redmine ingestion...")
	
	// Fetch all open issues + recent closed?
	// For MVP, just fetch last 100 updated issues
	endpoint := fmt.Sprintf("%s/issues.json?limit=100&sort=updated_on:desc&status_id=*", c.BaseURL)
	
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

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("redmine error %d: %s", resp.StatusCode, string(body))
	}

	var result IssuesResponse
	// To be safe:
	bodyBytes, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return err
	}

	for _, issue := range result.Issues {
		issueID := fmt.Sprintf("%s:%d", schema.TableIssue, issue.ID)
		trackerID := fmt.Sprintf("%s:%d", schema.TableTracker, issue.Tracker.ID)
		authorID := fmt.Sprintf("%s:%s", schema.TableAuthor, sanitizeID(issue.Author.Name))

		// 1. Create/Update Tracker
		dbClient.Execute(fmt.Sprintf("UPDATE %s SET name = '%s';", trackerID, escapeSQL(issue.Tracker.Name)))

		// 2. Create/Update Author (Redmine user)
		dbClient.Execute(fmt.Sprintf("UPDATE %s SET name = '%s';", authorID, escapeSQL(issue.Author.Name)))

		// 3. Update Issue
		// We use CONTENT for safety with complex strings, or SET
		// escapeSQL is crucial here.
		ql := fmt.Sprintf("UPDATE %s SET subject = '%s', description = '%s', status = '%s', updated_on = '%s';", 
			issueID, escapeSQL(issue.Subject), escapeSQL(issue.Description), escapeSQL(issue.Status.Name), issue.UpdatedOn)
		dbClient.Execute(ql)

		// 4. Link Issue -> Tracker
		dbClient.Execute(fmt.Sprintf("RELATE %s->%s->%s;", issueID, schema.EdgePartOf, trackerID))
		
		fmt.Printf("Ingested Issue #%d\n", issue.ID)
	}
	
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

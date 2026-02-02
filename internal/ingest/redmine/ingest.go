package redmine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/deckonline/knowledge_mcp/internal/auth"
	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

type Ingester interface {
	IngestIssue(ctx context.Context, dbClient db.Executor, issueIDStr string) error
}

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// Ensure Client implements Ingester
var _ Ingester = (*Client)(nil)

func NewClient(url, key string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(url, "/"),
		APIKey:  key,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
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
func (c *Client) IngestIssue(ctx context.Context, dbClient db.Executor, issueIDStr string) error {
	logger.Info("Fetching Redmine Issue #%s...", issueIDStr)
	
	// Use provided context or Background
	if ctx == nil {
		ctx = context.Background()
	}

	issue, err := c.GetIssue(ctx, issueIDStr)
	if err != nil {
		return err
	}
	if issue == nil {
		return nil // Not found
	}

	issueID := fmt.Sprintf("%s:%d", schema.TableIssue, issue.ID)
	trackerID := fmt.Sprintf("%s:%d", schema.TableTracker, issue.Tracker.ID)
	authorID := fmt.Sprintf("%s:%s", schema.TableAuthor, db.SanitizeID(issue.Author.Name))

	// Combined Update Transaction
	// 1. Create/Update Tracker
	// 2. Create/Update Author
	// 3. Update Issue
	// 4. Link Issue -> Tracker
	// 5. Link Issue -> Author
	ql := fmt.Sprintf(`
		UPDATE %s SET name = $tracker_name;
		UPDATE %s SET name = $author_name;
		UPDATE %s SET subject = $subject, description = $description, status = $status, updated_on = $updated_on;
		RELATE %s->%s->%s;
		RELATE %s->%s->%s;
	`, trackerID, authorID, issueID, issueID, schema.EdgePartOf, trackerID, authorID, schema.EdgeAuthored, issueID)

	if _, err := dbClient.SmartQuery(ql, map[string]interface{}{
		"tracker_name": issue.Tracker.Name,
		"author_name":  issue.Author.Name,
		"subject":      issue.Subject,
		"description":  issue.Description,
		"status":       issue.Status.Name,
		"updated_on":   issue.UpdatedOn,
	}); err != nil {
		return fmt.Errorf("failed to ingest issue graph: %w", err)
	}
	
	logger.Info("Successfully ingested Issue #%d", issue.ID)
	
	return nil
}

// GetIssue fetches a raw Issue from Redmine
func (c *Client) GetIssue(ctx context.Context, id string) (*Issue, error) {
	endpoint := fmt.Sprintf("%s/issues/%s.json?include=journals", c.BaseURL, id)
	
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	
	// Determine Key: Context > Client Config
	apiKey := c.APIKey
	if k, ok := ctx.Value(auth.RedmineKeyContextKey).(string); ok && k != "" {
		apiKey = k
	}

	req.Header.Set("X-Redmine-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	logger.Debug("Redmine: GET %s", endpoint)
	start := time.Now()
	resp, err := c.HTTP.Do(req)
	duration := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("redmine request failed: %w", err)
	}
	defer resp.Body.Close()
	logger.Debug("Redmine: Received %d in %v", resp.StatusCode, duration)

	if resp.StatusCode == 404 {
		return nil, nil // Not found
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("redmine error %d: %s", resp.StatusCode, string(body))
	}

	type SingleIssueResponse struct {
		Issue Issue `json:"issue"`
	}

	var result SingleIssueResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	
	return &result.Issue, nil
}

// SearchIssues finds issues matching a query (subject contains)
func (c *Client) SearchIssues(ctx context.Context, query string) ([]Issue, error) {
	// Redmine API filtering: https://www.redmine.org/projects/redmine/wiki/Rest_Issues
	// Filtering by subject is not directly "search query" but we use `subject` filter if available or generic text search
	// Usually `f[]=subject&op[subject]=~&v[subject]=<query>` logic.
	// For simplicity, we just use standard listing.
	// Note: Generic search is often just `issues.json`.
	
	endpoint := fmt.Sprintf("%s/issues.json?subject=~%s&limit=10", c.BaseURL, query)
	
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	
	// Determine Key: Context > Client Config
	apiKey := c.APIKey
	if k, ok := ctx.Value(auth.RedmineKeyContextKey).(string); ok && k != "" {
		apiKey = k
	}

	req.Header.Set("X-Redmine-API-Key", apiKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("redmine search error %d", resp.StatusCode)
	}

	var result IssuesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	
	return result.Issues, nil
}

// UpdateIssue updates an issue (e.g. adding notes)
func (c *Client) UpdateIssue(ctx context.Context, id string, notes string) error {
	endpoint := fmt.Sprintf("%s/issues/%s.json", c.BaseURL, id)
	
	payload := map[string]interface{}{
		"issue": map[string]string{
			"notes": notes,
		},
	}
	
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "PUT", endpoint, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	
	// Determine Key: Context > Client Config
	apiKey := c.APIKey
	if k, ok := ctx.Value(auth.RedmineKeyContextKey).(string); ok && k != "" {
		apiKey = k
	}

	req.Header.Set("X-Redmine-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("redmine update error %d: %s", resp.StatusCode, string(respBody))
	}
	
	return nil
}



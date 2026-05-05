package redmine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

type SearchIssuesParams struct {
	Query        string
	ProjectID    string
	StatusID     string
	TrackerID    string
	AssignedToID string
	AuthorID     string
	PriorityID   string
	UpdatedFrom  string
	UpdatedTo    string
	Limit        int
	Offset       int
	Sort         string
}

type SearchIssuesResult struct {
	Issues     []Issue `json:"issues"`
	TotalCount int     `json:"total_count"`
	Offset     int     `json:"offset"`
	Limit      int     `json:"limit"`
}

type UpdateIssueParams struct {
	Notes          string
	StatusID       int
	PriorityID     int
	AssignedToID   int
	FixedVersionID int
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
	Issues     []Issue `json:"issues"`
	TotalCount int     `json:"total_count"`
	Offset     int     `json:"offset"`
	Limit      int     `json:"limit"`
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

	issueID := db.FormatRecordID(schema.TableIssue, fmt.Sprintf("%d", issue.ID))
	trackerID := db.FormatRecordID(schema.TableTracker, fmt.Sprintf("%d", issue.Tracker.ID))
	authorID := db.FormatRecordID(schema.TableAuthor, db.SanitizeID(issue.Author.Name))

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

	if _, err := dbClient.SmartQuery(ctx, ql, map[string]interface{}{
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

func ValidateSearchParams(params SearchIssuesParams) error {
	if params.Limit < 0 {
		return errors.New("limit must be >= 0")
	}
	if params.Limit > 100 {
		return errors.New("limit must be <= 100")
	}
	if params.Offset < 0 {
		return errors.New("offset must be >= 0")
	}

	allowedSort := map[string]bool{
		"":                true,
		"updated_on:desc": true,
		"updated_on:asc":  true,
		"priority:desc":   true,
		"priority:asc":    true,
		"id:desc":         true,
		"id:asc":          true,
		"created_on:desc": true,
		"created_on:asc":  true,
	}
	if !allowedSort[params.Sort] {
		return fmt.Errorf("invalid sort '%s'", params.Sort)
	}

	if params.UpdatedFrom != "" {
		if err := validateDate(params.UpdatedFrom); err != nil {
			return fmt.Errorf("updated_from: %w", err)
		}
	}
	if params.UpdatedTo != "" {
		if err := validateDate(params.UpdatedTo); err != nil {
			return fmt.Errorf("updated_to: %w", err)
		}
	}

	return nil
}

func validateDate(value string) error {
	if _, err := time.Parse("2006-01-02", value); err == nil {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return nil
	}
	return errors.New("must be YYYY-MM-DD or RFC3339")
}

// SearchIssues finds issues matching a query (subject contains)
func (c *Client) SearchIssues(ctx context.Context, query string) ([]Issue, error) {
	res, err := c.SearchIssuesAdvanced(ctx, SearchIssuesParams{
		Query: query,
		Limit: 10,
	})
	if err != nil {
		return nil, err
	}
	return res.Issues, nil
}

func (c *Client) SearchMyIssues(ctx context.Context, params SearchIssuesParams) (*SearchIssuesResult, error) {
	params.AssignedToID = "me"
	if params.Sort == "" {
		params.Sort = "updated_on:desc"
	}
	if params.Limit == 0 {
		params.Limit = 20
	}
	return c.SearchIssuesAdvanced(ctx, params)
}

func (c *Client) SearchIssuesAdvanced(ctx context.Context, params SearchIssuesParams) (*SearchIssuesResult, error) {
	if err := ValidateSearchParams(params); err != nil {
		return nil, err
	}

	if params.Limit == 0 {
		params.Limit = 20
	}

	v := url.Values{}
	if params.Query != "" {
		v.Set("subject", "~"+params.Query)
	}
	if params.ProjectID != "" {
		v.Set("project_id", params.ProjectID)
	}
	if params.StatusID != "" {
		v.Set("status_id", params.StatusID)
	}
	if params.TrackerID != "" {
		v.Set("tracker_id", params.TrackerID)
	}
	if params.AssignedToID != "" {
		v.Set("assigned_to_id", params.AssignedToID)
	}
	if params.AuthorID != "" {
		v.Set("author_id", params.AuthorID)
	}
	if params.PriorityID != "" {
		v.Set("priority_id", params.PriorityID)
	}
	if params.UpdatedFrom != "" {
		v.Set("updated_on", ">="+params.UpdatedFrom)
	}
	if params.UpdatedTo != "" {
		v.Set("updated_on_to", params.UpdatedTo)
	}
	v.Set("limit", fmt.Sprintf("%d", params.Limit))
	v.Set("offset", fmt.Sprintf("%d", params.Offset))
	if params.Sort != "" {
		v.Set("sort", params.Sort)
	}

	endpoint := fmt.Sprintf("%s/issues.json?%s", c.BaseURL, v.Encode())

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
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("redmine search error %d: %s", resp.StatusCode, string(body))
	}

	var result IssuesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &SearchIssuesResult{
		Issues:     result.Issues,
		TotalCount: result.TotalCount,
		Offset:     result.Offset,
		Limit:      result.Limit,
	}, nil
}

// UpdateIssue updates an issue (e.g. adding notes)
func (c *Client) UpdateIssue(ctx context.Context, id string, params UpdateIssueParams) error {
	endpoint := fmt.Sprintf("%s/issues/%s.json", c.BaseURL, id)

	issuePayload := map[string]interface{}{}
	if params.Notes != "" {
		issuePayload["notes"] = params.Notes
	}
	if params.StatusID > 0 {
		issuePayload["status_id"] = params.StatusID
	}
	if params.PriorityID > 0 {
		issuePayload["priority_id"] = params.PriorityID
	}
	if params.AssignedToID > 0 {
		issuePayload["assigned_to_id"] = params.AssignedToID
	}
	if params.FixedVersionID > 0 {
		issuePayload["fixed_version_id"] = params.FixedVersionID
	}

	if len(issuePayload) == 0 {
		return errors.New("no update fields provided")
	}

	payload := map[string]interface{}{
		"issue": issuePayload,
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



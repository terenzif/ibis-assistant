package redmine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/deckonline/knowledge_mcp/internal/auth"
	legacy "github.com/deckonline/knowledge_mcp/internal/ingest/redmine"
	"github.com/deckonline/knowledge_mcp/internal/ticketing"
)

type Provider struct {
	baseURL string
	apiKey  string
	http    *http.Client
	legacy  *legacy.Client
}

func New(cfg ticketing.RedmineConfig) *Provider {
	client := legacy.NewClient(cfg.BaseURL, cfg.APIKey)
	return &Provider{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		http:    client.HTTP,
		legacy:  client,
	}
}

func (p *Provider) Name() ticketing.ProviderName {
	return ticketing.ProviderRedmine
}

func (p *Provider) SearchIssues(ctx context.Context, auth ticketing.AuthContext, params ticketing.SearchParams) (*ticketing.SearchResult, error) {
	legacyParams := legacy.SearchIssuesParams{
		Query:        params.Query,
		ProjectID:    params.ProjectKey,
		StatusID:     params.Status,
		TrackerID:    params.Type,
		AssignedToID: params.Assignee,
		AuthorID:     params.Author,
		PriorityID:   params.Priority,
		UpdatedFrom:  params.UpdatedFrom,
		UpdatedTo:    params.UpdatedTo,
		Limit:        params.Limit,
		Offset:       params.Offset,
		Sort:         params.Sort,
	}
	if legacyParams.Limit == 0 {
		legacyParams.Limit = 20
	}
	if legacyParams.Sort == "" {
		legacyParams.Sort = "updated_on:desc"
	}

	ctx = withAuthKey(ctx, p.effectiveAPIKey(auth))
	result, err := p.legacy.SearchIssuesAdvanced(ctx, legacyParams)
	if err != nil {
		return nil, err
	}
	return &ticketing.SearchResult{
		Issues:     mapIssues(result.Issues),
		TotalCount: result.TotalCount,
		Offset:     result.Offset,
		Limit:      result.Limit,
	}, nil
}

func (p *Provider) SearchMyIssues(ctx context.Context, auth ticketing.AuthContext, params ticketing.SearchParams) (*ticketing.SearchResult, error) {
	legacyParams := legacy.SearchIssuesParams{
		Query:       params.Query,
		ProjectID:   params.ProjectKey,
		StatusID:    params.Status,
		TrackerID:   params.Type,
		PriorityID:  params.Priority,
		UpdatedFrom: params.UpdatedFrom,
		UpdatedTo:   params.UpdatedTo,
		Limit:       params.Limit,
		Offset:      params.Offset,
		Sort:        params.Sort,
	}
	if legacyParams.Limit == 0 {
		legacyParams.Limit = 20
	}
	if legacyParams.Sort == "" {
		legacyParams.Sort = "updated_on:desc"
	}

	ctx = withAuthKey(ctx, p.effectiveAPIKey(auth))
	result, err := p.legacy.SearchMyIssues(ctx, legacyParams)
	if err != nil {
		return nil, err
	}
	return &ticketing.SearchResult{
		Issues:     mapIssues(result.Issues),
		TotalCount: result.TotalCount,
		Offset:     result.Offset,
		Limit:      result.Limit,
	}, nil
}

func (p *Provider) GetIssue(ctx context.Context, auth ticketing.AuthContext, id string, projectKey string) (*ticketing.Ticket, error) {
	ctx = withAuthKey(ctx, p.effectiveAPIKey(auth))
	issue, err := p.legacy.GetIssue(ctx, id)
	if err != nil {
		return nil, err
	}
	if issue == nil {
		return nil, nil
	}
	t := mapIssue(*issue)
	t.ProjectKey = projectKey
	return &t, nil
}

func (p *Provider) CreateIssue(ctx context.Context, auth ticketing.AuthContext, params ticketing.CreateParams) (*ticketing.Ticket, error) {
	if strings.TrimSpace(params.Title) == "" {
		return nil, fmt.Errorf("title is required")
	}

	issue := map[string]interface{}{
		"subject":     params.Title,
		"description": params.Description,
	}
	if params.ProjectKey != "" {
		issue["project_id"] = params.ProjectKey
	}
	if params.Type != "" {
		if n, err := strconv.Atoi(params.Type); err == nil {
			issue["tracker_id"] = n
		} else {
			issue["tracker_id"] = params.Type
		}
	}
	if params.Assignee != "" {
		if n, err := strconv.Atoi(params.Assignee); err == nil {
			issue["assigned_to_id"] = n
		}
	}
	if params.Priority != "" {
		if n, err := strconv.Atoi(params.Priority); err == nil {
			issue["priority_id"] = n
		}
	}
	for k, v := range params.ProviderFields {
		issue[k] = v
	}

	payload := map[string]interface{}{"issue": issue}
	body, _ := json.Marshal(payload)
	endpoint := fmt.Sprintf("%s/issues.json", p.baseURL)
	resp, err := p.do(ctx, auth, http.MethodPost, endpoint, bytes.NewReader(body), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("redmine create error %d: %s", resp.StatusCode, string(raw))
	}

	var out struct {
		Issue struct {
			ID int `json:"id"`
		} `json:"issue"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Issue.ID == 0 {
		return &ticketing.Ticket{Provider: ticketing.ProviderRedmine, Title: params.Title, ProjectKey: params.ProjectKey}, nil
	}
	return p.GetIssue(ctx, auth, strconv.Itoa(out.Issue.ID), params.ProjectKey)
}

func (p *Provider) UpdateIssue(ctx context.Context, auth ticketing.AuthContext, id string, projectKey string, params ticketing.UpdateParams) (*ticketing.Ticket, error) {
	update := legacy.UpdateIssueParams{Notes: params.Notes}
	if params.Status != "" {
		if n, err := strconv.Atoi(params.Status); err == nil {
			update.StatusID = n
		}
	}
	if params.Priority != "" {
		if n, err := strconv.Atoi(params.Priority); err == nil {
			update.PriorityID = n
		}
	}
	if params.Assignee != "" {
		if n, err := strconv.Atoi(params.Assignee); err == nil {
			update.AssignedToID = n
		}
	}
	if params.FixedVersion != "" {
		if n, err := strconv.Atoi(params.FixedVersion); err == nil {
			update.FixedVersionID = n
		}
	}

	ctx = withAuthKey(ctx, p.effectiveAPIKey(auth))
	if err := p.legacy.UpdateIssue(ctx, id, update); err != nil {
		return nil, err
	}
	return p.GetIssue(ctx, auth, id, projectKey)
}

func (p *Provider) AddComment(ctx context.Context, auth ticketing.AuthContext, id string, projectKey string, comment string) error {
	_, err := p.UpdateIssue(ctx, auth, id, projectKey, ticketing.UpdateParams{Notes: comment})
	return err
}

func (p *Provider) AssignIssue(ctx context.Context, auth ticketing.AuthContext, id string, projectKey string, assignee string) error {
	_, err := p.UpdateIssue(ctx, auth, id, projectKey, ticketing.UpdateParams{Assignee: assignee})
	return err
}

func (p *Provider) TransitionIssue(ctx context.Context, auth ticketing.AuthContext, id string, projectKey string, transition string) error {
	_, err := p.UpdateIssue(ctx, auth, id, projectKey, ticketing.UpdateParams{Status: transition})
	return err
}

func (p *Provider) ListStatuses(ctx context.Context, auth ticketing.AuthContext, projectKey string, issueType string) ([]ticketing.Status, error) {
	endpoint := fmt.Sprintf("%s/issue_statuses.json", p.baseURL)
	resp, err := p.do(ctx, auth, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("redmine list statuses error %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		IssueStatuses []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"issue_statuses"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	statuses := make([]ticketing.Status, 0, len(out.IssueStatuses))
	for _, s := range out.IssueStatuses {
		statuses = append(statuses, ticketing.Status{ID: strconv.Itoa(s.ID), Name: s.Name})
	}
	return statuses, nil
}

func (p *Provider) SearchUsers(ctx context.Context, auth ticketing.AuthContext, query string, limit int) ([]ticketing.User, error) {
	ctx = withAuthKey(ctx, p.effectiveAPIKey(auth))
	res, err := p.legacy.GetUsers(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	users := make([]ticketing.User, 0, len(res.Users))
	for _, u := range res.Users {
		users = append(users, ticketing.User{
			ID:    strconv.Itoa(u.ID),
			Name:  strings.TrimSpace(u.Firstname + " " + u.Lastname),
			Email: u.Mail,
		})
	}
	return users, nil
}

func (p *Provider) ListProjects(ctx context.Context, auth ticketing.AuthContext) ([]ticketing.Project, error) {
	endpoint := fmt.Sprintf("%s/projects.json", p.baseURL)
	query := url.Values{}
	query.Set("limit", "100")
	resp, err := p.do(ctx, auth, http.MethodGet, endpoint+"?"+query.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("redmine list projects error %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Projects []struct {
			ID         int    `json:"id"`
			Identifier string `json:"identifier"`
			Name       string `json:"name"`
		} `json:"projects"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	projects := make([]ticketing.Project, 0, len(out.Projects))
	for _, prj := range out.Projects {
		projects = append(projects, ticketing.Project{ID: strconv.Itoa(prj.ID), Key: prj.Identifier, Name: prj.Name})
	}
	return projects, nil
}

func (p *Provider) do(ctx context.Context, auth ticketing.AuthContext, method, endpoint string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("X-Redmine-API-Key", p.effectiveAPIKey(auth))
	return p.http.Do(req)
}

func (p *Provider) effectiveAPIKey(auth ticketing.AuthContext) string {
	if strings.TrimSpace(auth.RedmineAPIKey) != "" {
		return strings.TrimSpace(auth.RedmineAPIKey)
	}
	return strings.TrimSpace(p.apiKey)
}

func withAuthKey(ctx context.Context, apiKey string) context.Context {
	if strings.TrimSpace(apiKey) == "" {
		return ctx
	}
	return context.WithValue(ctx, auth.RedmineKeyContextKey, apiKey)
}

func mapIssues(issues []legacy.Issue) []ticketing.Ticket {
	out := make([]ticketing.Ticket, 0, len(issues))
	for _, issue := range issues {
		out = append(out, mapIssue(issue))
	}
	return out
}

func mapIssue(issue legacy.Issue) ticketing.Ticket {
	externalID := strconv.Itoa(issue.ID)
	return ticketing.Ticket{
		Provider:    ticketing.ProviderRedmine,
		ID:          externalID,
		ExternalID:  externalID,
		ExternalKey: externalID,
		Title:       issue.Subject,
		Description: issue.Description,
		Status:      issue.Status.Name,
		Type:        issue.Tracker.Name,
		Author:      issue.Author.Name,
		CreatedOn:   issue.CreatedOn,
		UpdatedOn:   issue.UpdatedOn,
	}
}

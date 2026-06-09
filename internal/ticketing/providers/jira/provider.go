package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/deckonline/knowledge_mcp/internal/ticketing"
)

type Provider struct {
	baseURL string
	email   string
	token   string
	http    *http.Client
}

func New(cfg ticketing.JiraConfig) *Provider {
	return &Provider{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		email:   strings.TrimSpace(cfg.Email),
		token:   strings.TrimSpace(cfg.APIToken),
		http:    &http.Client{},
	}
}

func (p *Provider) Name() ticketing.ProviderName {
	return ticketing.ProviderJira
}

func (p *Provider) SearchIssues(ctx context.Context, auth ticketing.AuthContext, params ticketing.SearchParams) (*ticketing.SearchResult, error) {
	jql := buildJQL(params, false)
	if params.Limit == 0 {
		params.Limit = 20
	}
	if params.Sort == "" {
		params.Sort = "updated DESC"
	}
	body := map[string]interface{}{
		"jql":        jql,
		"maxResults": params.Limit,
		"startAt":    params.Offset,
		"fields":     []string{"summary", "description", "status", "issuetype", "assignee", "project", "created", "updated"},
	}
	resp, err := p.doJSON(ctx, auth, http.MethodPost, p.baseURL+"/rest/api/3/search", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jira search error %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Total  int `json:"total"`
		StartAt int `json:"startAt"`
		MaxResults int `json:"maxResults"`
		Issues []jiraIssue `json:"issues"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	issues := make([]ticketing.Ticket, 0, len(out.Issues))
	for _, issue := range out.Issues {
		issues = append(issues, mapIssue(issue))
	}
	return &ticketing.SearchResult{Issues: issues, TotalCount: out.Total, Offset: out.StartAt, Limit: out.MaxResults}, nil
}

func (p *Provider) SearchMyIssues(ctx context.Context, auth ticketing.AuthContext, params ticketing.SearchParams) (*ticketing.SearchResult, error) {
	return p.SearchIssues(ctx, auth, withMyIssues(params))
}

func (p *Provider) GetIssue(ctx context.Context, auth ticketing.AuthContext, id string, projectKey string) (*ticketing.Ticket, error) {
	endpoint := fmt.Sprintf("%s/rest/api/3/issue/%s", p.baseURL, url.PathEscape(id))
	resp, err := p.do(ctx, auth, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jira get issue error %d: %s", resp.StatusCode, string(raw))
	}
	var issue jiraIssue
	if err := json.NewDecoder(resp.Body).Decode(&issue); err != nil {
		return nil, err
	}
	t := mapIssue(issue)
	if t.ProjectKey == "" {
		t.ProjectKey = projectKey
	}
	return &t, nil
}

func (p *Provider) CreateIssue(ctx context.Context, auth ticketing.AuthContext, params ticketing.CreateParams) (*ticketing.Ticket, error) {
	if strings.TrimSpace(params.Title) == "" {
		return nil, fmt.Errorf("title is required")
	}
	issueType := params.Type
	if issueType == "" {
		issueType = "Task"
	}
	fields := map[string]interface{}{
		"project": map[string]string{"key": params.ProjectKey},
		"summary": params.Title,
		"issuetype": map[string]string{"name": issueType},
	}
	if params.Description != "" {
		fields["description"] = toADF(params.Description)
	}
	if params.Assignee != "" {
		fields["assignee"] = map[string]string{"accountId": params.Assignee}
	}
	for k, v := range params.ProviderFields {
		fields[k] = v
	}

	resp, err := p.doJSON(ctx, auth, http.MethodPost, p.baseURL+"/rest/api/3/issue", map[string]interface{}{"fields": fields})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jira create issue error %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Key == "" {
		out.Key = out.ID
	}
	return p.GetIssue(ctx, auth, out.Key, params.ProjectKey)
}

func (p *Provider) UpdateIssue(ctx context.Context, auth ticketing.AuthContext, id string, projectKey string, params ticketing.UpdateParams) (*ticketing.Ticket, error) {
	fields := map[string]interface{}{}
	if params.Status != "" {
		if err := p.TransitionIssue(ctx, auth, id, projectKey, params.Status); err != nil {
			return nil, err
		}
	}
	if params.Assignee != "" {
		fields["assignee"] = map[string]string{"accountId": params.Assignee}
	}
	if params.Type != "" {
		fields["issuetype"] = map[string]string{"name": params.Type}
	}
	for k, v := range params.ProviderFields {
		fields[k] = v
	}
	if len(fields) > 0 {
		resp, err := p.doJSON(ctx, auth, http.MethodPut, fmt.Sprintf("%s/rest/api/3/issue/%s", p.baseURL, url.PathEscape(id)), map[string]interface{}{"fields": fields})
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			raw, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("jira update issue error %d: %s", resp.StatusCode, string(raw))
		}
	}
	if params.Notes != "" {
		if err := p.AddComment(ctx, auth, id, projectKey, params.Notes); err != nil {
			return nil, err
		}
	}
	return p.GetIssue(ctx, auth, id, projectKey)
}

func (p *Provider) AddComment(ctx context.Context, auth ticketing.AuthContext, id string, projectKey string, comment string) error {
	endpoint := fmt.Sprintf("%s/rest/api/3/issue/%s/comment", p.baseURL, url.PathEscape(id))
	resp, err := p.doJSON(ctx, auth, http.MethodPost, endpoint, map[string]interface{}{"body": toADF(comment)})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("jira add comment error %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

func (p *Provider) AssignIssue(ctx context.Context, auth ticketing.AuthContext, id string, projectKey string, assignee string) error {
	_, err := p.UpdateIssue(ctx, auth, id, projectKey, ticketing.UpdateParams{Assignee: assignee})
	return err
}

func (p *Provider) TransitionIssue(ctx context.Context, auth ticketing.AuthContext, id string, projectKey string, transition string) error {
	transitionID := strings.TrimSpace(transition)
	if _, err := strconv.Atoi(transitionID); err != nil {
		list, err := p.listTransitions(ctx, auth, id)
		if err != nil {
			return err
		}
		transitionID = ""
		for _, tr := range list {
			if strings.EqualFold(strings.TrimSpace(tr.Name), strings.TrimSpace(transition)) {
				transitionID = tr.ID
				break
			}
		}
		if transitionID == "" {
			return fmt.Errorf("jira transition '%s' not found", transition)
		}
	}
	endpoint := fmt.Sprintf("%s/rest/api/3/issue/%s/transitions", p.baseURL, url.PathEscape(id))
	payload := map[string]interface{}{"transition": map[string]string{"id": transitionID}}
	resp, err := p.doJSON(ctx, auth, http.MethodPost, endpoint, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("jira transition error %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

func (p *Provider) ListStatuses(ctx context.Context, auth ticketing.AuthContext, projectKey string, issueType string) ([]ticketing.Status, error) {
	resp, err := p.do(ctx, auth, http.MethodGet, p.baseURL+"/rest/api/3/status", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jira list statuses error %d: %s", resp.StatusCode, string(raw))
	}
	var out []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	statuses := make([]ticketing.Status, 0, len(out))
	for _, s := range out {
		statuses = append(statuses, ticketing.Status{ID: s.ID, Name: s.Name})
	}
	return statuses, nil
}

func (p *Provider) SearchUsers(ctx context.Context, auth ticketing.AuthContext, query string, limit int) ([]ticketing.User, error) {
	if limit <= 0 {
		limit = 20
	}
	v := url.Values{}
	v.Set("query", query)
	v.Set("maxResults", strconv.Itoa(limit))
	endpoint := fmt.Sprintf("%s/rest/api/3/user/search?%s", p.baseURL, v.Encode())
	resp, err := p.do(ctx, auth, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jira search users error %d: %s", resp.StatusCode, string(raw))
	}
	var out []struct {
		AccountID string `json:"accountId"`
		DisplayName string `json:"displayName"`
		Email string `json:"emailAddress"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	users := make([]ticketing.User, 0, len(out))
	for _, u := range out {
		users = append(users, ticketing.User{ID: u.AccountID, Name: u.DisplayName, Email: u.Email})
	}
	return users, nil
}

func (p *Provider) ListProjects(ctx context.Context, auth ticketing.AuthContext) ([]ticketing.Project, error) {
	endpoint := fmt.Sprintf("%s/rest/api/3/project/search", p.baseURL)
	resp, err := p.do(ctx, auth, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jira list projects error %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Values []struct {
			ID string `json:"id"`
			Key string `json:"key"`
			Name string `json:"name"`
		} `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	projects := make([]ticketing.Project, 0, len(out.Values))
	for _, p := range out.Values {
		projects = append(projects, ticketing.Project{ID: p.ID, Key: p.Key, Name: p.Name})
	}
	return projects, nil
}

func (p *Provider) listTransitions(ctx context.Context, auth ticketing.AuthContext, id string) ([]struct{ ID, Name string }, error) {
	endpoint := fmt.Sprintf("%s/rest/api/3/issue/%s/transitions", p.baseURL, url.PathEscape(id))
	resp, err := p.do(ctx, auth, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jira list transitions error %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Transitions []struct {
			ID string `json:"id"`
			Name string `json:"name"`
		} `json:"transitions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	transitions := make([]struct{ ID, Name string }, 0, len(out.Transitions))
	for _, tr := range out.Transitions {
		transitions = append(transitions, struct{ ID, Name string }{ID: tr.ID, Name: tr.Name})
	}
	return transitions, nil
}

func (p *Provider) doJSON(ctx context.Context, auth ticketing.AuthContext, method, endpoint string, payload interface{}) (*http.Response, error) {
	body, _ := json.Marshal(payload)
	return p.do(ctx, auth, method, endpoint, bytes.NewReader(body), "application/json")
}

func (p *Provider) do(ctx context.Context, auth ticketing.AuthContext, method, endpoint string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Authorization", "Basic "+p.basicToken(auth))
	req.Header.Set("Accept", "application/json")
	return p.http.Do(req)
}

func (p *Provider) basicToken(auth ticketing.AuthContext) string {
	email := strings.TrimSpace(auth.JiraEmail)
	token := strings.TrimSpace(auth.JiraAPIToken)
	if email == "" {
		email = p.email
	}
	if token == "" {
		token = p.token
	}
	raw := []byte(email + ":" + token)
	return base64.StdEncoding.EncodeToString(raw)
}

func withMyIssues(params ticketing.SearchParams) ticketing.SearchParams {
	params.Query = strings.TrimSpace(params.Query)
	if params.Sort == "" {
		params.Sort = "updated DESC"
	}
	// Handled by buildJQL using a marker in assignee.
	params.Assignee = "current_user"
	return params
}

type jiraIssue struct {
	ID string `json:"id"`
	Key string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
		Description interface{} `json:"description"`
		Status struct {
			Name string `json:"name"`
		} `json:"status"`
		IssueType struct {
			Name string `json:"name"`
		} `json:"issuetype"`
		Assignee struct {
			DisplayName string `json:"displayName"`
		} `json:"assignee"`
		Project struct {
			Key string `json:"key"`
		} `json:"project"`
		Created string `json:"created"`
		Updated string `json:"updated"`
	} `json:"fields"`
	Self string `json:"self"`
}

func mapIssue(issue jiraIssue) ticketing.Ticket {
	desc := ""
	if issue.Fields.Description != nil {
		if b, err := json.Marshal(issue.Fields.Description); err == nil {
			desc = string(b)
		}
	}
	return ticketing.Ticket{
		Provider: ticketing.ProviderJira,
		ID: issue.Key,
		ExternalID: issue.ID,
		ExternalKey: issue.Key,
		ProjectKey: issue.Fields.Project.Key,
		Title: issue.Fields.Summary,
		Description: desc,
		Status: issue.Fields.Status.Name,
		Type: issue.Fields.IssueType.Name,
		Assignee: issue.Fields.Assignee.DisplayName,
		URL: issue.Self,
		CreatedOn: issue.Fields.Created,
		UpdatedOn: issue.Fields.Updated,
	}
}

func buildJQL(params ticketing.SearchParams, includeCurrentUser bool) string {
	parts := []string{}
	if params.ProjectKey != "" {
		parts = append(parts, fmt.Sprintf(`project = "%s"`, escapeJQL(params.ProjectKey)))
	}
	if params.Query != "" {
		parts = append(parts, fmt.Sprintf(`text ~ "%s"`, escapeJQL(params.Query)))
	}
	if params.Status != "" {
		parts = append(parts, fmt.Sprintf(`status = "%s"`, escapeJQL(params.Status)))
	}
	if params.Type != "" {
		parts = append(parts, fmt.Sprintf(`issuetype = "%s"`, escapeJQL(params.Type)))
	}
	if params.Assignee != "" {
		if params.Assignee == "current_user" || includeCurrentUser {
			parts = append(parts, "assignee = currentUser()")
		} else {
			parts = append(parts, fmt.Sprintf(`assignee = "%s"`, escapeJQL(params.Assignee)))
		}
	}
	if params.UpdatedFrom != "" {
		parts = append(parts, fmt.Sprintf(`updated >= "%s"`, escapeJQL(params.UpdatedFrom)))
	}
	if params.UpdatedTo != "" {
		parts = append(parts, fmt.Sprintf(`updated <= "%s"`, escapeJQL(params.UpdatedTo)))
	}
	query := strings.Join(parts, " AND ")
	if strings.TrimSpace(query) == "" {
		query = "ORDER BY updated DESC"
	}
	if params.Sort != "" {
		query += " ORDER BY " + params.Sort
	}
	return query
}

func escapeJQL(value string) string {
	return strings.ReplaceAll(value, `"`, `\\"`)
}

func toADF(text string) map[string]interface{} {
	return map[string]interface{}{
		"type":    "doc",
		"version": 1,
		"content": []map[string]interface{}{
			{
				"type": "paragraph",
				"content": []map[string]string{{"type": "text", "text": text}},
			},
		},
	}
}

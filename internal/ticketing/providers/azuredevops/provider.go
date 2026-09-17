package azuredevops

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

	"github.com/terenzif/ibis-server/internal/ticketing"
)

type Provider struct {
	orgURL  string
	project string
	repo    string
	pat     string
	http    *http.Client
}

func New(cfg ticketing.AzureDevOpsConfig) *Provider {
	return &Provider{
		orgURL:  strings.TrimRight(cfg.OrganizationURL, "/"),
		project: strings.TrimSpace(cfg.Project),
		repo:    strings.TrimSpace(cfg.Repository),
		pat:     strings.TrimSpace(cfg.PAT),
		http:    &http.Client{},
	}
}

func (p *Provider) Name() ticketing.ProviderName {
	return ticketing.ProviderAzureDevOps
}

func (p *Provider) SearchIssues(ctx context.Context, auth ticketing.AuthContext, params ticketing.SearchParams) (*ticketing.SearchResult, error) {
	project := p.resolveProject(params.ProjectKey)
	if project == "" {
		return nil, fmt.Errorf("project key is required for azure_devops search")
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}
	query := p.buildWIQL(params, false)
	ids, err := p.queryIDs(ctx, auth, project, query)
	if err != nil {
		return nil, err
	}
	issues, err := p.getWorkItems(ctx, auth, ids)
	if err != nil {
		return nil, err
	}
	if len(issues) > params.Limit {
		issues = issues[:params.Limit]
	}
	return &ticketing.SearchResult{Issues: issues, TotalCount: len(issues), Offset: params.Offset, Limit: params.Limit}, nil
}

func (p *Provider) SearchMyIssues(ctx context.Context, auth ticketing.AuthContext, params ticketing.SearchParams) (*ticketing.SearchResult, error) {
	project := p.resolveProject(params.ProjectKey)
	if project == "" {
		return nil, fmt.Errorf("project key is required for azure_devops search")
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}
	query := p.buildWIQL(params, true)
	ids, err := p.queryIDs(ctx, auth, project, query)
	if err != nil {
		return nil, err
	}
	issues, err := p.getWorkItems(ctx, auth, ids)
	if err != nil {
		return nil, err
	}
	if len(issues) > params.Limit {
		issues = issues[:params.Limit]
	}
	return &ticketing.SearchResult{Issues: issues, TotalCount: len(issues), Offset: params.Offset, Limit: params.Limit}, nil
}

func (p *Provider) GetIssue(ctx context.Context, auth ticketing.AuthContext, id string, projectKey string) (*ticketing.Ticket, error) {
	id = normalizeWorkItemID(id)
	endpoint := fmt.Sprintf("%s/%s/_apis/wit/workitems/%s?api-version=7.1", p.orgURL, url.PathEscape(p.resolveProject(projectKey)), url.PathEscape(id))
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
		return nil, fmt.Errorf("azure devops get issue error %d: %s", resp.StatusCode, string(raw))
	}
	var item workItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, err
	}
	t := mapWorkItem(item)
	if t.ProjectKey == "" {
		t.ProjectKey = p.resolveProject(projectKey)
	}
	return &t, nil
}

func (p *Provider) CreateIssue(ctx context.Context, auth ticketing.AuthContext, params ticketing.CreateParams) (*ticketing.Ticket, error) {
	project := p.resolveProject(params.ProjectKey)
	if project == "" {
		return nil, fmt.Errorf("project key is required")
	}
	workItemType := params.Type
	if workItemType == "" {
		workItemType = "Task"
	}
	op := []map[string]string{{"op": "add", "path": "/fields/System.Title", "value": params.Title}}
	if params.Description != "" {
		op = append(op, map[string]string{"op": "add", "path": "/fields/System.Description", "value": params.Description})
	}
	if params.Assignee != "" {
		op = append(op, map[string]string{"op": "add", "path": "/fields/System.AssignedTo", "value": params.Assignee})
	}
	for k, v := range params.ProviderFields {
		op = append(op, map[string]string{"op": "add", "path": "/fields/" + k, "value": fmt.Sprintf("%v", v)})
	}

	endpoint := fmt.Sprintf("%s/%s/_apis/wit/workitems/$%s?api-version=7.1", p.orgURL, url.PathEscape(project), url.PathEscape(workItemType))
	resp, err := p.doJSON(ctx, auth, http.MethodPost, endpoint, op, "application/json-patch+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure devops create issue error %d: %s", resp.StatusCode, string(raw))
	}
	var item workItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, err
	}
	t := mapWorkItem(item)
	return &t, nil
}

func (p *Provider) UpdateIssue(ctx context.Context, auth ticketing.AuthContext, id string, projectKey string, params ticketing.UpdateParams) (*ticketing.Ticket, error) {
	id = normalizeWorkItemID(id)
	op := []map[string]string{}
	if params.Status != "" {
		op = append(op, map[string]string{"op": "add", "path": "/fields/System.State", "value": params.Status})
	}
	if params.Type != "" {
		op = append(op, map[string]string{"op": "add", "path": "/fields/System.WorkItemType", "value": params.Type})
	}
	if params.Assignee != "" {
		op = append(op, map[string]string{"op": "add", "path": "/fields/System.AssignedTo", "value": params.Assignee})
	}
	if params.Priority != "" {
		op = append(op, map[string]string{"op": "add", "path": "/fields/Microsoft.VSTS.Common.Priority", "value": params.Priority})
	}
	if params.Notes != "" {
		op = append(op, map[string]string{"op": "add", "path": "/fields/System.History", "value": params.Notes})
	}
	for k, v := range params.ProviderFields {
		op = append(op, map[string]string{"op": "add", "path": "/fields/" + k, "value": fmt.Sprintf("%v", v)})
	}
	if len(op) == 0 {
		return p.GetIssue(ctx, auth, id, projectKey)
	}
	endpoint := fmt.Sprintf("%s/%s/_apis/wit/workitems/%s?api-version=7.1", p.orgURL, url.PathEscape(p.resolveProject(projectKey)), url.PathEscape(id))
	resp, err := p.doJSON(ctx, auth, http.MethodPatch, endpoint, op, "application/json-patch+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure devops update issue error %d: %s", resp.StatusCode, string(raw))
	}
	var item workItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, err
	}
	t := mapWorkItem(item)
	return &t, nil
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
	project := p.resolveProject(projectKey)
	if issueType == "" {
		issueType = "Task"
	}
	endpoint := fmt.Sprintf("%s/%s/_apis/wit/workitemtypes/%s/states?api-version=7.1", p.orgURL, url.PathEscape(project), url.PathEscape(issueType))
	resp, err := p.do(ctx, auth, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure devops list statuses error %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Value []struct {
			Name string `json:"name"`
		} `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	statuses := make([]ticketing.Status, 0, len(out.Value))
	for idx, s := range out.Value {
		statuses = append(statuses, ticketing.Status{ID: strconv.Itoa(idx + 1), Name: s.Name})
	}
	return statuses, nil
}

func (p *Provider) SearchUsers(ctx context.Context, auth ticketing.AuthContext, query string, limit int) ([]ticketing.User, error) {
	if limit <= 0 {
		limit = 20
	}
	v := url.Values{}
	v.Set("searchFilter", "General")
	v.Set("filterValue", query)
	v.Set("queryMembership", "None")
	v.Set("api-version", "7.1")
	endpoint := fmt.Sprintf("%s/_apis/identities?%s", p.orgURL, v.Encode())
	resp, err := p.do(ctx, auth, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure devops search users error %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Value []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
			Mail        string `json:"mailAddress"`
		} `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	users := make([]ticketing.User, 0, len(out.Value))
	for idx, u := range out.Value {
		if idx >= limit {
			break
		}
		users = append(users, ticketing.User{ID: u.ID, Name: u.DisplayName, Email: u.Mail})
	}
	return users, nil
}

func (p *Provider) ListProjects(ctx context.Context, auth ticketing.AuthContext) ([]ticketing.Project, error) {
	endpoint := fmt.Sprintf("%s/_apis/projects?api-version=7.1", p.orgURL)
	resp, err := p.do(ctx, auth, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure devops list projects error %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Value []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	projects := make([]ticketing.Project, 0, len(out.Value))
	for _, p := range out.Value {
		projects = append(projects, ticketing.Project{ID: p.ID, Key: p.Name, Name: p.Name})
	}
	return projects, nil
}

func (p *Provider) queryIDs(ctx context.Context, auth ticketing.AuthContext, project string, wiql string) ([]int, error) {
	endpoint := fmt.Sprintf("%s/%s/_apis/wit/wiql?api-version=7.1", p.orgURL, url.PathEscape(project))
	resp, err := p.doJSON(ctx, auth, http.MethodPost, endpoint, map[string]string{"query": wiql}, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure devops wiql error %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		WorkItems []struct {
			ID int `json:"id"`
		} `json:"workItems"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(out.WorkItems))
	for _, item := range out.WorkItems {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

func (p *Provider) getWorkItems(ctx context.Context, auth ticketing.AuthContext, ids []int) ([]ticketing.Ticket, error) {
	if len(ids) == 0 {
		return []ticketing.Ticket{}, nil
	}
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, strconv.Itoa(id))
	}
	endpoint := fmt.Sprintf("%s/_apis/wit/workitems?ids=%s&api-version=7.1", p.orgURL, strings.Join(values, ","))
	resp, err := p.do(ctx, auth, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure devops get workitems error %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Value []workItem `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	issues := make([]ticketing.Ticket, 0, len(out.Value))
	for _, item := range out.Value {
		issues = append(issues, mapWorkItem(item))
	}
	return issues, nil
}

func (p *Provider) doJSON(ctx context.Context, auth ticketing.AuthContext, method, endpoint string, payload interface{}, contentType string) (*http.Response, error) {
	body, _ := json.Marshal(payload)
	return p.do(ctx, auth, method, endpoint, bytes.NewReader(body), contentType)
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
	pat := strings.TrimSpace(auth.AzureDevOpsPAT)
	if pat == "" {
		pat = p.pat
	}
	return base64.StdEncoding.EncodeToString([]byte(":" + pat))
}

func (p *Provider) resolveProject(projectKey string) string {
	if strings.TrimSpace(projectKey) != "" {
		return strings.TrimSpace(projectKey)
	}
	return p.project
}

func (p *Provider) buildWIQL(params ticketing.SearchParams, myOnly bool) string {
	project := p.resolveProject(params.ProjectKey)
	parts := []string{fmt.Sprintf("[System.TeamProject] = '%s'", strings.ReplaceAll(project, "'", "''"))}
	if params.Query != "" {
		parts = append(parts, fmt.Sprintf("[System.Title] CONTAINS '%s'", strings.ReplaceAll(params.Query, "'", "''")))
	}
	if params.Status != "" {
		parts = append(parts, fmt.Sprintf("[System.State] = '%s'", strings.ReplaceAll(params.Status, "'", "''")))
	}
	if params.Type != "" {
		parts = append(parts, fmt.Sprintf("[System.WorkItemType] = '%s'", strings.ReplaceAll(params.Type, "'", "''")))
	}
	if params.Assignee != "" && !myOnly {
		parts = append(parts, fmt.Sprintf("[System.AssignedTo] = '%s'", strings.ReplaceAll(params.Assignee, "'", "''")))
	}
	if myOnly {
		parts = append(parts, "[System.AssignedTo] = @Me")
	}
	return fmt.Sprintf("SELECT [System.Id] FROM WorkItems WHERE %s ORDER BY [System.ChangedDate] DESC", strings.Join(parts, " AND "))
}

type workItem struct {
	ID     int                    `json:"id"`
	URL    string                 `json:"url"`
	Fields map[string]interface{} `json:"fields"`
}

func mapWorkItem(item workItem) ticketing.Ticket {
	id := strconv.Itoa(item.ID)
	title := toString(item.Fields["System.Title"])
	desc := toString(item.Fields["System.Description"])
	status := toString(item.Fields["System.State"])
	issueType := toString(item.Fields["System.WorkItemType"])
	project := toString(item.Fields["System.TeamProject"])
	assignee := ""
	if raw, ok := item.Fields["System.AssignedTo"].(map[string]interface{}); ok {
		assignee = toString(raw["displayName"])
	}
	if assignee == "" {
		assignee = toString(item.Fields["System.AssignedTo"])
	}
	return ticketing.Ticket{
		Provider:    ticketing.ProviderAzureDevOps,
		ID:          id,
		ExternalID:  id,
		ExternalKey: id,
		ProjectKey:  project,
		Title:       title,
		Description: desc,
		Status:      status,
		Type:        issueType,
		Assignee:    assignee,
		URL:         item.URL,
		UpdatedOn:   toString(item.Fields["System.ChangedDate"]),
		CreatedOn:   toString(item.Fields["System.CreatedDate"]),
	}
}

func toString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return strconv.FormatInt(int64(val), 10)
	case int:
		return strconv.Itoa(val)
	default:
		if val == nil {
			return ""
		}
		return fmt.Sprintf("%v", val)
	}
}

func normalizeWorkItemID(id string) string {
	id = strings.TrimSpace(id)
	if strings.Contains(id, "#") {
		parts := strings.Split(id, "#")
		id = parts[len(parts)-1]
	}
	return id
}

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

	"github.com/terenzif/ibis-arc/internal/repopr"
)

type Config struct {
	OrganizationURL string
	Project         string
	Repository      string
	PAT             string
}

type Provider struct {
	orgURL  string
	project string
	repo    string
	pat     string
	http    *http.Client
}

func New(cfg Config) *Provider {
	return &Provider{
		orgURL:  strings.TrimRight(cfg.OrganizationURL, "/"),
		project: strings.TrimSpace(cfg.Project),
		repo:    strings.TrimSpace(cfg.Repository),
		pat:     strings.TrimSpace(cfg.PAT),
		http:    &http.Client{},
	}
}

func (p *Provider) Name() repopr.ProviderName {
	return repopr.ProviderAzureDevOps
}

func (p *Provider) CreatePR(ctx context.Context, auth repopr.AuthContext, params repopr.CreateParams) (*repopr.PullRequest, error) {
	project := p.resolveProject(params.ProjectName)
	repo := p.resolveRepo(params.Repository)
	if project == "" || repo == "" {
		return nil, fmt.Errorf("azure devops project and repository are required")
	}
	if strings.TrimSpace(params.SourceBranch) == "" {
		return nil, fmt.Errorf("source_branch is required")
	}
	source := ensureRef(params.SourceBranch)
	target := ensureRef(params.TargetBranch)
	payload := map[string]interface{}{
		"sourceRefName": source,
		"targetRefName": target,
		"title":         params.Title,
		"description":   appendTicketBlock(params.Description, params.TicketIDs),
	}
	if len(params.ReviewerIDs) > 0 {
		reviewers := make([]map[string]string, 0, len(params.ReviewerIDs))
		for _, reviewer := range params.ReviewerIDs {
			reviewer = strings.TrimSpace(reviewer)
			if reviewer == "" {
				continue
			}
			reviewers = append(reviewers, map[string]string{"id": reviewer})
		}
		if len(reviewers) > 0 {
			payload["reviewers"] = reviewers
		}
	}
	if params.AutoComplete {
		payload["completionOptions"] = map[string]interface{}{
			"deleteSourceBranch": false,
			"squashMerge":        false,
		}
	}

	endpoint := fmt.Sprintf("%s/%s/_apis/git/repositories/%s/pullrequests?api-version=7.1", p.orgURL, url.PathEscape(project), url.PathEscape(repo))
	resp, err := p.doJSON(ctx, auth, http.MethodPost, endpoint, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure devops create pr error %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		PullRequestID int    `json:"pullRequestId"`
		Title         string `json:"title"`
		Status        string `json:"status"`
		URL           string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &repopr.PullRequest{Provider: repopr.ProviderAzureDevOps, ID: strconv.Itoa(out.PullRequestID), Title: out.Title, Status: out.Status, URL: out.URL}, nil
}

func (p *Provider) CompletePR(ctx context.Context, auth repopr.AuthContext, params repopr.CompleteParams) (*repopr.PullRequest, error) {
	project := p.resolveProject(params.ProjectName)
	repo := p.resolveRepo(params.Repository)
	if project == "" || repo == "" {
		return nil, fmt.Errorf("azure devops project and repository are required")
	}
	if strings.TrimSpace(params.PRID) == "" {
		return nil, fmt.Errorf("pr_id is required")
	}
	payload := map[string]interface{}{
		"status": "completed",
		"completionOptions": map[string]interface{}{
			"deleteSourceBranch": params.DeleteSourceBranch,
			"squashMerge":        params.Squash,
		},
	}
	endpoint := fmt.Sprintf("%s/%s/_apis/git/repositories/%s/pullrequests/%s?api-version=7.1", p.orgURL, url.PathEscape(project), url.PathEscape(repo), url.PathEscape(params.PRID))
	resp, err := p.doJSON(ctx, auth, http.MethodPatch, endpoint, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure devops complete pr error %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		PullRequestID int    `json:"pullRequestId"`
		Title         string `json:"title"`
		Status        string `json:"status"`
		URL           string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &repopr.PullRequest{Provider: repopr.ProviderAzureDevOps, ID: strconv.Itoa(out.PullRequestID), Title: out.Title, Status: out.Status, URL: out.URL}, nil
}

func (p *Provider) resolveProject(project string) string {
	if strings.TrimSpace(project) != "" {
		return strings.TrimSpace(project)
	}
	return p.project
}

func (p *Provider) resolveRepo(repo string) string {
	if strings.TrimSpace(repo) != "" {
		return strings.TrimSpace(repo)
	}
	return p.repo
}

func ensureRef(branch string) string {
	branch = strings.TrimSpace(branch)
	if strings.HasPrefix(branch, "refs/") {
		return branch
	}
	return "refs/heads/" + branch
}

func appendTicketBlock(description string, ticketIDs []string) string {
	description = strings.TrimSpace(description)
	if len(ticketIDs) == 0 {
		return description
	}
	block := "Linked tickets: " + strings.Join(ticketIDs, ", ")
	if description == "" {
		return block
	}
	return description + "\n\n" + block
}

func (p *Provider) doJSON(ctx context.Context, auth repopr.AuthContext, method, endpoint string, payload interface{}) (*http.Response, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+p.basicToken(auth))
	return p.http.Do(req)
}

func (p *Provider) basicToken(auth repopr.AuthContext) string {
	pat := strings.TrimSpace(auth.AzureDevOpsPAT)
	if pat == "" {
		pat = p.pat
	}
	return base64.StdEncoding.EncodeToString([]byte(":" + pat))
}

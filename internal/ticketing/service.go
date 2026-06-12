package ticketing

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/terenzif/ibis-assistant/internal/auth"
	"github.com/terenzif/ibis-assistant/internal/db"
	"github.com/terenzif/ibis-assistant/internal/logger"
	"github.com/terenzif/ibis-assistant/internal/schema"
)

var digitRegex = regexp.MustCompile(`\d+`)

type Service struct {
	cfg       *Config
	providers map[ProviderName]TicketProvider
	db        db.Executor
}

func NewService(cfg *Config, dbClient ...db.Executor) *Service {
	if cfg == nil {
		cfg = defaultConfig()
	}
	cfg.normalize()
	var exec db.Executor
	if len(dbClient) > 0 {
		exec = dbClient[0]
	}
	return &Service{
		cfg:       cfg,
		providers: map[ProviderName]TicketProvider{},
		db:        exec,
	}
}

func (s *Service) RegisterProvider(p TicketProvider) {
	if p == nil {
		return
	}
	s.providers[p.Name()] = p
}

func (s *Service) Config() *Config {
	return s.cfg
}

func (s *Service) Capabilities() map[string]interface{} {
	providers := make([]string, 0, len(s.providers))
	for name := range s.providers {
		providers = append(providers, string(name))
	}
	return map[string]interface{}{
		"default_provider":     s.cfg.DefaultProvider,
		"project_provider_map": s.cfg.ProjectProviderMap,
		"providers":            providers,
		"header_hints":         s.cfg.HeaderHints,
		"client_env_hints":     s.cfg.ClientEnvHints,
	}
}

func (s *Service) ReferencePatternsForProject(projectKey string) []string {
	if patterns, ok := s.cfg.ReferencePatterns[projectKey]; ok {
		return patterns
	}
	if patterns, ok := s.cfg.ReferencePatterns["*"]; ok {
		return patterns
	}
	return nil
}

func (s *Service) resolveProvider(providerArg string, projectKey string) (ProviderName, TicketProvider, error) {
	if providerArg != "" {
		name := ProviderName(strings.ToLower(strings.TrimSpace(providerArg)))
		provider, ok := s.providers[name]
		if !ok {
			return name, nil, fmt.Errorf("provider '%s' is not registered", name)
		}
		return name, provider, nil
	}

	if mapped, ok := s.cfg.ProjectProviderMap[projectKey]; ok && mapped != "" {
		provider, ok := s.providers[mapped]
		if !ok {
			return mapped, nil, fmt.Errorf("provider '%s' is not registered", mapped)
		}
		return mapped, provider, nil
	}

	name := s.cfg.DefaultProvider
	provider, ok := s.providers[name]
	if !ok {
		return name, nil, fmt.Errorf("provider '%s' is not registered", name)
	}
	return name, provider, nil
}

func (s *Service) authFromContext(ctx context.Context) AuthContext {
	out := AuthContext{}
	if v, ok := ctx.Value(auth.RedmineKeyContextKey).(string); ok {
		out.RedmineAPIKey = strings.TrimSpace(v)
	}
	if v, ok := ctx.Value(auth.JiraEmailContextKey).(string); ok {
		out.JiraEmail = strings.TrimSpace(v)
	}
	if v, ok := ctx.Value(auth.JiraAPITokenContextKey).(string); ok {
		out.JiraAPIToken = strings.TrimSpace(v)
	}
	if v, ok := ctx.Value(auth.AzureDevOpsPATContextKey).(string); ok {
		out.AzureDevOpsPAT = strings.TrimSpace(v)
	}
	return out
}

func (s *Service) Search(ctx context.Context, params SearchParams) (*SearchResult, error) {
	providerName, provider, err := s.resolveProvider(params.Provider, params.ProjectKey)
	if err != nil {
		return nil, err
	}
	params.Provider = string(providerName)
	return provider.SearchIssues(ctx, s.authFromContext(ctx), params)
}

func (s *Service) SearchMy(ctx context.Context, params SearchParams) (*SearchResult, error) {
	providerName, provider, err := s.resolveProvider(params.Provider, params.ProjectKey)
	if err != nil {
		return nil, err
	}
	params.Provider = string(providerName)
	return provider.SearchMyIssues(ctx, s.authFromContext(ctx), params)
}

func (s *Service) Get(ctx context.Context, providerArg, id, projectKey string) (*Ticket, error) {
	_, provider, err := s.resolveProvider(providerArg, projectKey)
	if err != nil {
		return nil, err
	}
	return provider.GetIssue(ctx, s.authFromContext(ctx), id, projectKey)
}

func (s *Service) Create(ctx context.Context, params CreateParams) (*Ticket, error) {
	name, provider, err := s.resolveProvider(params.Provider, params.ProjectKey)
	if err != nil {
		return nil, err
	}
	if params.ProviderFields == nil {
		params.ProviderFields = map[string]interface{}{}
	}
	if params.ProviderFieldsJSON != "" {
		extra, err := ParseProviderFieldsJSON(params.ProviderFieldsJSON)
		if err != nil {
			return nil, err
		}
		for k, v := range extra {
			params.ProviderFields[k] = v
		}
	}
	if params.Type == "" {
		if byProject, ok := s.cfg.DefaultCreateType[string(name)]; ok {
			if t, ok := byProject[params.ProjectKey]; ok && t != "" {
				params.Type = t
			} else if t, ok := byProject["*"]; ok {
				params.Type = t
			}
		}
	}
	return provider.CreateIssue(ctx, s.authFromContext(ctx), params)
}

func (s *Service) Update(ctx context.Context, id, projectKey string, params UpdateParams) (*Ticket, error) {
	name, provider, err := s.resolveProvider(params.Provider, projectKey)
	if err != nil {
		return nil, err
	}
	if params.ProviderFields == nil {
		params.ProviderFields = map[string]interface{}{}
	}
	if params.ProviderFieldsJSON != "" {
		extra, err := ParseProviderFieldsJSON(params.ProviderFieldsJSON)
		if err != nil {
			return nil, err
		}
		for k, v := range extra {
			params.ProviderFields[k] = v
		}
	}

	if params.WorkflowAction != "" {
		target, err := s.resolveWorkflowTarget(ctx, name, s.authFromContext(ctx), params.WorkflowAction, projectKey, params.Type)
		if err != nil {
			return nil, err
		}
		if target != "" {
			if err := provider.TransitionIssue(ctx, s.authFromContext(ctx), id, projectKey, target); err != nil {
				return nil, err
			}
		}
	}

	return provider.UpdateIssue(ctx, s.authFromContext(ctx), id, projectKey, params)
}

func (s *Service) AddComment(ctx context.Context, providerArg, id, projectKey, comment string) error {
	_, provider, err := s.resolveProvider(providerArg, projectKey)
	if err != nil {
		return err
	}
	return provider.AddComment(ctx, s.authFromContext(ctx), id, projectKey, comment)
}

func (s *Service) Assign(ctx context.Context, providerArg, id, projectKey, assignee string) error {
	_, provider, err := s.resolveProvider(providerArg, projectKey)
	if err != nil {
		return err
	}
	return provider.AssignIssue(ctx, s.authFromContext(ctx), id, projectKey, assignee)
}

func (s *Service) Transition(ctx context.Context, providerArg, id, projectKey, transition string) error {
	_, provider, err := s.resolveProvider(providerArg, projectKey)
	if err != nil {
		return err
	}
	return provider.TransitionIssue(ctx, s.authFromContext(ctx), id, projectKey, transition)
}

func (s *Service) ListStatuses(ctx context.Context, providerArg, projectKey, issueType string) ([]Status, error) {
	_, provider, err := s.resolveProvider(providerArg, projectKey)
	if err != nil {
		return nil, err
	}
	return provider.ListStatuses(ctx, s.authFromContext(ctx), projectKey, issueType)
}

func (s *Service) SearchUsers(ctx context.Context, providerArg, projectKey, query string, limit int) ([]User, error) {
	_, provider, err := s.resolveProvider(providerArg, projectKey)
	if err != nil {
		return nil, err
	}
	return provider.SearchUsers(ctx, s.authFromContext(ctx), query, limit)
}

func (s *Service) ListProjects(ctx context.Context, providerArg string) ([]Project, error) {
	_, provider, err := s.resolveProvider(providerArg, "")
	if err != nil {
		return nil, err
	}
	return provider.ListProjects(ctx, s.authFromContext(ctx))
}

func (s *Service) MarkResolved(ctx context.Context, providerArg, id, projectKey, issueType string) (*WorkflowActionResult, error) {
	return s.applyWorkflow(ctx, providerArg, id, projectKey, issueType, WorkflowResolve)
}

func (s *Service) MarkClosed(ctx context.Context, providerArg, id, projectKey, issueType string) (*WorkflowActionResult, error) {
	return s.applyWorkflow(ctx, providerArg, id, projectKey, issueType, WorkflowClose)
}

func (s *Service) Reopen(ctx context.Context, providerArg, id, projectKey, issueType string) (*WorkflowActionResult, error) {
	return s.applyWorkflow(ctx, providerArg, id, projectKey, issueType, WorkflowReopen)
}

func (s *Service) applyWorkflow(ctx context.Context, providerArg, id, projectKey, issueType, action string) (*WorkflowActionResult, error) {
	name, provider, err := s.resolveProvider(providerArg, projectKey)
	if err != nil {
		return nil, err
	}
	authCtx := s.authFromContext(ctx)
	target, err := s.resolveWorkflowTarget(ctx, name, authCtx, action, projectKey, issueType)
	if err != nil {
		return nil, err
	}
	if target == "" {
		return nil, fmt.Errorf("no workflow target for action '%s'", action)
	}
	if err := provider.TransitionIssue(ctx, authCtx, id, projectKey, target); err != nil {
		return nil, err
	}
	return &WorkflowActionResult{Provider: name, Action: action, Target: target, IssueID: id}, nil
}

func (s *Service) resolveWorkflowTarget(ctx context.Context, provider ProviderName, authCtx AuthContext, action, projectKey, issueType string) (string, error) {
	action = normalizeWorkflowAction(action)
	cfg, ok := s.cfg.Workflow.Actions[action]
	if ok {
		if byProject, ok := cfg.PerProvider[provider]; ok {
			if target, ok := byProject[projectKey]; ok && strings.TrimSpace(target) != "" {
				return strings.TrimSpace(target), nil
			}
			if target, ok := byProject["*"]; ok && strings.TrimSpace(target) != "" {
				return strings.TrimSpace(target), nil
			}
		}
	}

	p, ok := s.providers[provider]
	if !ok {
		return "", fmt.Errorf("provider '%s' is not registered", provider)
	}
	statuses, err := p.ListStatuses(ctx, authCtx, projectKey, issueType)
	if err != nil {
		return "", err
	}
	target := inferStatusID(action, statuses)
	if target == "" {
		return "", errors.New("unable to infer workflow status target")
	}
	return target, nil
}

func inferStatusID(action string, statuses []Status) string {
	action = normalizeWorkflowAction(action)
	for _, status := range statuses {
		name := strings.ToLower(strings.TrimSpace(status.Name))
		switch action {
		case WorkflowResolve:
			if name == "resolved" || name == "risolto" || name == "done" || name == "fixed" {
				return status.ID
			}
		case WorkflowClose:
			if name == "closed" || name == "chiuso" {
				return status.ID
			}
		case WorkflowReopen:
			if name == "reopened" || name == "open" || name == "riaperto" {
				return status.ID
			}
		}
	}
	return ""
}

func normalizeWorkflowAction(action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "resolved":
		return WorkflowResolve
	case "closed":
		return WorkflowClose
	case "reopened":
		return WorkflowReopen
	default:
		return action
	}
}

func (s *Service) ResolveReference(projectKey string, ref IssueReference) (IssueReference, error) {
	resolved := ref
	if strings.TrimSpace(resolved.ExternalKey) == "" {
		resolved.ExternalKey = strings.TrimSpace(resolved.ExternalID)
	}
	if strings.TrimSpace(resolved.ExternalID) == "" {
		resolved.ExternalID = extractDigits(resolved.ExternalKey)
		if resolved.ExternalID == "" {
			resolved.ExternalID = strings.TrimSpace(resolved.ExternalKey)
		}
	}
	if resolved.Provider == "" {
		providerName, _, err := s.resolveProvider("", projectKey)
		if err != nil {
			return resolved, err
		}
		resolved.Provider = providerName
	}
	if resolved.ProjectKey == "" {
		resolved.ProjectKey = projectKey
	}
	return resolved, nil
}

func (s *Service) IngestIssueReference(ctx context.Context, projectKey string, ref IssueReference) error {
	resolved, err := s.ResolveReference(projectKey, ref)
	if err != nil {
		return err
	}
	_, provider, err := s.resolveProvider(string(resolved.Provider), projectKey)
	if err != nil {
		return err
	}
	issue, err := provider.GetIssue(ctx, s.authFromContext(ctx), resolved.ExternalKey, resolved.ProjectKey)
	if err != nil {
		return err
	}
	if issue == nil {
		return nil
	}
	if issue.Provider == "" {
		issue.Provider = resolved.Provider
	}
	if issue.ExternalID == "" {
		issue.ExternalID = resolved.ExternalID
	}
	if issue.ExternalKey == "" {
		issue.ExternalKey = resolved.ExternalKey
	}
	if issue.ProjectKey == "" {
		issue.ProjectKey = resolved.ProjectKey
	}
	if err := s.upsertIssueGraph(ctx, issue); err != nil {
		return err
	}
	logger.Debug("Ingested ticket reference provider=%s id=%s", issue.Provider, issue.ExternalID)
	return nil
}

func (s *Service) upsertIssueGraph(ctx context.Context, issue *Ticket) error {
	if s.db == nil || issue == nil {
		return nil
	}

	externalKey := strings.TrimSpace(issue.ExternalKey)
	if externalKey == "" {
		externalKey = strings.TrimSpace(issue.ExternalID)
	}
	if externalKey == "" {
		externalKey = "unknown"
	}

	issueID := IssueRecordID(issue.Provider, externalKey)
	trackerID := ""
	if strings.TrimSpace(issue.Type) != "" {
		trackerID = db.FormatRecordID(schema.TableTracker, db.SanitizeID(fmt.Sprintf("%s_%s", issue.Provider, issue.Type)))
	}
	authorID := ""
	if strings.TrimSpace(issue.Author) != "" {
		authorID = db.FormatRecordID(schema.TableAuthor, db.SanitizeID(issue.Author))
	}

	var ql strings.Builder
	if trackerID != "" {
		ql.WriteString(fmt.Sprintf("UPDATE %s SET name = $tracker_name;\n", trackerID))
	}
	if authorID != "" {
		ql.WriteString(fmt.Sprintf("UPDATE %s SET name = $author_name;\n", authorID))
	}
	ql.WriteString(fmt.Sprintf("UPDATE %s SET provider = $provider, external_id = $external_id, external_key = $external_key, project_key = $project_key, subject = $subject, description = $description, status = $status, type = $type, updated_on = $updated_on, url = $url;\n", issueID))
	if trackerID != "" {
		ql.WriteString(fmt.Sprintf("RELATE %s->%s->%s;\n", issueID, schema.EdgePartOf, trackerID))
	}
	if authorID != "" {
		ql.WriteString(fmt.Sprintf("RELATE %s->%s->%s;\n", authorID, schema.EdgeAuthored, issueID))
	}

	_, err := s.db.SmartQuery(ctx, ql.String(), map[string]interface{}{
		"provider":     string(issue.Provider),
		"external_id":  issue.ExternalID,
		"external_key": externalKey,
		"project_key":  issue.ProjectKey,
		"subject":      issue.Title,
		"description":  issue.Description,
		"status":       issue.Status,
		"type":         issue.Type,
		"updated_on":   issue.UpdatedOn,
		"url":          issue.URL,
		"tracker_name": issue.Type,
		"author_name":  issue.Author,
	})
	return err
}

func extractDigits(input string) string {
	match := digitRegex.FindString(input)
	return strings.TrimSpace(match)
}

func IssueRecordID(provider ProviderName, externalKey string) string {
	key := strings.TrimSpace(externalKey)
	if key == "" {
		key = "unknown"
	}
	if provider == ProviderRedmine {
		return db.FormatRecordID(schema.TableIssue, db.SanitizeID(key))
	}
	return db.FormatRecordID(schema.TableIssue, db.SanitizeID(fmt.Sprintf("%s_%s", provider, key)))
}

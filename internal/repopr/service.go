package repopr

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/terenzif/ibis-assistant/internal/auth"
)

var (
	jiraPattern    = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b`)
	redminePattern = regexp.MustCompile(`#\d+`)
	adoPattern     = regexp.MustCompile(`\b[A-Z][A-Z0-9]+#\d+\b`)
)

type Service struct {
	cfg       Config
	providers map[ProviderName]Provider
}

func NewService(cfg Config) *Service {
	if cfg.DefaultProvider == "" {
		cfg.DefaultProvider = ProviderAzureDevOps
	}
	if cfg.ProjectProviderMap == nil {
		cfg.ProjectProviderMap = map[string]ProviderName{}
	}
	if cfg.ProjectTargetBranch == nil {
		cfg.ProjectTargetBranch = map[string]string{}
	}
	return &Service{cfg: cfg, providers: map[ProviderName]Provider{}}
}

func (s *Service) RegisterProvider(provider Provider) {
	if provider == nil {
		return
	}
	s.providers[provider.Name()] = provider
}

func (s *Service) resolveProvider(providerArg, projectName string) (ProviderName, Provider, error) {
	if strings.TrimSpace(providerArg) != "" {
		name := ProviderName(strings.ToLower(strings.TrimSpace(providerArg)))
		provider, ok := s.providers[name]
		if !ok {
			return name, nil, fmt.Errorf("repo provider '%s' not registered", name)
		}
		return name, provider, nil
	}
	if mapped, ok := s.cfg.ProjectProviderMap[projectName]; ok && mapped != "" {
		provider, ok := s.providers[mapped]
		if !ok {
			return mapped, nil, fmt.Errorf("repo provider '%s' not registered", mapped)
		}
		return mapped, provider, nil
	}
	provider, ok := s.providers[s.cfg.DefaultProvider]
	if !ok {
		return s.cfg.DefaultProvider, nil, fmt.Errorf("repo provider '%s' not registered", s.cfg.DefaultProvider)
	}
	return s.cfg.DefaultProvider, provider, nil
}

func (s *Service) CreatePR(ctx context.Context, params CreateParams) (*PullRequest, error) {
	name, provider, err := s.resolveProvider(params.Provider, params.ProjectName)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.TargetBranch) == "" {
		if target, ok := s.cfg.ProjectTargetBranch[params.ProjectName]; ok && strings.TrimSpace(target) != "" {
			params.TargetBranch = target
		} else if strings.TrimSpace(s.cfg.DefaultTargetBranch) != "" {
			params.TargetBranch = s.cfg.DefaultTargetBranch
		} else {
			params.TargetBranch = "main"
		}
	}
	if strings.TrimSpace(params.Title) == "" {
		params.Title = buildPRTitle(params.SourceBranch, params.TicketIDs)
	}
	if strings.TrimSpace(params.Description) == "" {
		params.Description = buildPRDescription(params.SourceBranch, params.TicketIDs)
	}

	allTickets := normalizeTicketIDs(params.TicketIDs)
	autoDetected := detectTicketIDs(params.SourceBranch + "\n" + params.Title + "\n" + params.Description)
	allTickets = mergeTicketIDs(allTickets, autoDetected)
	params.TicketIDs = allTickets
	params.Provider = string(name)

	return provider.CreatePR(ctx, s.authFromContext(ctx), params)
}

func (s *Service) CompletePR(ctx context.Context, params CompleteParams) (*PullRequest, error) {
	name, provider, err := s.resolveProvider(params.Provider, params.ProjectName)
	if err != nil {
		return nil, err
	}
	params.Provider = string(name)
	return provider.CompletePR(ctx, s.authFromContext(ctx), params)
}

func (s *Service) authFromContext(ctx context.Context) AuthContext {
	out := AuthContext{}
	if v, ok := ctx.Value(auth.AzureDevOpsPATContextKey).(string); ok {
		out.AzureDevOpsPAT = strings.TrimSpace(v)
	}
	return out
}

func buildPRTitle(sourceBranch string, ticketIDs []string) string {
	tickets := mergeTicketIDs(normalizeTicketIDs(ticketIDs), detectTicketIDs(sourceBranch))
	if len(tickets) == 0 {
		return fmt.Sprintf("PR from %s", sourceBranch)
	}
	return fmt.Sprintf("%s - %s", strings.Join(tickets, ", "), sourceBranch)
}

func buildPRDescription(sourceBranch string, ticketIDs []string) string {
	tickets := mergeTicketIDs(normalizeTicketIDs(ticketIDs), detectTicketIDs(sourceBranch))
	if len(tickets) == 0 {
		return fmt.Sprintf("Automated PR for branch %s", sourceBranch)
	}
	return fmt.Sprintf("Automated PR for branch %s\n\nLinked tickets: %s", sourceBranch, strings.Join(tickets, ", "))
}

func detectTicketIDs(input string) []string {
	seen := map[string]bool{}
	out := []string{}
	collect := func(matches []string) {
		for _, m := range matches {
			m = strings.TrimSpace(m)
			if m == "" || seen[m] {
				continue
			}
			seen[m] = true
			out = append(out, m)
		}
	}
	collect(jiraPattern.FindAllString(input, -1))
	collect(adoPattern.FindAllString(input, -1))
	collect(redminePattern.FindAllString(input, -1))
	sort.Strings(out)
	return out
}

func normalizeTicketIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func mergeTicketIDs(current, extra []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, set := range [][]string{current, extra} {
		for _, id := range set {
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

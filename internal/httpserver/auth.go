package httpserver

import (
	"context"
	"net/http"
	"strings"

	"github.com/terenzif/ibis-assistant/internal/auth"
)

// InjectAuth copies ticketing and git credential headers into ctx.
func InjectAuth(ctx context.Context, r *http.Request) context.Context {
	if key := strings.TrimSpace(r.Header.Get("X-Redmine-API-Key")); key != "" {
		ctx = context.WithValue(ctx, auth.RedmineKeyContextKey, key)
	}
	if email := strings.TrimSpace(r.Header.Get("X-Jira-Email")); email != "" {
		ctx = context.WithValue(ctx, auth.JiraEmailContextKey, email)
	}
	if token := strings.TrimSpace(r.Header.Get("X-Jira-API-Token")); token != "" {
		ctx = context.WithValue(ctx, auth.JiraAPITokenContextKey, token)
	}
	if pat := strings.TrimSpace(r.Header.Get("X-Azure-DevOps-PAT")); pat != "" {
		ctx = context.WithValue(ctx, auth.AzureDevOpsPATContextKey, pat)
	}
	if org := strings.TrimSpace(r.Header.Get("X-Azure-DevOps-Org")); org != "" {
		ctx = context.WithValue(ctx, auth.AzureDevOpsOrgContextKey, org)
	}
	if project := strings.TrimSpace(r.Header.Get("X-Azure-DevOps-Project")); project != "" {
		ctx = context.WithValue(ctx, auth.AzureDevOpsProjectContextKey, project)
	}
	if repo := strings.TrimSpace(r.Header.Get("X-Azure-DevOps-Repo")); repo != "" {
		ctx = context.WithValue(ctx, auth.AzureDevOpsRepoContextKey, repo)
	}
	if gitToken := strings.TrimSpace(r.Header.Get("X-Git-Token")); gitToken != "" {
		ctx = context.WithValue(ctx, auth.GitTokenContextKey, gitToken)
	} else if gitPat := strings.TrimSpace(r.Header.Get("X-Git-PAT")); gitPat != "" {
		ctx = context.WithValue(ctx, auth.GitTokenContextKey, gitPat)
	}
	return ctx
}

// Auth copies ticketing and git credential headers into the request context.
func Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(InjectAuth(r.Context(), r)))
	})
}

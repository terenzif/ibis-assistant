package dynamic

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/terenzif/ibis-assistant/internal/auth"
	"github.com/terenzif/ibis-assistant/internal/config"
	"github.com/terenzif/ibis-assistant/internal/db"
	"github.com/terenzif/ibis-assistant/internal/gitrepo"
	"github.com/terenzif/ibis-assistant/internal/logger"
)

// SyncWorkspace assicura che il repo sia aggiornato usando il runner generalizzato.
// Ritorna (repoPath, actualCommit, isAligned, error).
// isAligned è true se il commit richiesto è stato fatto checkout con successo.
func SyncWorkspace(ctx context.Context, cfg *config.Config, dbClient db.Executor, repoName, originUrl, branch, commit string) (string, string, bool, error) {
	repoPath := filepath.Join(cfg.DiscoveryRoot, "dynamic", repoName)
	logger.Info("Syncing workspace for %s at %s (Branch: %s, Commit: %s)", repoName, repoPath, branch, commit)

	err := os.MkdirAll(filepath.Dir(repoPath), 0755)
	if err != nil {
		return "", "", false, fmt.Errorf("failed to create dynamic repos directory: %w", err)
	}

	// 1. Resolve git credentials
	var cred *gitrepo.Credential

	// A. Try request-level context override
	if tokenVal := ctx.Value(auth.GitTokenContextKey); tokenVal != nil {
		if tokenStr, ok := tokenVal.(string); ok && tokenStr != "" {
			cred = &gitrepo.Credential{
				Target:   originUrl,
				Provider: "generic",
				AuthType: gitrepo.AuthTypeToken,
				Token:    tokenStr,
			}
			logger.Debug("Using Git runtime token override from context")
		}
	}

	// B. If not found, check SurrealDB git_credential table
	if cred == nil && dbClient != nil {
		store := gitrepo.NewCredentialStore(dbClient)
		c, err := store.GetCredential(ctx, originUrl)
		if err == nil && c != nil {
			cred = c
			logger.Debug("Using Git credential from DB (URL specific match): %s", originUrl)
		} else {
			if host, errHost := getHost(originUrl); errHost == nil {
				c, err = store.GetCredential(ctx, host)
				if err == nil && c != nil {
					cred = c
					logger.Debug("Using Git credential from DB (host match): %s", host)
				}
			}
		}
	}

	// C. Fallback to legacy config.json
	if cred == nil {
		token := cfg.GetGitToken(originUrl)
		if token != "" {
			cred = &gitrepo.Credential{
				Target:   originUrl,
				Provider: "generic",
				AuthType: gitrepo.AuthTypeToken,
				Token:    token,
			}
			logger.Debug("Using legacy config Git token fallback")
		}
	}

	_, err = os.Stat(repoPath)
	if os.IsNotExist(err) {
		// Clone new repository
		logger.Info("Cloning repository %s from %s", repoName, originUrl)

		var cloneArgs []string
		cloneArgs = append(cloneArgs, "clone")
		if branch != "" {
			cloneArgs = append(cloneArgs, "-b", branch)
		}
		cloneArgs = append(cloneArgs, originUrl, repoPath)

		runner := gitrepo.NewRunner(cred)
		out, err := runner.Run(ctx, filepath.Dir(repoPath), cloneArgs...)
		if err != nil {
			if isAuthError(err, out) {
				return "", "", false, &gitrepo.CredentialsRequiredError{
					Provider: detectProvider(originUrl),
					Target:   originUrl,
					Message:  "Git credentials required or authentication failed during clone",
				}
			}
			return "", "", false, fmt.Errorf("git clone failed: %v, output: %s", err, string(out))
		}
	} else {
		// Repository exists, clean, reset and fetch
		logger.Info("Repository %s already exists. Resetting and fetching from %s", repoName, originUrl)

		runner := gitrepo.NewRunner(cred)

		if _, err := runner.Run(ctx, repoPath, "clean", "-fd"); err != nil {
			logger.Warn("git clean failed: %v", err)
		}

		if _, err := runner.Run(ctx, repoPath, "reset", "--hard"); err != nil {
			logger.Warn("git reset failed: %v", err)
		}

		if _, err := runner.Run(ctx, repoPath, "remote", "set-url", "origin", originUrl); err != nil {
			if _, addErr := runner.Run(ctx, repoPath, "remote", "add", "origin", originUrl); addErr != nil {
				logger.Warn("Failed to add remote origin: %v", addErr)
			}
		}

		out, err := runner.Run(ctx, repoPath, "fetch", "origin")
		if err != nil {
			if isAuthError(err, out) {
				return "", "", false, &gitrepo.CredentialsRequiredError{
					Provider: detectProvider(originUrl),
					Target:   originUrl,
					Message:  "Git credentials required or authentication failed during fetch",
				}
			}
			return "", "", false, fmt.Errorf("git fetch failed: %v, output: %s", err, string(out))
		}
	}

	isAligned := true
	target := branch
	if commit != "" {
		target = commit
	}

	if target != "" {
		logger.Info("Checking out target: %s", target)
		runner := gitrepo.NewRunner(cred)
		out, err := runner.Run(ctx, repoPath, "checkout", target)
		if err != nil {
			if commit != "" && branch != "" {
				logger.Warn("Failed to checkout commit %s. Falling back to branch %s. Output: %s", commit, branch, string(out))
				isAligned = false
				outFallback, errFallback := runner.Run(ctx, repoPath, "checkout", branch)
				if errFallback != nil {
					return "", "", false, fmt.Errorf("git checkout fallback to %s failed: %v, output: %s", branch, errFallback, string(outFallback))
				}
			} else {
				return "", "", false, fmt.Errorf("git checkout %s failed: %v, output: %s", target, err, string(out))
			}
		}
	}

	// Determine actual commit we ended up on
	actualCommit := ""
	runner := gitrepo.NewRunner(cred)
	outRev, errRev := runner.Run(ctx, repoPath, "rev-parse", "HEAD")
	if errRev == nil {
		actualCommit = strings.TrimSpace(string(outRev))
	} else {
		logger.Warn("Failed to rev-parse HEAD: %v", errRev)
	}

	return repoPath, actualCommit, isAligned, nil
}

func getHost(rawURL string) (string, error) {
	if strings.HasPrefix(rawURL, "git@") {
		parts := strings.Split(rawURL, "@")
		if len(parts) > 1 {
			hostParts := strings.Split(parts[1], ":")
			if len(hostParts) > 0 {
				return hostParts[0], nil
			}
		}
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	host := u.Host
	if strings.Contains(host, ":") {
		parts := strings.Split(host, ":")
		host = parts[0]
	}
	return host, nil
}

func isAuthError(err error, output []byte) bool {
	outStr := strings.ToLower(string(output))
	return strings.Contains(outStr, "authentication failed") ||
		strings.Contains(outStr, "terminal prompts disabled") ||
		strings.Contains(outStr, "could not read username") ||
		strings.Contains(outStr, "fatal: could not read from remote repository") ||
		strings.Contains(outStr, "permission denied (publickey)") ||
		strings.Contains(outStr, "could not read passphrase") ||
		strings.Contains(outStr, "the requested url returned error: 403") ||
		strings.Contains(outStr, "the requested url returned error: 401") ||
		strings.Contains(outStr, "invalid username or password") ||
		strings.Contains(outStr, "repository not found") ||
		strings.Contains(outStr, "access denied")
}

func detectProvider(rawURL string) string {
	lowerURL := strings.ToLower(rawURL)
	if strings.Contains(lowerURL, "github.com") {
		return "github"
	}
	if strings.Contains(lowerURL, "gitlab.com") {
		return "gitlab"
	}
	if strings.Contains(lowerURL, "dev.azure.com") || strings.Contains(lowerURL, "visualstudio.com") {
		return "azure_devops"
	}
	return "generic"
}

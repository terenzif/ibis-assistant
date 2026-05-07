package dynamic

import (
	"context"
	"fmt"
	"strings"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/deckonline/knowledge_mcp/internal/logger"
)

// SyncWorkspace assicura che il repo sia aggiornato. 
// Ritorna (repoPath, actualCommit, isAligned, error).
// isAligned è true se il commit richiesto è stato fatto checkout con successo.
func SyncWorkspace(ctx context.Context, discoveryRoot, repoName, originUrl, branch, commit string) (string, string, bool, error) {
	repoPath := filepath.Join(discoveryRoot, "dynamic", repoName)
	logger.Info("Syncing workspace for %s at %s (Branch: %s, Commit: %s)", repoName, repoPath, branch, commit)

	err := os.MkdirAll(filepath.Dir(repoPath), 0755)
	if err != nil {
		return "", "", false, fmt.Errorf("failed to create dynamic repos directory: %w", err)
	}

	_, err = os.Stat(repoPath)
	if os.IsNotExist(err) {
		// Clone new repository
		logger.Info("Cloning repository %s from %s", repoName, originUrl)
		
		// Note: We can't always clone a specific commit directly if the server doesn't support it,
		// so we clone the branch first, then checkout the commit if provided.
		var cloneArgs []string
		token := w.Config.GetGitToken(originUrl)
		if token != "" {
			cloneArgs = append(cloneArgs, "-c", fmt.Sprintf("http.extraHeader=AUTHORIZATION: bearer %s", token))
		}
		cloneArgs = append(cloneArgs, "clone")
		if branch != "" {
			cloneArgs = append(cloneArgs, "-b", branch)
		}
		cloneArgs = append(cloneArgs, originUrl, repoPath)
		
		cmd := exec.CommandContext(ctx, "git", cloneArgs...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", "", false, fmt.Errorf("git clone failed: %v, output: %s", err, string(out))
		}
	} else {
		// Repository exists, clean and fetch
		logger.Info("Repository %s already exists. Resetting and fetching from %s", repoName, originUrl)
		
		var fetchArgs []string
		token := w.Config.GetGitToken(originUrl)
		if token != "" {
			fetchArgs = append(fetchArgs, "-c", fmt.Sprintf("http.extraHeader=AUTHORIZATION: bearer %s", token))
		}
		fetchArgs = append(fetchArgs, "fetch", "origin")

		cmds := [][]string{
			{"clean", "-fd"},
			{"reset", "--hard"},
			// Ensure remote exists and is correct
			{"remote", "set-url", "origin", originUrl},
			fetchArgs,
		}

		for _, args := range cmds {
			cmd := exec.CommandContext(ctx, "git", args...)
			cmd.Dir = repoPath
			out, err := cmd.CombinedOutput()
			if err != nil {
				// If remote set-url fails because remote doesn't exist, we add it
				if args[0] == "remote" && args[1] == "set-url" {
					addCmd := exec.CommandContext(ctx, "git", "remote", "add", "origin", originUrl)
					addCmd.Dir = repoPath
					if _, addErr := addCmd.CombinedOutput(); addErr != nil {
						logger.Warn("Failed to add remote origin: %v", addErr)
					}
					continue
				}
				return "", "", false, fmt.Errorf("git %s failed: %v, output: %s", args[0], err, string(out))
			}
		}
	}

	isAligned := true
	target := branch
	if commit != "" {
		target = commit
	}

	if target != "" {
		logger.Info("Checking out target: %s", target)
		cmd := exec.CommandContext(ctx, "git", "checkout", target)
		cmd.Dir = repoPath
		out, err := cmd.CombinedOutput()
		if err != nil {
			if commit != "" && branch != "" {
				// Fallback to branch if commit failed
				logger.Warn("Failed to checkout commit %s. Falling back to branch %s. Output: %s", commit, branch, string(out))
				isAligned = false
				cmdFallback := exec.CommandContext(ctx, "git", "checkout", branch)
				cmdFallback.Dir = repoPath
				outFallback, errFallback := cmdFallback.CombinedOutput()
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
	cmdRev := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmdRev.Dir = repoPath
	outRev, errRev := cmdRev.CombinedOutput()
	if errRev == nil {
		actualCommit = strings.TrimSpace(string(outRev))
	} else {
		logger.Warn("Failed to rev-parse HEAD: %v", errRev)
	}

	return repoPath, actualCommit, isAligned, nil
}

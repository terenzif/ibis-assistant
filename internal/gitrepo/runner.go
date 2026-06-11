package gitrepo

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var basicAuthRegex = regexp.MustCompile(`(https?://)([^:]+):([^@]+)(@)`)

type GitCommandRunner struct {
	cred *Credential
}

func NewRunner(cred *Credential) *GitCommandRunner {
	return &GitCommandRunner{cred: cred}
}

func (r *GitCommandRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	var cmdArgs []string
	var env []string
	var tempKeyFile string

	defer func() {
		if tempKeyFile != "" {
			os.Remove(tempKeyFile)
		}
	}()

	if r.cred != nil {
		switch r.cred.AuthType {
		case AuthTypeToken:
			var headerVal string
			switch ProviderName(strings.ToLower(r.cred.Provider)) {
			case ProviderAzureDevOps:
				headerVal = fmt.Sprintf("AUTHORIZATION: bearer %s", r.cred.Token)
			case ProviderGitHub:
				authStr := fmt.Sprintf("git:%s", r.cred.Token)
				encoded := base64.StdEncoding.EncodeToString([]byte(authStr))
				headerVal = fmt.Sprintf("Authorization: Basic %s", encoded)
			case ProviderGitLab:
				headerVal = fmt.Sprintf("Private-Token: %s", r.cred.Token)
			default:
				authStr := fmt.Sprintf("git:%s", r.cred.Token)
				encoded := base64.StdEncoding.EncodeToString([]byte(authStr))
				headerVal = fmt.Sprintf("Authorization: Basic %s", encoded)
			}
			cmdArgs = append(cmdArgs, "-c", fmt.Sprintf("http.extraHeader=%s", headerVal))

		case AuthTypeBasic:
			authStr := fmt.Sprintf("%s:%s", r.cred.Username, r.cred.Token)
			encoded := base64.StdEncoding.EncodeToString([]byte(authStr))
			headerVal := fmt.Sprintf("Authorization: Basic %s", encoded)
			cmdArgs = append(cmdArgs, "-c", fmt.Sprintf("http.extraHeader=%s", headerVal))

		case AuthTypeSSH:
			if r.cred.SSHPrivateKey != "" {
				tmpFile, err := os.CreateTemp("", "git-ssh-key-*")
				if err == nil {
					tmpFile.WriteString(r.cred.SSHPrivateKey)
					tmpFile.Close()
					os.Chmod(tmpFile.Name(), 0600)
					tempKeyFile = tmpFile.Name()

					sshCmd := fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=no", tempKeyFile)
					env = append(env, fmt.Sprintf("GIT_SSH_COMMAND=%s", sshCmd))
				}
			}
		}
	}

	cmdArgs = append(cmdArgs, args...)

	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	out, err := cmd.CombinedOutput()
	sanitizedOut := r.maskSecrets(out)

	if err != nil {
		return sanitizedOut, fmt.Errorf("git command failed: %w, output: %s", err, string(sanitizedOut))
	}

	return sanitizedOut, nil
}

func (r *GitCommandRunner) maskSecrets(input []byte) []byte {
	text := string(input)

	// 1. Redact credentials embedded in URLs
	text = basicAuthRegex.ReplaceAllString(text, "${1}[REDACTED]:[REDACTED]${4}")

	if r.cred == nil {
		return []byte(text)
	}

	// 2. Redact sensitive values from our credential
	secrets := []string{}
	if r.cred.Token != "" {
		secrets = append(secrets, r.cred.Token)
		authStr1 := fmt.Sprintf("git:%s", r.cred.Token)
		secrets = append(secrets, base64.StdEncoding.EncodeToString([]byte(authStr1)))
		if r.cred.Username != "" {
			authStr2 := fmt.Sprintf("%s:%s", r.cred.Username, r.cred.Token)
			secrets = append(secrets, base64.StdEncoding.EncodeToString([]byte(authStr2)))
		}
	}
	if r.cred.SSHPrivateKey != "" {
		lines := strings.Split(r.cred.SSHPrivateKey, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.Contains(trimmed, "BEGIN") && !strings.Contains(trimmed, "END") {
				secrets = append(secrets, trimmed)
			}
		}
	}

	for _, secret := range secrets {
		if len(secret) > 4 {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}

	return []byte(text)
}

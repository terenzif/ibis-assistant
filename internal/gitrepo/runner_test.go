package gitrepo

import (
	"context"
	"io/ioutil"
	"os"
	"strings"
	"testing"
)

func TestGitCommandRunner_MaskSecrets(t *testing.T) {
	cred := &Credential{
		Provider: "github",
		AuthType: AuthTypeToken,
		Token:    "ghp_superSecretToken12345",
	}

	runner := NewRunner(cred)

	// Test URL masking
	inputURL := []byte("Cloning into 'repo' from https://oauth2:ghp_superSecretToken12345@github.com/org/repo.git...")
	maskedURL := runner.maskSecrets(inputURL)
	if strings.Contains(string(maskedURL), "ghp_superSecretToken12345") {
		t.Errorf("expected URL token to be masked, got: %s", string(maskedURL))
	}
	if !strings.Contains(string(maskedURL), "[REDACTED]") {
		t.Errorf("expected [REDACTED] in masked URL, got: %s", string(maskedURL))
	}

	// Test token masking in general text
	inputText := []byte("error: ghp_superSecretToken12345 occurred")
	maskedText := runner.maskSecrets(inputText)
	if strings.Contains(string(maskedText), "ghp_superSecretToken12345") {
		t.Errorf("expected token to be masked in text, got: %s", string(maskedText))
	}
	if !strings.Contains(string(maskedText), "[REDACTED]") {
		t.Errorf("expected [REDACTED] in masked text, got: %s", string(maskedText))
	}
}

func TestGitCommandRunner_RunLocalGit(t *testing.T) {
	// We can run 'git version' as a smoke test
	runner := NewRunner(nil)
	tmpDir, err := ioutil.TempDir("", "git-runner-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	out, err := runner.Run(context.Background(), tmpDir, "version")
	if err != nil {
		t.Fatalf("git version failed: %v, output: %s", err, string(out))
	}

	if !strings.Contains(string(out), "git version") {
		t.Errorf("expected output to contain 'git version', got: %s", string(out))
	}
}

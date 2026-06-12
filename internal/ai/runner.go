package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/terenzif/ibis-assistant/internal/logger"
)

// StartOllamaDaemon launches the local Ollama process in the background.
// If not found, it downloads and installs/deploys it on-demand.
func StartOllamaDaemon(ctx context.Context) (*exec.Cmd, error) {
	execPath, err := EnsureOllamaBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(execPath, "serve")
	
	// Direct log output to ollama.log next to the executable
	logPath := "ollama.log"
	if exe, err := os.Executable(); err == nil {
		logPath = filepath.Join(filepath.Dir(exe), "ollama.log")
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	logger.Info("Starting local Ollama daemon: %s serve", execPath)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start Ollama process: %w", err)
	}

	return cmd, nil
}

// EnsureOllama checks if Ollama is running, starts/installs it if configured, and pulls the model if missing.
func EnsureOllama(ctx context.Context, apiURL, model string, autoStart, autoPull bool) (*exec.Cmd, error) {
	if apiURL == "" {
		apiURL = "http://127.0.0.1:11434"
	}

	client := &http.Client{Timeout: 2 * time.Second}

	checkTags := func() ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", apiURL+"/api/tags", nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("status %d", resp.StatusCode)
		}
		return io.ReadAll(resp.Body)
	}

	var cmd *exec.Cmd
	var tagsBody []byte
	var err error

	logger.Info("Checking if Ollama is running at %s...", apiURL)
	tagsBody, err = checkTags()
	if err != nil {
		if !autoStart {
			return nil, fmt.Errorf("ollama is not running and auto-start is disabled: %w", err)
		}

		logger.Warn("Ollama is not running. Attempting to start local daemon...")
		cmd, err = StartOllamaDaemon(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to auto-start Ollama: %w", err)
		}

		// Wait for daemon to become ready
		ready := false
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				return nil, ctx.Err()
			default:
				tagsBody, err = checkTags()
				if err == nil {
					ready = true
					break
				}
				time.Sleep(1 * time.Second)
			}
		}

		if !ready {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("ollama daemon started but failed to respond on %s within 30s", apiURL)
		}
		logger.Info("Ollama daemon started successfully and is responsive.")
	} else {
		logger.Info("Ollama is already running and responsive.")
	}

	// Pull model on-demand if missing
	if autoPull && model != "" {
		if !isModelInstalled(tagsBody, model) {
			logger.Info("Model %s is not installed. Pulling model on-demand...", model)
			if err := pullModel(ctx, apiURL, model); err != nil {
				return cmd, fmt.Errorf("failed to pull model %s: %w", model, err)
			}
			logger.Info("Model %s successfully pulled.", model)
		} else {
			logger.Info("Model %s is already installed.", model)
		}
	}

	return cmd, nil
}

// EnsureOllamaBinary checks if the Ollama binary is present.
// If not found, it downloads and installs/deploys it.
func EnsureOllamaBinary(ctx context.Context) (string, error) {
	execPath, err := findOllamaExec()
	if err == nil {
		return execPath, nil
	}

	logger.Info("Ollama binary not found on standard paths. Starting download on-demand...")

	var downloadURL string
	var localDest string
	var errDownload error

	exeDir := "."
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}

	targetOS := runtime.GOOS
	switch targetOS {
	case "windows":
		downloadURL = "https://github.com/ollama/ollama/releases/latest/download/OllamaSetup.exe"
		setupPath := filepath.Join(os.TempDir(), "OllamaSetup.exe")
		
		logger.Info("Downloading Ollama installer from %s to %s...", downloadURL, setupPath)
		errDownload = downloadFile(ctx, downloadURL, setupPath)
		if errDownload != nil {
			return "", fmt.Errorf("failed to download Ollama setup: %w", errDownload)
		}
		defer os.Remove(setupPath)

		logger.Info("Executing silent installation of Ollama...")
		setupCmd := exec.CommandContext(ctx, setupPath, "/VERYSILENT", "/NORESTART")
		if errRun := setupCmd.Run(); errRun != nil {
			return "", fmt.Errorf("failed to execute Ollama installation: %w", errRun)
		}

		// Wait a brief moment for the installer to finish writing files
		time.Sleep(3 * time.Second)

		// Search again
		execPath, err = findOllamaExec()
		if err != nil {
			return "", fmt.Errorf("ollama installation completed but binary still not found: %w", err)
		}
		logger.Info("Ollama successfully installed and found at: %s", execPath)
		return execPath, nil

	case "linux":
		downloadURL = "https://github.com/ollama/ollama/releases/latest/download/ollama-linux-amd64"
		binDir := filepath.Join(exeDir, "bin")
		_ = os.MkdirAll(binDir, 0755)
		localDest = filepath.Join(binDir, "ollama")

		logger.Info("Downloading Ollama Linux binary from %s to %s...", downloadURL, localDest)
		errDownload = downloadFile(ctx, downloadURL, localDest)
		if errDownload != nil {
			return "", fmt.Errorf("failed to download Ollama binary: %w", errDownload)
		}

		if errChmod := os.Chmod(localDest, 0755); errChmod != nil {
			return "", fmt.Errorf("failed to set executable permission on Ollama binary: %w", errChmod)
		}

		logger.Info("Ollama successfully downloaded and marked executable.")
		return localDest, nil

	case "darwin":
		downloadURL = "https://github.com/ollama/ollama/releases/latest/download/ollama-darwin"
		binDir := filepath.Join(exeDir, "bin")
		_ = os.MkdirAll(binDir, 0755)
		localDest = filepath.Join(binDir, "ollama")

		logger.Info("Downloading Ollama macOS binary from %s to %s...", downloadURL, localDest)
		errDownload = downloadFile(ctx, downloadURL, localDest)
		if errDownload != nil {
			return "", fmt.Errorf("failed to download Ollama macOS binary: %w", errDownload)
		}

		if errChmod := os.Chmod(localDest, 0755); errChmod != nil {
			return "", fmt.Errorf("failed to set executable permission on Ollama binary: %w", errChmod)
		}

		logger.Info("Ollama successfully downloaded and marked executable.")
		return localDest, nil

	default:
		return "", fmt.Errorf("automatic download not supported on OS: %s", targetOS)
	}
}

func findOllamaExec() (string, error) {
	exeDir := "."
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}

	if runtime.GOOS == "windows" {
		// Try next to executable or bin/ first
		paths := []string{
			filepath.Join(exeDir, "ollama.exe"),
			filepath.Join(exeDir, "bin", "ollama.exe"),
		}
		for _, path := range paths {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}

		// 1. Try LOCALAPPDATA
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData != "" {
			path := filepath.Join(localAppData, "Programs", "Ollama", "ollama.exe")
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
		// 2. Try ProgramFiles
		programFiles := os.Getenv("ProgramFiles")
		if programFiles != "" {
			path := filepath.Join(programFiles, "Ollama", "ollama.exe")
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
		// 3. Try PATH
		if path, err := exec.LookPath("ollama.exe"); err == nil {
			return path, nil
		}
	} else {
		// macOS / Linux
		// Try next to executable or bin/ first
		paths := []string{
			filepath.Join(exeDir, "ollama"),
			filepath.Join(exeDir, "bin", "ollama"),
		}
		for _, path := range paths {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}

		if path, err := exec.LookPath("ollama"); err == nil {
			return path, nil
		}
		stdPaths := []string{
			"/usr/local/bin/ollama",
			"/usr/bin/ollama",
			"/bin/ollama",
			"/Applications/Ollama.app/Contents/Resources/ollama",
		}
		for _, path := range stdPaths {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("ollama executable not found in standard paths")
}

func isModelInstalled(tagsResponse []byte, model string) bool {
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(tagsResponse, &tags); err != nil {
		return false
	}
	for _, m := range tags.Models {
		if m.Name == model || m.Name == model+":latest" || strings.HasPrefix(m.Name, model+":") {
			return true
		}
	}
	return false
}

func pullModel(ctx context.Context, apiURL, model string) error {
	url := fmt.Sprintf("%s/api/pull", apiURL)
	payload := map[string]string{"name": model}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to pull model, status %d: %s", resp.StatusCode, string(respBody))
	}

	dec := json.NewDecoder(resp.Body)
	for {
		var progress struct {
			Status    string `json:"status"`
			Digest    string `json:"digest"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
		}
		if err := dec.Decode(&progress); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		if progress.Total > 0 {
			pct := float64(progress.Completed) / float64(progress.Total) * 100
			logger.Info("Ollama pull progress (%s): %.2f%% completed", model, pct)
		} else {
			logger.Info("Ollama pull: %s", progress.Status)
		}
	}
	return nil
}

func downloadFile(ctx context.Context, url, filepathStr string) error {
	out, err := os.Create(filepathStr)
	if err != nil {
		return err
	}
	defer out.Close()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %d", resp.StatusCode)
	}

	_, err = io.Copy(out, resp.Body)
	return err
}

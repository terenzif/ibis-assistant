package db

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
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

	"github.com/deckonline/knowledge_mcp/internal/logger"
)

// GitHubRelease represents a release on GitHub.
type GitHubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []GitHubAsset `json:"assets"`
}

// GitHubAsset represents a file attached to a release.
type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// EnsureSurrealDB checks if the SurrealDB binary is present and up-to-date.
// If not, it downloads the latest version from GitHub.
func EnsureSurrealDB() error {
	binName := "surreal"
	if runtime.GOOS == "windows" {
		binName = "surreal.exe"
	}

	logger.Info("Checking SurrealDB installation (%s)...", binName)

	// 1. Get local version
	localVer, err := getLocalVersion(binName)
	if err == nil {
		logger.Info("Found local SurrealDB version: %s", localVer)
	} else {
		logger.Info("SurrealDB not found or version check failed: %v", err)
	}

	// 2. Get latest release info from GitHub
	// Use a short timeout to avoid blocking startup too long if offline
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	release, err := getLatestRelease(ctx)
	if err != nil {
		if localVer != "" {
			logger.Warn("Failed to check for updates (using local version): %v", err)
			return nil
		}
		return fmt.Errorf("failed to get release info and no local binary found: %w", err)
	}

	// Clean tag name (remove 'v' prefix)
	remoteVer := strings.TrimPrefix(release.TagName, "v")

	// 3. Compare versions
	if localVer == remoteVer {
		logger.Info("SurrealDB is up to date (%s).", localVer)
		return nil
	}

	logger.Info("Newer SurrealDB version available: %s (Local: %s). Downloading...", remoteVer, localVer)

	// 4. Download and Install
	if err := installSurreal(ctx, release, binName); err != nil {
		if localVer != "" {
			logger.Warn("Failed to update SurrealDB (using local version): %v", err)
			return nil
		}
		return fmt.Errorf("failed to install SurrealDB: %w", err)
	}

	logger.Info("SurrealDB updated successfully to %s.", remoteVer)
	return nil
}

func getLatestRelease(ctx context.Context) (*GitHubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/surrealdb/surrealdb/releases/latest", nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned status: %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	return &release, nil
}

func getLocalVersion(binName string) (string, error) {
	// Check if file exists first
	if _, err := os.Stat(binName); os.IsNotExist(err) {
		return "", err
	}

	// Run "surreal version"
	// Use absolute path or strict relative path
	path := filepath.Join(".", binName)
	cmd := exec.Command(path, "version")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	// Output format example: "surreal 2.0.4 ..."
	// We want "2.0.4"
	parts := strings.Fields(string(out))
	if len(parts) >= 2 && strings.EqualFold(parts[0], "surreal") {
		return parts[1], nil
	} else if len(parts) >= 1 {
		// Fallback/Unknown format, just return first token if it looks like a version
		return parts[0], nil
	}

	return "", fmt.Errorf("could not parse version output: %s", string(out))
}

func installSurreal(ctx context.Context, release *GitHubRelease, binName string) error {
	// Determine target asset name pattern
	// Windows: surreal-vX.Y.Z.windows-amd64.exe OR .zip
	// Linux: surreal-vX.Y.Z.linux-amd64.tgz
	// Darwin: surreal-vX.Y.Z.darwin-amd64.tgz

	targetOS := runtime.GOOS
	targetArch := runtime.GOARCH

	// Match asset
	var assetURL string
	var assetName string

	for _, a := range release.Assets {
		name := strings.ToLower(a.Name)
		if strings.Contains(name, targetOS) && strings.Contains(name, targetArch) {
			// Prefer .tgz, .exe, or .zip
			if strings.HasSuffix(name, ".tgz") || strings.HasSuffix(name, ".exe") || strings.HasSuffix(name, ".zip") {
				assetURL = a.BrowserDownloadURL
				assetName = a.Name
				break
			}
		}
	}

	if assetURL == "" {
		return fmt.Errorf("no compatible asset found for %s/%s in release %s", targetOS, targetArch, release.TagName)
	}

	logger.Info("Downloading %s...", assetName)

	// Download to temp file
	tmpFile := "download_" + assetName
	if err := downloadFile(ctx, assetURL, tmpFile); err != nil {
		return err
	}
	defer os.Remove(tmpFile)

	// Install
	if strings.HasSuffix(assetName, ".exe") {
		// Windows: Just rename/move
		if err := replaceBinary(tmpFile, binName); err != nil {
			return err
		}
	} else if strings.HasSuffix(assetName, ".tgz") {
		// Linux/Mac: Extract
		if err := extractTarGz(tmpFile, binName); err != nil {
			return err
		}
	} else if strings.HasSuffix(assetName, ".zip") {
		// Windows: Extract Zip
		if err := extractZip(tmpFile, binName); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("unsupported asset type: %s", assetName)
	}

	// Ensure executable permissions (Unix)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(binName, 0755); err != nil {
			return fmt.Errorf("failed to set executable permissions: %w", err)
		}
	}

	return nil
}

func downloadFile(ctx context.Context, url, filepath string) error {
	out, err := os.Create(filepath)
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

func replaceBinary(src, dest string) error {
	// On Windows, we can't overwrite a running binary, but we can rename it.
	// But simple strategy: Remove old, Rename new.
	// If Remove fails (locked), we can't update.

	// Try to remove existing
	if _, err := os.Stat(dest); err == nil {
		// Try rename first to a backup (surreal.exe.old)
		backup := dest + ".old"
		os.Remove(backup) // ignore error
		if err := os.Rename(dest, backup); err != nil {
			return fmt.Errorf("failed to replace existing binary (locked?): %w", err)
		}
	}

	if err := os.Rename(src, dest); err != nil {
		return fmt.Errorf("failed to move new binary into place: %w", err)
	}
	return nil
}

func extractTarGz(archivePath, targetBinName string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		name := filepath.Base(header.Name)
		if name == "surreal" || name == "surreal.exe" {
			outFile, err := os.Create(targetBinName)
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
			return nil
		}
	}

	return fmt.Errorf("binary 'surreal' not found in archive")
}

func extractZip(archivePath, targetBinName string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if name == "surreal" || name == "surreal.exe" {
			rc, err := f.Open()
			if err != nil {
				return err
			}

			outFile, err := os.Create(targetBinName)
			if err != nil {
				rc.Close()
				return err
			}
			_, err = io.Copy(outFile, rc)
			outFile.Close()
			rc.Close()
			return err
		}
	}
	return fmt.Errorf("binary 'surreal' not found in zip archive")
}

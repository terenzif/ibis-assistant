package code

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

	"github.com/terenzif/ibis-assistant/internal/logger"
)

type GitHubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []GitHubAsset `json:"assets"`
}

type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func getAstGrepBinPath() string {
	binName := "sg"
	if runtime.GOOS == "windows" {
		binName = "sg.exe"
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), binName)
	}
	return binName
}

// EnsureAstGrep checks if the ast-grep (sg) binary is present and up-to-date.
func EnsureAstGrep(autoUpdate bool) error {
	binName := getAstGrepBinPath()

	logger.Info("Checking ast-grep (sg) installation (%s)...", binName)

	localVer, err := getLocalVersion(binName)
	if err == nil {
		logger.Info("Found local ast-grep version: %s", localVer)
	} else {
		logger.Info("ast-grep not found or version check failed: %v", err)
	}

	if !autoUpdate {
		if localVer != "" {
			logger.Info("ast-grep auto-update is disabled, using local version: %s", localVer)
			_ = EnsureAstGrepRules()
			return nil
		}
		logger.Warn("ast-grep auto-update is disabled but no local binary found. Force checking for download...")
	}

	apiCtx, apiCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer apiCancel()

	release, err := getLatestRelease(apiCtx)
	if err != nil {
		if localVer != "" {
			logger.Warn("Failed to check for updates (using local version): %v", err)
			_ = EnsureAstGrepRules()
			return nil
		}
		return fmt.Errorf("failed to get release info and no local binary found: %w", err)
	}

	remoteVer := strings.TrimPrefix(release.TagName, "v")

	if localVer == remoteVer {
		logger.Info("ast-grep is up to date (%s).", localVer)
		_ = EnsureAstGrepRules()
		return nil
	}

	logger.Info("Newer ast-grep version available: %s (Local: %s). Downloading...", remoteVer, localVer)

	downloadCtx, downloadCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer downloadCancel()

	if err := installAstGrep(downloadCtx, release, binName); err != nil {
		if localVer != "" {
			logger.Warn("Failed to update ast-grep (using local version): %v", err)
			_ = EnsureAstGrepRules()
			return nil
		}
		return fmt.Errorf("failed to install ast-grep: %w", err)
	}

	logger.Info("ast-grep updated successfully to %s.", remoteVer)
	_ = EnsureAstGrepRules()
	return nil
}

// EnsureAstGrepRules copies sgconfig.yml + rules/ next to the sg binary (and
// executable dir) so ParseAST can find LogAlign extractors when the process
// cwd is testrun/ or another harness directory.
func EnsureAstGrepRules() error {
	srcRoot := findSGConfigDir()
	if srcRoot == "" {
		// Walk from this source file's repo via cwd parents already tried;
		// last resort: look beside the running binary's parent (repo root).
		if cwd, err := os.Getwd(); err == nil {
			dir := cwd
			for i := 0; i < 8; i++ {
				if _, err := os.Stat(filepath.Join(dir, "rules", "go-logs.yml")); err == nil {
					srcRoot = dir
					break
				}
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				dir = parent
			}
		}
	}
	if srcRoot == "" {
		logger.Debug("EnsureAstGrepRules: no rules source found")
		return nil
	}

	targets := map[string]struct{}{}
	if exe, err := os.Executable(); err == nil {
		targets[filepath.Dir(exe)] = struct{}{}
	}
	if bin := getAstGrepBinPath(); bin != "" {
		if abs, err := filepath.Abs(filepath.Dir(bin)); err == nil {
			targets[abs] = struct{}{}
		}
	}
	for dest := range targets {
		if dest == "" || dest == srcRoot {
			continue
		}
		if err := syncSGAssets(srcRoot, dest); err != nil {
			logger.Warn("EnsureAstGrepRules: sync to %s failed: %v", dest, err)
		} else {
			logger.Info("EnsureAstGrepRules: synced rules/sgconfig → %s", dest)
		}
	}
	return nil
}

func syncSGAssets(srcRoot, destDir string) error {
	srcCfg := filepath.Join(srcRoot, "sgconfig.yml")
	dstCfg := filepath.Join(destDir, "sgconfig.yml")
	if err := copyFile(srcCfg, dstCfg); err != nil {
		return err
	}
	srcRules := filepath.Join(srcRoot, "rules")
	dstRules := filepath.Join(destDir, "rules")
	if err := os.RemoveAll(dstRules); err != nil && !os.IsNotExist(err) {
		return err
	}
	return copyDir(srcRules, dstRules)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}

func getLatestRelease(ctx context.Context) (*GitHubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/ast-grep/ast-grep/releases/latest", nil)
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
	if _, err := os.Stat(binName); os.IsNotExist(err) {
		return "", err
	}

	path, err := filepath.Abs(binName)
	if err != nil {
		return "", err
	}
	cmd := exec.Command(path, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	// Output format: "ast-grep 0.31.0"
	parts := strings.Fields(string(out))
	if len(parts) >= 2 {
		return parts[1], nil
	}
	return "", fmt.Errorf("could not parse version output: %s", string(out))
}

func installAstGrep(ctx context.Context, release *GitHubRelease, binName string) error {
	targetOS := runtime.GOOS
	targetArch := runtime.GOARCH

	// map standard GOOS/GOARCH to ast-grep asset naming conventions
	osMap := map[string]string{
		"windows": "pc-windows-msvc",
		"linux":   "unknown-linux-gnu",
		"darwin":  "apple-darwin",
	}
	archMap := map[string]string{
		"amd64": "x86_64",
		"arm64": "aarch64",
	}

	astGrepOS, ok := osMap[targetOS]
	if !ok {
		astGrepOS = targetOS
	}
	astGrepArch, ok := archMap[targetArch]
	if !ok {
		astGrepArch = targetArch
	}

	var assetURL string
	var assetName string

	for _, a := range release.Assets {
		name := strings.ToLower(a.Name)
		// e.g. "app-x86_64-pc-windows-msvc.zip"
		if strings.Contains(name, astGrepArch) && strings.Contains(name, astGrepOS) {
			if strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".tar.gz") {
				assetURL = a.BrowserDownloadURL
				assetName = a.Name
				break
			}
		}
	}

	if assetURL == "" {
		return fmt.Errorf("no compatible asset found for %s/%s in release %s", astGrepArch, astGrepOS, release.TagName)
	}

	logger.Info("Downloading %s...", assetName)

	tmpFile := "download_" + assetName
	if err := downloadFile(ctx, assetURL, tmpFile); err != nil {
		return err
	}
	defer os.Remove(tmpFile)

	if strings.HasSuffix(assetName, ".zip") {
		if err := extractZip(tmpFile, binName); err != nil {
			return err
		}
	} else if strings.HasSuffix(assetName, ".tar.gz") {
		if err := extractTarGz(tmpFile, binName); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("unsupported asset type: %s", assetName)
	}

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
		if name == "sg" || name == "sg.exe" || name == "ast-grep" || name == "ast-grep.exe" {
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

	return fmt.Errorf("binary 'sg' not found in archive")
}

func extractZip(archivePath, targetBinName string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if name == "sg" || name == "sg.exe" || name == "ast-grep" || name == "ast-grep.exe" {
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
	return fmt.Errorf("binary 'sg' not found in zip archive")
}

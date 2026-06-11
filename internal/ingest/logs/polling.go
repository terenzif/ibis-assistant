package logs

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hirochachacha/go-smb2"
	"github.com/jlaffaye/ftp"
	"github.com/pkg/sftp"
	"github.com/terenzif/ibis-arc/internal/ai"
	"github.com/terenzif/ibis-arc/internal/config"
	"github.com/terenzif/ibis-arc/internal/db"
	"github.com/terenzif/ibis-arc/internal/logger"
	"golang.org/x/crypto/ssh"
)

type PollingManager struct {
	Cfg    *config.Config
	DB     db.Executor
	AI     *ai.Client
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

func NewPollingManager(cfg *config.Config, dbClient db.Executor, aiClient *ai.Client) *PollingManager {
	return &PollingManager{
		Cfg: cfg,
		DB:  dbClient,
		AI:  aiClient,
	}
}

func (pm *PollingManager) Start(ctx context.Context) {
	if pm.DB == nil {
		logger.Warn("PollingManager: database is not connected. Polling disabled.")
		return
	}

	pm.ctx, pm.cancel = context.WithCancel(ctx)

	for _, source := range pm.Cfg.LogIngestion.Polling {
		if !source.Enabled {
			continue
		}

		source := source
		pm.wg.Add(1)
		go func() {
			defer pm.wg.Done()

			interval := 15 * time.Minute
			if source.PollInterval != "" {
				if dur, err := time.ParseDuration(source.PollInterval); err == nil {
					interval = dur
				} else {
					logger.Warn("PollingManager: invalid poll interval '%s' for source %s, using 15m: %v", source.PollInterval, source.Name, err)
				}
			}

			logger.Info("Starting polling client for source: %s (protocol: %s, interval: %v)", source.Name, source.Protocol, interval)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			if err := pm.pollSource(source); err != nil {
				logger.Error("PollingManager: error polling source %s: %v", source.Name, err)
			}

			for {
				select {
				case <-ticker.C:
					if err := pm.pollSource(source); err != nil {
						logger.Error("PollingManager: error polling source %s: %v", source.Name, err)
					}
				case <-pm.ctx.Done():
					return
				}
			}
		}()
	}
}

func (pm *PollingManager) Stop() {
	if pm.cancel != nil {
		pm.cancel()
	}
	pm.wg.Wait()
	logger.Info("PollingManager stopped.")
}

func (pm *PollingManager) pollSource(source config.PollingSource) error {
	logger.Debug("PollingManager: polling source %s...", source.Name)
	switch strings.ToLower(source.Protocol) {
	case "ftp":
		return pollFTP(pm.ctx, source, pm.DB, pm.AI, pm.Cfg)
	case "sftp":
		return pollSFTP(pm.ctx, source, pm.DB, pm.AI, pm.Cfg)
	case "smb":
		return pollSMB(pm.ctx, source, pm.DB, pm.AI, pm.Cfg)
	default:
		return fmt.Errorf("unsupported protocol: %s", source.Protocol)
	}
}

func pollFTP(ctx context.Context, source config.PollingSource, dbClient db.Executor, aiClient *ai.Client, cfg *config.Config) error {
	addr := fmt.Sprintf("%s:%d", source.Host, source.Port)
	if source.Port == 0 {
		addr = fmt.Sprintf("%s:21", source.Host)
	}
	conn, err := ftp.Dial(addr, ftp.DialWithTimeout(10*time.Second))
	if err != nil {
		return fmt.Errorf("FTP dial failed: %w", err)
	}
	defer conn.Quit()

	err = conn.Login(source.User, source.Password)
	if err != nil {
		return fmt.Errorf("FTP login failed: %w", err)
	}

	entries, err := conn.List(source.RemoteDir)
	if err != nil {
		return fmt.Errorf("FTP list failed: %w", err)
	}

	for _, entry := range entries {
		if entry.Type != ftp.EntryTypeFile {
			continue
		}

		matched, _ := filepath.Match(source.FilePattern, entry.Name)
		if !matched {
			continue
		}

		remotePath := filepath.ToSlash(filepath.Join(source.RemoteDir, entry.Name))

		alreadyProcessed, err := isFileProcessed(ctx, dbClient, remotePath, int64(entry.Size))
		if err != nil || alreadyProcessed {
			continue
		}

		resp, err := conn.Retr(remotePath)
		if err != nil {
			logger.Error("FTP retrieve failed for %s: %v", remotePath, err)
			continue
		}

		bodyBytes, err := io.ReadAll(resp)
		resp.Close()
		if err != nil {
			logger.Error("FTP read failed for %s: %v", remotePath, err)
			continue
		}

		lines := strings.Split(string(bodyBytes), "\n")
		processLogLines(ctx, source.ProjectName, entry.Name, lines, dbClient, aiClient, cfg)

		if source.ArchiveDir != "" {
			archivePath := filepath.ToSlash(filepath.Join(source.ArchiveDir, entry.Name))
			err = conn.Rename(remotePath, archivePath)
			if err != nil {
				logger.Error("FTP rename/archive failed for %s: %v", remotePath, err)
			}
		}

		recordFileProcessed(ctx, dbClient, remotePath, int64(entry.Size))
	}

	return nil
}

func pollSFTP(ctx context.Context, source config.PollingSource, dbClient db.Executor, aiClient *ai.Client, cfg *config.Config) error {
	addr := fmt.Sprintf("%s:%d", source.Host, source.Port)
	if source.Port == 0 {
		addr = fmt.Sprintf("%s:22", source.Host)
	}

	sshConfig := &ssh.ClientConfig{
		User: source.User,
		Auth: []ssh.AuthMethod{
			ssh.Password(source.Password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	sshConn, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return fmt.Errorf("SSH dial failed: %w", err)
	}
	defer sshConn.Close()

	client, err := sftp.NewClient(sshConn)
	if err != nil {
		return fmt.Errorf("SFTP client init failed: %w", err)
	}
	defer client.Close()

	files, err := client.ReadDir(source.RemoteDir)
	if err != nil {
		return fmt.Errorf("SFTP readdir failed: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		matched, _ := filepath.Match(source.FilePattern, file.Name())
		if !matched {
			continue
		}

		remotePath := filepath.ToSlash(filepath.Join(source.RemoteDir, file.Name()))

		alreadyProcessed, err := isFileProcessed(ctx, dbClient, remotePath, file.Size())
		if err != nil || alreadyProcessed {
			continue
		}

		f, err := client.Open(remotePath)
		if err != nil {
			logger.Error("SFTP open failed for %s: %v", remotePath, err)
			continue
		}

		bodyBytes, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			logger.Error("SFTP read failed for %s: %v", remotePath, err)
			continue
		}

		lines := strings.Split(string(bodyBytes), "\n")
		processLogLines(ctx, source.ProjectName, file.Name(), lines, dbClient, aiClient, cfg)

		if source.ArchiveDir != "" {
			archivePath := filepath.ToSlash(filepath.Join(source.ArchiveDir, file.Name()))
			client.MkdirAll(source.ArchiveDir)
			err = client.Rename(remotePath, archivePath)
			if err != nil {
				logger.Error("SFTP rename/archive failed for %s: %v", remotePath, err)
			}
		}

		recordFileProcessed(ctx, dbClient, remotePath, file.Size())
	}

	return nil
}

func pollSMB(ctx context.Context, source config.PollingSource, dbClient db.Executor, aiClient *ai.Client, cfg *config.Config) error {
	addr := fmt.Sprintf("%s:%d", source.Host, source.Port)
	if source.Port == 0 {
		addr = fmt.Sprintf("%s:445", source.Host)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("SMB dial failed: %w", err)
	}
	defer conn.Close()

	d := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     source.User,
			Password: source.Password,
		},
	}

	s, err := d.Dial(conn)
	if err != nil {
		return fmt.Errorf("SMB dialer dial failed: %w", err)
	}
	defer s.Logoff()

	cleaned := filepath.Clean(source.RemoteDir)
	parts := strings.Split(filepath.ToSlash(cleaned), "/")
	if len(parts) == 0 || parts[0] == "" {
		return fmt.Errorf("SMB requires a share name in RemoteDir (e.g. 'share/path')")
	}
	shareName := parts[0]
	subDir := ""
	if len(parts) > 1 {
		subDir = strings.Join(parts[1:], "/")
	}

	fs, err := s.Mount(shareName)
	if err != nil {
		return fmt.Errorf("SMB mount share '%s' failed: %w", shareName, err)
	}
	defer fs.Umount()

	files, err := fs.ReadDir(subDir)
	if err != nil {
		return fmt.Errorf("SMB readdir '%s' failed: %w", subDir, err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		matched, _ := filepath.Match(source.FilePattern, file.Name())
		if !matched {
			continue
		}

		remotePath := filepath.ToSlash(filepath.Join(subDir, file.Name()))
		fullDBPath := fmt.Sprintf("smb://%s/%s/%s", source.Host, shareName, remotePath)

		alreadyProcessed, err := isFileProcessed(ctx, dbClient, fullDBPath, file.Size())
		if err != nil || alreadyProcessed {
			continue
		}

		f, err := fs.Open(remotePath)
		if err != nil {
			logger.Error("SMB open failed for %s: %v", remotePath, err)
			continue
		}

		bodyBytes, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			logger.Error("SMB read failed for %s: %v", remotePath, err)
			continue
		}

		lines := strings.Split(string(bodyBytes), "\n")
		processLogLines(ctx, source.ProjectName, file.Name(), lines, dbClient, aiClient, cfg)

		if source.ArchiveDir != "" {
			archiveSharePath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(source.ArchiveDir)), shareName+"/")
			archivePath := filepath.ToSlash(filepath.Join(archiveSharePath, file.Name()))
			err = fs.Rename(remotePath, archivePath)
			if err != nil {
				logger.Error("SMB rename/archive failed for %s: %v", remotePath, err)
			}
		}

		recordFileProcessed(ctx, dbClient, fullDBPath, file.Size())
	}

	return nil
}

func isFileProcessed(ctx context.Context, dbClient db.Executor, path string, size int64) (bool, error) {
	if dbClient == nil {
		return false, nil
	}
	res, err := dbClient.Execute(ctx, fmt.Sprintf("SELECT id FROM log_processed_file WHERE path = '%s' AND size = %d;", db.EscapeSQL(path), size))
	if err != nil {
		return false, err
	}
	rows, ok := res.([]interface{})
	if ok && len(rows) > 0 {
		return true, nil
	}
	return false, nil
}

func recordFileProcessed(ctx context.Context, dbClient db.Executor, path string, size int64) {
	if dbClient == nil {
		return
	}
	id := fmt.Sprintf("log_processed_file:%s", db.SanitizeID(filepath.Base(path)))
	timestamp := time.Now().Format(time.RFC3339)
	dbClient.Execute(ctx, fmt.Sprintf("UPDATE %s SET path = '%s', size = %d, processed_at = '%s';",
		id, db.EscapeSQL(path), size, timestamp))
}

func processLogLines(ctx context.Context, project, fileName string, lines []string, dbClient db.Executor, aiClient *ai.Client, cfg *config.Config) {
	var cleanLines []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			cleanLines = append(cleanLines, trimmed)
		}
	}

	if len(cleanLines) == 0 {
		return
	}

	path := filepath.Join(cfg.LogsRoot, project, fileName)
	analyzer := NewLogAnalyzer(ctx, path, cfg, dbClient, aiClient)

	anomalies, err := analyzer.ProcessBatchSync(ctx, cleanLines)
	if err != nil {
		logger.Error("Polling log processing error for %s/%s: %v", project, fileName, err)
		return
	}

	if len(anomalies) > 0 {
		notifier := NewNotifier(cfg, dbClient)
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Remote Polling Log Report for Project: %s (File: %s)\r\n\r\n", project, fileName))
		sb.WriteString(fmt.Sprintf("Detected %d errors/anomalies:\r\n\r\n", len(anomalies)))
		maxSeverity := 0
		for _, e := range anomalies {
			if e.Severity > maxSeverity {
				maxSeverity = e.Severity
			}
			sb.WriteString(fmt.Sprintf("Category: %s\r\n", e.Category))
			sb.WriteString(fmt.Sprintf("Severity: %d/10\r\n", e.Severity))
			if e.File != "" {
				sb.WriteString(fmt.Sprintf("File: %s\r\n", e.File))
			}
			sb.WriteString("Stack Trace:\r\n")
			sb.WriteString(e.StackTrace + "\r\n\r\n")
		}
		notifier.QueueNotification(ctx, project, fileName, len(anomalies), maxSeverity, sb.String())
	}
}

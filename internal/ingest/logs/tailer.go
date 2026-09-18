package logs

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nxadm/tail"
	"github.com/terenzif/ibis-server/internal/ai"
	"github.com/terenzif/ibis-server/internal/config"
	"github.com/terenzif/ibis-server/internal/db"
	"github.com/terenzif/ibis-server/internal/logger"
)

type Tailer struct {
	Path     string
	Cfg      *config.Config
	DB       db.Executor
	AI       *ai.Client
	tail     *tail.Tail
	Analyzer *LogAnalyzer
}

func NewTailer(ctx context.Context, path string, cfg *config.Config, dbClient db.Executor, aiClient *ai.Client) *Tailer {
	project := ""
	if cfg != nil {
		parent := filepath.Base(filepath.Dir(filepath.Clean(path)))
		rootBase := filepath.Base(filepath.Clean(cfg.LogsRoot))
		if parent != "" && parent != "." && parent != rootBase {
			project = parent
		}
	}
	return &Tailer{
		Path:     path,
		Cfg:      cfg,
		DB:       dbClient,
		AI:       aiClient,
		Analyzer: NewLogAnalyzerWithProject(ctx, path, project, cfg, dbClient, aiClient),
	}
}

func (t *Tailer) Tail(ctx context.Context) {
	// Retrieve the last processed position to ensure idempotency across restarts.
	offset := t.Analyzer.GetOffset(ctx)
	logger.Info("Initializing log tailer for: %s (Resuming from offset: %d)", t.Path, offset)

	seekInfo := &tail.SeekInfo{Offset: offset, Whence: 0}

	tc := tail.Config{
		Follow:    true,
		ReOpen:    true,
		MustExist: false,
		Poll:      true,
		Location:  seekInfo,
	}

	tailer, err := tail.TailFile(t.Path, tc)
	if err != nil {
		logger.Error("Failed to tail file %s: %v", t.Path, err)
		return
	}
	t.tail = tailer

	go func() {
		<-ctx.Done()
		t.Stop()
	}()

	var batch []string
	var allAnomalies []SemanticError
	var discoveryDone bool
	var chunkRegex *regexp.Regexp

	startTime := time.Now()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	flush := func(lines []string) {
		if len(lines) == 0 {
			return
		}
		anomalies, err := t.Analyzer.ProcessBatchSync(ctx, lines)
		if err != nil {
			logger.Error("Log batch analysis failed for %s: %v", t.Path, err)
			return
		}
		if n := len(anomalies); n > 0 {
			allAnomalies = append(allAnomalies, anomalies...)
			t.emitReport(ctx, allAnomalies)
		}
		if pos, err := tailer.Tell(); err == nil {
			t.Analyzer.UpdateOffset(ctx, pos)
		}
	}

	for {
		select {
		case line, ok := <-tailer.Lines:
			if !ok {
				flush(batch)
				t.finalize(ctx, allAnomalies, startTime)
				logger.Info("Stopped tail on: %s", t.Path)
				return
			}
			if line.Err != nil {
				logger.Error("Error reading line from %s: %v", t.Path, line.Err)
				continue
			}
			text := strings.TrimSpace(line.Text)
			if text == "" {
				continue
			}
			batch = append(batch, text)

			if !discoveryDone && len(batch) >= 50 {
				regexStr := t.Analyzer.DiscoverFormat(ctx, batch)
				if regexStr != "" {
					if r, err := regexp.Compile(regexStr); err == nil {
						chunkRegex = r
						logger.Info("Discovered log chunk delimiter: %s", regexStr)
					}
				}
				discoveryDone = true
			}

			shouldProcess := false
			if len(batch) >= 100 {
				if chunkRegex != nil {
					if chunkRegex.MatchString(text) {
						shouldProcess = true
					} else if len(batch) >= 500 {
						shouldProcess = true
					}
				} else {
					shouldProcess = true
				}
			}

			if shouldProcess {
				var processBatch []string
				if chunkRegex != nil && chunkRegex.MatchString(text) {
					processBatch = batch[:len(batch)-1]
					batch = []string{text}
				} else {
					processBatch = batch
					batch = nil
				}
				flush(processBatch)
			}
		case <-ctx.Done():
			flush(batch)
			t.finalize(ctx, allAnomalies, startTime)
			logger.Info("Context cancelled, stopping tail on: %s", t.Path)
			return
		case <-ticker.C:
			if len(batch) > 0 {
				toFlush := batch
				batch = nil
				flush(toFlush)
			}
		}
	}
}

func (t *Tailer) emitReport(ctx context.Context, anomalies []SemanticError) {
	if len(anomalies) == 0 {
		return
	}
	reporter := NewReporter(t.Cfg, t.DB)
	cost := float64(len(anomalies)) * 0.0001
	reportPath, err := reporter.GenerateReport(t.Analyzer.Project, t.Path, anomalies, cost)
	if err != nil {
		logger.Error("Failed to generate report for %s: %v", t.Path, err)
		return
	}
	notifier := NewNotifier(t.Cfg, t.DB)
	content, readErr := os.ReadFile(reportPath)
	if readErr == nil {
		_ = notifier.QueueNotification(ctx, t.Analyzer.Project, t.Path, len(anomalies), 5, string(content))
	} else {
		_ = notifier.SendEmail(reportPath)
	}
}

func (t *Tailer) finalize(ctx context.Context, anomalies []SemanticError, start time.Time) {
	_ = start
	t.emitReport(ctx, anomalies)
}

func (t *Tailer) Stop() {
	if t.tail != nil {
		t.tail.Stop()
	}
}

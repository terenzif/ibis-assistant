package logs

import (
	"context"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/nxadm/tail"
	"github.com/terenzif/ibis-assistant/internal/ai"
	"github.com/terenzif/ibis-assistant/internal/config"
	"github.com/terenzif/ibis-assistant/internal/db"
	"github.com/terenzif/ibis-assistant/internal/logger"
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
	return &Tailer{
		Path:     path,
		Cfg:      cfg,
		DB:       dbClient,
		AI:       aiClient,
		Analyzer: NewLogAnalyzer(ctx, path, cfg, dbClient, aiClient),
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
	var totalErrors int
	var discoveryDone bool
	var chunkRegex *regexp.Regexp

	startTime := time.Now()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case line, ok := <-tailer.Lines:
			if !ok {
				if len(batch) > 0 {
					t.Analyzer.ProcessBatch(ctx, batch)
				}
				t.finalize(ctx, totalErrors, startTime)
				logger.Info("Stopped tail on: %s", t.Path)
				return
			}
			if line.Err != nil {
				logger.Error("Error reading line from %s: %v", t.Path, line.Err)
				continue
			}
			text := strings.TrimSpace(line.Text)
			if text != "" {
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

					t.Analyzer.ProcessBatch(ctx, processBatch)
					totalErrors += len(processBatch)
					if pos, err := tailer.Tell(); err == nil {
						t.Analyzer.UpdateOffset(ctx, pos)
					}
				}
			}
		case <-ctx.Done():
			if len(batch) > 0 {
				t.Analyzer.ProcessBatch(ctx, batch)
			}
			t.finalize(ctx, totalErrors, startTime)
			logger.Info("Context cancelled, stopping tail on: %s", t.Path)
			return
		case <-ticker.C:
			if len(batch) > 0 {
				t.Analyzer.ProcessBatch(ctx, batch)
				totalErrors += len(batch)
				batch = nil
				if pos, err := tailer.Tell(); err == nil {
					t.Analyzer.UpdateOffset(ctx, pos)
				}
			}
		}
	}
}

func (t *Tailer) finalize(ctx context.Context, totalErrors int, start time.Time) {
	// Trigger Report and Email if we processed anything
	if totalErrors > 0 {
		reporter := NewReporter(t.Cfg, t.DB)
		// Cost calculation - simple estimate
		cost := float64(totalErrors) * 0.0001

		reportPath, err := reporter.GenerateReport(t.Analyzer.Project, t.Path, totalErrors, cost)
		if err == nil {
			notifier := NewNotifier(t.Cfg, t.DB)
			content, readErr := os.ReadFile(reportPath)
			if readErr == nil {
				// Queue the notification which handles throttling/emergency alerts
				notifier.QueueNotification(ctx, t.Analyzer.Project, t.Path, totalErrors, 5, string(content))
			} else {
				notifier.SendEmail(reportPath)
			}
		}
	}
}

func (t *Tailer) Stop() {
	if t.tail != nil {
		t.tail.Stop()
	}
}

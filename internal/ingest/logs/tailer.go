package logs

import (
	"context"
	"strings"
	"time"

	"github.com/deckonline/knowledge_mcp/internal/ai"
	"github.com/deckonline/knowledge_mcp/internal/config"
	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/nxadm/tail"
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
				t.finalize(totalErrors, startTime)
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
				if len(batch) >= 100 {
					t.Analyzer.ProcessBatch(ctx, batch)
					totalErrors += len(batch) // Rough estimate or count from analyzer
					batch = nil
					// Persist offset
					if pos, err := tailer.Tell(); err == nil {
						t.Analyzer.UpdateOffset(ctx, pos)
					}
				}
			}
		case <-ctx.Done():
			if len(batch) > 0 {
				t.Analyzer.ProcessBatch(ctx, batch)
			}
			t.finalize(totalErrors, startTime)
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

func (t *Tailer) finalize(totalErrors int, start time.Time) {
	// Trigger Report and Email if we processed anything
	if totalErrors > 0 {
		reporter := NewReporter(t.Cfg, t.DB)
		// Cost calculation - simple estimate
		cost := float64(totalErrors) * 0.0001 
		
		reportPath, err := reporter.GenerateReport(t.Analyzer.Project, t.Path, totalErrors, cost)
		if err == nil {
			notifier := NewNotifier(t.Cfg)
			notifier.SendEmail(reportPath)
		}
	}
}

func (t *Tailer) Stop() {
	if t.tail != nil {
		t.tail.Stop()
	}
}

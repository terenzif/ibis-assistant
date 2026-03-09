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

func NewTailer(path string, cfg *config.Config, dbClient db.Executor, aiClient *ai.Client) *Tailer {
	return &Tailer{
		Path:     path,
		Cfg:      cfg,
		DB:       dbClient,
		AI:       aiClient,
		Analyzer: NewLogAnalyzer(path, cfg, dbClient, aiClient),
	}
}

func (t *Tailer) Tail(ctx context.Context) {
	logger.Info("Starting tail on: %s", t.Path)

	seekInfo := &tail.SeekInfo{Offset: 0, Whence: 0}

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
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case line, ok := <-tailer.Lines:
			if !ok {
				if len(batch) > 0 {
					t.Analyzer.ProcessBatch(ctx, batch)
				}
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
				// Small batch logic to prevent huge memory buildup
				if len(batch) >= 100 {
					t.Analyzer.ProcessBatch(ctx, batch)
					batch = nil
				}
			}
		case <-ticker.C:
			if len(batch) > 0 {
				t.Analyzer.ProcessBatch(ctx, batch)
				batch = nil
			}
		}
	}
}

func (t *Tailer) Stop() {
	if t.tail != nil {
		t.tail.Stop()
	}
}

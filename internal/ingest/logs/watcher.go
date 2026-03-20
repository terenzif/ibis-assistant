package logs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/deckonline/knowledge_mcp/internal/ai"
	"github.com/deckonline/knowledge_mcp/internal/config"
	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	Config  *config.Config
	DB      db.Executor
	AI      *ai.Client
	tailers map[string]*Tailer
	mu      sync.Mutex
	cancel  context.CancelFunc
}

func NewWatcher(cfg *config.Config, dbClient db.Executor, aiClient *ai.Client) *Watcher {
	return &Watcher{
		Config:  cfg,
		DB:      dbClient,
		AI:      aiClient,
		tailers: make(map[string]*Tailer),
	}
}

func (w *Watcher) Start(ctx context.Context) error {
	wCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel

	// Ensure logs dir exists
	err := os.MkdirAll(w.Config.LogsRoot, 0755)
	if err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	// Read existing files
	entries, err := os.ReadDir(w.Config.LogsRoot)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && (strings.HasSuffix(e.Name(), ".txt") || strings.HasSuffix(e.Name(), ".log")) {
				path := filepath.Join(w.Config.LogsRoot, e.Name())
				if !strings.HasPrefix(e.Name(), "report_") {
					w.addTailer(wCtx, path)
				}
			}
		}
	}

	go func() {
		defer watcher.Close()
		for {
			select {
			case <-wCtx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) {
					// Check if it's a log file and not a report
					name := filepath.Base(event.Name)
					if (strings.HasSuffix(name, ".txt") || strings.HasSuffix(name, ".log")) && !strings.HasPrefix(name, "report_") {
						w.addTailer(wCtx, event.Name)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Error("Watcher error: %v", err)
			}
		}
	}()

	logger.Info("Live monitoring log folder: %s", w.Config.LogsRoot)
	return watcher.Add(w.Config.LogsRoot)
}

func (w *Watcher) addTailer(ctx context.Context, path string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Normalize path
	absPath, _ := filepath.Abs(path)

	if _, exists := w.tailers[absPath]; exists {
		return // already tailing
	}
	t := NewTailer(ctx, absPath, w.Config, w.DB, w.AI)
	w.tailers[absPath] = t
	go t.Tail(ctx)
}

func (w *Watcher) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, t := range w.tailers {
		t.Stop()
	}
}

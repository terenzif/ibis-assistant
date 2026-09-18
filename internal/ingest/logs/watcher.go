package logs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/terenzif/ibis-assistant/internal/ai"
	"github.com/terenzif/ibis-assistant/internal/config"
	"github.com/terenzif/ibis-assistant/internal/db"
	"github.com/terenzif/ibis-assistant/internal/logger"
)

type Watcher struct {
	Config  *config.Config
	DB      db.Executor
	AI      *ai.Client
	tailers map[string]*Tailer
	mu      sync.Mutex
	cancel  context.CancelFunc
	fs      *fsnotify.Watcher
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

	if err := os.MkdirAll(w.Config.LogsRoot, 0755); err != nil {
		return err
	}

	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.fs = fsWatcher

	if err := w.addDirRecursive(wCtx, w.Config.LogsRoot); err != nil {
		_ = fsWatcher.Close()
		return err
	}

	go func() {
		defer fsWatcher.Close()
		for {
			select {
			case <-wCtx.Done():
				return
			case event, ok := <-fsWatcher.Events:
				if !ok {
					return
				}
				if !(event.Has(fsnotify.Create) || event.Has(fsnotify.Write) || event.Has(fsnotify.Rename)) {
					continue
				}
				info, err := os.Stat(event.Name)
				if err != nil {
					continue
				}
				if info.IsDir() {
					if event.Has(fsnotify.Create) {
						_ = w.addDirRecursive(wCtx, event.Name)
					}
					continue
				}
				name := filepath.Base(event.Name)
				if isWatchableLog(name) {
					w.addTailer(wCtx, event.Name)
				}
			case err, ok := <-fsWatcher.Errors:
				if !ok {
					return
				}
				logger.Error("Watcher error: %v", err)
			}
		}
	}()

	logger.Info("Live monitoring log folder (recursive): %s", w.Config.LogsRoot)
	return nil
}

func isWatchableLog(name string) bool {
	if strings.HasPrefix(name, "report_") {
		return false
	}
	return strings.HasSuffix(name, ".txt") || strings.HasSuffix(name, ".log")
}

func (w *Watcher) addDirRecursive(ctx context.Context, dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}

	return filepath.WalkDir(absDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			logger.Warn("Log watch walk error on %s: %v", path, walkErr)
			return nil
		}
		if d.IsDir() {
			if err := w.fs.Add(path); err != nil {
				logger.Warn("Failed to watch log dir %s: %v", path, err)
			} else {
			}
			return nil
		}
		if isWatchableLog(d.Name()) {
			w.addTailer(ctx, path)
		}
		return nil
	})
}

func (w *Watcher) addTailer(ctx context.Context, path string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	absPath, _ := filepath.Abs(path)
	if _, exists := w.tailers[absPath]; exists {
		return
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

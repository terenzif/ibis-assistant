package progress

import (
	"context"
	"sync"
	"time"
)

type ctxKey struct{}

// Reporter emits coarse progress for a long MCP tool call.
type Reporter func(done, total int, message string)

// With attaches a reporter to ctx. A nil reporter is a no-op.
func With(ctx context.Context, r Reporter) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, r)
}

// Report invokes the context reporter when present.
func Report(ctx context.Context, done, total int, message string) {
	r, _ := ctx.Value(ctxKey{}).(Reporter)
	if r != nil {
		r(done, total, message)
	}
}

// Throttle drops duplicate in-phase updates closer than minInterval.
// First report, phase-message changes, and completion always pass through.
func Throttle(r Reporter, minInterval time.Duration) Reporter {
	if r == nil {
		return nil
	}
	if minInterval <= 0 {
		return r
	}
	var mu sync.Mutex
	var last time.Time
	var lastMsg string
	return func(done, total int, message string) {
		now := time.Now()
		mu.Lock()
		defer mu.Unlock()
		complete := total > 0 && done >= total
		phaseChange := message != lastMsg
		if !last.IsZero() && !phaseChange && !complete && now.Sub(last) < minInterval {
			return
		}
		last = now
		lastMsg = message
		r(done, total, message)
	}
}

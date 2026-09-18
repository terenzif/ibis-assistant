package progress

import "context"

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

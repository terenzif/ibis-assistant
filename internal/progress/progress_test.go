package progress

import (
	"context"
	"testing"
)

func TestReportNoopWithoutReporter(t *testing.T) {
	Report(context.Background(), 1, 2, "x")
}

func TestReportInvokes(t *testing.T) {
	var got string
	ctx := With(context.Background(), func(done, total int, message string) {
		got = message
		if done != 1 || total != 3 {
			t.Fatalf("done/total = %d/%d", done, total)
		}
	})
	Report(ctx, 1, 3, "phase")
	if got != "phase" {
		t.Fatalf("got %q", got)
	}
}

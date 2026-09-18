package progress

import (
	"context"
	"testing"
	"time"
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

func TestThrottleDropsRapidSamePhase(t *testing.T) {
	var n int
	r := Throttle(func(done, total int, message string) {
		n++
	}, time.Hour)
	r(0, 10, "walk")
	r(1, 10, "walk")
	r(2, 10, "walk")
	r(10, 10, "walk")
	if n != 2 {
		t.Fatalf("got %d reports, want first+complete", n)
	}
	r(0, 10, "embed")
	if n != 3 {
		t.Fatalf("phase change should pass, got %d", n)
	}
}

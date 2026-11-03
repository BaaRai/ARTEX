package main

import (
	"context"
	"testing"

	"github.com/Autumn-27/artex/agent"
)

// The shutdown cause must survive the parent context being cancelled first;
// losing it would drop the diagnostic on every graceful stop.
func TestShutdownContextPreservesNamedCause(t *testing.T) {
	signalCtx, signalCancel := context.WithCancel(context.Background())
	ctx, shutdown := shutdownContext(signalCtx)
	defer shutdown(nil)
	signalCancel()
	<-ctx.Done()
	if code, _, _, ok := agent.AbortReason(ctx); !ok || code != "shutdown" {
		t.Fatalf("code=%q ok=%v, want shutdown", code, ok)
	}
}

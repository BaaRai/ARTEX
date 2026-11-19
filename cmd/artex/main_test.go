package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/config"
)

// The startup snapshot is meant to be safe to paste into an issue: no DSN
// password may survive redaction.
func TestConfigSnapshotRedactsDSNPassword(t *testing.T) {
	s := configSnapshot{
		Version:         "test",
		ConfigFileFound: true,
		PGDSN:           config.Redact("postgres://artex:super-secret@localhost:5432/artex?sslmode=disable"),
		PGDSNSource:     "environment variable ARTEX_PG_DSN",
		TrustedProxies:  []string{},
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if strings.Contains(out, "super-secret") {
		t.Fatalf("DSN password leaked into snapshot: %s", out)
	}
	if !strings.Contains(out, "****") {
		t.Fatalf("redaction marker missing from snapshot: %s", out)
	}
}

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

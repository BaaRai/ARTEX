package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/agent"
)

// A shell spawned after startup must not see the startup credentials; other
// variables still pass through.
func TestScrubSecretEnvHidesCredentialsFromSubprocesses(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh in PATH")
	}
	for _, k := range secretEnv {
		t.Setenv(k, "leak-"+k)
	}
	t.Setenv("ARTEX_SCRUB_KEEP", "kept")

	scrubSecretEnv()

	out, err := exec.Command(sh, "-c", "env").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range secretEnv {
		if strings.Contains(string(out), k+"=") {
			t.Errorf("%s still reaches subprocesses", k)
		}
	}
	if !strings.Contains(string(out), "ARTEX_SCRUB_KEEP=kept") {
		t.Errorf("non-secret variables must still pass through")
	}
}

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

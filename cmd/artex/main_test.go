package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/agent"
)

// A child process spawned after startup must not see the startup secrets, while
// other variables still pass through.
func TestScrubSecretEnvHidesSecretsFromChildren(t *testing.T) {
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
	env := "\n" + string(out)
	for _, k := range secretEnv {
		if strings.Contains(env, "\n"+k+"=") {
			t.Errorf("%s still reaches child processes", k)
		}
	}
	if !strings.Contains(env, "\nARTEX_SCRUB_KEEP=kept\n") {
		t.Errorf("non-secret variable did not reach the child process")
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

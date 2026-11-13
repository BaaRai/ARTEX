package server

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
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

	ScrubSecretEnv()

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

// New must apply the options it receives. An earlier version accepted
// ...Option and never called them, so the binary kept the secrets in the
// environment while the unit test above still passed.
func TestNewAppliesSecretScrubOption(t *testing.T) {
	// t.Setenv restores both after the test: the scrub unsets them for the rest
	// of this process, and later tests still need the DSN.
	if dsn := os.Getenv("ARTEX_PG_DSN"); dsn != "" {
		t.Setenv("ARTEX_PG_DSN", dsn)
	}
	t.Setenv("ARTEX_SECRET_KEY", os.Getenv("ARTEX_SECRET_KEY"))
	t.Setenv("ANTHROPIC_API_KEY", "scrub-probe")
	t.Setenv("OPENAI_API_KEY", "scrub-probe")

	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("no manager: %v", err)
	}
	t.Cleanup(func() { m.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir(), WithSecretScrub())

	if os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("OPENAI_API_KEY") != "" {
		t.Fatal("New did not apply WithSecretScrub: the secrets are still in the environment")
	}
}

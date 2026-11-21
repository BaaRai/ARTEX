//go:build linux

package server

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// isolationRootFixture is newExecRootFixture with the Bash shim installed, a key
// directory like /app/keys (0700, jwt.key and secret.key 0600) and a 0600 config.
type isolationRootFixture struct {
	*execRootFixture
	keyDir, config string
}

func newIsolationRootFixture(t *testing.T) *isolationRootFixture {
	t.Helper()
	f := newExecRootFixture(t) // skips without root
	if err := InstallBashShim(f.priv); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.SplitList(os.Getenv("PATH"))[0]) })
	keyDir := filepath.Join(f.data, "..", filepath.Base(f.data)+"-keys")
	if err := os.Mkdir(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(keyDir) })
	for _, name := range []string{"jwt.key", "secret.key"} {
		if err := os.WriteFile(filepath.Join(keyDir, name), []byte("key-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(keyDir, "..", filepath.Base(f.data)+"-config.json")
	if err := os.WriteFile(config, []byte(`{"database":{"dsn":"postgres://u:pw@h/db"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(config) })
	return &isolationRootFixture{execRootFixture: f, keyDir: keyDir, config: config}
}

func (f *isolationRootFixture) targets() IsolationTargets {
	return IsolationTargets{DataDir: f.data, KeyDir: f.keyDir, ConfigPath: f.config, DSN: os.Getenv("ARTEX_PG_DSN")}
}

// The deployment the self-test is for: root server, executor drop, shim, root-only
// keys and config. It passes, through the real Bash shim, and with ARTEX_PG_DSN set
// it also finds that the executor cannot open a session on that database without
// the password (PostgreSQL asks for one on TCP).
func TestIsolationSelfTestPassesAsRoot(t *testing.T) {
	f := newIsolationRootFixture(t)
	if os.Getenv("ARTEX_PG_DSN") == "" {
		t.Log("ARTEX_PG_DSN unset: the database check is not exercised")
	}
	if err := IsolationSelfTest(f.priv, f.targets()); err != nil {
		t.Fatalf("self-test on an isolated setup: %v", err)
	}
}

// A secret the executor can read fails the self-test, for the Bash path and the
// file tool path both.
func TestIsolationSelfTestDetectsReadableSecretAsRoot(t *testing.T) {
	f := newIsolationRootFixture(t)
	key := filepath.Join(f.keyDir, "jwt.key")
	if err := os.Chmod(f.keyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(key, 0o644); err != nil {
		t.Fatal(err)
	}
	err := IsolationSelfTest(f.priv, f.targets())
	if err == nil || !strings.Contains(err.Error(), "Bash can read "+key) || !strings.Contains(err.Error(), "the file tools can read "+key) {
		t.Fatalf("self-test with a world-readable key: %v", err)
	}
}

// Without the shim on PATH, "bash" is the real bash and runs as root: the self-test
// sees it.
func TestIsolationSelfTestDetectsMissingShimAsRoot(t *testing.T) {
	f := newExecRootFixture(t)
	err := IsolationSelfTest(f.priv, IsolationTargets{DataDir: f.data})
	if err == nil || !strings.Contains(err.Error(), "Bash runs as uid 0") {
		t.Fatalf("self-test without the Bash shim: %v", err)
	}
}

// A database that lets the server's user in without a password fails the self-test:
// the probe, as the executor, opens a session on it.
func TestIsolationSelfTestDetectsPasswordlessDatabaseAsRoot(t *testing.T) {
	f := newIsolationRootFixture(t)
	host, port := fakeTrustPG(t)
	tg := f.targets()
	tg.DSN = "postgres://artex:server-password@" + host + ":" + strconv.Itoa(int(port)) + "/artex"
	err := IsolationSelfTest(f.priv, tg)
	if err == nil || !strings.Contains(err.Error(), `database session as "artex"`) {
		t.Fatalf("self-test against a passwordless database: %v", err)
	}
}

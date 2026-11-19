//go:build linux

package server

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const sealHelperEnv = "ARTEX_SEAL_TEST_HELPER"

// sealHelper is the server's start sequence in miniature: seal, scrub, then report
// what the kernel environment block and a child process see. It runs in the
// re-executed image, where SealProcessEnv restores the values from the handoff.
func sealHelper() {
	SealProcessEnv()
	proc, err := os.ReadFile("/proc/self/environ")
	if err != nil {
		fmt.Printf("SEAL error %v\n", err)
		return
	}
	fmt.Printf("SEAL proc %x\n", proc)
	for _, k := range secretEnv {
		v, ok := os.LookupEnv(k)
		fmt.Printf("SEAL value %s %x %t\n", k, v, ok)
	}
	ScrubSecretEnv()
	child, err := exec.Command("env").Output()
	if err != nil {
		fmt.Printf("SEAL error %v\n", err)
		return
	}
	fmt.Printf("SEAL child %x\n", child)
	fmt.Println("SEAL done")
}

// TestSealedEnvironmentHandoff starts the test binary with the secrets in its
// environment, as the server is started. The kernel environment block of the sealed
// image and the environment of a child must hold none of the secret names, and the
// values restored in the process must equal the originals, including empty and
// multi-line ones.
func TestSealedEnvironmentHandoff(t *testing.T) {
	if os.Getenv(sealHelperEnv) == "1" {
		sealHelper()
		return
	}
	want := map[string]string{
		"ARTEX_PG_DSN":      "postgres://artex:p@ss@db.internal/artex?sslmode=disable\nsecond line\twith tab",
		"ARTEX_SECRET_KEY":  "",
		"ANTHROPIC_API_KEY": "sk-ant-test",
		"OPENAI_API_KEY":    "=leading-equals",
		// Proxy and endpoint URLs can carry credentials in their userinfo.
		"ARTEX_LLM_PROXY":                    "http://proxyuser:proxypass@proxy.internal:3128",
		"ARTEX_LLM_BASE_URL":                 "https://apiuser:apipass@llm.internal/v1",
		"ARTEX_ARCHIVE_S3_ACCESS_KEY_ID":     "AKIATESTONLY",
		"ARTEX_ARCHIVE_S3_SECRET_ACCESS_KEY": "s3-secret-test",
		"ARTEX_ARCHIVE_S3_SESSION_TOKEN":     "",
	}
	env := os.Environ()
	for k, v := range want {
		env = append(env, k+"="+v)
	}
	env = append(env, sealHelperEnv+"=1")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSealedEnvironmentHandoff$")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper failed: %v\n%s", err, out)
	}

	var proc, child []byte
	got := map[string]string{}
	seen := map[string]bool{}
	done := false
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "SEAL ") {
			continue
		}
		f := strings.Split(line, " ")
		switch f[1] {
		case "error":
			t.Fatalf("helper: %s", line)
		case "proc":
			proc = mustHex(t, f[2])
		case "child":
			child = mustHex(t, f[2])
		case "value":
			// SEAL value NAME <hex> <present>; the hex part is empty for an empty value.
			name, present := f[2], f[len(f)-1]
			hexPart := ""
			if len(f) == 5 {
				hexPart = f[3]
			}
			if present != "true" {
				t.Fatalf("%s is not set after sealing", name)
			}
			got[name] = string(mustHex(t, hexPart))
			seen[name] = true
		case "done":
			done = true
		}
	}
	if !done {
		t.Fatalf("helper did not finish:\n%s", out)
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s missing from the helper report", name)
		}
		if got[name] != want[name] {
			t.Errorf("%s restored as %q, want %q", name, got[name], want[name])
		}
	}
	for name := range want {
		if bytes.Contains(proc, []byte(name+"=")) {
			t.Errorf("kernel environment block still holds %s", name)
		}
		if bytes.Contains(child, []byte(name+"=")) {
			t.Errorf("child process environment holds %s", name)
		}
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

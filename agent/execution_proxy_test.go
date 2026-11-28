package agent

import (
	"github.com/Autumn-27/artex/accessaudit"
	"strings"
	"testing"
)

func TestExecutionProxyContextAndEnvironment(t *testing.T) {
	released := false
	ctx := WithRunInfo(t.Context(), RunInfo{TaskID: 81, IntentID: 4})
	ctx = accessaudit.WithProxyFactory(ctx, func(identity accessaudit.Identity) (string, string, func()) {
		if identity.TaskID != "81" || identity.Agent != "worker" || identity.ToolUseID != "" {
			t.Fatalf("identity=%+v", identity)
		}
		return "http://opaque:artex-pace@localhost:8788", "/public/ca.pem", func() { released = true }
	})
	// Even when the full-recording proxy/CA supplied by Manager are empty.
	env, release := executionProxyEnv(ctx, "worker", "", "")
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy"} {
		if values[key] != "http://opaque:artex-pace@localhost:8788" {
			t.Fatalf("%s lost execution credential", key)
		}
	}
	for _, key := range []string{"SSL_CERT_FILE", "CURL_CA_BUNDLE", "REQUESTS_CA_BUNDLE"} {
		if values[key] != "/public/ca.pem" {
			t.Fatalf("%s lost CA", key)
		}
	}
	release()
	if !released {
		t.Fatal("lease not released")
	}
}

package traffic

import (
	"bytes"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/accessaudit"
)

func executionAuditStore(t *testing.T, tr *Traffic) *accessaudit.Store {
	t.Helper()
	audit, err := accessaudit.Open(filepath.Join(t.TempDir(), "access.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { audit.Close() })
	tr.SetAccessAudit(audit)
	return audit
}
func executionEvents(t *testing.T, audit *accessaudit.Store) []accessaudit.Event {
	t.Helper()
	page, err := audit.Search(t.Context(), accessaudit.Query{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	return page.Events
}
func assertNoCapture(t *testing.T, tr *Traffic) {
	t.Helper()
	if count, err := tr.Count(); err != nil || count != 0 {
		t.Fatalf("full capture off: rows=%d err=%v", count, err)
	}
	err := filepath.WalkDir(filepath.Join(tr.dir, "_blobs"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return fmt.Errorf("unexpected captured blob")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Real subprocesses exercise proxy environment semantics, including HTTPS
// CONNECT, redirects, Python urllib and requests, without parsing commands into
// purported accesses. The fixture is local and deliberately uses NO_PROXY="".
func TestExecutionAuditBashCurlPython(t *testing.T) {
	for _, bin := range []string{"bash", "curl", "python3"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " unavailable")
		}
	}
	if err := exec.Command("python3", "-c", "import requests").Run(); err != nil {
		t.Skip("python requests unavailable")
	}
	tr, _, _ := startProxy(t)
	tr.SetCaptureEnabled(false)
	audit := executionAuditStore(t, tr)
	proxyURL, _, release := tr.ExecutionProxy(accessaudit.Identity{TaskID: "81", Agent: "work#test", RunID: "controlled-run"})
	defer release()
	var hits, leaks atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Proxy-Authorization") != "" {
			leaks.Add(1)
		}
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Set-Cookie", "body-capture-canary=secret")
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/done?token=private-canary", 302)
			return
		}
		if r.URL.Path == "/sse" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: body-capture-canary\n\n")
			return
		}
		fmt.Fprint(w, strings.Repeat("body-capture-canary", 100000))
	})
	target := httptest.NewServer(handler)
	defer target.Close()
	tlsTarget := httptest.NewTLSServer(handler)
	defer tlsTarget.Close()
	env := []string{}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		switch strings.ToLower(key) {
		case "http_proxy", "https_proxy", "all_proxy", "no_proxy", "ssl_cert_file", "curl_ca_bundle", "requests_ca_bundle":
			continue
		}
		env = append(env, value)
	}
	env = append(env, "HTTP_PROXY="+proxyURL, "HTTPS_PROXY="+proxyURL, "http_proxy="+proxyURL, "https_proxy="+proxyURL, "NO_PROXY=", "no_proxy=", "SSL_CERT_FILE="+tr.CACertPath(), "CURL_CA_BUNDLE="+tr.CACertPath(), "REQUESTS_CA_BUNDLE="+tr.CACertPath())
	scripts := []struct{ name, script string }{
		{"curl-http", `curl -fsSL --max-time 15 -H 'X-Artex-Task-ID: forged' "$1/redirect" >/dev/null`},
		{"curl-https", `curl -fsS --max-time 15 -d 'body-capture-canary' "$2/post" >/dev/null`},
		{"urllib-http", `python3 -c 'import urllib.request,sys; urllib.request.urlopen(sys.argv[1]+"/sse",timeout=15).read()' "$1"`},
		{"urllib-https", `python3 -c 'import urllib.request,sys; urllib.request.urlopen(sys.argv[1]+"/urllib",timeout=15).read()' "$2"`},
		{"requests-http", `python3 -c 'import requests,sys; requests.get(sys.argv[1]+"/requests",timeout=15).raise_for_status()' "$1"`},
		{"requests-https", `python3 -c 'import requests,sys; requests.get(sys.argv[1]+"/requests",timeout=15).raise_for_status()' "$2"`},
	}
	for _, tc := range scripts {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "bash", "-c", tc.script, "audit-test", target.URL, localhostURL(tlsTarget.URL))
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("subprocess: %v %s", err, out)
			}
		})
	}
	events := executionEvents(t, audit)
	responses, requests := 0, 0
	for _, event := range events {
		if event.Source != "execution_proxy" || event.TaskID != "81" || event.Agent != "work#test" || event.RunID != "controlled-run" || event.Tool != "Bash" || event.ToolUseID != "" {
			t.Fatalf("incorrect provenance: %+v", event)
		}
		if strings.Contains(event.URL, "private-canary") {
			t.Fatal("query value leaked")
		}
		if event.Outcome == "response_headers" {
			responses++
		}
		if event.Outcome == "proxy_request" {
			requests++
		}
	}
	if responses != 7 || requests != 7 || hits.Load() != 7 || leaks.Load() != 0 {
		t.Fatalf("responses=%d requests=%d origin_hits=%d auth_leaks=%d", responses, requests, hits.Load(), leaks.Load())
	}
	assertNoCapture(t, tr)
	// After release the same credential cannot claim task identity.
	release()
	client := proxyClient(t, tr, proxyURL)
	resp, err := client.Get(target.URL + "/revoked")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	for _, event := range executionEvents(t, audit) {
		if strings.Contains(event.URL, "/revoked") && (event.TaskID != "" || event.Source == "execution_proxy") {
			t.Fatal("revoked credential retained identity")
		}
	}
	// Re-enable full capture: it remains independently functional.
	tr.SetCaptureEnabled(true)
	resp, err = client.Get(target.URL + "/capture-on")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if count, err := tr.Count(); err != nil || count != 1 {
		t.Fatalf("capture on: rows=%d err=%v", count, err)
	}
}

func TestExecutionAuditToggleDuringResponse(t *testing.T) {
	tr, _, addr := startProxy(t)
	tr.SetCaptureEnabled(false)
	audit := executionAuditStore(t, tr)
	entered, finish := make(chan struct{}), make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-finish
		fmt.Fprint(w, "do-not-capture-this-in-flight-body")
	}))
	defer target.Close()
	done := make(chan error, 1)
	go func() {
		resp, err := proxyClient(t, tr, addr).Post(target.URL, "text/plain", bytes.NewBufferString("private-request"))
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		done <- err
	}()
	<-entered
	tr.SetCaptureEnabled(true)
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertNoCapture(t, tr)
	if len(executionEvents(t, audit)) != 1 {
		t.Fatal("metadata missing during toggle")
	}
}

func TestExecutionAuditUpstreamAndUnknownCredentials(t *testing.T) {
	tr, _, _ := startProxy(t)
	tr.SetCaptureEnabled(false)
	audit := executionAuditStore(t, tr)
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("execution credential leaked upstream")
		}
		if r.URL.Hostname() != "never-resolve.invalid" {
			t.Error("upstream destination lost")
		}
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	if err := tr.SetUpstreamProxy(upstream.URL); err != nil {
		t.Fatal(err)
	}
	proxyURL, _, release := tr.ExecutionProxy(accessaudit.Identity{TaskID: "81"})
	defer release()
	client := proxyClient(t, tr, proxyURL)
	resp, err := client.Get("http://never-resolve.invalid/upstream")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if hits.Load() != 1 {
		t.Fatal("configured upstream was bypassed")
	}
	// Unknown credentials and task headers are not trusted attribution.
	u, _ := url.Parse(proxyURL)
	u.User = url.UserPassword("invented-task-81", "artex-pace")
	req, _ := http.NewRequest("GET", "http://never-resolve.invalid/forged", nil)
	req.Header.Set("X-Artex-Task-ID", "81")
	resp, err = proxyClient(t, tr, u.String()).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, event := range executionEvents(t, audit) {
		if strings.Contains(event.URL, "/forged") && event.TaskID != "" {
			t.Fatal("forged identity accepted")
		}
	}
	assertNoCapture(t, tr)
}

func TestExecutionAuditWebSocketToggle(t *testing.T) {
	tr, _, _ := startProxy(t)
	audit := executionAuditStore(t, tr)
	proxyURL, _, release := tr.ExecutionProxy(accessaudit.Identity{TaskID: "81"})
	defer release()
	target := echoWSServer(t)
	conn, _, err := wsDialer(t, tr, proxyURL).Dial("wss://"+strings.TrimPrefix(localhostURL(target.URL), "https://")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWS(conn)
	send := func(message string) {
		t.Helper()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
	}
	send("recorded-before-toggle")
	// Wait for async frame hooks before comparing the stored stream length.
	waitFor(t, "initial frames", func() bool {
		records := streamExchanges(t, tr, "%websocket%")
		return len(records) == 1 && len(allMessages(t, tr, records[0].id)) >= 2
	})
	rows := streamExchanges(t, tr, "%websocket%")
	before := len(allMessages(t, tr, rows[0].id))
	tr.SetCaptureEnabled(false)
	send("off-canary")
	tr.SetCaptureEnabled(true)
	send("old-stream-stays-off-canary")
	if after := len(allMessages(t, tr, rows[0].id)); after != before {
		t.Fatalf("old stream resumed recording: before=%d after=%d", before, after)
	}
	for _, event := range executionEvents(t, audit) {
		if event.TaskID != "81" {
			t.Fatal("WebSocket lost execution context")
		}
	}
}

func TestExecutionAuditRejectsUntrustedUpstreamTLS(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(204) }))
	defer target.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	tr, err := OpenAuditProxy(t.TempDir(), address, false)
	if err != nil {
		t.Fatal(err)
	}
	tr.SetCaptureEnabled(false)
	audit := executionAuditStore(t, tr)
	go func() { _ = tr.Start() }()
	t.Cleanup(func() { tr.proxy.Close(); tr.Close() })
	waitFor(t, "strict TLS proxy", func() bool {
		c, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			c.Close()
		}
		return err == nil
	})
	client := proxyClient(t, tr, tr.ProxyAddr())
	client.Timeout = 5 * time.Second
	response, err := client.Get(localhostURL(target.URL) + "/must-not-reach-target")
	if err == nil {
		response.Body.Close()
		if response.StatusCode < 500 {
			t.Fatalf("untrusted upstream accepted: status=%d", response.StatusCode)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("untrusted TLS target received HTTP request")
	}
	for _, event := range executionEvents(t, audit) {
		if event.Outcome == "response_headers" {
			t.Fatal("invented target response after TLS failure")
		}
	}
	if audit.Failures() != 0 {
		t.Fatal("CONNECT was misrecorded as an invalid resource URL")
	}
	assertNoCapture(t, tr)
}

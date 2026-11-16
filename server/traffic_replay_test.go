package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/traffic"
)

// replayServer wires a Server like ingestServer. With traffic off,
// s.m.Traffic() is nil. With trafficOn but not live, the store is open and the
// proxy is not listening. With live, NewManager opens the store and starts the
// recording proxy on a free loopback port, as in production; the proxy has no
// exported stop, so it keeps listening until the test binary exits.
func replayServer(t *testing.T, trafficOn, live bool) (*Server, func(body string) *httptest.ResponseRecorder) {
	t.Helper()
	skipWithoutDSN(t)
	proxyAddr := ""
	if live {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		proxyAddr = ln.Addr().String()
		ln.Close()
	}
	m, err := NewManager(t.TempDir(), proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	if live {
		if m.traffic == nil {
			t.Fatal("NewManager did not open the traffic store")
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			c, err := net.Dial("tcp", proxyAddr)
			if err == nil {
				c.Close()
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("proxy did not listen on %s: %v", proxyAddr, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	} else if trafficOn {
		if m.traffic, err = traffic.Open(filepath.Join(m.dir, "traffic"), ":0"); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	s.archiveWG.Wait()
	<-s.side.done
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	return s, func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/traffic/replay", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
}

func TestTrafficReplayRouteTrafficOff(t *testing.T) {
	_, replay := replayServer(t, false, false)
	if w := replay(`{"method":"GET","url":"http://example.com/"}`); w.Code != 503 {
		t.Fatalf("traffic off = %d %s, want 503", w.Code, w.Body)
	}
}

func TestTrafficReplayRouteValidation(t *testing.T) {
	_, replay := replayServer(t, true, false)
	for name, c := range map[string]struct {
		body string
		want int
	}{
		"bad json":     {`{"method":`, 400},
		"CONNECT":      {`{"method":"CONNECT","url":"http://example.com/"}`, 400},
		"ftp url":      {`{"method":"GET","url":"ftp://example.com/x"}`, 400},
		"no host":      {`{"method":"GET","url":"http:///x"}`, 400},
		"bad base64":   {`{"method":"POST","url":"http://example.com/","body":"%%%","body_base64":true}`, 400},
		"header CRLF":  {`{"method":"GET","url":"http://example.com/","headers":[{"name":"X-A","value":"a\r\nb"}]}`, 400},
		"too large":    {`{"method":"POST","url":"http://example.com/","body":"` + strings.Repeat("a", 17<<20) + `"}`, 413},
		"proxy down":   {`{"method":"GET","url":"http://127.0.0.1:1/"}`, 502},
		"body too big": {`{"method":"POST","url":"http://example.com/","body":"` + base64.StdEncoding.EncodeToString(make([]byte, 9<<20)) + `","body_base64":true}`, 413},
	} {
		if w := replay(c.body); w.Code != c.want {
			t.Errorf("%s: %d %s, want %d", name, w.Code, strings.TrimSpace(w.Body.String()), c.want)
		}
	}
}

func TestTrafficReplayRouteEndToEnd(t *testing.T) {
	s, replay := replayServer(t, true, true)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Seen", r.Method+" "+r.Header.Get("X-Custom"))
		_, _ = w.Write(append([]byte("got:"), b...))
	}))
	t.Cleanup(target.Close)
	url := target.URL + "/replayed?x=1"
	req, _ := json.Marshal(map[string]any{
		"method": "PUT", "url": url,
		"headers": []map[string]string{{"name": "X-Custom", "value": "edited"}},
		"body":    base64.StdEncoding.EncodeToString([]byte("payload")), "body_base64": true,
	})
	w := replay(string(req))
	if w.Code != 200 {
		t.Fatalf("replay = %d %s", w.Code, w.Body)
	}
	var res struct {
		Status     int                    `json:"status"`
		Headers    []traffic.ReplayHeader `json:"headers"`
		Raw        string                 `json:"raw"`
		DurationMS int64                  `json:"duration_ms"`
		Truncated  bool                   `json:"truncated"`
		Recorded   bool                   `json:"recorded"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || res.Truncated || !res.Recorded || !strings.Contains(res.Raw, "X-Seen: PUT edited") || !strings.HasSuffix(res.Raw, "got:payload") {
		t.Fatalf("result %+v", res)
	}
	seen := false
	for _, h := range res.Headers {
		seen = seen || (h.Name == "X-Seen" && h.Value == "PUT edited")
	}
	if !seen {
		t.Fatalf("headers %+v lack X-Seen", res.Headers)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		if err := s.m.Traffic().DB().QueryRow(`SELECT COUNT(*) FROM exchanges WHERE method='PUT' AND url=?`, url).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("replayed exchange not recorded (%d rows)", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

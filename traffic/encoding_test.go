package traffic

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

const compressedPage = "<html><body>Welcome admin. root:x:0:0:root:/root:/bin/bash</body></html>"

// TestCompressedResponseIsSearchable records a gzip response through the real
// proxy, as a Go client (nuclei, httpx, ffuf) fetches it: Accept-Encoding gzip
// is sent by default and the proxy keeps the body as it crossed the wire. Body
// search and fact evidence must still see its text.
func TestCompressedResponseIsSearchable(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			io.WriteString(w, compressedPage)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		io.WriteString(gz, compressedPage)
		gz.Close()
	}))
	defer target.Close()
	tr, _, proxyURL := startProxy(t)
	pu, _ := url.Parse(proxyURL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	resp, err := client.Get(target.URL + "/passwd")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	host := strings.TrimPrefix(target.URL, "http://")
	var rows []ExchangeMeta
	for deadline := time.Now().Add(5 * time.Second); len(rows) == 0 && time.Now().Before(deadline); {
		rows, _ = tr.query(host, "", "", 0, 10)
		time.Sleep(10 * time.Millisecond)
	}
	if len(rows) != 1 {
		t.Fatalf("exchange not recorded: %d rows", len(rows))
	}
	if _, r, _ := tr.Get(rows[0].ID); !strings.Contains(r, "Content-Encoding: gzip") {
		t.Fatalf("test needs a gzip capture, got response %q", r)
	}

	hits, err := tr.query(host, "", "root:x:0", 0, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("body search for text of a gzip response: %d rows, err=%v; want 1", len(hits), err)
	}
	id, ok, err := tr.ResponseContaining([]string{"root:x:0:0:root"}, 0, time.Now().Unix()+1)
	if err != nil || !ok || id != rows[0].ID {
		t.Fatalf("ResponseContaining on a gzip response = (%q, %v, %v), want (%q, true, nil)", id, ok, err, rows[0].ID)
	}
}

func TestCompressedBodyIndexEncodings(t *testing.T) {
	var br, zl bytes.Buffer
	bw := brotli.NewWriter(&br)
	io.WriteString(bw, compressedPage)
	bw.Close()
	zw := zlib.NewWriter(&zl)
	io.WriteString(zw, compressedPage)
	zw.Close()

	for _, tc := range []struct {
		enc  string
		body []byte
	}{{"br", br.Bytes()}, {"deflate", zl.Bytes()}} {
		t.Run(tc.enc, func(t *testing.T) {
			tr, _ := openTraffic(t)
			f := newFlow("api.example.com", "GET", "/"+tc.enc, nil, tc.body)
			f.Response.Header.Set("Content-Type", "text/html")
			f.Response.Header.Set("Content-Encoding", tc.enc)
			tr.record(f)
			hits, err := tr.query("api.example.com", "", "root:x:0", 0, 10)
			if err != nil || len(hits) != 1 {
				t.Fatalf("body search for text of a %s response: %d rows, err=%v; want 1", tc.enc, len(hits), err)
			}
		})
	}
}

// TestDecodeBodyIsBounded keeps a compression bomb from a target from being
// expanded beyond the index limit.
func TestDecodeBodyIsBounded(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write(make([]byte, 4*maxIndexBody))
	gz.Close()
	out, ok := decodeBody(http.Header{"Content-Encoding": {"gzip"}}, buf.Bytes(), maxIndexBody)
	if !ok || len(out) != maxIndexBody {
		t.Fatalf("decodeBody returned %d bytes, ok=%v; want %d", len(out), ok, maxIndexBody)
	}
}

// TestTruncatedCompressedBodyIsIndexed covers a capture cut short mid-stream:
// the text decoded before the cut is still indexed.
func TestTruncatedCompressedBodyIsIndexed(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	io.WriteString(gz, compressedPage)
	for i := range 4096 {
		fmt.Fprintf(gz, "row %d\n", i)
	}
	gz.Close()
	cut := buf.Bytes()[:buf.Len()/2]

	tr, _ := openTraffic(t)
	f := newFlow("api.example.com", "GET", "/cut", nil, cut)
	f.Response.Header.Set("Content-Type", "text/html")
	f.Response.Header.Set("Content-Encoding", "gzip")
	tr.record(f)
	hits, err := tr.query("api.example.com", "", "root:x:0", 0, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("body search for text of a truncated gzip response: %d rows, err=%v; want 1", len(hits), err)
	}
}

// TestIndexBodySkipsBinaryContentType keeps an encoded body with a binary
// content type out of the index without decoding it.
func TestIndexBodySkipsBinaryContentType(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	io.WriteString(gz, compressedPage)
	gz.Close()
	h := http.Header{"Content-Encoding": {"gzip"}, "Content-Type": {"image/png"}}
	if text, ok := indexBody(h, buf.Bytes()); ok || text != "" {
		t.Fatalf("indexBody on an image/png body = (%q, %v), want (\"\", false)", text, ok)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"
)

// newProxy builds a reverse proxy that forwards to upstream, rewriting auth and
// identity on the way. The token is injected as a Bearer credential and never
// appears in any log line or response body.
func newProxy(upstream *url.URL, token string) *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		// FlushInterval -1 disables buffering so SSE chunks pass through as the
		// upstream flushes them.
		FlushInterval: -1,
		Director: func(req *http.Request) {
			req.URL.Scheme = upstream.Scheme
			req.URL.Host = upstream.Host
			req.Host = upstream.Host
			// Path and query are left as received so /v1/messages forwards verbatim.

			rewriteAuth(req.Header, token)
			mergeBeta(req.Header)
			rewriteBody(req)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// err carries the upstream host and network cause, never the token.
			log.Printf("llmshim: upstream error for %s %s: %v", r.Method, r.URL.Path, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, `{"error":"upstream unreachable"}`)
		},
	}
	return proxy
}

// rewriteBody injects the Claude Code identity into a JSON request body. A body
// that does not parse as a JSON object is forwarded byte-for-byte. req.Body is
// always left readable for the downstream transport.
func rewriteBody(req *http.Request) {
	if req.Body == nil {
		return
	}
	orig, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		// Could not read the incoming body; forward an empty body rather than
		// a half-consumed reader.
		req.Body = io.NopCloser(bytes.NewReader(nil))
		req.ContentLength = 0
		return
	}

	var m map[string]any
	if json.Unmarshal(orig, &m) == nil {
		injectIdentity(m)
		if nb, err := json.Marshal(m); err == nil {
			setBody(req, nb)
			return
		}
	}

	// Non-JSON (or re-marshal failure): restore the original bytes unmodified.
	setBody(req, orig)
}

func setBody(req *http.Request, b []byte) {
	req.Body = io.NopCloser(bytes.NewReader(b))
	req.ContentLength = int64(len(b))
	req.Header.Set("Content-Length", strconv.Itoa(len(b)))
}

// statusRecorder captures the response status for the request logger while
// preserving Flusher behaviour needed for streaming responses.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logRequests logs method, path, status and latency only — never headers,
// body, or the token.
func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r)
		log.Printf("llmshim: %s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start))
	})
}

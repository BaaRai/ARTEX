package accessaudit

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync"
)

type transport struct {
	base http.RoundTripper
	tool string
}

// Transport goes INSIDE the governor so cache hits are not network evidence.
func Transport(base http.RoundTripper, tool string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return transport{base, tool}
}
func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	id := rand.Text()
	e := Event{Source: "native_http", Outcome: "transport_attempt", Tool: t.tool, Method: req.Method, URL: req.URL.String(), RequestID: id}
	Record(req.Context(), e)
	resp, err := t.base.RoundTrip(req)
	e.Outcome = "transport_error"
	if err == nil && resp != nil {
		e.Outcome = "response_headers"
		e.Status = resp.StatusCode
	}
	Record(req.Context(), e)
	return resp, err
}

type fetchKey struct{}
type fetchTrace struct {
	mu      sync.Mutex
	raw, id string
}

// TraceFetch observes Norma's real network callbacks without replacing its
// client, TLS, redirect or pinned-dial policy. Tool output is never evidence.
func TraceFetch(ctx context.Context, u *url.URL) context.Context {
	st := &fetchTrace{raw: u.String(), id: rand.Text()}
	ctx = context.WithValue(ctx, fetchKey{}, st)
	emit := func(outcome string) {
		st.mu.Lock()
		e := Event{Source: "webfetch_trace", Outcome: outcome, Tool: "WebFetch", Method: "GET", URL: st.raw, RequestID: st.id}
		st.mu.Unlock()
		Record(ctx, e)
	}
	Record(ctx, Event{Source: "webfetch_trace", Outcome: "tool_attempt", Tool: "WebFetch", Method: "GET", URL: u.String()})
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GetConn: func(string) { st.mu.Lock(); st.id = rand.Text(); st.mu.Unlock() },
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			if i.Err != nil {
				emit("write_error")
			} else {
				emit("request_written")
			}
		},
		GotFirstResponseByte: func() { emit("response_started") },
	})
}

// FetchRedirect runs only after the existing redirect gate allows the hop.
func FetchRedirect(req *http.Request) {
	if st, ok := req.Context().Value(fetchKey{}).(*fetchTrace); ok {
		st.mu.Lock()
		st.raw = req.URL.String()
		st.mu.Unlock()
	}
}

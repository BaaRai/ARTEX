package accessaudit

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type failTransport struct{}

func (failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("secret URL must not be recorded")
}
func TestHTTPRedirectAttributionAndFailure(t *testing.T) {
	s := testStore(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first" {
			http.Redirect(w, r, "/last?token=hidden", 302)
			return
		}
		w.WriteHeader(204)
	}))
	defer target.Close()
	ctx := WithIdentity(WithStore(t.Context(), s), func(i *Identity) { i.TaskID = "7"; i.RunID = "run-1" })
	req, _ := http.NewRequestWithContext(ctx, "GET", target.URL+"/first", nil)
	resp, err := (&http.Client{Transport: Transport(nil, "test")}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	_, err = Transport(failTransport{}, "failed").RoundTrip(req)
	if err == nil {
		t.Fatal("error swallowed")
	}
	p, err := s.Search(t.Context(), window())
	if err != nil || len(p.Events) != 6 {
		t.Fatal(p, err)
	}
	counts := map[string]int{}
	for _, e := range p.Events {
		counts[e.Outcome]++
		if e.TaskID != "7" || e.RunID != "run-1" {
			t.Fatal(e)
		}
	}
	if counts["transport_attempt"] != 3 || counts["response_headers"] != 2 || counts["transport_error"] != 1 {
		t.Fatal(counts)
	}
}

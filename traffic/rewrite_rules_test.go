package traffic

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Autumn-27/artex/traffic/rewrite"
)

// The proxy stores the compiled rewrite rules and does not apply them. With a
// snapshot whose rules match every exchange, what the target receives, what the
// client receives and what is recorded are the same as with no rule at all.
func TestRewriteRulesStoredNotApplied(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r.Method+" "+r.RequestURI+" X-Client="+r.Header.Get("X-Client")+" "+string(body))
		mu.Unlock()
		w.Header().Set("X-Target", "t")
		w.WriteHeader(http.StatusTeapot)
		io.WriteString(w, "target body")
	}))
	t.Cleanup(target.Close)

	set := rewrite.Compile([]rewrite.Rule{
		{ID: 1, Name: "request", Enabled: true, Phase: rewrite.PhaseRequest, Actions: []rewrite.Action{
			{Type: rewrite.ActionSetMethod, Value: "PUT"},
			{Type: rewrite.ActionSetPathQuery, Value: "/rewritten"},
			{Type: rewrite.ActionSetHeader, Name: "X-Client", Value: "rewritten"},
			{Type: rewrite.ActionSetBody, Value: "rewritten"}}},
		{ID: 2, Name: "response", Enabled: true, Phase: rewrite.PhaseResponse, Actions: []rewrite.Action{
			{Type: rewrite.ActionSetStatus, Value: "200"},
			{Type: rewrite.ActionSetHeader, Name: "X-Rewritten", Value: "1"},
			{Type: rewrite.ActionSetBody, Value: "rewritten"}}},
	}, rewrite.Options{})
	if set.Len(rewrite.PhaseRequest) != 1 || set.Len(rewrite.PhaseResponse) != 1 || len(set.LoadErrors()) != 0 {
		t.Fatalf("the snapshot must hold both rules: %v", set.LoadErrors())
	}

	// canon makes a recorded message comparable: the recorder writes headers in
	// map order, and the target dates its response.
	canon := func(raw string) string {
		head, body, _ := strings.Cut(raw, "\n\n")
		lines := strings.Split(head, "\n")
		sort.Strings(lines[1:])
		lines = slices.DeleteFunc(lines, func(l string) bool { return strings.HasPrefix(l, "Date: ") })
		return strings.Join(lines, "\n") + "\n\n" + body
	}
	// exchange sends one request through a fresh proxy holding set and returns
	// what the client received and what the proxy recorded.
	exchange := func(set *rewrite.Set) (client, recorded string) {
		tr, _, proxyURL := startProxy(t)
		if tr.RewriteRules() != nil {
			t.Fatal("Open published rewrite rules")
		}
		tr.SetRewriteRules(set)
		if tr.RewriteRules() != set {
			t.Fatal("RewriteRules must return the snapshot last published")
		}
		req, err := http.NewRequest("POST", target.URL+"/path?q=1", strings.NewReader("client body"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Client", "c")
		resp, err := proxyClient(t, tr, proxyURL).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		client = resp.Status + " X-Target=" + resp.Header.Get("X-Target") + " X-Rewritten=" + resp.Header.Get("X-Rewritten") + " " + string(body)
		reqText, respText, err := tr.Get(onlyExchangeID(t, tr))
		if err != nil {
			t.Fatal(err)
		}
		return client, canon(reqText) + "\n--\n" + canon(respText)
	}

	baseClient, baseRecorded := exchange(nil)
	client, recorded := exchange(set)
	if want := "418 I'm a teapot X-Target=t X-Rewritten= target body"; baseClient != want || client != want {
		t.Fatalf("client received %q without rules and %q with rules, want %q", baseClient, client, want)
	}
	if recorded != baseRecorded {
		t.Fatalf("the recorded exchange differs with rules stored:\n without: %s\n with:    %s", baseRecorded, recorded)
	}
	if !strings.Contains(recorded, "client body") || !strings.Contains(recorded, "target body") || strings.Contains(recorded, "rewritten") {
		t.Fatalf("recorded exchange: %s", recorded)
	}
	want := "POST /path?q=1 X-Client=c client body"
	if len(seen) != 2 || seen[0] != want || seen[1] != want {
		t.Fatalf("the target received %q, want %q twice", seen, want)
	}
}

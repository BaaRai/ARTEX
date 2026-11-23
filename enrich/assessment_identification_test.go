package enrich

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Autumn-27/artex/egress"
	"github.com/Autumn-27/artex/intercept"
)

func TestAssessmentEnrichmentUserAgent(t *testing.T) {
	const taggedUA = "ARTEX-Assessment/1.0 (engagement=FS-17)"
	const legacyUA = "LegacyFixture/1.2"
	prev := egress.CurrentProfile()
	t.Cleanup(func() { egress.SetProfile(prev) })
	t.Setenv("ARTEX_TARGET_USER_AGENT", legacyUA)
	legacy, ok := egress.ProfileByName("balanced")
	if !ok {
		t.Fatal("balanced noise profile unavailable")
	}

	type observation struct{ path, ua string }
	received := make(chan observation, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- observation{r.URL.Path, r.UserAgent()}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/follow", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	// The wire behavior only needs the real HTTP client. Use the existing
	// unreachable database fixture for the persistence step.
	e := &Engine{as: deadAssetStore(t), client: buildClient(nil)}

	for _, tc := range []struct {
		path, id, configuredUA, want string
		legacy                       bool
	}{
		{path: "/tagged", id: "42", configuredUA: taggedUA, want: taggedUA},
		{path: "/untagged", id: "43", want: "Go-http-client/1.1"},
		{path: "/base", id: "44", configuredUA: "ARTEX-Assessment/1.0", want: "ARTEX-Assessment/1.0"},
		{path: "/tagged-with-legacy", id: "42", configuredUA: taggedUA, legacy: true, want: taggedUA},
		{path: "/untagged-with-legacy", id: "43", legacy: true, want: legacyUA},
		{path: "/redirect", id: "42", configuredUA: taggedUA, want: taggedUA},
	} {
		t.Run(tc.path[1:], func(t *testing.T) {
			profile := egress.Profile{Name: "off"}
			if tc.legacy {
				profile = legacy
			}
			egress.SetProfile(profile)
			ctx := intercept.WithTargetScope(context.Background(), intercept.TargetScope{
				TaskID: tc.id, AssessmentUserAgent: tc.configuredUA,
			})
			e.doHTTP(ctx, 1, srv.URL+tc.path)
			select {
			case got := <-received:
				if want := (observation{tc.path, tc.want}); got != want {
					t.Errorf("observed %v, want %v", got, want)
				}
			default:
				t.Fatal("local server received no request")
			}
			if len(received) != 0 {
				t.Fatalf("unexpected additional requests: %d", len(received))
			}
		})
	}
}

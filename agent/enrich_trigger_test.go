package agent

import (
	"context"
	"testing"
)

// fakeEnrich records the triggers the tool layer hands to the enrichment engine.
// Before insert_assets called the engine, the ToolSet field was assigned but
// never read, so no asset was ever resolved or probed (see triggerEnrichment).
type fakeEnrich struct {
	resolved []string
	probed   []string
	probeCtx []context.Context
}

func (f *fakeEnrich) ResolveDomain(id int64, host string) {
	f.resolved = append(f.resolved, host)
}

func (f *fakeEnrich) ProbeSite(ctx context.Context, id int64, url string) {
	f.probed = append(f.probed, url)
	f.probeCtx = append(f.probeCtx, ctx)
}

// TestInsertAssetsTriggersEnrichment pins which inserted asset types start an
// async auto-completion job: a domain resolves, an HTTP service is probed, and
// everything the engine cannot act on is left alone.
func TestInsertAssetsTriggersEnrichment(t *testing.T) {
	cases := []struct {
		name       string
		typ        string
		item       assetInputItem
		wantDomain string
		wantProbe  string
	}{
		{name: "root_domain resolves", typ: "root_domain", item: assetInputItem{Domain: "example.com"}, wantDomain: "example.com"},
		{name: "subdomain resolves", typ: "subdomain", item: assetInputItem{Domain: "api.example.com"}, wantDomain: "api.example.com"},
		{name: "domain is trimmed", typ: "subdomain", item: assetInputItem{Domain: "  api.example.com  "}, wantDomain: "api.example.com"},
		{name: "http service is probed", typ: "service", item: assetInputItem{URL: "https://example.com:8443/login"}, wantProbe: "https://example.com:8443/login"},
		{name: "url is trimmed", typ: "service", item: assetInputItem{URL: " https://example.com/ "}, wantProbe: "https://example.com/"},
		// A non-HTTP service has no url: nothing to probe.
		{name: "non-http service is skipped", typ: "service", item: assetInputItem{ServiceName: "ssh", Port: 22}},
		{name: "ip is skipped", typ: "ip", item: assetInputItem{IP: "203.0.113.7"}},
		{name: "app is skipped", typ: "app", item: assetInputItem{AppName: "portal"}},
		{name: "endpoint is skipped", typ: "endpoint", item: assetInputItem{URL: "https://example.com/api", Method: "GET"}},
		{name: "blank domain is skipped", typ: "subdomain", item: assetInputItem{Domain: "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enr := &fakeEnrich{}
			ts := NewToolSet(nil, "worker")
			ts.SetEnrich(enr)
			ctx := context.Background()
			ts.triggerEnrichment(ctx, tc.typ, 7, tc.item)

			if tc.wantDomain != "" {
				if len(enr.resolved) != 1 || enr.resolved[0] != tc.wantDomain {
					t.Fatalf("resolved %v, want [%s]", enr.resolved, tc.wantDomain)
				}
			} else if len(enr.resolved) != 0 {
				t.Fatalf("resolved %v, want no DNS job", enr.resolved)
			}
			if tc.wantProbe != "" {
				if len(enr.probed) != 1 || enr.probed[0] != tc.wantProbe {
					t.Fatalf("probed %v, want [%s]", enr.probed, tc.wantProbe)
				}
				// The probe must keep the caller's context: it carries the target
				// scope the engine gates the dial on, so a replaced or background
				// context would make the probe refuse to dial.
				if len(enr.probeCtx) != 1 || enr.probeCtx[0] != ctx {
					t.Fatalf("probe did not receive the caller context")
				}
			} else if len(enr.probed) != 0 {
				t.Fatalf("probed %v, want no HTTP probe", enr.probed)
			}
		})
	}
}

// TestEnrichmentTriggerWithoutEngineIsNoop pins that the trigger is optional:
// tool sets built outside a worker run carry no engine, and that must not panic.
func TestEnrichmentTriggerWithoutEngineIsNoop(t *testing.T) {
	ts := NewToolSet(nil, "worker")
	ts.triggerEnrichment(context.Background(), "root_domain", 1, assetInputItem{Domain: "example.com"})
	ts.triggerEnrichment(context.Background(), "service", 2, assetInputItem{URL: "https://example.com/"})

	// A write that produced no id (a no-op upsert path) must not be triggered either.
	enr := &fakeEnrich{}
	ts.SetEnrich(enr)
	ts.triggerEnrichment(context.Background(), "root_domain", 0, assetInputItem{Domain: "example.com"})
	ts.triggerEnrichment(context.Background(), "service", -1, assetInputItem{URL: "https://example.com/"})
	if len(enr.resolved) != 0 || len(enr.probed) != 0 {
		t.Fatalf("triggered without a persisted asset id: resolved %v probed %v", enr.resolved, enr.probed)
	}
}

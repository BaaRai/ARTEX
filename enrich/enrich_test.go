package enrich

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestDetectRecordsWAFFromHeaders pins the wiring the asset graph depends on: a
// Cloudflare response must land a Cloudflare verdict, not just "a WAF".
func TestDetectRecordsWAFFromHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("cf-ray", "8a1b2c3d4e5f6789-SJC")
	h.Set("Server", "cloudflare")

	techs, extra := detect(h, nil, "https://example.test/", 200)
	if len(techs) == 0 {
		t.Error("no technologies detected from a cloudflare banner")
	}
	raw, ok := extra["waf"]
	if !ok {
		t.Fatalf("no waf verdict in %v", extra)
	}
	// Round-trip through JSON: the bag is stored as jsonb, so the shape must
	// survive encoding rather than only existing in memory.
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("waf verdict is not encodable: %v", err)
	}
	var obs []wafObservation
	if err := json.Unmarshal(b, &obs); err != nil {
		t.Fatalf("waf verdict does not decode as observations: %v (%s)", err, b)
	}
	if len(obs) == 0 || obs[0].Name != "Cloudflare" {
		t.Fatalf("waf = %+v, want Cloudflare first", obs)
	}
	if obs[0].Confidence < 0.8 {
		t.Errorf("confidence %v is too low for a cf-ray header", obs[0].Confidence)
	}
	if len(obs[0].Evidence) == 0 {
		t.Error("verdict carries no evidence")
	}
}

// TestDetectQuietResponse is the merge contract: a response with nothing to say
// must produce an empty bag, because the upsert merges extra by key and an empty
// object leaves whatever an earlier probe stored untouched.
func TestDetectQuietResponse(t *testing.T) {
	techs, extra := detect(http.Header{}, []byte("<html><title>plain</title></html>"), "https://example.test/", 200)
	if len(techs) != 0 || len(extra) != 0 {
		t.Errorf("a plain 200 produced techs=%v extra=%v", techs, extra)
	}
}

// TestDetectBlockStatusIsNotAVendor guards against over-claiming: a bare 403 with
// no signatures must stay a low-confidence generic hint, never a named product.
func TestDetectBlockStatusIsNotAVendor(t *testing.T) {
	_, extra := detect(http.Header{}, []byte("forbidden"), "https://example.test/", 403)
	raw, ok := extra["waf"]
	if !ok {
		t.Fatalf("a 403 with no signatures recorded no verdict: %v", extra)
	}
	b, _ := json.Marshal(raw)
	var obs []wafObservation
	if err := json.Unmarshal(b, &obs); err != nil {
		t.Fatal(err)
	}
	if len(obs) != 1 || obs[0].Name != "Generic WAF" {
		t.Fatalf("block status gave %+v, want a single Generic WAF", obs)
	}
	if obs[0].Confidence > 0.5 {
		t.Errorf("generic confidence %v is too high to be a hint", obs[0].Confidence)
	}
}

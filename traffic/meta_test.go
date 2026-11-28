package traffic

import "testing"

// TestMetaReturnsExchangeIdentity checks Meta reports the same id and capture
// time the exchange was stored under, so callers that only have the bodies can
// still identify the row.
func TestMetaReturnsExchangeIdentity(t *testing.T) {
	tr, _ := openTraffic(t)
	tr.record(newFlow("api.example.com", "GET", "/login", nil, []byte("ok")))
	id := onlyExchangeID(t, tr)

	m, err := tr.Meta(id)
	if err != nil {
		t.Fatalf("Meta(%q): %v", id, err)
	}
	if m.ID != id {
		t.Fatalf("id = %q, want %q", m.ID, id)
	}
	if m.TS <= 0 {
		t.Fatalf("ts = %d, want a positive capture time", m.TS)
	}
	if m.Host != "api.example.com" || m.Method != "GET" || m.Status != 200 {
		t.Fatalf("meta = %+v, want host/method/status of the recorded flow", m)
	}
}

// TestMetaUnknownID checks a missing exchange is an error rather than a zero row.
func TestMetaUnknownID(t *testing.T) {
	tr, _ := openTraffic(t)
	if m, err := tr.Meta("1700000000-9999"); err == nil {
		t.Fatalf("Meta of unknown id = %+v, want error", m)
	}
}

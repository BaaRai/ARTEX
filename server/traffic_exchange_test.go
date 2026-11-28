package server

import (
	"encoding/json"
	"testing"
)

// TestTrafficExchangeCarriesIdentity pins the contract the bench-v2 R2 check
// reads: GET /api/traffic/exchange must name the exchange it returned and when
// it was captured. scripts/benchmark/run_bench.py credits a finding only when a
// referenced traffic_ref exists in the capture store with a timestamp inside the
// run window; with the bodies alone the timestamp lookup finds nothing and every
// verified finding is rejected as "traffic_ref_missing_timestamp".
func TestTrafficExchangeCarriesIdentity(t *testing.T) {
	s, request := ingestServer(t)
	db := s.m.Traffic().DB()
	const ts = 1700001200
	if _, err := db.Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES('1700001200-0001',?,'api.example.com','GET','/login','http://api.example.com/login',200,'text/html',0,0,'')`, ts); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO exchange_bodies(id,req_head,resp_head) VALUES('1700001200-0001','GET /login HTTP/1.1\n','HTTP/1.1 200 OK\n')`); err != nil {
		t.Fatal(err)
	}

	w := request("GET", "/api/traffic/exchange?id=1700001200-0001", "")
	if w.Code != 200 {
		t.Fatalf("GET exchange = %d %s", w.Code, w.Body)
	}
	var got struct {
		ID   string `json:"id"`
		TS   int64  `json:"ts"`
		Req  string `json:"req"`
		Resp string `json:"resp"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}
	if got.ID != "1700001200-0001" {
		t.Fatalf("id = %q, want the requested exchange id (body %s)", got.ID, w.Body)
	}
	if got.TS != ts {
		t.Fatalf("ts = %d, want the capture time %d (body %s)", got.TS, ts, w.Body)
	}
	// The bodies stay exactly as before; the identity fields are additive.
	if got.Req == "" || got.Resp == "" {
		t.Fatalf("bodies missing: req %q resp %q", got.Req, got.Resp)
	}
}

// TestTrafficExchangeUnknownID checks the 404 path still reports a missing
// exchange rather than an empty record.
func TestTrafficExchangeUnknownID(t *testing.T) {
	_, request := ingestServer(t)
	if w := request("GET", "/api/traffic/exchange?id=does-not-exist", ""); w.Code != 404 {
		t.Fatalf("unknown exchange = %d %s, want 404", w.Code, w.Body)
	}
}

package server

import (
	"encoding/json"
	"testing"
)

func TestTrafficStreamRoute(t *testing.T) {
	s, request := ingestServer(t)
	db := s.m.Traffic().DB()
	if _, err := db.Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES('1-0001',1,'ws.example.com','GET','/ws','wss://ws.example.com/ws',101,'websocket',0,0,'')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO exchange_bodies(id,req_head,resp_head) VALUES('1-0001','GET /ws HTTP/1.1\n','HTTP/1.1 101 Switching Protocols\n')`); err != nil {
		t.Fatal(err)
	}
	const total = 250
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= total; i++ {
		kind, payload := 1, []byte("hello")
		if i%2 == 0 {
			kind, payload = 2, []byte{0, 1, 2}
		}
		if _, err := tx.Exec(`INSERT INTO stream_messages(exchange_id,seq,ts,direction,kind,size,payload) VALUES('1-0001',?,1,'c2s',?,?,?)`, i, kind, len(payload), payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	type page struct {
		Items []struct {
			Seq      int64  `json:"seq"`
			Type     string `json:"type"`
			Payload  string `json:"payload"`
			Encoding string `json:"encoding"`
		} `json:"items"`
		Total int `json:"total"`
	}
	get := func(path string, wantStatus int) page {
		t.Helper()
		w := request("GET", path, "")
		if w.Code != wantStatus {
			t.Fatalf("GET %s = %d %s, want %d", path, w.Code, w.Body, wantStatus)
		}
		var p page
		if wantStatus == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
				t.Fatalf("GET %s: %v in %s", path, err, w.Body)
			}
		}
		return p
	}

	get("/api/traffic/stream", 400)
	if p := get("/api/traffic/stream?exchange=nope", 200); p.Items == nil || len(p.Items) != 0 || p.Total != 0 {
		t.Fatalf("unknown exchange = %+v, want items [] and total 0", p)
	}
	p := get("/api/traffic/stream?exchange=1-0001", 200)
	if p.Total != total || len(p.Items) != 50 || p.Items[0].Seq != 1 {
		t.Fatalf("default page: total %d, %d items, first seq %d", p.Total, len(p.Items), p.Items[0].Seq)
	}
	if first, second := p.Items[0], p.Items[1]; first.Type != "text" || first.Payload != "hello" || second.Type != "binary" || second.Encoding != "base64" || second.Payload != "AAEC" {
		t.Fatalf("payloads %+v %+v", first, second)
	}
	if p := get("/api/traffic/stream?exchange=1-0001&limit=500", 200); len(p.Items) != 200 {
		t.Fatalf("limit=500 returned %d items, want the 200 cap", len(p.Items))
	}
	if p := get("/api/traffic/stream?exchange=1-0001&offset=-5&limit=1", 200); len(p.Items) != 1 || p.Items[0].Seq != 1 {
		t.Fatalf("negative offset: %+v, want seq 1", p.Items)
	}
	if p := get("/api/traffic/stream?exchange=1-0001&offset=248&limit=10", 200); len(p.Items) != 2 || p.Items[0].Seq != 249 {
		t.Fatalf("last page: %+v, want seqs 249 and 250", p.Items)
	}

	w := request("GET", "/api/traffic/exchange?id=1-0001", "")
	var ex struct {
		StreamCount int    `json:"stream_count"`
		StreamKind  string `json:"stream_kind"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ex); err != nil || w.Code != 200 || ex.StreamCount != total || ex.StreamKind != "websocket" {
		t.Fatalf("exchange = %d %s", w.Code, w.Body)
	}
}

package traffic

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/traffic/rewrite"
)

func TestHARRoundTripPlainBase64BinaryEmptyAndLargeBlob(t *testing.T) {
	src, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	// Install an enabled capture rewrite rule to prove HAR import NEVER runs rules
	// or writes exchange_rewrites rows.
	set := rewrite.Compile([]rewrite.Rule{{
		ID: 42, Name: "should-not-apply-to-har", Enabled: true, Phase: rewrite.PhaseRequest,
		Actions: []rewrite.Action{{Type: rewrite.ActionSetHeader, Name: "X-Injected", Value: "1"}},
	}}, rewrite.Options{})
	if len(set.LoadErrors()) != 0 {
		t.Fatalf("compile rewrite rules: %v", set.LoadErrors())
	}
	src.SetRewriteRules(set)

	binaryResp := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff, 0xfe, 0x01}
	binaryReq := []byte{0x00, 0x01, 0x02, 0x80, 0xff}
	largeTextBody := "LARGE-HAR-NEEDLE-" + strings.Repeat("abcdefghij", (maxInlineBody/10)+50) // > 256 KiB -> blob!

	inputHAR := HAR{
		Log: &HARLog{
			Version: "1.2",
			Creator: HARCreator{Name: "TestBrowser", Version: "1.0"},
			Entries: []HAREntry{
				{
					StartedDateTime: "2026-05-01T10:00:00.789Z",
					Request: HARRequest{
						Method:      "POST",
						URL:         "https://app.example.com/api/v1/items/42?mode=full",
						HTTPVersion: "HTTP/1.1",
						Headers: []HARHeader{
							{Name: ":method", Value: "POST"},
							{Name: ":authority", Value: "app.example.com"},
							{Name: "Host", Value: "app.example.com"},
							{Name: "Content-Type", Value: "application/json"},
							{Name: "X-Trace", Value: "one"},
							{Name: "X-Trace", Value: "two"},
						},
						PostData: &HARPostData{
							MimeType: "application/json",
							Text:     `{"action":"create","needle":"plain-req-token"}`,
						},
					},
					Response: HARResponse{
						Status:      201,
						StatusText:  "Created",
						HTTPVersion: "HTTP/1.1",
						Headers: []HARHeader{
							{Name: "Content-Type", Value: "application/json; charset=utf-8"},
							{Name: "X-Resp", Value: "ok"},
						},
						Content: HARContent{
							MimeType: "application/json; charset=utf-8",
							Text:     `{"id":42,"msg":"plain-resp-marker"}`,
						},
					},
				},
				{
					StartedDateTime: "2026-05-01T10:00:05Z",
					Request: HARRequest{
						Method:      "POST",
						URL:         "https://cdn.example.com/upload",
						HTTPVersion: "HTTP/2.0",
						Headers:     []HARHeader{{Name: "Content-Type", Value: "application/octet-stream"}},
						PostData: &HARPostData{
							MimeType: "application/octet-stream",
							Text:     base64.StdEncoding.EncodeToString(binaryReq),
							Encoding: "base64",
						},
					},
					Response: HARResponse{
						Status:      200,
						HTTPVersion: "HTTP/2.0",
						Headers:     []HARHeader{{Name: "Content-Type", Value: "image/png"}},
						Content: HARContent{
							MimeType: "image/png",
							Text:     base64.StdEncoding.EncodeToString(binaryResp),
							Encoding: "base64",
						},
					},
				},
				{
					StartedDateTime: "2026-05-01T10:00:10Z",
					Request: HARRequest{
						Method:  "GET",
						URL:     "https://app.example.com/empty",
						Headers: []HARHeader{{Name: "Accept", Value: "*/*"}},
					},
					Response: HARResponse{
						Status:  204,
						Headers: []HARHeader{},
						Content: HARContent{},
					},
				},
				{
					StartedDateTime: "2026-05-01T10:00:15Z",
					Request: HARRequest{
						Method:  "GET",
						URL:     "https://blob.example.com/big.txt",
						Headers: []HARHeader{},
					},
					Response: HARResponse{
						Status:  200,
						Headers: []HARHeader{{Name: "Content-Type", Value: "text/plain"}},
						Content: HARContent{
							MimeType: "text/plain",
							Text:     largeTextBody,
						},
					},
				},
			},
		},
	}

	rawIn, err := json.Marshal(inputHAR)
	if err != nil {
		t.Fatal(err)
	}

	res, err := src.ImportHAR(rawIn, HARImportOptions{})
	if err != nil {
		t.Fatalf("ImportHAR: %v", err)
	}
	if res.Imported != 4 || res.Skipped != 0 || res.Failed != 0 || res.Truncated {
		t.Fatalf("ImportHAR result = %+v, want 4/0/0/false", res)
	}

	// Re-importing the exact same HAR must be idempotent (all skipped).
	res2, err := src.ImportHAR(rawIn, HARImportOptions{})
	if err != nil {
		t.Fatalf("re-import HAR: %v", err)
	}
	if res2.Imported != 0 || res2.Skipped != 4 || res2.Failed != 0 {
		t.Fatalf("re-import result = %+v, want 0/4/0", res2)
	}

	// Verify none of the imported exchanges are flagged rewritten or have exchange_rewrites rows.
	var rwRows int
	if err := src.DB().QueryRow(`SELECT COUNT(*) FROM exchange_rewrites`).Scan(&rwRows); err != nil || rwRows != 0 {
		t.Fatalf("exchange_rewrites count = %d err=%v, want 0", rwRows, err)
	}
	allRows, total, err := src.Page(PageQuery{RespMin: -1, RespMax: -1}, 0, 20)
	if err != nil || total != 4 || len(allRows) != 4 {
		t.Fatalf("Page after import = (%d, %d, %v), want (4, 4, nil)", len(allRows), total, err)
	}
	for _, r := range allRows {
		if !strings.HasPrefix(r.ID, "har-") {
			t.Fatalf("expected har- prefix on id %q", r.ID)
		}
		if r.Rewritten {
			t.Fatalf("imported exchange %s unexpectedly flagged rewritten", r.ID)
		}
		if rw, ids, err := src.RewriteInfo(r.ID); err != nil || rw || len(ids) != 0 {
			t.Fatalf("RewriteInfo(%s) = (%v, %v, %v), want (false, nil, nil)", r.ID, rw, ids, err)
		}
	}

	// Verify searchable via FTS (both inline body and spilled large text body).
	if rows, tot, err := src.Page(PageQuery{Body: "plain-resp-marker", RespMin: -1, RespMax: -1}, 0, 10); err != nil || tot != 1 || len(rows) != 1 {
		t.Fatalf("FTS search inline body = (%d, %d, %v), want 1", len(rows), tot, err)
	}
	if rows, tot, err := src.Page(PageQuery{Body: "LARGE-HAR-NEEDLE", RespMin: -1, RespMax: -1}, 0, 10); err != nil || tot != 1 || len(rows) != 1 {
		t.Fatalf("FTS search blob body = (%d, %d, %v), want 1", len(rows), tot, err)
	}

	// Verify large body actually spilled to a blob and registered in blob_refs.
	var blobHash string
	var blobRefCount int
	if err := src.DB().QueryRow(`SELECT resp_blob FROM exchange_bodies b JOIN exchanges e ON e.id=b.id WHERE e.host='blob.example.com'`).Scan(&blobHash); err != nil || blobHash == "" {
		t.Fatalf("large body did not spill to blob: hash=%q err=%v", blobHash, err)
	}
	if err := src.DB().QueryRow(`SELECT COUNT(*) FROM blob_refs WHERE hash=?`, blobHash).Scan(&blobRefCount); err != nil || blobRefCount != 1 {
		t.Fatalf("blob_refs count for %s = %d err=%v, want 1", blobHash, blobRefCount, err)
	}

	// Export HAR from src.
	exported, meta, err := src.ExportHAR(PageQuery{RespMin: -1, RespMax: -1}, HARExportOptions{CreatorVersion: "2.3.0"})
	if err != nil {
		t.Fatalf("ExportHAR: %v", err)
	}
	if meta.Exported != 4 || meta.TotalMatched != 4 || meta.Truncated || meta.TruncatedReason != "" {
		t.Fatalf("ExportHAR meta = %+v", meta)
	}
	if exported.Log.Version != "1.2" || exported.Log.Creator.Name != "ARTEX" || exported.Log.Creator.Version != "2.3.0" {
		t.Fatalf("ExportHAR header = %+v", exported.Log)
	}
	if len(exported.Log.Entries) != 4 {
		t.Fatalf("ExportHAR entries = %d, want 4", len(exported.Log.Entries))
	}

	// Verify no invented fields (time, serverIPAddress) in marshaled HAR JSON.
	rawExported, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawExported), `"serverIPAddress"`) || strings.Contains(string(rawExported), `"time":`) {
		t.Fatalf("exported HAR invented prohibited fields: %s", string(rawExported))
	}

	// Verify stable chronological ordering & exact body encodings.
	e0 := exported.Log.Entries[0]
	if e0.Request.URL != "https://app.example.com/api/v1/items/42?mode=full" || e0.Response.Content.Encoding != "" || e0.Response.Content.Text != `{"id":42,"msg":"plain-resp-marker"}` {
		t.Fatalf("entry 0 mismatch: %+v", e0)
	}
	e1 := exported.Log.Entries[1]
	if e1.Response.Content.Encoding != "base64" || e1.Request.PostData == nil || e1.Request.PostData.Encoding != "base64" {
		t.Fatalf("entry 1 binary encodings wrong: req=%+v resp=%+v", e1.Request.PostData, e1.Response.Content)
	}
	gotBinResp, err := base64.StdEncoding.DecodeString(e1.Response.Content.Text)
	if err != nil || !bytes.Equal(gotBinResp, binaryResp) {
		t.Fatalf("entry 1 binary resp mismatch: got=%x err=%v", gotBinResp, err)
	}
	gotBinReq, err := base64.StdEncoding.DecodeString(e1.Request.PostData.Text)
	if err != nil || !bytes.Equal(gotBinReq, binaryReq) {
		t.Fatalf("entry 1 binary req mismatch: got=%x err=%v", gotBinReq, err)
	}
	e2 := exported.Log.Entries[2]
	if e2.Request.PostData != nil || e2.Response.Content.Size != 0 || e2.Response.Content.Text != "" {
		t.Fatalf("entry 2 empty body mismatch: %+v", e2)
	}
	e3 := exported.Log.Entries[3]
	if e3.Response.Content.Size != len(largeTextBody) || e3.Response.Content.Text != largeTextBody || e3.Response.Content.Encoding != "" {
		t.Fatalf("entry 3 large blob body truncated or altered: size=%d len=%d", e3.Response.Content.Size, len(e3.Response.Content.Text))
	}

	// Re-importing the exported HAR into src must be 100% idempotent (same deterministic IDs!).
	reimpSame, err := src.ImportHAR(rawExported, HARImportOptions{})
	if err != nil {
		t.Fatalf("re-import exported HAR into src: %v", err)
	}
	if reimpSame.Imported != 0 || reimpSame.Skipped != 4 || reimpSame.Failed != 0 {
		t.Fatalf("re-import exported HAR into src = %+v, want 0/4/0", reimpSame)
	}

	// Re-importing the exported HAR into a fresh store must yield identical IDs, metadata, and bodies.
	dst, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	reimpFresh, err := dst.ImportHAR(rawExported, HARImportOptions{})
	if err != nil {
		t.Fatalf("import exported HAR into dst: %v", err)
	}
	if reimpFresh.Imported != 4 || reimpFresh.Skipped != 0 || reimpFresh.Failed != 0 {
		t.Fatalf("dst import = %+v, want 4/0/0", reimpFresh)
	}

	exportedDst, _, err := dst.ExportHAR(PageQuery{RespMin: -1, RespMax: -1}, HARExportOptions{CreatorVersion: "2.3.0"})
	if err != nil {
		t.Fatal(err)
	}
	rawDst, err := json.Marshal(exportedDst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rawExported, rawDst) {
		t.Fatalf("dst round-trip HAR differs from src export")
	}
}

func TestHARImportValidationDuplicatesAndPartialErrors(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	badDocs := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", "empty HAR"},
		{"malformed json", `{"log":`, "malformed JSON"},
		{"trailing garbage", `{"log":{"version":"1.2","entries":[]}} extra`, "trailing content"},
		{"missing log", `{}`, "missing log"},
		{"missing entries", `{"log":{"version":"1.2"}}`, "missing log.entries"},
		{"null entries", `{"log":{"version":"1.2","entries":null}}`, "missing log.entries"},
		{"unsupported version 1.1", `{"log":{"version":"1.1","entries":[]}}`, "unsupported HAR version"},
		{"unsupported version 2.0", `{"log":{"version":"2.0","entries":[]}}`, "unsupported HAR version"},
		{"empty version", `{"log":{"version":"","entries":[]}}`, "unsupported HAR version"},
	}
	for _, tc := range badDocs {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tr.ImportHAR([]byte(tc.raw), HARImportOptions{})
			if !errors.Is(err, ErrInvalidHAR) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ImportHAR(%s) err = %v, want ErrInvalidHAR containing %q", tc.name, err, tc.want)
			}
		})
	}

	// Duplicate entries inside one HAR + bad entries mixed with valid ones.
	mixed := `{
		"log": {
			"version": "1.2",
			"entries": [
				{
					"startedDateTime": "2026-05-01T12:00:00Z",
					"request": {"method": "GET", "url": "https://dup.example/a", "headers": []},
					"response": {"status": 200, "headers": [], "content": {"text": "ok"}}
				},
				{
					"startedDateTime": "2026-05-01T12:00:00Z",
					"request": {"method": "GET", "url": "https://dup.example/a", "headers": []},
					"response": {"status": 200, "headers": [], "content": {"text": "ok"}}
				},
				{
					"startedDateTime": "not-a-timestamp",
					"request": {"method": "GET", "url": "https://bad.example/ts", "headers": []},
					"response": {"status": 200, "headers": [], "content": {}}
				},
				{
					"startedDateTime": "2026-05-01T12:00:01Z",
					"request": {"method": "GET", "url": "https://bad.example/hdr", "headers": [{"name": "X-Bad", "value": "a\r\nb"}]},
					"response": {"status": 200, "headers": [], "content": {}}
				},
				{
					"startedDateTime": "2026-05-01T12:00:02Z",
					"request": {"method": "GET", "url": "https://bad.example/b64", "headers": []},
					"response": {"status": 200, "headers": [], "content": {"text": "!!!not-base64!!!", "encoding": "base64"}}
				},
				{
					"startedDateTime": "2026-05-01T12:00:03Z",
					"request": {"method": "GET", "url": "https://good.example/b", "headers": []},
					"response": {"status": 200, "headers": [], "content": {"text": "second"}}
				}
			]
		}
	}`
	res, err := tr.ImportHAR([]byte(mixed), HARImportOptions{})
	if err != nil {
		t.Fatalf("mixed ImportHAR: %v", err)
	}
	if res.Imported != 2 || res.Skipped != 1 || res.Failed != 3 || len(res.Errors) != 3 {
		t.Fatalf("mixed ImportHAR result = %+v, want imported=2 skipped=1 failed=3 errors=3", res)
	}

	// Entry cap returns ErrInvalidHAR.
	if _, err := tr.ImportHAR([]byte(mixed), HARImportOptions{MaxEntries: 2}); !errors.Is(err, ErrInvalidHAR) {
		t.Fatalf("MaxEntries exceeded err = %v, want ErrInvalidHAR", err)
	}

	// Byte cap truncates cleanly and reports remaining skipped.
	byteCapHAR := `{
		"log": {
			"version": "1.2",
			"entries": [
				{
					"startedDateTime": "2026-05-01T12:01:00Z",
					"request": {"method": "GET", "url": "https://cap.example/1", "headers": []},
					"response": {"status": 200, "headers": [], "content": {"text": "1234567890"}}
				},
				{
					"startedDateTime": "2026-05-01T12:01:01Z",
					"request": {"method": "GET", "url": "https://cap.example/2", "headers": []},
					"response": {"status": 200, "headers": [], "content": {"text": "1234567890"}}
				}
			]
		}
	}`
	capRes, err := tr.ImportHAR([]byte(byteCapHAR), HARImportOptions{MaxTotalBytes: 15})
	if err != nil {
		t.Fatalf("byte-capped ImportHAR: %v", err)
	}
	if capRes.Imported != 1 || capRes.Skipped != 1 || !capRes.Truncated || len(capRes.Errors) != 1 {
		t.Fatalf("byte-capped ImportHAR = %+v, want imported=1 skipped=1 truncated=true", capRes)
	}
}

func TestHARExportFiltersAndTruncationCaps(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	insert := func(id string, ts int64, host string, status int, body string) {
		t.Helper()
		if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, ts, host, "GET", "/"+id, "https://"+host+"/"+id, status, "text/plain", 0, len(body), ""); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.DB().Exec(`INSERT INTO exchange_bodies(id,req_head,req_body,resp_head,resp_body)
VALUES(?,?,?,?,?)`, id, "GET /"+id+" HTTP/1.1\nHost: "+host, []byte{}, "HTTP/1.1 200 OK", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	insert("e-2", 20, "alpha.example", 200, "bbbbbbbbbb")
	insert("e-1", 10, "alpha.example", 404, "aaaaaaaaaa")
	insert("e-3", 30, "beta.example", 200, "cccccccccc")

	// Filter by host + status, verify chronological order.
	doc, meta, err := tr.ExportHAR(PageQuery{Host: "alpha.example", RespMin: -1, RespMax: -1}, HARExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if meta.Exported != 2 || meta.TotalMatched != 2 || meta.Truncated {
		t.Fatalf("filtered export meta = %+v", meta)
	}
	if doc.Log.Entries[0].Request.URL != "https://alpha.example/e-1" || doc.Log.Entries[1].Request.URL != "https://alpha.example/e-2" {
		t.Fatalf("export not in stable (ts,id) order: %+v", doc.Log.Entries)
	}

	// Truncation by MaxEntries.
	docEntryCap, metaEntryCap, err := tr.ExportHAR(PageQuery{RespMin: -1, RespMax: -1}, HARExportOptions{MaxEntries: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !metaEntryCap.Truncated || metaEntryCap.TruncatedReason != "max_entries" || metaEntryCap.Exported != 2 || metaEntryCap.TotalMatched != 3 {
		t.Fatalf("entry-capped meta = %+v", metaEntryCap)
	}
	if !docEntryCap.Log.Truncated || docEntryCap.Log.TruncatedReason != "max_entries" || docEntryCap.Log.TotalMatched != 3 {
		t.Fatalf("entry-capped log fields = %+v", docEntryCap.Log)
	}

	// Truncation by MaxTotalBytes.
	docByteCap, metaByteCap, err := tr.ExportHAR(PageQuery{RespMin: -1, RespMax: -1}, HARExportOptions{MaxTotalBytes: 15})
	if err != nil {
		t.Fatal(err)
	}
	if !metaByteCap.Truncated || metaByteCap.TruncatedReason != "max_bytes" || metaByteCap.Exported != 1 || metaByteCap.TotalMatched != 3 {
		t.Fatalf("byte-capped meta = %+v", metaByteCap)
	}
	if !docByteCap.Log.Truncated || docByteCap.Log.TruncatedReason != "max_bytes" || docByteCap.Log.TotalMatched != 3 {
		t.Fatalf("byte-capped log fields = %+v", docByteCap.Log)
	}
}

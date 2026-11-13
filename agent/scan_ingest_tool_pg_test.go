package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// ingest_scan_output (PG): an nmap file writes its assets through insert_assets
// and its facts through record_fact; a second ingestion of the same file keeps
// the counters, adds no asset row and no fact; a path leaving the working
// directory through a symlink is refused.
func TestIngestScanOutputPG(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTaskWithOptions("ingest_scan_output", "scan-ingest.example.com", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const assetsWhere = `ip IN ('198.51.100.77','2001:db8::77') OR domain LIKE '%scan-ingest.example.com' OR url LIKE '%198.51.100.77%' OR url LIKE '%2001:db8::77%'`
	t.Cleanup(func() {
		_ = d.DeleteTask(task.ID)
		_, _ = d.Exec(`DELETE FROM assets WHERE ` + assetsWhere)
	})
	store := d.Exploration(task.ExplorationID)
	tsx := NewToolSet(store, "worker-1")
	tsx.SetTaskID(task.ID)
	tsx.SetAssetStore(d.Assets(), d.Companies())

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "scan.xml"), []byte(nmapFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := tsx.ingestScanOutput()
	call := func(path string) (map[string]any, actool.Result) {
		t.Helper()
		in, _ := json.Marshal(map[string]string{"format": "nmap-xml", "path": path})
		res, err := tool.Call(context.Background(), in, &actool.ToolContext{WorkingDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(res.Flatten()), &out)
		return out, res
	}
	count := func(q string) int {
		t.Helper()
		var n int
		if err := d.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	facts := func() int {
		all, err := store.ListByKind(db.KindFact, 500)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, f := range all {
			if f.Origin == "worker-1" {
				n++
			}
		}
		return n
	}

	first, res := call("scan.xml")
	if res.IsError || first["facts_written"] != 2.0 || first["assets_written"] != 6.0 || first["fact_errors"] != nil || first["asset_errors"] != nil {
		t.Fatalf("first ingestion: %s", res.Flatten())
	}
	if n := count(`SELECT count(*) FROM assets WHERE type='ip' AND ip IN ('198.51.100.77','2001:db8::77')`); n != 2 {
		t.Fatalf("%d ip assets, want 2", n)
	}
	if n := count(`SELECT count(*) FROM assets WHERE type='service' AND url IN ('http://198.51.100.77:80','https://198.51.100.77:443','https://[2001:db8::77]:8443')`); n != 3 {
		t.Fatalf("%d http service assets, want 3", n)
	}
	if n := count(`SELECT count(*) FROM assets WHERE url LIKE '%evil%' OR domain LIKE '%evil%' OR domain = 'ptr.example.net'`); n != 0 {
		t.Fatalf("%d assets from target text or a PTR name", n)
	}
	assets, nFacts := count(`SELECT count(*) FROM assets WHERE `+assetsWhere), facts()
	if nFacts != 2 {
		t.Fatalf("%d facts, want 2", nFacts)
	}

	// Re-ingestion: same counters, no new asset row, no new fact.
	again, res := call("scan.xml")
	if res.IsError || again["facts_written"] != 0.0 || again["facts_existing"] != 2.0 {
		t.Fatalf("re-ingestion: %s", res.Flatten())
	}
	for _, k := range []string{"hosts", "ports", "urls", "events", "skipped", "assets_written"} {
		if again[k] != first[k] {
			t.Fatalf("re-ingestion: %s = %v, first run %v", k, again[k], first[k])
		}
	}
	if a, f := count(`SELECT count(*) FROM assets WHERE `+assetsWhere), facts(); a != assets || f != nFacts {
		t.Fatalf("re-ingestion: %d assets and %d facts, first run %d and %d", a, f, assets, nFacts)
	}

	// A symlink in the working directory leading outside it is refused.
	outside := filepath.Join(t.TempDir(), "secret.xml")
	if err := os.WriteFile(outside, []byte(nmapFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.xml")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"escape.xml", outside, "../secret.xml"} {
		if _, res := call(p); !res.IsError {
			t.Fatalf("%s accepted: %s", p, res.Flatten())
		}
	}
}

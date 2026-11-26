package traffic

import (
	"fmt"
	"strings"
	"testing"
)

func queryPlan(t testing.TB, tr *Traffic, q string, args ...any) string {
	t.Helper()
	rows, err := tr.db.Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan = append(plan, detail)
	}
	return strings.Join(plan, "\n")
}

func TestHostSearchUsesHostTSIndex(t *testing.T) {
	tr, _ := openTraffic(t)

	plan := queryPlan(t, tr, `SELECT id FROM exchanges WHERE host=? ORDER BY ts DESC LIMIT 3`, "a.example")
	if !strings.Contains(plan, "idx_ex_host_ts") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("host search should walk idx_ex_host_ts without a sort:\n%s", plan)
	}
	plan = queryPlan(t, tr, `SELECT host, COUNT(*) AS n, MAX(ts) AS last FROM exchanges GROUP BY host ORDER BY last DESC`)
	if !strings.Contains(plan, "COVERING INDEX idx_ex_host_ts") {
		t.Fatalf("Hosts() should read only idx_ex_host_ts:\n%s", plan)
	}
}

func BenchmarkHostSearch(b *testing.B) {
	tr, err := Open(b.TempDir(), "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer tr.Close()
	tx, err := tr.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 50000; i++ {
		host := "big.example"
		if i%10 == 0 {
			host = fmt.Sprintf("h%d.example", i%200)
		}
		if _, err := tx.Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES (?,?,?,'GET','/','http://x/',200,'text/html',0,0,'/')`, fmt.Sprintf("bench-%d", i), int64(i), host); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.Run("search", func(b *testing.B) {
		for b.Loop() {
			if _, err := tr.query("big.example", "", "", 0, 3); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("hosts", func(b *testing.B) {
		for b.Loop() {
			if _, err := tr.Hosts(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

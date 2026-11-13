package server

import (
	"database/sql"
	"os"
	"testing"

	"github.com/Autumn-27/artex/db"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestMain acquires a PostgreSQL advisory lock (7337741002) for the entire
// server test suite so cross-package DELETE cleanup races with db/agent
// packages are avoided when running `go test ./...`.
func TestMain(m *testing.M) {
	// Own database per package (#80): with separate databases, packages stop
	// contending on shared tables and the advisory lock below stops
	// serializing go test ./... across packages.
	db.SuffixTestDatabase("server")
	// Fixed test key via env: NewManager's InitSecrets then writes no key file.
	os.Setenv("ARTEX_SECRET_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	dsn, _, err := db.DSN()
	if err != nil {
		os.Exit(m.Run())
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil || conn.Ping() != nil {
		os.Exit(m.Run())
	}
	defer conn.Close()
	if _, err := conn.Exec(`SELECT pg_advisory_lock(7337741002)`); err != nil {
		os.Exit(m.Run())
	}
	defer conn.Exec(`SELECT pg_advisory_unlock(7337741002)`) //nolint:errcheck
	os.Exit(m.Run())
}

// skipWithoutDSN skips the test when neither ARTEX_PG_DSN nor a config file
// supplies a database, so fixtures that t.Fatal on a NewManager error skip
// instead of failing. A configured database that cannot be reached still fails.
func skipWithoutDSN(t *testing.T) {
	t.Helper()
	if _, _, err := db.DSN(); err != nil {
		t.Skipf("no database config (%v) — skipping", err)
	}
}

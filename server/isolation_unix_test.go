//go:build unix

package server

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
)

// fakeTrustPG is a PostgreSQL wire endpoint that lets any user in without a
// password, like a server with trust authentication. It returns its address.
func fakeTrustPG(t *testing.T) (host string, port uint16) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				be := pgproto3.NewBackend(conn, conn)
				msg, err := be.ReceiveStartupMessage()
				for err == nil {
					switch msg.(type) {
					case *pgproto3.SSLRequest, *pgproto3.GSSEncRequest:
						if _, err = conn.Write([]byte("N")); err == nil {
							msg, err = be.ReceiveStartupMessage()
						}
						continue
					}
					break
				}
				if err != nil {
					return
				}
				be.Send(&pgproto3.AuthenticationOk{})
				be.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "16.0"})
				be.Send(&pgproto3.BackendKeyData{ProcessID: 1, SecretKey: []byte{0, 0, 0, 2}})
				be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
				if be.Flush() != nil {
					return
				}
				for {
					if _, err := be.Receive(); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), uint16(addr.Port)
}

func hasFailure(failures []string, sub string) bool {
	for _, f := range failures {
		if strings.Contains(f, sub) {
			return true
		}
	}
	return false
}

// The probe's checks, run in the test process (not as the executor): each kind of
// exposure is reported. The process can read the file, write the directory, carries
// a variable outside the allowlist, is not the executor and reaches a database that
// takes no password.
func TestCheckIsolationReportsEachExposure(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "jwt.key")
	if err := os.WriteFile(secret, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTEX_ISOLATION_CANARY", "canary")
	host, port := fakeTrustPG(t)
	failures := checkIsolationAsExecutor(isolationProbeSpec{
		UID: 10001, GID: 10001, ServerPID: os.Getpid(),
		Unreadable: []string{secret, filepath.Join(dir, "absent.key")},
		Unwritable: []string{dir},
		DB:         &isolationProbeDB{Host: host, Port: port, User: "artex", Database: "artex"},
	})
	for _, want := range []string{
		"not the executor uid 10001",
		"Bash can read " + secret,
		"Bash can create files in " + dir,
		"ARTEX_ISOLATION_CANARY reached Bash",
		`database session as "artex"`,
	} {
		if !hasFailure(failures, want) {
			t.Errorf("no failure %q in %q", want, failures)
		}
	}
	if hasFailure(failures, "absent.key") {
		t.Errorf("a missing file is not an exposure: %q", failures)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the write check left a file behind: %v", entries)
	}
}

// A database that asks for a password, or none listening, is not an exposure.
func TestConnectWithoutPasswordRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	ln.Close() // nothing listens there now
	if err := connectWithoutPassword(&isolationProbeDB{Host: "127.0.0.1", Port: port, User: "artex", Database: "artex"}); err == nil {
		t.Fatal("no database listening, yet a session opened")
	}
}

// Against the real test database: the server's user without its password is
// refused by PostgreSQL's authentication (it asks for the password on TCP).
func TestConnectWithoutPasswordRealPG(t *testing.T) {
	dsn := os.Getenv("ARTEX_PG_DSN")
	if dsn == "" {
		t.Skip("ARTEX_PG_DSN not set")
	}
	spec := isolationSpec(ExecPrivilege{Enabled: true, UID: 10001, GID: 10001}, IsolationTargets{DSN: dsn})
	if spec.DB == nil {
		t.Fatal("no database in the spec")
	}
	err := connectWithoutPassword(spec.DB)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || (pgErr.Code != "28P01" && pgErr.Code != "28000") {
		t.Fatalf("connection without password: %v, want an authentication failure (28P01/28000)", err)
	}
	t.Logf("refused as expected: SQLSTATE %s", pgErr.Code)
}

// The spec carries the server's paths and the database name, never its password.
func TestIsolationSpecHasNoPassword(t *testing.T) {
	spec := isolationSpec(ExecPrivilege{Enabled: true, UID: 10001, GID: 10002}, IsolationTargets{
		DataDir: "/app/data", KeyDir: "/app/keys", ConfigPath: "/app/config.json",
		DSN: "postgres://artex:s3cr3t-pw@postgres:5432/artex?sslmode=disable",
	})
	for _, want := range []string{"/app/keys/jwt.key", "/app/keys/secret.key", "/app/config.json", "/app/data/traffic/_ca/mitmproxy-ca.pem"} {
		found := false
		for _, p := range spec.Unreadable {
			found = found || p == want
		}
		if !found {
			t.Errorf("%s missing from the unreadable files %v", want, spec.Unreadable)
		}
	}
	if spec.DB == nil || spec.DB.Host != "postgres" || spec.DB.Port != 5432 || spec.DB.User != "artex" || spec.DB.Database != "artex" {
		t.Fatalf("db = %+v", spec.DB)
	}
	if spec.UID != 10001 || spec.GID != 10002 || spec.ServerPID != os.Getpid() {
		t.Fatalf("ids = %d:%d pid %d", spec.UID, spec.GID, spec.ServerPID)
	}
	if b := strings.Join(append(spec.Unreadable, spec.DB.Host, spec.DB.User), " "); strings.Contains(b, "s3cr3t") {
		t.Fatal("the password reached the spec")
	}
}

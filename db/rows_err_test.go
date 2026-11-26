package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type midStreamErrDriver struct{}
type midStreamErrConn struct{}
type midStreamErrRows struct{ n int }

func (midStreamErrDriver) Open(string) (driver.Conn, error)  { return midStreamErrConn{}, nil }
func (midStreamErrConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (midStreamErrConn) Close() error                        { return nil }
func (midStreamErrConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (midStreamErrConn) QueryContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	return &midStreamErrRows{}, nil
}

func (r *midStreamErrRows) Columns() []string {
	return []string{"id", "created_at", "level", "tag", "text"}
}
func (r *midStreamErrRows) Close() error { return nil }
func (r *midStreamErrRows) Next(dest []driver.Value) error {
	if r.n == 0 {
		r.n++
		dest[0] = int64(1)
		dest[1] = time.Now()
		dest[2] = "info"
		dest[3] = "test"
		dest[4] = "first row"
		return nil
	}
	if r.n == 1 {
		r.n++
		return errors.New("simulated mid-stream connection drop")
	}
	return io.EOF
}

var registerMidStreamErrDriverOnce sync.Once

func TestLogsMethodsReturnRowsErr(t *testing.T) {
	registerMidStreamErrDriverOnce.Do(func() {
		sql.Register("artex_test_mid_stream_err", midStreamErrDriver{})
	})
	raw, err := sql.Open("artex_test_mid_stream_err", "")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	d := &DB{DB: raw}

	if _, err := d.RecentLogs(10); err == nil {
		t.Fatal("RecentLogs returned nil error on mid-stream rows.Next failure")
	}
	if _, err := d.ListLogsBefore(100, 10); err == nil {
		t.Fatal("ListLogsBefore returned nil error on mid-stream rows.Next failure")
	}
}

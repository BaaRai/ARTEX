package db

import (
	"fmt"
	"testing"
	"time"
)

// InsertSettingIfAbsent is the fallback guarantee for auth.password_hash: the caller's
// GetSetting check may be defeated by a database error, or preempted by a concurrent
// request (bcrypt takes tens of milliseconds), so the "settable only once" guarantee must
// rest on the primary-key constraint, not on an upper-layer if.
func TestInsertSettingIfAbsentDoesNotOverwrite(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()

	// Use a test-only key; never touch the real auth.password_hash in the dev database.
	key := fmt.Sprintf("test.insert_if_absent.%d", time.Now().UnixNano())
	defer func() { _, _ = d.Exec(`DELETE FROM settings WHERE key=$1`, key) }()

	inserted, err := d.InsertSettingIfAbsent(key, "first")
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("first write should return inserted=true")
	}

	inserted, err = d.InsertSettingIfAbsent(key, "second")
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("an existing key should return inserted=false")
	}

	got, ok, err := d.GetSetting(key)
	if err != nil || !ok {
		t.Fatalf("GetSetting: ok=%v err=%v", ok, err)
	}
	if got != "first" {
		t.Fatalf("value was overwritten to %q, should stay %q", got, "first")
	}
}

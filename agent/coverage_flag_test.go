package agent

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

// A failed coverage-flag read stays enabled: the log says so, the worker keeps
// record_coverage and list_untested_assets, and AddAutoScope still writes. That
// write happens even when the stored flag is off, which is the outage behaviour.
func TestCoverageReadFailureKeepsToolsAndWritesAutoScope(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	off := false
	task, err := d.CreateTaskWithOptions("coverage outage", "goal", db.TaskCreateOptions{CoverageEnabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	live := d.Assets()
	stored, err := live.CoverageEnabled(task.ID)
	if err != nil || stored {
		t.Fatalf("stored flag enabled=%v err=%v, want off", stored, err)
	}
	if coverageOn(live, task.ID) {
		t.Fatal("a readable off flag was treated as on")
	}

	dsn, _, err := db.DSN()
	if err != nil {
		t.Fatal(err)
	}
	broken, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := broken.Close(); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	prev := log.Default().Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(prev) })
	if !coverageOn(broken.Assets(), task.ID) {
		t.Fatal("a failed coverage read turned the feature off")
	}
	if !strings.Contains(logged.String(), "leaving coverage on") {
		t.Fatalf("coverage read failure was not logged: %q", logged.String())
	}

	offTools := NewToolSet(nil, "worker-off")
	offTools.SetCoverageEnabled(false)
	for _, name := range []string{"record_coverage", "list_untested_assets"} {
		if hasTool(offTools, name) {
			t.Fatalf("%s stayed available with coverage off", name)
		}
	}
	outageTools := NewToolSet(nil, "worker-outage")
	outageTools.SetCoverageEnabled(coverageOn(broken.Assets(), task.ID))
	for _, name := range []string{"record_coverage", "list_untested_assets"} {
		if !hasTool(outageTools, name) {
			t.Fatalf("%s dropped when the coverage read failed", name)
		}
	}

	keptOff := fmt.Sprintf("off-%d.invalid", time.Now().UnixNano())
	writeIntentAutoScope(live, task.ID, []*db.Asset{{Type: "root_domain", Domain: keptOff}}, false)
	wrote := fmt.Sprintf("outage-%d.invalid", time.Now().UnixNano())
	writeIntentAutoScope(live, task.ID, []*db.Asset{{Type: "root_domain", Domain: wrote}}, coverageOn(broken.Assets(), task.ID))
	scope, err := live.ListTaskScope(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawKept, sawWrote bool
	for _, row := range scope {
		if row.Domain == keptOff {
			sawKept = true
		}
		if row.Domain == wrote {
			sawWrote = true
		}
	}
	if sawKept {
		t.Fatal("auto scope was written while coverage was off")
	}
	if !sawWrote {
		t.Fatal("auto scope was not written when the coverage read failed")
	}
}

package db

import (
	"testing"
	"testing/fstest"
)

const legacyFactCaptureFixture = `{
	"summary":"old fact", "evidence":"synthetic tool output",
	"source":"target_response", "confidence":"observed", "provenance":"captured_traffic",
	"provenance_traffic_id":"unowned-capture", "provenance_activity_id":9007199254740993,
	"extra":{"keep":true}
}`
const repairedFactCaptureFixture = `{
	"summary":"old fact", "evidence":"synthetic tool output",
	"source":"inference", "confidence":"inferred", "provenance":"unverified",
	"provenance_note":"The original capture association did not establish task ownership.",
	"provenance_activity_id":9007199254740993, "extra":{"keep":true}
}`
const strayFactCaptureFixture = `{
	"evidence":"synthetic command result", "provenance_activity_id":23,
	"source":"tool_output", "confidence":"\tOBSERVED\n", "provenance":"recorded_activity",
	"provenance_traffic_id":"unowned-capture"
}`
const repairedStrayFactCaptureFixture = `{
	"evidence":"synthetic command result", "provenance_activity_id":23,
	"source":"inference", "confidence":"inferred", "provenance":"unverified",
	"provenance_note":"The original capture association did not establish task ownership."
}`

func insertFactCaptureFixture(t *testing.T, d *DB, expID int64, kind, state, payload string) int64 {
	t.Helper()
	var id int64
	if err := d.QueryRow(`INSERT INTO exploration_nodes(exploration_id,kind,state,payload)
		VALUES($1,$2,$3,$4::jsonb) RETURNING id`, expID, kind, state, payload).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func assertFactCaptureFixture(t *testing.T, d *DB, id int64, want string, wantVersion int) {
	t.Helper()
	var payload string
	var matches bool
	var version int
	if err := d.QueryRow(`SELECT payload, payload=$2::jsonb, content_version FROM exploration_nodes WHERE id=$1`, id, want).
		Scan(&payload, &matches, &version); err != nil {
		t.Fatal(err)
	}
	if !matches || version != wantVersion {
		t.Errorf("node %d: got version %d payload %s, want version %d payload %s", id, version, payload, wantVersion, want)
	}
}

func TestLegacyFactCaptureMigration(t *testing.T) {
	dsn := scratchDSN(t, "fact_capture")
	old, err := openWithMigrations(dsn, fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var expID int64
	if err := old.QueryRow(`INSERT INTO explorations(goal) VALUES ('migration fixture') RETURNING id`).Scan(&expID); err != nil {
		t.Fatal(err)
	}
	captured := insertFactCaptureFixture(t, old, expID, KindFact, "confirmed", legacyFactCaptureFixture)
	withoutID := insertFactCaptureFixture(t, old, expID, KindFact, "dismissed", `{"provenance":"captured_traffic"}`)
	strayID := insertFactCaptureFixture(t, old, expID, KindFact, "confirmed", strayFactCaptureFixture)
	missingLabel := insertFactCaptureFixture(t, old, expID, KindFact, "confirmed", `{"provenance_traffic_id":"unowned-capture"}`)
	const recordedPayload = `{"source":"tool_output","confidence":"observed","provenance":"recorded_activity","provenance_activity_id":23}`
	recorded := insertFactCaptureFixture(t, old, expID, KindFact, "confirmed", recordedPayload)
	const operatorPayload = `{"source":"operator_input","provenance":"operator_input","confidence":"observed"}`
	operator := insertFactCaptureFixture(t, old, expID, KindFact, "confirmed", operatorPayload)
	otherKind := insertFactCaptureFixture(t, old, expID, KindFinding, "confirmed", legacyFactCaptureFixture)
	store := old.Exploration(expID)
	stale, err := store.AddDigest(map[string]any{"body": "old source classification"}, []int64{strayID})
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.AddDigest(map[string]any{"body": "independent observation"}, []int64{operator})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	check := func() {
		t.Helper()
		assertFactCaptureFixture(t, d, captured, repairedFactCaptureFixture, 1)
		const repairedWithoutConfidence = `{"provenance":"unverified","source":"inference","provenance_note":"The original capture association did not establish task ownership."}`
		assertFactCaptureFixture(t, d, withoutID, repairedWithoutConfidence, 1)
		assertFactCaptureFixture(t, d, missingLabel, repairedWithoutConfidence, 1)
		assertFactCaptureFixture(t, d, strayID, repairedStrayFactCaptureFixture, 1)
		assertFactCaptureFixture(t, d, recorded, recordedPayload, 0)
		assertFactCaptureFixture(t, d, operator, operatorPayload, 0)
		assertFactCaptureFixture(t, d, otherKind, legacyFactCaptureFixture, 0)

		store := d.Exploration(expID)
		active, err := store.ActiveDigests()
		if err != nil || len(active) != 1 || active[0].ID != current {
			t.Fatalf("active digests = %v, want only %d: %v", active, current, err)
		}
		edges, err := store.DigestMembers(stale)
		if err != nil || len(edges) != 0 {
			t.Fatalf("retired digest covers %v: %v", edges, err)
		}
		edges, err = store.DigestMembers(current)
		if err != nil || len(edges) != 1 || edges[0] != operator {
			t.Fatalf("unaffected digest covers %v, want %d: %v", edges, operator, err)
		}
	}
	check()
	if _, err := d.Exec(`SELECT repair_legacy_fact_captures($1)`, expID); err != nil {
		t.Fatal(err)
	}
	check()
}

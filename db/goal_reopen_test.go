package db

import (
	"context"
	"testing"
)

func TestFindingStatusReopensProvedGoals(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	newTaskWithProvedGoal := func(t *testing.T, label string) (taskID int64, es *ExplorationStore, findingID, findingNodeID, goalID int64) {
		t.Helper()
		task, err := d.CreateTaskWithOptions("reopen-"+label, "goal "+label, TaskCreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
		es = d.Exploration(task.ExplorationID)
		goalID, err = es.AddNode(KindGoal, map[string]any{"text": "prove " + label}, 0, "open", "goals", nil)
		if err != nil {
			t.Fatal(err)
		}
		rec, err := es.RecordFinding(ctx, RecordFindingInput{
			TaskID:        task.ID,
			ExplorationID: task.ExplorationID,
			VulnClass:     "sqli",
			Name:          "finding " + label,
			Severity:      "high",
			Summary:       "summary " + label,
			Worker:        "worker-1",
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = d.Exec(`DELETE FROM rsi_lessons WHERE id IN (SELECT lesson_id FROM rsi_lesson_evidence WHERE source_kind='finding' AND source_id=$1)`, rec.FindingID)
		})
		if err := es.Link(rec.NodeID, RelProves, goalID); err != nil {
			t.Fatal(err)
		}
		if err := es.SetNodeState(goalID, "met"); err != nil {
			t.Fatal(err)
		}
		return task.ID, es, rec.FindingID, rec.NodeID, goalID
	}

	assertGoalState := func(t *testing.T, es *ExplorationStore, goalID int64, want string) {
		t.Helper()
		n, err := es.GetNode(goalID)
		if err != nil || n == nil {
			t.Fatalf("GetNode(%d): %v", goalID, err)
		}
		if n.State != want {
			t.Fatalf("goal %d state = %q, want %q", goalID, n.State, want)
		}
	}

	assertProvesEdgeKept := func(t *testing.T, fromID, toID int64) {
		t.Helper()
		var cnt int
		if err := d.QueryRow(`SELECT COUNT(*) FROM exploration_edges WHERE src_id=$1 AND dst_id=$2 AND rel=$3`, fromID, toID, RelProves).Scan(&cnt); err != nil {
			t.Fatal(err)
		}
		if cnt != 1 {
			t.Fatalf("proves edge (%d -> %d) count = %d, want 1 (must be preserved for history)", fromID, toID, cnt)
		}
	}

	t.Run("single_status_path_reopens_for_fp_duplicate_ignored", func(t *testing.T) {
		for _, status := range []string{FindingFalsePositive, FindingDuplicate, FindingIgnored} {
			t.Run(status, func(t *testing.T) {
				_, es, fid, fnid, gid := newTaskWithProvedGoal(t, "single-"+status)
				_, found, _, err := d.SetFindingStatusWithNotify(ctx, fid, status)
				if err != nil || !found {
					t.Fatalf("SetFindingStatusWithNotify(%s): found=%v err=%v", status, found, err)
				}
				assertGoalState(t, es, gid, "open")
				assertProvesEdgeKept(t, fnid, gid)
			})
		}
	})

	t.Run("bulk_status_path_reopens_all_proved_goals", func(t *testing.T) {
		_, es1, fid1, fnid1, gid1 := newTaskWithProvedGoal(t, "bulk-1")
		_, es2, fid2, fnid2, gid2 := newTaskWithProvedGoal(t, "bulk-2")
		res, err := d.SetFindingStatusesWithNotify(ctx, []int64{fid1, fid2}, FindingFalsePositive)
		if err != nil || len(res) != 2 {
			t.Fatalf("SetFindingStatusesWithNotify: res=%+v err=%v", res, err)
		}
		assertGoalState(t, es1, gid1, "open")
		assertGoalState(t, es2, gid2, "open")
		assertProvesEdgeKept(t, fnid1, gid1)
		assertProvesEdgeKept(t, fnid2, gid2)
	})

	t.Run("retest_path_uses_same_tx_semantics", func(t *testing.T) {
		_, es, fid, fnid, gid := newTaskWithProvedGoal(t, "retest")
		// Retest concluding "fixed" calls SetFindingStatusTx(FindingFixed): goal stays met.
		rt, conv, _, err := d.CreateFindingRetest(ctx, fid, "test")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = d.Exec(`DELETE FROM rsi_lessons WHERE id IN (SELECT lesson_id FROM rsi_lesson_evidence WHERE source_kind='retest' AND source_id=$1)`, rt.ID)
			_ = d.DeleteConversation(conv.ID)
		})
		if _, err := d.StartFindingRetest(ctx, rt.ID); err != nil {
			t.Fatal(err)
		}
		if err := d.RecordFindingRetestResult(ctx, conv.ID, "fixed", "patched", "verified 403"); err != nil {
			t.Fatal(err)
		}
		if err := d.FinishFindingRetest(rt.ID, "completed", ""); err != nil {
			t.Fatal(err)
		}
		assertGoalState(t, es, gid, "met")

		// In-transaction rollback check: SetFindingStatusTx(FindingFalsePositive) reopens inside
		// the same tx, and rolling back the tx leaves the goal met.
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, found, changed, _, err := SetFindingStatusTx(ctx, tx, fid, FindingFalsePositive); err != nil || !found || !changed {
			tx.Rollback()
			t.Fatalf("SetFindingStatusTx: found=%v changed=%v err=%v", found, changed, err)
		}
		var stateInTx string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM exploration_nodes WHERE id=$1`, gid).Scan(&stateInTx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if stateInTx != "open" {
			tx.Rollback()
			t.Fatalf("goal state inside tx = %q, want open", stateInTx)
		}
		_ = tx.Rollback()
		assertGoalState(t, es, gid, "met")
		assertProvesEdgeKept(t, fnid, gid)
	})

	t.Run("finding_that_never_proved_a_goal_does_not_touch_other_goals", func(t *testing.T) {
		taskID, es, _, fnid1, gid1 := newTaskWithProvedGoal(t, "unlinked")
		rec2, err := es.RecordFinding(ctx, RecordFindingInput{
			TaskID:        taskID,
			ExplorationID: es.ID(),
			VulnClass:     "xss",
			Name:          "unlinked finding",
			Severity:      "medium",
			Summary:       "never proved any goal",
			Worker:        "worker-1",
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = d.Exec(`DELETE FROM rsi_lessons WHERE id IN (SELECT lesson_id FROM rsi_lesson_evidence WHERE source_kind='finding' AND source_id=$1)`, rec2.FindingID)
		})
		if _, found, _, err := d.SetFindingStatusWithNotify(ctx, rec2.FindingID, FindingFalsePositive); err != nil || !found {
			t.Fatalf("SetFindingStatusWithNotify(unlinked): found=%v err=%v", found, err)
		}
		assertGoalState(t, es, gid1, "met")
		assertProvesEdgeKept(t, fnid1, gid1)
	})

	t.Run("risk_accepted_keeps_proved_goal_met", func(t *testing.T) {
		_, es, fid, fnid, gid := newTaskWithProvedGoal(t, "risk-accepted")
		if _, found, _, err := d.SetFindingStatusWithNotify(ctx, fid, FindingRiskAccepted); err != nil || !found {
			t.Fatalf("SetFindingStatusWithNotify(risk_accepted): found=%v err=%v", found, err)
		}
		assertGoalState(t, es, gid, "met")
		assertProvesEdgeKept(t, fnid, gid)
	})
}

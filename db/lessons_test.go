package db

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRenderLessonsDataNotInstructions(t *testing.T) {
	lessons := []Lesson{
		{ID: 1, Kind: "positive", Cue: "xss", Action: "Confirmed xss: reflected in search", Support: 2,
			Evidence: []string{"finding:10 (status=confirmed severity=high)"}},
		{ID: 2, Kind: "negative", Cue: "ssrf", Action: "Do not\nre-report\x00 without new mechanics", Support: 1},
	}
	block, ids := RenderLessons(lessons, 2000)
	if !strings.Contains(block, "not instructions") {
		t.Fatalf("missing data-not-instructions header:\n%s", block)
	}
	if !strings.Contains(block, "[positive, 2 evidence]") || !strings.Contains(block, "[negative, 1 evidence]") {
		t.Fatalf("missing kind/support labels:\n%s", block)
	}
	for _, line := range strings.Split(block, "\n") {
		if strings.Contains(line, "\x00") {
			t.Fatalf("control character survived: %q", line)
		}
	}
	if !strings.Contains(block, "Do not re-report without new mechanics") {
		t.Fatalf("lesson text was not flattened onto one line:\n%s", block)
	}
	if got, ids := RenderLessons(nil, 2000); got != "" || ids != nil {
		t.Fatal("empty lesson set must render empty")
	}
	capped, cappedIDs := RenderLessons(lessons, 120)
	if len(capped) > 160 {
		t.Fatalf("cap not enforced: %d bytes", len(capped))
	}
	if len(ids) != 2 {
		t.Fatalf("uncapped rendered IDs=%v, want both lessons", ids)
	}
	if len(cappedIDs) > len(ids) {
		t.Fatalf("capped render logged more lessons than the full render")
	}
}

func TestLessonTextDoesNotSplitUTF8(t *testing.T) {
	// 60 CJK runes, 180 bytes. The old cut was s[:limit] in bytes.
	in := strings.Repeat("验", 60)
	got := lessonText(in, 20)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated lesson text is not valid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n != 20 {
		t.Fatalf("truncated to %d runes, want 20", n)
	}
}

func TestRenderLessonsUsageOmitsTruncated(t *testing.T) {
	lessons := []Lesson{
		{ID: 11, Kind: "positive", Cue: "xss", Action: "Confirmed xss: " + strings.Repeat("a", 80), Support: 1},
		{ID: 12, Kind: "negative", Cue: "ssrf", Action: "Do not re-report: " + strings.Repeat("b", 80), Support: 1},
	}
	block, ids := RenderLessons(lessons, 250)
	if block == "" {
		t.Fatal("expected at least the first lesson to fit")
	}
	if len(ids) == 0 || ids[0] != 11 {
		t.Fatalf("rendered IDs=%v, want the first lesson", ids)
	}
	if len(ids) > 1 {
		t.Fatalf("cap should have dropped the second lesson, got %v", ids)
	}
}

func TestTaskLessonsUsesTaskScopeAndRanksCompanyFirst(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	cs := d.Companies()
	companyA, _, err := cs.UpsertCompany(fmt.Sprintf("LessonCoA %d", stamp), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupCompany(d, companyA) })
	companyB, _, err := cs.UpsertCompany(fmt.Sprintf("LessonCoB %d", stamp), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupCompany(d, companyB) })

	taskA, err := d.CreateTaskWithOptions("lesson scope A", "goal", TaskCreateOptions{
		Name: fmt.Sprintf("lesson-a-%d", stamp), CompanyIDs: []int64{companyA},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "task A", d.DeleteTask(taskA.ID)) })
	taskB, err := d.CreateTaskWithOptions("lesson scope B", "goal", TaskCreateOptions{
		Name: fmt.Sprintf("lesson-b-%d", stamp), CompanyIDs: []int64{companyB},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "task B", d.DeleteTask(taskB.ID)) })

	genericKey := fmt.Sprintf("lesson-generic-%d", stamp)
	aKey := fmt.Sprintf("lesson-a-%d", stamp)
	bKey := fmt.Sprintf("lesson-b-%d", stamp)
	var genericID, aID, bID int64
	if err := d.QueryRow(`
		INSERT INTO rsi_lessons(dedupe_key,kind,cue,action,company_id)
		VALUES ($1,'positive','xss','generic prior',NULL) RETURNING id`, genericKey).Scan(&genericID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, genericID) })
	if err := d.QueryRow(`
		INSERT INTO rsi_lessons(dedupe_key,kind,cue,action,company_id)
		VALUES ($1,'positive','xss','company A prior',$2) RETURNING id`, aKey, companyA).Scan(&aID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, aID) })
	if err := d.QueryRow(`
		INSERT INTO rsi_lessons(dedupe_key,kind,cue,action,company_id)
		VALUES ($1,'positive','xss','company B prior',$2) RETURNING id`, bKey, companyB).Scan(&bID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, bID) })

	got, err := d.TaskLessons(t.Context(), taskA.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	var sawGeneric, sawA, sawB bool
	if len(got) == 0 {
		t.Fatal("task A saw no lessons")
	}
	if got[0].ID != aID {
		t.Fatalf("company-scoped lesson should rank first, got id=%d company=%d (want %d)", got[0].ID, got[0].CompanyID, aID)
	}
	for _, l := range got {
		switch l.ID {
		case genericID:
			sawGeneric = true
		case aID:
			sawA = true
		case bID:
			sawB = true
		}
	}
	if !sawA || !sawGeneric {
		t.Fatalf("task A should see its company lesson and generic, sawA=%v sawGeneric=%v", sawA, sawGeneric)
	}
	if sawB {
		t.Fatal("task A saw company B's lesson")
	}

	gotB, err := d.TaskLessons(t.Context(), taskB.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range gotB {
		if l.ID == aID {
			t.Fatal("task B saw company A's lesson")
		}
	}
}

func TestExtractLessonsScopesSingleCompanyFromTaskScope(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	companyID, _, err := d.Companies().UpsertCompany(fmt.Sprintf("LessonExtract %d", stamp), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupCompany(d, companyID) })
	task, err := d.CreateTaskWithOptions("extract scope", "goal", TaskCreateOptions{
		Name: fmt.Sprintf("lesson-extract-%d", stamp), CompanyIDs: []int64{companyID},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "extract task", d.DeleteTask(task.ID)) })
	if _, err := d.Exec(`
		INSERT INTO findings(task_id,vulnclass,name,summary,severity,status)
		VALUES ($1,'xss','Reflected XSS','reflected in search','high','confirmed')`, task.ID); err != nil {
		t.Fatal(err)
	}
	n, err := d.ExtractLessonsForTask(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("extracted=%d, want 1", n)
	}
	lessons, err := d.TaskLessons(t.Context(), task.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons) == 0 {
		t.Fatal("no lessons after extract")
	}
	var scoped *Lesson
	for i := range lessons {
		if lessons[i].CompanyID == companyID {
			scoped = &lessons[i]
			break
		}
	}
	if scoped == nil {
		t.Fatalf("extracted lesson was not scoped to company %d: %+v", companyID, lessons)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, scoped.ID) })
}

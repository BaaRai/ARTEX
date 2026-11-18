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
	// Generic last: without the company rank term, id DESC would put this first.
	if err := d.QueryRow(`
		INSERT INTO rsi_lessons(dedupe_key,kind,cue,action,company_id)
		VALUES ($1,'positive','xss','generic prior',NULL) RETURNING id`, genericKey).Scan(&genericID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, genericID) })

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

func TestExtractLessonsSkipsMultiCompanyTask(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	cs := d.Companies()
	companyA, _, err := cs.UpsertCompany(fmt.Sprintf("LessonMultiA %d", stamp), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupCompany(d, companyA) })
	companyB, _, err := cs.UpsertCompany(fmt.Sprintf("LessonMultiB %d", stamp), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupCompany(d, companyB) })

	task, err := d.CreateTaskWithOptions("extract multi company", "goal", TaskCreateOptions{
		Name: fmt.Sprintf("lesson-multi-%d", stamp), CompanyIDs: []int64{companyA, companyB},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "multi-company task", d.DeleteTask(task.ID)) })
	findingID := insertLessonFinding(t, d, task.ID, "confirmed")

	n, err := d.ExtractLessonsForTask(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("extracted=%d, want 0 for a multi-company task", n)
	}
	var evidence int
	if err := d.QueryRow(`
		SELECT COUNT(*) FROM rsi_lesson_evidence
		WHERE source_kind='finding' AND source_id=$1`, findingID).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if evidence != 0 {
		t.Fatalf("multi-company extract wrote %d evidence rows", evidence)
	}
}

func TestExtractLessonsGenericWhenTaskHasNoCompany(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	task, err := d.CreateTaskWithOptions("extract no company", "goal", TaskCreateOptions{
		Name: fmt.Sprintf("lesson-nocompany-%d", stamp),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "no-company task", d.DeleteTask(task.ID)) })
	findingID := insertLessonFinding(t, d, task.ID, "confirmed")

	n, err := d.ExtractLessonsForTask(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("extracted=%d, want 1", n)
	}
	var lessonID, companyID int64
	if err := d.QueryRow(`
		SELECT l.id, COALESCE(l.company_id,0)
		FROM rsi_lessons l
		JOIN rsi_lesson_evidence e ON e.lesson_id=l.id
		WHERE e.source_kind='finding' AND e.source_id=$1`, findingID).Scan(&lessonID, &companyID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, lessonID) })
	if companyID != 0 {
		t.Fatalf("no-company extract scoped lesson to company %d", companyID)
	}
}

func TestTaskLessonsRanksCategoryBeforeGeneric(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	category, err := d.CreateTaskCategory(fmt.Sprintf("LessonCat %d", stamp))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := d.DeleteTaskCategory(category.ID)
		cleanupErr(t, "category", err)
	})
	task, err := d.CreateTaskWithOptions("lesson category rank", "goal", TaskCreateOptions{
		Name: fmt.Sprintf("lesson-cat-%d", stamp), CategoryID: &category.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "category task", d.DeleteTask(task.ID)) })

	var catID, genericID int64
	if err := d.QueryRow(`
		INSERT INTO rsi_lessons(dedupe_key,kind,cue,action,category_id)
		VALUES ($1,'positive','xss','category prior',$2) RETURNING id`,
		fmt.Sprintf("lesson-cat-%d", stamp), category.ID).Scan(&catID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, catID) })
	// Generic last: without the category rank term, id DESC would put this first.
	if err := d.QueryRow(`
		INSERT INTO rsi_lessons(dedupe_key,kind,cue,action)
		VALUES ($1,'positive','xss','generic prior') RETURNING id`,
		fmt.Sprintf("lesson-cat-generic-%d", stamp)).Scan(&genericID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, genericID) })

	got, err := d.TaskLessons(t.Context(), task.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("categorized task saw no lessons")
	}
	if got[0].ID != catID {
		t.Fatalf("category-scoped lesson should rank first, got id=%d category=%d (want %d)",
			got[0].ID, got[0].CategoryID, catID)
	}
	var sawCat, sawGeneric bool
	for _, l := range got {
		switch l.ID {
		case catID:
			sawCat = true
		case genericID:
			sawGeneric = true
		}
	}
	if !sawCat || !sawGeneric {
		t.Fatalf("want category and generic lessons, sawCat=%v sawGeneric=%v", sawCat, sawGeneric)
	}
}

func TestLogLessonUsageRecordsOnlyRendered(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	task, err := d.CreateTaskWithOptions("lesson usage", "goal", TaskCreateOptions{
		Name: fmt.Sprintf("lesson-usage-%d", stamp),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "usage task", d.DeleteTask(task.ID)) })

	var firstID, secondID int64
	if err := d.QueryRow(`
		INSERT INTO rsi_lessons(dedupe_key,kind,cue,action)
		VALUES ($1,'positive','xss',$2) RETURNING id`,
		fmt.Sprintf("lesson-usage-a-%d", stamp),
		"Confirmed xss: "+strings.Repeat("a", 80)).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, firstID) })
	if err := d.QueryRow(`
		INSERT INTO rsi_lessons(dedupe_key,kind,cue,action)
		VALUES ($1,'negative','ssrf',$2) RETURNING id`,
		fmt.Sprintf("lesson-usage-b-%d", stamp),
		"Do not re-report: "+strings.Repeat("b", 80)).Scan(&secondID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, secondID) })

	lessons := []Lesson{
		{ID: firstID, Kind: "positive", Cue: "xss", Action: "Confirmed xss: " + strings.Repeat("a", 80), Support: 1},
		{ID: secondID, Kind: "negative", Cue: "ssrf", Action: "Do not re-report: " + strings.Repeat("b", 80), Support: 1},
	}
	_, ids := RenderLessons(lessons, 250)
	if len(ids) != 1 || ids[0] != firstID {
		t.Fatalf("rendered IDs=%v, want only %d", ids, firstID)
	}
	if err := d.LogLessonUsage(t.Context(), task.ID, ids, "shadow"); err != nil {
		t.Fatal(err)
	}

	rows, err := d.Query(`SELECT lesson_id FROM rsi_lesson_usage WHERE task_id=$1 ORDER BY lesson_id`, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var logged []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		logged = append(logged, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(logged) != 1 || logged[0] != firstID {
		t.Fatalf("usage log=%v, want only rendered lesson %d (dropped %d)", logged, firstID, secondID)
	}
}

func insertLessonFinding(t *testing.T, d *DB, taskID int64, status string) int64 {
	t.Helper()
	var id int64
	if err := d.QueryRow(`
		INSERT INTO findings(task_id,vulnclass,name,summary,severity,status)
		VALUES ($1,'xss','Reflected XSS','reflected in search','high',$2)
		RETURNING id`, taskID, status).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM findings WHERE id=$1`, id) })
	return id
}

func TestExtractLessonsRetractsReversedDisposition(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	task, err := d.CreateTask(fmt.Sprintf("lesson retract %d", stamp), "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "task", d.DeleteTask(task.ID)) })

	cue := fmt.Sprintf("xss-retract-%d", stamp)
	label := fmt.Sprintf("reflected in search %d", stamp)
	var fid int64
	if err := d.QueryRow(`
		INSERT INTO findings(task_id,vulnclass,name,summary,severity,status)
		VALUES ($1,$2,'Reflected XSS',$3,'high','confirmed')
		RETURNING id`, task.ID, cue, label).Scan(&fid); err != nil {
		t.Fatal(err)
	}

	n, err := d.ExtractLessonsForTask(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("first extract=%d, want 1", n)
	}
	first, err := d.TaskLessons(t.Context(), task.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	var pos *Lesson
	for i := range first {
		if first[i].Kind == "positive" && strings.Contains(first[i].Action, label) {
			pos = &first[i]
			break
		}
	}
	if pos == nil {
		t.Fatalf("no positive lesson: %+v", first)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, pos.ID) })
	posID := pos.ID

	if _, err := d.SetFindingStatus(fid, FindingFalsePositive); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExtractLessonsForTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	var posStatus string
	var posSupport int
	if err := d.QueryRow(`SELECT status,
		(SELECT COUNT(*) FROM rsi_lesson_evidence e WHERE e.lesson_id=rsi_lessons.id)
		FROM rsi_lessons WHERE id=$1`, posID).Scan(&posStatus, &posSupport); err != nil {
		t.Fatal(err)
	}
	if posStatus != "retired" || posSupport != 0 {
		t.Fatalf("positive after FP: status=%s support=%d", posStatus, posSupport)
	}

	afterFP, err := d.TaskLessons(t.Context(), task.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	var neg *Lesson
	for i := range afterFP {
		if afterFP[i].ID == posID {
			t.Fatal("retired positive still selected")
		}
		if afterFP[i].Kind == "negative" && strings.Contains(afterFP[i].Action, label) {
			neg = &afterFP[i]
		}
	}
	if neg == nil {
		t.Fatalf("no negative lesson after FP: %+v", afterFP)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, neg.ID) })

	if _, err := d.SetFindingStatus(fid, FindingConfirmed); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExtractLessonsForTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	if err := d.QueryRow(`SELECT status,
		(SELECT COUNT(*) FROM rsi_lesson_evidence e WHERE e.lesson_id=rsi_lessons.id)
		FROM rsi_lessons WHERE id=$1`, posID).Scan(&posStatus, &posSupport); err != nil {
		t.Fatal(err)
	}
	if posStatus != "candidate" || posSupport != 1 {
		t.Fatalf("positive after re-confirm: status=%s support=%d", posStatus, posSupport)
	}

	var negStatus string
	var negSupport int
	if err := d.QueryRow(`SELECT status,
		(SELECT COUNT(*) FROM rsi_lesson_evidence e WHERE e.lesson_id=rsi_lessons.id)
		FROM rsi_lessons WHERE id=$1`, neg.ID).Scan(&negStatus, &negSupport); err != nil {
		t.Fatal(err)
	}
	if negStatus != "retired" || negSupport != 0 {
		t.Fatalf("negative after re-confirm: status=%s support=%d", negStatus, negSupport)
	}
}

func TestExtractLessonsRetireOnlyWhenLastEvidence(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	task, err := d.CreateTask(fmt.Sprintf("lesson shared retract %d", stamp), "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "task", d.DeleteTask(task.ID)) })

	cue := fmt.Sprintf("xss-shared-%d", stamp)
	label := fmt.Sprintf("reflected in search %d", stamp)
	var aID, bID int64
	if err := d.QueryRow(`
		INSERT INTO findings(task_id,vulnclass,name,summary,severity,status)
		VALUES ($1,$2,'Reflected XSS',$3,'high','confirmed')
		RETURNING id`, task.ID, cue, label).Scan(&aID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`
		INSERT INTO findings(task_id,vulnclass,name,summary,severity,status)
		VALUES ($1,$2,'Reflected XSS',$3,'high','confirmed')
		RETURNING id`, task.ID, cue, label).Scan(&bID); err != nil {
		t.Fatal(err)
	}

	if _, err := d.ExtractLessonsForTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	lessons, err := d.TaskLessons(t.Context(), task.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	var pos *Lesson
	for i := range lessons {
		if lessons[i].Kind == "positive" && strings.Contains(lessons[i].Action, label) {
			pos = &lessons[i]
			break
		}
	}
	if pos == nil {
		t.Fatalf("no shared positive lesson: %+v", lessons)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, pos.ID) })
	if pos.Support != 2 {
		t.Fatalf("shared support=%d, want 2", pos.Support)
	}

	if _, err := d.SetFindingStatus(aID, FindingFalsePositive); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExtractLessonsForTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	var status string
	var support int
	if err := d.QueryRow(`SELECT status,
		(SELECT COUNT(*) FROM rsi_lesson_evidence e WHERE e.lesson_id=rsi_lessons.id)
		FROM rsi_lessons WHERE id=$1`, pos.ID).Scan(&status, &support); err != nil {
		t.Fatal(err)
	}
	if status == "retired" || support != 1 {
		t.Fatalf("shared positive should stay active with support 1, got status=%s support=%d", status, support)
	}

	after, err := d.TaskLessons(t.Context(), task.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	var sawNeg bool
	for i := range after {
		if after[i].ID == pos.ID && after[i].Kind == "positive" {
			if after[i].Support != 1 {
				t.Fatalf("selected support=%d, want 1", after[i].Support)
			}
		}
		if after[i].Kind == "negative" && strings.Contains(after[i].Action, label) {
			sawNeg = true
			t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, after[i].ID) })
		}
	}
	if !sawNeg {
		t.Fatalf("expected a negative lesson for the flipped finding: %+v", after)
	}
}

func TestExtractLessonsRetractsAllRetests(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	task, err := d.CreateTask(fmt.Sprintf("lesson all retests %d", stamp), "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "task", d.DeleteTask(task.ID)) })

	cue := fmt.Sprintf("xss-all-retest-%d", stamp)
	label := fmt.Sprintf("reflected in search %d", stamp)
	var fid int64
	if err := d.QueryRow(`
		INSERT INTO findings(task_id,vulnclass,name,summary,severity,status)
		VALUES ($1,$2,'Reflected XSS',$3,'high','confirmed')
		RETURNING id`, task.ID, cue, label).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM findings WHERE id=$1`, fid) })

	r1 := insertLessonRetest(t, d, fid, "reproduced")
	if _, err := d.ExtractLessonsForTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	reproduced := findLessonByAction(t, d, task.ID, "positive", "Reproduced", label)
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, reproduced.ID) })
	confirmed := findLessonByAction(t, d, task.ID, "positive", "Confirmed", label)
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, confirmed.ID) })
	if positiveRetestEvidence(t, d, r1) != 1 {
		t.Fatal("first retest was not extracted")
	}

	r2 := insertLessonRetest(t, d, fid, "fixed")
	if _, err := d.ExtractLessonsForTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	caution := findLessonByAction(t, d, task.ID, "caution", "Recheck", cue)
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, caution.ID) })
	if positiveRetestEvidence(t, d, r1) != 1 {
		t.Fatal("older reproduced retest lost its evidence after a later fixed retest")
	}

	if _, err := d.SetFindingStatus(fid, FindingFalsePositive); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExtractLessonsForTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	if n := positiveRetestEvidence(t, d, r1); n != 0 {
		t.Fatalf("older retest still has %d positive evidence rows after FP", n)
	}
	if n := positiveRetestEvidence(t, d, r2); n != 0 {
		t.Fatalf("latest retest still has %d positive evidence rows after FP", n)
	}
	status, support := lessonStatusSupport(t, d, reproduced.ID)
	if status != "retired" || support != 0 {
		t.Fatalf("reproduced retest lesson after FP: status=%s support=%d", status, support)
	}
	status, support = lessonStatusSupport(t, d, confirmed.ID)
	if status != "retired" || support != 0 {
		t.Fatalf("confirmed lesson after FP: status=%s support=%d", status, support)
	}
	neg := findLessonByAction(t, d, task.ID, "negative", "Do not re-report", label)
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, neg.ID) })
}

func TestExtractLessonsDoesNotUnretireWithEvidence(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	task, err := d.CreateTask(fmt.Sprintf("lesson keep retired %d", stamp), "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "task", d.DeleteTask(task.ID)) })

	cue := fmt.Sprintf("xss-keep-retired-%d", stamp)
	label := fmt.Sprintf("reflected in search %d", stamp)
	var fid int64
	if err := d.QueryRow(`
		INSERT INTO findings(task_id,vulnclass,name,summary,severity,status)
		VALUES ($1,$2,'Reflected XSS',$3,'high','confirmed')
		RETURNING id`, task.ID, cue, label).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM findings WHERE id=$1`, fid) })

	if _, err := d.ExtractLessonsForTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	pos := findLessonByAction(t, d, task.ID, "positive", "Confirmed", label)
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, pos.ID) })

	if _, err := d.Exec(`UPDATE rsi_lessons SET status='retired' WHERE id=$1`, pos.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExtractLessonsForTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	status, support := lessonStatusSupport(t, d, pos.ID)
	if status != "retired" {
		t.Fatalf("operator-retired lesson with evidence became %s", status)
	}
	if support != 1 {
		t.Fatalf("support=%d, want 1", support)
	}
}

func TestExtractLessonsLeavesAdopted(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	task, err := d.CreateTask(fmt.Sprintf("lesson keep adopted %d", stamp), "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "task", d.DeleteTask(task.ID)) })

	cue := fmt.Sprintf("xss-keep-adopted-%d", stamp)
	label := fmt.Sprintf("reflected in search %d", stamp)
	var fid int64
	if err := d.QueryRow(`
		INSERT INTO findings(task_id,vulnclass,name,summary,severity,status)
		VALUES ($1,$2,'Reflected XSS',$3,'high','confirmed')
		RETURNING id`, task.ID, cue, label).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM findings WHERE id=$1`, fid) })

	if _, err := d.ExtractLessonsForTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	pos := findLessonByAction(t, d, task.ID, "positive", "Confirmed", label)
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, pos.ID) })

	if _, err := d.Exec(`UPDATE rsi_lessons SET status='adopted' WHERE id=$1`, pos.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExtractLessonsForTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	status, _ := lessonStatusSupport(t, d, pos.ID)
	if status != "adopted" {
		t.Fatalf("adopted lesson became %s", status)
	}
}

func TestExtractLessonsReopenRetractsNothing(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	task, err := d.CreateTask(fmt.Sprintf("lesson reopen %d", stamp), "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "task", d.DeleteTask(task.ID)) })

	cue := fmt.Sprintf("xss-reopen-%d", stamp)
	label := fmt.Sprintf("reflected in search %d", stamp)
	var fid int64
	if err := d.QueryRow(`
		INSERT INTO findings(task_id,vulnclass,name,summary,severity,status)
		VALUES ($1,$2,'Reflected XSS',$3,'high','confirmed')
		RETURNING id`, task.ID, cue, label).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM findings WHERE id=$1`, fid) })

	if _, err := d.ExtractLessonsForTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	pos := findLessonByAction(t, d, task.ID, "positive", "Confirmed", label)
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM rsi_lessons WHERE id=$1`, pos.ID) })

	if _, err := d.SetFindingStatus(fid, FindingPending); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExtractLessonsForTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	status, support := lessonStatusSupport(t, d, pos.ID)
	if status != "candidate" || support != 1 {
		t.Fatalf("reopened finding retracted the lesson: status=%s support=%d", status, support)
	}
}

func insertLessonRetest(t *testing.T, d *DB, findingID int64, verdict string) int64 {
	t.Helper()
	var id int64
	if err := d.QueryRow(`
		INSERT INTO finding_retests(finding_id,status,verdict,snapshot)
		VALUES ($1,'completed',$2,'{}'::jsonb)
		RETURNING id`, findingID, verdict).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM finding_retests WHERE id=$1`, id) })
	return id
}

func findLessonByAction(t *testing.T, d *DB, taskID int64, kind, actionPrefix, label string) *Lesson {
	t.Helper()
	lessons, err := d.TaskLessons(t.Context(), taskID, 8)
	if err != nil {
		t.Fatal(err)
	}
	for i := range lessons {
		if lessons[i].Kind == kind && strings.Contains(lessons[i].Action, actionPrefix) && strings.Contains(lessons[i].Action, label) {
			return &lessons[i]
		}
	}
	t.Fatalf("no %s lesson with %q and %q: %+v", kind, actionPrefix, label, lessons)
	return nil
}

func positiveRetestEvidence(t *testing.T, d *DB, retestID int64) int {
	t.Helper()
	var n int
	if err := d.QueryRow(`
		SELECT COUNT(*) FROM rsi_lesson_evidence e
		JOIN rsi_lessons l ON l.id=e.lesson_id
		WHERE e.source_kind='retest' AND e.source_id=$1 AND l.kind='positive'`, retestID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func lessonStatusSupport(t *testing.T, d *DB, lessonID int64) (status string, support int) {
	t.Helper()
	if err := d.QueryRow(`SELECT status,
		(SELECT COUNT(*) FROM rsi_lesson_evidence e WHERE e.lesson_id=rsi_lessons.id)
		FROM rsi_lessons WHERE id=$1`, lessonID).Scan(&status, &support); err != nil {
		t.Fatal(err)
	}
	return status, support
}

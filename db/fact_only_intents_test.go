package db

import "testing"

// «Доказал — не зарегистрировал» в измеримой форме: интент, который дал факты и не
// дал ни одной находки. Тест держит оба случая сразу, иначе гейт зелёный на пустом
// множестве: интент А (факт, находки нет) должен попасть, интент Б (факт + находка) — нет.
func TestFactOnlyIntents(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		SkipPGUnavailable(t, err)
	}
	t.Cleanup(func() { d.Close() })

	expID, err := d.CreateExploration("fact only intents", "fact only intents")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM explorations WHERE id=$1`, expID) })
	store := d.Exploration(expID)

	// А: факт есть, находки нет — «доказал, не зарегистрировал».
	intentA, err := store.AddIntent(map[string]any{"summary": "proved X, never registered"}, 1, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	factA, err := store.AddNode(KindFact, map[string]any{"summary": "X is injectable"}, 1, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Link(intentA, RelYields, factA); err != nil {
		t.Fatal(err)
	}

	// Б: факт и находка — не должен попасть.
	intentB, err := store.AddIntent(map[string]any{"summary": "proved Y and registered"}, 1, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	factB, err := store.AddNode(KindFact, map[string]any{"summary": "Y is injectable"}, 1, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	findingB, err := store.AddNode(KindFinding, map[string]any{"summary": "Y finding"}, 1, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Link(intentB, RelYields, factB); err != nil {
		t.Fatal(err)
	}
	if err := store.Link(intentB, RelYields, findingB); err != nil {
		t.Fatal(err)
	}

	// В: интент вообще без фактов — тоже не должен попасть.
	intentC, err := store.AddIntent(map[string]any{"summary": "nothing yet"}, 1, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	_ = intentC

	got, err := store.FactOnlyIntents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("FactOnlyIntents returned %d intents, want 1 (only the fact-without-finding one): %+v", len(got), got)
	}
	if got[0].IntentID != intentA {
		t.Fatalf("FactOnlyIntents picked intent %d, want %d", got[0].IntentID, intentA)
	}
	if got[0].Facts != 1 {
		t.Fatalf("FactOnlyIntents facts = %d, want 1", got[0].Facts)
	}
	if got[0].Summary == "" {
		t.Fatal("FactOnlyIntents lost the intent summary — the planner cannot act on an id alone")
	}

	// Порог по умолчанию: limit<=0 не должен возвращать пусто.
	if many, err := store.FactOnlyIntents(0); err != nil || len(many) != 1 {
		t.Fatalf("FactOnlyIntents(0) = (%d, %v), want 1 row via the default cap", len(many), err)
	}
}

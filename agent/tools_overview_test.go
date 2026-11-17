package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Autumn-27/artex/db"
)

func TestOverviewTextBudgetIsFairAndUTF8Safe(t *testing.T) {
	if got := relatedOverviewBudgetForSources(1); got != relatedOverviewMaxTextPerSource {
		t.Fatalf("single source budget=%d want=%d", got, relatedOverviewMaxTextPerSource)
	}
	perSource := relatedOverviewBudgetForSources(db.MaxTaskSourceCount)
	if perSource*db.MaxTaskSourceCount > relatedOverviewTotalTextRunes {
		t.Fatalf("aggregate budget exceeded: per_source=%d", perSource)
	}

	budget := overviewTextBudget{remaining: 5}
	got := budget.take(strings.Repeat("α", 10), 20)
	if !utf8.ValidString(got) {
		t.Fatalf("budget truncation produced invalid UTF-8: %q", got)
	}
	if utf8.RuneCountInString(got) != 5 || !budget.truncated || budget.remaining != 0 {
		t.Fatalf("unexpected truncation: got=%q budget=%+v", got, budget)
	}
	if tail := budget.take("more", 20); tail != "" {
		t.Fatalf("exhausted budget returned more text: %q", tail)
	}
}

func TestCompactFindingIncludesStatus(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"summary": "SQLi on /search", "vulnclass": "sqli", "severity": "high", "status": "false_positive",
	})
	m := compactFinding(&db.Node{ID: 7, State: "confirmed", Payload: payload})
	if m["state"] != "confirmed" {
		t.Fatalf("node state: %#v", m["state"])
	}
	if m["status"] != "false_positive" {
		t.Fatalf("status: %#v", m["status"])
	}
	if m["vulnclass"] != "sqli" || m["severity"] != "high" || m["summary"] != "SQLi on /search" {
		t.Fatalf("fields: %#v", m)
	}

	plain, _ := json.Marshal(map[string]any{"summary": "x"})
	m = compactFinding(&db.Node{ID: 8, State: "confirmed", Payload: plain})
	if _, ok := m["status"]; ok {
		t.Fatalf("empty payload should omit status: %#v", m)
	}
}

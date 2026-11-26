package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// The cost ledger: profile prices (EUR per 1M tokens) turn token buckets into
// EUR on the usage aggregation; unpriced profiles stay nil.
func TestCostLedgerEurOnUsage(t *testing.T) {
	skipWithoutDSN(t)
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	if m.pg == nil {
		db.SkipNoPG(t)
	}

	pid, err := m.pg.SaveProfile(&db.LLMProfile{
		Name: "cost-test", Format: "openai", Model: "m", APIKey: "k",
		PriceInputPerM: 1.5, PriceOutputPerM: 4.0, PriceCacheReadPerM: 0.15,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec("DELETE FROM llm_profiles WHERE id=$1", pid) })

	stored, err := m.pg.ProfileByID(pid)
	if err != nil || stored == nil {
		t.Fatalf("ProfileByID: %v %v", stored, err)
	}
	if stored.PriceInputPerM != 1.5 || stored.PriceOutputPerM != 4.0 || stored.PriceCacheReadPerM != 0.15 || stored.PriceCacheWritePerM != 0 {
		t.Fatalf("prices did not round-trip: %+v", *stored)
	}

	if _, err := m.pg.Exec(`INSERT INTO llm_usage(task_id, profile_name, model, input_tokens, output_tokens, cache_read, cache_write, ts)
		VALUES ('', 'cost-test', 'm', 2000000, 500000, 1000000, 0, now())`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM llm_usage WHERE profile_name='cost-test'`) })

	profiles, err := m.pg.UsageByProfile()
	if err != nil {
		t.Fatal(err)
	}
	var got *float64
	for _, p := range profiles {
		if p.ProfileName == "cost-test" {
			got = p.CostEur
		}
	}
	if got == nil {
		t.Fatal("priced profile has no cost_eur")
	}
	want := 1.0*1.5 + 0.5*4.0 + 1.0*0.15 // fresh input 1M (2M incl. 1M cached) + 0.5M out + 1M cache-read = 3.65
	if d := *got - want; d > 0.001 || d < -0.001 {
		t.Fatalf("cost_eur = %v, want %v", *got, want)
	}
}

// Prices saved through the profile API come back in the profile list, so the
// settings dialog can show them and send them back unchanged on the next save.
// A negative price is stored as unset.
func TestProfilePricesThroughAPI(t *testing.T) {
	skipWithoutDSN(t)
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	if m.pg == nil {
		db.SkipNoPG(t)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())

	body, _ := json.Marshal(map[string]any{
		"name": "price-api-test", "format": "openai", "model": "m", "api_key": "k",
		"price_input_per_m": 2.5, "price_output_per_m": 10, "price_cache_read_per_m": -1,
	})
	rec := httptest.NewRecorder()
	s.pgSaveProfile(rec, httptest.NewRequest(http.MethodPost, "/api/llm/profiles", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("save: HTTP %d %s", rec.Code, rec.Body.String())
	}
	var saved struct{ ID int64 }
	_ = json.Unmarshal(rec.Body.Bytes(), &saved)
	t.Cleanup(func() { _, _ = m.pg.Exec("DELETE FROM llm_profiles WHERE id=$1", saved.ID) })

	listed := func() LLMProfileDTO {
		t.Helper()
		rec := httptest.NewRecorder()
		s.pgListProfiles(rec, httptest.NewRequest(http.MethodGet, "/api/llm/profiles", nil))
		var list struct{ Profiles []LLMProfileDTO }
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		for _, p := range list.Profiles {
			if p.ID == strconv.FormatInt(saved.ID, 10) {
				return p
			}
		}
		t.Fatal("saved profile missing from the list")
		return LLMProfileDTO{}
	}
	check := func(when string) {
		t.Helper()
		p := listed()
		if p.PriceInputPerM != 2.5 || p.PriceOutputPerM != 10 || p.PriceCacheReadPerM != 0 || p.PriceCacheWritePerM != 0 {
			t.Fatalf("%s: listed prices = %v %v %v %v; want 2.5 10 0 0", when, p.PriceInputPerM, p.PriceOutputPerM, p.PriceCacheReadPerM, p.PriceCacheWritePerM)
		}
	}
	check("after create")

	// An API save that leaves the prices out keeps them.
	body, _ = json.Marshal(map[string]any{"id": saved.ID, "name": "price-api-test", "format": "openai", "model": "m2"})
	rec = httptest.NewRecorder()
	s.pgSaveProfile(rec, httptest.NewRequest(http.MethodPost, "/api/llm/profiles", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("update: HTTP %d %s", rec.Code, rec.Body.String())
	}
	check("after an update without prices")
}

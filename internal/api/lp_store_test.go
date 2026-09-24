package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"eve-flipper/internal/esi"
)

func lpContract(id int32, price, volume float64) esi.PublicContract {
	return esi.PublicContract{
		ContractID:  id,
		Type:        "item_exchange",
		Price:       price,
		Volume:      volume,
		DateExpired: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
	}
}

func bpcItem(typeID int32, runs int) esi.ContractItem {
	return esi.ContractItem{TypeID: typeID, Quantity: 1, IsIncluded: true, IsBlueprintCopy: true, Runs: runs}
}

func TestLPBPCCandidateContractsFiltersByShape(t *testing.T) {
	expired := lpContract(5, 10, 0.01)
	expired.DateExpired = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	auction := lpContract(6, 10, 0.01)
	auction.Type = "auction"

	got := lpBPCCandidateContracts([]esi.PublicContract{
		lpContract(1, 40e6, 0.01), // a lone BPC
		lpContract(2, 40e6, 0.02), // two items: too big to be one BPC
		lpContract(3, 0, 0.01),    // no price
		lpContract(4, 40e6, 2500), // a ship
		expired,
		auction,
	})
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("candidates = %v, want [1]", got)
	}
}

func TestLPBPCPricesFromContracts(t *testing.T) {
	const raven, scorpion int32 = 17637, 32310
	contracts := []esi.PublicContract{
		lpContract(1, 400e6, 0.01), // 10 runs -> 40M/run
		lpContract(2, 36e6, 0.01),  // 1 run  -> 36M/run
		lpContract(3, 1e12, 0.01),  // absurd, but the median shrugs it off
		lpContract(4, 50e6, 0.01),  // a BPO, not a copy: ignored
		lpContract(5, 20e6, 0.01),  // not included (a "want" item): ignored
		lpContract(6, 20e6, 0.01),  // two items in one contract: ignored
		lpContract(7, 30e6, 0.01),  // scorpion
		lpContract(8, 99e6, 0.01),  // zero runs reported: ignored
	}
	bpo := bpcItem(raven, 0)
	bpo.IsBlueprintCopy = false
	wanted := bpcItem(raven, 1)
	wanted.IsIncluded = false
	items := map[int32][]esi.ContractItem{
		1: {bpcItem(raven, 10)},
		2: {bpcItem(raven, 1)},
		3: {bpcItem(raven, 1)},
		4: {bpo},
		5: {wanted},
		6: {bpcItem(raven, 1), bpcItem(raven, 1)},
		7: {bpcItem(scorpion, 1)},
		8: {bpcItem(raven, 0)},
	}
	got := lpBPCPricesFromContracts(contracts, items, map[int32]bool{raven: true, scorpion: true})

	r := got[raven]
	if r.Samples != 3 || r.PerRun != 40e6 {
		t.Fatalf("raven = %+v, want median 40M over 3 samples", r)
	}
	if s := got[scorpion]; s.Samples != 1 || s.PerRun != 30e6 {
		t.Fatalf("scorpion = %+v", s)
	}
	if _, ok := got[999]; ok {
		t.Fatal("an unwanted type must not appear")
	}
}

func TestLPQuoteFromBooks(t *testing.T) {
	sell := []esi.MarketOrder{{Price: 110, VolumeRemain: 3}, {Price: 100, VolumeRemain: 2}, {Price: 90, VolumeRemain: 0}}
	buy := []esi.MarketOrder{{Price: 80, VolumeRemain: 5}, {Price: 85, VolumeRemain: 1}}
	q := lpQuoteFromBooks(sell, buy)
	if q.BestAsk != 100 || q.BestBid != 85 || q.AskDepth != 5 || q.BidDepth != 6 {
		t.Fatalf("quote = %+v", q)
	}
	empty := lpQuoteFromBooks(nil, nil)
	if empty.BestAsk != 0 || empty.BestBid != 0 {
		t.Fatalf("an empty book must quote zero, got %+v", empty)
	}
}

func TestLPAvgDailyVolume(t *testing.T) {
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	day := func(n int) string { return now.AddDate(0, 0, -n).Format("2006-01-02") }
	entries := []esi.HistoryEntry{
		{Date: day(1), Volume: 10},
		{Date: day(2), Volume: 20},
		{Date: day(40), Volume: 1000}, // outside the window
	}
	got := lpAvgDailyVolume(entries, 30, now)
	if math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("got %v, want 30 units over a 30-day window = 1.0/day", got)
	}
	if lpAvgDailyVolume(nil, 30, now) != 0 {
		t.Fatal("no history is zero volume")
	}
}

func TestHostedQuotaClassifiesLPAnalyze(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/lp/analyze", nil)
	feature, metered := hostedQuotaFeatureForRequest(r)
	if !metered || feature != "scans" {
		t.Fatalf("POST /api/lp/analyze = (%q, %v), want (scans, true)", feature, metered)
	}
}

func TestLPBPCPriceOverrideRoutes(t *testing.T) {
	database := openAPITestDB(t)
	const userID = "user-lp-overrides"
	srv := newAuthedIndustryTestServer(t, database, userID)

	put := func(body string) map[string]map[string]float64 {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.handleLPBPCPricesPut(rec, requestWithUserID(http.MethodPut, "/api/auth/lp/bpc-prices", strings.NewReader(body), userID))
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT %s -> %d %s", body, rec.Code, rec.Body.String())
		}
		var out map[string]map[string]float64
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	got := put(`{"type_id": 17637, "price_per_run": 38500000}`)
	if got["prices"]["17637"] != 38_500_000 {
		t.Fatalf("after set: %v", got)
	}
	got = put(`{"type_id": 17637, "price_per_run": null}`)
	if _, ok := got["prices"]["17637"]; ok {
		t.Fatalf("a null price must clear the override: %v", got)
	}

	rec := httptest.NewRecorder()
	srv.handleLPBPCPricesPut(rec, requestWithUserID(http.MethodPut, "/api/auth/lp/bpc-prices", strings.NewReader(`{"price_per_run": 5}`), userID))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing type_id -> %d, want 400", rec.Code)
	}
}

func TestLPBalancesUnavailableWhenLoggedOut(t *testing.T) {
	srv := &Server{}
	rec := httptest.NewRecorder()
	srv.handleLPBalances(rec, requestWithUserID(http.MethodGet, "/api/auth/lp/balances", nil, "nobody"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: the tab falls back to typed LP, so this is never an error", rec.Code)
	}
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["available"] != false || out["reason"] != "not_logged_in" {
		t.Fatalf("got %v", out)
	}
}

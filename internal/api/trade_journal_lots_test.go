package api

import (
	"testing"

	"eve-flipper/internal/engine"
)

// The lots endpoint answers two different questions from one set of rows: one
// item's FIFO matches (the Summary drawer) and every sale in the window (the
// transactions view). These tests pin the difference, because widening the
// endpoint is only safe if the drawer's behaviour is unchanged.

func lotsFixture() []engine.TradeJournalLot {
	return []engine.TradeJournalLot{
		{TypeID: 34, TypeName: "Tritanium", Source: engine.LotSourceTrade,
			SellDate: "2026-09-01T10:00:00Z", NetProfit: 5_000_000},
		{TypeID: 34, TypeName: "Tritanium", Source: engine.LotSourceTrade,
			SellDate: "2026-09-04T10:00:00Z", NetProfit: 400},
		{TypeID: 12005, TypeName: "Ishtar", Source: engine.LotSourceManufacture,
			SellDate: "2026-09-03T10:00:00Z", NetProfit: 40_000_000},
		{TypeID: 11399, TypeName: "Morphite", Source: engine.LotSourceOrphan,
			SellDate: "2026-09-05T10:00:00Z", NetProfit: 0},
		{TypeID: 587, TypeName: "Rifter", Source: engine.LotSourceTrade,
			SellDate: "2026-09-02T10:00:00Z", NetProfit: -9_000_000},
	}
}

func TestFilterJournalLotsByTypeIsUnchangedByTheNewFilters(t *testing.T) {
	// The drawer's contract: every match for the item, in match order, uncapped.
	// A limit is still passed by the handler, so it must be ignored here.
	got, total := filterJournalLots(lotsFixture(), lotFilter{typeID: 34, limit: 1})
	if total != 2 || len(got) != 2 {
		t.Fatalf("want 2 lots and total 2, got %d lots total %d", len(got), total)
	}
	if got[0].SellDate != "2026-09-01T10:00:00Z" {
		t.Errorf("match order not preserved: first lot is %q", got[0].SellDate)
	}
	for _, l := range got {
		if l.TypeID != 34 {
			t.Errorf("type filter leaked type %d", l.TypeID)
		}
	}
}

func TestFilterJournalLotsWithoutTypeReturnsEverySourceNewestFirst(t *testing.T) {
	got, total := filterJournalLots(lotsFixture(), lotFilter{limit: 100})
	if total != 5 || len(got) != 5 {
		t.Fatalf("want all 5 lots, got %d total %d", len(got), total)
	}
	want := []string{
		"2026-09-05T10:00:00Z",
		"2026-09-04T10:00:00Z",
		"2026-09-03T10:00:00Z",
		"2026-09-02T10:00:00Z",
		"2026-09-01T10:00:00Z",
	}
	for i, w := range want {
		if got[i].SellDate != w {
			t.Errorf("row %d: want %s, got %s", i, w, got[i].SellDate)
		}
	}
}

func TestFilterJournalLotsBySource(t *testing.T) {
	for _, tc := range []struct {
		source engine.LotSource
		want   int
	}{
		{engine.LotSourceTrade, 3},
		{engine.LotSourceManufacture, 1},
		{engine.LotSourceOrphan, 1},
		{"", 5},
	} {
		got, total := filterJournalLots(lotsFixture(), lotFilter{source: tc.source, limit: 100})
		if len(got) != tc.want || total != tc.want {
			t.Errorf("source %q: want %d rows, got %d (total %d)", tc.source, tc.want, len(got), total)
		}
		for _, l := range got {
			if tc.source != "" && l.Source != tc.source {
				t.Errorf("source %q filter returned a %q row", tc.source, l.Source)
			}
		}
	}
}

func TestFilterJournalLotsByNameIsCaseInsensitiveSubstring(t *testing.T) {
	got, _ := filterJournalLots(lotsFixture(), lotFilter{nameQuery: "trit", limit: 100})
	if len(got) != 2 {
		t.Fatalf("want 2 Tritanium rows, got %d", len(got))
	}
	if none, _ := filterJournalLots(lotsFixture(), lotFilter{nameQuery: "nosuchitem", limit: 100}); len(none) != 0 {
		t.Errorf("want no rows for an unmatched name, got %d", len(none))
	}
}

func TestFilterJournalLotsMinProfitKeepsLossesOfTheSameSize(t *testing.T) {
	// The filter asks "what moved the needle", so a big loss has to survive it.
	got, _ := filterJournalLots(lotsFixture(), lotFilter{minProfit: 1_000_000, limit: 100})
	if len(got) != 3 {
		t.Fatalf("want 3 rows over 1M either way, got %d", len(got))
	}
	sawLoss := false
	for _, l := range got {
		if l.NetProfit < 0 {
			sawLoss = true
		}
		if l.NetProfit > -1_000_000 && l.NetProfit < 1_000_000 {
			t.Errorf("row under the floor survived: %.0f", l.NetProfit)
		}
	}
	if !sawLoss {
		t.Error("min_profit dropped the 9M loss; it is exactly the row worth seeing")
	}
}

func TestFilterJournalLotsCapsRowsButReportsThePreCapCount(t *testing.T) {
	got, total := filterJournalLots(lotsFixture(), lotFilter{limit: 2})
	if len(got) != 2 {
		t.Fatalf("want 2 rows after the cap, got %d", len(got))
	}
	// The UI says "showing 2 of 5"; a total derived from the slice would say 2.
	if total != 5 {
		t.Errorf("want pre-cap total 5, got %d", total)
	}
	// The cap keeps the newest rows, not an arbitrary slice.
	if got[0].SellDate != "2026-09-05T10:00:00Z" || got[1].SellDate != "2026-09-04T10:00:00Z" {
		t.Errorf("cap did not keep the most recent rows: %s, %s", got[0].SellDate, got[1].SellDate)
	}
}

func TestParseLotFilterSourceParam(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    engine.LotSource
		wantErr bool
	}{
		{"", "", false},
		{"combined", "", false},
		{"all", "", false},
		{"trade", engine.LotSourceTrade, false},
		{"manufacture", engine.LotSourceManufacture, false},
		// The analytics endpoint's parser rejects orphan; the transaction list
		// has to be able to isolate unmatched sells.
		{"orphan", engine.LotSourceOrphan, false},
		{"Trade", "", true},
		{"nonsense", "", true},
	} {
		got, err := parseLotFilterSourceParam(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("%q: err = %v, wantErr %v", tc.in, err, tc.wantErr)
		}
		if err == nil && got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

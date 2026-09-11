package engine

import (
	"reflect"
	"testing"
	"time"

	"eve-flipper/internal/esi"
)

// journalTxnsFrom converts the legacy engine's input into the journal engine's,
// so both can be fed byte-identical trade flow.
func journalTxnsFrom(txns []esi.WalletTransaction) []JournalTxn {
	out := make([]JournalTxn, 0, len(txns))
	for _, tx := range txns {
		out = append(out, JournalTxn{
			WalletKey:     "char:1",
			TransactionID: tx.TransactionID,
			Date:          tx.Date,
			TypeID:        tx.TypeID,
			TypeName:      tx.TypeName,
			UnitPrice:     tx.UnitPrice,
			Quantity:      tx.Quantity,
			IsBuy:         tx.IsBuy,
			LocationID:    tx.LocationID,
			LocationName:  tx.LocationName,
		})
	}
	return out
}

// TestToPortfolioPnL_MatchesLegacyEngineWithoutJobs is the anti-drift test, and
// the reason the two-engine refactor is safe to keep.
//
// Given the same transactions and zero industry jobs, the journal engine's
// projection must equal ComputePortfolioPnLWithOptions field for field. If the
// two matchers can ever disagree about a plain trading flow again, this fails.
func TestToPortfolioPnL_MatchesLegacyEngineWithoutJobs(t *testing.T) {
	// A flow with every wrinkle the two engines could disagree about: a buy
	// older than the window that still seeds the pool, two lots consumed by one
	// sell, a partial fill leaving an open position, two stations, two items,
	// and a loss-making day so the drawdown maths has something to chew on.
	txns := []esi.WalletTransaction{
		txn(-45, 34, "Tritanium", 60003760, "Jita IV-4", true, 5.0, 1000),
		txn(-20, 34, "Tritanium", 60003760, "Jita IV-4", true, 6.0, 1000),
		txn(-18, 35, "Pyerite", 60008494, "Amarr VIII", true, 11.0, 500),
		txn(-15, 34, "Tritanium", 60008494, "Amarr VIII", false, 9.0, 1500),
		txn(-12, 35, "Pyerite", 60003760, "Jita IV-4", false, 8.0, 200),
		txn(-5, 34, "Tritanium", 60003760, "Jita IV-4", false, 7.5, 400),
	}

	opt := PortfolioPnLOptions{
		LookbackDays:         30,
		SalesTaxPercent:      3.6,
		BrokerFeePercent:     1.5,
		LedgerLimit:          500,
		IncludeUnmatchedSell: false,
	}

	legacy := ComputePortfolioPnLWithOptions(txns, opt)

	// The journal's SinceDate is the legacy engine's lookback cutoff. Every
	// transaction above sits well clear of it, so the two windows agree even
	// though the two cutoffs are computed a few microseconds apart.
	journal := ComputeTradeJournal(journalTxnsFrom(txns), nil, TradeJournalOptions{
		SinceDate:        time.Now().UTC().AddDate(0, 0, -opt.LookbackDays),
		FIFOMode:         FIFOModeStrictDate,
		SalesTaxPercent:  opt.SalesTaxPercent,
		BrokerFeePercent: opt.BrokerFeePercent,
	})
	projected := journal.ToPortfolioPnL(opt, "")

	if !reflect.DeepEqual(legacy.Summary, projected.Summary) {
		t.Errorf("summary drifted\n legacy: %+v\n  proj: %+v", legacy.Summary, projected.Summary)
	}
	if !reflect.DeepEqual(legacy.DailyPnL, projected.DailyPnL) {
		t.Errorf("daily series drifted\n legacy: %+v\n  proj: %+v", legacy.DailyPnL, projected.DailyPnL)
	}
	if !reflect.DeepEqual(legacy.TopItems, projected.TopItems) {
		t.Errorf("per-item drifted\n legacy: %+v\n  proj: %+v", legacy.TopItems, projected.TopItems)
	}
	if !reflect.DeepEqual(legacy.TopStations, projected.TopStations) {
		t.Errorf("per-station drifted\n legacy: %+v\n  proj: %+v", legacy.TopStations, projected.TopStations)
	}
	if !reflect.DeepEqual(legacy.Ledger, projected.Ledger) {
		t.Errorf("ledger drifted\n legacy: %+v\n  proj: %+v", legacy.Ledger, projected.Ledger)
	}
	if !reflect.DeepEqual(legacy.OpenPositions, projected.OpenPositions) {
		t.Errorf("open positions drifted\n legacy: %+v\n  proj: %+v", legacy.OpenPositions, projected.OpenPositions)
	}
	if !reflect.DeepEqual(legacy.Coverage, projected.Coverage) {
		t.Errorf("coverage drifted\n legacy: %+v\n  proj: %+v", legacy.Coverage, projected.Coverage)
	}
	if !reflect.DeepEqual(legacy.SlotEfficiency, projected.SlotEfficiency) {
		t.Errorf("slot efficiency drifted\n legacy: %+v\n  proj: %+v", legacy.SlotEfficiency, projected.SlotEfficiency)
	}
	if !reflect.DeepEqual(legacy.Settings, projected.Settings) {
		t.Errorf("settings drifted\n legacy: %+v\n  proj: %+v", legacy.Settings, projected.Settings)
	}
}

// TestSummarizeRealizedLedger_UnmatchedRowsAreAsymmetric pins the shape the
// extraction had to preserve: a sell with no known cost basis credits the sell
// station and QtySold only. It never credits a buy station, because there isn't
// one, and it never adds to TotalBought. Symmetrising it would invent purchases
// that never happened.
func TestSummarizeRealizedLedger_UnmatchedRowsAreAsymmetric(t *testing.T) {
	opt := normalizePortfolioOptions(PortfolioPnLOptions{LookbackDays: 30})
	ledger := []RealizedTrade{
		{
			TypeID: 34, TypeName: "Tritanium", Quantity: 100,
			BuyDate: "2026-01-01T12:00:00Z", SellDate: "2026-01-02T12:00:00Z",
			BuyLocationID: 60003760, BuyLocationName: "Jita IV-4",
			SellLocationID: 60008494, SellLocationName: "Amarr VIII",
			BuyUnitPrice: 5, SellUnitPrice: 9,
			BuyGross: 500, SellGross: 900,
			BuyTotal: 500, SellTotal: 900, RealizedPnL: 400,
		},
		{
			TypeID: 35, TypeName: "Pyerite", Quantity: 50,
			SellDate:       "2026-01-02T12:00:00Z",
			SellLocationID: 60003760, SellLocationName: "Jita IV-4",
			SellUnitPrice: 8, SellGross: 400,
			SellTotal: 400, RealizedPnL: 400,
			Unmatched: true,
		},
	}

	_, items, stations, summary := summarizeRealizedLedger(ledger, opt)

	byStation := map[int64]StationPnL{}
	for _, s := range stations {
		byStation[s.LocationID] = s
	}
	// Jita is the matched row's BUY station and the unmatched row's SELL
	// station. It must be credited with the unmatched sell's 400 and with
	// nothing from the matched row's buy leg beyond TotalBought.
	jita, ok := byStation[60003760]
	if !ok {
		t.Fatal("Jita missing from station breakdown")
	}
	if jita.TotalSold != 400 {
		t.Errorf("Jita TotalSold = %v, want 400 (the unmatched sell)", jita.TotalSold)
	}
	if jita.TotalBought != 500 {
		t.Errorf("Jita TotalBought = %v, want 500 (matched row's buy leg only)", jita.TotalBought)
	}

	amarr, ok := byStation[60008494]
	if !ok {
		t.Fatal("Amarr missing from station breakdown")
	}
	if amarr.TotalBought != 0 {
		t.Errorf("Amarr TotalBought = %v, want 0 — nothing was bought there", amarr.TotalBought)
	}

	byItem := map[int32]ItemPnL{}
	for _, i := range items {
		byItem[i.TypeID] = i
	}
	pyerite, ok := byItem[35]
	if !ok {
		t.Fatal("Pyerite missing from item breakdown")
	}
	if pyerite.QtySold != 50 {
		t.Errorf("unmatched QtySold = %d, want 50", pyerite.QtySold)
	}
	if pyerite.QtyBought != 0 {
		t.Errorf("unmatched QtyBought = %d, want 0 — there is no buy leg", pyerite.QtyBought)
	}
	if pyerite.TotalBought != 0 {
		t.Errorf("unmatched TotalBought = %v, want 0", pyerite.TotalBought)
	}

	if summary.TotalBought != 500 {
		t.Errorf("summary TotalBought = %v, want 500 (matched row only)", summary.TotalBought)
	}
	if summary.TotalSold != 1300 {
		t.Errorf("summary TotalSold = %v, want 1300 (both rows)", summary.TotalSold)
	}
}

// TestToPortfolioPnL_ManufacturedLotCarriesBuildCostBasis asserts the number
// P&L previously could not see at all: a built-then-sold item's cost basis.
//
// Before this refactor the analytics surface ran FIFO over wallet transactions
// alone, so the Rifter below had no matching buy and showed up as a zero-cost
// windfall — 2M of pure profit against an item that cost 1,000,050 to make.
func TestToPortfolioPnL_ManufacturedLotCarriesBuildCostBasis(t *testing.T) {
	// Buy 10 Trit @ 5 → run a Rifter job (10 Trit + 1M install) → sell 1 @ 2M.
	// Build cost = 1_000_000 install + 50 materials = 1_000_050.
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 34, 10, 5, true),
		mkTxn(2, "char:1", "2026-01-10", 588, 1, 2_000_000, false),
	}
	jobs := []JournalIndustryJob{
		mkJob(100, 1, 587, 588, 1, 1_000_000, "2026-01-02", "2026-01-05"),
	}
	res := ComputeTradeJournal(txns, jobs, TradeJournalOptions{
		FIFOMode:  FIFOModeStrictDate,
		Materials: rifterMaterials(),
		Products:  rifterProducts(),
		MEByJob:   meZero,
	})

	mfg := res.ToPortfolioPnL(PortfolioPnLOptions{LookbackDays: 365}, LotSourceManufacture)
	if len(mfg.Ledger) != 1 {
		t.Fatalf("want 1 manufacturing ledger row, got %d", len(mfg.Ledger))
	}
	row := mfg.Ledger[0]
	if row.TypeID != 588 {
		t.Errorf("TypeID = %d, want 588 (the product, not the blueprint)", row.TypeID)
	}
	if row.BuyTotal != 1_000_050 {
		t.Errorf("BuyTotal = %v, want 1000050 (install + materials for the matched qty)", row.BuyTotal)
	}
	if row.BuyUnitPrice != 1_000_050 {
		t.Errorf("BuyUnitPrice = %v, want 1000050 — one run, one unit", row.BuyUnitPrice)
	}
	if row.Unmatched {
		t.Error("a built item is not an unmatched sell — it has a real cost basis")
	}
	if row.RealizedPnL != 999_950 {
		t.Errorf("RealizedPnL = %v, want 999950", row.RealizedPnL)
	}

	// The source filter has to bite: the same result viewed as trading-only
	// must not carry the manufacturing row.
	trading := res.ToPortfolioPnL(PortfolioPnLOptions{LookbackDays: 365}, LotSourceTrade)
	if len(trading.Ledger) != 0 {
		t.Errorf("trading-only ledger = %d rows, want 0", len(trading.Ledger))
	}

	// And Combined must equal the journal engine's own combined total, which is
	// the invariant the merged tab's three-way selector rests on.
	combined := res.ToPortfolioPnL(PortfolioPnLOptions{LookbackDays: 365}, "")
	if combined.Summary.TotalPnL != res.Totals.CombinedPnL {
		t.Errorf("combined TotalPnL = %v, journal CombinedPnL = %v — the two must agree",
			combined.Summary.TotalPnL, res.Totals.CombinedPnL)
	}
}

// TestItemLeaderboard_SplitsTradeAndBuildOnOneRow covers the case the flat
// items table could not show: a type you both flip and build.
func TestItemLeaderboard_SplitsTradeAndBuildOnOneRow(t *testing.T) {
	// 10 Trit -> one Rifter (install 1_000_000, materials 50) available 01-05,
	// plus one Rifter bought outright at 1.2M on 01-06, then both sold at 2M.
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 34, 10, 5, true),
		mkTxn(2, "char:1", "2026-01-06", 588, 1, 1_200_000, true),
		mkTxn(3, "char:1", "2026-01-10", 588, 2, 2_000_000, false),
	}
	jobs := []JournalIndustryJob{
		mkJob(100, 1, 587, 588, 1, 1_000_000, "2026-01-02", "2026-01-05"),
	}
	res := ComputeTradeJournal(txns, jobs, TradeJournalOptions{
		FIFOMode:  FIFOModeStrictDate,
		Materials: rifterMaterials(),
		Products:  rifterProducts(),
		MEByJob:   meZero,
	})

	rows := res.ItemLeaderboard(PortfolioPnLOptions{LookbackDays: 365}, 0)
	if len(rows) != 1 {
		t.Fatalf("want 1 leaderboard row (only the Rifter was sold), got %d", len(rows))
	}
	row := rows[0]
	if row.TypeID != 588 {
		t.Fatalf("TypeID = %d, want 588", row.TypeID)
	}
	if row.TradePnL != 800_000 {
		t.Errorf("TradePnL = %v, want 800000 (2M sale on a 1.2M buy)", row.TradePnL)
	}
	if row.ManufacturePnL != 999_950 {
		t.Errorf("ManufacturePnL = %v, want 999950 (2M sale on a 1000050 build)", row.ManufacturePnL)
	}
	if got, want := row.TradePnL+row.ManufacturePnL, row.NetPnL; got != want {
		t.Errorf("halves sum to %v but NetPnL = %v — the split must be exhaustive", got, want)
	}
	if got, want := row.TradeCost+row.ManufactureCost, row.CostBasis; got != want {
		t.Errorf("cost halves sum to %v but CostBasis = %v", got, want)
	}
	if row.QtySold != 2 {
		t.Errorf("QtySold = %d, want 2", row.QtySold)
	}
	if row.ROIPercent == nil {
		t.Fatal("ROIPercent is nil, but this row has a real cost basis")
	}
	if want := row.NetPnL / row.CostBasis * 100; *row.ROIPercent != want {
		t.Errorf("ROIPercent = %v, want %v", *row.ROIPercent, want)
	}
}

// TestItemLeaderboard_OrphanSellHasNoRowOrROI pins the two ways a leaderboard
// could lie about an unpriced sell: counting it as pure profit, or reporting
// its return as 0%.
func TestItemLeaderboard_OrphanSellHasNoRowOrROI(t *testing.T) {
	// A sale with nothing behind it — stock acquired before the archive starts.
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-10", 34, 100, 9, false),
	}
	res := ComputeTradeJournal(txns, nil, TradeJournalOptions{FIFOMode: FIFOModeStrictDate})

	strict := res.ItemLeaderboard(PortfolioPnLOptions{LookbackDays: 365}, 0)
	if len(strict) != 0 {
		t.Fatalf("strict mode leaderboard = %d rows, want 0 — an unpriced sell is not a win", len(strict))
	}

	legacy := res.ItemLeaderboard(PortfolioPnLOptions{
		LookbackDays:         365,
		IncludeUnmatchedSell: true,
	}, 0)
	if len(legacy) != 1 {
		t.Fatalf("legacy mode leaderboard = %d rows, want 1", len(legacy))
	}
	if legacy[0].CostBasis != 0 {
		t.Errorf("CostBasis = %v, want 0 for an orphan sell", legacy[0].CostBasis)
	}
	if legacy[0].ROIPercent != nil {
		t.Errorf("ROIPercent = %v, want nil — an unknown denominator, not 0%%", *legacy[0].ROIPercent)
	}
}

// TestItemLeaderboard_HonoursLimit keeps the cap from being silently the
// summarizer's default 50 when the caller asked for fewer or more.
func TestItemLeaderboard_HonoursLimit(t *testing.T) {
	txns := []JournalTxn{}
	id := int64(1)
	for typeID := int32(1000); typeID < 1060; typeID++ {
		txns = append(txns,
			mkTxn(id, "char:1", "2026-01-01", typeID, 1, 100, true),
			mkTxn(id+1, "char:1", "2026-01-05", typeID, 1, float64(200+typeID), false),
		)
		id += 2
	}
	res := ComputeTradeJournal(txns, nil, TradeJournalOptions{FIFOMode: FIFOModeStrictDate})

	if got := len(res.ItemLeaderboard(PortfolioPnLOptions{LookbackDays: 365}, 0)); got != 60 {
		t.Errorf("default limit returned %d rows, want all 60 — the leaderboard is not capped at the table's 50", got)
	}
	if got := len(res.ItemLeaderboard(PortfolioPnLOptions{LookbackDays: 365}, 10)); got != 10 {
		t.Errorf("limit 10 returned %d rows, want 10", got)
	}
}

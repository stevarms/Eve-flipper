package engine

import (
	"testing"
	"time"

	"eve-flipper/internal/sde"
)

// Test helpers ------------------------------------------------------------

func mkTxn(id int64, wallet string, day string, typeID int32, qty int32, price float64, isBuy bool) JournalTxn {
	return JournalTxn{
		WalletKey:     wallet,
		TransactionID: id,
		Date:          day + "T12:00:00Z",
		TypeID:        typeID,
		Quantity:      qty,
		UnitPrice:     price,
		IsBuy:         isBuy,
	}
}

func mkJob(id int64, char int64, bpType int32, productType int32, runs int32, installCost float64, start, done string) JournalIndustryJob {
	return JournalIndustryJob{
		JobID:           id,
		CharacterID:     char,
		ActivityID:      1, // manufacturing
		BlueprintTypeID: bpType,
		ProductTypeID:   productType,
		Runs:            runs,
		InstallCost:     installCost,
		Status:          "delivered",
		StartDate:       start + "T09:00:00Z",
		CompletedDate:   done + "T18:00:00Z",
		SuccessfulRuns:  runs,
	}
}

// simple no-decryptor-style MEResolver used across tests
func meZero(_ JournalIndustryJob) MEResolution {
	return MEResolution{ME: 0, Source: "fallback"}
}

// Products/materials helpers — Rifter BP produces 1 Rifter and eats Trit only.
func rifterMaterials() map[int32][]sde.BlueprintMaterial {
	return map[int32][]sde.BlueprintMaterial{
		587: { // hypothetical Rifter BP type
			{TypeID: 34, Quantity: 10}, // 10 Tritanium per run
		},
	}
}

func rifterProducts() map[int32]sde.BlueprintProduct {
	return map[int32]sde.BlueprintProduct{
		587: {TypeID: 588, Quantity: 1},
	}
}

// Actual tests -----------------------------------------------------------

func TestComputeTradeJournal_PureBuyOnly(t *testing.T) {
	// Just a buy — should produce an open position, zero realized profit.
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 34, 100, 5, true),
	}
	res := ComputeTradeJournal(txns, nil, TradeJournalOptions{FIFOMode: FIFOModeStrictDate})
	if res.Totals.TradingPnL != 0 {
		t.Errorf("TradingPnL = %f, want 0", res.Totals.TradingPnL)
	}
	if len(res.OpenPositions) != 1 || res.OpenPositions[0].Qty != 100 || res.OpenPositions[0].TypeID != 34 {
		t.Errorf("unexpected open positions: %+v", res.OpenPositions)
	}
}

func TestComputeTradeJournal_TradingFIFO(t *testing.T) {
	// Buy 10 @ 100 then sell 5 @ 150 → one match, profit = 5×(150−100) = 250 (no fees).
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 34, 10, 100, true),
		mkTxn(2, "char:1", "2026-01-05", 34, 5, 150, false),
	}
	res := ComputeTradeJournal(txns, nil, TradeJournalOptions{FIFOMode: FIFOModeStrictDate})
	if got := res.Totals.TradingPnL; got != 250 {
		t.Errorf("TradingPnL = %f, want 250", got)
	}
	if len(res.Lots) != 1 || res.Lots[0].Source != LotSourceTrade || res.Lots[0].MatchedQty != 5 {
		t.Errorf("unexpected lot: %+v", res.Lots)
	}
}

func TestComputeTradeJournal_TwoLotsConsumed(t *testing.T) {
	// Buy 10 @ 100, buy 10 @ 120, sell 15 @ 200.
	// Strict-date FIFO consumes 10@100 + 5@120 = 10*(200-100) + 5*(200-120) = 1000 + 400 = 1400.
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 34, 10, 100, true),
		mkTxn(2, "char:1", "2026-01-02", 34, 10, 120, true),
		mkTxn(3, "char:1", "2026-01-05", 34, 15, 200, false),
	}
	res := ComputeTradeJournal(txns, nil, TradeJournalOptions{FIFOMode: FIFOModeStrictDate})
	if got := res.Totals.TradingPnL; got != 1400 {
		t.Errorf("TradingPnL = %f, want 1400", got)
	}
	if len(res.Lots) != 2 {
		t.Fatalf("want 2 lots, got %d", len(res.Lots))
	}
	if res.Lots[0].MatchedQty != 10 || res.Lots[1].MatchedQty != 5 {
		t.Errorf("wrong match sizes: %v then %v", res.Lots[0].MatchedQty, res.Lots[1].MatchedQty)
	}
}

func TestComputeTradeJournal_OrphanSell(t *testing.T) {
	// Sell 5 @ 200 with no prior buy → orphan lot, unattributed = 1000.
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-05", 34, 5, 200, false),
	}
	res := ComputeTradeJournal(txns, nil, TradeJournalOptions{FIFOMode: FIFOModeStrictDate})
	if got := res.Totals.UnattributedISK; got != 1000 {
		t.Errorf("UnattributedISK = %f, want 1000", got)
	}
	if len(res.Lots) != 1 || res.Lots[0].Source != LotSourceOrphan {
		t.Errorf("unexpected lots: %+v", res.Lots)
	}
	if res.Totals.TradingPnL != 0 || res.Totals.ManufacturingPnL != 0 {
		t.Errorf("orphan should not count toward P&L: %+v", res.Totals)
	}
}

func TestComputeTradeJournal_CrossWalletFIFO(t *testing.T) {
	// Buy on char A (older), buy on char B (newer), sell on char C.
	// Strict-date FIFO pools across all wallets; oldest lot goes first.
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 34, 10, 100, true), // Alt-A buy
		mkTxn(2, "char:2", "2026-01-03", 34, 10, 120, true), // Alt-B buy
		mkTxn(3, "char:3", "2026-01-10", 34, 5, 200, false), // Trader sell
	}
	res := ComputeTradeJournal(txns, nil, TradeJournalOptions{FIFOMode: FIFOModeStrictDate})
	if len(res.Lots) != 1 {
		t.Fatalf("want 1 lot, got %d", len(res.Lots))
	}
	// Should have matched against Alt-A (older).
	if res.Lots[0].BuyWallet != "char:1" || res.Lots[0].SellWallet != "char:3" {
		t.Errorf("wrong wallets: buy=%s sell=%s", res.Lots[0].BuyWallet, res.Lots[0].SellWallet)
	}
	if got := res.Totals.TradingPnL; got != 500 {
		t.Errorf("TradingPnL = %f, want 500", got)
	}
}

func TestComputeTradeJournal_PureManufacturing(t *testing.T) {
	// Buy 10 Trit @ 5 → run a Rifter job (10 Trit + 1M install) → sell 1 Rifter @ 2M.
	// Materials cost = 10 * 5 = 50. Total build cost = 1_000_000 + 50 = 1_000_050.
	// MfgProfit = 2_000_000 - 1_000_050 = 999_950 (no fees).
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
	if got := res.Totals.ManufacturingPnL; got != 999_950 {
		t.Errorf("ManufacturingPnL = %f, want 999950", got)
	}
	if len(res.ManufacturingLots) != 1 {
		t.Errorf("want 1 mfg lot, got %d", len(res.ManufacturingLots))
	}
}

func TestComputeTradeJournal_MaterialFallback(t *testing.T) {
	// Only 5 Trit in the trade pool but job needs 10 → 5 real + 5 fallback @ region avg 8.
	// Materials cost = 5*5 + 5*8 = 65. Total build cost = 1_000_000 + 65 = 1_000_065.
	// Sell 1 @ 2M → MfgProfit = 999_935.
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 34, 5, 5, true),
		mkTxn(2, "char:1", "2026-01-10", 588, 1, 2_000_000, false),
	}
	jobs := []JournalIndustryJob{
		mkJob(100, 1, 587, 588, 1, 1_000_000, "2026-01-02", "2026-01-05"),
	}
	res := ComputeTradeJournal(txns, jobs, TradeJournalOptions{
		FIFOMode:        FIFOModeStrictDate,
		Materials:       rifterMaterials(),
		Products:        rifterProducts(),
		MEByJob:         meZero,
		RegionAvgByType: map[int32]float64{34: 8},
	})
	if got := res.Totals.ManufacturingPnL; got != 999_935 {
		t.Errorf("ManufacturingPnL = %f, want 999935", got)
	}
	if !res.ManufacturingLots[0].MaterialsEstimated {
		t.Error("MaterialsEstimated should be true when fallback was used")
	}
}

func TestComputeTradeJournal_MixedPool_StrictDate(t *testing.T) {
	// Buy 10 Rifter Jan 1 @ 1M, manufacture 5 Rifter completing Feb 1 (unit cost 0.5M),
	// sell 12 Rifter Mar 1 @ 2M. Strict-date FIFO: pop 10 bought (Jan 1), then 2 mfg.
	// Trading profit: 10 * (2M - 1M) = 10M.
	// Mfg profit: 2 * (2M - 0.5M) = 3M.
	// Combined: 13M.
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 588, 10, 1_000_000, true),
		mkTxn(2, "char:1", "2026-03-01", 588, 12, 2_000_000, false),
	}
	// One job producing 5 Rifter at ~500k each (5M install / 5 = 1M each, minus materials).
	// To hit a clean unit_cost = 0.5M I need install=2.5M and materials=0.
	jobs := []JournalIndustryJob{
		mkJob(100, 1, 587, 588, 5, 2_500_000, "2026-01-15", "2026-02-01"),
	}
	// No materials cost — use an empty Materials map so the job consumes nothing.
	res := ComputeTradeJournal(txns, jobs, TradeJournalOptions{
		FIFOMode:  FIFOModeStrictDate,
		Materials: map[int32][]sde.BlueprintMaterial{}, // no materials
		Products:  rifterProducts(),
		MEByJob:   meZero,
	})
	if got := res.Totals.TradingPnL; got != 10_000_000 {
		t.Errorf("TradingPnL = %f, want 10000000", got)
	}
	if got := res.Totals.ManufacturingPnL; got != 3_000_000 {
		t.Errorf("ManufacturingPnL = %f, want 3000000", got)
	}
	if got := res.Totals.CombinedPnL; got != 13_000_000 {
		t.Errorf("CombinedPnL = %f, want 13000000", got)
	}
}

func TestComputeTradeJournal_MixedPool_TradeFirst(t *testing.T) {
	// Same setup, trade_first: pop all 10 traded first, then 2 mfg. Same numbers
	// as strict_date in this scenario since strict_date also picks trade first
	// here — this test verifies mode = trade_first works without weirdness.
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 588, 10, 1_000_000, true),
		mkTxn(2, "char:1", "2026-03-01", 588, 12, 2_000_000, false),
	}
	jobs := []JournalIndustryJob{
		mkJob(100, 1, 587, 588, 5, 2_500_000, "2026-01-15", "2026-02-01"),
	}
	res := ComputeTradeJournal(txns, jobs, TradeJournalOptions{
		FIFOMode:  FIFOModeTradeFirst,
		Materials: map[int32][]sde.BlueprintMaterial{},
		Products:  rifterProducts(),
		MEByJob:   meZero,
	})
	if got := res.Totals.TradingPnL; got != 10_000_000 {
		t.Errorf("TradingPnL = %f, want 10000000", got)
	}
	if got := res.Totals.ManufacturingPnL; got != 3_000_000 {
		t.Errorf("ManufacturingPnL = %f, want 3000000", got)
	}
}

func TestComputeTradeJournal_MixedPool_ManufactureFirst(t *testing.T) {
	// Same setup, manufacture_first: pop all 5 mfg first, then 7 traded.
	// Mfg profit: 5 * (2M - 0.5M) = 7.5M.
	// Trading profit: 7 * (2M - 1M) = 7M.
	// Combined: 14.5M.
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 588, 10, 1_000_000, true),
		mkTxn(2, "char:1", "2026-03-01", 588, 12, 2_000_000, false),
	}
	jobs := []JournalIndustryJob{
		mkJob(100, 1, 587, 588, 5, 2_500_000, "2026-01-15", "2026-02-01"),
	}
	res := ComputeTradeJournal(txns, jobs, TradeJournalOptions{
		FIFOMode:  FIFOModeManufactureFirst,
		Materials: map[int32][]sde.BlueprintMaterial{},
		Products:  rifterProducts(),
		MEByJob:   meZero,
	})
	if got := res.Totals.ManufacturingPnL; got != 7_500_000 {
		t.Errorf("ManufacturingPnL = %f, want 7500000", got)
	}
	if got := res.Totals.TradingPnL; got != 7_000_000 {
		t.Errorf("TradingPnL = %f, want 7000000", got)
	}
	if got := res.Totals.CombinedPnL; got != 14_500_000 {
		t.Errorf("CombinedPnL = %f, want 14500000", got)
	}
}

func TestComputeTradeJournal_CombinedTotalsInvariant(t *testing.T) {
	// Any input should always satisfy Trading + Manufacturing == Combined.
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 34, 100, 5, true),
		mkTxn(2, "char:2", "2026-01-02", 34, 50, 6, true),
		mkTxn(3, "char:3", "2026-01-10", 34, 40, 10, false),
		mkTxn(4, "char:3", "2026-01-11", 588, 3, 1_500_000, false),
	}
	jobs := []JournalIndustryJob{
		mkJob(100, 1, 587, 588, 3, 1_000_000, "2026-01-05", "2026-01-08"),
	}
	res := ComputeTradeJournal(txns, jobs, TradeJournalOptions{
		FIFOMode:        FIFOModeStrictDate,
		Materials:       rifterMaterials(),
		Products:        rifterProducts(),
		MEByJob:         meZero,
		RegionAvgByType: map[int32]float64{34: 6},
	})
	if got := res.Totals.CombinedPnL; got != res.Totals.TradingPnL+res.Totals.ManufacturingPnL {
		t.Errorf("Combined != Trading + Manufacturing: %f vs %f + %f", got, res.Totals.TradingPnL, res.Totals.ManufacturingPnL)
	}
}

func TestComputeTradeJournal_ReactionsCountAsManufacturing(t *testing.T) {
	// Reactions (activity_id = 11) produce composites/hybrids at cost =
	// install + materials, same as manufacturing. Verify a reaction job
	// generates a mfg lot and its sale counts toward ManufacturingPnL.
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 34, 100, 10, true), // buy input mats
		mkTxn(2, "char:1", "2026-01-10", 588, 1, 5_000_000, false), // sell reaction output
	}
	// Reaction job: activity_id=11 (not 1).
	job := mkJob(100, 1, 587, 588, 1, 1_000_000, "2026-01-02", "2026-01-05")
	job.ActivityID = 11
	res := ComputeTradeJournal(txns, []JournalIndustryJob{job}, TradeJournalOptions{
		FIFOMode:  FIFOModeStrictDate,
		Materials: map[int32][]sde.BlueprintMaterial{587: {{TypeID: 34, Quantity: 100}}},
		Products:  map[int32]sde.BlueprintProduct{587: {TypeID: 588, Quantity: 1}},
		MEByJob:   meZero,
	})
	// Cost = 1M install + 100*10 = 1_001_000; sell 5M → mfg profit = 3_999_000.
	if got := res.Totals.ManufacturingPnL; got != 3_999_000 {
		t.Errorf("ManufacturingPnL = %f, want 3999000", got)
	}
	if len(res.ManufacturingLots) != 1 {
		t.Errorf("expected reaction to produce 1 mfg lot, got %d", len(res.ManufacturingLots))
	}
}

func TestComputeTradeJournal_SinceDateExcludesEarlierPnL(t *testing.T) {
	// A sell before the cutoff should still pop the buy lot (so open state is
	// correct) but its P&L shouldn't count toward Totals.
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 34, 10, 100, true),
		mkTxn(2, "char:1", "2026-01-05", 34, 5, 200, false), // before cutoff
		mkTxn(3, "char:1", "2026-02-10", 34, 3, 250, false), // after cutoff
	}
	cutoff, _ := time.Parse(time.RFC3339, "2026-02-01T00:00:00Z")
	res := ComputeTradeJournal(txns, nil, TradeJournalOptions{
		FIFOMode:  FIFOModeStrictDate,
		SinceDate: cutoff,
	})
	// Only the 2026-02-10 sell (3 units at profit 150 each = 450) should count.
	if got := res.Totals.TradingPnL; got != 450 {
		t.Errorf("TradingPnL = %f, want 450 (only post-cutoff)", got)
	}
}

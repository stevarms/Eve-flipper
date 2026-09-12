package engine

import (
	"math"
	"testing"
)

// A trade journal is a record of what happened, so where the wallet says what
// CCP actually charged, that is what a row must report — not what the
// character's current skills imply. These tests pin the three cases: the rate
// is used when known, it survives a sale being split across several cost lots,
// and its absence falls back cleanly.

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestComputeTradeJournal_UsesActualSellTaxRate(t *testing.T) {
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 34, 10, 100, true),
		mkTxn(2, "char:1", "2026-01-05", 34, 10, 200, false),
	}
	res := ComputeTradeJournal(txns, nil, TradeJournalOptions{
		FIFOMode:        FIFOModeStrictDate,
		SalesTaxPercent: 3.6, // what the old formula would have charged
		// What the wallet says was actually charged on transaction 2.
		ActualSellTaxRateByTxnID: map[int64]float64{2: 3.375},
	})
	if len(res.Lots) != 1 {
		t.Fatalf("want 1 lot, got %d", len(res.Lots))
	}
	lot := res.Lots[0]
	if !lot.SellTaxActual {
		t.Error("SellTaxActual is false on a row whose tax came from the wallet journal")
	}
	// 2000 gross × 3.375%.
	if !approx(lot.SellTax, 67.5) {
		t.Errorf("SellTax = %.4f, want 67.5 (the modelled 3.6%% would give 72)", lot.SellTax)
	}
}

// The reason the map holds a rate and not an ISK amount. One sale consumed two
// buy lots and is emitted as two rows; each taxes its own slice of the gross,
// and the slices have to add back up to the single charge the wallet recorded.
func TestComputeTradeJournal_ActualTaxApportionsAcrossSplitSale(t *testing.T) {
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 34, 10, 100, true),
		mkTxn(2, "char:1", "2026-01-02", 34, 10, 120, true),
		mkTxn(3, "char:1", "2026-01-05", 34, 15, 200, false),
	}
	res := ComputeTradeJournal(txns, nil, TradeJournalOptions{
		FIFOMode:                 FIFOModeStrictDate,
		SalesTaxPercent:          3.6,
		ActualSellTaxRateByTxnID: map[int64]float64{3: 3.375},
	})
	if len(res.Lots) != 2 {
		t.Fatalf("want 2 lots, got %d", len(res.Lots))
	}
	var sum float64
	for i, lot := range res.Lots {
		if !lot.SellTaxActual {
			t.Errorf("lot %d: SellTaxActual is false; both halves of one sale carry the same fact", i)
		}
		sum += lot.SellTax
	}
	// The whole sale: 15 × 200 = 3000 gross, taxed at 3.375% = 101.25.
	if !approx(sum, 101.25) {
		t.Errorf("split tax sums to %.4f, want 101.25 — the two rows must reconstruct the single charge", sum)
	}
}

func TestComputeTradeJournal_FallsBackToModelledTax(t *testing.T) {
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 34, 10, 100, true),
		mkTxn(2, "char:1", "2026-01-05", 34, 10, 200, false),
	}
	res := ComputeTradeJournal(txns, nil, TradeJournalOptions{
		FIFOMode:        FIFOModeStrictDate,
		SalesTaxPercent: 3.375,
		// Nothing for transaction 2 — e.g. a sale older than the ~30 days of
		// journal ESI will serve, which can never be reconciled.
		ActualSellTaxRateByTxnID: map[int64]float64{999: 1.0},
	})
	if len(res.Lots) != 1 {
		t.Fatalf("want 1 lot, got %d", len(res.Lots))
	}
	lot := res.Lots[0]
	if lot.SellTaxActual {
		t.Error("SellTaxActual is true on a row the journal said nothing about")
	}
	if !approx(lot.SellTax, 67.5) {
		t.Errorf("SellTax = %.4f, want 67.5 from the modelled rate", lot.SellTax)
	}
}

// An unmatched sell has no cost basis, but it is still a sale CCP taxed. The
// orphan branch computes fees separately, so it needs its own pin.
func TestComputeTradeJournal_OrphanSellUsesActualTax(t *testing.T) {
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-05", 34, 5, 200, false),
	}
	res := ComputeTradeJournal(txns, nil, TradeJournalOptions{
		FIFOMode:                 FIFOModeStrictDate,
		SalesTaxPercent:          3.6,
		ActualSellTaxRateByTxnID: map[int64]float64{1: 3.375},
	})
	if len(res.Lots) != 1 || res.Lots[0].Source != LotSourceOrphan {
		t.Fatalf("unexpected lots: %+v", res.Lots)
	}
	if !res.Lots[0].SellTaxActual {
		t.Error("an orphan sell was still taxed; the row must say the tax is a fact")
	}
	if !approx(res.Lots[0].SellTax, 33.75) { // 1000 × 3.375%
		t.Errorf("orphan SellTax = %.4f, want 33.75", res.Lots[0].SellTax)
	}
}

// A nil map is the ordinary state for every caller that does not reconcile
// (the projection paths, and any window with no archive), so it must behave
// exactly as the code did before actual rates existed.
func TestComputeTradeJournal_NilActualMapIsUnchangedBehaviour(t *testing.T) {
	txns := []JournalTxn{
		mkTxn(1, "char:1", "2026-01-01", 34, 10, 100, true),
		mkTxn(2, "char:1", "2026-01-05", 34, 10, 200, false),
	}
	res := ComputeTradeJournal(txns, nil, TradeJournalOptions{
		FIFOMode:        FIFOModeStrictDate,
		SalesTaxPercent: 3.375,
	})
	if len(res.Lots) != 1 {
		t.Fatalf("want 1 lot, got %d", len(res.Lots))
	}
	if res.Lots[0].SellTaxActual {
		t.Error("SellTaxActual set with no actual-rate map supplied")
	}
	if !approx(res.Lots[0].SellTax, 67.5) {
		t.Errorf("SellTax = %.4f, want 67.5", res.Lots[0].SellTax)
	}
}

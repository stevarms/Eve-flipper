package api

import (
	"math"
	"testing"

	"eve-flipper/internal/db"
)

// Everything here is about one question: when is the wallet journal allowed to
// overrule the fee model, and when must it keep quiet? Getting that boundary
// wrong is worse than not reading the journal at all — a modelled rate is off
// by a fraction of a percent, a mispaired one can be off by a factor of ten.

func tax(wallet, date string, amount float64) db.ArchivedJournalEntry {
	return db.ArchivedJournalEntry{WalletKey: wallet, Date: date, RefType: "transaction_tax", Amount: -amount}
}

func sale(wallet, date string, amount float64, txnID int64) db.ArchivedJournalEntry {
	return db.ArchivedJournalEntry{
		WalletKey: wallet, Date: date, RefType: "market_transaction",
		Amount: amount, ContextID: txnID, ContextIDType: "market_transaction_id",
	}
}

func TestActualFeesPairsASingleSale(t *testing.T) {
	got := actualFeesFromJournal([]db.ArchivedJournalEntry{
		sale("char:1", "2026-09-01T12:00:00Z", 1_000_000, 555),
		tax("char:1", "2026-09-01T12:00:00Z", 33_750),
	})
	rate, ok := got.rate(555)
	if !ok {
		t.Fatal("a lone tax entry sharing a second with a lone sale is unambiguous and must pair")
	}
	if !floatEq(rate, 3.375, 1e-9) {
		t.Errorf("rate = %.4f, want 3.375", rate)
	}
	if got.Paired != 1 || got.Unresolved != 0 {
		t.Errorf("Paired/Unresolved = %d/%d, want 1/0", got.Paired, got.Unresolved)
	}
	if !floatEq(got.SalesTaxISK, 33_750, 1e-9) {
		t.Errorf("SalesTaxISK = %.2f, want 33750", got.SalesTaxISK)
	}
}

// The case a naive same-second join gets wrong. Two fills land in one second,
// so the cross-product offers four pairings and two of them imply nonsense
// rates. Tax is proportional to gross, so sorting both sides by amount makes
// the correct pairing the only one consistent with the ordering.
func TestActualFeesPairsMultipleFillsInOneSecondByAmount(t *testing.T) {
	const sec = "2026-09-01T12:00:00Z"
	got := actualFeesFromJournal([]db.ArchivedJournalEntry{
		sale("char:1", sec, 10_000_000, 100),
		sale("char:1", sec, 1_000_000, 200),
		tax("char:1", sec, 33_750),  // belongs to the 1 M sale
		tax("char:1", sec, 337_500), // belongs to the 10 M sale
	})
	if got.Paired != 2 {
		t.Fatalf("Paired = %d, want 2", got.Paired)
	}
	for _, txnID := range []int64{100, 200} {
		rate, ok := got.rate(txnID)
		if !ok {
			t.Fatalf("txn %d unpaired", txnID)
		}
		if !floatEq(rate, 3.375, 1e-9) {
			t.Errorf("txn %d rate = %.4f, want 3.375 — the pairing crossed the two fills", txnID, rate)
		}
	}
}

// A second where the two sides disagree in count is a second we cannot reason
// about: something is missing, and the sort-by-amount argument only holds when
// the sequences correspond. Abandoning the whole group costs a slightly wrong
// modelled rate; guessing costs a wildly wrong one.
func TestActualFeesAbandonsUnbalancedSeconds(t *testing.T) {
	const sec = "2026-09-01T12:00:00Z"
	got := actualFeesFromJournal([]db.ArchivedJournalEntry{
		sale("char:1", sec, 10_000_000, 100),
		tax("char:1", sec, 337_500),
		tax("char:1", sec, 33_750), // its sale is missing from the archive
	})
	if len(got.SellTaxRateByTxnID) != 0 {
		t.Errorf("paired %d sales from an unbalanced second; must pair none", len(got.SellTaxRateByTxnID))
	}
	if got.Unresolved != 2 {
		t.Errorf("Unresolved = %d, want 2", got.Unresolved)
	}
	// The ISK still counts toward the period total — it was charged, whether or
	// not we can say which sale it belongs to.
	if !floatEq(got.SalesTaxISK, 371_250, 1e-9) {
		t.Errorf("SalesTaxISK = %.2f, want 371250", got.SalesTaxISK)
	}
}

// Two wallets can trade in the same second without one's tax landing on the
// other's sale. Grouping by wallet key rather than timestamp alone is what
// keeps a corp division out of a character's arithmetic.
func TestActualFeesDoesNotPairAcrossWallets(t *testing.T) {
	const sec = "2026-09-01T12:00:00Z"
	got := actualFeesFromJournal([]db.ArchivedJournalEntry{
		sale("char:1", sec, 1_000_000, 100),
		tax("corp:98:1", sec, 33_750),
	})
	if len(got.SellTaxRateByTxnID) != 0 {
		t.Error("a tax charge on one wallet was attributed to a sale on another")
	}
}

func TestActualFeesRejectsImplausibleRates(t *testing.T) {
	const sec = "2026-09-01T12:00:00Z"
	got := actualFeesFromJournal([]db.ArchivedJournalEntry{
		sale("char:1", sec, 1_000, 100),
		tax("char:1", sec, 900), // 90% — a mispairing, not a tax
	})
	if _, ok := got.rate(100); ok {
		t.Error("a 90% implied rate must be discarded in favour of the modelled rate")
	}
	if got.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1", got.Unresolved)
	}
}

// Corp journal rows are archived without a context_id, so they carry no link to
// a transaction. They fall back to the modelled rate by construction — the
// point of this test is that they do so quietly, without corrupting a pairing.
func TestActualFeesIgnoresSalesWithoutContext(t *testing.T) {
	const sec = "2026-09-01T12:00:00Z"
	got := actualFeesFromJournal([]db.ArchivedJournalEntry{
		{WalletKey: "corp:98:1", Date: sec, RefType: "market_transaction", Amount: 1_000_000},
		tax("corp:98:1", sec, 33_750),
	})
	if len(got.SellTaxRateByTxnID) != 0 {
		t.Error("a sale with no transaction id cannot be keyed and must not pair")
	}
	if got.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1", got.Unresolved)
	}
}

// A buy is not taxed; only the sell side is. If buys were grouped with sells
// the counts would balance wrongly and the sort-by-amount pairing would cross
// them.
func TestActualFeesIgnoresBuys(t *testing.T) {
	const sec = "2026-09-01T12:00:00Z"
	got := actualFeesFromJournal([]db.ArchivedJournalEntry{
		sale("char:1", sec, 1_000_000, 100),
		{WalletKey: "char:1", Date: sec, RefType: "market_transaction", Amount: -500_000, ContextID: 101},
		tax("char:1", sec, 33_750),
	})
	rate, ok := got.rate(100)
	if !ok || !floatEq(rate, 3.375, 1e-9) {
		t.Errorf("sell rate = %.4f (ok=%v), want 3.375 — a buy was counted as a taxable sale", rate, ok)
	}
	if _, ok := got.rate(101); ok {
		t.Error("a buy was given a sales tax rate")
	}
}

// Order-placement costs are period totals and nothing else. They have no
// context_id to attribute them by, and 542 broker charges against 3525 sales in
// the live archive is not a rounding difference — a charge covers an order, not
// a fill.
func TestActualFeesSumsOrderPlacementCosts(t *testing.T) {
	got := actualFeesFromJournal([]db.ArchivedJournalEntry{
		{WalletKey: "char:1", Date: "2026-09-01T12:00:00Z", RefType: "brokers_fee", Amount: -150_000},
		{WalletKey: "char:1", Date: "2026-09-02T12:00:00Z", RefType: "brokers_fee", Amount: -50_000},
		{WalletKey: "char:1", Date: "2026-09-02T12:00:01Z", RefType: "market_provider_tax", Amount: -20_000},
		{WalletKey: "char:1", Date: "2026-09-02T12:00:02Z", RefType: "player_donation", Amount: -1_000_000},
	})
	if !floatEq(got.BrokerFeeISK, 200_000, 1e-9) {
		t.Errorf("BrokerFeeISK = %.2f, want 200000", got.BrokerFeeISK)
	}
	if !floatEq(got.ProviderTaxISK, 20_000, 1e-9) {
		t.Errorf("ProviderTaxISK = %.2f, want 20000", got.ProviderTaxISK)
	}
	if !floatEq(got.orderCostsISK(), 220_000, 1e-9) {
		t.Errorf("orderCostsISK() = %.2f, want 220000", got.orderCostsISK())
	}
	// An unrelated ref_type must not leak into any bucket.
	if got.SalesTaxISK != 0 {
		t.Errorf("SalesTaxISK = %.2f, want 0", got.SalesTaxISK)
	}
}

func TestActualFeesEmptyInput(t *testing.T) {
	got := actualFeesFromJournal(nil)
	if _, ok := got.rate(1); ok {
		t.Error("empty journal reported a rate")
	}
	if got.Paired != 0 || got.Unresolved != 0 || got.orderCostsISK() != 0 {
		t.Error("empty journal produced non-zero totals")
	}
	if math.Abs(got.SalesTaxISK) > 0 {
		t.Error("empty journal produced sales tax")
	}
}

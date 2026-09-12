package api

import (
	"sort"

	"eve-flipper/internal/db"
)

// Actual market fees, recovered from the wallet journal.
//
// The trade journal used to charge every historic sale a rate computed from
// today's skills, which is the wrong shape for a record of what happened. ESI
// will not hand us the fee on a transaction — wallet transactions carry no fee
// fields at all — but the wallet journal records every charge CCP made, and we
// already archive it. This turns those charges back into per-sale facts where
// the data can prove the link, and into honest period totals where it cannot.
//
// The two halves are not symmetrical, and the asymmetry is the whole design:
//
//   - Sales tax is charged per sale. It can be attributed to a row.
//   - Broker fee and the structure's provider tax are charged when an order is
//     placed or modified, against the whole order. One charge can cover a
//     hundred fills spread over days, and an order that never filled still cost
//     ISK. There is no sale to attribute them to, so they stay period costs.

// journalRefTransactionTax and friends are the ESI ref_types this reads.
const (
	journalRefTransactionTax   = "transaction_tax"
	journalRefMarketTransact   = "market_transaction"
	journalRefBrokersFee       = "brokers_fee"
	journalRefMarketProviderTx = "market_provider_tax"
)

// maxPlausibleSalesTaxPercent bounds a recovered rate.
//
// The untrained rate is 7.5%, so anything above 10% means the pairing produced
// nonsense rather than that the user paid a remarkable tax. Such a pair is
// discarded and the sale falls back to the modelled rate, which is wrong by a
// little, rather than being charged a rate that is wrong by a lot.
const maxPlausibleSalesTaxPercent = 10.0

// actualFees is what the journal could tell us about one window.
type actualFees struct {
	// SellTaxRateByTxnID is the effective sales tax percentage actually
	// charged, keyed by sell transaction id.
	//
	// A rate, not an ISK amount, because one sale can be matched against
	// several buy lots and split across several journal rows; a rate divides
	// across that split on its own, and an amount would have to be
	// apportioned by hand at every call site.
	SellTaxRateByTxnID map[int64]float64

	// Period totals. Sales tax is per-sale and therefore also attributable;
	// the other two are order-placement costs and are not.
	SalesTaxISK    float64
	BrokerFeeISK   float64
	ProviderTaxISK float64

	// Paired counts sales whose tax we recovered; Unresolved counts tax
	// entries we could not confidently attribute to one.
	Paired     int
	Unresolved int
}

// rate returns the actual tax rate for a sell transaction, and whether the
// journal proved it.
func (a actualFees) rate(txnID int64) (float64, bool) {
	if a.SellTaxRateByTxnID == nil || txnID == 0 {
		return 0, false
	}
	r, ok := a.SellTaxRateByTxnID[txnID]
	return r, ok
}

// orderCostsISK is what the period paid to place orders rather than to fill
// them: broker fees plus the structure owner's cut.
func (a actualFees) orderCostsISK() float64 { return a.BrokerFeeISK + a.ProviderTaxISK }

// actualFeesFromJournal recovers per-sale sales tax and period fee totals from
// archived wallet journal entries.
//
// Linking a tax charge to its sale takes two hops. The second is exact: a
// market_transaction entry carries the sale's transaction_id in context_id, so
// it joins straight onto a lot's SellTxnID. The first is not: a transaction_tax
// entry carries no context at all, only a timestamp it shares with its sale.
//
// Within one wallet and one second, pairing is still derivable rather than
// guessable. Tax is proportional to gross, so sorting both sides by amount puts
// them in the same order and index-wise pairing is correct — which is what
// resolves the seconds holding several fills. Where the two sides differ in
// length something is missing and the ordering argument no longer holds, so the
// whole group is abandoned to the modelled rate instead of being guessed at. A
// naive join across those groups is where implausible rates come from.
func actualFeesFromJournal(entries []db.ArchivedJournalEntry) actualFees {
	out := actualFees{SellTaxRateByTxnID: make(map[int64]float64)}

	type group struct {
		taxes []float64 // positive ISK
		sales []db.ArchivedJournalEntry
	}
	type groupKey struct {
		wallet string
		date   string
	}
	// Keyed by wallet rather than character so a corp wallet's second cannot
	// collide with a character's.
	groups := make(map[groupKey]*group)
	at := func(e db.ArchivedJournalEntry) *group {
		k := groupKey{wallet: e.WalletKey, date: e.Date}
		g := groups[k]
		if g == nil {
			g = &group{}
			groups[k] = g
		}
		return g
	}

	for _, e := range entries {
		switch e.RefType {
		case journalRefTransactionTax:
			tax := -e.Amount // charges are negative
			if tax <= 0 {
				continue
			}
			out.SalesTaxISK += tax
			at(e).taxes = append(at(e).taxes, tax)
		case journalRefMarketTransact:
			// Sells only: a buy moves ISK out and is not taxed. Corp journal
			// rows are archived without context_id, so they never pair and
			// fall back to the modelled rate.
			if e.Amount <= 0 || e.ContextID == 0 {
				continue
			}
			at(e).sales = append(at(e).sales, e)
		case journalRefBrokersFee:
			out.BrokerFeeISK += -e.Amount
		case journalRefMarketProviderTx:
			out.ProviderTaxISK += -e.Amount
		}
	}

	for _, g := range groups {
		if len(g.taxes) == 0 {
			continue
		}
		if len(g.taxes) != len(g.sales) {
			out.Unresolved += len(g.taxes)
			continue
		}
		sort.Float64s(g.taxes)
		sort.Slice(g.sales, func(i, j int) bool { return g.sales[i].Amount < g.sales[j].Amount })
		for i, tax := range g.taxes {
			sale := g.sales[i]
			rate := tax / sale.Amount * 100.0
			if rate <= 0 || rate > maxPlausibleSalesTaxPercent {
				out.Unresolved++
				continue
			}
			out.SellTaxRateByTxnID[sale.ContextID] = rate
			out.Paired++
		}
	}

	return out
}

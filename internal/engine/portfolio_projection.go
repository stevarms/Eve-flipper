package engine

import (
	"sort"
	"time"
)

// portfolio_projection.go bridges the two P&L surfaces.
//
// ComputeTradeJournal is the richer matcher: it sees corp wallets, industry
// jobs and three FIFO modes, where ComputePortfolioPnLWithOptions sees only
// character wallet transactions. But the analytics — daily series, Sharpe,
// drawdown, Calmar, per-item and per-station breakdowns — were written against
// the poorer engine's output shape.
//
// Rather than reimplement those statistics over TradeJournalResult (two
// implementations of the same maths is exactly the drift this closes),
// ToPortfolioPnL projects the journal's lots into []RealizedTrade and runs them
// through summarizeRealizedLedger, the same function the legacy engine calls.
// Two matchers, one summarizer.
//
// The anti-drift guarantee, asserted by test: given zero industry jobs,
// ToPortfolioPnL must produce the same PortfolioPnL as
// ComputePortfolioPnLWithOptions over the same transactions.

// ToPortfolioPnL projects a trade-journal result into the PortfolioPnL shape
// the analytics UI consumes.
//
// src filters which lots participate: LotSourceTrade for trading only,
// LotSourceManufacture for manufacturing only, or "" for everything. The
// filter is applied before summarizing, which is what makes the
// Trading / Manufacturing / Combined split honest — Sharpe, max drawdown and
// profit factor are each computed over the selected slice rather than
// apportioned out of a combined number.
//
// Orphan sells (no known cost basis) follow opt.IncludeUnmatchedSell, the same
// switch the legacy engine uses, and land in the ledger flagged Unmatched.
func (r *TradeJournalResult) ToPortfolioPnL(opt PortfolioPnLOptions, src LotSource) *PortfolioPnL {
	opt = normalizePortfolioOptions(opt)
	out := &PortfolioPnL{
		DailyPnL:       []DailyPnLEntry{},
		TopItems:       []ItemPnL{},
		TopStations:    []StationPnL{},
		Ledger:         []RealizedTrade{},
		OpenPositions:  []OpenPosition{},
		SlotEfficiency: []PortfolioSlotEfficiency{},
		Settings: PortfolioSettings{
			LookbackDays:         opt.LookbackDays,
			SalesTaxPercent:      opt.SalesTaxPercent,
			BrokerFeePercent:     opt.BrokerFeePercent,
			LedgerLimit:          opt.LedgerLimit,
			IncludeUnmatchedSell: opt.IncludeUnmatchedSell,
		},
	}
	if r == nil {
		return out
	}

	// Build the full ledger first; LedgerLimit truncation happens at the very
	// end, after summarizeRealizedLedger has seen every row.
	ledger := make([]RealizedTrade, 0, len(r.Lots))
	coverage := MatchingCoverage{}

	for _, lot := range r.Lots {
		matched := lot.Source != LotSourceOrphan

		// Coverage is a property of the sell flow, not of the current view
		// filter, so it counts every lot the journal matched. Filtering it by
		// src would report "100% matched" for a manufacturing-only view that
		// in fact left half the character's sells unattributed.
		coverage.TotalSellQty += lot.MatchedQty
		coverage.TotalSellValue += lot.SellGross
		if matched {
			coverage.MatchedSellQty += lot.MatchedQty
			coverage.MatchedSellValue += lot.SellGross
		} else {
			coverage.UnmatchedSellQty += lot.MatchedQty
			coverage.UnmatchedSellValue += lot.SellGross
		}

		if src != "" && lot.Source != src {
			continue
		}
		if !matched && !opt.IncludeUnmatchedSell {
			continue
		}

		qty := float64(lot.MatchedQty)
		sellTotal := lot.SellGross - lot.SellBrokerFee - lot.SellTax

		if !matched {
			ledger = append(ledger, RealizedTrade{
				TypeID:            lot.TypeID,
				TypeName:          lot.TypeName,
				Quantity:          int32(lot.MatchedQty),
				SellTransactionID: lot.SellTxnID,
				SellDate:          lot.SellDate,
				SellLocationID:    lot.SellLocationID,
				SellLocationName:  lot.SellLocationName,
				SellUnitPrice:     lot.SellUnitPrice,
				SellGross:         lot.SellGross,
				SellBrokerFee:     lot.SellBrokerFee,
				SellTax:           lot.SellTax,
				SellTotal:         sellTotal,
				RealizedPnL:       sellTotal,
				Unmatched:         true,
			})
			continue
		}

		buyGross := lot.BuyUnitPrice * qty
		buyTotal := buyGross + lot.BuyFees
		pnl := sellTotal - buyTotal
		margin := 0.0
		if buyTotal > 0 {
			margin = pnl / buyTotal * 100
		}

		ledger = append(ledger, RealizedTrade{
			TypeID:            lot.TypeID,
			TypeName:          lot.TypeName,
			Quantity:          int32(lot.MatchedQty),
			BuyTransactionID:  lot.BuyTxnID,
			SellTransactionID: lot.SellTxnID,
			BuyDate:           lot.BuyDate,
			SellDate:          lot.SellDate,
			HoldingDays:       holdingDaysBetween(lot.BuyDate, lot.SellDate),
			BuyLocationID:     lot.BuyLocationID,
			BuyLocationName:   lot.BuyLocationName,
			SellLocationID:    lot.SellLocationID,
			SellLocationName:  lot.SellLocationName,
			BuyUnitPrice:      lot.BuyUnitPrice,
			SellUnitPrice:     lot.SellUnitPrice,
			BuyGross:          buyGross,
			SellGross:         lot.SellGross,
			BuyFee:            lot.BuyFees,
			SellBrokerFee:     lot.SellBrokerFee,
			SellTax:           lot.SellTax,
			BuyTotal:          buyTotal,
			SellTotal:         sellTotal,
			RealizedPnL:       pnl,
			MarginPercent:     margin,
		})
	}

	if coverage.TotalSellQty > 0 {
		coverage.MatchRateQtyPct = float64(coverage.MatchedSellQty) / float64(coverage.TotalSellQty) * 100
	}
	if coverage.TotalSellValue > 0 {
		coverage.MatchRateValuePct = coverage.MatchedSellValue / coverage.TotalSellValue * 100
	}
	out.Coverage = coverage

	days, items, stations, summary := summarizeRealizedLedger(ledger, opt)

	openPositions := make([]OpenPosition, 0, len(r.OpenPositions))
	totalOpenCost := 0.0
	for _, p := range r.OpenPositions {
		if p.Qty <= 0 {
			continue
		}
		if src != "" && p.Source != src {
			continue
		}
		openPositions = append(openPositions, OpenPosition{
			TypeID:        p.TypeID,
			TypeName:      p.TypeName,
			LocationID:    p.LocationID,
			LocationName:  p.LocationName,
			Quantity:      p.Qty,
			AvgCost:       p.AvgUnitCost,
			CostBasis:     p.CostBasis,
			OldestLotDate: dayOnly(p.OldestDate),
		})
		totalOpenCost += p.CostBasis
	}
	sort.Slice(openPositions, func(i, j int) bool {
		return openPositions[i].CostBasis > openPositions[j].CostBasis
	})
	summary.OpenPositions = len(openPositions)
	summary.OpenCostBasis = totalOpenCost

	// Ledger newest first, then truncated — the same order and the same tie
	// breaks as the legacy engine, so the two produce byte-identical rows.
	sort.Slice(ledger, func(i, j int) bool {
		if ledger[i].SellDate == ledger[j].SellDate {
			if ledger[i].SellTransactionID == ledger[j].SellTransactionID {
				return ledger[i].BuyTransactionID > ledger[j].BuyTransactionID
			}
			return ledger[i].SellTransactionID > ledger[j].SellTransactionID
		}
		return ledger[i].SellDate > ledger[j].SellDate
	})
	if opt.LedgerLimit > 0 && len(ledger) > opt.LedgerLimit {
		ledger = ledger[:opt.LedgerLimit]
	}
	if len(openPositions) > 50 {
		openPositions = openPositions[:50]
	}

	out.DailyPnL = days
	out.Summary = summary
	out.TopItems = items
	out.TopStations = stations
	out.Ledger = ledger
	out.OpenPositions = openPositions
	out.SlotEfficiency = ComputePortfolioSlotEfficiency(out, nil)
	return out
}

// holdingDaysBetween returns whole days held, clamped at zero. Unparseable or
// missing dates yield 0 rather than a negative sentinel — the legacy engine's
// behaviour, and the only sane answer for a lot whose buy date is unknown.
func holdingDaysBetween(buyDate, sellDate string) int {
	buy, err := time.Parse(time.RFC3339, buyDate)
	if err != nil {
		return 0
	}
	sell, err := time.Parse(time.RFC3339, sellDate)
	if err != nil {
		return 0
	}
	days := int(sell.Sub(buy).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}

// dayOnly narrows an RFC3339 timestamp to YYYY-MM-DD, the format OpenPosition
// uses. A value already in that form passes through unchanged.
func dayOnly(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Format("2006-01-02")
	}
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

package engine

// The desk used to judge orders purely on how fast they would fill, so an
// order could be deeply underwater and still report "hold — on track": it
// was on track, nobody had asked whether filling was a good idea. These
// cover the profitability layer sitting in front of that verdict, and the
// break-even guard that stops the desk advising a losing reprice.

import (
	"math"
	"testing"
	"time"

	"eve-flipper/internal/esi"
)

const orderDeskMarginTestType = 34

// orderDeskMarginMine is one of our own orders at Jita, dated so expiry
// never enters into any of the verdicts below.
func orderDeskMarginMine(price float64, isBuy bool) esi.CharacterOrder {
	return esi.CharacterOrder{
		OrderID: 8001, TypeID: orderDeskMarginTestType, TypeName: "Tritanium",
		LocationID: 60003760, LocationName: "Jita", RegionID: 10000002,
		Price: price, VolumeRemain: 10, VolumeTotal: 10,
		IsBuyOrder: isBuy, Duration: 90,
		Issued: time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339),
	}
}

// orderDeskMarginBookOrder is somebody else's order at the same station.
func orderDeskMarginBookOrder(id int64, price float64, vol int32, isBuy bool) esi.MarketOrder {
	return esi.MarketOrder{
		OrderID: id, TypeID: orderDeskMarginTestType, RegionID: 10000002,
		LocationID: 60003760, Price: price, VolumeRemain: vol, IsBuyOrder: isBuy,
	}
}

func orderDeskMarginOpts() OrderDeskOptions {
	return OrderDeskOptions{
		SalesTaxPercent: 8, BrokerFeePercent: 1,
		TargetETADays: 3, WarnExpiryDays: 2,
	}
}

// orderDeskMarginProceeds is the fraction of a sale price that reaches the
// wallet under orderDeskMarginOpts: 8% tax + 1% broker.
const orderDeskMarginProceeds = 0.91

// orderDeskMarginRow runs the desk over one order of ours plus the rest of
// the station book and returns the single row, so each case below only has
// to say what it is actually about.
func orderDeskMarginRow(t *testing.T, mine esi.CharacterOrder, others []esi.MarketOrder, opt OrderDeskOptions) OrderDeskOrder {
	t.Helper()

	regional := []esi.MarketOrder{{
		OrderID: mine.OrderID, TypeID: mine.TypeID, RegionID: mine.RegionID,
		LocationID: mine.LocationID, Price: mine.Price,
		VolumeRemain: mine.VolumeRemain, IsBuyOrder: mine.IsBuyOrder,
	}}
	regional = append(regional, others...)

	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	history := map[OrderDeskHistoryKey][]esi.HistoryEntry{
		NewOrderDeskHistoryKey(mine.RegionID, mine.TypeID): orderDeskTestHistory(
			end, 7, func(time.Weekday) int64 { return 150 }, mine.Price),
	}

	got := ComputeOrderDesk([]esi.CharacterOrder{mine}, regional, history, nil, opt)
	if len(got.Orders) != 1 {
		t.Fatalf("orders len = %d, want 1", len(got.Orders))
	}
	return got.Orders[0]
}

func TestComputeOrderDesk_BuyMarginPricesTheExitAsAnUndercut(t *testing.T) {
	// You cannot resell *at* the best ask, you have to beat it, which is the
	// same step the Suggested column already tells you to take. Pricing the
	// exit at the raw ask would overstate every buy order's margin.
	row := orderDeskMarginRow(t,
		orderDeskMarginMine(100, true),
		[]esi.MarketOrder{orderDeskMarginBookOrder(8100, 120, 50, false)},
		orderDeskMarginOpts())

	if row.MarginBasis != orderDeskMarginBook {
		t.Fatalf("margin_basis = %q, want %q", row.MarginBasis, orderDeskMarginBook)
	}
	wantExit := NextSellUndercut(120)
	if wantExit >= 120 {
		t.Fatalf("NextSellUndercut(120) = %v, expected it to be below the ask", wantExit)
	}
	if row.ExitPrice != wantExit {
		t.Fatalf("exit_price = %v, want the undercut %v rather than the raw ask", row.ExitPrice, wantExit)
	}

	wantMargin := wantExit*orderDeskMarginProceeds - 100
	if math.Abs(row.MarginUnitISK-wantMargin) > 1e-9 {
		t.Fatalf("margin_unit_isk = %v, want %v", row.MarginUnitISK, wantMargin)
	}
	// The reference for a buy order is what we are bidding, not the exit.
	if math.Abs(row.MarginPercent-wantMargin) > 1e-9 {
		t.Fatalf("margin_percent = %v, want %v (margin over the 100 ISK bid)", row.MarginPercent, wantMargin)
	}
	if row.WarnThinMargin {
		t.Fatalf("warn_thin_margin set on a %.2f%% margin against a 3%% floor", row.MarginPercent)
	}
	if row.Recommendation != "hold" {
		t.Fatalf("recommendation = %q (%s), want hold on a healthy order at the top of the book",
			row.Recommendation, row.Reason)
	}
}

func TestComputeOrderDesk_BuyMarginGoneRecommendsCancel(t *testing.T) {
	// The report this exists for: the sell side fell far enough that filling
	// the buy order is a loss, and the desk still said "on track".
	row := orderDeskMarginRow(t,
		orderDeskMarginMine(100, true),
		[]esi.MarketOrder{orderDeskMarginBookOrder(8100, 105, 50, false)},
		orderDeskMarginOpts())

	if row.MarginUnitISK >= 0 {
		t.Fatalf("margin_unit_isk = %v, want a loss at a 105 ask against a 100 bid", row.MarginUnitISK)
	}
	if row.Recommendation != "cancel" {
		t.Fatalf("recommendation = %q (%s), want cancel", row.Recommendation, row.Reason)
	}
	if row.Reason != "margin gone: -4.5% at current book" {
		t.Fatalf("reason = %q, want the margin-gone reason naming the shortfall", row.Reason)
	}
}

func TestComputeOrderDesk_ThinMarginWarnsWithoutChangingTheAction(t *testing.T) {
	// Thin is a warning, not a verdict. A 1.8% margin is still a margin, and
	// the user decides whether it is worth the slot.
	mine := orderDeskMarginMine(100, true)
	others := []esi.MarketOrder{orderDeskMarginBookOrder(8100, 112, 50, false)}

	row := orderDeskMarginRow(t, mine, others, orderDeskMarginOpts())
	if row.MarginUnitISK <= 0 {
		t.Fatalf("margin_unit_isk = %v, want it positive but thin", row.MarginUnitISK)
	}
	if row.MarginPercent >= 3 {
		t.Fatalf("margin_percent = %v, want it under the 3%% default floor", row.MarginPercent)
	}
	if !row.WarnThinMargin {
		t.Fatalf("warn_thin_margin not set on a %.2f%% margin against a 3%% floor", row.MarginPercent)
	}
	if row.Recommendation != "hold" {
		t.Fatalf("recommendation = %q (%s), want the thin margin to leave the action alone",
			row.Recommendation, row.Reason)
	}

	// Drop the floor under the margin and the warning goes away without
	// anything else moving.
	opt := orderDeskMarginOpts()
	opt.MinMarginPercent = 1
	relaxed := orderDeskMarginRow(t, mine, others, opt)
	if relaxed.WarnThinMargin {
		t.Fatalf("warn_thin_margin still set with the floor at 1%% under a %.2f%% margin", relaxed.MarginPercent)
	}
	if relaxed.MarginUnitISK != row.MarginUnitISK || relaxed.Recommendation != row.Recommendation {
		t.Fatalf("the floor changed the margin or the action: %v/%q vs %v/%q",
			relaxed.MarginUnitISK, relaxed.Recommendation, row.MarginUnitISK, row.Recommendation)
	}
}

func TestComputeOrderDesk_BuyWithNoSellSideIsUnmeasured(t *testing.T) {
	// No ask at this station means no answer, and "no data" and "no profit"
	// call for opposite responses — so the row must say it was unmeasured
	// and otherwise behave exactly as it did before margins existed.
	row := orderDeskMarginRow(t, orderDeskMarginMine(100, true), nil, orderDeskMarginOpts())

	if row.MarginBasis != orderDeskMarginNone {
		t.Fatalf("margin_basis = %q, want %q", row.MarginBasis, orderDeskMarginNone)
	}
	if row.ExitPrice != 0 || row.MarginUnitISK != 0 || row.MarginPercent != 0 {
		t.Fatalf("unmeasured row reported numbers: exit=%v margin=%v pct=%v",
			row.ExitPrice, row.MarginUnitISK, row.MarginPercent)
	}
	if row.WarnThinMargin {
		t.Fatalf("warn_thin_margin set on an unmeasured row")
	}
	if row.Recommendation != "hold" || row.Reason != "on track" {
		t.Fatalf("recommendation = %q (%s), want the pre-margin verdict", row.Recommendation, row.Reason)
	}
}

func TestComputeOrderDesk_SellMarginComesFromCostBasis(t *testing.T) {
	// The book cannot tell us what held stock cost, so a sell order is only
	// measurable against the FIFO journal's open positions.
	opt := orderDeskMarginOpts()
	opt.CostBasisByType = map[int32]float64{orderDeskMarginTestType: 100}

	row := orderDeskMarginRow(t, orderDeskMarginMine(120, false), nil, opt)

	if row.MarginBasis != orderDeskMarginCostBasis {
		t.Fatalf("margin_basis = %q, want %q", row.MarginBasis, orderDeskMarginCostBasis)
	}
	if row.CostBasisISK != 100 {
		t.Fatalf("cost_basis_isk = %v, want 100", row.CostBasisISK)
	}
	if row.ExitPrice != 0 {
		t.Fatalf("exit_price = %v, want it unset on a sell row — the exit is the order itself", row.ExitPrice)
	}
	wantMargin := 120*orderDeskMarginProceeds - 100
	if math.Abs(row.MarginUnitISK-wantMargin) > 1e-9 {
		t.Fatalf("margin_unit_isk = %v, want %v", row.MarginUnitISK, wantMargin)
	}
	// A sell order's percentage is return on what the stock cost.
	if math.Abs(row.MarginPercent-wantMargin) > 1e-9 {
		t.Fatalf("margin_percent = %v, want %v (margin over the 100 ISK basis)", row.MarginPercent, wantMargin)
	}
	if row.Recommendation != "hold" {
		t.Fatalf("recommendation = %q (%s), want hold", row.Recommendation, row.Reason)
	}
}

func TestComputeOrderDesk_SellBelowCostRecommendsCancel(t *testing.T) {
	opt := orderDeskMarginOpts()
	opt.CostBasisByType = map[int32]float64{orderDeskMarginTestType: 100}

	// Listed at what we paid, which after tax and broker fee is a loss.
	row := orderDeskMarginRow(t, orderDeskMarginMine(100, false), nil, opt)

	if row.Recommendation != "cancel" {
		t.Fatalf("recommendation = %q (%s), want cancel", row.Recommendation, row.Reason)
	}
	if row.Reason != "below cost: -9.0% vs basis" {
		t.Fatalf("reason = %q, want the below-cost reason naming the shortfall", row.Reason)
	}
}

func TestComputeOrderDesk_SellWithoutCostBasisIsUnmeasured(t *testing.T) {
	// A cold wallet archive must leave the tab exactly as it is today rather
	// than reporting a zero margin on everything held.
	for _, tc := range []struct {
		name  string
		basis map[int32]float64
	}{
		{"nil map", nil},
		{"different type", map[int32]float64{35: 100}},
		{"non-positive cost", map[int32]float64{orderDeskMarginTestType: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opt := orderDeskMarginOpts()
			opt.CostBasisByType = tc.basis

			row := orderDeskMarginRow(t, orderDeskMarginMine(120, false), nil, opt)

			if row.MarginBasis != orderDeskMarginNone {
				t.Fatalf("margin_basis = %q, want %q", row.MarginBasis, orderDeskMarginNone)
			}
			if row.CostBasisISK != 0 || row.MarginUnitISK != 0 || row.MarginPercent != 0 {
				t.Fatalf("unmeasured row reported numbers: cost=%v margin=%v pct=%v",
					row.CostBasisISK, row.MarginUnitISK, row.MarginPercent)
			}
			if row.Recommendation != "hold" || row.Reason != "on track" {
				t.Fatalf("recommendation = %q (%s), want the pre-margin verdict", row.Recommendation, row.Reason)
			}
		})
	}
}

func TestComputeOrderDesk_RepriceIsNotAdvisedBelowCost(t *testing.T) {
	// The reprice branches key on queue position alone, so on an undercut
	// sell order they say "match the leader" without ever asking whether the
	// leader's price is one you can afford to meet.
	opt := orderDeskMarginOpts()
	opt.CostBasisByType = map[int32]float64{orderDeskMarginTestType: 100}

	// Ours at 120 is above water; the 105 ahead of us, after fees, is not.
	row := orderDeskMarginRow(t,
		orderDeskMarginMine(120, false),
		[]esi.MarketOrder{orderDeskMarginBookOrder(8200, 105, 300, false)},
		opt)

	if row.Position != 2 {
		t.Fatalf("position = %d, want 2", row.Position)
	}
	if row.MarginUnitISK <= 0 {
		t.Fatalf("margin_unit_isk = %v, want our own price still above cost", row.MarginUnitISK)
	}
	if row.SuggestedPrice >= 120 {
		t.Fatalf("suggested_price = %v, want it below ours — the reprice branch has to be live", row.SuggestedPrice)
	}
	if row.Recommendation != "cancel" {
		t.Fatalf("recommendation = %q (%s), want cancel instead of chasing the price below cost",
			row.Recommendation, row.Reason)
	}
	if row.Reason != "reprice would sell below cost" {
		t.Fatalf("reason = %q, want the reprice-below-cost reason", row.Reason)
	}
}

func TestComputeOrderDesk_OverbiddingIsNotAdvisedPastBreakEven(t *testing.T) {
	// Same guard from the bid side: outbidding the leader can cost more than
	// the resale is worth, and the desk used to recommend it regardless.
	row := orderDeskMarginRow(t,
		orderDeskMarginMine(100, true),
		[]esi.MarketOrder{
			orderDeskMarginBookOrder(8300, 105, 300, true),
			orderDeskMarginBookOrder(8301, 112, 50, false),
		},
		orderDeskMarginOpts())

	if row.Position != 2 {
		t.Fatalf("position = %d, want 2", row.Position)
	}
	if row.MarginUnitISK <= 0 {
		t.Fatalf("margin_unit_isk = %v, want our own bid still profitable", row.MarginUnitISK)
	}
	if row.SuggestedPrice <= 105 {
		t.Fatalf("suggested_price = %v, want an overbid above the 105 leader", row.SuggestedPrice)
	}
	if row.Recommendation != "cancel" {
		t.Fatalf("recommendation = %q (%s), want cancel instead of an overbid that erases the margin",
			row.Recommendation, row.Reason)
	}
	if row.Reason != "overbidding would erase the margin" {
		t.Fatalf("reason = %q, want the overbid reason", row.Reason)
	}
}

func TestComputeOrderDesk_UnavailableBookLeavesMarginUnmeasured(t *testing.T) {
	// Without a book there is no ask to price an exit against, and a stale
	// cost basis on its own is not enough to call an order a loss.
	mine := orderDeskMarginMine(100, false)
	opt := orderDeskMarginOpts()
	opt.CostBasisByType = map[int32]float64{orderDeskMarginTestType: 1000}

	got := ComputeOrderDesk(
		[]esi.CharacterOrder{mine},
		nil,
		nil,
		map[OrderDeskHistoryKey]bool{NewOrderDeskHistoryKey(mine.RegionID, mine.TypeID): true},
		opt)
	if len(got.Orders) != 1 {
		t.Fatalf("orders len = %d, want 1", len(got.Orders))
	}
	row := got.Orders[0]

	if row.MarginBasis != orderDeskMarginNone {
		t.Fatalf("margin_basis = %q, want %q", row.MarginBasis, orderDeskMarginNone)
	}
	if row.Recommendation != "hold" || row.Reason != "market book unavailable" {
		t.Fatalf("recommendation = %q (%s), want the book-unavailable verdict", row.Recommendation, row.Reason)
	}
}

func TestComputeOrderDesk_MinMarginPercentIsClampedAndDefaulted(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   float64
		want float64
	}{
		{"zero takes the default", 0, orderDeskDefaultMinMarginPercent},
		{"negative takes the default", -5, orderDeskDefaultMinMarginPercent},
		{"above 100 is clamped", 250, 100},
		{"a sane value survives", 7.5, 7.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opt := orderDeskMarginOpts()
			opt.MinMarginPercent = tc.in
			got := ComputeOrderDesk(nil, nil, nil, nil, opt)
			if got.Settings.MinMarginPercent != tc.want {
				t.Fatalf("settings.min_margin_percent = %v, want %v", got.Settings.MinMarginPercent, tc.want)
			}
		})
	}
}

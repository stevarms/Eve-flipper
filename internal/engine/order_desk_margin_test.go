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

func TestComputeOrderDesk_SellBelowCostRecommendsReview(t *testing.T) {
	opt := orderDeskMarginOpts()
	opt.CostBasisByType = map[int32]float64{orderDeskMarginTestType: 100}

	// Listed at what we paid, which after tax and broker fee is a loss.
	// Unlike a buy order there is no obviously right answer here — the ISK
	// is already spent, so cancelling swaps realising the loss for holding
	// stock. The verdict points at the disposition panel instead of
	// asserting that one of those is better.
	row := orderDeskMarginRow(t, orderDeskMarginMine(100, false), nil, opt)

	if row.Recommendation != "review" {
		t.Fatalf("recommendation = %q (%s), want review", row.Recommendation, row.Reason)
	}
	if row.Reason != "below cost: -9.0% — weigh cut, move or hold" {
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
	if row.Recommendation != "review" {
		t.Fatalf("recommendation = %q (%s), want review instead of chasing the price below cost",
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

// The trade station a buy row exits at, and a quiet station beside it. Bidding
// from the second at a range that reaches the first is how you avoid the hub's
// broker fee, and it is what these cover.
const (
	orderDeskMarginHub        = 60003760
	orderDeskMarginOutstation = 60015116
)

// orderDeskMarginBookOrderAt is somebody else's order at a named station.
func orderDeskMarginBookOrderAt(id, station int64, price float64, vol int32, isBuy bool) esi.MarketOrder {
	return esi.MarketOrder{
		OrderID: id, TypeID: orderDeskMarginTestType, RegionID: 10000002,
		LocationID: station, Price: price, VolumeRemain: vol, IsBuyOrder: isBuy,
	}
}

// orderDeskMarginMineAt is one of our own orders at a named station.
func orderDeskMarginMineAt(station int64, price float64, isBuy bool) esi.CharacterOrder {
	mine := orderDeskMarginMine(price, isBuy)
	mine.LocationID = station
	mine.LocationName = "Outstation"
	return mine
}

// orderDeskMarginHubOpts quotes buy exits at the hub, the way the API fills
// this in from the configured trade station.
func orderDeskMarginHubOpts() OrderDeskOptions {
	opt := orderDeskMarginOpts()
	opt.ExitStationByRegion = map[int32]int64{10000002: orderDeskMarginHub}
	return opt
}

func TestComputeOrderDesk_BuyOutsideTheTradeStationExitsThere(t *testing.T) {
	// The report this exists for: bids placed a system out of Jita to dodge
	// its broker fee showed no margin at all, because their own station sells
	// nothing and that was the only sell side the desk would look at. The
	// stock was always going to be sold in Jita.
	row := orderDeskMarginRow(t,
		orderDeskMarginMineAt(orderDeskMarginOutstation, 100, true),
		[]esi.MarketOrder{orderDeskMarginBookOrderAt(8100, orderDeskMarginHub, 120, 50, false)},
		orderDeskMarginHubOpts())

	if row.MarginBasis != orderDeskMarginBook {
		t.Fatalf("margin_basis = %q, want %q — the hub's ask is the exit", row.MarginBasis, orderDeskMarginBook)
	}
	wantExit := NextSellUndercut(120)
	if row.ExitPrice != wantExit {
		t.Fatalf("exit_price = %v, want the hub undercut %v", row.ExitPrice, wantExit)
	}
	if row.ExitLocationID != orderDeskMarginHub {
		t.Fatalf("exit_location_id = %d, want %d — a margin quoted elsewhere has to say where",
			row.ExitLocationID, orderDeskMarginHub)
	}
	wantMargin := wantExit*orderDeskMarginProceeds - 100
	if math.Abs(row.MarginUnitISK-wantMargin) > 1e-9 {
		t.Fatalf("margin_unit_isk = %v, want %v", row.MarginUnitISK, wantMargin)
	}
}

func TestComputeOrderDesk_TradeStationOutranksTheLocalSellSide(t *testing.T) {
	// Where the stock gets sold is a decision about the trade, not a
	// consequence of which backwater happens to have an order standing in it.
	// The optimistic 200 next door is not an exit; the hub is.
	row := orderDeskMarginRow(t,
		orderDeskMarginMineAt(orderDeskMarginOutstation, 100, true),
		[]esi.MarketOrder{
			orderDeskMarginBookOrderAt(8100, orderDeskMarginOutstation, 200, 1, false),
			orderDeskMarginBookOrderAt(8101, orderDeskMarginHub, 120, 50, false),
		},
		orderDeskMarginHubOpts())

	if row.ExitLocationID != orderDeskMarginHub {
		t.Fatalf("exit_location_id = %d, want the hub %d", row.ExitLocationID, orderDeskMarginHub)
	}
	if want := NextSellUndercut(120); row.ExitPrice != want {
		t.Fatalf("exit_price = %v, want %v — the lone local ask is not an exit", row.ExitPrice, want)
	}
}

func TestComputeOrderDesk_BuyAtTheTradeStationExitsLocally(t *testing.T) {
	// An order already standing at the trade station has nothing to say about
	// where it exits, and a row claiming a remote exit would put a hauling
	// caveat on a margin that is takeable on the spot.
	row := orderDeskMarginRow(t,
		orderDeskMarginMineAt(orderDeskMarginHub, 100, true),
		[]esi.MarketOrder{orderDeskMarginBookOrderAt(8100, orderDeskMarginHub, 120, 50, false)},
		orderDeskMarginHubOpts())

	if row.MarginBasis != orderDeskMarginBook {
		t.Fatalf("margin_basis = %q, want %q", row.MarginBasis, orderDeskMarginBook)
	}
	if row.ExitLocationID != 0 {
		t.Fatalf("exit_location_id = %d on a local exit, want 0", row.ExitLocationID)
	}
}

func TestComputeOrderDesk_NoTradeStationKeepsTheStationBook(t *testing.T) {
	// Nil options are the pre-change desk, and the hub's ask must stay
	// invisible to it — otherwise every existing case here is measuring
	// something other than what it says.
	row := orderDeskMarginRow(t,
		orderDeskMarginMineAt(orderDeskMarginOutstation, 100, true),
		[]esi.MarketOrder{orderDeskMarginBookOrderAt(8100, orderDeskMarginHub, 120, 50, false)},
		orderDeskMarginOpts())

	if row.MarginBasis != orderDeskMarginNone {
		t.Fatalf("margin_basis = %q, want %q without a trade station", row.MarginBasis, orderDeskMarginNone)
	}
	if row.ExitPrice != 0 || row.ExitLocationID != 0 {
		t.Fatalf("unmeasured row reported an exit: price=%v location=%d", row.ExitPrice, row.ExitLocationID)
	}
}

func TestComputeOrderDesk_SellRowIgnoresTheTradeStation(t *testing.T) {
	// A sell order is already standing where its stock is. Only bids can exit
	// somewhere else, and a sell row is measured against cost regardless.
	opt := orderDeskMarginHubOpts()
	opt.CostBasisByType = map[int32]float64{orderDeskMarginTestType: 80}
	row := orderDeskMarginRow(t,
		orderDeskMarginMineAt(orderDeskMarginOutstation, 100, false),
		[]esi.MarketOrder{orderDeskMarginBookOrderAt(8100, orderDeskMarginHub, 60, 50, false)},
		opt)

	if row.MarginBasis != orderDeskMarginCostBasis {
		t.Fatalf("margin_basis = %q, want %q", row.MarginBasis, orderDeskMarginCostBasis)
	}
	if row.ExitLocationID != 0 {
		t.Fatalf("exit_location_id = %d on a sell row, want 0", row.ExitLocationID)
	}
}

package engine

// Four things the desk could not see, and one it could see but reported as a
// fault:
//
//   - a bid parked 40% under the book, reported as "buried" every day;
//   - a region-range bid three systems away, invisible to a station book;
//   - the ISK a reprice commits, as opposed to the concession it costs;
//   - the price level that reprice would buy at, across the trailing year;
//   - a price the user set by hand, which no amount of book reading outranks.

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"eve-flipper/internal/esi"
)

const (
	orderDeskRiskType = 34

	// Jita, and two systems at known distances from it.
	orderDeskRiskJitaStation  = int64(60003760)
	orderDeskRiskJitaSystem   = int32(30000142)
	orderDeskRiskNearStation  = int64(60011866)
	orderDeskRiskNearSystem   = int32(30000144)
	orderDeskRiskFarStation   = int64(60008494)
	orderDeskRiskFarSystem    = int32(30002187)
	orderDeskRiskOtherStation = int64(60003757) // same system as Jita 4-4
)

// orderDeskRiskJumps is a fake universe covering only the pairs below. Every
// other pair is unknown, which is the -1 the real ShortestPath returns and the
// case the predicate has to treat as out of reach rather than in it.
func orderDeskRiskJumps(from, to int32) int {
	if from == to {
		return 0
	}
	switch {
	case from == orderDeskRiskNearSystem && to == orderDeskRiskJitaSystem:
		return 1
	case from == orderDeskRiskFarSystem && to == orderDeskRiskJitaSystem:
		return 5
	}
	return -1
}

func orderDeskRiskMine(price float64, vol int32, isBuy bool) esi.CharacterOrder {
	return esi.CharacterOrder{
		OrderID: 9001, TypeID: orderDeskRiskType, TypeName: "Tritanium",
		LocationID: orderDeskRiskJitaStation, LocationName: "Jita",
		RegionID: 10000002, Price: price, VolumeRemain: vol, VolumeTotal: vol,
		IsBuyOrder: isBuy, Duration: 90,
		Issued: time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339),
	}
}

// orderDeskRiskBook is somebody else's order, placed anywhere with any range.
func orderDeskRiskBook(id int64, price float64, vol int32, isBuy bool,
	loc int64, sys int32, rng string) esi.MarketOrder {
	return esi.MarketOrder{
		OrderID: id, TypeID: orderDeskRiskType, RegionID: 10000002,
		LocationID: loc, SystemID: sys, Price: price, VolumeRemain: vol,
		IsBuyOrder: isBuy, Range: rng,
	}
}

// orderDeskRiskAtJita is the common case: a station-range order in the same
// building as ours.
func orderDeskRiskAtJita(id int64, price float64, vol int32, isBuy bool) esi.MarketOrder {
	return orderDeskRiskBook(id, price, vol, isBuy,
		orderDeskRiskJitaStation, orderDeskRiskJitaSystem, "station")
}

func orderDeskRiskOpts() OrderDeskOptions {
	return OrderDeskOptions{
		SalesTaxPercent: 8, BrokerFeePercent: 1,
		TargetETADays: 3, WarnExpiryDays: 2,
		StationSystemID: map[int64]int32{orderDeskRiskJitaStation: orderDeskRiskJitaSystem},
		JumpsBetween:    orderDeskRiskJumps,
	}
}

// orderDeskRiskRow runs the desk over one order of ours plus a book and
// returns the single row.
func orderDeskRiskRow(t *testing.T, mine esi.CharacterOrder,
	others []esi.MarketOrder, opt OrderDeskOptions) OrderDeskOrder {
	t.Helper()

	regional := []esi.MarketOrder{orderDeskRiskBook(mine.OrderID, mine.Price,
		mine.VolumeRemain, mine.IsBuyOrder, mine.LocationID,
		orderDeskRiskJitaSystem, "station")}
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

// orderDeskRiskYear is a year of daily averages ramping evenly from lo to hi.
// The ramp is uniform, so a price a tenth of the way up the range sits near
// the tenth percentile and every expectation below can be read off by eye.
func orderDeskRiskYear(lo, hi float64) []esi.HistoryEntry {
	const days = 365
	end := time.Now().UTC()
	out := make([]esi.HistoryEntry, 0, days)
	for i := days - 1; i >= 0; i-- {
		frac := float64(days-1-i) / float64(days-1)
		out = append(out, esi.HistoryEntry{
			Date:    end.AddDate(0, 0, -i).Format("2006-01-02"),
			Volume:  100,
			Average: lo + (hi-lo)*frac,
		})
	}
	return out
}

func orderDeskRiskPercentiles(t *testing.T, lo, hi float64) map[OrderDeskHistoryKey]PricePercentiles {
	t.Helper()
	pct := CalcPricePercentiles(orderDeskRiskYear(lo, hi), 0, time.Now().UTC())
	if pct.Basis != PercentileBasisHistory {
		t.Fatalf("test percentiles refused: %s", pct.Reason)
	}
	return map[OrderDeskHistoryKey]PricePercentiles{
		NewOrderDeskHistoryKey(10000002, orderDeskRiskType): pct,
	}
}

// --- 1. range-aware buy competition ------------------------------------

func TestOrderDeskBidReaches(t *testing.T) {
	cases := []struct {
		name string
		loc  int64
		sys  int32
		rng  string
		want bool
	}{
		{"same station, station range", orderDeskRiskJitaStation, orderDeskRiskJitaSystem, "station", true},
		{"same system, other station, station range", orderDeskRiskOtherStation, orderDeskRiskJitaSystem, "station", false},
		{"same system, other station, system range", orderDeskRiskOtherStation, orderDeskRiskJitaSystem, "solarsystem", true},
		{"other system, system range", orderDeskRiskNearStation, orderDeskRiskNearSystem, "solarsystem", false},
		{"other system, region range", orderDeskRiskFarStation, orderDeskRiskFarSystem, "region", true},
		{"one jump away, range 1", orderDeskRiskNearStation, orderDeskRiskNearSystem, "1", true},
		{"five jumps away, range 1", orderDeskRiskFarStation, orderDeskRiskFarSystem, "1", false},
		{"five jumps away, range 5", orderDeskRiskFarStation, orderDeskRiskFarSystem, "5", true},
		{"five jumps away, range 10", orderDeskRiskFarStation, orderDeskRiskFarSystem, "10", true},
		{"unknown distance, range 40", 60099999, 30099999, "40", false},
		{"same system, range 0", orderDeskRiskOtherStation, orderDeskRiskJitaSystem, "0", true},
		{"empty range", orderDeskRiskNearStation, orderDeskRiskNearSystem, "", false},
		{"unparseable range", orderDeskRiskNearStation, orderDeskRiskNearSystem, "wibble", false},
		{"region range, mixed case and padding", orderDeskRiskFarStation, orderDeskRiskFarSystem, " Region ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := orderDeskRiskBook(1, 100, 1, true, tc.loc, tc.sys, tc.rng)
			got := orderDeskBidReaches(o, orderDeskRiskJitaStation,
				orderDeskRiskJitaSystem, orderDeskRiskJumps)
			if got != tc.want {
				t.Fatalf("reaches = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestComputeOrderDesk_RegionRangeBidCompetesFromAnotherStation(t *testing.T) {
	// The bid that changed the answer: 105 at Perimeter, region range, so it
	// takes every unit a seller at Jita wants to move before ours does.
	mine := orderDeskRiskMine(100, 10, true)
	others := []esi.MarketOrder{
		orderDeskRiskBook(3001, 105, 40, true,
			orderDeskRiskNearStation, orderDeskRiskNearSystem, "region"),
		orderDeskRiskAtJita(3002, 200, 100, false), // an ask, so margin is measurable
	}

	row := orderDeskRiskRow(t, mine, others, orderDeskRiskOpts())
	if row.Position != 2 {
		t.Fatalf("position = %d, want 2 (outbid from Perimeter)", row.Position)
	}
	if row.QueueAheadQty != 40 {
		t.Fatalf("queue_ahead_qty = %d, want 40", row.QueueAheadQty)
	}
	if math.Abs(row.BestPrice-105) > 1e-9 {
		t.Fatalf("best_price = %v, want 105", row.BestPrice)
	}
	if row.CompetingRemoteBids != 1 {
		t.Fatalf("competing_remote_bids = %d, want 1", row.CompetingRemoteBids)
	}

	// The same book with range awareness switched off has to reproduce the
	// old numbers exactly, or every pre-existing test is measuring a
	// different desk than the one that ships.
	opt := orderDeskRiskOpts()
	opt.JumpsBetween = nil
	opt.StationSystemID = nil
	blind := orderDeskRiskRow(t, mine, others, opt)
	if blind.Position != 1 {
		t.Fatalf("station-only position = %d, want 1", blind.Position)
	}
	if blind.QueueAheadQty != 0 {
		t.Fatalf("station-only queue_ahead_qty = %d, want 0", blind.QueueAheadQty)
	}
	if blind.CompetingRemoteBids != 0 {
		t.Fatalf("station-only competing_remote_bids = %d, want 0", blind.CompetingRemoteBids)
	}
}

func TestComputeOrderDesk_StationRangeBidElsewhereIsNotCompetition(t *testing.T) {
	// Same price, same system, one station over — but station range, so it
	// cannot touch a seller in our building and must not move our position.
	mine := orderDeskRiskMine(100, 10, true)
	others := []esi.MarketOrder{
		orderDeskRiskBook(3003, 105, 40, true,
			orderDeskRiskOtherStation, orderDeskRiskJitaSystem, "station"),
		orderDeskRiskAtJita(3004, 200, 100, false),
	}
	row := orderDeskRiskRow(t, mine, others, orderDeskRiskOpts())
	if row.Position != 1 {
		t.Fatalf("position = %d, want 1", row.Position)
	}
	if row.CompetingRemoteBids != 0 {
		t.Fatalf("competing_remote_bids = %d, want 0", row.CompetingRemoteBids)
	}
}

func TestComputeOrderDesk_SellRowsIgnoreRemoteOrders(t *testing.T) {
	// Sell orders are always station range in EVE, so a cheaper ask three
	// systems away is not competition and must not appear as depth ahead.
	mine := orderDeskRiskMine(100, 10, false)
	others := []esi.MarketOrder{
		orderDeskRiskBook(3005, 90, 500, false,
			orderDeskRiskFarStation, orderDeskRiskFarSystem, "station"),
	}
	row := orderDeskRiskRow(t, mine, others, orderDeskRiskOpts())
	if row.Position != 1 {
		t.Fatalf("position = %d, want 1 (remote asks are not competition)", row.Position)
	}
	if row.QueueAheadQty != 0 {
		t.Fatalf("queue_ahead_qty = %d, want 0", row.QueueAheadQty)
	}
}

// --- 2. lowball detection ----------------------------------------------

// orderDeskRiskLowballBook is a deep bid at Jita well above ours, which is
// what makes a parked bid look "buried" to the liquidity verdict.
func orderDeskRiskLowballBook() []esi.MarketOrder {
	return []esi.MarketOrder{
		orderDeskRiskAtJita(4001, 100, 5000, true),
		orderDeskRiskAtJita(4002, 200, 100, false),
	}
}

func TestComputeOrderDesk_ParkedBidIsNotBuried(t *testing.T) {
	mine := orderDeskRiskMine(60, 10, true) // 40% under best
	row := orderDeskRiskRow(t, mine, orderDeskRiskLowballBook(), orderDeskRiskOpts())

	if !row.IsLowball {
		t.Fatalf("is_lowball = false on a bid 40%% under best")
	}
	if row.Recommendation != "hold" {
		t.Fatalf("recommendation = %q (%s), want hold", row.Recommendation, row.Reason)
	}
	if !strings.Contains(row.Reason, "parked bid") {
		t.Fatalf("reason = %q, want it to name the parked bid", row.Reason)
	}

	// Raising the threshold past the actual discount has to restore the old
	// verdict. If it does not, the quiet came from a blanket suppression
	// rather than from the threshold, and every bid went quiet with it.
	//
	// The jump gate has to be moved out of the way too: at any setting where
	// this bid is not a lowball it is still a 67% raise, and the capital
	// verdict would answer first. That interaction is the subject of
	// TestComputeOrderDesk_CapitalJumpFiresWhenTheLowballThresholdIsRaised;
	// here it would only hide what is being tested.
	opt := orderDeskRiskOpts()
	opt.LowballDiscountPct = 50
	opt.RepriceJumpPct = 200
	loud := orderDeskRiskRow(t, mine, orderDeskRiskLowballBook(), opt)
	if loud.IsLowball {
		t.Fatalf("is_lowball = true at a 50%% threshold on a 40%% discount")
	}
	if loud.Recommendation != "reprice" {
		t.Fatalf("recommendation = %q (%s), want reprice at the raised threshold",
			loud.Recommendation, loud.Reason)
	}
	if !strings.Contains(loud.Reason, "buried") {
		t.Fatalf("reason = %q, want the original liquidity verdict back", loud.Reason)
	}
}

func TestComputeOrderDesk_ParkedBidNearExpiryStillSurfaces(t *testing.T) {
	// The failure mode this must not introduce is a parked bid lapsing in
	// silence.
	mine := orderDeskRiskMine(60, 10, true)
	mine.Duration = 1
	mine.Issued = time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)

	row := orderDeskRiskRow(t, mine, orderDeskRiskLowballBook(), orderDeskRiskOpts())
	if row.Recommendation != "review" {
		t.Fatalf("recommendation = %q (%s), want review", row.Recommendation, row.Reason)
	}
	if !strings.Contains(row.Reason, "expiring") {
		t.Fatalf("reason = %q, want it to name the expiry", row.Reason)
	}
}

func TestComputeOrderDesk_PatientBidFlagOverridesTheThreshold(t *testing.T) {
	// 5% under best is nowhere near the automatic threshold, so only the
	// declared flag can make this hold.
	mine := orderDeskRiskMine(95, 10, true)
	opt := orderDeskRiskOpts()
	opt.PatientBidTypes = map[int32]bool{orderDeskRiskType: true}

	row := orderDeskRiskRow(t, mine, orderDeskRiskLowballBook(), opt)
	if !row.IsLowball || !row.PatientBid {
		t.Fatalf("is_lowball = %v, patient_bid = %v, want both true", row.IsLowball, row.PatientBid)
	}
	if row.Recommendation != "hold" {
		t.Fatalf("recommendation = %q (%s), want hold", row.Recommendation, row.Reason)
	}
	if !row.HasHoldingRule {
		t.Fatalf("has_holding_rule = false on a declared patient bid")
	}
}

func TestComputeOrderDesk_ParkedBidUnderwaterStillReportsTheLoss(t *testing.T) {
	// A parked bid whose exit has collapsed below it is not on track, and
	// the margin verdict outranks the parked-bid one.
	mine := orderDeskRiskMine(60, 10, true)
	others := []esi.MarketOrder{
		orderDeskRiskAtJita(4003, 100, 5000, true),
		orderDeskRiskAtJita(4004, 61, 100, false), // exit below our own bid
	}
	row := orderDeskRiskRow(t, mine, others, orderDeskRiskOpts())
	if !row.IsLowball {
		t.Fatalf("is_lowball = false")
	}
	if row.MarginUnitISK >= 0 {
		t.Fatalf("margin_unit_isk = %v, want negative", row.MarginUnitISK)
	}
	if row.Recommendation != "cancel" {
		t.Fatalf("recommendation = %q (%s), want cancel", row.Recommendation, row.Reason)
	}
}

func TestComputeOrderDesk_LowballSuggestionComesFromTheTrailingYear(t *testing.T) {
	mine := orderDeskRiskMine(60, 10, true)
	opt := orderDeskRiskOpts()
	opt.PercentilesByKey = orderDeskRiskPercentiles(t, 100, 200)

	row := orderDeskRiskRow(t, mine, orderDeskRiskLowballBook(), opt)
	if row.PercentileBasis != PercentileBasisHistory {
		t.Fatalf("percentile_basis = %q, want history", row.PercentileBasis)
	}
	// A tenth percentile of an even ramp from 100 to 200 sits near 110.
	if row.LowballPrice < 105 || row.LowballPrice > 115 {
		t.Fatalf("lowball_price = %v, want ~110", row.LowballPrice)
	}
	if math.Abs(row.LowballFillDaysPct-10) > 3 {
		t.Fatalf("lowball_fill_days_pct = %v, want ~10", row.LowballFillDaysPct)
	}
}

// --- 3. capital and price level ----------------------------------------

func TestComputeOrderDesk_BookRunawayIsANewBuyNotAReprice(t *testing.T) {
	// The case that prompted all of this: 100 units bid at 50k, the book now
	// 499k, the best ask 600k. Per-unit margin at the new bid is healthy, and
	// following the book would commit 44.9M ISK more than the order holds, at
	// a price near the top of everything the item traded all year.
	mine := orderDeskRiskMine(50_000, 100, true)
	others := []esi.MarketOrder{
		orderDeskRiskAtJita(5001, 499_000, 10, true),
		orderDeskRiskAtJita(5002, 600_000, 100, false),
	}
	opt := orderDeskRiskOpts()
	opt.PercentilesByKey = orderDeskRiskPercentiles(t, 40_000, 520_000)

	row := orderDeskRiskRow(t, mine, others, opt)

	if math.Abs(row.SuggestedPrice-499_100) > 1e-6 {
		t.Fatalf("suggested_price = %v, want 499100", row.SuggestedPrice)
	}
	if math.Abs(row.AddedCapitalISK-44_910_000) > 1 {
		t.Fatalf("added_capital_isk = %v, want 44910000", row.AddedCapitalISK)
	}
	// The margin test the old desk applied, and passed.
	if row.SuggestedMarginPercent <= 0 {
		t.Fatalf("suggested_margin_percent = %v, want positive — the point is that "+
			"margin alone clears this trade", row.SuggestedMarginPercent)
	}
	if row.SuggestedPricePercentile < orderDeskRiskPercentile {
		t.Fatalf("suggested_price_percentile = %v, want >= %v",
			row.SuggestedPricePercentile, orderDeskRiskPercentile)
	}
	if row.Recommendation != "review" {
		t.Fatalf("recommendation = %q (%s), want review", row.Recommendation, row.Reason)
	}
	if !strings.Contains(row.Reason, "percentile") {
		t.Fatalf("reason = %q, want it to name the price level", row.Reason)
	}
	if !strings.Contains(row.Reason, "44.9M") {
		t.Fatalf("reason = %q, want it to name the ISK committed", row.Reason)
	}
}

func TestComputeOrderDesk_SmallBookMoveStillReprices(t *testing.T) {
	// Same shape, 5% instead of 900%: a maintenance move, and the desk has
	// to keep saying so or the new gates have simply replaced the old advice.
	mine := orderDeskRiskMine(100_000, 10, true)
	others := []esi.MarketOrder{
		orderDeskRiskAtJita(5003, 105_000, 5000, true),
		orderDeskRiskAtJita(5004, 600_000, 100, false),
	}
	opt := orderDeskRiskOpts()
	opt.PercentilesByKey = orderDeskRiskPercentiles(t, 40_000, 520_000)

	row := orderDeskRiskRow(t, mine, others, opt)
	if row.IsLowball {
		t.Fatalf("is_lowball = true on a bid 5%% under best")
	}
	if row.Recommendation != "reprice" {
		t.Fatalf("recommendation = %q (%s), want reprice", row.Recommendation, row.Reason)
	}
	if row.AddedCapitalISK <= 0 {
		t.Fatalf("added_capital_isk = %v, want positive and reported anyway",
			row.AddedCapitalISK)
	}
}

func TestComputeOrderDesk_CapitalJumpFiresWhenTheLowballThresholdIsRaised(t *testing.T) {
	// At default prefs the lowball threshold and the capital-jump threshold
	// are the same line — a bid needs raising 25% exactly when it sits 20%
	// under best — so the jump test only has a window of its own once
	// Lowball % is raised above it. This is that window, and it is also the
	// only place the "new buy, not a reprice" wording appears.
	mine := orderDeskRiskMine(100_000, 100, true)
	others := []esi.MarketOrder{
		orderDeskRiskAtJita(5005, 140_000, 5000, true),
		orderDeskRiskAtJita(5006, 600_000, 100, false),
	}
	opt := orderDeskRiskOpts()
	opt.LowballDiscountPct = 50

	row := orderDeskRiskRow(t, mine, others, opt)
	if row.IsLowball {
		t.Fatalf("is_lowball = true at a 50%% threshold on a 29%% discount")
	}
	if row.Recommendation != "review" {
		t.Fatalf("recommendation = %q (%s), want review", row.Recommendation, row.Reason)
	}
	if !strings.Contains(row.Reason, "new buy") {
		t.Fatalf("reason = %q, want it to say this is a new buy", row.Reason)
	}
}

func TestComputeOrderDesk_PercentilesSurviveTheDerivedCache(t *testing.T) {
	// The desk reads percentiles out of a JSON cache, where the sorted series
	// is long gone and RankOfPrice has to interpolate the stored ladder. If
	// that path disagreed with the live one, every verdict above would hold
	// in tests and not in production.
	live := orderDeskRiskPercentiles(t, 40_000, 520_000)
	key := NewOrderDeskHistoryKey(10000002, orderDeskRiskType)

	blob, err := json.Marshal(live[key])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var cached PricePercentiles
	if err := json.Unmarshal(blob, &cached); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mine := orderDeskRiskMine(50_000, 100, true)
	others := []esi.MarketOrder{
		orderDeskRiskAtJita(5007, 499_000, 10, true),
		orderDeskRiskAtJita(5008, 600_000, 100, false),
	}
	opt := orderDeskRiskOpts()
	opt.PercentilesByKey = map[OrderDeskHistoryKey]PricePercentiles{key: cached}

	row := orderDeskRiskRow(t, mine, others, opt)
	if row.PercentileBasis != PercentileBasisHistory {
		t.Fatalf("percentile_basis = %q, want history", row.PercentileBasis)
	}
	if row.SuggestedPricePercentile < orderDeskRiskPercentile {
		t.Fatalf("suggested_price_percentile = %v, want >= %v after a round trip",
			row.SuggestedPricePercentile, orderDeskRiskPercentile)
	}
	if row.Recommendation != "review" {
		t.Fatalf("recommendation = %q (%s), want review", row.Recommendation, row.Reason)
	}
}

// --- 5. prices set by hand ---------------------------------------------

func TestComputeOrderDesk_SellTargetOutranksTheBook(t *testing.T) {
	// The desk wants to undercut to 199; the target says 620. Saying so once
	// should be enough.
	mine := orderDeskRiskMine(700, 10, false)
	others := []esi.MarketOrder{
		orderDeskRiskAtJita(6001, 200, 500, false),
		orderDeskRiskAtJita(6002, 150, 500, true),
	}
	opt := orderDeskRiskOpts()
	opt.TargetPriceByType = map[int32]float64{orderDeskRiskType: 620}

	row := orderDeskRiskRow(t, mine, others, opt)
	if row.Recommendation != "hold" {
		t.Fatalf("recommendation = %q (%s), want hold", row.Recommendation, row.Reason)
	}
	if !strings.Contains(row.Reason, "620") {
		t.Fatalf("reason = %q, want it to name the target", row.Reason)
	}
	if !row.HasHoldingRule || row.TargetMet {
		t.Fatalf("has_holding_rule = %v, target_met = %v, want true/false",
			row.HasHoldingRule, row.TargetMet)
	}
	// The market is at 200 of a 620 target: a third of the way.
	if math.Abs(row.TargetProgressPct-200.0/620.0*100.0) > 1e-6 {
		t.Fatalf("target_progress_pct = %v, want ~32", row.TargetProgressPct)
	}
}

func TestComputeOrderDesk_SellTargetBelowTheMarketStandsAside(t *testing.T) {
	// A target the market has already cleared must not freeze the row: the
	// rule is a floor, not a veto.
	mine := orderDeskRiskMine(700, 10, false)
	others := []esi.MarketOrder{
		orderDeskRiskAtJita(6003, 200, 500, false),
		orderDeskRiskAtJita(6004, 150, 500, true),
	}
	opt := orderDeskRiskOpts()
	opt.TargetPriceByType = map[int32]float64{orderDeskRiskType: 120}
	opt.CostBasisByType = map[int32]float64{orderDeskRiskType: 100}

	row := orderDeskRiskRow(t, mine, others, opt)
	if !row.TargetMet {
		t.Fatalf("target_met = false with the market above the target")
	}
	if row.Recommendation == "hold" {
		t.Fatalf("recommendation = hold (%s), want the book's own verdict", row.Reason)
	}
}

func TestComputeOrderDesk_BidCeilingStopsTheChase(t *testing.T) {
	mine := orderDeskRiskMine(100, 10, true)
	others := []esi.MarketOrder{
		orderDeskRiskAtJita(6005, 500, 5000, true),
		orderDeskRiskAtJita(6006, 900, 100, false),
	}
	opt := orderDeskRiskOpts()
	opt.MaxBidByType = map[int32]float64{orderDeskRiskType: 120}

	row := orderDeskRiskRow(t, mine, others, opt)
	if row.Recommendation != "hold" {
		t.Fatalf("recommendation = %q (%s), want hold", row.Recommendation, row.Reason)
	}
	if !strings.Contains(row.Reason, "ceiling") {
		t.Fatalf("reason = %q, want it to name the ceiling", row.Reason)
	}
	if math.Abs(row.MaxBidPrice-120) > 1e-9 {
		t.Fatalf("max_bid_price = %v, want 120", row.MaxBidPrice)
	}
}

func TestComputeOrderDesk_TargetIsTheFourthMarginBasis(t *testing.T) {
	// A sell with no cost basis used to report basis "none", and every
	// margin-driven branch — the break-even guard included — stood down for
	// it. Those were exactly the rows showing a dash in the Margin column.
	mine := orderDeskRiskMine(700, 10, false)
	others := []esi.MarketOrder{
		orderDeskRiskAtJita(6007, 800, 500, false),
		orderDeskRiskAtJita(6008, 150, 500, true),
	}
	opt := orderDeskRiskOpts()
	opt.TargetPriceByType = map[int32]float64{orderDeskRiskType: 400}

	row := orderDeskRiskRow(t, mine, others, opt)
	if row.MarginBasis != orderDeskMarginTarget {
		t.Fatalf("margin_basis = %q, want target", row.MarginBasis)
	}
	// Proceeds at 700 against proceeds at the 400 target.
	want := 700*orderDeskMarginProceeds - 400*orderDeskMarginProceeds
	if math.Abs(row.MarginUnitISK-want) > 1e-6 {
		t.Fatalf("margin_unit_isk = %v, want %v", row.MarginUnitISK, want)
	}
	// A target shortfall is a decision not going your way, not a position
	// underwater, and must not be reported with the words for a loss.
	if orderDeskMarginIsLoss(row.MarginBasis) {
		t.Fatalf("target basis counted as a loss basis")
	}
}

func TestComputeOrderDesk_MarginAfterRepriceIsReported(t *testing.T) {
	// Position 1 has nothing to move to, so before and after must agree —
	// which is the check that the two numbers share a reference.
	mine := orderDeskRiskMine(200, 10, false)
	others := []esi.MarketOrder{orderDeskRiskAtJita(7001, 150, 500, true)}
	opt := orderDeskRiskOpts()
	opt.CostBasisByType = map[int32]float64{orderDeskRiskType: 100}

	row := orderDeskRiskRow(t, mine, others, opt)
	if row.Position != 1 {
		t.Fatalf("position = %d, want 1", row.Position)
	}
	if math.Abs(row.SuggestedMarginUnitISK-row.MarginUnitISK) > 1e-9 {
		t.Fatalf("suggested_margin_unit_isk = %v, margin_unit_isk = %v, want equal",
			row.SuggestedMarginUnitISK, row.MarginUnitISK)
	}
	if math.Abs(row.SuggestedMarginPercent-row.MarginPercent) > 1e-9 {
		t.Fatalf("suggested_margin_percent = %v, margin_percent = %v, want equal",
			row.SuggestedMarginPercent, row.MarginPercent)
	}
	if row.WarnThinAfterReprice {
		t.Fatalf("warn_thin_after_reprice = true with no reprice on the table")
	}
}

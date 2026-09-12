package engine

import (
	"fmt"
	"math"
	"testing"
	"time"
)

var todayTestNow = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

// todayTestTrade is a scan candidate that clears every data-completeness
// gate, so a test can vary one thing at a time.
func todayTestTrade(typeID int32, headline, realizable float64) StationTrade {
	return StationTrade{
		TypeID:                typeID,
		TypeName:              fmt.Sprintf("Item %d", typeID),
		StationID:             60003760,
		StationName:           "Jita IV-4",
		BuyPrice:              100,
		SellPrice:             130,
		MarginPercent:         20,
		DailyVolume:           1000,
		S2BPerDay:             100,
		BfSPerDay:             100,
		DailyProfit:           headline,
		RealizableDailyProfit: realizable,
		CapitalRequired:       1000,
		HistoryAvailable:      true,
	}
}

// todayTestGoodHistory is a settled, profitable record, so grading turns on
// whatever else the test is varying rather than on sample size.
func todayTestGoodHistory(typeIDs ...int32) map[int32]TodayItemHistory {
	out := map[int32]TodayItemHistory{}
	for _, id := range typeIDs {
		out[id] = TodayItemHistory{TypeID: id, SellsQty: 30, RealizedISK: 50e6}
	}
	return out
}

func todayTestInputs(trades ...StationTrade) TodayInputs {
	ids := make([]int32, 0, len(trades))
	for _, t := range trades {
		ids = append(ids, t.TypeID)
	}
	return TodayInputs{
		Now:         todayTestNow,
		ScanTrades:  trades,
		Capital:     TodayCapitalInput{WalletISK: 1e9},
		ItemHistory: todayTestGoodHistory(ids...),
	}
}

func todayFindAction(t *testing.T, actions []TodayAction, typeID int32) (TodayAction, bool) {
	t.Helper()
	for _, a := range actions {
		if a.TypeID == typeID {
			return a, true
		}
	}
	return TodayAction{}, false
}

// The whole point of the ranking. A headline 10M/day opportunity whose
// realistic figure is 2M must sort below a 3M/day one that is solid, even
// though it is more than three times the advertised number.
func TestBuildTodayPlanRanksOnDownsideNotHeadline(t *testing.T) {
	flashy := todayTestTrade(1, 10e6, 2e6)
	steady := todayTestTrade(2, 3e6, 3e6)

	plan := BuildTodayPlan(todayTestInputs(flashy, steady), TodayOpts{})

	if len(plan.Actions) != 2 {
		t.Fatalf("advised actions = %d, want 2 (not advised: %d)", len(plan.Actions), len(plan.NotAdvised))
	}
	if plan.Actions[0].TypeID != 2 {
		t.Fatalf("top action is type %d, want the steady row (2) - ranking is following the headline",
			plan.Actions[0].TypeID)
	}
	// And the inversion is real: the row that lost is the one with the
	// bigger expected figure.
	if plan.Actions[0].ExpectedISK7d >= plan.Actions[1].ExpectedISK7d {
		t.Fatalf("expected %.0f vs %.0f - the test no longer demonstrates an inversion",
			plan.Actions[0].ExpectedISK7d, plan.Actions[1].ExpectedISK7d)
	}
	if plan.Actions[0].DownsideISK7d <= plan.Actions[1].DownsideISK7d {
		t.Fatalf("downside %.0f should beat %.0f",
			plan.Actions[0].DownsideISK7d, plan.Actions[1].DownsideISK7d)
	}
}

func TestBuildTodayPlanBlocksItemsYourOwnHistoryLostMoneyOn(t *testing.T) {
	in := todayTestInputs(todayTestTrade(1, 10e6, 9e6))
	in.ItemHistory = map[int32]TodayItemHistory{
		1: {TypeID: 1, SellsQty: 14, RealizedISK: -3.1e6},
	}

	plan := BuildTodayPlan(in, TodayOpts{})

	if len(plan.Actions) != 0 {
		t.Fatalf("advised %d actions, want none - a losing record must not be recommended", len(plan.Actions))
	}
	held, ok := todayFindAction(t, plan.NotAdvised, 1)
	if !ok {
		t.Fatal("the blocked row vanished entirely; it must stay visible in NotAdvised")
	}
	if held.Grade != TodayGradeAvoid {
		t.Fatalf("grade = %q, want %q", held.Grade, TodayGradeAvoid)
	}
	if len(held.Reliability.Blockers) == 0 {
		t.Fatal("blocked row carries no blocker text, so the UI cannot say why")
	}
}

// Below the real-sample floor a losing streak is noise: hold the row back,
// but do not condemn the item outright.
func TestBuildTodayPlanTinyLosingSampleIsUnprovenNotAvoid(t *testing.T) {
	in := todayTestInputs(todayTestTrade(1, 10e6, 9e6))
	in.ItemHistory = map[int32]TodayItemHistory{
		1: {TypeID: 1, SellsQty: 2, RealizedISK: -1e6},
	}

	plan := BuildTodayPlan(in, TodayOpts{})
	held, ok := todayFindAction(t, plan.NotAdvised, 1)
	if !ok {
		t.Fatalf("row was advised on a 2-sale losing sample; actions=%d", len(plan.Actions))
	}
	if held.Grade == TodayGradeAvoid {
		t.Fatal("a 2-sale sample condemned the item; that is noise, not evidence")
	}
}

func TestBuildTodayPlanRespectsDoNotTradeVerdict(t *testing.T) {
	in := todayTestInputs(todayTestTrade(1, 10e6, 9e6))
	in.EdgeByType = map[int32]TodayEdge{
		1: {LabelCode: "do_not_trade", Advice: "Personal history is weak here.", SampleTrades: 8},
	}

	plan := BuildTodayPlan(in, TodayOpts{})
	if len(plan.Actions) != 0 {
		t.Fatalf("advised %d actions despite a do_not_trade verdict", len(plan.Actions))
	}
	if got := plan.NotAdvised[0].Grade; got != TodayGradeAvoid {
		t.Fatalf("grade = %q, want %q", got, TodayGradeAvoid)
	}
}

// Unknown is not zero. A row missing an input the profit claim depends on
// must not appear in the queue asserting a number.
func TestBuildTodayPlanTreatsMissingInputsAsUnknownNotZero(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*StationTrade)
	}{
		{"no price history", func(tr *StationTrade) { tr.HistoryAvailable = false }},
		{"no volume estimate", func(tr *StationTrade) { tr.DailyVolume = 0; tr.S2BPerDay = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trade := todayTestTrade(1, 10e6, 9e6)
			tc.mutate(&trade)
			plan := BuildTodayPlan(todayTestInputs(trade), TodayOpts{})

			if len(plan.Actions) != 0 {
				t.Fatalf("advised an action with a missing input (%s)", tc.name)
			}
			if len(plan.NotAdvised) == 0 {
				t.Fatal("row disappeared instead of being held back as unproven")
			}
			if got := plan.NotAdvised[0].Grade; got != TodayGradeUnproven {
				t.Fatalf("grade = %q, want %q - missing data is unknown, not bad", got, TodayGradeUnproven)
			}
		})
	}
}

// A position with no cost basis cannot be said to be in profit, which is
// exactly the trap OrderDeskOrder.MarginBasis exists to warn about.
func TestBuildTodayPlanListingWithoutCostBasisIsUnproven(t *testing.T) {
	in := TodayInputs{
		Now:     todayTestNow,
		Capital: TodayCapitalInput{WalletISK: 1e9},
		Positions: []TodayPosition{{
			TypeID: 7, TypeName: "Salvaged Alloys", Qty: 100,
			MarketPrice: 5000, UnrealizedISK: 200e3, CostBasis: 0,
			StationID: 60003760,
		}},
	}
	plan := BuildTodayPlan(in, TodayOpts{})
	if len(plan.Actions) != 0 {
		t.Fatalf("advised listing stock whose cost is unknown; actions=%d", len(plan.Actions))
	}
	if got := plan.NotAdvised[0].Grade; got != TodayGradeUnproven {
		t.Fatalf("grade = %q, want %q", got, TodayGradeUnproven)
	}
}

// --- Repricing economics ----------------------------------------------

func todayTestDeskOrder() OrderDeskOrder {
	return OrderDeskOrder{
		OrderID: 900, TypeID: 5, TypeName: "Zeugma", LocationID: 60003760,
		LocationName: "Jita IV-4", IsBuyOrder: false,
		Price: 1_240_000, SuggestedPrice: 1_237_000,
		VolumeRemain: 100, Notional: 124e6, NetNotional: 120e6,
		Position: 3, TotalOrders: 11, BookAvailable: true,
		EstimatedFillPerDay: 20, QueueAheadQty: 200,
		MarginUnitISK: 1000, MarginBasis: "cost_basis",
		FlowBasis: "weekday", ETADays: 4.1, DaysToClearQueue: 2.2,
		DaysToExpire:     30,
		NetRelistGainISK: -5000,
		Recommendation:   "reprice",
	}
}

func TestBuildTodayPlanValuesRepriceAsUnlockedFillNotRelistGain(t *testing.T) {
	o := todayTestDeskOrder()
	in := TodayInputs{
		Now:         todayTestNow,
		Desk:        &OrderDeskResponse{Orders: []OrderDeskOrder{o}},
		Capital:     TodayCapitalInput{WalletISK: 1e9},
		ItemHistory: todayTestGoodHistory(5),
	}

	plan := BuildTodayPlan(in, TodayOpts{})
	a, ok := todayFindAction(t, plan.Actions, 5)
	if !ok {
		t.Fatalf("reprice not advised; not-advised=%d", len(plan.NotAdvised))
	}

	// 20/day over 7 days clears the 200-unit queue and then all 100 of
	// ours, so repricing unlocks the full remainder at 1000 ISK margin,
	// less the 5k it costs to move.
	const wantExpected = 100*1000.0 - 5000
	if math.Abs(a.ExpectedISK7d-wantExpected) > 1 {
		t.Fatalf("expected = %.0f, want %.0f", a.ExpectedISK7d, wantExpected)
	}
	// The desk publishes no band, so a weekday-profile fill is discounted.
	const wantDownside = 100*0.75*1000.0 - 5000
	if math.Abs(a.DownsideISK7d-wantDownside) > 1 {
		t.Fatalf("downside = %.0f, want %.0f", a.DownsideISK7d, wantDownside)
	}
	// NetRelistGainISK is the cost of moving, never the reward. If the
	// value ever equals it, the sign error is back.
	if a.ExpectedISK7d == o.NetRelistGainISK {
		t.Fatal("reprice value equals NetRelistGainISK - that field is a cost, not a gain")
	}
}

// An order already at the front of the queue gains nothing from repricing,
// and the broker fee makes it a straight loss.
func TestBuildTodayPlanWillNotAdviseARepriceThatOnlyCostsMoney(t *testing.T) {
	o := todayTestDeskOrder()
	o.QueueAheadQty = 0

	in := TodayInputs{
		Now:         todayTestNow,
		Desk:        &OrderDeskResponse{Orders: []OrderDeskOrder{o}},
		ItemHistory: todayTestGoodHistory(5),
	}
	plan := BuildTodayPlan(in, TodayOpts{})

	if len(plan.Actions) != 0 {
		t.Fatalf("advised a reprice worth %.0f ISK", plan.Actions[0].ExpectedISK7d)
	}
	if got := plan.NotAdvised[0].Grade; got != TodayGradeAvoid {
		t.Fatalf("grade = %q, want %q", got, TodayGradeAvoid)
	}
}

func TestBuildTodayPlanRepriceWithoutCostBasisIsUnproven(t *testing.T) {
	o := todayTestDeskOrder()
	o.MarginBasis = "none"

	plan := BuildTodayPlan(TodayInputs{
		Now:         todayTestNow,
		Desk:        &OrderDeskResponse{Orders: []OrderDeskOrder{o}},
		ItemHistory: todayTestGoodHistory(5),
	}, TodayOpts{})

	if len(plan.Actions) != 0 {
		t.Fatal("advised a reprice whose profitability is unknown")
	}
	if got := plan.NotAdvised[0].Grade; got != TodayGradeUnproven {
		t.Fatalf("grade = %q, want %q", got, TodayGradeUnproven)
	}
}

// Cancelling a buy order returns ISK to the wallet; cancelling a sell order
// returns stock to the hangar. Only one of those frees capital, and pricing
// the other as though it did would float it up the queue on money that
// never arrives.
func TestBuildTodayPlanOnlyBuyCancelsFreeCapital(t *testing.T) {
	buy := OrderDeskOrder{
		OrderID: 1, TypeID: 10, TypeName: "A", LocationID: 60003760,
		IsBuyOrder: true, Notional: 500e6, VolumeRemain: 10,
		Recommendation: "cancel", Reason: "buried", DaysToExpire: 20,
	}
	sell := OrderDeskOrder{
		OrderID: 2, TypeID: 11, TypeName: "B", LocationID: 60003760,
		IsBuyOrder: false, Notional: 500e6, VolumeRemain: 10,
		Recommendation: "cancel", Reason: "buried", DaysToExpire: 20,
	}

	plan := BuildTodayPlan(TodayInputs{
		Now:     todayTestNow,
		Desk:    &OrderDeskResponse{Orders: []OrderDeskOrder{buy, sell}},
		Capital: TodayCapitalInput{WalletISK: 100e6, BuyOrderISK: 500e6},
	}, TodayOpts{})

	buyAction, ok := todayFindAction(t, plan.Actions, 10)
	if !ok {
		t.Fatal("buy cancel missing from the queue")
	}
	sellAction, ok := todayFindAction(t, plan.Actions, 11)
	if !ok {
		t.Fatal("sell cancel missing from the queue")
	}
	if buyAction.ExpectedISK7d <= 0 {
		t.Fatalf("buy cancel valued at %.0f; freeing 500M should be worth something", buyAction.ExpectedISK7d)
	}
	if sellAction.ExpectedISK7d != 0 {
		t.Fatalf("sell cancel valued at %.0f; it frees no ISK", sellAction.ExpectedISK7d)
	}
}

// --- Certain work ------------------------------------------------------

func TestBuildTodayPlanGradesCollectedISKAsProven(t *testing.T) {
	in := TodayInputs{
		Now: todayTestNow,
		IndustryJobs: []TodayIndustryJob{{
			JobID: 1, ProductTypeID: 200, ProductTypeName: "Hobgoblin II",
			ProductQuantity: 500, UnitValueISK: 100e3,
			EndDate: todayTestNow.Add(-3 * time.Hour), Status: "active",
			OutputLocationID: 1234, FacilityName: "Botane Sotiyo",
		}},
		Planets: []TodayPlanet{{
			PlanetID: 40001, SolarSystemName: "Botane",
			ExpiredExtractorPins: 2, NetISKPerDay: 2.1e6,
			NextExpiry: todayTestNow.Add(-2 * time.Hour),
		}},
	}

	plan := BuildTodayPlan(in, TodayOpts{})
	if len(plan.Actions) != 2 {
		t.Fatalf("advised %d actions, want 2 (deliver + PI)", len(plan.Actions))
	}
	for _, a := range plan.Actions {
		if a.Grade != TodayGradeProven {
			t.Fatalf("%s graded %q; ISK that already exists is not a forecast", a.Kind, a.Grade)
		}
	}
	// An expired extractor is bleeding now, so it leads regardless of size.
	if plan.Actions[0].Kind != TodayActionPIRestart {
		t.Fatalf("top action is %q, want the expired extractor first", plan.Actions[0].Kind)
	}
	if plan.Actions[0].Urgency != "now" {
		t.Fatalf("expired extractor urgency = %q, want now", plan.Actions[0].Urgency)
	}
}

// --- Sizing ------------------------------------------------------------

func TestCapTodayQuantityTakesTheSmallestBoundAndNamesIt(t *testing.T) {
	cases := []struct {
		name    string
		lim     todayQuantityLimits
		wantQty int64
		wantCap string
	}{
		{
			name:    "investment ceiling",
			lim:     todayQuantityLimits{FlowPerDay: 100, AvgDailyVolume: 1000, UnitPrice: 100, FreeWalletISK: 1e9, MaxInvestmentISK: 2000},
			wantQty: 20,
			wantCap: "your max investment setting",
		},
		{
			name:    "market depth",
			lim:     todayQuantityLimits{FlowPerDay: 1000, AvgDailyVolume: 40, UnitPrice: 100, FreeWalletISK: 1e9},
			wantQty: 10,
			wantCap: "market depth",
		},
		{
			name:    "wallet",
			lim:     todayQuantityLimits{FlowPerDay: 1000, AvgDailyVolume: 1e6, UnitPrice: 100, FreeWalletISK: 550},
			wantQty: 5,
			wantCap: "free ISK in your wallet",
		},
		{
			name: "your own record",
			lim: todayQuantityLimits{
				FlowPerDay: 1000, AvgDailyVolume: 1e6, UnitPrice: 100, FreeWalletISK: 1e9,
				Edge: &TodayEdge{MaxRecommendedQty: 7},
			},
			wantQty: 7,
			wantCap: "the size your own trades have worked at",
		},
		{
			name: "portfolio concentration",
			lim: todayQuantityLimits{
				FlowPerDay: 1000, AvgDailyVolume: 1e6, UnitPrice: 100, FreeWalletISK: 1e9,
				Risk: &TodayPositionRisk{SuggestedBuyISK: 300},
			},
			wantQty: 3,
			wantCap: "portfolio concentration",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := capTodayQuantity(tc.lim)
			if got.Quantity != tc.wantQty {
				t.Fatalf("quantity = %d, want %d", got.Quantity, tc.wantQty)
			}
			found := false
			for _, c := range got.Caps {
				if c == tc.wantCap {
					found = true
				}
			}
			if !found {
				t.Fatalf("caps = %v, want it to name %q", got.Caps, tc.wantCap)
			}
		})
	}
}

// Every bound below one unit is a real answer - there is no size at which
// this is worth doing - not something to round up to 1.
func TestCapTodayQuantityReturnsNothingWhenNoSizeFits(t *testing.T) {
	got := capTodayQuantity(todayQuantityLimits{
		FlowPerDay: 100, AvgDailyVolume: 1000, UnitPrice: 1e6, FreeWalletISK: 1000,
	})
	if got.Quantity != 0 {
		t.Fatalf("quantity = %d, want 0 when the wallet cannot afford one unit", got.Quantity)
	}
}

func TestRealityDiscountOnlyEverCutsAProjection(t *testing.T) {
	if got := realityDiscount(nil); got != 1 {
		t.Fatalf("no edge = %v, want 1 (no discount)", got)
	}
	if got := realityDiscount(&TodayEdge{RealityRatio: 2.5}); got != 1 {
		t.Fatalf("ratio 2.5 = %v, want 1 - a good record must not inflate a projection", got)
	}
	if got := realityDiscount(&TodayEdge{RealityRatio: 0.05}); got != todayRealityFloor {
		t.Fatalf("ratio 0.05 = %v, want the floor %v - one bad month must not empty the queue",
			got, todayRealityFloor)
	}
}

// --- Prices, budget, empties -------------------------------------------

// EVE rejects a price off its 4-significant-digit grid, so a price the user
// has to round is a price the app failed to compute.
func TestBuildTodayPlanPastePricesAreOnTheLegalGrid(t *testing.T) {
	plan := BuildTodayPlan(todayTestInputs(todayTestTrade(1, 10e6, 9e6)), TodayOpts{})
	if len(plan.Actions) == 0 {
		t.Fatal("no actions to check")
	}
	a := plan.Actions[0]
	if a.PastePrice <= a.CurrentPrice {
		t.Fatalf("bid %.4f does not beat the current best buy %.4f", a.PastePrice, a.CurrentPrice)
	}
	step := priceStep(a.PastePrice)
	if math.Abs(math.Round(a.PastePrice/step)*step-a.PastePrice) > step/1000 {
		t.Fatalf("bid %.6f is not a multiple of the legal step %v", a.PastePrice, step)
	}
}

func TestBuildTodayPlanCutsTheListAtTheTimeBudget(t *testing.T) {
	trades := make([]StationTrade, 0, 5)
	for i := int32(1); i <= 5; i++ {
		trades = append(trades, todayTestTrade(i, float64(10-i)*1e6, float64(10-i)*1e6))
	}
	plan := BuildTodayPlan(todayTestInputs(trades...), TodayOpts{BudgetSeconds: 100})

	// Buys cost 45s each, so two fit inside 100 and the third does not.
	if plan.Budget.InBudgetCount != 2 {
		t.Fatalf("in-budget = %d, want 2 at 45s each in a 100s budget", plan.Budget.InBudgetCount)
	}
	if plan.Budget.TotalCount != 5 {
		t.Fatalf("total = %d, want 5", plan.Budget.TotalCount)
	}
	if plan.Actions[2].InBudget {
		t.Fatal("third action marked in-budget")
	}
	if plan.Budget.BeyondISK7d <= 0 {
		t.Fatal("nothing counted beyond the cut line")
	}
}

func TestBuildTodayPlanOnEmptyInputsIsEmptyNotZeroed(t *testing.T) {
	plan := BuildTodayPlan(TodayInputs{}, TodayOpts{})

	if plan.Actions == nil || len(plan.Actions) != 0 {
		t.Fatalf("actions = %#v, want an empty non-nil slice", plan.Actions)
	}
	if plan.Budget.TotalCount != 0 || plan.Budget.InBudgetCount != 0 {
		t.Fatalf("budget = %+v, want zeros", plan.Budget)
	}
	if plan.Performance.Measured {
		t.Fatal("performance reported as measured with no journal days")
	}
	if len(plan.Warnings) == 0 {
		t.Fatal("an empty plan must say why it is empty")
	}
	if plan.GeneratedAt == "" {
		t.Fatal("no generated_at stamp")
	}
}

// The daily-return rate prices idle and freed capital. It must not run away
// on one good month, and must not collapse to zero on a flat one.
func TestTodayDailyReturnRateIsBounded(t *testing.T) {
	if got := todayDailyReturnRate(TodayPerformance{}, 0); got != todayMinDailyReturn {
		t.Fatalf("no data = %v, want the floor %v", got, todayMinDailyReturn)
	}
	huge := TodayPerformance{Avg30dISKPerDay: 1e9}
	if got := todayDailyReturnRate(huge, 1e9); got != todayMaxDailyReturn {
		t.Fatalf("runaway rate = %v, want the ceiling %v", got, todayMaxDailyReturn)
	}
	normal := TodayPerformance{Avg30dISKPerDay: 10e6}
	if got := todayDailyReturnRate(normal, 1e9); math.Abs(got-0.01) > 1e-9 {
		t.Fatalf("10M/day on 1B = %v, want 0.01", got)
	}
}

func TestBuildTodayPlanBatchesCarryOnlyAdvisedRows(t *testing.T) {
	good := todayTestTrade(1, 10e6, 9e6)
	bad := todayTestTrade(2, 20e6, 19e6)

	in := todayTestInputs(good, bad)
	in.EdgeByType = map[int32]TodayEdge{2: {LabelCode: "do_not_trade", Advice: "no"}}

	plan := BuildTodayPlan(in, TodayOpts{})
	if len(plan.Batches) != 1 {
		t.Fatalf("batches = %d, want 1", len(plan.Batches))
	}
	for _, item := range plan.Batches[0].Items {
		if item.TypeID == 2 {
			t.Fatal("a blocked row reached the multibuy batch, bypassing the grading entirely")
		}
	}
}

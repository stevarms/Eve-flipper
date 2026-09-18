package engine

import (
	"math"
	"strings"
	"testing"

	"eve-flipper/internal/esi"
)

// Owner IDs used throughout. The character and the corporation deliberately
// share the numeric ID 2002: the owner key is kind plus ID, and a test where the
// two IDs differ would pass even if the kind were dropped from the key.
const (
	ownerJitaAlt   int64 = 1001
	ownerFrontline int64 = 2002
	ownerCorp      int64 = 2002
)

func fwLot(state string, qty int64, unitCost float64) FWLot {
	return FWLot{
		TypeID:                typeInfernoLM,
		TypeName:              "Inferno Light Missile",
		State:                 state,
		Qty:                   qty,
		QtyRemaining:          qty,
		UnitCostISK:           unitCost,
		DestStationID:         staVillasenV,
		AcquiredByCharacterID: ownerJitaAlt,
		HolderOwnerKind:       "character",
		HolderOwnerID:         ownerFrontline,
		HolderName:            "Frontline Alt",
	}
}

func stateOf(t *testing.T, budget FWBudget, state string) FWStateCommitment {
	t.Helper()
	for _, row := range budget.ByState {
		if row.State == state {
			return row
		}
	}
	t.Fatalf("state %q is missing from the breakdown; every state holding a lot must appear", state)
	return FWStateCommitment{}
}

// TestMeasureFWBudget_StatesConsumeAndRelease is the plan's budget rule: the four
// middle states all consume headroom and consume the same amount, sold and pulled
// release it.
//
// Every lot here is identical but for its state, so the only thing that can be
// producing a difference is the state -- which is the whole claim. The equal
// amounts matter as much as the totals: verification step 11 marks one lot
// bought, then in_transit, then at_dest, then listed, and headroom must not move
// after the purchase.
func TestMeasureFWBudget_StatesConsumeAndRelease(t *testing.T) {
	const perLot = 100_000_000.0 // 100 units at 1M

	var lots []FWLot
	for _, state := range fwLotLifecycle {
		lots = append(lots, fwLot(state, 100, 1_000_000))
	}
	budget := MeasureFWBudget(1_000_000_000, lots)

	if budget.CommittedISK != 4*perLot {
		t.Errorf("committed = %.0f, want %.0f -- exactly four states consume", budget.CommittedISK, 4*perLot)
	}
	if budget.HeadroomISK != 600_000_000 {
		t.Errorf("headroom = %.0f, want 600000000", budget.HeadroomISK)
	}
	if budget.Lots != 7 || budget.CommittedLots != 4 {
		t.Errorf("lots = %d and committed lots = %d, want 7 and 4", budget.Lots, budget.CommittedLots)
	}

	for _, state := range []string{FWLotBought, FWLotInTransit, FWLotAtDest, FWLotListed} {
		if got := stateOf(t, budget, state).CommittedISK; got != perLot {
			t.Errorf("%s committed %.0f, want %.0f -- the handoff must not change what is committed", state, got, perLot)
		}
	}
	for _, state := range []string{FWLotPlanned, FWLotSold, FWLotPulled} {
		row := stateOf(t, budget, state)
		if row.CommittedISK != 0 {
			t.Errorf("%s committed %.0f, want 0", state, row.CommittedISK)
		}
		if row.Lots != 1 {
			t.Errorf("%s shows %d lots, want 1 -- a released lot still has to be countable", state, row.Lots)
		}
	}

	// Lifecycle order, not descending ISK: the breakdown answers "where is my
	// capital stuck", and that answer is a position along the handoff.
	for i, row := range budget.ByState {
		if row.State != fwLotLifecycle[i] {
			t.Fatalf("by-state row %d is %q, want %q", i, row.State, fwLotLifecycle[i])
		}
	}
}

// TestMeasureFWBudget_PlannedIsNotSpent is the deliberate departure from the
// plan's shorthand formula, which reads "everything not sold or pulled".
//
// A planned lot has spent nothing. If it consumed headroom, saving a plan and
// regenerating it would subtract the plan from itself -- the second pass would
// find no budget for the very items it had just proposed. This test is that
// round trip, because the totals alone would not show the consequence.
func TestMeasureFWBudget_PlannedIsNotSpent(t *testing.T) {
	planned := []FWLot{fwLot(FWLotPlanned, 7_000, 100)}
	budget := MeasureFWBudget(1_000_000, planned)

	if budget.CommittedISK != 0 || budget.TrimHeadroom() != 1_000_000 {
		t.Fatalf("committed %.0f with %.0f trimmable, want 0 and the full budget",
			budget.CommittedISK, budget.TrimHeadroom())
	}

	rows := BuildFWSupplyPlan([]FWSupplyItem{gapItem(typeInfernoLM, "Inferno Light Missile", 1_000, 0)}, coverConfig())
	shipment := BuildFWShipment(rows, FWShipmentConfig{HeadroomISK: budget.TrimHeadroom()})
	if len(shipment.Lines) != 1 || shipment.Lines[0].ShipQty != 7_000 {
		t.Fatalf("regenerating against saved planned lots shipped %+v; the plan must not eat its own headroom",
			shipment.Lines)
	}
}

// TestMeasureFWBudget_BuyerIsNotTheHolder: the Jita alt buys, someone else holds.
// The ISK is committed exactly once and shows against the holder, because the
// question the panel answers is "whose hangar is my capital sitting in".
func TestMeasureFWBudget_BuyerIsNotTheHolder(t *testing.T) {
	held := fwLot(FWLotAtDest, 100, 1_000_000)

	corpHeld := fwLot(FWLotListed, 50, 1_000_000)
	corpHeld.HolderOwnerKind = "corporation"
	corpHeld.HolderOwnerID = ownerCorp
	corpHeld.HolderName = "Deep Space Logistics"

	budget := MeasureFWBudget(1_000_000_000, []FWLot{held, corpHeld})

	if budget.CommittedISK != 150_000_000 {
		t.Errorf("committed = %.0f, want 150000000 -- counted once, not once per owner", budget.CommittedISK)
	}
	if len(budget.ByOwner) != 2 {
		t.Fatalf("by-owner has %d rows, want 2 -- a character and a corporation with the same numeric "+
			"ID are different owners", len(budget.ByOwner))
	}

	first, second := budget.ByOwner[0], budget.ByOwner[1]
	if first.OwnerKind != "character" || first.OwnerID != ownerFrontline || first.CommittedISK != 100_000_000 {
		t.Errorf("largest holder = %+v, want the character at 100M", first)
	}
	if second.OwnerKind != "corporation" || second.CommittedISK != 50_000_000 {
		t.Errorf("second holder = %+v, want the corporation at 50M", second)
	}
	for _, owner := range budget.ByOwner {
		if owner.OwnerID == ownerJitaAlt && owner.OwnerKind == "character" {
			t.Errorf("the buyer appears as a holder: %+v -- which wallet paid is accounting", owner)
		}
	}
}

// TestMeasureFWBudget_DestsGroupSeparately. One destination per campaign today,
// but lots carry their own, and grouping by it from day one is what keeps a
// forward tier additive rather than a migration.
func TestMeasureFWBudget_DestsGroupSeparately(t *testing.T) {
	rear := fwLot(FWLotAtDest, 100, 1_000_000)
	rear.DestStationID = staOnnamonIV

	forward := fwLot(FWLotListed, 20, 1_000_000)
	forward.DestStationID = staVillasenV

	budget := MeasureFWBudget(1_000_000_000, []FWLot{rear, forward})

	if len(budget.ByDest) != 2 {
		t.Fatalf("by-dest has %d rows, want 2", len(budget.ByDest))
	}
	if budget.ByDest[0].DestStationID != staOnnamonIV || budget.ByDest[0].CommittedISK != 100_000_000 {
		t.Errorf("first dest = %+v, want Onnamon IV at 100M", budget.ByDest[0])
	}
	if budget.ByDest[1].DestStationID != staVillasenV || budget.ByDest[1].CommittedISK != 20_000_000 {
		t.Errorf("second dest = %+v, want Villasen V at 20M", budget.ByDest[1])
	}
	if budget.CommittedISK != 120_000_000 {
		t.Errorf("committed = %.0f, want the two dests to sum to 120M", budget.CommittedISK)
	}
}

// TestMeasureFWBudget_ListedValueNeverGates. A listed lot marked up 3x is still
// 100M of capital, not 300M of headroom. Treating an expectation as budget is how
// a budget stops being one.
func TestMeasureFWBudget_ListedValueNeverGates(t *testing.T) {
	listed := fwLot(FWLotListed, 100, 1_000_000)
	listed.ListedPrice = 3_000_000

	budget := MeasureFWBudget(1_000_000_000, []FWLot{listed})

	if budget.CommittedISK != 100_000_000 {
		t.Errorf("committed = %.0f, want cost not market value", budget.CommittedISK)
	}
	if budget.ListedValueISK != 300_000_000 {
		t.Errorf("listed value = %.0f, want 300000000 reported alongside", budget.ListedValueISK)
	}
	if budget.HeadroomISK != 900_000_000 {
		t.Errorf("headroom = %.0f, want 900000000 -- the markup must not move it", budget.HeadroomISK)
	}
}

// TestMeasureFWBudget_OverCommittedIsNotClamped. Lower the budget on a running
// campaign and every existing lot stays bought. How far over is the useful
// number, so headroom goes negative -- but nothing may be bought on it.
func TestMeasureFWBudget_OverCommittedIsNotClamped(t *testing.T) {
	budget := MeasureFWBudget(100_000_000, []FWLot{fwLot(FWLotListed, 400, 1_000_000)})

	if budget.HeadroomISK != -300_000_000 {
		t.Errorf("headroom = %.0f, want -300000000 shown rather than hidden at zero", budget.HeadroomISK)
	}
	if !budget.OverCommitted() {
		t.Error("OverCommitted() is false at -300M")
	}
	if budget.TrimHeadroom() != 0 {
		t.Errorf("TrimHeadroom() = %.0f, want 0 -- negative headroom buys nothing", budget.TrimHeadroom())
	}
}

// gapItem is a fully stocked-out candidate: nothing local, a live Jita price, and
// enough killmails behind it to be shippable.
func gapItem(typeID int32, name string, dailyDestroyed float64, stocked int32) FWSupplyItem {
	item := FWSupplyItem{
		TypeID:         typeID,
		TypeName:       name,
		Category:       "ammo",
		VolumeM3:       missileVolumeM3,
		DailyDestroyed: dailyDestroyed,
		KillsWithItem:  40,
		JitaBestSell:   100,
	}
	if stocked > 0 {
		item.LocalOrders = []esi.MarketOrder{fwSell(typeID, staVillasenV, 150, stocked)}
	}
	return item
}

// TestBuildFWShipment_BiggestHolesFirst is the budget doing what the plan says it
// does: 1.5B buys the biggest holes first.
//
// Three gaps of 700k, 350k and 9.5M ISK at cost against 800k of headroom. The
// deepest gap is funded whole, the next is cut to what is left, and the third --
// which is a hull, and expensive -- gets nothing. The ordering comes from
// BuildFWSupplyPlan and is not re-derived here, so a change to the ranking shows
// up in this test rather than diverging from it silently.
func TestBuildFWShipment_BiggestHolesFirst(t *testing.T) {
	rows := BuildFWSupplyPlan([]FWSupplyItem{
		gapItem(typeInfernoLM, "Inferno Light Missile", 1_000, 0), // deficit 7,000 -> 700,000 ISK
		gapItem(typeNovaLM, "Nova Light Missile", 500, 0),         // deficit 3,500 -> 350,000 ISK
		{
			TypeID: typeTristan, TypeName: "Tristan", Category: "ship",
			VolumeM3: tristanVolumeM3, DailyDestroyed: 3, KillsWithItem: 40, JitaBestSell: 500_000,
			LocalOrders: []esi.MarketOrder{fwSell(typeTristan, staVillasenV, 700_000, 2)},
		},
	}, coverConfig())

	shipment := BuildFWShipment(rows, FWShipmentConfig{HeadroomISK: 800_000, CargoCapacityM3: 1_500})

	if len(shipment.Lines) != 3 {
		t.Fatalf("shipped %d lines, want 3 -- a dropped item stays on the list saying it got nothing", len(shipment.Lines))
	}
	if shipment.FullyFunded != 1 || shipment.Trimmed != 1 || shipment.Dropped != 1 {
		t.Errorf("funded/trimmed/dropped = %d/%d/%d, want 1/1/1",
			shipment.FullyFunded, shipment.Trimmed, shipment.Dropped)
	}

	first := shipment.Lines[0]
	if first.TypeID != typeInfernoLM || first.ShipQty != 7_000 || first.TrimReason != "" {
		t.Errorf("first line = %d qty %d (%q), want the whole 7,000 Inferno",
			first.TypeID, first.ShipQty, first.TrimReason)
	}

	second := shipment.Lines[1]
	if second.TypeID != typeNovaLM || second.ShipQty != 1_000 {
		t.Fatalf("second line = %d qty %d, want 1,000 Nova -- what 100,000 ISK of headroom buys",
			second.TypeID, second.ShipQty)
	}
	if second.PlannedQty != 3_500 || second.Funded() {
		t.Errorf("trimmed line reports planned %d and Funded() %v, want 3,500 and false -- a half-funded "+
			"gap must not read as a small one", second.PlannedQty, second.Funded())
	}
	if !strings.Contains(second.TrimReason, "budget") {
		t.Errorf("trim reason = %q, want it to name the budget as the binding constraint", second.TrimReason)
	}

	third := shipment.Lines[2]
	if third.ShipQty != 0 || third.ShipCostISK != 0 || !strings.Contains(third.TrimReason, "dropped") {
		t.Errorf("third line = qty %d cost %.0f (%q), want a dropped hull",
			third.ShipQty, third.ShipCostISK, third.TrimReason)
	}

	if shipment.TotalCostISK != 800_000 || shipment.RemainingISK != 0 {
		t.Errorf("total %.0f with %.0f left, want the whole 800,000 spent",
			shipment.TotalCostISK, shipment.RemainingISK)
	}
	if shipment.TotalCargoM3 != 120 || shipment.Trips != 1 {
		t.Errorf("cargo %.2f m3 over %d trips, want 120 m3 in one -- 8,000 missiles is a rounding error",
			shipment.TotalCargoM3, shipment.Trips)
	}

	// The greedy rank order's uncomfortable consequence, stated rather than
	// smoothed over: one row took most of the list and others went short.
	if len(shipment.Notes) == 0 || !strings.Contains(shipment.Notes[0], "Inferno Light Missile") {
		t.Errorf("notes = %v, want the row that absorbed the budget named", shipment.Notes)
	}
}

// TestBuildFWShipment_ExactlyAffordableIsFullyFunded pins the boundary, so a
// rounding change cannot quietly turn a funded list into a trimmed one.
func TestBuildFWShipment_ExactlyAffordableIsFullyFunded(t *testing.T) {
	rows := BuildFWSupplyPlan([]FWSupplyItem{gapItem(typeInfernoLM, "Inferno Light Missile", 1_000, 0)}, coverConfig())
	if rows[0].CostISK != 700_000 {
		t.Fatalf("the fixture costs %.0f, not the 700,000 this boundary is about", rows[0].CostISK)
	}

	shipment := BuildFWShipment(rows, FWShipmentConfig{HeadroomISK: 700_000})

	if shipment.FullyFunded != 1 || shipment.Trimmed != 0 || shipment.Lines[0].ShipQty != 7_000 {
		t.Errorf("at exactly the cost: funded %d trimmed %d qty %d, want the whole line",
			shipment.FullyFunded, shipment.Trimmed, shipment.Lines[0].ShipQty)
	}
	if len(shipment.Notes) != 0 {
		t.Errorf("notes = %v, want silence when nothing went short", shipment.Notes)
	}
}

// TestBuildFWShipment_CargoBoundsTheList is where hull economics live. A packaged
// Tristan is 2,500 m3, so a deep space transport carries 24 in one trip and a
// blockade runner four -- the numbers from the plan, asserted rather than quoted.
func TestBuildFWShipment_CargoBoundsTheList(t *testing.T) {
	rows := BuildFWSupplyPlan([]FWSupplyItem{{
		TypeID: typeTristan, TypeName: "Tristan", Category: "ship",
		VolumeM3: tristanVolumeM3, DailyDestroyed: 5, KillsWithItem: 40, JitaBestSell: 500_000,
	}}, coverConfig())
	if rows[0].SuggestedQty != 35 {
		t.Fatalf("the fixture asks for %d hulls, not the 35 these trip counts are about", rows[0].SuggestedQty)
	}
	const noBudgetPressure = 1_000_000_000.0

	dst := BuildFWShipment(rows, FWShipmentConfig{HeadroomISK: noBudgetPressure, CargoCapacityM3: 60_000, MaxTrips: 1})
	if dst.Lines[0].ShipQty != 24 || !strings.Contains(dst.Lines[0].TrimReason, "cargo") {
		t.Errorf("one DST trip carried %d (%q), want 24 with cargo named as the constraint",
			dst.Lines[0].ShipQty, dst.Lines[0].TrimReason)
	}

	runner := BuildFWShipment(rows, FWShipmentConfig{HeadroomISK: noBudgetPressure, CargoCapacityM3: 10_000, MaxTrips: 1})
	if runner.Lines[0].ShipQty != 4 {
		t.Errorf("one blockade runner trip carried %d, want 4", runner.Lines[0].ShipQty)
	}

	twoTrips := BuildFWShipment(rows, FWShipmentConfig{HeadroomISK: noBudgetPressure, CargoCapacityM3: 60_000, MaxTrips: 2})
	if twoTrips.Lines[0].ShipQty != 35 || twoTrips.Trips != 2 {
		t.Errorf("two DST trips carried %d over %d trips, want all 35 over 2",
			twoTrips.Lines[0].ShipQty, twoTrips.Trips)
	}

	// A hold size with no trip limit is not a limit at all -- it only sizes the
	// reported trip count.
	unbounded := BuildFWShipment(rows, FWShipmentConfig{HeadroomISK: noBudgetPressure, CargoCapacityM3: 10_000})
	if unbounded.Lines[0].ShipQty != 35 || unbounded.Trips != 9 {
		t.Errorf("unbounded trips carried %d over %d trips, want all 35 over 9",
			unbounded.Lines[0].ShipQty, unbounded.Trips)
	}

	// The reported "9 trips" is the whole reason this note exists: it is
	// indistinguishable on screen from a nine-trip haul somebody sized for, so
	// the shipment has to say that nothing was trimmed to fit.
	if !hasNoteAbout(unbounded.Notes, "hauler limit is off") {
		t.Errorf("an unbounded list did not say so: %q", unbounded.Notes)
	}
	if hasNoteAbout(twoTrips.Notes, "hauler limit is off") {
		t.Errorf("a two-trip bounded list claimed the bound was off: %q", twoTrips.Notes)
	}

	// One hold is not a haul anybody could misread, so the note stays quiet
	// there -- otherwise every campaign without a trip limit carries it.
	oneHold := BuildFWShipment(rows, FWShipmentConfig{HeadroomISK: noBudgetPressure, CargoCapacityM3: 600_000})
	if oneHold.Trips != 1 {
		t.Fatalf("the fixture needs %d trips at 600k m3, not the 1 this case is about", oneHold.Trips)
	}
	if hasNoteAbout(oneHold.Notes, "hauler limit is off") {
		t.Errorf("a single-trip list carried the unbounded note: %q", oneHold.Notes)
	}
}

func hasNoteAbout(notes []string, substr string) bool {
	for _, n := range notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

// TestBuildFWShipment_ThinEvidenceIsNeverBought. The plan keeps a quantity on a
// row seen on one or two losses so the evidence is visible with a number beside
// it; the shipment is where that quantity is refused. Both halves are asserted,
// because the value of the first is that it is not silently zero.
func TestBuildFWShipment_ThinEvidenceIsNeverBought(t *testing.T) {
	item := gapItem(typeInfernoLM, "Inferno Light Missile", 1_000, 0)
	item.KillsWithItem = MinKillsWithItem - 1

	rows := BuildFWSupplyPlan([]FWSupplyItem{item}, coverConfig())
	if rows[0].Shippable || rows[0].SuggestedQty == 0 {
		t.Fatalf("the plan row reports shippable %v with qty %d, want a visible quantity on an unshippable row",
			rows[0].Shippable, rows[0].SuggestedQty)
	}

	shipment := BuildFWShipment(rows, FWShipmentConfig{HeadroomISK: 1_000_000_000})
	if len(shipment.Lines) != 0 {
		t.Errorf("shipped %+v on %d killmails' evidence, want nothing", shipment.Lines, item.KillsWithItem)
	}
}

// TestBuildFWShipment_ThinCoverAsksForNothing documents a consequence of the
// sizing formula that is easy to misread from the verdict name.
//
// A `thin` row has cover at or above the target by definition, so
// (target - cover) * destroyed is never positive and it ships zero. "Top up, do
// not lead" turns out to mean the top-up is nothing: an item already carrying
// eight days against a seven-day target needs no ISK. So `thin` is a label on the
// gap table, not a second shipping tier -- and the ordering rule the budget
// trims by only ever has gaps to order.
func TestBuildFWShipment_ThinCoverAsksForNothing(t *testing.T) {
	rows := BuildFWSupplyPlan([]FWSupplyItem{
		gapItem(typeScourgeLM, "Scourge Light Missile", 1_000, 10_000), // 10 days of cover
		gapItem(typeInfernoLM, "Inferno Light Missile", 1_000, 0),
	}, coverConfig())

	thin := rowFor(t, rows, typeScourgeLM)
	if thin.Verdict != VerdictThin {
		t.Fatalf("Scourge at 10 days is %q, want %q", thin.Verdict, VerdictThin)
	}
	if thin.SuggestedQty != 0 {
		t.Errorf("a thin row asks for %d units; past the cover target there is nothing to top up", thin.SuggestedQty)
	}

	shipment := BuildFWShipment(rows, FWShipmentConfig{HeadroomISK: 1_000_000_000})
	if len(shipment.Lines) != 1 || shipment.Lines[0].TypeID != typeInfernoLM {
		t.Errorf("shipped %+v, want only the gap", shipment.Lines)
	}
}

// TestBuildFWShipment_NothingToShipMakesNoTrip. RouteCargoTrips answers 1 for an
// empty cargo because a route hop happens regardless; a shipment with nothing in
// it is not a trip anyone makes.
func TestBuildFWShipment_NothingToShipMakesNoTrip(t *testing.T) {
	empty := BuildFWShipment(nil, FWShipmentConfig{HeadroomISK: 1_000_000_000, CargoCapacityM3: 60_000})
	if len(empty.Lines) != 0 || empty.Trips != 0 || empty.TotalCostISK != 0 {
		t.Errorf("empty shipment = %d lines, %d trips, %.0f ISK; want nothing on all three",
			len(empty.Lines), empty.Trips, empty.TotalCostISK)
	}

	broke := BuildFWShipment(
		BuildFWSupplyPlan([]FWSupplyItem{gapItem(typeInfernoLM, "Inferno Light Missile", 1_000, 0)}, coverConfig()),
		FWShipmentConfig{HeadroomISK: 0},
	)
	if broke.Dropped != 1 || broke.Trips != 0 {
		t.Errorf("with no headroom: dropped %d over %d trips, want 1 and 0", broke.Dropped, broke.Trips)
	}
	if len(broke.Notes) == 0 || !strings.Contains(broke.Notes[len(broke.Notes)-1], "got nothing") {
		t.Errorf("notes = %v, want the list to say plainly that an item got nothing", broke.Notes)
	}
}

// TestBuildFWShipment_ProfitFollowsTheTrimAndFreightIsInTheMargin holds apart the
// two figures that are easiest to conflate.
//
// The budget gates on what the stock cost, because that is the capital the
// campaign can get back. The margin is taken against landed cost, because
// freight is spent whether or not the item sells. So the two totals differ by
// exactly the freight, and a line the budget cut earns on what ships rather than
// on what cover asked for.
func TestBuildFWShipment_ProfitFollowsTheTrimAndFreightIsInTheMargin(t *testing.T) {
	cfg := coverConfig()
	cfg.FreightISKPerM3 = 10 // 0.15 ISK on a 0.015 m3 missile

	rows := BuildFWSupplyPlan([]FWSupplyItem{
		gapItem(typeInfernoLM, "Inferno Light Missile", 1_000, 0), // deficit 7,000
		gapItem(typeNovaLM, "Nova Light Missile", 500, 0),         // deficit 3,500
	}, cfg)

	// Enough for the first line whole and a fifth of the second.
	shipment := BuildFWShipment(rows, FWShipmentConfig{HeadroomISK: 770_000})

	first, second := shipment.Lines[0], shipment.Lines[1]
	if first.ShipQty != 7_000 || second.ShipQty != 700 || second.PlannedQty != 3_500 {
		t.Fatalf("shipped %d and %d of %d, want the whole first line and 700 of the second",
			first.ShipQty, second.ShipQty, second.PlannedQty)
	}

	const unitProfit = 83.85 // 200 x 0.92 less 100.15 landed
	if want := unitProfit * 7_000; math.Abs(first.ShipProfitISK-want) > 1e-6 {
		t.Errorf("first line earns %.2f, want %.2f", first.ShipProfitISK, want)
	}
	if want := unitProfit * 700; math.Abs(second.ShipProfitISK-want) > 1e-6 {
		t.Errorf("trimmed line earns %.2f, want %.2f -- on what ships, not on what it asked for",
			second.ShipProfitISK, want)
	}

	if want := 770_000.0; math.Abs(shipment.TotalCostISK-want) > 1e-6 {
		t.Errorf("stock cost %.2f, want %.2f -- freight buys no stock and must stay out of the budget",
			shipment.TotalCostISK, want)
	}
	if want := 7_700 * 100.15; math.Abs(shipment.TotalLandedISK-want) > 1e-6 {
		t.Errorf("landed %.2f, want %.2f -- cost plus the freight to move it", shipment.TotalLandedISK, want)
	}
	if shipment.TotalLandedISK <= shipment.TotalCostISK {
		t.Error("landed is not above cost -- the freight term has gone missing and the margin is overstated")
	}
	if want := unitProfit * 7_700; math.Abs(shipment.TotalProfitISK-want) > 1e-6 {
		t.Errorf("shipment earns %.2f, want %.2f -- the sum of the lines", shipment.TotalProfitISK, want)
	}
	if want := shipment.TotalProfitISK / shipment.TotalLandedISK * 100; math.Abs(shipment.MarginPct-want) > 1e-9 {
		t.Errorf("margin = %.4f%%, want %.4f%% against landed cost", shipment.MarginPct, want)
	}
}

// TestBuildFWShipment_NothingShippedEarnsNothing: an empty list must report a
// zero margin rather than a division by zero.
func TestBuildFWShipment_NothingShippedEarnsNothing(t *testing.T) {
	shipment := BuildFWShipment(nil, FWShipmentConfig{HeadroomISK: 1_000_000})
	if shipment.TotalProfitISK != 0 || shipment.TotalLandedISK != 0 || shipment.MarginPct != 0 {
		t.Errorf("empty shipment reports profit %.2f landed %.2f margin %.2f%%, want zeros",
			shipment.TotalProfitISK, shipment.TotalLandedISK, shipment.MarginPct)
	}
}

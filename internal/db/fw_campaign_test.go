package db

import (
	"testing"
	"time"

	"eve-flipper/internal/engine"
)

// Two users, so every scoping assertion has something to leak into.
const (
	fwUserA = "user-a"
	fwUserB = "user-b"
)

// Station and owner IDs from the live probes, so a failure reads as a place
// rather than a number: Onnamon IV is the Caldari bulwark station, Villasen V a
// frontline one.
const (
	fwStaOnnamonIV = 60015070
	fwStaVillasenV = 60015108

	fwJitaAltID   = 1001 // buys, never holds
	fwFrontlineID = 2002 // holds and sells
	fwCorpID      = 2002 // deliberately the same number as the character
)

func fwTestCampaign(userID string) *FWCampaign {
	return &FWCampaign{
		UserID:            userID,
		Name:              "Caldari -- Onnamon",
		MilitiaFactionID:  500001,
		DestStationID:     fwStaOnnamonIV,
		BudgetISK:         1_500_000_000,
		TargetCoverDays:   7,
		CoveredMultiple:   2,
		MinMarginPct:      12,
		FreightISKPerM3:   250,
		StepOverDaysCover: 0.5,
		CategoryCeilings:  map[string]float64{"hull": 1.3, "ammo": 2.5},
		ShipProfile:       "deep_space_transport",
		MaxTrips:          2,
		MaxJumpsFromFront: 2,
		PinnedSystems:     []int32{30045324},
		ExcludedSystems:   []int32{30045306},
		BuyerCharacterID:  fwJitaAltID,
		SellerOwnerKind:   "corporation",
		SellerOwnerID:     fwCorpID,
	}
}

// TestFWCampaignRoundTrip is the v52 assertion the plan names: a campaign and its
// lots survive a write and a read with the owner triple and the lot's own
// destination intact.
//
// The owner triple is the part worth pinning. The buyer is not the holder, and
// the two are stored in different columns for different reasons -- collapse them
// and the budget can no longer say whose hangar the stranded capital is in.
func TestFWCampaignRoundTrip(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	want := fwTestCampaign(fwUserA)
	id, err := d.SaveFWCampaign(want)
	if err != nil {
		t.Fatalf("save campaign: %v", err)
	}
	if id == 0 || want.CampaignID != id {
		t.Fatalf("save returned id %d, struct carries %d", id, want.CampaignID)
	}

	got, err := d.GetFWCampaign(fwUserA, id)
	if err != nil || got == nil {
		t.Fatalf("get campaign: %v (got %v)", err, got)
	}
	if got.MilitiaFactionID != 500001 || got.DestStationID != fwStaOnnamonIV {
		t.Errorf("militia/dest round-tripped as %d/%d", got.MilitiaFactionID, got.DestStationID)
	}
	if got.BudgetISK != 1_500_000_000 || got.MinMarginPct != 12 || got.StepOverDaysCover != 0.5 {
		t.Errorf("numbers round-tripped as budget %.0f margin %.1f stepover %.2f",
			got.BudgetISK, got.MinMarginPct, got.StepOverDaysCover)
	}
	if got.SourceStationID != jitaIVMoon4StationID {
		t.Errorf("source station defaulted to %d, want Jita 4-4 %d", got.SourceStationID, jitaIVMoon4StationID)
	}
	if got.CategoryCeilings["hull"] != 1.3 || got.CategoryCeilings["ammo"] != 2.5 {
		t.Errorf("category ceilings round-tripped as %v", got.CategoryCeilings)
	}
	if len(got.PinnedSystems) != 1 || got.PinnedSystems[0] != 30045324 {
		t.Errorf("pinned systems round-tripped as %v", got.PinnedSystems)
	}
	if len(got.ExcludedSystems) != 1 || got.ExcludedSystems[0] != 30045306 {
		t.Errorf("excluded systems round-tripped as %v", got.ExcludedSystems)
	}
	if got.BuyerCharacterID != fwJitaAltID || got.SellerOwnerKind != "corporation" || got.SellerOwnerID != fwCorpID {
		t.Errorf("ownership round-tripped as buyer %d seller %s/%d",
			got.BuyerCharacterID, got.SellerOwnerKind, got.SellerOwnerID)
	}
	if got.CreatedAt == "" || got.UpdatedAt == "" {
		t.Errorf("timestamps are empty: created %q updated %q", got.CreatedAt, got.UpdatedAt)
	}

	lot := &engine.FWLot{
		TypeID:                2488, // Inferno Light Missile
		TypeName:              "Inferno Light Missile",
		State:                 engine.FWLotAtDest,
		Qty:                   7000,
		QtyRemaining:          6400,
		UnitCostISK:           102.5,
		ListedPrice:           240,
		DestStationID:         fwStaVillasenV, // not the campaign's dest, on purpose
		AcquiredByCharacterID: fwJitaAltID,
		HolderOwnerKind:       "character",
		HolderOwnerID:         fwFrontlineID,
		HolderName:            "Frontline Alt",
	}
	if _, err := d.SaveFWLot(fwUserA, id, lot); err != nil {
		t.Fatalf("save lot: %v", err)
	}
	if lot.LotID == 0 {
		t.Fatal("save did not stamp the lot ID back onto the struct")
	}

	lots, err := d.GetFWLots(fwUserA, id)
	if err != nil {
		t.Fatalf("get lots: %v", err)
	}
	if len(lots) != 1 {
		t.Fatalf("got %d lots, want 1", len(lots))
	}
	back := lots[0]
	if back.TypeID != 2488 || back.TypeName != "Inferno Light Missile" || back.State != engine.FWLotAtDest {
		t.Errorf("lot identity round-tripped as %d/%q/%q", back.TypeID, back.TypeName, back.State)
	}
	if back.Qty != 7000 || back.QtyRemaining != 6400 || back.UnitCostISK != 102.5 || back.ListedPrice != 240 {
		t.Errorf("lot quantities round-tripped as %d/%d at %.2f listed %.2f",
			back.Qty, back.QtyRemaining, back.UnitCostISK, back.ListedPrice)
	}
	if back.DestStationID != fwStaVillasenV {
		t.Errorf("lot dest round-tripped as %d; a lot's dest is its own, defaulted from "+
			"the campaign rather than dictated by it", back.DestStationID)
	}
	if back.AcquiredByCharacterID != fwJitaAltID {
		t.Errorf("buyer round-tripped as %d, want the Jita alt %d", back.AcquiredByCharacterID, fwJitaAltID)
	}
	if back.HolderOwnerKind != "character" || back.HolderOwnerID != fwFrontlineID || back.HolderName != "Frontline Alt" {
		t.Errorf("holder round-tripped as %s/%d/%q", back.HolderOwnerKind, back.HolderOwnerID, back.HolderName)
	}
}

// TestFWLotsGroupByTheirOwnDest: two lots at two destinations must not merge.
//
// One destination per campaign is the shipped behaviour, so this asserts the
// column that keeps the two-tier option additive: the budget already groups by
// dest, so a forward tier is later lots with a different dest rather than a
// migration of the lots already recorded.
func TestFWLotsGroupByTheirOwnDest(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	id, err := d.SaveFWCampaign(fwTestCampaign(fwUserA))
	if err != nil {
		t.Fatalf("save campaign: %v", err)
	}

	for _, spec := range []struct {
		dest int64
		qty  int64
		cost float64
	}{
		{fwStaOnnamonIV, 1000, 100_000}, // 100M staged at the bulwark
		{fwStaVillasenV, 200, 100_000},  // 20M pushed forward
	} {
		lot := &engine.FWLot{
			TypeID:          2488,
			State:           engine.FWLotListed,
			Qty:             spec.qty,
			QtyRemaining:    spec.qty,
			UnitCostISK:     spec.cost,
			DestStationID:   spec.dest,
			HolderOwnerKind: "character",
			HolderOwnerID:   fwFrontlineID,
		}
		if _, err := d.SaveFWLot(fwUserA, id, lot); err != nil {
			t.Fatalf("save lot at %d: %v", spec.dest, err)
		}
	}

	budget, err := d.GetFWBudget(fwUserA, id)
	if err != nil {
		t.Fatalf("budget: %v", err)
	}
	if len(budget.ByDest) != 2 {
		t.Fatalf("got %d dest rows, want 2: %+v", len(budget.ByDest), budget.ByDest)
	}
	if budget.ByDest[0].DestStationID != fwStaOnnamonIV || budget.ByDest[0].CommittedISK != 100_000_000 {
		t.Errorf("first dest row is %d at %.0f, want Onnamon IV at 100M",
			budget.ByDest[0].DestStationID, budget.ByDest[0].CommittedISK)
	}
	if budget.ByDest[1].DestStationID != fwStaVillasenV || budget.ByDest[1].CommittedISK != 20_000_000 {
		t.Errorf("second dest row is %d at %.0f, want Villasen V at 20M",
			budget.ByDest[1].DestStationID, budget.ByDest[1].CommittedISK)
	}
	if budget.CommittedISK != 120_000_000 {
		t.Errorf("committed %.0f, want 120M -- the dest breakdown must sum to the total", budget.CommittedISK)
	}
	if budget.HeadroomISK != 1_380_000_000 {
		t.Errorf("headroom %.0f, want 1.38B against a 1.5B budget", budget.HeadroomISK)
	}
}

// TestFWCampaignsAreUserScoped: another user's campaign ID is not a way in.
//
// Both users get a campaign so the IDs are real and adjacent, which is the case
// a scoping bug actually survives -- guessing 1 when 1 exists.
func TestFWCampaignsAreUserScoped(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	idA, err := d.SaveFWCampaign(fwTestCampaign(fwUserA))
	if err != nil {
		t.Fatalf("save A: %v", err)
	}
	idB, err := d.SaveFWCampaign(fwTestCampaign(fwUserB))
	if err != nil {
		t.Fatalf("save B: %v", err)
	}
	if idA == idB {
		t.Fatalf("both campaigns got id %d", idA)
	}

	lot := &engine.FWLot{
		TypeID: 2488, State: engine.FWLotBought, Qty: 100, QtyRemaining: 100,
		UnitCostISK: 1_000_000, DestStationID: fwStaOnnamonIV,
	}
	if _, err := d.SaveFWLot(fwUserA, idA, lot); err != nil {
		t.Fatalf("save lot: %v", err)
	}

	if got, err := d.GetFWCampaign(fwUserB, idA); err != nil || got != nil {
		t.Errorf("user B read user A's campaign: %v, %v", got, err)
	}
	if lots, err := d.GetFWLots(fwUserB, idA); err != nil || len(lots) != 0 {
		t.Errorf("user B read %d of user A's lots: %v", len(lots), err)
	}
	if list, err := d.ListFWCampaigns(fwUserB); err != nil || len(list) != 1 || list[0].CampaignID != idB {
		t.Errorf("user B's list is %+v (err %v), want only campaign %d", list, err, idB)
	}

	// A cross-user write reports that it wrote nothing rather than appearing to
	// succeed. Silent no-ops are how a UI ends up showing an edit that is not there.
	steal := fwTestCampaign(fwUserB)
	steal.CampaignID = idA
	steal.BudgetISK = 99
	if _, err := d.SaveFWCampaign(steal); err == nil {
		t.Error("user B updated user A's campaign without an error")
	}
	if err := d.SetFWLotState(fwUserB, idA, lot.LotID, engine.FWLotSold); err == nil {
		t.Error("user B advanced user A's lot without an error")
	}
	after, err := d.GetFWCampaign(fwUserA, idA)
	if err != nil || after == nil {
		t.Fatalf("re-read A: %v", err)
	}
	if after.BudgetISK != 1_500_000_000 {
		t.Errorf("user A's budget is now %.0f", after.BudgetISK)
	}
	lots, err := d.GetFWLots(fwUserA, idA)
	if err != nil || len(lots) != 1 || lots[0].State != engine.FWLotBought {
		t.Errorf("user A's lot is now %+v (err %v)", lots, err)
	}
}

// TestFWPlanCacheIsRegenerableWhileLotsSurvive is the split the plan asks for,
// asserted from the destructive side: throwing the plan away must not touch the
// record of what was spent. The plan can be rebuilt from zkill and a market
// fetch; the lots cannot be rebuilt from anything.
func TestFWPlanCacheIsRegenerableWhileLotsSurvive(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	id, err := d.SaveFWCampaign(fwTestCampaign(fwUserA))
	if err != nil {
		t.Fatalf("save campaign: %v", err)
	}
	lot := &engine.FWLot{
		TypeID: 2488, State: engine.FWLotListed, Qty: 5000, QtyRemaining: 5000,
		UnitCostISK: 100, DestStationID: fwStaOnnamonIV,
		HolderOwnerKind: "character", HolderOwnerID: fwFrontlineID,
	}
	if _, err := d.SaveFWLot(fwUserA, id, lot); err != nil {
		t.Fatalf("save lot: %v", err)
	}

	if _, _, ok := d.GetFWPlan(fwUserA, id); ok {
		t.Error("an unplanned campaign reported a cached plan")
	}
	if err := d.SaveFWPlan(fwUserA, id, "2026-09-17T12:00:00Z", `{"rows":1}`); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	payload, generatedAt, ok := d.GetFWPlan(fwUserA, id)
	if !ok || payload != `{"rows":1}` || generatedAt != "2026-09-17T12:00:00Z" {
		t.Fatalf("plan round-tripped as %q / %q / %v", payload, generatedAt, ok)
	}

	// Regenerating replaces rather than duplicating.
	if err := d.SaveFWPlan(fwUserA, id, "2026-09-17T13:00:00Z", `{"rows":2}`); err != nil {
		t.Fatalf("regenerate plan: %v", err)
	}
	if payload, generatedAt, _ = d.GetFWPlan(fwUserA, id); payload != `{"rows":2}` || generatedAt != "2026-09-17T13:00:00Z" {
		t.Errorf("regenerated plan is %q / %q", payload, generatedAt)
	}

	if err := d.DeleteFWPlan(fwUserA, id); err != nil {
		t.Fatalf("delete plan: %v", err)
	}
	if _, _, ok := d.GetFWPlan(fwUserA, id); ok {
		t.Error("plan survived its own deletion")
	}
	lots, err := d.GetFWLots(fwUserA, id)
	if err != nil || len(lots) != 1 || lots[0].QtyRemaining != 5000 {
		t.Errorf("lots after dropping the plan: %+v (err %v)", lots, err)
	}
	if budget, err := d.GetFWBudget(fwUserA, id); err != nil || budget.CommittedISK != 500_000 {
		t.Errorf("committed after dropping the plan is %.0f (err %v), want 500k", budget.CommittedISK, err)
	}
}

// TestFWLotStateTransitionDoesNotMoveHeadroom is verification step 11 as a test:
// headroom drops at `bought` and then must not move again through the handoff.
//
// SetFWLotState cannot write a quantity, which is what makes that structural
// rather than a habit -- so this asserts both the ISK and that the quantity came
// through untouched.
func TestFWLotStateTransitionDoesNotMoveHeadroom(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	id, err := d.SaveFWCampaign(fwTestCampaign(fwUserA))
	if err != nil {
		t.Fatalf("save campaign: %v", err)
	}
	lot := &engine.FWLot{
		TypeID: 2488, State: engine.FWLotBought, Qty: 7000, QtyRemaining: 7000,
		UnitCostISK: 100, DestStationID: fwStaOnnamonIV,
		AcquiredByCharacterID: fwJitaAltID,
		HolderOwnerKind:       "character", HolderOwnerID: fwFrontlineID,
	}
	if _, err := d.SaveFWLot(fwUserA, id, lot); err != nil {
		t.Fatalf("save lot: %v", err)
	}

	first, err := d.GetFWBudget(fwUserA, id)
	if err != nil {
		t.Fatalf("budget: %v", err)
	}
	if first.CommittedISK != 700_000 {
		t.Fatalf("committed at `bought` is %.0f, want 700k", first.CommittedISK)
	}

	for _, state := range []string{engine.FWLotInTransit, engine.FWLotAtDest, engine.FWLotListed} {
		if err := d.SetFWLotState(fwUserA, id, lot.LotID, state); err != nil {
			t.Fatalf("advance to %s: %v", state, err)
		}
		budget, err := d.GetFWBudget(fwUserA, id)
		if err != nil {
			t.Fatalf("budget at %s: %v", state, err)
		}
		if budget.CommittedISK != first.CommittedISK || budget.HeadroomISK != first.HeadroomISK {
			t.Errorf("at %s committed %.0f headroom %.0f, want %.0f / %.0f -- moving a lot "+
				"along the handoff must not change headroom by one ISK",
				state, budget.CommittedISK, budget.HeadroomISK, first.CommittedISK, first.HeadroomISK)
		}
		lots, err := d.GetFWLots(fwUserA, id)
		if err != nil || len(lots) != 1 || lots[0].QtyRemaining != 7000 || lots[0].State != state {
			t.Fatalf("lot at %s is %+v (err %v)", state, lots, err)
		}
	}

	// Selling is what releases it.
	if err := d.SetFWLotState(fwUserA, id, lot.LotID, engine.FWLotSold); err != nil {
		t.Fatalf("advance to sold: %v", err)
	}
	sold, err := d.GetFWBudget(fwUserA, id)
	if err != nil {
		t.Fatalf("budget at sold: %v", err)
	}
	if sold.CommittedISK != 0 || sold.HeadroomISK != 1_500_000_000 {
		t.Errorf("at sold committed %.0f headroom %.0f, want 0 / the full budget",
			sold.CommittedISK, sold.HeadroomISK)
	}
}

// TestFWLotRejectsUnknownState: an unrecognised state does not consume budget, so
// storing one hands back headroom for ISK that is already gone. That is the
// failure that funds a second shipment the campaign cannot afford, so the store
// refuses the write rather than the budget silently absorbing it.
func TestFWLotRejectsUnknownState(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	id, err := d.SaveFWCampaign(fwTestCampaign(fwUserA))
	if err != nil {
		t.Fatalf("save campaign: %v", err)
	}

	bad := &engine.FWLot{TypeID: 2488, State: "shipped", Qty: 10, QtyRemaining: 10, UnitCostISK: 100}
	if _, err := d.SaveFWLot(fwUserA, id, bad); err == nil {
		t.Error("stored a lot in state \"shipped\"")
	}
	if err := d.SetFWLotState(fwUserA, id, 1, "shipped"); err == nil {
		t.Error("advanced a lot to state \"shipped\"")
	}

	// An empty state is the planned row a UI posts before anything is bought, so
	// it defaults rather than failing.
	blank := &engine.FWLot{TypeID: 2488, Qty: 10, QtyRemaining: 10, UnitCostISK: 100}
	if _, err := d.SaveFWLot(fwUserA, id, blank); err != nil {
		t.Fatalf("save lot with no state: %v", err)
	}
	if blank.State != engine.FWLotPlanned {
		t.Errorf("a stateless lot became %q, want planned", blank.State)
	}

	// More remaining than was ever bought inflates committed capital, which is the
	// direction that matters: it would report ISK as spent that never was.
	impossible := &engine.FWLot{TypeID: 2488, State: engine.FWLotBought, Qty: 10, QtyRemaining: 11, UnitCostISK: 100}
	if _, err := d.SaveFWLot(fwUserA, id, impossible); err == nil {
		t.Error("stored 11 remaining of 10 bought")
	}

	// A consuming lot with nothing remaining is a contradiction -- bought stock
	// exists until it sells -- so it reads as all of it rather than as free.
	unset := &engine.FWLot{TypeID: 2488, State: engine.FWLotBought, Qty: 500, UnitCostISK: 1_000}
	if _, err := d.SaveFWLot(fwUserA, id, unset); err != nil {
		t.Fatalf("save lot with no remaining: %v", err)
	}
	if unset.QtyRemaining != 500 {
		t.Errorf("remaining defaulted to %d, want 500", unset.QtyRemaining)
	}
}

// TestDeleteFWCampaignTakesItsLots: the cascade is written out in the store, so
// it is asserted here rather than assumed from a foreign key that does not exist.
func TestDeleteFWCampaignTakesItsLots(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	keep, err := d.SaveFWCampaign(fwTestCampaign(fwUserA))
	if err != nil {
		t.Fatalf("save keeper: %v", err)
	}
	drop, err := d.SaveFWCampaign(fwTestCampaign(fwUserA))
	if err != nil {
		t.Fatalf("save doomed: %v", err)
	}
	for _, id := range []int64{keep, drop} {
		lot := &engine.FWLot{
			TypeID: 2488, State: engine.FWLotBought, Qty: 100, QtyRemaining: 100,
			UnitCostISK: 100, DestStationID: fwStaOnnamonIV,
		}
		if _, err := d.SaveFWLot(fwUserA, id, lot); err != nil {
			t.Fatalf("save lot in %d: %v", id, err)
		}
		if err := d.SaveFWPlan(fwUserA, id, "", "{}"); err != nil {
			t.Fatalf("save plan for %d: %v", id, err)
		}
	}

	if err := d.DeleteFWCampaign(fwUserA, drop); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := d.GetFWCampaign(fwUserA, drop); got != nil {
		t.Error("deleted campaign still reads back")
	}
	if lots, _ := d.GetFWLots(fwUserA, drop); len(lots) != 0 {
		t.Errorf("%d lots outlived their campaign", len(lots))
	}
	if _, _, ok := d.GetFWPlan(fwUserA, drop); ok {
		t.Error("plan cache outlived its campaign")
	}
	if lots, _ := d.GetFWLots(fwUserA, keep); len(lots) != 1 {
		t.Errorf("the other campaign has %d lots, want 1", len(lots))
	}
	if _, _, ok := d.GetFWPlan(fwUserA, keep); !ok {
		t.Error("the other campaign lost its plan")
	}
}

// TestFittingDemandScopesDoNotCollide is the scope-key assertion the plan names.
//
// Three profiles for what a v7-era key would have called one thing: a region, a
// militia, and the same militia over a different window. Storing any of them
// must not disturb the others -- the 7-day militia profile is the one FW Supply
// reads, and it is the one a region refresh would have overwritten.
func TestFittingDemandScopesDoNotCollide(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	blackRise := RegionDemandScope(10000069)
	caldari7d := MilitiaDemandScope(500001, 7*86400)
	caldari24h := MilitiaDemandScope(500001, 86400)

	if err := d.SaveFittingDemandProfile(blackRise, []FittingDemandItem{
		{TypeID: 2488, TypeName: "Inferno Light Missile", Category: "ammo", EstDailyDemand: 1000},
	}); err != nil {
		t.Fatalf("save region profile: %v", err)
	}
	if err := d.SaveFittingDemandProfile(caldari7d, []FittingDemandItem{
		{TypeID: 2488, TypeName: "Inferno Light Missile", Category: "ammo", EstDailyDemand: 4553},
		{TypeID: 266, TypeName: "Scourge Light Missile", Category: "ammo", EstDailyDemand: 7033},
	}); err != nil {
		t.Fatalf("save militia profile: %v", err)
	}
	if err := d.SaveFittingDemandProfile(caldari24h, []FittingDemandItem{
		{TypeID: 2488, TypeName: "Inferno Light Missile", Category: "ammo", EstDailyDemand: 12},
	}); err != nil {
		t.Fatalf("save 24h militia profile: %v", err)
	}

	region, err := d.GetFittingDemandProfile(blackRise)
	if err != nil || len(region) != 1 || region[0].EstDailyDemand != 1000 {
		t.Fatalf("region profile is %+v (err %v)", region, err)
	}
	if region[0].ScopeKind != DemandScopeRegion || region[0].ScopeID != 10000069 {
		t.Errorf("region row carries scope %s/%d", region[0].ScopeKind, region[0].ScopeID)
	}

	militia, err := d.GetFittingDemandProfile(caldari7d)
	if err != nil || len(militia) != 2 {
		t.Fatalf("militia profile is %+v (err %v)", militia, err)
	}
	// Heaviest destruction first: Scourge outpaces Inferno, and the ordering is
	// what the gap table ranks on before cover is applied.
	if militia[0].TypeID != 266 || militia[1].TypeID != 2488 {
		t.Errorf("militia rows came back as %d then %d, want Scourge then Inferno",
			militia[0].TypeID, militia[1].TypeID)
	}
	if militia[1].EstDailyDemand != 4553 {
		t.Errorf("Inferno's militia rate is %.0f, want 4553 -- the region's 1000 leaked in",
			militia[1].EstDailyDemand)
	}
	if militia[0].WindowSeconds != 7*86400 {
		t.Errorf("militia window round-tripped as %d", militia[0].WindowSeconds)
	}

	window, err := d.GetFittingDemandProfile(caldari24h)
	if err != nil || len(window) != 1 || window[0].EstDailyDemand != 12 {
		t.Errorf("24h militia profile is %+v (err %v) -- the window is part of the key, "+
			"so the 7-day profile must not have overwritten it", window, err)
	}

	// Refreshing one scope replaces only that scope's rows.
	if err := d.SaveFittingDemandProfile(caldari7d, []FittingDemandItem{
		{TypeID: 2488, TypeName: "Inferno Light Missile", Category: "ammo", EstDailyDemand: 5000},
	}); err != nil {
		t.Fatalf("refresh militia profile: %v", err)
	}
	if got, _ := d.GetFittingDemandProfile(caldari7d); len(got) != 1 || got[0].EstDailyDemand != 5000 {
		t.Errorf("refreshed militia profile is %+v; a type that stopped being destroyed "+
			"should disappear rather than linger at last week's rate", got)
	}
	if got, _ := d.GetFittingDemandProfile(blackRise); len(got) != 1 || got[0].EstDailyDemand != 1000 {
		t.Errorf("region profile after a militia refresh is %+v", got)
	}
	if got, _ := d.GetFittingDemandProfile(caldari24h); len(got) != 1 {
		t.Errorf("24h militia profile after a 7-day refresh is %+v", got)
	}

	if !d.IsFittingProfileFresh(caldari7d, time.Hour) {
		t.Error("a profile written a moment ago is not fresh")
	}
	if d.IsFittingProfileFresh(MilitiaDemandScope(500004, 7*86400), time.Hour) {
		t.Error("a scope that was never written reported fresh")
	}
	if err := d.SaveFittingDemandProfile(DemandScope{}, nil); err == nil {
		t.Error("stored a profile under an empty scope; nothing would ever read it back")
	}
}

// TestFWCampaignShipmentConfigResolvesTheHull: a campaign stores a profile name,
// and the shipping list needs m3. The pair is what stops the two drifting apart
// -- a deep space transport is 60,000 m3, which is 24 packaged frigates.
func TestFWCampaignShipmentConfigResolvesTheHull(t *testing.T) {
	c := fwTestCampaign(fwUserA)
	cfg := c.ShipmentConfig(800_000)
	if cfg.CargoCapacityM3 != 60000 || cfg.MaxTrips != 2 || cfg.HeadroomISK != 800_000 {
		t.Errorf("shipment config is %+v, want 60,000 m3 over 2 trips at 800k", cfg)
	}

	// No profile means the cargo bound is off, not that the hold is unlimited: the
	// budget is then the only limit, which is the honest reading of an unstated hull.
	c.ShipProfile = ""
	if got := c.ShipmentConfig(1).CargoCapacityM3; got != 0 {
		t.Errorf("an unstated ship profile resolved to %.0f m3", got)
	}

	supply := c.SupplyConfig()
	if supply.DestStationID != fwStaOnnamonIV || supply.TargetCoverDays != 7 || supply.StepOverDaysCover != 0.5 {
		t.Errorf("supply config is %+v", supply)
	}
	if supply.SalesTaxPercent != 0 || supply.BrokerFeePercent != 0 {
		t.Errorf("supply config carries fee rates %.2f/%.2f; those follow the listing "+
			"character's skills and standings, not the campaign",
			supply.SalesTaxPercent, supply.BrokerFeePercent)
	}
}

// TestSetFWLotStates_AllOrNothing is the reason the bulk endpoint exists rather
// than the client looping the single-lot one.
//
// The budget is derived from the lots. A batch that applied to two of three rows
// would leave headroom describing a position the campaign was never in, and the
// screen showing a mixture of before and after with no way to tell which. So a
// stale lot id fails the whole batch and nothing moves -- asserted here by
// checking the two valid lots afterwards, because "it returned an error" and "it
// changed nothing" are different claims and only the second one is useful.
func TestSetFWLotStates_AllOrNothing(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	id, err := d.SaveFWCampaign(fwTestCampaign(fwUserA))
	if err != nil {
		t.Fatalf("save campaign: %v", err)
	}
	var ids []int64
	for _, typeID := range []int32{2488, 2456, 12608} {
		lot := &engine.FWLot{
			TypeID: typeID, State: engine.FWLotBought, Qty: 1000, QtyRemaining: 1000,
			UnitCostISK: 100, DestStationID: fwStaOnnamonIV,
		}
		lotID, err := d.SaveFWLot(fwUserA, id, lot)
		if err != nil {
			t.Fatalf("save lot %d: %v", typeID, err)
		}
		ids = append(ids, lotID)
	}

	// The happy path: one call moves all three.
	if err := d.SetFWLotStates(fwUserA, id, ids, engine.FWLotInTransit); err != nil {
		t.Fatalf("bulk advance: %v", err)
	}
	lots, err := d.GetFWLots(fwUserA, id)
	if err != nil {
		t.Fatalf("lots: %v", err)
	}
	for _, lot := range lots {
		if lot.State != engine.FWLotInTransit {
			t.Errorf("lot %d is %q, want in_transit", lot.LotID, lot.State)
		}
	}

	// Now a batch carrying one id that is not a lot of this campaign. It must
	// fail, and the two real lots must not have moved.
	stale := append([]int64{}, ids[0], ids[1], 999999)
	if err := d.SetFWLotStates(fwUserA, id, stale, engine.FWLotAtDest); err == nil {
		t.Fatal("a batch with an unknown lot id succeeded")
	}
	lots, err = d.GetFWLots(fwUserA, id)
	if err != nil {
		t.Fatalf("lots after failed batch: %v", err)
	}
	for _, lot := range lots {
		if lot.State != engine.FWLotInTransit {
			t.Errorf("lot %d is %q after a failed batch, want in_transit unchanged -- "+
				"the transaction leaked and the budget now describes a position that never existed",
				lot.LotID, lot.State)
		}
	}
}

// TestSetFWLotStates_RejectsUnknownStateAndOtherUsers: the two ways a bulk move
// could quietly corrupt the budget.
//
// An unrecognised state consumes no budget, so storing one hands back headroom
// for ISK that is already gone -- the same failure TestFWLotRejectsUnknownState
// pins for the single-lot path, which the bulk path must not route around. And a
// lot belonging to another user must be invisible here for the same reason it is
// invisible everywhere else, not merely skipped.
func TestSetFWLotStates_RejectsUnknownStateAndOtherUsers(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	idA, err := d.SaveFWCampaign(fwTestCampaign(fwUserA))
	if err != nil {
		t.Fatalf("save campaign A: %v", err)
	}
	lotA, err := d.SaveFWLot(fwUserA, idA, &engine.FWLot{
		TypeID: 2488, State: engine.FWLotBought, Qty: 10, QtyRemaining: 10, UnitCostISK: 100,
	})
	if err != nil {
		t.Fatalf("save lot A: %v", err)
	}

	if err := d.SetFWLotStates(fwUserA, idA, []int64{lotA}, "shipped"); err == nil {
		t.Error("bulk-advanced a lot to the unknown state \"shipped\"")
	}

	// Another user's request naming A's lot finds nothing and fails.
	idB, err := d.SaveFWCampaign(fwTestCampaign(fwUserB))
	if err != nil {
		t.Fatalf("save campaign B: %v", err)
	}
	if err := d.SetFWLotStates(fwUserB, idB, []int64{lotA}, engine.FWLotSold); err == nil {
		t.Error("user B moved user A's lot")
	}
	lots, err := d.GetFWLots(fwUserA, idA)
	if err != nil || len(lots) != 1 || lots[0].State != engine.FWLotBought {
		t.Fatalf("A's lot is %+v (err %v), want still bought", lots, err)
	}
}

// TestDeleteFWLots_ReleasesHeadroomAndToleratesGoneRows.
//
// Deleting is the one bulk action that is not reversible, so what it releases is
// worth asserting: the budget is recomputed from the rows that remain, not
// adjusted by the caller.
//
// The tolerance for an already-deleted id is the deliberate asymmetry against
// SetFWLotStates. A missing row there means the requested end state has not been
// reached; here it means it already has, and failing the batch would leave the
// trader unable to clear a selection that two tabs both acted on.
func TestDeleteFWLots_ReleasesHeadroomAndToleratesGoneRows(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	id, err := d.SaveFWCampaign(fwTestCampaign(fwUserA))
	if err != nil {
		t.Fatalf("save campaign: %v", err)
	}
	var ids []int64
	for _, typeID := range []int32{2488, 2456} {
		lotID, err := d.SaveFWLot(fwUserA, id, &engine.FWLot{
			TypeID: typeID, State: engine.FWLotBought, Qty: 1000, QtyRemaining: 1000,
			UnitCostISK: 100, DestStationID: fwStaOnnamonIV,
		})
		if err != nil {
			t.Fatalf("save lot %d: %v", typeID, err)
		}
		ids = append(ids, lotID)
	}

	before, err := d.GetFWBudget(fwUserA, id)
	if err != nil {
		t.Fatalf("budget: %v", err)
	}
	if before.CommittedISK != 200_000 {
		t.Fatalf("committed is %.0f, want 200k", before.CommittedISK)
	}

	// Delete both, with one id repeated: a selection acted on twice must not be
	// an error, because the end state it asks for is the one that holds.
	if err := d.DeleteFWLots(fwUserA, id, append(ids, ids[0])); err != nil {
		t.Fatalf("bulk delete: %v", err)
	}
	after, err := d.GetFWBudget(fwUserA, id)
	if err != nil {
		t.Fatalf("budget after delete: %v", err)
	}
	if after.CommittedISK != 0 {
		t.Errorf("committed after deleting every lot is %.0f, want 0", after.CommittedISK)
	}
	lots, err := d.GetFWLots(fwUserA, id)
	if err != nil || len(lots) != 0 {
		t.Fatalf("lots after delete: %+v (err %v)", lots, err)
	}
}

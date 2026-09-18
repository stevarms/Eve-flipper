package engine

import (
	"strings"
	"testing"

	"eve-flipper/internal/esi"
)

// The minimum-lot floor is the user's rule, and the 10x cap is the user's
// correction to it: destruction is a proxy for demand, not demand itself, so a
// slow item deserves more than the two units that died -- but not fifty, because
// a floor only ever bites on the slowest movers and an absolute one would park
// months of cover in exactly the stock the warzone consumes slowest.
//
// 2 Miner I becoming 20 rather than 50 is the whole of that compromise as a
// number, and it is the case that prompted the rule.
func TestApplyFWMinLot_MinerIBecomesTwentyNotFifty(t *testing.T) {
	item := FWSupplyItem{
		TypeID: 3831, TypeName: "Miner I", Category: "module",
		VolumeM3: 5, DailyDestroyed: 0.3, KillsWithItem: 40, JitaBestSell: 9_300,
	}
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, coverConfig()), 3831)

	if row.CoverSizedQty != 2 {
		t.Fatalf("cover sized %d, want 2 -- 7 days at 0.3 destroyed a day", row.CoverSizedQty)
	}
	if row.SuggestedQty != 20 {
		t.Errorf("suggested qty = %d, want 20 (10x the 2 cover asked for), not 50 (the raw <100k floor)",
			row.SuggestedQty)
	}
	// Both edges of the bet in words: a stretched lot is a wager that destruction
	// understates demand, so the row has to read as one rather than as a measured
	// quantity.
	for _, want := range []string{"sized 2", "raised to 20", "50-item floor", "100k", "capped at 10x demand", "days of cover"} {
		if !strings.Contains(row.QtyReason, want) {
			t.Errorf("qty reason %q does not say %q", row.QtyReason, want)
		}
	}
}

// The cap only binds when the line is very slow. Where 10x clears the band, the
// band is what sizes the lot.
func TestApplyFWMinLot_BandWinsWhenTenTimesClearsIt(t *testing.T) {
	item := FWSupplyItem{
		TypeID: 200, TypeName: "Cheap Thing", Category: "ammo",
		VolumeM3: 0.01, DailyDestroyed: 2, KillsWithItem: 40, JitaBestSell: 90_000,
	}
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, coverConfig()), 200)
	if row.CoverSizedQty != 14 || row.SuggestedQty != 50 {
		t.Errorf("sized %d raised to %d, want 14 -> 50: 10x14 clears the 50-item band, so the band is the floor",
			row.CoverSizedQty, row.SuggestedQty)
	}
	if strings.Contains(row.QtyReason, "capped") {
		t.Errorf("qty reason claims the cap bound when it did not: %q", row.QtyReason)
	}
}

// Above the floor nothing happens at all, and the row says nothing -- ammo
// already exceeds every band, which is most of the buy list.
func TestApplyFWMinLot_AboveTheFloorIsUntouched(t *testing.T) {
	item := FWSupplyItem{
		TypeID: 201, TypeName: "Bulk Ammo", Category: "ammo",
		VolumeM3: 0.0025, DailyDestroyed: 100, KillsWithItem: 300, JitaBestSell: 100,
	}
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, coverConfig()), 201)
	if row.SuggestedQty != 700 || row.CoverSizedQty != 700 {
		t.Errorf("sized %d raised to %d, want 700 both ways", row.CoverSizedQty, row.SuggestedQty)
	}
	if row.QtyReason != "" {
		t.Errorf("qty reason = %q, want silence when the floor did not bite", row.QtyReason)
	}
}

// The two lines the floor may not cross.
//
// Zero stays zero, because the cover model declining to want an item is not
// something a price band may overrule -- and the shipment gate reads
// SuggestedQty <= 0, so a floor that could raise a zero would put stock in the
// buy list that nothing asked for. A covered row is never stretched for the same
// reason from the other direction.
func TestApplyFWMinLot_ZeroStaysZeroAndCoveredIsNeverStretched(t *testing.T) {
	// 8 stocked against 1 a day is 8 days of cover: thin against a 7-day target,
	// so the sizing block runs, and the deficit is negative.
	thin := FWSupplyItem{
		TypeID: 202, TypeName: "Slightly Over", Category: "module",
		VolumeM3: 5, DailyDestroyed: 1, KillsWithItem: 40, JitaBestSell: 50_000,
		LocalOrders: []esi.MarketOrder{fwSell(202, staVillasenV, 80_000, 8)},
	}
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{thin}, coverConfig()), 202)
	if row.Verdict != VerdictThin {
		t.Fatalf("verdict %q, want %q -- this case is about the thin sizing branch", row.Verdict, VerdictThin)
	}
	if row.SuggestedQty != 0 || row.QtyReason != "" {
		t.Errorf("a thin row already past target got qty %d (%q), want 0 and silence",
			row.SuggestedQty, row.QtyReason)
	}
	// And it must not reach the shipment, which is what a raised zero would have
	// broken.
	shipment := BuildFWShipment([]FWSupplyRow{row}, FWShipmentConfig{HeadroomISK: 1_000_000_000})
	if len(shipment.Lines) != 0 {
		t.Errorf("shipment has %d line(s), want none from a zero-quantity row", len(shipment.Lines))
	}

	covered := FWSupplyItem{
		TypeID: 203, TypeName: "Deep Stock", Category: "module",
		VolumeM3: 5, DailyDestroyed: 1, KillsWithItem: 40, JitaBestSell: 50_000,
		LocalOrders: []esi.MarketOrder{fwSell(203, staVillasenV, 80_000, 500)},
	}
	cov := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{covered}, coverConfig()), 203)
	if cov.Verdict != VerdictCovered {
		t.Fatalf("verdict %q, want %q", cov.Verdict, VerdictCovered)
	}
	if cov.SuggestedQty != 0 || cov.QtyReason != "" {
		t.Errorf("a covered row got qty %d (%q), want 0 and silence", cov.SuggestedQty, cov.QtyReason)
	}
}

// The band boundaries are the user's numbers and nothing else in the codebase
// recovers them, so they are pinned here. Strict <, so 100,000 exactly is the
// 30-item band rather than the 50-item one.
func TestFWMinLotFloor_BandBoundaries(t *testing.T) {
	cases := []struct {
		price float64
		floor int64
	}{
		{1, 50},
		{99_999, 50},
		{100_000, 30},
		{999_999, 30},
		{1_000_000, 15},
		{1_499_999, 15},
		{1_500_000, 5},
		{4_999_999, 5},
		{5_000_000, 0},
		{50_000_000, 0},
		{0, 0},
	}
	for _, c := range cases {
		if got := fwMinLotFloor(c.price); got != c.floor {
			t.Errorf("floor at %.0f ISK = %d, want %d", c.price, got, c.floor)
		}
	}
}

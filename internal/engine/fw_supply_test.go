package engine

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"eve-flipper/internal/esi"
)

// Real type IDs from the local SDE, not invented ones. Volumes are the SDE's.
//
// Worth noting while these were being looked up: zkillboard's static commonAmmo
// list has the wrong IDs for most of its entries -- 248 is Microwave M, not
// Void M; 233 is Iridium Charge L, not Antimatter Charge L; 2203 is Acolyte I,
// a drone, not EMP L. That list is the fallback path the plan is replacing, so
// it is out of scope here, but it is a real defect.
const (
	typeTristan   int32 = 593   // 2,500 m3 packaged
	typeScourgeLM int32 = 210   // 0.015 m3
	typeInfernoLM int32 = 211   // 0.015 m3
	typeNovaLM    int32 = 213   // 0.015 m3
	typeVoidM     int32 = 12789 // 0.0125 m3

	tristanVolumeM3 = 2500.0
	missileVolumeM3 = 0.015
)

func fwSell(typeID int32, station int64, price float64, qty int32) esi.MarketOrder {
	return esi.MarketOrder{TypeID: typeID, LocationID: station, Price: price, VolumeRemain: qty, VolumeTotal: qty, Duration: 90}
}

func fwSeed(typeID int32, station int64, price float64, qty int32) esi.MarketOrder {
	return esi.MarketOrder{TypeID: typeID, LocationID: station, Price: price, VolumeRemain: qty, VolumeTotal: qty, Duration: 365}
}

// coverConfig is a campaign with freight switched off, so the verdict under test
// is cover rather than hull freight economics -- which has its own test, because
// on a 2,500 m3 hull freight dominates everything else.
func coverConfig() FWSupplyConfig {
	return FWSupplyConfig{
		DestStationID:    staVillasenV,
		TargetCoverDays:  7,
		CoveredMultiple:  2,
		MinMarginPct:     10,
		SalesTaxPercent:  5,
		BrokerFeePercent: 3,
	}
}

func rowFor(t *testing.T, rows []FWSupplyRow, typeID int32) FWSupplyRow {
	t.Helper()
	for _, row := range rows {
		if row.TypeID == typeID {
			return row
		}
	}
	t.Fatalf("type %d is missing from the plan -- every candidate must produce a row, even a covered one", typeID)
	return FWSupplyRow{}
}

// TestBuildFWSupplyPlan_TristanBothWays is the rule in the user's own words, as a
// number: 500 hulls against 3 a day is 166 days and nothing to add; 2 hulls
// against the same 3 a day is the gap the tool exists to find.
//
// The two cases differ only in stocked quantity, so nothing but cover can be
// producing the difference.
func TestBuildFWSupplyPlan_TristanBothWays(t *testing.T) {
	const dailyDestroyed = 3.0

	stocked := func(qty int32) FWSupplyItem {
		item := FWSupplyItem{
			TypeID:         typeTristan,
			TypeName:       "Tristan",
			Category:       "ship",
			VolumeM3:       tristanVolumeM3,
			DailyDestroyed: dailyDestroyed,
			KillsWithItem:  40,
			JitaBestSell:   500_000,
		}
		if qty > 0 {
			item.LocalOrders = []esi.MarketOrder{fwSell(typeTristan, staVillasenV, 700_000, qty)}
		}
		return item
	}

	deep := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{stocked(500)}, coverConfig()), typeTristan)
	if deep.Verdict != VerdictCovered {
		t.Errorf("500 hulls against %.0f a day = %.0f days: verdict %q, want %q (%s)",
			dailyDestroyed, deep.DaysOfCover, deep.Verdict, VerdictCovered, deep.VerdictReason)
	}
	if want := 166.0; math.Abs(float64(deep.DaysOfCover)-want) > 1 {
		t.Errorf("days of cover = %.1f, want about %.0f", deep.DaysOfCover, want)
	}
	if deep.SuggestedQty != 0 {
		t.Errorf("suggested qty = %d, want 0 -- a covered item ships nothing", deep.SuggestedQty)
	}

	thin := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{stocked(2)}, coverConfig()), typeTristan)
	if thin.Verdict != VerdictGap {
		t.Errorf("2 hulls against %.0f a day = %.2f days: verdict %q, want %q (%s)",
			dailyDestroyed, thin.DaysOfCover, thin.Verdict, VerdictGap, thin.VerdictReason)
	}
	// (7 - 0.667) days short at 3 a day. The cover figure stays visible after the
	// minimum-lot floor raises the shipped quantity, because it is the number the
	// cover model is accountable for.
	if want := int64(19); thin.CoverSizedQty != want {
		t.Errorf("cover-sized qty = %d, want %d -- enough to reach the 7-day target and no more",
			thin.CoverSizedQty, want)
	}
	// A 500k hull is in the <1m band, whose floor is 30, and 10x19 is well past
	// it -- so the floor is what sizes this lot, and m3 and cost follow the lot
	// that will actually be bought.
	if want := int64(30); thin.SuggestedQty != want {
		t.Errorf("suggested qty = %d, want %d -- the <1m band floor", thin.SuggestedQty, want)
	}
	if want := 30 * tristanVolumeM3; thin.CargoM3 != want {
		t.Errorf("cargo = %.0f m3, want %.0f -- a frigate run is not a rounding error", thin.CargoM3, want)
	}
	if want := 30 * 500_000.0; thin.CostISK != want {
		t.Errorf("cost = %.0f, want %.0f (at Jita, which is what the budget counts)", thin.CostISK, want)
	}
}

// TestBuildFWSupplyPlan_OneDamageTypeStockedThreeAbsent is the measured case from
// the plan: Scourge Light Missile 86 days deep at the same station where Inferno,
// Nova and Mjolnir are at zero while being destroyed by the thousand.
//
// This is what a static "T1 frigs and fittings" list cannot express -- all four
// are the same item in the same fitting guide, and only the destruction-versus-
// stock ratio tells them apart.
func TestBuildFWSupplyPlan_OneDamageTypeStockedThreeAbsent(t *testing.T) {
	items := []FWSupplyItem{
		{
			TypeID: typeScourgeLM, TypeName: "Scourge Light Missile", Category: "ammo",
			VolumeM3: missileVolumeM3, DailyDestroyed: 7033, KillsWithItem: 310, JitaBestSell: 100,
			LocalOrders: []esi.MarketOrder{fwSell(typeScourgeLM, staVillasenV, 150, 605_154)},
		},
		{
			TypeID: typeInfernoLM, TypeName: "Inferno Light Missile", Category: "ammo",
			VolumeM3: missileVolumeM3, DailyDestroyed: 4553, KillsWithItem: 240, JitaBestSell: 100,
		},
		{
			TypeID: typeNovaLM, TypeName: "Nova Light Missile", Category: "ammo",
			VolumeM3: missileVolumeM3, DailyDestroyed: 1331, KillsWithItem: 95, JitaBestSell: 100,
		},
	}
	rows := BuildFWSupplyPlan(items, coverConfig())

	scourge := rowFor(t, rows, typeScourgeLM)
	if scourge.Verdict != VerdictCovered {
		t.Errorf("Scourge at %.0f days: verdict %q, want %q", scourge.DaysOfCover, scourge.Verdict, VerdictCovered)
	}
	if want := 86.0; math.Abs(float64(scourge.DaysOfCover)-want) > 1 {
		t.Errorf("Scourge cover = %.1f days, want about %.0f", scourge.DaysOfCover, want)
	}

	inferno := rowFor(t, rows, typeInfernoLM)
	if inferno.Verdict != VerdictGap || inferno.DaysOfCover != 0 {
		t.Errorf("Inferno: verdict %q at %.2f days, want %q at 0", inferno.Verdict, inferno.DaysOfCover, VerdictGap)
	}
	if want := int64(7 * 4553); inferno.SuggestedQty != want {
		t.Errorf("Inferno qty = %d, want %d (7 days at %.0f destroyed a day)", inferno.SuggestedQty, want, 4553.0)
	}

	// The gap with the higher destruction rate leads, because that is the order a
	// budget should be spent in.
	if rows[0].TypeID != typeInfernoLM {
		t.Errorf("first row is type %d, want Inferno (%d) -- the biggest hole comes first", rows[0].TypeID, typeInfernoLM)
	}
	if rows[len(rows)-1].TypeID != typeScourgeLM {
		t.Errorf("last row is type %d, want the covered Scourge (%d)", rows[len(rows)-1].TypeID, typeScourgeLM)
	}
}

// TestBuildFWSupplyPlan_ThinEvidenceIsNotShippable: an item seen on two losses
// has a destruction rate, but not one worth spending on.
//
// It stays in the table with its kill count rather than disappearing, because
// "seen twice" and "not seen" are different answers and the row is where the
// user can tell them apart.
func TestBuildFWSupplyPlan_ThinEvidenceIsNotShippable(t *testing.T) {
	item := func(kills int) FWSupplyItem {
		return FWSupplyItem{
			TypeID: typeVoidM, TypeName: "Void M", Category: "ammo", VolumeM3: 0.0125,
			DailyDestroyed: 900, KillsWithItem: kills, JitaBestSell: 250,
		}
	}

	thin := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item(MinKillsWithItem - 1)}, coverConfig()), typeVoidM)
	if thin.Shippable {
		t.Errorf("%d killmails is under the %d-kill gate and must not be shippable", MinKillsWithItem-1, MinKillsWithItem)
	}
	if thin.KillsWithItem != MinKillsWithItem-1 {
		t.Errorf("kill count = %d, want %d kept on the row so thin evidence reads as thin",
			thin.KillsWithItem, MinKillsWithItem-1)
	}
	if thin.Verdict != VerdictGap {
		t.Errorf("verdict = %q, want %q -- shippability is a flag, not a verdict", thin.Verdict, VerdictGap)
	}

	solid := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item(MinKillsWithItem)}, coverConfig()), typeVoidM)
	if !solid.Shippable {
		t.Errorf("%d killmails meets the gate and must be shippable", MinKillsWithItem)
	}
}

// TestBuildFWSupplyPlan_SeedsAreNotStock is finding 6 on the cover side. A
// Caldari Navy station listing a million rounds on a 365-day order is not stock
// the militia is buying -- treating it as cover would hide a total gap behind a
// number that never moves.
func TestBuildFWSupplyPlan_SeedsAreNotStock(t *testing.T) {
	item := FWSupplyItem{
		TypeID: typeInfernoLM, TypeName: "Inferno Light Missile", Category: "ammo",
		VolumeM3: missileVolumeM3, DailyDestroyed: 4553, KillsWithItem: 240, JitaBestSell: 100,
		LocalOrders: []esi.MarketOrder{fwSeed(typeInfernoLM, staVillasenV, 500, 1_000_000)},
	}
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, coverConfig()), typeInfernoLM)

	if row.StockedQty != 0 {
		t.Errorf("stocked = %d, want 0 -- a million seeded rounds are not stock", row.StockedQty)
	}
	if row.Verdict != VerdictGap {
		t.Errorf("verdict = %q, want %q -- unfiltered this reads as 219 days of cover", row.Verdict, VerdictGap)
	}
	// With the seed gone the book is empty, so the reference stands unopposed.
	if row.PriceRule != PriceRuleReference {
		t.Errorf("price rule = %q, want %q -- a seed is not competition either", row.PriceRule, PriceRuleReference)
	}
	if row.CompetingUnitsBelow != 0 {
		t.Errorf("competing units = %d, want 0", row.CompetingUnitsBelow)
	}
}

// TestBuildFWSupplyPlan_SeedUnderOurCostIsAWall covers the one place a seed still
// counts, which is a deliberate addition to the plan.
//
// Seeds are excluded from depth, calibration and undercut targets because they
// never move. But one resting under our landed cost is a price we cannot sell
// through, and ignoring it entirely would recommend shipping an item an NPC
// already sells cheaper.
func TestBuildFWSupplyPlan_SeedUnderOurCostIsAWall(t *testing.T) {
	cfg := coverConfig()
	item := FWSupplyItem{
		TypeID: typeInfernoLM, TypeName: "Inferno Light Missile", Category: "ammo",
		VolumeM3: missileVolumeM3, DailyDestroyed: 4553, KillsWithItem: 240, JitaBestSell: 100,
		// Landed 100, floor ~119.6. The seed sits under it.
		LocalOrders: []esi.MarketOrder{fwSeed(typeInfernoLM, staVillasenV, 100, 1_000_000)},
	}
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, cfg), typeInfernoLM)

	if row.Verdict != VerdictUnpriceable {
		t.Errorf("verdict = %q, want %q -- an NPC undersells our landed cost", row.Verdict, VerdictUnpriceable)
	}
	if !strings.Contains(row.VerdictReason, "NPC") {
		t.Errorf("reason = %q, want it to name the NPC so the row explains itself", row.VerdictReason)
	}
	if row.SuggestedQty != 0 {
		t.Errorf("suggested qty = %d, want 0", row.SuggestedQty)
	}
}

// TestBuildFWSupplyPlan_HullFreightBeatsTheCeiling is the plan's named case: a
// 2,500 m3 hull whose freight exceeds what a 1.30x hull markup can carry.
//
// The answer is unpriceable, not a loss-making suggestion and not a quiet lift of
// the ceiling to make the numbers work.
func TestBuildFWSupplyPlan_HullFreightBeatsTheCeiling(t *testing.T) {
	cfg := coverConfig()
	cfg.FreightISKPerM3 = 800

	item := FWSupplyItem{
		TypeID: typeTristan, TypeName: "Tristan", Category: "ship", VolumeM3: tristanVolumeM3,
		DailyDestroyed: 3, KillsWithItem: 40, JitaBestSell: 500_000,
	}
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, cfg), typeTristan)

	if row.Verdict != VerdictUnpriceable {
		t.Errorf("verdict = %q, want %q", row.Verdict, VerdictUnpriceable)
	}
	if want := 2_500_000.0; row.LandedCost != want {
		t.Errorf("landed cost = %.0f, want %.0f (500k hull plus 2,500 m3 at 800)", row.LandedCost, want)
	}
	if row.ReferencePrice >= row.FloorPrice {
		t.Errorf("reference %.0f is not below the floor %.0f -- this test is no longer testing anything",
			row.ReferencePrice, row.FloorPrice)
	}
	if row.SuggestedPrice != 0 || row.PriceRule != PriceRuleNone {
		t.Errorf("suggested %.0f by rule %q, want no price at all", row.SuggestedPrice, row.PriceRule)
	}
	if row.SuggestedQty != 0 {
		t.Errorf("suggested qty = %d, want 0 -- nothing ships at a loss", row.SuggestedQty)
	}
	// The row must say which side lost, not just that it failed.
	if !strings.Contains(row.VerdictReason, "1.30") {
		t.Errorf("reason = %q, want it to name the ceiling that could not cover freight", row.VerdictReason)
	}
}

// TestBuildFWSupplyPlan_CoveredOutranksUnpriceable pins verdict precedence.
//
// The same unpriceable hull, but already stocked 166 days deep. "Already stocked"
// is the more useful answer: we are not shipping it either way, and saying we
// could not price it invites someone to go and fix the pricing.
func TestBuildFWSupplyPlan_CoveredOutranksUnpriceable(t *testing.T) {
	cfg := coverConfig()
	cfg.FreightISKPerM3 = 800

	item := FWSupplyItem{
		TypeID: typeTristan, TypeName: "Tristan", Category: "ship", VolumeM3: tristanVolumeM3,
		DailyDestroyed: 3, KillsWithItem: 40, JitaBestSell: 500_000,
		LocalOrders: []esi.MarketOrder{fwSell(typeTristan, staVillasenV, 700_000, 500)},
	}
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, cfg), typeTristan)

	if row.Verdict != VerdictCovered {
		t.Errorf("verdict = %q, want %q -- a stocked item we could not price is still one we are not shipping",
			row.Verdict, VerdictCovered)
	}
	if row.SuggestedPrice != 0 {
		t.Errorf("suggested price = %.0f, want none -- the pricing still failed", row.SuggestedPrice)
	}
}

// ladderBook is a destination book with five priced types under 1k ISK, two NPC
// seeds that would drag the median down if counted, and one richly marked-up type
// nobody in the warzone is losing.
func ladderBook() []esi.MarketOrder {
	return []esi.MarketOrder{
		fwSell(101, staVillasenV, 130, 500),  // Jita 100 -> 1.30
		fwSell(102, staVillasenV, 300, 500),  // Jita 200 -> 1.50
		fwSell(103, staVillasenV, 550, 500),  // Jita 500 -> 1.10
		fwSell(104, staVillasenV, 1200, 500), // Jita 800 -> 1.50
		fwSell(105, staVillasenV, 900, 500),  // Jita 900 -> 1.00

		// Seeds. Counted, these would make the median 1.10 instead of 1.30.
		fwSeed(101, staVillasenV, 90, 1_000_000),
		fwSeed(102, staVillasenV, 100, 1_000_000),

		// Sold here, but not something the warzone destroys: a 5.00 ratio that
		// would pull the median to 1.40 if the destroyed-type restriction slipped.
		fwSell(106, staVillasenV, 500, 500), // Jita 100 -> 5.00

		// A different station's book. Same region, different market.
		fwSell(101, staRakapasV, 400, 500),
	}
}

func ladderJitaPrices() map[int32]float64 {
	return map[int32]float64{101: 100, 102: 200, 103: 500, 104: 800, 105: 900, 106: 100}
}

func ladderDestroyed() map[int32]bool {
	return map[int32]bool{101: true, 102: true, 103: true, 104: true, 105: true}
}

// TestDeriveMarkupLadder_MeasuresPlayersOnly: the ladder is "what the neighbours
// charge", and a seed, a foreign station and an item nobody loses are all not
// the neighbours.
func TestDeriveMarkupLadder_MeasuresPlayersOnly(t *testing.T) {
	ladder := DeriveMarkupLadder(staVillasenV, ladderBook(), ladderJitaPrices(), ladderDestroyed())

	band := ladder.Band(100)
	if band.Samples != 5 {
		t.Fatalf("band samples = %d, want 5 -- one per destroyed type with both books at this station", band.Samples)
	}
	if band.Median != 1.30 {
		t.Errorf("median = %.4f, want 1.3000. Counting the two seeds gives 1.10; counting the "+
			"undestroyed 5.00x type gives 1.40 -- so this one number pins both exclusions.", band.Median)
	}
	if band.P75 != 1.50 {
		t.Errorf("p75 = %.4f, want 1.5000", band.P75)
	}
	if band.P75 < band.Median {
		t.Error("p75 below the median is arithmetically impossible and means the percentile helper is wrong")
	}
	if !band.Calibrated() {
		t.Error("5 samples meets minBandSamples and must be usable")
	}
}

// TestDeriveMarkupLadder_BandsDoNotBleed: the whole point of banding is that a
// sub-1k consumable and a 10M hull tolerate different markups, so a price must
// land in exactly one band.
func TestDeriveMarkupLadder_BandsDoNotBleed(t *testing.T) {
	ladder := DeriveMarkupLadder(staVillasenV, ladderBook(), ladderJitaPrices(), ladderDestroyed())

	// Every calibrated sample went into the sub-1k band; the ones above it are empty.
	for _, price := range []float64{1_000, 50_000, 500_000, 5_000_000, 50_000_000} {
		if band := ladder.Band(price); band.Samples != 0 {
			t.Errorf("band for %.0f ISK has %d samples, want 0 -- no fixture type is priced there", price, band.Samples)
		}
	}
	if band := ladder.Band(999.99); band.Samples != 5 {
		t.Errorf("999.99 ISK falls in the sub-1k band, got %d samples", band.Samples)
	}
	// 1,000 is the boundary and belongs to the band above, not below.
	if ladder.Band(1_000).MaxJitaPrice != 1e4 {
		t.Errorf("1,000 ISK landed in the band capped at %.0f, want the 1k-10k band", ladder.Band(1_000).MaxJitaPrice)
	}
}

// TestPriceFWSupply_AllSeedStationFallsBackToTheCeiling is the plan's named case.
//
// A station whose book is entirely NPC seed yields no percentiles at all. The
// honest answer is the category ceiling, not a median of the two or three ratios
// that happen to survive.
func TestPriceFWSupply_AllSeedStationFallsBackToTheCeiling(t *testing.T) {
	seedOnly := []esi.MarketOrder{
		fwSeed(101, staVillasenV, 3_000_000_000, 100),
		fwSeed(102, staVillasenV, 1_500_000_000, 100),
	}
	ladder := DeriveMarkupLadder(staVillasenV, seedOnly, map[int32]float64{101: 100, 102: 200}, ladderDestroyed())

	band := ladder.Band(100)
	if band.Samples != 0 || band.Calibrated() {
		t.Fatalf("an all-seed book produced %d samples; a 365-day order at 3B is not a price anyone paid", band.Samples)
	}

	cfg := coverConfig()
	cfg.Ladder = ladder
	item := FWSupplyItem{
		TypeID: typeInfernoLM, TypeName: "Inferno Light Missile", Category: "ammo",
		VolumeM3: missileVolumeM3, DailyDestroyed: 4553, KillsWithItem: 240, JitaBestSell: 100,
	}
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, cfg), typeInfernoLM)

	if row.ReferenceMarkup != 2.00 {
		t.Errorf("reference markup = %.2fx, want the 2.00x ammo ceiling", row.ReferenceMarkup)
	}
	if !strings.Contains(row.ReferenceSource, "ceiling") {
		t.Errorf("reference source = %q, want it to say the ceiling set the price", row.ReferenceSource)
	}
}

// TestPriceFWSupply_CeilingClampsAGenerousBand: where a station's own customers
// pay more than the category tolerates, the ceiling wins. That is the whole
// non-gouging guard, and it has to bind in the direction that costs us money.
func TestPriceFWSupply_CeilingClampsAGenerousBand(t *testing.T) {
	generous := MarkupLadder{StationID: staVillasenV, Bands: []MarkupBand{
		{MaxJitaPrice: 1e3, Samples: 40, Median: 3.00, P75: 6.00},
		{MaxJitaPrice: 1e4}, {MaxJitaPrice: 1e5}, {MaxJitaPrice: 1e6}, {MaxJitaPrice: 1e7},
		{MaxJitaPrice: math.MaxFloat64},
	}}
	cfg := coverConfig()
	cfg.Ladder = generous

	item := FWSupplyItem{
		TypeID: typeInfernoLM, Category: "ammo", VolumeM3: missileVolumeM3,
		DailyDestroyed: 4553, KillsWithItem: 240, JitaBestSell: 100,
	}
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, cfg), typeInfernoLM)

	if row.ReferenceMarkup != 2.00 {
		t.Errorf("reference markup = %.2fx, want it clamped to the 2.00x ammo ceiling despite a 3.00x band median",
			row.ReferenceMarkup)
	}
	if row.SuggestedPrice != 200 {
		t.Errorf("suggested price = %.2f, want 200", row.SuggestedPrice)
	}
}

// competitorConfig prices a 0.015 m3 missile: Jita 100, freight 7.5 ISK a unit,
// landed 107.5, floor ~128.5, ammo ceiling 2.00x so the reference is 200.
func competitorConfig() FWSupplyConfig {
	cfg := coverConfig()
	cfg.FreightISKPerM3 = 500
	cfg.StepOverDaysCover = DefaultStepOverDaysCover
	return cfg
}

func competitorItem(orders ...esi.MarketOrder) FWSupplyItem {
	return FWSupplyItem{
		TypeID: typeInfernoLM, TypeName: "Inferno Light Missile", Category: "ammo",
		VolumeM3: missileVolumeM3, DailyDestroyed: 1000, KillsWithItem: 240, JitaBestSell: 100,
		LocalOrders: orders,
	}
}

// TestPriceFWSupply_LadderNeverPropsAPriceUp is the constraint the user stated in
// their own words, in both directions.
//
// A competitor resting below the reference with real depth gets undercut -- the
// derived markup caps what we charge and must never stop us going under someone.
// A competitor resting above the reference does not drag us up to meet them.
func TestPriceFWSupply_LadderNeverPropsAPriceUp(t *testing.T) {
	cfg := competitorConfig()

	// Below the reference, 5,000 units against 1,000 destroyed a day: that is the
	// market price, and the reference is fiction.
	under := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{
		competitorItem(fwSell(typeInfernoLM, staVillasenV, 180, 5_000)),
	}, cfg), typeInfernoLM)

	// Guard that the fixture still poses the question: the competitor has to be
	// under the reference, or this is testing the branch above instead.
	if under.ReferencePrice != 200 {
		t.Fatalf("reference = %.2f, want 200 -- the competitor at 180 must sit below it", under.ReferencePrice)
	}
	if under.PriceRule != PriceRuleUndercut {
		t.Errorf("price rule = %q, want %q (%s)", under.PriceRule, PriceRuleUndercut, under.PriceReason)
	}
	if !(under.SuggestedPrice < 180) {
		t.Errorf("suggested %.2f, want strictly under the competitor at 180", under.SuggestedPrice)
	}
	if under.SuggestedPrice >= under.ReferencePrice {
		t.Errorf("suggested %.2f is not below the %.2f reference either", under.SuggestedPrice, under.ReferencePrice)
	}
	if !(under.SuggestedPrice > under.FloorPrice) {
		t.Errorf("suggested %.2f is at or below the floor %.2f", under.SuggestedPrice, under.FloorPrice)
	}
	if under.CompetingUnitsBelow != 5_000 || under.CompetingOrdersBelow != 1 {
		t.Errorf("competition reported as %d units in %d orders, want 5000 in 1 -- the row has to show its working",
			under.CompetingUnitsBelow, under.CompetingOrdersBelow)
	}
	// "1.99x -- undercutting 1 order(s) holding 5000 units at 180.00; reference was 2.00x"
	if !strings.Contains(under.PriceReason, "reference") {
		t.Errorf("reason = %q, want both the chosen price and the reference named", under.PriceReason)
	}

	// Above the reference: they are welcome to their price.
	over := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{
		competitorItem(fwSell(typeInfernoLM, staVillasenV, 400, 5_000)),
	}, cfg), typeInfernoLM)

	if over.PriceRule != PriceRuleReference {
		t.Errorf("price rule = %q, want %q (%s)", over.PriceRule, PriceRuleReference, over.PriceReason)
	}
	if over.SuggestedPrice != 200 {
		t.Errorf("suggested %.2f, want the 200 reference -- a competitor at 400 is not a licence to charge 400",
			over.SuggestedPrice)
	}
	if over.CompetingUnitsBelow != 0 {
		t.Errorf("competing units below the reference = %d, want 0", over.CompetingUnitsBelow)
	}
}

// TestPriceFWSupply_CompetitorBelowOurCost: depth decides here exactly as it does
// above our cost.
//
// Two units dumped cheap will clear on their own, so we step over them and wait
// -- abandoning the market over two units is the same mistake as chasing them
// down. Material depth under our cost is a different thing: it is the price the
// item actually trades at, and we cannot deliver it for that.
func TestPriceFWSupply_CompetitorBelowOurCost(t *testing.T) {
	cfg := competitorConfig()

	// 2 units against 1,000 destroyed a day. The floor is ~128.5, so 110 is under
	// our cost -- and irrelevant.
	shallow := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{
		competitorItem(fwSell(typeInfernoLM, staVillasenV, 110, 2)),
	}, cfg), typeInfernoLM)

	if !(shallow.FloorPrice > 110) {
		t.Fatalf("floor = %.2f, want it above the 110 competitor or this tests nothing", shallow.FloorPrice)
	}
	if shallow.Verdict != VerdictGap {
		t.Errorf("verdict = %q, want %q -- two units do not close a market", shallow.Verdict, VerdictGap)
	}
	if shallow.PriceRule != PriceRuleStepOver {
		t.Errorf("price rule = %q, want %q (%s)", shallow.PriceRule, PriceRuleStepOver, shallow.PriceReason)
	}
	if shallow.SuggestedPrice != 200 {
		t.Errorf("suggested %.2f, want the 200 reference", shallow.SuggestedPrice)
	}
	if shallow.CompetingUnitsBelow != 2 {
		t.Errorf("competing units = %d, want 2 shown so the dump is visible", shallow.CompetingUnitsBelow)
	}
	// The row has to say the units it stepped over were under our cost, or the
	// price looks like a normal step-over and the risk is invisible.
	if !strings.Contains(shallow.PriceReason, "landed cost") {
		t.Errorf("reason = %q, want it to name that the dump is below our landed cost", shallow.PriceReason)
	}

	// 5,000 units at the same price is what the item trades for, and we cannot
	// deliver it there.
	deep := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{
		competitorItem(fwSell(typeInfernoLM, staVillasenV, 110, 5_000)),
	}, cfg), typeInfernoLM)

	if deep.Verdict != VerdictUnpriceable {
		t.Errorf("verdict = %q, want %q", deep.Verdict, VerdictUnpriceable)
	}
	if deep.SuggestedPrice != 0 {
		t.Errorf("suggested %.2f, want no price -- not a loss-making undercut", deep.SuggestedPrice)
	}
	if deep.PriceRule != PriceRuleNone {
		t.Errorf("price rule = %q, want %q -- and never a silent fall back to the reference", deep.PriceRule, PriceRuleNone)
	}
	if !strings.Contains(deep.PriceReason, "landed cost") {
		t.Errorf("reason = %q, want it to name landed cost", deep.PriceReason)
	}
}

// TestPriceFWSupply_BelowCostBoundaryIsTheSameBoundary: the below-cost dump and
// the ordinary cheap competitor share one threshold, so the campaign has one
// setting to reason about rather than two.
func TestPriceFWSupply_BelowCostBoundaryIsTheSameBoundary(t *testing.T) {
	cfg := competitorConfig()
	const threshold = 500 // 0.5 days x 1,000 destroyed a day

	at := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{
		competitorItem(fwSell(typeInfernoLM, staVillasenV, 110, threshold)),
	}, cfg), typeInfernoLM)
	if at.Verdict != VerdictUnpriceable {
		t.Errorf("at exactly %d units below cost: verdict %q, want %q -- the boundary is inclusive, as it is above cost",
			threshold, at.Verdict, VerdictUnpriceable)
	}

	below := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{
		competitorItem(fwSell(typeInfernoLM, staVillasenV, 110, threshold-1)),
	}, cfg), typeInfernoLM)
	if below.PriceRule != PriceRuleStepOver {
		t.Errorf("at %d units below cost: rule %q, want %q (%s)",
			threshold-1, below.PriceRule, PriceRuleStepOver, below.PriceReason)
	}
}

// TestPriceFWSupply_SeedBelowCostIsNotSteppedOver is the asymmetry, asserted.
//
// A player dumping two units under our cost is stepped over, because they will
// clear. An NPC seed at the same price and the same trivial depth is not, because
// it will not.
func TestPriceFWSupply_SeedBelowCostIsNotSteppedOver(t *testing.T) {
	cfg := competitorConfig()
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{
		competitorItem(fwSeed(typeInfernoLM, staVillasenV, 110, 2)),
	}, cfg), typeInfernoLM)

	if row.Verdict != VerdictUnpriceable {
		t.Errorf("verdict = %q, want %q -- an NPC does not run out of stock the way a dumper does",
			row.Verdict, VerdictUnpriceable)
	}
	if !strings.Contains(row.VerdictReason, "NPC") {
		t.Errorf("reason = %q, want it to name the NPC", row.VerdictReason)
	}
}

// TestPriceFWSupply_StepOverBoundary pins the default rather than leaving it to
// whatever a caller happens to pass.
//
// At 1,000 destroyed a day and StepOverDaysCover of 0.5, 500 units is half a day
// of sales. Exactly 500 is material and gets undercut; 499 is worth stepping over
// and waiting out. And 1,000,000 seeded units below the reference are not depth
// at all -- without that exclusion every seeded type would read as infinite
// competition and never be priced.
func TestPriceFWSupply_StepOverBoundary(t *testing.T) {
	cfg := competitorConfig()
	const threshold = 500 // 0.5 days x 1,000 destroyed a day

	at := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{
		competitorItem(fwSell(typeInfernoLM, staVillasenV, 180, threshold)),
	}, cfg), typeInfernoLM)
	if at.PriceRule != PriceRuleUndercut {
		t.Errorf("at exactly %d units: rule %q, want %q -- the boundary is inclusive (%s)",
			threshold, at.PriceRule, PriceRuleUndercut, at.PriceReason)
	}
	if !(at.SuggestedPrice < 180) {
		t.Errorf("suggested %.2f, want under the competitor at 180", at.SuggestedPrice)
	}

	below := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{
		competitorItem(fwSell(typeInfernoLM, staVillasenV, 180, threshold-1)),
	}, cfg), typeInfernoLM)
	if below.PriceRule != PriceRuleStepOver {
		t.Errorf("at %d units: rule %q, want %q (%s)", threshold-1, below.PriceRule, PriceRuleStepOver, below.PriceReason)
	}
	if below.SuggestedPrice != 200 {
		t.Errorf("suggested %.2f, want the 200 reference -- 499 units is not worth abandoning the markup for",
			below.SuggestedPrice)
	}

	// Same 499 real units, plus a seed under the reference. The seed must change
	// nothing.
	withSeed := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{
		competitorItem(
			fwSell(typeInfernoLM, staVillasenV, 180, threshold-1),
			fwSeed(typeInfernoLM, staVillasenV, 190, 1_000_000),
		),
	}, cfg), typeInfernoLM)
	if withSeed.PriceRule != below.PriceRule || withSeed.SuggestedPrice != below.SuggestedPrice {
		t.Errorf("a 1,000,000-unit seed changed the outcome from %q at %.2f to %q at %.2f",
			below.PriceRule, below.SuggestedPrice, withSeed.PriceRule, withSeed.SuggestedPrice)
	}
	if withSeed.CompetingUnitsBelow != threshold-1 {
		t.Errorf("competing units = %d, want %d -- a 365-day order is not competing depth",
			withSeed.CompetingUnitsBelow, threshold-1)
	}
}

// TestPriceFWSupply_StepOverIsTheTristanRuleAgain is the user's own illustration:
// two units resting cheap are nothing against a day's destruction, so stepping
// over them beats abandoning the markup to clear them.
func TestPriceFWSupply_StepOverIsTheTristanRuleAgain(t *testing.T) {
	cfg := competitorConfig()
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{
		competitorItem(
			fwSell(typeInfernoLM, staVillasenV, 150, 2),
			fwSell(typeInfernoLM, staVillasenV, 280, 500),
		),
	}, cfg), typeInfernoLM)

	if row.PriceRule != PriceRuleStepOver {
		t.Errorf("rule = %q, want %q (%s)", row.PriceRule, PriceRuleStepOver, row.PriceReason)
	}
	if row.SuggestedPrice != 200 {
		t.Errorf("suggested %.2f, want the 200 reference rather than 149.9 to clear two units", row.SuggestedPrice)
	}
	// Orders above the reference are stock but not competition below it.
	if row.CompetingUnitsBelow != 2 {
		t.Errorf("competing units below the reference = %d, want 2", row.CompetingUnitsBelow)
	}
	if row.StockedQty != 502 {
		t.Errorf("stocked = %d, want 502 -- cover counts the whole book, whatever its price", row.StockedQty)
	}
}

// TestBuildFWSupplyPlan_RankedForTheBudget: §6 trims this list from the top, so
// the order has to be the order ISK should be spent in.
func TestBuildFWSupplyPlan_RankedForTheBudget(t *testing.T) {
	cfg := coverConfig()
	ammo := func(id int32, stocked int32, daily float64) FWSupplyItem {
		item := FWSupplyItem{
			TypeID: id, Category: "ammo", VolumeM3: missileVolumeM3,
			DailyDestroyed: daily, KillsWithItem: 50, JitaBestSell: 100,
		}
		if stocked > 0 {
			item.LocalOrders = []esi.MarketOrder{fwSell(id, staVillasenV, 300, stocked)}
		}
		return item
	}

	rows := BuildFWSupplyPlan([]FWSupplyItem{
		ammo(1001, 1000, 100), // 10 days -> thin
		ammo(1002, 0, 100),    // 0 days  -> gap, deficit 7
		ammo(1003, 500, 100),  // 5 days  -> gap, deficit 2
		ammo(1004, 3000, 100), // 30 days -> covered
	}, cfg)

	want := []int32{1002, 1003, 1001, 1004}
	for i, typeID := range want {
		if rows[i].TypeID != typeID {
			got := make([]int32, len(rows))
			for j, row := range rows {
				got[j] = row.TypeID
			}
			t.Fatalf("order = %v, want %v (gap before thin before covered, biggest hole first)", got, want)
		}
	}
	if d := rows[0].CoverDeficit(cfg.TargetCoverDays); d != 7 {
		t.Errorf("leading deficit = %.1f days, want 7", d)
	}
	if d := rows[3].CoverDeficit(cfg.TargetCoverDays); d != 0 {
		t.Errorf("a covered item has deficit %.1f, want 0 -- never negative", d)
	}
}

// TestCeilingFor_UnknownCategoryGetsTheStrictest: guessing generously is the
// gouging direction, so an item we could not classify is not one to be
// adventurous about.
func TestCeilingFor_UnknownCategoryGetsTheStrictest(t *testing.T) {
	cfg := FWSupplyConfig{}.withDefaults()

	if got := cfg.ceilingFor("ship"); got != 1.30 {
		t.Errorf("ship ceiling = %.2f, want 1.30", got)
	}
	if got := cfg.ceilingFor("ammo"); got != 2.00 {
		t.Errorf("ammo ceiling = %.2f, want 2.00", got)
	}
	for _, unknown := range []string{"", "implant", "nonsense"} {
		if got := cfg.ceilingFor(unknown); got != 1.30 {
			t.Errorf("ceiling for %q = %.2f, want the strictest configured (1.30)", unknown, got)
		}
	}
}

// TestBuildFWSupplyPlan_NoJitaSellIsUnpriceable: an item with no Jita sell order
// is one we cannot buy, which the plan uses as the self-filter for anything
// unrestockable. It is not a gap we can fill however deep the demand.
func TestBuildFWSupplyPlan_NoJitaSellIsUnpriceable(t *testing.T) {
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{{
		TypeID: typeInfernoLM, Category: "ammo", VolumeM3: missileVolumeM3,
		DailyDestroyed: 4553, KillsWithItem: 240, JitaBestSell: 0,
	}}, coverConfig()), typeInfernoLM)

	if row.Verdict != VerdictUnpriceable {
		t.Errorf("verdict = %q, want %q", row.Verdict, VerdictUnpriceable)
	}
	if row.SuggestedQty != 0 {
		t.Errorf("suggested qty = %d, want 0", row.SuggestedQty)
	}
}

// TestBuildFWSupplyPlan_NothingDestroyedIsNotAGap: a type with no measured
// destruction has infinite cover by definition. Dividing by zero and calling the
// result a gap would put every idle item at the top of the shipping list.
func TestBuildFWSupplyPlan_NothingDestroyedIsNotAGap(t *testing.T) {
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{{
		TypeID: typeVoidM, Category: "ammo", VolumeM3: 0.0125,
		DailyDestroyed: 0, KillsWithItem: 0, JitaBestSell: 250,
	}}, coverConfig()), typeVoidM)

	if row.Verdict != VerdictCovered {
		t.Errorf("verdict = %q, want %q", row.Verdict, VerdictCovered)
	}
	if row.SuggestedQty != 0 || row.CargoM3 != 0 || row.CostISK != 0 {
		t.Errorf("suggested %d units / %.1f m3 / %.0f ISK, want nothing at all",
			row.SuggestedQty, row.CargoM3, row.CostISK)
	}
}

// TestFWSupplyConfig_Defaults: a half-configured campaign has to behave, because
// a zero TargetCoverDays would divide the whole gap table by zero.
func TestFWSupplyConfig_Defaults(t *testing.T) {
	cfg := FWSupplyConfig{}.withDefaults()

	if cfg.TargetCoverDays != DefaultTargetCoverDays || cfg.CoveredMultiple != DefaultCoveredMultiple {
		t.Errorf("cover defaults = %.1f / %.1f, want %.1f / %.1f",
			cfg.TargetCoverDays, cfg.CoveredMultiple, DefaultTargetCoverDays, DefaultCoveredMultiple)
	}
	if cfg.StepOverDaysCover != DefaultStepOverDaysCover {
		t.Errorf("step-over default = %.2f, want %.2f", cfg.StepOverDaysCover, DefaultStepOverDaysCover)
	}
	if got := cfg.SalesTaxPercent + cfg.BrokerFeePercent; got != DefaultSellFeePct {
		t.Errorf("fee fallback = %.1f%%, want %.1f%% -- the same figure WarTracker has always assumed",
			got, DefaultSellFeePct)
	}

	// An explicit tax alone must not silently pick up the fallback broker fee on
	// top: the caller said what the fees are.
	explicit := FWSupplyConfig{SalesTaxPercent: 3.6}.withDefaults()
	if explicit.BrokerFeePercent != 0 {
		t.Errorf("broker fee = %.1f%%, want 0 -- the caller configured the fees", explicit.BrokerFeePercent)
	}
}

// A plan is only useful if it can be written down. encoding/json refuses to
// encode an infinity, and it refuses by failing the whole document -- so the
// two places FW Supply legitimately holds one, the top markup band and the
// cover of an item nothing is destroying, once took every row down with them.
// Both survive the wire now, and unbounded cover must come back unbounded:
// a cached plan reading zero would turn "covered forever" into a gap.
func TestUnboundedValuesSurviveJSON(t *testing.T) {
	ladder := DeriveMarkupLadder(60015070, nil, nil, nil)
	if _, err := json.Marshal(ladder); err != nil {
		t.Fatalf("marshal ladder: %v -- the top band must not be an infinity", err)
	}

	row := FWSupplyRow{TypeID: 587, DaysOfCover: CoverDays(math.Inf(1))}
	if !row.DaysOfCover.Unbounded() {
		t.Fatal("+Inf cover is not reported as unbounded")
	}
	payload, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal row: %v -- unbounded cover must not fail the plan", err)
	}
	if !strings.Contains(string(payload), `"days_of_cover":null`) {
		t.Errorf("unbounded cover encoded as %s, want null", payload)
	}

	var back FWSupplyRow
	if err := json.Unmarshal(payload, &back); err != nil {
		t.Fatalf("unmarshal row: %v", err)
	}
	if !back.DaysOfCover.Unbounded() {
		t.Errorf("cover read back as %v, want unbounded -- zero would read as a gap", back.DaysOfCover)
	}

	finite, err := json.Marshal(FWSupplyRow{TypeID: 587, DaysOfCover: 86})
	if err != nil {
		t.Fatalf("marshal finite cover: %v", err)
	}
	if !strings.Contains(string(finite), `"days_of_cover":86`) {
		t.Errorf("finite cover encoded as %s, want 86", finite)
	}
}

// TestBuildFWSupplyPlan_MarginIsMeasuredAfterFreightAndFees is the question a
// suggested price does not answer on its own: is charging it worth the trip.
//
// Every figure here is arithmetic the trader can redo by hand, which is the
// point -- a margin that cannot be checked against the market window is a number
// to be trusted rather than a number to be used.
func TestBuildFWSupplyPlan_MarginIsMeasuredAfterFreightAndFees(t *testing.T) {
	cfg := coverConfig() // 5% tax, 3% broker, 10% minimum margin
	cfg.FreightISKPerM3 = 10

	item := gapItem(typeInfernoLM, "Inferno Light Missile", 1_000, 0)
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, cfg), typeInfernoLM)

	// 100 ISK at Jita plus 0.015 m3 at 10 ISK/m3.
	if want := 100.15; math.Abs(row.LandedCost-want) > 1e-9 {
		t.Fatalf("landed cost = %.4f, want %.4f -- the rest of this test is measured against it", row.LandedCost, want)
	}
	// Empty book, so the ammo ceiling sets the price: 100 x 2.00.
	if want := 200.0; math.Abs(row.SuggestedPrice-want) > 1e-9 {
		t.Fatalf("suggested %.4f, want %.4f", row.SuggestedPrice, want)
	}

	if want := 184.0; math.Abs(row.NetUnitISK-want) > 1e-9 { // 200 less 8% of fees
		t.Errorf("net per unit = %.4f, want %.4f -- broker and tax come off the sale", row.NetUnitISK, want)
	}
	if want := 83.85; math.Abs(row.UnitProfitISK-want) > 1e-9 {
		t.Errorf("unit profit = %.4f, want %.4f", row.UnitProfitISK, want)
	}
	if want := 83.7244; math.Abs(row.MarginPct-want) > 1e-3 {
		t.Errorf("margin = %.4f%%, want %.4f%% of landed cost", row.MarginPct, want)
	}
	if want := 83.85 * 7_000; math.Abs(row.ProfitISK-want) > 1e-6 {
		t.Errorf("row profit = %.2f, want %.2f across the %d it asks for", row.ProfitISK, want, row.SuggestedQty)
	}
}

// TestApplyFWMargin_TheFloorReportsExactlyTheMinimum ties the two definitions of
// margin together.
//
// FloorPrice is built from MinMarginPct and MarginPct is computed back out of it,
// independently -- so if either ever changes its basis, the pair stops agreeing
// here rather than in a trade. Landed cost is the basis on both sides, which is
// also what the Order Desk measures a sell row against.
func TestApplyFWMargin_TheFloorReportsExactlyTheMinimum(t *testing.T) {
	cfg := coverConfig().withDefaults()
	cfg.FreightISKPerM3 = 10

	item := gapItem(typeInfernoLM, "Inferno Light Missile", 1_000, 0)
	priced := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, cfg), typeInfernoLM)

	atFloor := FWSupplyRow{SuggestedPrice: priced.FloorPrice, LandedCost: priced.LandedCost}
	applyFWMargin(&atFloor, cfg)

	if math.Abs(atFloor.MarginPct-cfg.MinMarginPct) > 1e-9 {
		t.Errorf("a row priced at its floor reports %.6f%%, want exactly the %.2f%% minimum the floor was built from",
			atFloor.MarginPct, cfg.MinMarginPct)
	}
}

// TestBuildFWSupplyPlan_UnpriceableEarnsNothing: a refusal must not read as an
// opportunity. The hull whose freight beats its ceiling has a landed cost and a
// floor, so a margin is computable -- against a price we declined to name.
func TestBuildFWSupplyPlan_UnpriceableEarnsNothing(t *testing.T) {
	cfg := coverConfig()
	cfg.FreightISKPerM3 = 800

	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{{
		TypeID: typeTristan, TypeName: "Tristan", Category: "ship", VolumeM3: tristanVolumeM3,
		DailyDestroyed: 3, KillsWithItem: 40, JitaBestSell: 500_000,
	}}, cfg), typeTristan)

	if row.Verdict != VerdictUnpriceable {
		t.Fatalf("verdict = %q, want %q -- this test needs the unpriceable case", row.Verdict, VerdictUnpriceable)
	}
	if row.NetUnitISK != 0 || row.UnitProfitISK != 0 || row.MarginPct != 0 || row.ProfitISK != 0 {
		t.Errorf("unpriceable row reports net %.2f profit %.2f margin %.2f%% total %.2f, want zeros",
			row.NetUnitISK, row.UnitProfitISK, row.MarginPct, row.ProfitISK)
	}
}

// --- included/excluded: your judgment overriding the model's, per item ------
//
// Excluded is entirely an fw_plan_build.go concern -- a type never becomes a
// candidate, so there is nothing for this package to test. Included is the
// engine's: it relaxes exactly two of its own cautions and nothing else.

// TestBuildFWSupplyPlan_IncludedSkipsTheThinEvidenceGate: one killmail is not
// three, and would ordinarily withhold a quantity from an otherwise real gap.
// Included is a statement that you trust this item more than the evidence does.
func TestBuildFWSupplyPlan_IncludedSkipsTheThinEvidenceGate(t *testing.T) {
	item := FWSupplyItem{
		TypeID: typeVoidM, TypeName: "Void M", Category: "ammo", VolumeM3: 0.0125,
		DailyDestroyed: 900, KillsWithItem: 1, JitaBestSell: 250, Included: true,
	}
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, coverConfig()), typeVoidM)

	if !row.Shippable {
		t.Errorf("1 killmail is under the %d-kill gate, but Included must ship it anyway", MinKillsWithItem)
	}
	if !row.Included {
		t.Error("the row did not carry Included through from the item")
	}
	if row.KillsWithItem != 1 {
		t.Errorf("kill count = %d, want 1 kept on the row -- the override changes what happens, not what is shown",
			row.KillsWithItem)
	}
}

// TestPriceFWSupply_IncludedStepsOverAMaterialDump is the pricing half. 5,000
// units resting under our landed cost is the exact fixture
// TestPriceFWSupply_CompetitorBelowOurCost refuses to price -- material depth,
// genuinely below cost. Included turns that refusal into the same step-over a
// trivial dump already gets, with a reason that names it as a deliberate
// override rather than the automatic judgment.
func TestPriceFWSupply_IncludedStepsOverAMaterialDump(t *testing.T) {
	cfg := competitorConfig()
	item := competitorItem(fwSell(typeInfernoLM, staVillasenV, 110, 5_000))
	item.Included = true
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, cfg), typeInfernoLM)

	if row.Verdict == VerdictUnpriceable {
		t.Fatalf("verdict is still unpriceable, so Included did not reach the pricing refusal: %s", row.PriceReason)
	}
	if row.PriceRule != PriceRuleStepOver {
		t.Errorf("price rule = %q, want %q (%s)", row.PriceRule, PriceRuleStepOver, row.PriceReason)
	}
	if row.SuggestedPrice != row.ReferencePrice {
		t.Errorf("suggested %.2f, want the %.2f reference -- an override buys in, it does not undercut a below-cost dump",
			row.SuggestedPrice, row.ReferencePrice)
	}
	if !row.Included {
		t.Error("the row did not carry Included through from the item")
	}
	if !strings.Contains(row.PriceReason, "included override") {
		t.Errorf("reason = %q, want it to say this was a deliberate override, not the automatic step-over judgment",
			row.PriceReason)
	}
}

// TestBuildFWSupplyPlan_IncludedDoesNotInventARate is the boundary stated
// explicitly in the plan: Included unlocks a candidate that already has a real
// rate, it does not fabricate one for a type nothing measures. Fabricating a
// rate here would be inventing demand with real ISK on the line.
func TestBuildFWSupplyPlan_IncludedDoesNotInventARate(t *testing.T) {
	item := FWSupplyItem{
		TypeID: typeVoidM, TypeName: "Void M", Category: "ammo", VolumeM3: 0.0125,
		DailyDestroyed: 0, KillsWithItem: 0, JitaBestSell: 250, Included: true,
	}
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, coverConfig()), typeVoidM)

	if row.Verdict != VerdictCovered {
		t.Errorf("verdict = %q, want %q -- nothing measured is nothing to size against, Included or not",
			row.Verdict, VerdictCovered)
	}
	if row.SuggestedQty != 0 {
		t.Errorf("suggested qty = %d, want 0 -- Included must not invent a quantity from a zero rate", row.SuggestedQty)
	}
}

// TestPriceFWSupply_IncludedDoesNotOverrideNPCSeed: the one refusal that stays
// absolute regardless of the override, because a seed never reprices -- there is
// no dump to wait out, so buying in anyway is a guaranteed, permanent loss rather
// than a bet.
func TestPriceFWSupply_IncludedDoesNotOverrideNPCSeed(t *testing.T) {
	cfg := competitorConfig()
	item := competitorItem(fwSeed(typeInfernoLM, staVillasenV, 110, 2))
	item.Included = true
	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{item}, cfg), typeInfernoLM)

	if row.Verdict != VerdictUnpriceable {
		t.Errorf("verdict = %q, want %q -- Included must not buy into a wall that will never move", row.Verdict, VerdictUnpriceable)
	}
	if !strings.Contains(row.VerdictReason, "NPC") {
		t.Errorf("reason = %q, want it to still name the NPC", row.VerdictReason)
	}
}

// TestPriceFWSupply_IncludedDoesNotOverrideCeilingBelowFloor: the other refusal
// that stays absolute. A ceiling too low for freight and fees is a configuration
// fact about the item and the destination, not a competition judgment, and
// overriding it would mean listing at a guaranteed loss.
func TestPriceFWSupply_IncludedDoesNotOverrideCeilingBelowFloor(t *testing.T) {
	cfg := coverConfig()
	cfg.FreightISKPerM3 = 800

	row := rowFor(t, BuildFWSupplyPlan([]FWSupplyItem{{
		TypeID: typeTristan, TypeName: "Tristan", Category: "ship", VolumeM3: tristanVolumeM3,
		DailyDestroyed: 3, KillsWithItem: 40, JitaBestSell: 500_000, Included: true,
	}}, cfg), typeTristan)

	if row.Verdict != VerdictUnpriceable {
		t.Errorf("verdict = %q, want %q -- Included must not list at a price guaranteed to lose money",
			row.Verdict, VerdictUnpriceable)
	}
	if row.SuggestedPrice != 0 {
		t.Errorf("suggested price = %.2f, want 0", row.SuggestedPrice)
	}
}

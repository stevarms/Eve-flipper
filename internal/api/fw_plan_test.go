package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
	"eve-flipper/internal/gankcheck"
	"eve-flipper/internal/sde"
	"eve-flipper/internal/zkillboard"
)

// fw_plan_test.go -- the plan endpoint's failure shapes.
//
// Every assertion here is about the same mistake, which is the one that makes
// this feature dangerous rather than merely wrong: a fetch that did not happen
// must never read as a market with nothing in it. An unmeasured region looks like
// a dead one, an unread book looks like an empty one, and an unchecked route
// looks like a safe one. All three look like opportunities, so none of them can
// be allowed to be silent.
//
// The rest is determinism. A plan the user reads down and then regenerates must
// not reshuffle because a map iterated differently.

// fwTestSDE is a three-system, three-station universe: enough to exercise the
// region fallback, the bulwark badge and the missing-station paths without
// loading the real SDE.
func fwTestSDE() *sde.Data {
	return &sde.Data{
		Regions: map[int32]*sde.Region{
			10000069: {ID: 10000069, Name: "Black Rise"},
			10000033: {ID: 10000033, Name: "The Citadel"},
			10000030: {ID: 10000030, Name: "Heimatar"},
		},
		Systems: map[int32]*sde.SolarSystem{
			30045324: {ID: 30045324, Name: "Onnamon", RegionID: 10000069, Security: 0.56},
			30045352: {ID: 30045352, Name: "Villasen", RegionID: 10000069, Security: 0.14},
			30000142: {ID: 30000142, Name: "Jita", RegionID: 10000033, Security: 0.95},
			// A system the SDE knows with no region, which is the shape a partial
			// load leaves behind.
			30099999: {ID: 30099999, Name: "Nowhere"},
		},
		Stations: map[int64]*sde.Station{
			60015070: {ID: 60015070, Name: "Onnamon IV - Moon 1 - Caldari Navy Assembly Plant", SystemID: 30045324},
			60015108: {ID: 60015108, Name: "Villasen V - Moon 1 - Caldari Navy Logistic Support", SystemID: 30045352},
			60003760: {ID: 60003760, Name: "Jita IV - Moon 4 - Caldari Navy Assembly Plant", SystemID: 30000142},
			60099999: {ID: 60099999, Name: "Nowhere I - Station", SystemID: 30099999},
		},
		Types: map[int32]*sde.ItemType{
			2488:  {ID: 2488, Name: "Warrior II", Volume: 5},
			24492: {ID: 24492, Name: "Inferno Light Missile", Volume: 0.0025},
			593:   {ID: 593, Name: "Tristan", Volume: 2500},
		},
	}
}

// --- the region fan-out ------------------------------------------------------

// TestFWDepthRegionsDeduplicatesAndSorts: forty candidates across three regions
// must be three fetches, in an order that does not depend on map iteration.
func TestFWDepthRegionsDeduplicatesAndSorts(t *testing.T) {
	candidates := []engine.FWStagingCandidate{
		{StationID: 1, StationName: "C", RegionID: 10000069},
		{StationID: 2, StationName: "A", RegionID: 10000033},
		{StationID: 3, StationName: "D", RegionID: 10000069},
		{StationID: 4, StationName: "B", RegionID: 10000030},
	}
	regions, byRegion, warnings := fwDepthRegions(candidates, fwTestSDE())

	if len(warnings) != 0 {
		t.Errorf("three regions warned about: %v", warnings)
	}
	want := []int32{10000030, 10000033, 10000069}
	if len(regions) != len(want) {
		t.Fatalf("regions: got %v want %v", regions, want)
	}
	for i, r := range want {
		if regions[i] != r {
			t.Fatalf("regions not sorted: got %v want %v", regions, want)
		}
	}
	if len(byRegion[10000069]) != 2 {
		t.Errorf("two stations in Black Rise not both recorded: %v", byRegion[10000069])
	}
}

// TestFWDepthRegionsCapsTheFanOutAndNamesWhatItDropped is the mis-set-radius
// guard. A radius that spans the cluster must cost one capped request, and the
// stations it silently under-ranks must be named -- a candidate measured with no
// depth is indistinguishable from a candidate whose market is dead.
func TestFWDepthRegionsCapsTheFanOutAndNamesWhatItDropped(t *testing.T) {
	var candidates []engine.FWStagingCandidate
	for i := 0; i < fwMaxDepthRegions+4; i++ {
		candidates = append(candidates, engine.FWStagingCandidate{
			StationID:   int64(i + 1),
			StationName: "station",
			RegionID:    int32(10000000 + i),
		})
	}
	regions, _, warnings := fwDepthRegions(candidates, fwTestSDE())

	if len(regions) != fwMaxDepthRegions {
		t.Errorf("fan-out not capped: %d regions", len(regions))
	}
	if len(warnings) == 0 {
		t.Fatal("regions dropped silently, so their stations rank last for no stated reason")
	}
	// The lowest-numbered regions survive, so the cap is deterministic rather
	// than dropping whichever ones a map iterated last.
	if regions[0] != 10000000 {
		t.Errorf("cap dropped a non-deterministic set: first region %d", regions[0])
	}
}

// TestFWDepthRegionsFallsBackToTheSDE: a candidate whose RegionID the ring did
// not set still has to be fetched for, or it loses its depth score to a missing
// field rather than to a missing market.
func TestFWDepthRegionsFallsBackToTheSDE(t *testing.T) {
	candidates := []engine.FWStagingCandidate{
		{StationID: 60015070, StationName: "Onnamon IV", SystemID: 30045324},
	}
	regions, _, _ := fwDepthRegions(candidates, fwTestSDE())
	if len(regions) != 1 || regions[0] != 10000069 {
		t.Errorf("region not recovered from the SDE: %v", regions)
	}

	// And a system with no region at all is skipped rather than fetched as 0,
	// which would be a request for a region that does not exist.
	unknown := []engine.FWStagingCandidate{
		{StationID: 60099999, StationName: "Nowhere I", SystemID: 30099999},
	}
	if regions, _, _ := fwDepthRegions(unknown, fwTestSDE()); len(regions) != 0 {
		t.Errorf("a region-less candidate produced a fetch: %v", regions)
	}
}

// TestFWDepthRegionsEmptyRingFetchesNothing: an empty ring must not warn and must
// not fetch. A militia holding no systems is a real state, not a failure.
func TestFWDepthRegionsEmptyRingFetchesNothing(t *testing.T) {
	regions, byRegion, warnings := fwDepthRegions(nil, fwTestSDE())
	if len(regions) != 0 || len(byRegion) != 0 || len(warnings) != 0 {
		t.Errorf("empty ring produced work: %v %v %v", regions, byRegion, warnings)
	}
}

// --- the price books ---------------------------------------------------------

// TestFWBestSellByTypeTakesTheCheapest: the cost basis is what you would pay, so
// it is the lowest live sell, and buy orders and exhausted orders are not it.
func TestFWBestSellByTypeTakesTheCheapest(t *testing.T) {
	orders := []esi.MarketOrder{
		{TypeID: 2488, Price: 900_000, VolumeRemain: 10, Duration: 90},
		{TypeID: 2488, Price: 750_000, VolumeRemain: 40, Duration: 30},
		{TypeID: 2488, Price: 100_000, VolumeRemain: 0, Duration: 90},     // exhausted
		{TypeID: 2488, Price: 10_000, VolumeRemain: 50, IsBuyOrder: true}, // a bid
		{TypeID: 24492, Price: 0, VolumeRemain: 100, Duration: 90},        // nonsense
	}
	best := fwBestSellByType(orders, false)

	if got := best[2488]; got != 750_000 {
		t.Errorf("best sell: got %v want 750000", got)
	}
	if _, priced := best[24492]; priced {
		t.Error("a zero-priced order became a cost basis")
	}
}

// TestFWBestSellByTypeKeepsNPCSeedsWhenAsked is the asymmetry worth pinning. At
// the source hub a 365-day seed is a price you can actually pay, so it belongs in
// the cost basis; at the destination it is competition that will never move, so
// the caller excludes it there. One function, two callers, opposite answers.
func TestFWBestSellByTypeKeepsNPCSeedsWhenAsked(t *testing.T) {
	orders := []esi.MarketOrder{
		{TypeID: 593, Price: 5_000_000, VolumeRemain: 100, Duration: 365},
		{TypeID: 593, Price: 9_000_000, VolumeRemain: 3, Duration: 90},
	}
	if got := fwBestSellByType(orders, false)[593]; got != 5_000_000 {
		t.Errorf("seed excluded from the source basis: got %v want 5000000", got)
	}
	if got := fwBestSellByType(orders, true)[593]; got != 9_000_000 {
		t.Errorf("seed counted as competition: got %v want 9000000", got)
	}
	// A book that is nothing but seed yields no price at all when they are
	// excluded, rather than a zero that would read as free.
	seedOnly := []esi.MarketOrder{{TypeID: 593, Price: 5_000_000, VolumeRemain: 100, Duration: 365}}
	if _, priced := fwBestSellByType(seedOnly, true)[593]; priced {
		t.Error("an all-seed book produced a player price")
	}
}

// TestFWLadderCalibratedNeedsRealSamples: a ladder of thin bands must report
// itself uncalibrated, because the caller turns that into the warning that says
// the prices came from the ceilings instead of from the station.
func TestFWLadderCalibratedNeedsRealSamples(t *testing.T) {
	thin := engine.MarkupLadder{StationID: 60015070, Bands: []engine.MarkupBand{
		{MaxJitaPrice: 1_000, Samples: 1, Median: 1.17},
		{MaxJitaPrice: 10_000, Samples: 4, Median: 1.18},
	}}
	if fwLadderCalibrated(thin) {
		t.Error("a ladder with no band above the sample floor reported itself calibrated")
	}
	thin.Bands = append(thin.Bands, engine.MarkupBand{MaxJitaPrice: 100_000, Samples: 40, Median: 1.57})
	if !fwLadderCalibrated(thin) {
		t.Error("one well-sampled band was not enough to count as calibrated")
	}
	if fwLadderCalibrated(engine.MarkupLadder{StationID: 1}) {
		t.Error("an empty ladder reported itself calibrated")
	}
}

// --- the gap table's input ---------------------------------------------------

func fwTestDemand() *zkillboard.MilitiaDemandProfile {
	return &zkillboard.MilitiaDemandProfile{
		MilitiaFactionID: esi.MilitiaCaldari,
		Items: map[int32]*zkillboard.ItemDemandProfile{
			24492: {TypeID: 24492, TypeName: "Inferno Light Missile", Category: "ammo",
				KillmailCount: 180, EstDailyDemand: 4553},
			2488: {TypeID: 2488, TypeName: "Warrior II", Category: "drone",
				KillmailCount: 2, EstDailyDemand: 40},
			593: {TypeID: 593, TypeName: "Tristan", Category: "ship",
				KillmailCount: 12, EstDailyDemand: 3},
		},
	}
}

// TestFWSupplyItemsDropsUnbuyableAndKeepsThinEvidence pins both halves of the
// filter, which pull in opposite directions.
//
// An item with no sell order at the source cannot be bought at any quantity, so
// it goes. An item seen on two losses stays, with its count, because the engine
// decides shippability and the gap table exists to show thin evidence as thin --
// dropping it here would hide exactly what the user needs to judge.
func TestFWSupplyItemsDropsUnbuyableAndKeepsThinEvidence(t *testing.T) {
	sourceBest := map[int32]float64{
		24492: 105,
		2488:  750_000,
		// Tristan deliberately absent: destroyed, but nothing to buy.
	}
	items, skipped := fwSupplyItems(fwTestDemand(), nil, sourceBest, nil, fwTestSDE(), nil, nil)

	if skipped != 1 {
		t.Errorf("unbuyable types skipped: got %d want 1", skipped)
	}
	byType := map[int32]engine.FWSupplyItem{}
	for _, it := range items {
		byType[it.TypeID] = it
	}
	if _, shipped := byType[593]; shipped {
		t.Error("a type with no source sell order was planned for anyway")
	}
	thin, kept := byType[2488]
	if !kept {
		t.Fatal("a two-killmail item was dropped, hiding the thinness instead of showing it")
	}
	if thin.KillsWithItem != 2 {
		t.Errorf("KillsWithItem lost: got %d want 2", thin.KillsWithItem)
	}
	if thin.VolumeM3 != 5 {
		t.Errorf("volume not read from the SDE: got %v want 5", thin.VolumeM3)
	}
	if ammo := byType[24492]; ammo.DailyDestroyed != 4553 || ammo.JitaBestSell != 105 {
		t.Errorf("ammo row wrong: %+v", ammo)
	}
}

// TestFWSupplyItemsExcludedTypeNeverBecomesARow: the ask was to omit a type, not
// to badge it, so an excluded type must not appear as covered, unpriceable, or
// anything else -- it is checked before demand or price are even looked up.
func TestFWSupplyItemsExcludedTypeNeverBecomesARow(t *testing.T) {
	sourceBest := map[int32]float64{24492: 105, 2488: 750_000, 593: 9_000_000}
	items, skipped := fwSupplyItems(fwTestDemand(), nil, sourceBest, nil, fwTestSDE(),
		map[int32]bool{24492: true}, nil)

	if skipped != 0 {
		t.Errorf("an excluded type was counted as unbuyable-and-skipped: %d", skipped)
	}
	for _, it := range items {
		if it.TypeID == 24492 {
			t.Fatalf("the excluded type produced a row: %+v", it)
		}
	}
	if len(items) != 2 {
		t.Fatalf("excluding one candidate changed the others: got %d items, want 2", len(items))
	}
}

// TestFWSupplyItemsIncludedMarksTheItemNotEveryone: Included has to name the one
// type it applies to. Marking every candidate would be indistinguishable from a
// bug that always sets the flag.
func TestFWSupplyItemsIncludedMarksTheItemNotEveryone(t *testing.T) {
	sourceBest := map[int32]float64{24492: 105, 2488: 750_000}
	items, _ := fwSupplyItems(fwTestDemand(), nil, sourceBest, nil, fwTestSDE(),
		nil, map[int32]bool{2488: true})

	byType := map[int32]engine.FWSupplyItem{}
	for _, it := range items {
		byType[it.TypeID] = it
	}
	if !byType[2488].Included {
		t.Error("the included type was not marked Included")
	}
	if byType[24492].Included {
		t.Error("a type that was not included came out marked Included anyway")
	}
}

// TestFWSupplyItemsIsDeterministic: the user reads a ranked table and then
// regenerates it. Two runs over the same inputs must produce the same order, or
// every regeneration looks like the market moved.
func TestFWSupplyItemsIsDeterministic(t *testing.T) {
	sourceBest := map[int32]float64{24492: 105, 2488: 750_000, 593: 9_000_000}
	first, _ := fwSupplyItems(fwTestDemand(), nil, sourceBest, nil, fwTestSDE(), nil, nil)
	for i := 0; i < 20; i++ {
		again, _ := fwSupplyItems(fwTestDemand(), nil, sourceBest, nil, fwTestSDE(), nil, nil)
		if len(again) != len(first) {
			t.Fatalf("row count changed between runs: %d then %d", len(first), len(again))
		}
		for j := range first {
			if again[j].TypeID != first[j].TypeID {
				t.Fatalf("rows reordered at %d: %d then %d", j, first[j].TypeID, again[j].TypeID)
			}
		}
	}
}

// TestFWSupplyItemsNoDemandIsNoRows: a failed killmail pass must yield nothing to
// ship, not everything. The caller turns this into a warning.
//
// Both windows nil, because with two passes the dangerous case is not "neither
// ran" but "the one I was not looking at ran": either half alone is enough to
// earn rows, so the empty result has to require both to be absent.
func TestFWSupplyItemsNoDemandIsNoRows(t *testing.T) {
	items, skipped := fwSupplyItems(nil, nil, map[int32]float64{24492: 105}, nil, fwTestSDE(), nil, nil)
	if len(items) != 0 || skipped != 0 {
		t.Errorf("two nil demand profiles produced %d items and %d skips", len(items), skipped)
	}
}

// --- the two windows ---------------------------------------------------------

// fwTestLongDemand is the same warzone measured over a quarter instead of a week,
// and it disagrees with fwTestDemand in all three of the ways that matter:
//
//   - Inferno Light Missile is present in both, at a quieter long rate -- the
//     ordinary case, where the week ran hot.
//   - Warrior II is present in both, and the long window has far more evidence
//     behind the same kind of number: two killmails in seven days is thin, thirty
//     in ninety days is a habit.
//   - 1877 (Scourge Fury) is present ONLY here. A staple that happened not to die
//     this week is the entire reason the long window was added.
//
// And Tristan (593) is present only in the short window, which is the spike half
// of the same comparison.
func fwTestLongDemand() *zkillboard.MilitiaDemandProfile {
	return &zkillboard.MilitiaDemandProfile{
		MilitiaFactionID: esi.MilitiaCaldari,
		WindowSeconds:    7_776_000,
		Items: map[int32]*zkillboard.ItemDemandProfile{
			24492: {TypeID: 24492, TypeName: "Inferno Light Missile", Category: "ammo",
				KillmailCount: 1900, EstDailyDemand: 1200},
			2488: {TypeID: 2488, TypeName: "Warrior II", Category: "drone",
				KillmailCount: 30, EstDailyDemand: 6},
			1877: {TypeID: 1877, TypeName: "Scourge Fury Light Missile", Category: "ammo",
				KillmailCount: 640, EstDailyDemand: 900},
		},
	}
}

// TestFWSupplyItemsUnionsTheTwoWindows is the union rule, which is the whole
// shape of the feature.
//
// Intersecting would drop Scourge Fury -- an item that did not die this week and
// dies constantly over a quarter -- which is precisely the item the long window
// exists to find. Preferring the long window would drop Tristan, which is the
// spike the short window exists to catch. Both rows have to be here, and each has
// to carry both numbers so a reader can see which window is speaking.
func TestFWSupplyItemsUnionsTheTwoWindows(t *testing.T) {
	sourceBest := map[int32]float64{24492: 105, 2488: 750_000, 593: 9_000_000, 1877: 220}
	items, skipped := fwSupplyItems(fwTestDemand(), fwTestLongDemand(), sourceBest, nil, fwTestSDE(), nil, nil)
	if skipped != 0 {
		t.Errorf("everything was priced, yet %d types were skipped", skipped)
	}

	byType := map[int32]engine.FWSupplyItem{}
	ordered := make([]int32, 0, len(items))
	for _, it := range items {
		byType[it.TypeID] = it
		ordered = append(ordered, it.TypeID)
	}
	// Sorted by type id, once each, however many windows saw them.
	want := []int32{593, 1877, 2488, 24492}
	if len(ordered) != len(want) {
		t.Fatalf("row set wrong: got %v want %v", ordered, want)
	}
	for i := range want {
		if ordered[i] != want[i] {
			t.Fatalf("row order wrong: got %v want %v", ordered, want)
		}
	}

	// The long-only staple: a row, with no short rate at all rather than a
	// fabricated one.
	staple := byType[1877]
	if staple.DailyDestroyedLong != 900 || staple.KillsWithItemLong != 640 {
		t.Errorf("long-only staple lost its long rate: %+v", staple)
	}
	if staple.DailyDestroyed != 0 || staple.KillsWithItem != 0 {
		t.Errorf("long-only staple invented a short rate: %+v", staple)
	}
	if staple.TypeName != "Scourge Fury Light Missile" || staple.Category != "ammo" {
		t.Errorf("long-only staple lost its naming: %+v", staple)
	}

	// The short-only spike: kept, and visibly a spike -- a short rate with nothing
	// behind it over the quarter.
	spike := byType[593]
	if spike.DailyDestroyed != 3 || spike.KillsWithItem != 12 {
		t.Errorf("short-only spike lost its short rate: %+v", spike)
	}
	if spike.DailyDestroyedLong != 0 || spike.KillsWithItemLong != 0 {
		t.Errorf("short-only spike invented a long rate: %+v", spike)
	}

	// Both windows: both numbers, unblended. A 4553/day week against a 1200/day
	// quarter is the comparison the feature is for, and averaging them would
	// destroy it.
	ammo := byType[24492]
	if ammo.DailyDestroyed != 4553 || ammo.DailyDestroyedLong != 1200 {
		t.Errorf("ammo rates blended or crossed: %+v", ammo)
	}
	if ammo.KillsWithItem != 180 || ammo.KillsWithItemLong != 1900 {
		t.Errorf("ammo evidence counts crossed: %+v", ammo)
	}
	if ammo.DailyDestroyed <= ammo.DailyDestroyedLong {
		t.Error("the short window did not read hotter than the long one, so the fixture no longer tests a spike")
	}

	// Thin this week, well-evidenced over the quarter. Both counts have to survive
	// because MinKillsWithItem is judged against the window that sizes the row.
	thin := byType[2488]
	if thin.KillsWithItem != 2 || thin.KillsWithItemLong != 30 {
		t.Errorf("the thin-week/steady-quarter row lost one of its counts: %+v", thin)
	}
	if thin.VolumeM3 != 5 {
		t.Errorf("volume not read from the SDE on a two-window row: %+v", thin)
	}
}

// TestFWSupplyItemsShortOnlyIsUnchanged is the no-regression guard.
//
// With no long window configured -- which is every campaign that existed before
// this change, and the default for every one created after -- the result must be
// exactly what it was: the same rows, in the same order, with the same numbers,
// and the long fields zero rather than absent-looking-like-zero on a row that
// meant something else.
func TestFWSupplyItemsShortOnlyIsUnchanged(t *testing.T) {
	sourceBest := map[int32]float64{24492: 105, 2488: 750_000}
	items, skipped := fwSupplyItems(fwTestDemand(), nil, sourceBest, nil, fwTestSDE(), nil, nil)
	if skipped != 1 {
		t.Errorf("unbuyable types skipped: got %d want 1", skipped)
	}
	want := []engine.FWSupplyItem{
		{TypeID: 2488, TypeName: "Warrior II", Category: "drone", VolumeM3: 5,
			DailyDestroyed: 40, KillsWithItem: 2, JitaBestSell: 750_000},
		{TypeID: 24492, TypeName: "Inferno Light Missile", Category: "ammo", VolumeM3: 0.0025,
			DailyDestroyed: 4553, KillsWithItem: 180, JitaBestSell: 105},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("the short-only path changed shape:\n got %+v\nwant %+v", items, want)
	}
}

// TestFWSupplyItemsZeroInBothWindowsIsDropped: a rate of zero in one window is
// not a reason to drop a type -- that is the union rule -- but zero in both is,
// because there is nothing to size against on either side and the row would be a
// name with no demand behind it.
func TestFWSupplyItemsZeroInBothWindowsIsDropped(t *testing.T) {
	short := &zkillboard.MilitiaDemandProfile{Items: map[int32]*zkillboard.ItemDemandProfile{
		24492: {TypeID: 24492, TypeName: "Inferno Light Missile", EstDailyDemand: 0, KillmailCount: 1},
	}}
	long := &zkillboard.MilitiaDemandProfile{Items: map[int32]*zkillboard.ItemDemandProfile{
		24492: {TypeID: 24492, TypeName: "Inferno Light Missile", EstDailyDemand: 0, KillmailCount: 3},
	}}
	items, skipped := fwSupplyItems(short, long, map[int32]float64{24492: 105}, nil, fwTestSDE(), nil, nil)
	if len(items) != 0 || skipped != 0 {
		t.Errorf("a type with no rate in either window produced %d items and %d skips", len(items), skipped)
	}
	// And zero in one window alone is still a row.
	long.Items[24492].EstDailyDemand = 900
	items, _ = fwSupplyItems(short, long, map[int32]float64{24492: 105}, nil, fwTestSDE(), nil, nil)
	if len(items) != 1 || items[0].DailyDestroyedLong != 900 {
		t.Errorf("a long-only rate did not earn a row: %+v", items)
	}
}

// --- the destination ---------------------------------------------------------

// TestFWResolveDestinationNamesTheStation, including the bulwark badge that makes
// Onnamon legible as the staging hub rather than as one more highsec station.
func TestFWResolveDestinationNamesTheStation(t *testing.T) {
	dest, err := fwResolveDestination(fwTestSDE(), 60015070, esi.MilitiaCaldari)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if dest.SystemID != 30045324 || dest.RegionID != 10000069 {
		t.Errorf("geography wrong: %+v", dest)
	}
	if dest.Security != 0.56 {
		t.Errorf("security: got %v want 0.56", dest.Security)
	}
	if !dest.IsBulwark {
		t.Error("Onnamon not badged as the Caldari bulwark")
	}
	// The same station is not the Gallente bulwark, so the badge is per militia
	// rather than a property of the station.
	if other, err := fwResolveDestination(fwTestSDE(), 60015070, esi.MilitiaGallente); err != nil || other.IsBulwark {
		t.Errorf("Onnamon badged as the Gallente bulwark: %+v %v", other, err)
	}
	// A frontline station is a valid destination and simply is not a bulwark.
	if villasen, err := fwResolveDestination(fwTestSDE(), 60015108, esi.MilitiaCaldari); err != nil || villasen.IsBulwark {
		t.Errorf("Villasen: %+v %v", villasen, err)
	}
}

// TestFWResolveDestinationRefusesWhatItCannotAddress is the load-bearing failure.
// The region is what every later fetch is addressed by, so a station whose region
// is unknown must fail rather than let the plan fetch some other region's book and
// present it as the destination's competition.
func TestFWResolveDestinationRefusesWhatItCannotAddress(t *testing.T) {
	if _, err := fwResolveDestination(fwTestSDE(), 60099999, esi.MilitiaCaldari); err == nil {
		t.Error("a station with no region resolved, so its market would be fetched from region 0")
	}
	if _, err := fwResolveDestination(fwTestSDE(), 61000000, esi.MilitiaCaldari); err == nil {
		t.Error("a station absent from the SDE resolved")
	}
}

// --- occupancy drift ---------------------------------------------------------

// TestFWDiffOccupancyReportsAnOccupierChange: this is the risk with no market
// signal. The book looks identical the day the militia leaves, so the change has
// to be stated in terms of what it costs -- the demand behind the gap table.
func TestFWDiffOccupancyReportsAnOccupierChange(t *testing.T) {
	was := []fwOccupancy{
		{SystemID: 30045352, SystemName: "Villasen", OccupierFactionID: esi.MilitiaCaldari, Contested: "contested"},
	}
	live := map[int32]esi.FWSystem{
		30045352: {SolarSystemID: 30045352, OccupierFactionID: esi.MilitiaGallente, Contested: "contested"},
	}
	drift := fwDiffOccupancy(was, live)
	if len(drift) != 1 {
		t.Fatalf("occupier change not reported: %v", drift)
	}
	if !strings.Contains(drift[0], "Villasen") {
		t.Errorf("drift does not name the system: %q", drift[0])
	}
	if !strings.Contains(drift[0], "Gallente Federation") {
		t.Errorf("drift does not name the new holder: %q", drift[0])
	}
	if !strings.Contains(drift[0], "demand") {
		t.Errorf("drift states the fact but not the consequence, so it reads as trivia: %q", drift[0])
	}
}

// TestFWDiffOccupancyIsSilentWhenNothingMoved: a warning that fires every read
// gets ignored, which is how the one that matters gets missed.
func TestFWDiffOccupancyIsSilentWhenNothingMoved(t *testing.T) {
	was := []fwOccupancy{
		{SystemID: 30045352, SystemName: "Villasen", OccupierFactionID: esi.MilitiaCaldari, Contested: "contested"},
	}
	live := map[int32]esi.FWSystem{
		30045352: {SolarSystemID: 30045352, OccupierFactionID: esi.MilitiaCaldari, Contested: "contested",
			VictoryPoints: 900}, // points move constantly and are not drift
	}
	if drift := fwDiffOccupancy(was, live); len(drift) != 0 {
		t.Errorf("unchanged occupancy reported as drift: %v", drift)
	}
	if drift := fwDiffOccupancy(nil, live); len(drift) != 0 {
		t.Errorf("an empty snapshot produced drift: %v", drift)
	}
}

// TestFWDiffOccupancyReportsContestedAndDeparture: a contested-state change is
// worth a line and no more; a system gone from the list has stopped being
// contested, which is an observation rather than missing data -- the endpoint
// only lists contested systems.
func TestFWDiffOccupancyReportsContestedAndDeparture(t *testing.T) {
	was := []fwOccupancy{
		{SystemID: 30045352, SystemName: "Villasen", OccupierFactionID: esi.MilitiaCaldari, Contested: "contested"},
		{SystemID: 30045324, SystemName: "Kinakka", OccupierFactionID: esi.MilitiaCaldari, Contested: "vulnerable"},
	}
	live := map[int32]esi.FWSystem{
		30045352: {SolarSystemID: 30045352, OccupierFactionID: esi.MilitiaCaldari, Contested: "vulnerable"},
	}
	drift := fwDiffOccupancy(was, live)
	if len(drift) != 2 {
		t.Fatalf("expected a contested change and a departure: %v", drift)
	}
	if !strings.Contains(drift[0], "contested") || !strings.Contains(drift[0], "vulnerable") {
		t.Errorf("contested change does not name both states: %q", drift[0])
	}
	if !strings.Contains(drift[1], "no longer contested") {
		t.Errorf("a departed system misreported: %q", drift[1])
	}
}

// --- the haul ----------------------------------------------------------------

// TestFWWorstDangerEscalatesRegardlessOfOrder is the quiet-failure guard. A route
// whose red system happens to be read first and whose last nine are green must
// not come back green.
func TestFWWorstDangerEscalatesRegardlessOfOrder(t *testing.T) {
	red := gankcheck.SystemDanger{SystemID: 1, SystemName: "Tama", DangerLevel: "red", KillsTotal: 140}
	yellow := gankcheck.SystemDanger{SystemID: 2, SystemName: "Nourvukaiken", DangerLevel: "yellow", KillsTotal: 12}
	green := gankcheck.SystemDanger{SystemID: 3, SystemName: "Onnamon", DangerLevel: "green"}

	for _, route := range [][]gankcheck.SystemDanger{
		{red, green, green},
		{green, green, red},
		{green, yellow, red, green},
		{red, yellow},
	} {
		verdict, hot := fwWorstDanger(route)
		if verdict != "red" {
			t.Errorf("route %v read as %q", route, verdict)
		}
		if len(hot) == 0 {
			t.Errorf("route %v named no hot system, so the verdict has nothing behind it", route)
		}
	}

	if verdict, hot := fwWorstDanger([]gankcheck.SystemDanger{green, yellow, green}); verdict != "yellow" || len(hot) != 1 {
		t.Errorf("yellow route: verdict %q hot %v", verdict, hot)
	}
	// Green systems are not listed as hot: a "hot systems" list containing the
	// quiet ones is a list nobody reads.
	if verdict, hot := fwWorstDanger([]gankcheck.SystemDanger{green, green}); verdict != "green" || len(hot) != 0 {
		t.Errorf("clean route: verdict %q hot %v", verdict, hot)
	}
}

// TestFWWorstDangerEmptyIsGreen: gankcheck answered and found nothing, which is
// green. The case where it did not answer is Checked false, and the two must not
// be confused -- that is the whole reason Checked exists.
func TestFWWorstDangerEmptyIsGreen(t *testing.T) {
	verdict, hot := fwWorstDanger(nil)
	if verdict != "green" || len(hot) != 0 {
		t.Errorf("empty reading: verdict %q hot %v", verdict, hot)
	}
	// And the unchecked case renders differently, by construction: a route with
	// Checked false carries no verdict at all.
	route := fwPlanRoute{Jumps: 7}
	if route.Checked || route.Verdict != "" {
		t.Error("an unchecked route carries a verdict, so it renders as a clean one")
	}
}

// --- small pieces with sharp edges -------------------------------------------

// TestInt32SetNilStaysNil: the ring reads a nil pin set as "nobody is pinned" and
// an empty non-nil one the same way, but a nil exclude set must not be
// constructed as something the ring could mistake for a filter.
func TestInt32SetNilStaysNil(t *testing.T) {
	if got := int32Set(nil); got != nil {
		t.Errorf("nil list produced %v", got)
	}
	if got := int32Set([]int32{}); got != nil {
		t.Errorf("empty list produced %v", got)
	}
	got := int32Set([]int32{30045324, 30045352, 30045324})
	if len(got) != 2 || !got[30045324] || !got[30045352] {
		t.Errorf("set: %v", got)
	}
}

// TestJoinAndReadsAsASentenceAndStaysShort: these strings go into warnings, and a
// warning that prints forty station names is one the user scrolls past.
func TestJoinAndReadsAsASentenceAndStaysShort(t *testing.T) {
	if got := joinAnd(nil); got != "nothing" {
		t.Errorf("empty: %q", got)
	}
	if got := joinAnd([]string{"Black Rise"}); got != "Black Rise" {
		t.Errorf("one: %q", got)
	}
	if got := joinAnd([]string{"Black Rise", "Placid"}); got != "Black Rise and Placid" {
		t.Errorf("two: %q", got)
	}
	if got := joinAnd([]string{"a", "b", "c"}); got != "a, b and c" {
		t.Errorf("three: %q", got)
	}
	long := joinAnd([]string{"a", "b", "c", "d", "e", "f", "g"})
	if !strings.Contains(long, "4 others") {
		t.Errorf("long list not truncated: %q", long)
	}
}

// TestFWMilitiaLabelNamesEveryMilitia: the label is a heading and appears inside
// the drift warning, so an unnamed militia would read as "faction 500004 now
// holds this" -- true and useless.
func TestFWMilitiaLabelNamesEveryMilitia(t *testing.T) {
	for militia := range esi.BulwarkSystems {
		name := fwMilitiaLabel(militia)
		if name == "" || strings.HasPrefix(name, "faction ") {
			t.Errorf("militia %d unnamed: %q", militia, name)
		}
	}
	// Zero is the uncontested case and must read as prose, not as a missing value.
	if got := fwMilitiaLabel(0); got != "nobody" {
		t.Errorf("unoccupied: %q", got)
	}
	if got := fwMilitiaLabel(500010); !strings.Contains(got, "500010") {
		t.Errorf("an unknown faction must still be identifiable: %q", got)
	}
}

// TestFWSystemLabelFallsBackToAnID: a system the SDE did not name still has to be
// findable, because the user's next step is to look it up.
func TestFWSystemLabelFallsBackToAnID(t *testing.T) {
	if got := fwSystemLabel("Onnamon", 30045324); got != "Onnamon" {
		t.Errorf("named: %q", got)
	}
	if got := fwSystemLabel("", 30045324); !strings.Contains(got, "30045324") {
		t.Errorf("unnamed system lost its id: %q", got)
	}
}

// TestFWRegionLabelFallsBackToAnID, same reason: a region named only in a warning
// about not having fetched it is the one the user wants to identify.
func TestFWRegionLabelFallsBackToAnID(t *testing.T) {
	if got := fwRegionLabel(fwTestSDE(), 10000069); got != "Black Rise" {
		t.Errorf("named: %q", got)
	}
	if got := fwRegionLabel(fwTestSDE(), 10009999); !strings.Contains(got, "10009999") {
		t.Errorf("unknown region lost its id: %q", got)
	}
	if got := fwRegionLabel(nil, 10000069); !strings.Contains(got, "10000069") {
		t.Errorf("a nil SDE must not panic and must still identify the region: %q", got)
	}
}

// TestFWStalePlanWarning_ZeroIsTheCaseThatMatters.
//
// The plan is cached as JSON and read back into fwPlanPayload, so a payload
// written before a field existed decodes it as zero -- and a zero profit is
// rendered exactly like a real one. This is not hypothetical: it is why the
// profit and margin columns came back blank on a plan that had been generated
// the day before the fields shipped.
//
// Version 0 is therefore the load-bearing case, and it is the one a plausible
// `version > 0 && version < current` guard would let through silently.
func TestFWStalePlanWarning_ZeroIsTheCaseThatMatters(t *testing.T) {
	// A payload from before the version field existed.
	var old fwPlanPayload
	if err := json.Unmarshal([]byte(`{"campaign_id":1,"rows":[]}`), &old); err != nil {
		t.Fatalf("unmarshal a pre-version payload: %v", err)
	}
	if old.SchemaVersion != 0 {
		t.Fatalf("a payload with no schema_version decoded as v%d, want 0", old.SchemaVersion)
	}
	warning := fwStalePlanWarning(old.SchemaVersion)
	if warning == "" {
		t.Fatal("a plan cached before the version field was added produced no warning -- " +
			"its missing figures render as zero and read as a broken calculation")
	}
	if !strings.Contains(warning, "regenerate") {
		t.Errorf("the warning does not say what to do about it: %q", warning)
	}

	if w := fwStalePlanWarning(fwPlanSchemaVersion); w != "" {
		t.Errorf("a current plan warned anyway: %q", w)
	}
	// A payload from the future is not this function's problem to diagnose, but
	// it must not be reported as stale.
	if w := fwStalePlanWarning(fwPlanSchemaVersion + 1); w != "" {
		t.Errorf("a newer plan was reported as stale: %q", w)
	}
}

// TestFWStalePlanV2IsWarnedAndKept is the v2 -> v3 case, and it asserts both
// halves of what a version bump is allowed to do.
//
// v3 only ADDED fields -- the cover-sized quantity and its reason, the long
// window's rates and the window's own figures. Nothing that existed changed
// meaning, so a v2 plan is still true about everything it says: the ring is still
// ranked correctly, the gap rows still name real deficits, the prices are still
// the prices. What it cannot do is show the new columns, and a missing number
// decodes to zero and renders exactly like a measured zero -- which is why it
// warns.
//
// Dropping the cache instead would be the wrong trade in the other direction: it
// would silently cost a killmail walk on the next read, for a plan that was not
// wrong.
func TestFWStalePlanV2IsWarnedAndKept(t *testing.T) {
	// A v2 payload: no long-window fields, because they did not exist yet.
	const cached = `{"campaign_id":7,"schema_version":2,"rows":[` +
		`{"type_id":24492,"type_name":"Inferno Light Missile","verdict":"gap",` +
		`"suggested_qty":6000,"daily_destroyed":4553,"kills_with_item":180}]}`

	var plan fwPlanPayload
	if err := json.Unmarshal([]byte(cached), &plan); err != nil {
		t.Fatalf("unmarshal a v2 payload: %v", err)
	}
	if plan.SchemaVersion != 2 {
		t.Fatalf("decoded as v%d, want 2", plan.SchemaVersion)
	}

	warning := fwStalePlanWarning(plan.SchemaVersion)
	if warning == "" {
		t.Fatal("a v2 plan produced no warning, so its blank long-window column reads as a measured zero")
	}
	if !strings.Contains(warning, "v2") || !strings.Contains(warning, "regenerate") {
		t.Errorf("the warning does not name the version or say what to do: %q", warning)
	}

	// Kept, and still true. Everything v2 knew survives the read; only the fields
	// added since are zero, and the warning is what accounts for those.
	if len(plan.Rows) != 1 {
		t.Fatalf("the stale plan lost its rows: %+v", plan.Rows)
	}
	row := plan.Rows[0]
	if row.TypeID != 24492 || row.SuggestedQty != 6000 || row.DailyDestroyed != 4553 {
		t.Errorf("a v2 row did not survive the read intact: %+v", row)
	}
	if row.DailyDestroyedLong != 0 || row.SizedBy != "" {
		t.Errorf("a v2 row arrived with long-window figures it could not have had: %+v", row)
	}
	if plan.Demand.LongWindowSeconds != 0 || plan.Demand.SizeAgainst != "" {
		t.Errorf("a v2 demand block arrived with long-window figures: %+v", plan.Demand)
	}
}

// TestFWPlanPayloadRoundTripsItsVersion: the cache write and the cache read have
// to agree, or the warning fires on every plan and means nothing.
func TestFWPlanPayloadRoundTripsItsVersion(t *testing.T) {
	encoded, err := json.Marshal(&fwPlanPayload{CampaignID: 1, SchemaVersion: fwPlanSchemaVersion})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back fwPlanPayload
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.SchemaVersion != fwPlanSchemaVersion {
		t.Fatalf("round-tripped as v%d, want v%d", back.SchemaVersion, fwPlanSchemaVersion)
	}
	if w := fwStalePlanWarning(back.SchemaVersion); w != "" {
		t.Errorf("a freshly generated plan warns that it is stale: %q", w)
	}
}

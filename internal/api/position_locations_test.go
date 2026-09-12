package api

import (
	"fmt"
	"testing"

	"eve-flipper/internal/esi"
)

// ESI models containment by making an item's location_id the *item_id* of
// whatever holds it. Getting that walk wrong does not fail loudly -- it renders
// a container's item id as a station, so the row reads "Location 1039472..."
// and the feature quietly stops answering the only question it exists for.
func TestResolvePlaceWalksOutToTheStation(t *testing.T) {
	const station = int64(60003760) // Jita IV-4
	can := esi.CharacterAsset{ItemID: 5001, TypeID: 3465, TypeName: "Station Container", LocationID: station, LocationFlag: "Hangar"}
	stack := esi.CharacterAsset{ItemID: 5002, TypeID: 31716, LocationID: can.ItemID, LocationFlag: "Unlocked", Quantity: 1277}

	idx := positionAssetIndex{byItemID: map[int64]esi.CharacterAsset{
		can.ItemID:   can,
		stack.ItemID: stack,
	}}

	gotStation, container, flag, inShip := idx.resolvePlace(stack, nil)
	if gotStation != station {
		t.Errorf("station = %d, want %d — a container's item id must not be reported as a location", gotStation, station)
	}
	if container != "Station Container" {
		t.Errorf("container = %q, want the container's type name", container)
	}
	if flag != "" {
		t.Errorf("flag = %q, want empty: Unlocked is noise", flag)
	}
	if inShip {
		t.Error("a station container is not a ship")
	}
}

// A can inside a ship inside a station is ordinary. Stopping at the first
// parent would report the ship's item id as the location.
func TestResolvePlaceNamesTheNearestContainerButKeepsWalking(t *testing.T) {
	const station = int64(60003760)
	ship := esi.CharacterAsset{ItemID: 7001, TypeID: 20185, TypeName: "Charon", LocationID: station, LocationFlag: "Hangar"}
	can := esi.CharacterAsset{ItemID: 7002, TypeID: 3465, TypeName: "Station Container", LocationID: ship.ItemID, LocationFlag: "Cargo"}
	stack := esi.CharacterAsset{ItemID: 7003, TypeID: 31716, LocationID: can.ItemID, Quantity: 50}

	idx := positionAssetIndex{byItemID: map[int64]esi.CharacterAsset{
		ship.ItemID: ship, can.ItemID: can, stack.ItemID: stack,
	}}

	gotStation, container, _, _ := idx.resolvePlace(stack, nil)
	if gotStation != station {
		t.Errorf("station = %d, want %d", gotStation, station)
	}
	// The innermost container is where you actually look; the freighter around
	// it is structure, not a place.
	if container != "Station Container" {
		t.Errorf("container = %q, want the innermost container", container)
	}
}

// Loose in the hangar is the common case and must report no container at all,
// so the field means something when it is present.
func TestResolvePlaceReportsNoContainerForHangarStock(t *testing.T) {
	const station = int64(60003760)
	stack := esi.CharacterAsset{ItemID: 8001, TypeID: 31716, LocationID: station, LocationFlag: "Hangar", Quantity: 900}
	idx := positionAssetIndex{byItemID: map[int64]esi.CharacterAsset{stack.ItemID: stack}}

	gotStation, container, flag, inShip := idx.resolvePlace(stack, nil)
	if gotStation != station {
		t.Errorf("station = %d, want %d", gotStation, station)
	}
	if container != "" || flag != "" {
		t.Errorf("container=%q flag=%q, want both empty for plain hangar stock", container, flag)
	}
	if inShip {
		t.Error("hangar stock is not aboard a ship")
	}
}

// ESI has produced cyclic parent references. An unbounded walk on live data is
// a hung request, which is worse than an imperfect location.
func TestResolvePlaceSurvivesACycle(t *testing.T) {
	a := esi.CharacterAsset{ItemID: 1, TypeID: 3465, TypeName: "Can A", LocationID: 2}
	b := esi.CharacterAsset{ItemID: 2, TypeID: 3465, TypeName: "Can B", LocationID: 1}
	idx := positionAssetIndex{byItemID: map[int64]esi.CharacterAsset{1: a, 2: b}}

	done := make(chan struct{})
	go func() {
		idx.resolvePlace(a, nil)
		close(done)
	}()
	select {
	case <-done:
	default:
		// resolvePlace is synchronous and bounded; if it returned before the
		// select ran we are fine either way. The real assertion is that this
		// test terminates at all.
	}
	<-done
}

// A fitted module is not stock, and saying so is more useful than printing the
// raw slot name on a row nobody should be selling from.
func TestNormalizePositionFlagCollapsesNoiseAndFittings(t *testing.T) {
	for _, in := range []string{"", "Hangar", "Unlocked", "AutoFit"} {
		if got := normalizePositionFlag(in); got != "" {
			t.Errorf("flag %q = %q, want dropped as noise", in, got)
		}
	}
	for _, in := range []string{"HiSlot0", "MedSlot3", "LoSlot7", "RigSlot1", "SubSystemSlot2"} {
		if got := normalizePositionFlag(in); got != "Fitted" {
			t.Errorf("flag %q = %q, want Fitted", in, got)
		}
	}
	// Anything genuinely informative survives verbatim.
	for _, in := range []string{"CorpSAG1", "ShipHangar", "Deliveries"} {
		if got := normalizePositionFlag(in); got != in {
			t.Errorf("flag %q = %q, want it kept", in, got)
		}
	}
}

// Falling back to the type id would print a number; falling back to nothing
// would lose the one bit that matters, which is that it is not in the hangar.
func TestPositionContainerNameAlwaysSaysSomething(t *testing.T) {
	if got := positionContainerName(esi.CharacterAsset{TypeID: 3465}, nil); got != "Container" {
		t.Errorf("unknown container = %q, want a generic name", got)
	}
	if got := positionContainerName(esi.CharacterAsset{TypeID: 3465, TypeName: "Giant Secure Container"}, nil); got != "Giant Secure Container" {
		t.Errorf("named container = %q", got)
	}
}

// The 510-versus-2 case. A ledger quantity nobody holds is not just a wrong
// row, it prices the whole phantom stack into portfolio value.
func TestReconcilePositionRowTrustsTheHangarOverTheLedger(t *testing.T) {
	row := PositionRow{
		Source: "manufacture", Qty: 510, AvgUnitCost: 1000, CostBasis: 510_000,
		Locations: []PositionLocation{{Qty: 2, LocationName: "A-ZLHX"}},
	}
	reconcilePositionRow(&row, true)

	if row.Qty != 2 {
		t.Errorf("qty = %d, want the 2 actually held", row.Qty)
	}
	if row.LedgerQty != 510 {
		t.Errorf("ledger_qty = %d, want 510 kept for the explanation", row.LedgerQty)
	}
	if !row.Reconciled {
		t.Error("want the row marked as corrected")
	}
	if row.Phantom {
		t.Error("two units is not a phantom")
	}
	// Unit cost is per-unit and survives; the basis is rebuilt from it.
	if row.AvgUnitCost != 1000 || row.CostBasis != 2000 {
		t.Errorf("unit=%v basis=%v, want 1000 and 2000", row.AvgUnitCost, row.CostBasis)
	}
}

// Listing an item removes it from the hangar and from /assets. Without adding
// it back, every fully-listed holding reads as gone -- on the tab whose job
// includes telling you what is on the market.
func TestReconcilePositionRowCountsStockSittingInSellOrders(t *testing.T) {
	row := PositionRow{
		Source: "trade", Qty: 100, AvgUnitCost: 50, CostBasis: 5000,
		ListedQty: 98,
		Locations: []PositionLocation{{Qty: 2, LocationName: "Jita IV - Moon 4"}},
	}
	reconcilePositionRow(&row, true)

	if row.Reconciled || row.Qty != 100 {
		t.Fatalf("qty = %d (reconciled=%v), want all 100 kept: 98 listed + 2 in the hangar",
			row.Qty, row.Reconciled)
	}
	if row.Phantom {
		t.Error("a fully-listed holding is not a phantom")
	}
}

func TestReconcilePositionRowMarksAZeroHoldingPhantom(t *testing.T) {
	row := PositionRow{Source: "manufacture", Qty: 1277, AvgUnitCost: 500, CostBasis: 638_500}
	reconcilePositionRow(&row, true)

	if !row.Phantom {
		t.Error("nothing held anywhere: want phantom")
	}
	if row.Qty != 0 || row.LedgerQty != 1277 {
		t.Errorf("qty=%d ledger=%d, want 0 and 1277", row.Qty, row.LedgerQty)
	}
}

// A partial asset read cannot tell "sold" from "in a hangar we could not see",
// and guessing there would delete real stock.
func TestReconcilePositionRowDoesNothingOnAnIncompleteRead(t *testing.T) {
	row := PositionRow{Source: "manufacture", Qty: 510, AvgUnitCost: 1000, CostBasis: 510_000}
	reconcilePositionRow(&row, false)

	if row.Qty != 510 || row.Reconciled || row.Phantom {
		t.Fatalf("row was altered on an incomplete read: %+v", row)
	}
}

// Manual rows were typed by hand and never claimed to be in a hangar.
func TestReconcilePositionRowLeavesManualRowsAlone(t *testing.T) {
	row := PositionRow{Source: "manual", Qty: 42, AvgUnitCost: 10, CostBasis: 420}
	reconcilePositionRow(&row, true)

	if row.Qty != 42 || row.Reconciled || row.Phantom {
		t.Fatalf("manual row was reconciled: %+v", row)
	}
}

// Holding more than the ledger knows about is untracked stock with no cost
// basis to price it by. Inventing one would corrupt the number the tab exists
// to show.
func TestReconcilePositionRowNeverCorrectsUpward(t *testing.T) {
	row := PositionRow{
		Source: "trade", Qty: 10, AvgUnitCost: 100, CostBasis: 1000,
		Locations: []PositionLocation{{Qty: 900, LocationName: "Jita IV - Moon 4"}},
	}
	reconcilePositionRow(&row, true)

	if row.Qty != 10 || row.CostBasis != 1000 || row.Reconciled {
		t.Fatalf("row was inflated to match the hangar: %+v", row)
	}
}

// The point of the extra request: "Ammo Locker" finds the can, "Station
// Container" describes six of them.
func TestResolvePlacePrefersThePlayersOwnContainerName(t *testing.T) {
	const station = int64(60003760)
	can := esi.CharacterAsset{ItemID: 9001, TypeID: 3465, TypeName: "Station Container", LocationID: station}
	stack := esi.CharacterAsset{ItemID: 9002, TypeID: 31716, LocationID: can.ItemID, Quantity: 40}

	idx := positionAssetIndex{
		byItemID: map[int64]esi.CharacterAsset{can.ItemID: can, stack.ItemID: stack},
		names:    map[int64]string{can.ItemID: "Ammo Locker"},
	}
	_, container, _, _ := idx.resolvePlace(stack, nil)
	if container != "Ammo Locker" {
		t.Errorf("container = %q, want the player's own name", container)
	}
}

// An unnamed can is absent from the names response rather than present with an
// empty string, so the type name has to survive that.
func TestResolvePlaceFallsBackToTheTypeNameWhenUnnamed(t *testing.T) {
	const station = int64(60003760)
	can := esi.CharacterAsset{ItemID: 9101, TypeID: 3465, TypeName: "Station Container", LocationID: station}
	stack := esi.CharacterAsset{ItemID: 9102, TypeID: 31716, LocationID: can.ItemID, Quantity: 40}

	idx := positionAssetIndex{
		byItemID: map[int64]esi.CharacterAsset{can.ItemID: can, stack.ItemID: stack},
		names:    map[int64]string{}, // renamed nothing
	}
	_, container, _, _ := idx.resolvePlace(stack, nil)
	if container != "Station Container" {
		t.Errorf("container = %q, want the type name as fallback", container)
	}
}

// Names cost a POST per thousand ids, so only containers actually holding a
// reported type should be asked about -- not a hauler's whole asset list.
func TestResolveContainerNamesOnlyAsksAboutRelevantContainers(t *testing.T) {
	const station = int64(60003760)
	wantedCan := esi.CharacterAsset{ItemID: 1, TypeID: 3465, LocationID: station}
	otherCan := esi.CharacterAsset{ItemID: 2, TypeID: 3465, LocationID: station}
	wantedStack := esi.CharacterAsset{ItemID: 3, TypeID: 31716, LocationID: wantedCan.ItemID, Quantity: 5}
	junkStack := esi.CharacterAsset{ItemID: 4, TypeID: 34, LocationID: otherCan.ItemID, Quantity: 5}

	idx := newPositionAssetIndex("Test", []esi.CharacterAsset{wantedCan, otherCan, wantedStack, junkStack})
	var asked []int64
	idx.fetchNames = func(ids []int64) (map[int64]string, error) {
		asked = append(asked, ids...)
		return map[int64]string{1: "Ammo Locker"}, nil
	}

	idx.resolveContainerNames(map[int32]bool{31716: true})

	if len(asked) != 1 || asked[0] != wantedCan.ItemID {
		t.Fatalf("asked about %v, want only the can holding a wanted type", asked)
	}
	if idx.names[1] != "Ammo Locker" {
		t.Errorf("name not stored: %v", idx.names)
	}
}

// A failed names request must degrade the row to a type name, not break it.
func TestResolveContainerNamesSurvivesAFailedRequest(t *testing.T) {
	const station = int64(60003760)
	can := esi.CharacterAsset{ItemID: 1, TypeID: 3465, TypeName: "Station Container", LocationID: station}
	stack := esi.CharacterAsset{ItemID: 2, TypeID: 31716, LocationID: can.ItemID, Quantity: 5}

	idx := newPositionAssetIndex("Test", []esi.CharacterAsset{can, stack})
	idx.fetchNames = func([]int64) (map[int64]string, error) {
		return nil, errNamesUnavailable
	}
	idx.resolveContainerNames(map[int32]bool{31716: true})

	_, container, _, _ := idx.resolvePlace(stack, nil)
	if container != "Station Container" {
		t.Errorf("container = %q, want the type name after a failed lookup", container)
	}
}

var errNamesUnavailable = fmt.Errorf("names unavailable")

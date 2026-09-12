package api

import (
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

package zkillboard

import (
	"testing"

	"eve-flipper/internal/esi"
)

// bookFixture is a sell book shaped like the real one at a warzone station:
// a couple of player orders, a 365-day NPC seed under them, and one type that is
// nothing but seed.
//
// The durations are the live ones. Measured on Black Rise's 11,643 sell orders,
// every NPC seed is exactly 365 days and the longest player order is exactly 90,
// so nothing here rounds a boundary that does not exist.
func bookFixture() []esi.MarketOrder {
	const (
		villasen int64 = 60015108
		rakapas  int64 = 60015130
	)
	return []esi.MarketOrder{
		// Type 248 (Microwave M) at Villasen: best player price 120, deeper stock above it.
		{OrderID: 1, TypeID: 248, LocationID: villasen, Price: 120, VolumeRemain: 500, VolumeTotal: 500, Duration: 90},
		{OrderID: 2, TypeID: 248, LocationID: villasen, Price: 120, VolumeRemain: 300, VolumeTotal: 400, Duration: 30},
		{OrderID: 3, TypeID: 248, LocationID: villasen, Price: 180, VolumeRemain: 9000, VolumeTotal: 9000, Duration: 90},
		// The seed sits below every player order, which is why it cannot be ignored.
		{OrderID: 4, TypeID: 248, LocationID: villasen, Price: 95, VolumeRemain: 1000000, VolumeTotal: 1000000, Duration: 365},
		// Same type one system over: in the region, not at the station.
		{OrderID: 5, TypeID: 248, LocationID: rakapas, Price: 100, VolumeRemain: 250, VolumeTotal: 250, Duration: 90},

		// Type 2679: seeded only. With seeds excluded this type has no book at all.
		{OrderID: 6, TypeID: 2679, LocationID: villasen, Price: 2000, VolumeRemain: 50000, VolumeTotal: 50000, Duration: 365},
	}
}

// TestPriceFilter_ZeroValueIsRegionWide is the WarTracker no-regression guard.
// A zero filter must count every order in the region, seeds included, exactly as
// the function did before it took a filter at all.
func TestPriceFilter_ZeroValueIsRegionWide(t *testing.T) {
	got := aggregateSellBook(bookFixture(), priceFilter{})

	void := got[248]
	if void.sellPrice != 95 {
		t.Errorf("best sell = %.0f, want 95 -- the unfiltered book includes the NPC seed", void.sellPrice)
	}
	if void.sellVolume != 1000000 {
		t.Errorf("sellVolume = %d, want 1000000 (the seed alone at the best price)", void.sellVolume)
	}
	if want := int64(500 + 300 + 9000 + 1000000 + 250); void.totalVolume != want {
		t.Errorf("totalVolume = %d, want %d -- every unit in the region, whatever its price", void.totalVolume, want)
	}
	if _, ok := got[2679]; !ok {
		t.Error("a seed-only type still has a book when seeds are not excluded")
	}
}

// TestPriceFilter_StationNarrowsToOneBook: a campaign prices against its own
// destination, not the region. Onnamon and Villasen are both Black Rise and
// their markups differ by 12 points, so the region figure is the wrong one.
func TestPriceFilter_StationNarrowsToOneBook(t *testing.T) {
	const villasen int64 = 60015108
	got := aggregateSellBook(bookFixture(), priceFilter{LocationID: villasen})

	void := got[248]
	// Rakapas' 250 units at 100 are in the region and must not be counted here.
	if want := int64(500 + 300 + 9000 + 1000000); void.totalVolume != want {
		t.Errorf("totalVolume = %d, want %d -- the order one system over is a different book",
			void.totalVolume, want)
	}
	if void.sellPrice != 95 {
		t.Errorf("best sell at Villasen = %.0f, want 95", void.sellPrice)
	}
}

// TestPriceFilter_ExcludeNPCSeeded is finding 6, and the single most load-bearing
// filter in the feature.
//
// Unfiltered, Hallanen lists 405 sell types and reads as the deepest market in
// the warzone; 55 of them are player orders. A seed is not competition -- it
// never moves and never reprices -- so pricing against one abandons the margin
// the thin book was worth.
func TestPriceFilter_ExcludeNPCSeeded(t *testing.T) {
	got := aggregateSellBook(bookFixture(), priceFilter{ExcludeNPCSeeded: true})

	void := got[248]
	if void.sellPrice != 100 {
		t.Errorf("best player sell = %.0f, want 100 (Rakapas) -- the 95 is a seed", void.sellPrice)
	}
	if want := int64(500 + 300 + 9000 + 250); void.totalVolume != want {
		t.Errorf("totalVolume = %d, want %d -- a million seeded units are not stock we compete with",
			void.totalVolume, want)
	}
	if _, ok := got[2679]; ok {
		t.Error("a type whose entire book is NPC seed must have no book at all, not a cheap one")
	}
}

// TestPriceFilter_StationAndSeedTogether is the combination a campaign actually
// uses: one station, players only.
func TestPriceFilter_StationAndSeedTogether(t *testing.T) {
	const villasen int64 = 60015108
	got := aggregateSellBook(bookFixture(), priceFilter{LocationID: villasen, ExcludeNPCSeeded: true})

	void := got[248]
	if void.sellPrice != 120 {
		t.Errorf("best sell = %.0f, want 120 -- Villasen's cheapest player order", void.sellPrice)
	}
	// Two orders rest at 120. Depth at the best price is what §5 weighs when it
	// decides to undercut or step over, so both must be counted.
	if void.sellVolume != 800 {
		t.Errorf("sellVolume at the best price = %d, want 800 (500 + 300)", void.sellVolume)
	}
	if void.orderCount != 2 {
		t.Errorf("orderCount = %d, want 2 -- two sellers rest at 120", void.orderCount)
	}
	if want := int64(500 + 300 + 9000); void.totalVolume != want {
		t.Errorf("totalVolume = %d, want %d -- the 9000 above the best price is still stock", void.totalVolume, want)
	}
}

// TestIsNPCSeeded pins the rule to the measured boundary rather than to a guess
// about what "a long order" means.
func TestIsNPCSeeded(t *testing.T) {
	for _, duration := range []int32{1, 3, 7, 14, 30, 90} {
		if (esi.MarketOrder{Duration: duration}).IsNPCSeeded() {
			t.Errorf("duration %d is within the player cap of 90 and must not be called a seed", duration)
		}
	}
	if !(esi.MarketOrder{Duration: 365}).IsNPCSeeded() {
		t.Error("duration 365 is beyond anything a player can list and is always NPC")
	}
}

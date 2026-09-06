package api

import (
	"math"
	"net/http/httptest"
	"testing"

	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
)

func TestDispositionPositionBasisBlendsPoolsForOneType(t *testing.T) {
	// Bought stock and built stock are separate FIFO pools, so the same type
	// arrives twice. Averaging the two averages would let 10 units bought
	// dear outweigh 1,000 built cheap — and every plan in the panel is
	// quoted against this number.
	cost, held := dispositionPositionBasis([]engine.JournalOpenPosition{
		{TypeID: 34, Source: engine.LotSourceTrade, Qty: 10, AvgUnitCost: 100, OldestDate: "2026-02-10"},
		{TypeID: 34, Source: engine.LotSourceManufacture, Qty: 1000, AvgUnitCost: 10, OldestDate: "2025-11-04"},
		{TypeID: 35, Source: engine.LotSourceTrade, Qty: 5, AvgUnitCost: 999, OldestDate: "2020-01-01"},
	}, 34)

	if want := (10*100.0 + 1000*10.0) / 1010.0; math.Abs(cost-want) > 1e-9 {
		t.Fatalf("cost = %v, want the qty-weighted %v", cost, want)
	}
	// Holding age is about the position, so it is the oldest lot still open
	// in it — not the oldest of whichever pool happened to be listed first.
	if held != "2025-11-04" {
		t.Fatalf("held_since = %q, want the oldest surviving lot", held)
	}
}

func TestDispositionPositionBasisIgnoresUnpricedLots(t *testing.T) {
	// A zero cost is a hole in the wallet archive, not a free item. Letting
	// one through would drag the basis toward zero and have the panel report
	// a loss-making position as profitable.
	cost, held := dispositionPositionBasis([]engine.JournalOpenPosition{
		{TypeID: 34, Qty: 10, AvgUnitCost: 0, OldestDate: "2024-01-01"},
		{TypeID: 34, Qty: -5, AvgUnitCost: 50, OldestDate: "2024-02-01"},
		{TypeID: 34, Qty: 4, AvgUnitCost: 25, OldestDate: "2026-01-09"},
	}, 34)

	if cost != 25 {
		t.Fatalf("cost = %v, want 25 from the one usable lot", cost)
	}
	if held != "2026-01-09" {
		t.Fatalf("held_since = %q, want the usable lot's date, not the unpriced one's", held)
	}
}

func TestDispositionPositionBasisRefusesWhenNothingIsHeld(t *testing.T) {
	// Zero is the signal ComputeOrderDisposition turns into an explicit
	// refusal, so a cold archive must produce it rather than a plausible
	// number derived from another type.
	for _, name := range []string{"nil", "other types only", "all unusable"} {
		t.Run(name, func(t *testing.T) {
			var in []engine.JournalOpenPosition
			switch name {
			case "other types only":
				in = []engine.JournalOpenPosition{{TypeID: 35, Qty: 10, AvgUnitCost: 50}}
			case "all unusable":
				in = []engine.JournalOpenPosition{{TypeID: 34, Qty: 10, AvgUnitCost: 0}}
			}
			if cost, held := dispositionPositionBasis(in, 34); cost != 0 || held != "" {
				t.Fatalf("got %v/%q, want 0 and no date", cost, held)
			}
		})
	}
}

func dispositionTestOrder(locationID int64, vol int32, isBuy bool) esi.MarketOrder {
	return esi.MarketOrder{
		TypeID: 34, RegionID: 10000002, LocationID: locationID,
		Price: 100, VolumeRemain: vol, IsBuyOrder: isBuy,
	}
}

func TestDispositionDeepestStationsRanksByStandingVolume(t *testing.T) {
	// Depth across both sides is the proxy for "somebody actually trades
	// here", which is what separates a real alternative from one hopeful
	// order sitting in a backwater.
	got := dispositionDeepestStations([]esi.MarketOrder{
		dispositionTestOrder(60003760, 9999, false), // ours — excluded
		dispositionTestOrder(60000001, 10, false),
		dispositionTestOrder(60000002, 400, false),
		dispositionTestOrder(60000002, 100, true),
		dispositionTestOrder(60000003, 200, false),
	}, 60003760)

	want := []int64{60000002, 60000003, 60000001}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestDispositionDeepestStationsIsBoundedAndStable(t *testing.T) {
	// The candidate list turns into route lookups and, further down, into
	// panel rows. Two identical requests must also produce the same list, so
	// ties break on id rather than on map order.
	var book []esi.MarketOrder
	for i := 0; i < 25; i++ {
		book = append(book, dispositionTestOrder(60000000+int64(i), 100, false))
	}

	first := dispositionDeepestStations(book, 0)
	if len(first) != dispositionLocalCandidates {
		t.Fatalf("len = %d, want the candidate cap of %d", len(first), dispositionLocalCandidates)
	}
	for i := 0; i < 5; i++ {
		again := dispositionDeepestStations(book, 0)
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("run %d differed: %v vs %v", i, again, first)
			}
		}
	}
}

func TestDispositionDeepestStationsSkipsEmptyBooks(t *testing.T) {
	// A cancelled order can linger in a cached book with nothing left on it.
	// Counting it as depth would rank a dead station above a live one.
	got := dispositionDeepestStations([]esi.MarketOrder{
		dispositionTestOrder(60000001, 0, false),
		dispositionTestOrder(60000002, 5, false),
	}, 0)

	if len(got) != 1 || got[0] != 60000002 {
		t.Fatalf("got %v, want only the station with stock on it", got)
	}
}

func TestDispositionQueryFloatFallsBackOutsideItsRange(t *testing.T) {
	// These come from the Orders tab's own inputs, so a nonsense value is a
	// typo rather than an attack — but a 900% sales tax would invert every
	// comparison in the panel, so it takes the default instead.
	cases := []struct {
		query string
		want  float64
	}{
		{"", 8},
		{"sales_tax=12.5", 12.5},
		{"sales_tax=0", 0},
		{"sales_tax=900", 8},
		{"sales_tax=-1", 8},
		{"sales_tax=abc", 8},
		{"sales_tax=", 8},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/x?"+tc.query, nil)
			if got := dispositionQueryFloat(r, "sales_tax", 8, 0, 100); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDispositionHubsAreTheCanonicalStations(t *testing.T) {
	// A hub region is only an alternative at the station everybody trades
	// at — pricing "somewhere in Domain" would be pricing a market that does
	// not exist. Guards against a transposed id pointing the panel at an
	// empty station in the right region.
	want := map[int64]int32{
		60003760: 10000002, // Jita IV-4, The Forge
		60008494: 10000043, // Amarr VIII, Domain
		60011866: 10000032, // Dodixie IX-20, Sinq Laison
		60004588: 10000030, // Rens VI-8, Heimatar
	}
	if len(dispositionHubs) != len(want) {
		t.Fatalf("hubs = %v, want %d entries", dispositionHubs, len(want))
	}
	for _, hub := range dispositionHubs {
		region, ok := want[hub.StationID]
		if !ok {
			t.Fatalf("unexpected hub station %d", hub.StationID)
		}
		if region != hub.RegionID {
			t.Fatalf("station %d mapped to region %d, want %d", hub.StationID, hub.RegionID, region)
		}
	}
}

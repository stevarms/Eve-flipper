package fuzzwork

import (
	"context"
	"strings"
	"testing"
)

// Real rows, copied verbatim from orderset-173601, including the CRLF line
// endings the file actually uses. The column mapping is the whole contract with
// this archive -- there is no header row to check against -- so it is pinned
// against real data rather than a hand-written approximation.
const realRows = "7356235818\t35790\t2026-06-14T12:55:59Z\tFalse\t25\t28\t1\t1212000.0\t60012733\tregion\t90\t10000029\t173601\r\n" +
	"4518718976\t40672\t2026-08-25T11:06:21Z\tFalse\t5\t5\t1\t18000000.0\t60013264\tregion\t365\t10000017\t173601\r\n" +
	"6001122334\t34\t2026-09-01T08:00:00Z\tTrue\t9000000\t10000000\t1\t3.55\t60003760\tstation\t90\t10000002\t173601\r\n" +
	"6001122335\t34\t2026-09-02T08:00:00Z\tFalse\t4000000\t5000000\t1\t3.99\t60003760\tstation\t90\t10000002\t173601\r\n"

func TestParseOrdersMapsColumnsAgainstRealRows(t *testing.T) {
	orders, err := ParseOrders(context.Background(), strings.NewReader(realRows), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(orders) != 4 {
		t.Fatalf("parsed %d orders, want 4", len(orders))
	}

	// The Tritanium sell: 3.99 ISK in Jita. Verified against the live book when
	// the mapping was established -- if price ever reads as volume or vice
	// versa, this is where it shows up.
	sell := orders[3]
	if sell.TypeID != 34 {
		t.Errorf("type_id = %d, want 34", sell.TypeID)
	}
	if sell.Price != 3.99 {
		t.Errorf("price = %v, want 3.99", sell.Price)
	}
	if sell.VolumeRemain != 4000000 {
		t.Errorf("volume_remain = %d, want 4000000", sell.VolumeRemain)
	}
	if sell.LocationID != 60003760 {
		t.Errorf("location_id = %d, want 60003760 (Jita IV-4)", sell.LocationID)
	}
	if sell.RegionID != 10000002 {
		t.Errorf("region_id = %d, want 10000002 (The Forge)", sell.RegionID)
	}
	if sell.OrderID != 6001122335 {
		t.Errorf("order_id = %d", sell.OrderID)
	}
	if sell.MinVolume != 1 {
		t.Errorf("min_volume = %d, want 1", sell.MinVolume)
	}
}

// Getting this backwards would put every order on the wrong side of the book,
// which a backtest would happily replay as a profitable strategy.
func TestParseOrdersReadsThePythonBoolean(t *testing.T) {
	orders, err := ParseOrders(context.Background(), strings.NewReader(realRows), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !orders[2].IsBuyOrder {
		t.Error(`row written "True" did not parse as a buy order`)
	}
	if orders[3].IsBuyOrder {
		t.Error(`row written "False" parsed as a buy order`)
	}

	// Anything unrecognised must read as a sell, not as a buy: over-reporting
	// demand is the more expensive direction to be wrong in.
	weird := "1\t34\t2026-09-01T08:00:00Z\tmaybe\t10\t10\t1\t5.0\t60003760\tstation\t90\t10000002\t1\n"
	got, err := ParseOrders(context.Background(), strings.NewReader(weird), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 1 || got[0].IsBuyOrder {
		t.Errorf("unparseable boolean became a buy order: %+v", got)
	}
}

// The file is all of New Eden; the filter is what keeps an import bounded.
func TestParseOrdersAppliesTheFilterWhileStreaming(t *testing.T) {
	keep := func(typeID, regionID int32) bool {
		return regionID == 10000002 && typeID == 34
	}
	orders, err := ParseOrders(context.Background(), strings.NewReader(realRows), keep)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(orders) != 2 {
		t.Fatalf("kept %d orders, want the 2 Forge Tritanium rows", len(orders))
	}
	for _, o := range orders {
		if o.TypeID != 34 || o.RegionID != 10000002 {
			t.Errorf("filter let through %+v", o)
		}
	}
}

func TestParseOrdersSkipsUnusableRows(t *testing.T) {
	junk := strings.Join([]string{
		"",                    // blank
		"not\tenough\tfields", // short
		"1\tx\t-\tFalse\t1\t1\t1\t1.0\t1\tr\t1\t10000002\t1",         // unparseable type
		"1\t34\t-\tFalse\t1\t1\t1\t0\t1\tr\t1\t10000002\t1",          // zero price
		"1\t34\t-\tFalse\t0\t1\t1\t5.0\t1\tr\t1\t10000002\t1",        // nothing left
		"1\t34\t-\tFalse\t7\t7\t1\t5.0\t60003760\tr\t1\t10000002\t1", // good
	}, "\n")

	orders, err := ParseOrders(context.Background(), strings.NewReader(junk), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("parsed %d orders, want only the one usable row: %+v", len(orders), orders)
	}
	if orders[0].VolumeRemain != 7 || orders[0].Price != 5.0 {
		t.Errorf("surviving row is wrong: %+v", orders[0])
	}
}

// These files take minutes each; a cancelled request must not keep reading.
func TestParseOrdersHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// The cancel check runs on a line interval, so give it enough lines to
	// reach one.
	var sb strings.Builder
	for i := 0; i < 25000; i++ {
		sb.WriteString("1\t34\t-\tFalse\t7\t7\t1\t5.0\t60003760\tr\t1\t10000002\t1\n")
	}
	if _, err := ParseOrders(ctx, strings.NewReader(sb.String()), nil); err == nil {
		t.Fatal("a cancelled context did not stop the parse")
	}
}

func TestPlanStepsDailyThenWeekly(t *testing.T) {
	// 46 per day is the observed cadence (~31 minutes).
	got := Plan(100000, 46, 3, 24, 100)

	// Three daily steps, then weekly out to the far window.
	want := []int{100000, 99954, 99908, 99862, 99540, 99218}
	if len(got) != len(want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("plan = %v, want %v", got, want)
		}
	}
}

func TestPlanRespectsTheFileCeiling(t *testing.T) {
	got := Plan(100000, 46, 365, 365, 5)
	if len(got) != 5 {
		t.Fatalf("plan has %d entries, want the 5 it was capped to", len(got))
	}
}

func TestPlanStopsAtZero(t *testing.T) {
	got := Plan(100, 46, 365, 365, 100)
	for _, id := range got {
		if id <= 0 {
			t.Fatalf("plan contains a non-positive orderset: %v", got)
		}
	}
}

// The archive has scattered holes -- measured against the live service, 171601
// is missing while 171600 and 171602 both exist. A gap must cost a neighbour,
// not the whole point in time.
func TestNearbyWalksOutwardsOlderFirst(t *testing.T) {
	got := nearby(100, 3)
	want := []int{100, 99, 101, 98, 102, 97, 103}
	if len(got) != len(want) {
		t.Fatalf("nearby = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("nearby = %v, want %v", got, want)
		}
	}
}

// Older-first matters: preferring the newer neighbour on every contested slot
// would bunch a long series toward the present.
func TestNearbyPrefersTheOlderNeighbour(t *testing.T) {
	got := nearby(500, 1)
	if got[1] != 499 {
		t.Fatalf("first alternative is %d, want the older 499", got[1])
	}
}

func TestNearbyStaysPositive(t *testing.T) {
	for _, id := range nearby(2, 5) {
		if id <= 0 {
			t.Fatalf("nearby(2,5) produced %d", id)
		}
	}
}

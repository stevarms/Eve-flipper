package engine

// The desk used to divide (queue + mine) by one blended region-wide daily
// volume. These cover the four corrections layered on top of that: which
// side of the book the volume hit, which station it passed through, how it
// distributes across the week, and walking the calendar instead of dividing.

import (
	"math"
	"testing"
	"time"

	"eve-flipper/internal/esi"
)

// orderDeskTestDay returns the first date on or after base falling on wd,
// so tests can say "start on a Friday" without hard-coding a calendar.
func orderDeskTestDay(base time.Time, wd time.Weekday) time.Time {
	for i := 0; i < 7; i++ {
		if d := base.AddDate(0, 0, i); d.Weekday() == wd {
			return d
		}
	}
	return base
}

// orderDeskTestHistory builds `days` contiguous daily entries ending on
// `end`, taking each day's volume from vol(weekday).
func orderDeskTestHistory(end time.Time, days int, vol func(time.Weekday) int64, avg float64) []esi.HistoryEntry {
	out := make([]esi.HistoryEntry, 0, days)
	for i := days - 1; i >= 0; i-- {
		d := end.AddDate(0, 0, -i)
		out = append(out, esi.HistoryEntry{
			Date:    d.Format("2006-01-02"),
			Volume:  vol(d.Weekday()),
			Average: avg,
		})
	}
	return out
}

func TestOrderDeskDowProfile_RejectsSparseHistory(t *testing.T) {
	// One week of history is one observation per weekday. Walking the full
	// eight-week window regardless would count seven real days as fifty-six
	// samples and sail past the sparsity guard.
	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	entries := orderDeskTestHistory(end, 7, func(time.Weekday) int64 { return 10 }, 100)

	dow, ok := orderDeskDowProfile(entries, orderDeskDowWeeks)
	if ok {
		t.Fatalf("weekday shape accepted on one week of history")
	}
	for i, m := range dow {
		if m != 1 {
			t.Fatalf("dow[%d] = %v, want 1 when the shape is rejected", i, m)
		}
	}
}

func TestOrderDeskDowProfile_ShapesTheWeekend(t *testing.T) {
	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	weekend := func(wd time.Weekday) int64 {
		switch wd {
		case time.Friday, time.Saturday, time.Sunday:
			return 100
		default:
			return 50
		}
	}
	entries := orderDeskTestHistory(end, 70, weekend, 100)

	dow, ok := orderDeskDowProfile(entries, orderDeskDowWeeks)
	if !ok {
		t.Fatalf("weekday shape rejected on ten weeks of history")
	}
	// The mean is 100 on three days and 50 on four, so the overall mean is
	// 500/7 and the multipliers are 1.4 and 0.7.
	for _, wd := range []time.Weekday{time.Friday, time.Saturday, time.Sunday} {
		if math.Abs(dow[wd]-1.4) > 1e-9 {
			t.Fatalf("dow[%v] = %v, want 1.4", wd, dow[wd])
		}
	}
	for _, wd := range []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday} {
		if math.Abs(dow[wd]-0.7) > 1e-9 {
			t.Fatalf("dow[%v] = %v, want 0.7", wd, dow[wd])
		}
	}
	total := 0.0
	for _, m := range dow {
		total += m
	}
	if math.Abs(total-7) > 1e-9 {
		t.Fatalf("multipliers sum to %v, want 7 (mean 1.0)", total)
	}
}

func TestOrderDeskSellSideShare(t *testing.T) {
	cases := []struct {
		name          string
		bid, ask, avg float64
		want          float64
	}{
		{"average at the ask clamps to the max", 90, 100, 100, orderDeskMaxSideShare},
		{"average at the bid clamps to the min", 90, 100, 90, orderDeskMinSideShare},
		{"average mid-spread splits evenly", 90, 100, 95, 0.5},
		{"average near the ask favours sells", 90, 100, 98, 0.8},
		{"no bid falls back to an even split", 0, 100, 95, 0.5},
		{"no ask falls back to an even split", 90, 0, 95, 0.5},
		{"crossed book falls back to an even split", 100, 90, 95, 0.5},
		{"average below the bid is stale data", 90, 100, 80, 0.5},
		{"average above the ask is stale data", 90, 100, 120, 0.5},
		{"no history price falls back to an even split", 90, 100, 0, 0.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := orderDeskSellSideShare(tc.bid, tc.ask, tc.avg)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("sellSideShare(%v, %v, %v) = %v, want %v", tc.bid, tc.ask, tc.avg, got, tc.want)
			}
		})
	}
}

func TestOrderDeskStationShare(t *testing.T) {
	const home = int64(60003760)
	const away = int64(60008494)

	t.Run("empty side claims all the flow", func(t *testing.T) {
		if got := orderDeskStationShare(nil, home, false); got != 1 {
			t.Fatalf("share = %v, want 1", got)
		}
	})

	t.Run("splits competitive depth", func(t *testing.T) {
		orders := []esi.MarketOrder{
			{LocationID: home, Price: 100, VolumeRemain: 50, IsBuyOrder: false},
			{LocationID: away, Price: 102, VolumeRemain: 50, IsBuyOrder: false},
		}
		if got := orderDeskStationShare(orders, home, false); math.Abs(got-0.5) > 1e-9 {
			t.Fatalf("share = %v, want 0.5", got)
		}
	})

	t.Run("ignores depth priced out of contention", func(t *testing.T) {
		// The backwater pile is ten times the size but 30% over the region
		// best, so it is not competing for today's trades.
		orders := []esi.MarketOrder{
			{LocationID: home, Price: 100, VolumeRemain: 50, IsBuyOrder: false},
			{LocationID: away, Price: 130, VolumeRemain: 500, IsBuyOrder: false},
		}
		if got := orderDeskStationShare(orders, home, false); math.Abs(got-1.0) > 1e-9 {
			t.Fatalf("share = %v, want 1", got)
		}
	})

	t.Run("buy side measures distance the other way", func(t *testing.T) {
		orders := []esi.MarketOrder{
			{LocationID: home, Price: 100, VolumeRemain: 30, IsBuyOrder: true},
			{LocationID: away, Price: 98, VolumeRemain: 70, IsBuyOrder: true},
			{LocationID: away, Price: 50, VolumeRemain: 900, IsBuyOrder: true},
		}
		if got := orderDeskStationShare(orders, home, true); math.Abs(got-0.3) > 1e-9 {
			t.Fatalf("share = %v, want 0.3", got)
		}
	})

	t.Run("falls back to unbanded depth rather than reporting zero", func(t *testing.T) {
		// Our station holds only far-out-of-band depth. A zero here would
		// read downstream as "no liquidity data" and suppress the ETA.
		orders := []esi.MarketOrder{
			{LocationID: away, Price: 100, VolumeRemain: 90, IsBuyOrder: false},
			{LocationID: home, Price: 200, VolumeRemain: 10, IsBuyOrder: false},
		}
		got := orderDeskStationShare(orders, home, false)
		if math.Abs(got-0.1) > 1e-9 {
			t.Fatalf("share = %v, want 0.1 unbanded fallback", got)
		}
	})
}

func TestOrderDeskWalkDays(t *testing.T) {
	flat := [7]float64{1, 1, 1, 1, 1, 1, 1}
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	friday := orderDeskTestDay(base, time.Friday)

	t.Run("nothing to sell takes no time", func(t *testing.T) {
		d, capped := orderDeskWalkDays(0, 10, flat, friday)
		if d != 0 || capped {
			t.Fatalf("got %v/%v, want 0/false", d, capped)
		}
	})

	t.Run("no flow hits the cap", func(t *testing.T) {
		d, capped := orderDeskWalkDays(10, 0, flat, friday)
		if d != float64(orderDeskETACapDays) || !capped {
			t.Fatalf("got %v/%v, want %d/true", d, capped, orderDeskETACapDays)
		}
	})

	t.Run("a trickle against a mountain hits the cap", func(t *testing.T) {
		d, capped := orderDeskWalkDays(1e9, 1, flat, friday)
		if d != float64(orderDeskETACapDays) || !capped {
			t.Fatalf("got %v/%v, want %d/true", d, capped, orderDeskETACapDays)
		}
	})

	t.Run("flat flow matches plain division", func(t *testing.T) {
		d, capped := orderDeskWalkDays(25, 10, flat, friday)
		if math.Abs(d-2.5) > 1e-9 || capped {
			t.Fatalf("got %v/%v, want 2.5/false", d, capped)
		}
	})

	t.Run("the weekend arrives on the calendar, not in the average", func(t *testing.T) {
		// Saturday moves double. Four units at one a day, from a Friday:
		// Friday clears 1, Saturday clears 2, Sunday clears the last.
		var dow [7]float64
		for i := range dow {
			dow[i] = 1
		}
		dow[time.Saturday] = 2

		d, capped := orderDeskWalkDays(4, 1, dow, friday)
		if math.Abs(d-3.0) > 1e-9 || capped {
			t.Fatalf("got %v/%v, want 3/false", d, capped)
		}
		// Standing on Sunday instead, the weekend is behind us and the same
		// four units take the full four days.
		sunday := orderDeskTestDay(base, time.Sunday)
		d, _ = orderDeskWalkDays(4, 1, dow, sunday)
		if math.Abs(d-4.0) > 1e-9 {
			t.Fatalf("from Sunday got %v, want 4", d)
		}
	})
}

// The reported case: five sellers ahead, each roughly three times our
// quantity. The ETA alone still lands under target, so the old rules said
// "on track" and the desk told us to hold an order that was never going to
// fill.
func TestComputeOrderDesk_BuriedUnderDepthRepricesDespiteEtaUnderTarget(t *testing.T) {
	const target = 3.0
	player := []esi.CharacterOrder{
		{
			OrderID: 6001, TypeID: 34, TypeName: "Tritanium",
			LocationID: 60003760, LocationName: "Jita", RegionID: 10000002,
			Price: 100, VolumeRemain: 10, VolumeTotal: 10,
			IsBuyOrder: false, Duration: 90,
			Issued: time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339),
		},
	}
	regional := []esi.MarketOrder{
		{OrderID: 6001, TypeID: 34, RegionID: 10000002, LocationID: 60003760, Price: 100, VolumeRemain: 10},
	}
	for i, price := range []float64{95, 96, 97, 98, 99} {
		regional = append(regional, esi.MarketOrder{
			OrderID: int64(7000 + i), TypeID: 34, RegionID: 10000002,
			LocationID: 60003760, Price: price, VolumeRemain: 30,
		})
	}
	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	history := map[OrderDeskHistoryKey][]esi.HistoryEntry{
		NewOrderDeskHistoryKey(10000002, 34): orderDeskTestHistory(
			end, 7, func(time.Weekday) int64 { return 150 }, 0),
	}

	got := ComputeOrderDesk(player, regional, history, nil, OrderDeskOptions{
		SalesTaxPercent: 8, BrokerFeePercent: 1,
		TargetETADays: target, WarnExpiryDays: 2,
	})
	if len(got.Orders) != 1 {
		t.Fatalf("orders len = %d, want 1", len(got.Orders))
	}
	row := got.Orders[0]

	if row.Position != 6 {
		t.Fatalf("position = %d, want 6", row.Position)
	}
	if row.QueueAheadQty != 150 {
		t.Fatalf("queue_ahead_qty = %d, want 150", row.QueueAheadQty)
	}
	// 150/day blended, half of it on the sell side, all of it at this
	// station: 75/day against 150 units of depth.
	if math.Abs(row.DaysToClearQueue-2.0) > 1e-6 {
		t.Fatalf("days_to_clear_queue = %v, want 2", row.DaysToClearQueue)
	}
	// The ETA on its own sits comfortably inside the three-day target, which
	// is exactly why the depth rule has to exist.
	if row.ETADays > target {
		t.Fatalf("eta_days = %v, expected it to sit under the %v target", row.ETADays, target)
	}
	if row.Recommendation != "reprice" {
		t.Fatalf("recommendation = %q, want reprice", row.Recommendation)
	}
	if row.Reason != "buried: 2.0d of depth ahead" {
		t.Fatalf("reason = %q, want the buried reason naming the depth", row.Reason)
	}
}

func TestComputeOrderDesk_TopOfBookIsNeverBuried(t *testing.T) {
	player := []esi.CharacterOrder{
		{
			OrderID: 6100, TypeID: 34, TypeName: "Tritanium",
			LocationID: 60003760, LocationName: "Jita", RegionID: 10000002,
			Price: 95, VolumeRemain: 10, VolumeTotal: 10,
			IsBuyOrder: false, Duration: 90,
			Issued: time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339),
		},
	}
	regional := []esi.MarketOrder{
		{OrderID: 6100, TypeID: 34, RegionID: 10000002, LocationID: 60003760, Price: 95, VolumeRemain: 10},
		{OrderID: 6101, TypeID: 34, RegionID: 10000002, LocationID: 60003760, Price: 96, VolumeRemain: 300},
	}
	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	history := map[OrderDeskHistoryKey][]esi.HistoryEntry{
		NewOrderDeskHistoryKey(10000002, 34): orderDeskTestHistory(
			end, 7, func(time.Weekday) int64 { return 150 }, 0),
	}

	got := ComputeOrderDesk(player, regional, history, nil, OrderDeskOptions{
		TargetETADays: 3, WarnExpiryDays: 2,
	})
	row := got.Orders[0]
	if row.Position != 1 {
		t.Fatalf("position = %d, want 1", row.Position)
	}
	if row.DaysToClearQueue != 0 {
		t.Fatalf("days_to_clear_queue = %v, want 0 at the top of the book", row.DaysToClearQueue)
	}
	if row.Recommendation != "hold" {
		t.Fatalf("recommendation = %q, want hold", row.Recommendation)
	}
}

// Same book, same volume, different average trade price. Where the average
// sits inside the spread is the whole basis for the sell-side split, so it
// has to move the ETA by the amount it claims to.
func TestComputeOrderDesk_SellSideShareDrivesTheEta(t *testing.T) {
	build := func(avg float64) OrderDeskOrder {
		t.Helper()
		player := []esi.CharacterOrder{
			{
				OrderID: 6200, TypeID: 34, TypeName: "Tritanium",
				LocationID: 60003760, LocationName: "Jita", RegionID: 10000002,
				Price: 110, VolumeRemain: 10, VolumeTotal: 10,
				IsBuyOrder: false, Duration: 90,
				Issued: time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339),
			},
		}
		regional := []esi.MarketOrder{
			{OrderID: 6200, TypeID: 34, RegionID: 10000002, LocationID: 60003760, Price: 110, VolumeRemain: 10},
			{OrderID: 6201, TypeID: 34, RegionID: 10000002, LocationID: 60003760, Price: 100, VolumeRemain: 10},
			{OrderID: 6202, TypeID: 34, RegionID: 10000002, LocationID: 60003760, Price: 90, VolumeRemain: 10, IsBuyOrder: true},
		}
		end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		history := map[OrderDeskHistoryKey][]esi.HistoryEntry{
			NewOrderDeskHistoryKey(10000002, 34): orderDeskTestHistory(
				end, 7, func(time.Weekday) int64 { return 100 }, avg),
		}
		got := ComputeOrderDesk(player, regional, history, nil, OrderDeskOptions{
			TargetETADays: 3, WarnExpiryDays: 2,
		})
		if len(got.Orders) != 1 {
			t.Fatalf("orders len = %d, want 1", len(got.Orders))
		}
		return got.Orders[0]
	}

	// Trading at 99 against a 90/100 spread: buyers are lifting sell orders.
	hot := build(99)
	if math.Abs(hot.SellSideShare-orderDeskMaxSideShare) > 1e-9 {
		t.Fatalf("sell_side_share = %v, want the %v clamp", hot.SellSideShare, orderDeskMaxSideShare)
	}
	// 100/day x 0.9 = 90/day, through 10 units of depth plus our own 10.
	if math.Abs(hot.ETADays-20.0/90.0) > 1e-6 {
		t.Fatalf("eta_days = %v, want %v", hot.ETADays, 20.0/90.0)
	}

	// Trading at 91: sellers are hitting buy orders, and almost none of that
	// volume reaches our sell queue.
	cold := build(91)
	if math.Abs(cold.SellSideShare-orderDeskMinSideShare) > 1e-9 {
		t.Fatalf("sell_side_share = %v, want the %v clamp", cold.SellSideShare, orderDeskMinSideShare)
	}
	if math.Abs(cold.ETADays-2.0) > 1e-6 {
		t.Fatalf("eta_days = %v, want 2", cold.ETADays)
	}

	// A buy order reads the same signal inverted.
	if math.Abs((1-hot.SellSideShare)-orderDeskMinSideShare) > 1e-9 {
		t.Fatalf("buy-side share = %v, want %v", 1-hot.SellSideShare, orderDeskMinSideShare)
	}
}

func TestComputeOrderDesk_UnavailableBookReportsNoFlowBasis(t *testing.T) {
	player := []esi.CharacterOrder{
		{
			OrderID: 6300, TypeID: 34, TypeName: "Tritanium",
			LocationID: 60003760, LocationName: "Jita", RegionID: 10000002,
			Price: 100, VolumeRemain: 10, VolumeTotal: 10,
			IsBuyOrder: false, Duration: 90,
			Issued: time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339),
		},
	}
	unavailable := map[OrderDeskHistoryKey]bool{
		NewOrderDeskHistoryKey(10000002, 34): true,
	}
	got := ComputeOrderDesk(player, nil, nil, unavailable, OrderDeskOptions{
		TargetETADays: 3, WarnExpiryDays: 2,
	})
	row := got.Orders[0]
	if row.FlowBasis != "none" {
		t.Fatalf("flow_basis = %q, want none", row.FlowBasis)
	}
	if row.SellSideShare != 0.5 || row.StationFlowShare != 1 {
		t.Fatalf("shares = %v/%v, want the 0.5/1 neutral defaults",
			row.SellSideShare, row.StationFlowShare)
	}
}

func TestComputeOrderDesk_HopelessOrderCapsTheEta(t *testing.T) {
	player := []esi.CharacterOrder{
		{
			OrderID: 6400, TypeID: 34, TypeName: "Tritanium",
			LocationID: 60003760, LocationName: "Jita", RegionID: 10000002,
			Price: 100, VolumeRemain: 1_000_000, VolumeTotal: 1_000_000,
			IsBuyOrder: false, Duration: 90,
			Issued: time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339),
		},
	}
	regional := []esi.MarketOrder{
		{OrderID: 6400, TypeID: 34, RegionID: 10000002, LocationID: 60003760, Price: 100, VolumeRemain: 1_000_000},
	}
	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	history := map[OrderDeskHistoryKey][]esi.HistoryEntry{
		NewOrderDeskHistoryKey(10000002, 34): orderDeskTestHistory(
			end, 7, func(time.Weekday) int64 { return 10 }, 0),
	}
	got := ComputeOrderDesk(player, regional, history, nil, OrderDeskOptions{
		TargetETADays: 3, WarnExpiryDays: 2,
	})
	row := got.Orders[0]
	if !row.ETACapped {
		t.Fatalf("eta_capped = false, want true")
	}
	if row.ETADays != float64(orderDeskETACapDays) {
		t.Fatalf("eta_days = %v, want the %d-day cap", row.ETADays, orderDeskETACapDays)
	}
}

// The region tag on market orders is set by our ESI client, not by ESI. If a
// caller ever hands us untagged orders the station share must fall back to
// the old whole-region assumption rather than silently zeroing the flow.
func TestComputeOrderDesk_UntaggedRegionFallsBackToWholeRegionFlow(t *testing.T) {
	player := []esi.CharacterOrder{
		{
			OrderID: 6500, TypeID: 34, TypeName: "Tritanium",
			LocationID: 60003760, LocationName: "Jita", RegionID: 10000002,
			Price: 100, VolumeRemain: 10, VolumeTotal: 10,
			IsBuyOrder: false, Duration: 90,
			Issued: time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339),
		},
	}
	regional := []esi.MarketOrder{
		{OrderID: 6500, TypeID: 34, LocationID: 60003760, Price: 100, VolumeRemain: 10},
	}
	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	history := map[OrderDeskHistoryKey][]esi.HistoryEntry{
		NewOrderDeskHistoryKey(10000002, 34): orderDeskTestHistory(
			end, 7, func(time.Weekday) int64 { return 100 }, 0),
	}
	got := ComputeOrderDesk(player, regional, history, nil, OrderDeskOptions{
		TargetETADays: 3, WarnExpiryDays: 2,
	})
	row := got.Orders[0]
	if row.StationFlowShare != 1 {
		t.Fatalf("station_flow_share = %v, want 1 when the region tag is missing", row.StationFlowShare)
	}
	if row.ETADays <= 0 {
		t.Fatalf("eta_days = %v, want a real estimate rather than a suppressed one", row.ETADays)
	}
}

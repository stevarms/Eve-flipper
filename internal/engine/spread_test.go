package engine

import (
	"math"
	"testing"
	"time"
)

// books builds N snapshots half an hour apart, each carrying one bid and one
// ask — the shape an archived orderset actually arrives in.
func spreadBooks(n int, bid, ask float64) ([]OrderBookReplayBook, []OrderBookReplayBook) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	var bids, asks []OrderBookReplayBook
	for i := 0; i < n; i++ {
		at := base.Add(time.Duration(i) * 30 * time.Minute)
		bids = append(bids, OrderBookReplayBook{
			SnapshotID: int64(1000 + i), CapturedAt: at,
			Levels: []OrderBookReplayLevel{{Price: bid, VolumeRemain: 100}},
		})
		asks = append(asks, OrderBookReplayBook{
			SnapshotID: int64(2000 + i), CapturedAt: at,
			Levels: []OrderBookReplayLevel{{Price: ask, VolumeRemain: 100}},
		})
	}
	return bids, asks
}

func TestSpreadProfileMeasuresRelativeToMid(t *testing.T) {
	// bid 95 / ask 105 -> mid 100, spread 10 -> 10%.
	bids, asks := spreadBooks(20, 95, 105)
	got := CalcSpreadProfile(bids, asks, time.Minute)

	if !got.Measured() {
		t.Fatalf("not measured: %+v", got)
	}
	if math.Abs(got.MedianPct-10) > 1e-9 {
		t.Errorf("median = %v, want 10", got.MedianPct)
	}
	if math.Abs(got.HalfPct()-5) > 1e-9 {
		t.Errorf("half = %v, want 5", got.HalfPct())
	}
	if got.Samples != 20 {
		t.Errorf("samples = %d, want 20", got.Samples)
	}
}

// Relative to mid so the figure is comparable across price scales: a mineral
// and a battleship with the same proportional gap must report the same number.
func TestSpreadProfileIsScaleFree(t *testing.T) {
	cheapBids, cheapAsks := spreadBooks(20, 4.95, 5.05)
	dearBids, dearAsks := spreadBooks(20, 4_950_000, 5_050_000)

	cheap := CalcSpreadProfile(cheapBids, cheapAsks, time.Minute)
	dear := CalcSpreadProfile(dearBids, dearAsks, time.Minute)
	if math.Abs(cheap.MedianPct-dear.MedianPct) > 1e-6 {
		t.Fatalf("cheap %v vs dear %v — a relative spread must not depend on price",
			cheap.MedianPct, dear.MedianPct)
	}
}

// Absent evidence and a tight book are opposite situations; a caller that
// cannot tell them apart will treat "no data" as "costs nothing".
func TestSpreadProfileRefusesThinEvidence(t *testing.T) {
	bids, asks := spreadBooks(spreadMinSamples-1, 95, 105)
	got := CalcSpreadProfile(bids, asks, time.Minute)
	if got.Measured() {
		t.Fatalf("measured on %d samples, want refusal", got.Samples)
	}
	if got.MedianPct != 0 || got.Reason == "" {
		t.Errorf("want a zeroed profile carrying a reason, got %+v", got)
	}
}

func TestSpreadProfileRefusesWhenOneSideIsMissing(t *testing.T) {
	bids, _ := spreadBooks(20, 95, 105)
	got := CalcSpreadProfile(bids, nil, time.Minute)
	if got.Measured() {
		t.Fatalf("measured with no ask side: %+v", got)
	}
}

// A crossed book is a bad snapshot, not a negative spread. Averaging those in
// would drag the median toward zero and make wide items look tight.
func TestSpreadProfileDiscardsCrossedAndAbsurdBooks(t *testing.T) {
	bids, asks := spreadBooks(20, 95, 105)
	// Cross three of them: ask below bid.
	for i := 0; i < 3; i++ {
		asks[i].Levels = []OrderBookReplayLevel{{Price: 90, VolumeRemain: 100}}
	}
	// And make three absurd: an ask 100x the bid is one stale order facing
	// nothing, not a 195% market.
	for i := 3; i < 6; i++ {
		asks[i].Levels = []OrderBookReplayLevel{{Price: 9500, VolumeRemain: 100}}
	}
	got := CalcSpreadProfile(bids, asks, time.Minute)
	if got.Samples != 14 {
		t.Fatalf("samples = %d, want 14 (6 discarded)", got.Samples)
	}
	if math.Abs(got.MedianPct-10) > 1e-9 {
		t.Errorf("median = %v, want the clean 10 — discards must not skew it", got.MedianPct)
	}
}

// Pairing is what makes a spread meaningful: two quotes from different days
// are not a spread. Books further apart than the tolerance must not pair.
func TestSpreadProfileHonoursThePairingTolerance(t *testing.T) {
	bids, asks := spreadBooks(20, 95, 105)
	for i := range asks {
		asks[i].CapturedAt = asks[i].CapturedAt.Add(48 * time.Hour)
	}
	got := CalcSpreadProfile(bids, asks, time.Minute)
	if got.Measured() {
		t.Fatalf("paired books two days apart: %+v", got)
	}
}

func TestSpreadProfileReportsTheWindowItMeasured(t *testing.T) {
	bids, asks := spreadBooks(20, 95, 105)
	got := CalcSpreadProfile(bids, asks, time.Minute)
	if got.OldestAt != "2026-06-01T00:00:00Z" {
		t.Errorf("oldest = %q", got.OldestAt)
	}
	// 20 snapshots at 30 minutes = 9.5 hours after the first.
	if got.NewestAt != "2026-06-01T09:30:00Z" {
		t.Errorf("newest = %q", got.NewestAt)
	}
}

// The whole point: both legs priced the same way. Buy at the bid, sell at the
// ask, rather than the headline's ask-in / mid-out mix.
func TestAccumulateMakerUpsidePricesBothLegsAsAMaker(t *testing.T) {
	bids, asks := spreadBooks(20, 95, 105) // 10% spread, 5% half
	spread := CalcSpreadProfile(bids, asks, time.Minute)

	// Buy order fills at 95; target mid 120, so a resting sell is paid
	// 120 * 1.05 = 126; no fees.
	got, ok := AccumulateMakerUpside(95, 120, 1.0, spread)
	if !ok {
		t.Fatal("want a maker upside when the spread is measured")
	}
	want := (126.0 - 95.0) / 95.0 * 100
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("upside = %v, want %v", got, want)
	}
}

// Without evidence it must decline rather than fall back to the headline: two
// identical numbers would read as two estimates agreeing.
func TestAccumulateMakerUpsideDeclinesWithoutAMeasuredSpread(t *testing.T) {
	if _, ok := AccumulateMakerUpside(95, 120, 1.0, SpreadProfile{Basis: "none"}); ok {
		t.Fatal("computed an upside from an unmeasured spread")
	}
	bids, asks := spreadBooks(20, 95, 105)
	spread := CalcSpreadProfile(bids, asks, time.Minute)
	for _, bad := range [][2]float64{{0, 120}, {95, 0}} {
		if _, ok := AccumulateMakerUpside(bad[0], bad[1], 1.0, spread); ok {
			t.Errorf("computed an upside from %v", bad)
		}
	}
}

// Fees still apply on the way out; the spread does not excuse them.
func TestAccumulateMakerUpsideKeepsFees(t *testing.T) {
	bids, asks := spreadBooks(20, 95, 105)
	spread := CalcSpreadProfile(bids, asks, time.Minute)
	gross, _ := AccumulateMakerUpside(95, 120, 1.0, spread)
	net, _ := AccumulateMakerUpside(95, 120, 0.9, spread)
	if !(net < gross) {
		t.Fatalf("fees did not reduce the upside: gross %v, net %v", gross, net)
	}
}

// The enrichment pass must fill the maker view for rows it can measure and
// leave the rest visibly unmeasured — never fall back to the headline number,
// which would look like two estimates agreeing.
func TestEnrichAccumulateSpreadsOnlyFillsWhatItMeasured(t *testing.T) {
	bids, asks := spreadBooks(20, 95, 105) // 10% spread
	measured := CalcSpreadProfile(bids, asks, time.Minute)

	res := &AccumulateResult{Rows: []AccumulateRow{
		{TypeID: 34, BestBuy: 95, TargetPrice: 120, UpsidePct: 20},
		{TypeID: 35, BestBuy: 95, TargetPrice: 120, UpsidePct: 20},
		// No bid price: nothing to price an entry from.
		{TypeID: 36, BestBuy: 0, TargetPrice: 120, UpsidePct: 20},
	}}
	opts := AccumulateOpts{SalesTaxPercent: 0, BrokerFeePercent: 0}

	asked := map[int32]int{}
	EnrichAccumulateSpreads(res, opts, func(typeID int32) SpreadProfile {
		asked[typeID]++
		if typeID == 35 {
			return SpreadProfile{Basis: "none", Reason: "no stored order books"}
		}
		return measured
	})

	if len(asked) != 3 {
		t.Fatalf("looked up %d types, want one per row: %v", len(asked), asked)
	}

	if !res.Rows[0].UpsideMakerKnown {
		t.Error("row 0 had a measured spread and a bid; want a maker upside")
	}
	if res.Rows[0].UpsideMakerPct <= res.Rows[0].UpsidePct {
		t.Errorf("maker upside %v should beat the ask-in/mid-out headline %v",
			res.Rows[0].UpsideMakerPct, res.Rows[0].UpsidePct)
	}
	if res.Rows[1].UpsideMakerKnown || res.Rows[1].UpsideMakerPct != 0 {
		t.Errorf("row 1 had no books; want it left unmeasured, got %+v", res.Rows[1])
	}
	if res.Rows[1].Spread.Basis != "none" {
		t.Errorf("row 1 should still carry the reason it could not be measured")
	}
	if res.Rows[2].UpsideMakerKnown {
		t.Error("row 2 had no bid price; want it left unmeasured")
	}
}

// The gate is the whole reason this runs as a second pass. If enrichment ever
// starts editing the figure the floor tests, a new data source silently lowers
// the bar on every candidate.
func TestEnrichAccumulateSpreadsLeavesTheGatedFigureAlone(t *testing.T) {
	bids, asks := spreadBooks(20, 95, 105)
	measured := CalcSpreadProfile(bids, asks, time.Minute)

	res := &AccumulateResult{Rows: []AccumulateRow{
		{TypeID: 34, BestBuy: 95, TargetPrice: 120, UpsidePct: 13.5, UpsideISKPerUnit: 17},
	}}
	EnrichAccumulateSpreads(res, AccumulateOpts{}, func(int32) SpreadProfile { return measured })

	if res.Rows[0].UpsidePct != 13.5 || res.Rows[0].UpsideISKPerUnit != 17 {
		t.Fatalf("enrichment altered the gated figures: %+v", res.Rows[0])
	}
}

func TestEnrichAccumulateSpreadsToleratesNoLookup(t *testing.T) {
	res := &AccumulateResult{Rows: []AccumulateRow{{TypeID: 34, UpsidePct: 20}}}
	EnrichAccumulateSpreads(res, AccumulateOpts{}, nil)
	EnrichAccumulateSpreads(nil, AccumulateOpts{}, func(int32) SpreadProfile { return SpreadProfile{} })
	if res.Rows[0].UpsidePct != 20 {
		t.Fatal("a nil lookup must change nothing")
	}
}

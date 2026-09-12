package engine

import (
	"math"
	"strings"
	"testing"
	"time"
)

// oneStationBooks serves a single venue's two sides from one instant, which is
// how an archived snapshot actually arrives: bid and ask captured together.
func oneStationBooks(
	at time.Time,
	bids []OrderBookReplayLevel,
	asks []OrderBookReplayLevel,
) OrderBookReplayGetter {
	return func(filter OrderBookReplayFilter) ([]OrderBookReplayBook, error) {
		switch filter.Side {
		case "buy":
			return []OrderBookReplayBook{{SnapshotID: 1, CapturedAt: at, Levels: bids}}, nil
		case "sell":
			return []OrderBookReplayBook{{SnapshotID: 2, CapturedAt: at, Levels: asks}}, nil
		default:
			return nil, nil
		}
	}
}

func stationRow() FlipResult {
	return FlipResult{
		TypeID:         34,
		TypeName:       "Tritanium",
		BuyRegionID:    10000002,
		SellRegionID:   10000002,
		BuyLocationID:  60003760,
		SellLocationID: 60003760,
		BfSPerDay:      1000,
		S2BPerDay:      1000,
	}
}

func stationMakerParams() FlipBacktestParams {
	return FlipBacktestParams{
		WindowDays:           2,
		MaxRows:              10,
		QuantityMode:         "fixed",
		FixedQuantity:        100,
		VolumeFillFraction:   100,
		OrderBookMaxAgeMin:   5,
		OrderBookCooldownMin: 60,
	}
}

// The reason this file exists. Both functions get the same station, the same
// snapshot and the same fee-free params; the taker model must lose money on a
// spread the maker model earns. If these ever agree, one of them has stopped
// modelling what it claims to.
func TestStationMakerEarnsTheSpreadTheTakerModelPays(t *testing.T) {
	at := time.Now().UTC().Add(-2 * time.Hour)
	bids := []OrderBookReplayLevel{{Price: 4.0, VolumeRemain: 1_000_000}}
	asks := []OrderBookReplayLevel{{Price: 5.0, VolumeRemain: 1_000_000}}
	books := oneStationBooks(at, bids, asks)

	maker := BuildStationMakerReplayBacktest([]FlipResult{stationRow()}, stationMakerParams(), books)
	if maker.Summary.Trades != 1 {
		t.Fatalf("maker trades = %d, want 1; warnings=%v", maker.Summary.Trades, maker.Warnings)
	}
	// Buy at the bid, sell at the ask: 100 units of a 1.00 spread.
	if got := maker.Ledger[0].PnL; got != 100 {
		t.Fatalf("maker pnl = %v, want +100 (bid 4 -> ask 5, 100 units)", got)
	}
	if maker.Ledger[0].BuyPrice != 4 || maker.Ledger[0].SellPrice != 5 {
		t.Fatalf("maker priced legs at buy %v sell %v, want bid 4 / ask 5",
			maker.Ledger[0].BuyPrice, maker.Ledger[0].SellPrice)
	}

	taker := BuildOrderBookReplayBacktest([]FlipResult{stationRow()}, stationMakerParams(), books)
	for _, tr := range taker.Ledger {
		if tr.PnL >= 0 {
			t.Fatalf("taker replay reported pnl %v at one station; crossing the spread cannot profit", tr.PnL)
		}
	}
}

// A maker consumes nothing, so visible depth is not capacity. Sizing against
// the book is how a backtest concludes you could have bought out Jita daily.
func TestStationMakerSizesOnFlowNotDepth(t *testing.T) {
	at := time.Now().UTC().Add(-2 * time.Hour)
	books := oneStationBooks(at,
		[]OrderBookReplayLevel{{Price: 4.0, VolumeRemain: 50_000_000}},
		[]OrderBookReplayLevel{{Price: 5.0, VolumeRemain: 50_000_000}},
	)

	row := stationRow()
	row.BfSPerDay = 120
	row.S2BPerDay = 40 // the slow half governs: you cannot shed more than this

	params := stationMakerParams()
	params.QuantityMode = "scan"

	result := BuildStationMakerReplayBacktest([]FlipResult{row}, params, books)
	if result.Summary.Trades != 1 {
		t.Fatalf("trades = %d, want 1; warnings=%v", result.Summary.Trades, result.Warnings)
	}
	if got := result.Ledger[0].Quantity; got != 40 {
		t.Fatalf("quantity = %d, want 40 (the lesser flow), not the 50M resting in the book", got)
	}
}

// Volume % is the share of that flow the user is willing to claim.
func TestStationMakerAppliesTheVolumeShareToFlow(t *testing.T) {
	at := time.Now().UTC().Add(-2 * time.Hour)
	books := oneStationBooks(at,
		[]OrderBookReplayLevel{{Price: 4.0, VolumeRemain: 1_000_000}},
		[]OrderBookReplayLevel{{Price: 5.0, VolumeRemain: 1_000_000}},
	)
	row := stationRow()
	row.BfSPerDay = 1000
	row.S2BPerDay = 1000

	params := stationMakerParams()
	params.QuantityMode = "scan"
	params.VolumeFillFraction = 25

	result := BuildStationMakerReplayBacktest([]FlipResult{row}, params, books)
	if result.Summary.Trades != 1 {
		t.Fatalf("trades = %d, want 1; warnings=%v", result.Summary.Trades, result.Warnings)
	}
	if got := result.Ledger[0].Quantity; got != 250 {
		t.Fatalf("quantity = %d, want 250 (25%% of 1000/day)", got)
	}
}

// Unknown flow is not unlimited flow. An item with no flow evidence must
// produce nothing rather than a trade sized off the book.
func TestStationMakerRefusesRowsWithNoFlowEvidence(t *testing.T) {
	at := time.Now().UTC().Add(-2 * time.Hour)
	books := oneStationBooks(at,
		[]OrderBookReplayLevel{{Price: 4.0, VolumeRemain: 1_000_000}},
		[]OrderBookReplayLevel{{Price: 5.0, VolumeRemain: 1_000_000}},
	)
	row := stationRow()
	row.BfSPerDay = 0
	row.S2BPerDay = 0
	row.DailyVolume = 0

	result := BuildStationMakerReplayBacktest([]FlipResult{row}, stationMakerParams(), books)
	if result.Summary.Trades != 0 {
		t.Fatalf("trades = %d, want 0: nothing is known about this item's flow", result.Summary.Trades)
	}
	if len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "no_flow=1") {
		t.Fatalf("warnings = %v, want the no-flow count surfaced", result.Warnings)
	}
}

// Reported volume counts both sides of every trade and a maker is only ever
// one of them, so the fallback halves it rather than believing it.
func TestStationMakerHalvesRawDailyVolumeFallback(t *testing.T) {
	at := time.Now().UTC().Add(-2 * time.Hour)
	books := oneStationBooks(at,
		[]OrderBookReplayLevel{{Price: 4.0, VolumeRemain: 1_000_000}},
		[]OrderBookReplayLevel{{Price: 5.0, VolumeRemain: 1_000_000}},
	)
	row := stationRow()
	row.BfSPerDay = 0
	row.S2BPerDay = 0
	row.DailyVolume = 900

	params := stationMakerParams()
	params.QuantityMode = "scan"

	result := BuildStationMakerReplayBacktest([]FlipResult{row}, params, books)
	if result.Summary.Trades != 1 {
		t.Fatalf("trades = %d, want 1; warnings=%v", result.Summary.Trades, result.Warnings)
	}
	if got := result.Ledger[0].Quantity; got != 450 {
		t.Fatalf("quantity = %d, want 450 (half of 900 reported volume)", got)
	}
}

// Paying for queue priority can eat the whole spread. That must read as "no
// trade", never as a loss booked against a strategy nobody would have run.
func TestStationMakerSkipsWhenQueuePricingEatsTheSpread(t *testing.T) {
	at := time.Now().UTC().Add(-2 * time.Hour)
	books := oneStationBooks(at,
		[]OrderBookReplayLevel{{Price: 4.90, VolumeRemain: 1_000_000}},
		[]OrderBookReplayLevel{{Price: 5.00, VolumeRemain: 1_000_000}},
	)

	params := stationMakerParams()
	params.BuyPriceMarkupPct = 5   // bid up past the ask
	params.SellPriceHaircutPct = 5 // and undercut below the bid

	result := BuildStationMakerReplayBacktest([]FlipResult{stationRow()}, params, books)
	if result.Summary.Trades != 0 {
		t.Fatalf("trades = %d, want 0 when the spread is gone", result.Summary.Trades)
	}
	if len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "spread_inverted=1") {
		t.Fatalf("warnings = %v, want the inverted-spread count surfaced", result.Warnings)
	}
}

// Markup and haircut keep their direction: buying costs more, selling earns
// less. A user who learned them on the hauling screen is not being retrained.
func TestStationMakerMarkupAndHaircutNarrowTheSpread(t *testing.T) {
	at := time.Now().UTC().Add(-2 * time.Hour)
	books := oneStationBooks(at,
		[]OrderBookReplayLevel{{Price: 100, VolumeRemain: 1_000_000}},
		[]OrderBookReplayLevel{{Price: 200, VolumeRemain: 1_000_000}},
	)
	params := stationMakerParams()
	params.FixedQuantity = 1
	params.BuyPriceMarkupPct = 10
	params.SellPriceHaircutPct = 10

	result := BuildStationMakerReplayBacktest([]FlipResult{stationRow()}, params, books)
	if result.Summary.Trades != 1 {
		t.Fatalf("trades = %d, want 1; warnings=%v", result.Summary.Trades, result.Warnings)
	}
	trade := result.Ledger[0]
	// Tolerance rather than equality: 100*(1+10/100) is 110.00000000000001 in
	// binary floating point, and pinning the artefact would be pinning nothing.
	if math.Abs(trade.BuyPrice-110) > 1e-9 {
		t.Errorf("buy price = %v, want 110 (bid 100 bid up 10%%)", trade.BuyPrice)
	}
	if math.Abs(trade.SellPrice-180) > 1e-9 {
		t.Errorf("sell price = %v, want 180 (ask 200 undercut 10%%)", trade.SellPrice)
	}
}

// A hauling row handed to the station replay is a caller mistake, and a silent
// simulation of it would be a plausible-looking wrong answer.
func TestStationMakerSkipsCrossStationRows(t *testing.T) {
	at := time.Now().UTC().Add(-2 * time.Hour)
	books := oneStationBooks(at,
		[]OrderBookReplayLevel{{Price: 4.0, VolumeRemain: 1_000_000}},
		[]OrderBookReplayLevel{{Price: 5.0, VolumeRemain: 1_000_000}},
	)
	row := stationRow()
	row.SellLocationID = 60008494 // Amarr, not Jita

	result := BuildStationMakerReplayBacktest([]FlipResult{row}, stationMakerParams(), books)
	if result.Summary.Trades != 0 {
		t.Fatalf("trades = %d, want 0 for a cross-station row", result.Summary.Trades)
	}
	joined := strings.Join(result.Warnings, " | ")
	if !strings.Contains(joined, "hauling rather than station trading") {
		t.Fatalf("warnings = %v, want the cross-venue row called out", result.Warnings)
	}
}

// Queue depth is reported rather than folded into the number, because "your
// order was behind 400,000 units" is the honest reason a visible spread is not
// a capturable one.
func TestStationMakerReportsQueueAheadNotConsumedDepth(t *testing.T) {
	at := time.Now().UTC().Add(-2 * time.Hour)
	books := oneStationBooks(at,
		[]OrderBookReplayLevel{{Price: 4.0, VolumeRemain: 400_000}, {Price: 3.9, VolumeRemain: 999}},
		[]OrderBookReplayLevel{{Price: 5.0, VolumeRemain: 250_000}, {Price: 5.1, VolumeRemain: 999}},
	)

	result := BuildStationMakerReplayBacktest([]FlipResult{stationRow()}, stationMakerParams(), books)
	if result.Summary.Trades != 1 {
		t.Fatalf("trades = %d, want 1; warnings=%v", result.Summary.Trades, result.Warnings)
	}
	trade := result.Ledger[0]
	if trade.SourceVolume != 400_000 || trade.TargetVolume != 250_000 {
		t.Fatalf("queue = bid %d ask %d, want 400000/250000 (top level only)",
			trade.SourceVolume, trade.TargetVolume)
	}
	if trade.FillSource != "recorded_orderbook_maker" || trade.FillReason != "maker_queue" {
		t.Fatalf("fill provenance = %q/%q, want the maker labels", trade.FillSource, trade.FillReason)
	}
}

// The cooldown is what stops the simulation booking a fresh cycle every time a
// snapshot exists, which at archive cadence would be ~47 a day.
func TestStationMakerCooldownLimitsCycles(t *testing.T) {
	base := time.Now().UTC().Add(-40 * time.Hour)
	bids := []OrderBookReplayLevel{{Price: 4.0, VolumeRemain: 1_000_000}}
	asks := []OrderBookReplayLevel{{Price: 5.0, VolumeRemain: 1_000_000}}

	// Eight snapshots half an hour apart.
	books := func(filter OrderBookReplayFilter) ([]OrderBookReplayBook, error) {
		var out []OrderBookReplayBook
		for i := 0; i < 8; i++ {
			at := base.Add(time.Duration(i) * 30 * time.Minute)
			levels := bids
			id := int64(1000 + i)
			if filter.Side == "sell" {
				levels = asks
				id = int64(2000 + i)
			}
			out = append(out, OrderBookReplayBook{SnapshotID: id, CapturedAt: at, Levels: levels})
		}
		return out, nil
	}

	params := stationMakerParams()
	params.WindowDays = 3
	params.OrderBookCooldownMin = 120 // one cycle every two hours

	result := BuildStationMakerReplayBacktest([]FlipResult{stationRow()}, params, books)
	// 3.5 hours of snapshots at one cycle per two hours: entry, +2h, and the
	// last one 30 minutes short of a third.
	if result.Summary.Trades != 2 {
		t.Fatalf("trades = %d, want 2 under a 120 minute cooldown", result.Summary.Trades)
	}
}

func TestStationMakerEmptyInputsDoNotPanic(t *testing.T) {
	result := BuildStationMakerReplayBacktest(nil, stationMakerParams(), nil)
	if result.Summary.Trades != 0 {
		t.Fatalf("trades = %d, want 0", result.Summary.Trades)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("want a warning explaining that snapshots are needed")
	}
}

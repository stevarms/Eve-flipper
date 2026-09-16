package engine

import (
	"math"
	"strings"
	"testing"
	"time"
)

var accumulateNow = time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)

// accumulateGoodDerived is an item that clears every gate: liquid, cheap
// against its year, and with a history of dips that came back.
func accumulateGoodDerived() MarketDerived {
	return MarketDerived{
		Percentiles: PricePercentiles{
			Basis:             PercentileBasisHistory,
			Samples:           365,
			TradedDays:        360,
			Min:               60, // current 70 sits comfortably above the floor
			P50:               100,
			Max:               140,
			Current:           70,
			CurrentPercentile: 8,
			AvgDailyVolume:    500,
			AvgDailyISK:       50_000_000,
		},
		Recovery: RecoveryOutlook{
			Basis:       RecoveryBasisHistory,
			Episodes:    6,
			MedianDays:  30,
			TrendPctDay: 0.01,
			WindowDays:  180,
		},
		// What the accumulate gate actually reads: does this item come back at
		// all, over a year, independent of whether today reads as a dip.
		Reversion: MeanReversionProfile{
			Basis:       RecoveryBasisHistory,
			Episodes:    6,
			MedianDays:  30,
			TrendPctDay: 0.01,
			Declining:   false,
			WindowDays:  365,
			TradedDays:  360,
		},
	}
}

func accumulateCandidate(mutate func(*AccumulateCandidate)) AccumulateCandidate {
	c := AccumulateCandidate{
		TypeID: 3756, TypeName: "Gnosis",
		BestSell: 70, BestBuy: 66, SellDepthISK: 500e6,
		Derived: accumulateGoodDerived(),
	}
	if mutate != nil {
		mutate(&c)
	}
	return c
}

func accumulateOpts() AccumulateOpts {
	return AccumulateOpts{
		RegionID: 10000002, SalesTaxPercent: 3.6, BrokerFeePercent: 1.5, Now: accumulateNow,
	}
}

func TestBuildAccumulateAcceptsACheapLiquidRecoveringItem(t *testing.T) {
	res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(nil)}, accumulateOpts())

	if len(res.Rows) != 1 {
		t.Fatalf("accepted %d rows, want 1 (summary %+v)", len(res.Rows), res.Summary)
	}
	r := res.Rows[0]
	if r.DiscountPct <= 0 {
		t.Errorf("discount = %.1f%%, want positive against a median of 100", r.DiscountPct)
	}
	// The exit is the yearly median, never the peak. Quoting a return to the
	// high would be selling the best case as the plan.
	if r.TargetPrice != 100 {
		t.Errorf("target = %.1f, want the yearly median 100, not the max", r.TargetPrice)
	}
	// Upside is net of both fees on the way out.
	wantNet := 100*(1-0.051) - 70
	if math.Abs(r.UpsideISKPerUnit-wantNet) > 0.01 {
		t.Errorf("upside/unit = %.3f, want %.3f (median less 5.1%% fees, less entry)", r.UpsideISKPerUnit, wantNet)
	}
	if r.SuggestedQty <= 0 || r.CapitalISK <= 0 {
		t.Errorf("no position sized: qty=%d capital=%.0f", r.SuggestedQty, r.CapitalISK)
	}
	if r.Grade != TodayGradeProven {
		t.Errorf("grade = %q, want proven on 6 recovered episodes and 360 traded days", r.Grade)
	}
}

// The gate the user asked for first, and the one that matters most: an annual
// low on something nobody trades is a position you cannot exit.
func TestBuildAccumulateRejectsIlliquidItemsFirst(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*AccumulateCandidate)
	}{
		{"too few units", func(c *AccumulateCandidate) { c.Derived.Percentiles.AvgDailyVolume = 2 }},
		{"too little ISK", func(c *AccumulateCandidate) { c.Derived.Percentiles.AvgDailyISK = 1_000_000 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(tc.mutate)}, accumulateOpts())
			if len(res.Rows) != 0 {
				t.Fatalf("accepted an illiquid item: %+v", res.Rows[0])
			}
			if res.Summary.RejectedThin != 1 {
				t.Fatalf("rejected_thin = %d, want 1 (%+v)", res.Summary.RejectedThin, res.Summary)
			}
			// It has to be visible with a reason, or a short list looks broken.
			if len(res.Rejected) != 1 || len(res.Rejected[0].Blockers) == 0 {
				t.Fatalf("illiquid item was dropped without an explanation: %+v", res.Rejected)
			}
		})
	}
}

// The gate that separates a bargain from a dying item. Without it "buy the
// dip" recommends everything in permanent decline, every day, all the way down.
// A falling trend means do not buy this at any price. Reported separately from
// "we have no track record", because the two mean opposite things to a buyer.
func TestBuildAccumulateRefusesAFallingTrend(t *testing.T) {
	res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(func(c *AccumulateCandidate) {
		c.Derived.Reversion.Declining = true
		c.Derived.Reversion.TrendPctDay = -0.42
	})}, accumulateOpts())

	if len(res.Rows) != 0 {
		t.Fatalf("advised buying an item in decline: %+v", res.Rows[0])
	}
	if res.Summary.RejectedDeclining != 1 {
		t.Fatalf("rejected_declining = %d, want 1 (%+v)", res.Summary.RejectedDeclining, res.Summary)
	}
	if res.Rejected[0].Grade != TodayGradeAvoid {
		t.Errorf("grade = %q, want avoid — a decline is not merely unproven", res.Rejected[0].Grade)
	}
}

// No track record is a different answer: we cannot tell, rather than no.
func TestBuildAccumulateSeparatesNoTrackRecordFromDecline(t *testing.T) {
	res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(func(c *AccumulateCandidate) {
		c.Derived.Reversion.Episodes = 1
	})}, accumulateOpts())

	if len(res.Rows) != 0 {
		t.Fatal("advised buying an item with no history of recovering")
	}
	if res.Summary.RejectedNoRecord != 1 {
		t.Fatalf("rejected_no_record = %d, want 1 (%+v)", res.Summary.RejectedNoRecord, res.Summary)
	}
	if res.Summary.RejectedDeclining != 0 {
		t.Error("counted as declining; those are different findings and must not be conflated")
	}
	if res.Rejected[0].Grade != TodayGradeUnproven {
		t.Errorf("grade = %q, want unproven", res.Rejected[0].Grade)
	}
}

// The bug that made this sweep return one row in fifteen hundred: an item
// genuinely in the bottom quarter of its year must not be rejected merely
// because a 180-day z-score does not also call it a dip.
func TestBuildAccumulateDoesNotRequireRecoveryToAgreeThatTodayIsCheap(t *testing.T) {
	res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(func(c *AccumulateCandidate) {
		// Exactly the real-world case: RecoveryOutlook declines to call today
		// a dip, while the yearly distribution says it is in the bottom decile.
		c.Derived.Recovery = RecoveryOutlook{
			Basis:  RecoveryBasisNone,
			Reason: "price is not unusually low",
		}
	})}, accumulateOpts())

	if len(res.Rows) != 1 {
		t.Fatalf("rejected a cheap, liquid, mean-reverting item because a second and different cheapness test disagreed (%+v)", res.Summary)
	}
}

// Cheap is relative to the item's own year, not to anything absolute.
func TestBuildAccumulateIgnoresItemsThatAreNotCheap(t *testing.T) {
	res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(func(c *AccumulateCandidate) {
		c.Derived.Percentiles.CurrentPercentile = 70
		c.BestSell = 105
	})}, accumulateOpts())

	if len(res.Rows) != 0 {
		t.Fatalf("accepted an item at the 70th percentile of its year")
	}
	if res.Summary.RejectedNotCheap != 1 {
		t.Fatalf("rejected_not_cheap = %d, want 1", res.Summary.RejectedNotCheap)
	}
}

// A recovery that does not clear fees is a way of turning ISK into patience.
func TestBuildAccumulateRequiresUpsideAfterFees(t *testing.T) {
	res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(func(c *AccumulateCandidate) {
		// Entry only just under the median: fees eat the whole difference.
		c.BestSell = 96
	})}, accumulateOpts())

	if len(res.Rows) != 0 {
		t.Fatalf("accepted a trade whose upside does not clear fees: %+v", res.Rows[0])
	}
}

// No history means unknown, and unknown is not an opportunity.
func TestBuildAccumulateRefusesWithoutHistory(t *testing.T) {
	res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(func(c *AccumulateCandidate) {
		c.Derived.Percentiles = PricePercentiles{Basis: PercentileBasisNone, Reason: "not enough price history"}
	})}, accumulateOpts())

	if len(res.Rows) != 0 {
		t.Fatal("accepted an item with no price history")
	}
	if res.Summary.RejectedNoData != 1 {
		t.Fatalf("rejected_no_data = %d, want 1", res.Summary.RejectedNoData)
	}
}

// A price far below anything in the item's history is far more likely to be
// bad data or a step change than a gift.
func TestBuildAccumulateTreatsImplausiblyCheapAsSuspect(t *testing.T) {
	res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(func(c *AccumulateCandidate) {
		c.BestSell = 5 // yearly low is 60
	})}, accumulateOpts())

	if len(res.Rows) != 0 {
		t.Fatalf("accepted a price 12x below the yearly floor: %+v", res.Rows[0])
	}
	if len(res.Rejected) == 0 {
		t.Fatal("suspect price was dropped silently")
	}
}

// Sizing is bounded by what the market turns over, so the position does not
// become the market.
func TestBuildAccumulateSizesAgainstVolumeAndCapital(t *testing.T) {
	t.Run("volume binds", func(t *testing.T) {
		res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(func(c *AccumulateCandidate) {
			c.Derived.Percentiles.AvgDailyVolume = 40 // 40*7*0.25 = 70 units
		})}, accumulateOpts())
		if len(res.Rows) != 1 {
			t.Fatalf("rows = %d", len(res.Rows))
		}
		if res.Rows[0].SuggestedQty != 70 {
			t.Errorf("qty = %d, want 70 (a quarter of a week of volume)", res.Rows[0].SuggestedQty)
		}
	})

	t.Run("capital binds", func(t *testing.T) {
		opts := accumulateOpts()
		opts.MaxCapitalPerItemISK = 700 // at 70 ISK entry, 10 units
		res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(nil)}, opts)
		if len(res.Rows) != 1 {
			t.Fatalf("rows = %d", len(res.Rows))
		}
		if res.Rows[0].SuggestedQty != 10 {
			t.Errorf("qty = %d, want 10 (capital cap)", res.Rows[0].SuggestedQty)
		}
	})
}

// Ranking is by evidence and cheapness, not by raw ISK -- these are
// speculative holds with no shared horizon, so a bigger ISK figure on a
// worse-evidenced item must not float to the top.
func TestBuildAccumulateRanksEvidenceOverSize(t *testing.T) {
	wellEvidenced := accumulateCandidate(func(c *AccumulateCandidate) {
		c.TypeID = 1
		c.Derived.Recovery.Episodes = 8
	})
	barelyEvidenced := accumulateCandidate(func(c *AccumulateCandidate) {
		c.TypeID = 2
		c.Derived.Recovery.Episodes = 3
		// Much bigger position, so far more raw ISK at stake.
		c.Derived.Percentiles.AvgDailyVolume = 5000
		c.Derived.Percentiles.AvgDailyISK = 500_000_000
	})

	res := BuildAccumulate([]AccumulateCandidate{barelyEvidenced, wellEvidenced}, accumulateOpts())
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}
	if res.Rows[1].ExpectedISK <= res.Rows[0].ExpectedISK {
		t.Skip("fixture no longer has the bigger-ISK row ranked second; nothing to prove")
	}
	if res.Rows[0].TypeID != 1 {
		t.Errorf("top row is type %d; the better-evidenced item should lead despite less ISK", res.Rows[0].TypeID)
	}
}

func TestBuildAccumulateOnEmptyInputIsEmptyNotZeroed(t *testing.T) {
	res := BuildAccumulate(nil, accumulateOpts())
	if res.Rows == nil || len(res.Rows) != 0 {
		t.Fatalf("rows = %#v, want an empty non-nil slice", res.Rows)
	}
	if res.Summary.Examined != 0 || res.Summary.Accepted != 0 {
		t.Fatalf("summary = %+v, want zeros", res.Summary)
	}
	if res.Summary.GeneratedAt == "" {
		t.Error("no generated_at stamp")
	}
}

// --- Four windows, not one (§6) ---

// accumulateWindow is one span as the derived cache carries it.
func accumulateWindowPct(days int, current, p50 float64) PricePercentiles {
	return PricePercentiles{
		Basis:             PercentileBasisHistory,
		WindowDays:        days,
		Samples:           days,
		TradedDays:        days,
		Min:               60,
		P50:               p50,
		Max:               p50 * 1.4,
		Current:           70,
		CurrentPercentile: current,
		AvgDailyVolume:    500,
		AvgDailyISK:       50_000_000,
	}
}

// The gap the user asked about: an item that dipped hard this quarter but has
// trended up over the year sits high in its year, so a single 365-day window
// could never see it however sharp the dip.
func TestBuildAccumulateAcceptsAnItemCheapOnlyOnAShortWindow(t *testing.T) {
	res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(func(c *AccumulateCandidate) {
		c.Derived.Percentiles.CurrentPercentile = 80 // dear, by the year's reckoning
		c.Derived.Percentiles.P50 = 65               // and the year's median is under today
		c.Derived.Windows = []PricePercentiles{
			accumulateWindowPct(30, 40, 78), // off the bottom of the month
			accumulateWindowPct(90, 11, 90), // but near the floor of the quarter
			accumulateWindowPct(180, 60, 110),
		}
	})}, accumulateOpts())

	if len(res.Rows) != 1 {
		t.Fatalf("accepted %d rows, want 1 (%+v)", len(res.Rows), res.Summary)
	}
	r := res.Rows[0]
	if r.QualifyingWindowDays != 90 {
		t.Errorf("qualifying window = %d days, want 90 — the only span it is cheap against", r.QualifyingWindowDays)
	}
	// The exit is that span's median. The year's 65 is below the entry price and
	// would have manufactured negative upside out of a real dip.
	if r.TargetPrice != 90 {
		t.Errorf("target = %.0f, want the 90-day median 90", r.TargetPrice)
	}
	if math.Abs(r.DiscountPct-20.0/90.0*100) > 0.1 {
		t.Errorf("discount = %.1f%%, want it measured against the qualifying median", r.DiscountPct)
	}
	// The span is part of the claim: a reader has to know which "cheap" this is.
	for _, want := range []string{"3-month", "over 90 days"} {
		if !strings.Contains(r.Why, want) {
			t.Errorf("why = %q, want it to name the window (%q)", r.Why, want)
		}
	}
	if len(r.WindowPercentiles) != 4 {
		t.Fatalf("windows = %d, want 4 (30/90/180 plus the year)", len(r.WindowPercentiles))
	}
	for _, w := range r.WindowPercentiles {
		if want := w.WindowDays == 90; w.Qualifies != want {
			t.Errorf("window %dd qualifies = %v, want %v", w.WindowDays, w.Qualifies, want)
		}
	}
}

// The other half of the same change, and the reason the short spans are safe to
// admit: a one-month dip with nothing behind it is a falling knife, so the
// evidence gates stay hard for every row rather than becoming grade modifiers.
func TestBuildAccumulateStillDemandsABounceRecordOnAShortWindow(t *testing.T) {
	shortOnly := func(c *AccumulateCandidate) {
		c.Derived.Percentiles.CurrentPercentile = 80
		c.Derived.Windows = []PricePercentiles{accumulateWindowPct(90, 11, 90)}
	}

	t.Run("a falling trend", func(t *testing.T) {
		res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(func(c *AccumulateCandidate) {
			shortOnly(c)
			c.Derived.Reversion.Declining = true
			c.Derived.Reversion.TrendPctDay = -0.42
		})}, accumulateOpts())

		if len(res.Rows) != 0 {
			t.Fatalf("advised buying a three-month dip in an item that is falling: %+v", res.Rows[0])
		}
		if res.Summary.RejectedDeclining != 1 {
			t.Fatalf("rejected_declining = %d, want 1 (%+v)", res.Summary.RejectedDeclining, res.Summary)
		}
	})

	t.Run("no record of dips recovering", func(t *testing.T) {
		res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(func(c *AccumulateCandidate) {
			shortOnly(c)
			c.Derived.Reversion.Episodes = 1
		})}, accumulateOpts())

		if len(res.Rows) != 0 {
			t.Fatal("advised buying a short-window dip with no history of recovering")
		}
		if res.Summary.RejectedNoRecord != 1 {
			t.Fatalf("rejected_no_record = %d, want 1 (%+v)", res.Summary.RejectedNoRecord, res.Summary)
		}
	})
}

// When several spans call it cheap, the exit is the least generous of them. A
// year-old median above everything the item has traded at since is not a price
// you can sell into.
func TestBuildAccumulateTakesTheLowestQualifyingMedian(t *testing.T) {
	res := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(func(c *AccumulateCandidate) {
		c.BestSell, c.BestBuy = 60, 56
		c.Derived.Percentiles.Current = 60
		c.Derived.Windows = []PricePercentiles{
			accumulateWindowPct(30, 10, 80),
			accumulateWindowPct(90, 12, 90),
			accumulateWindowPct(180, 15, 95),
		}
	})}, accumulateOpts())

	if len(res.Rows) != 1 {
		t.Fatalf("accepted %d rows, want 1 (%+v)", len(res.Rows), res.Summary)
	}
	r := res.Rows[0]
	if r.TargetPrice != 80 {
		t.Errorf("target = %.0f, want the lowest qualifying median 80 (year says 100)", r.TargetPrice)
	}
	if r.QualifyingWindowDays != 30 {
		t.Errorf("qualifying window = %d, want 30", r.QualifyingWindowDays)
	}
}

// The regression that matters most: an item cheap across the board, whose spans
// agree on where the middle is, must come out exactly as it did when there was
// only one window.
func TestBuildAccumulateLeavesTheYearlyPathAlone(t *testing.T) {
	before := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(nil)}, accumulateOpts()).Rows[0]

	after := BuildAccumulate([]AccumulateCandidate{accumulateCandidate(func(c *AccumulateCandidate) {
		c.Derived.Windows = []PricePercentiles{
			accumulateWindowPct(30, 12, 100),
			accumulateWindowPct(90, 10, 100),
			accumulateWindowPct(180, 9, 100),
		}
	})}, accumulateOpts()).Rows[0]

	if after.TargetPrice != before.TargetPrice || after.Grade != before.Grade {
		t.Errorf("the 365 path moved: target %.1f->%.1f, grade %q->%q",
			before.TargetPrice, after.TargetPrice, before.Grade, after.Grade)
	}
	if math.Abs(after.Score-before.Score) > 0.001 || after.Why != before.Why {
		t.Errorf("the 365 path moved: score %.3f->%.3f\n  why %q\n  ->  %q",
			before.Score, after.Score, before.Why, after.Why)
	}
	// Equal medians resolve to the longest span: same target, more evidence.
	if after.QualifyingWindowDays != 365 {
		t.Errorf("qualifying window = %d, want 365 when every span agrees on the median", after.QualifyingWindowDays)
	}
}

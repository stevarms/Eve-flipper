package engine

import (
	"math"
	"testing"
	"time"

	"eve-flipper/internal/esi"
)

var percentileNow = time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)

// percentileHistory builds `days` of daily entries ending the day before
// `percentileNow`, priced by the supplied function.
func percentileHistory(days int, price func(i int) float64, volume int64) []esi.HistoryEntry {
	out := make([]esi.HistoryEntry, 0, days)
	for i := days - 1; i >= 0; i-- {
		d := percentileNow.AddDate(0, 0, -1-i)
		out = append(out, esi.HistoryEntry{
			Date:    d.Format("2006-01-02"),
			Average: price(days - 1 - i),
			Volume:  volume,
		})
	}
	return out
}

func TestCalcPricePercentilesRanksAKnownDistribution(t *testing.T) {
	// 400 days walking 1..400 ISK. With a linear ramp the percentiles are
	// the values themselves, which makes the arithmetic checkable by hand.
	h := percentileHistory(400, func(i int) float64 { return float64(i + 1) }, 10)

	pct := CalcPricePercentiles(h, 0, percentileNow)

	if pct.Basis != PercentileBasisHistory {
		t.Fatalf("basis = %q (%s), want history", pct.Basis, pct.Reason)
	}
	// A year window over a 400-day series keeps the most recent 365.
	if pct.Samples != 365 {
		t.Fatalf("samples = %d, want 365 (the window, not the whole series)", pct.Samples)
	}
	// Those are prices 36..400, so the median is the midpoint of that range.
	if math.Abs(pct.P50-218) > 1 {
		t.Errorf("p50 = %.1f, want ~218", pct.P50)
	}
	if !(pct.P25 < pct.P50 && pct.P50 < pct.P75 && pct.P75 < pct.P90 && pct.P90 <= pct.P95) {
		t.Errorf("percentiles are not ordered: %+v", pct)
	}
	if pct.Min != 36 || pct.Max != 400 {
		t.Errorf("range = %.0f..%.0f, want 36..400", pct.Min, pct.Max)
	}
	// The series ends at its own maximum, so today is the dearest it has been.
	if pct.CurrentPercentile < 99 {
		t.Errorf("current percentile = %.1f, want ~100 on a rising series", pct.CurrentPercentile)
	}
}

// The whole point of the suggestion: a target has to be a price the item has
// genuinely traded at, which means it must fall inside the observed range.
func TestCalcPricePercentilesTargetsSitInsideTheObservedRange(t *testing.T) {
	h := percentileHistory(400, func(i int) float64 {
		// A seasonal shape: mostly flat with a sharp autumn spike, which is
		// the case the Gnosis-style target exists for.
		if i%365 > 300 && i%365 < 330 {
			return 130
		}
		return 70 + float64(i%7)
	}, 25)

	pct := CalcPricePercentiles(h, 0, percentileNow)
	if pct.Basis != PercentileBasisHistory {
		t.Fatalf("basis = %q (%s)", pct.Basis, pct.Reason)
	}
	for _, c := range []struct {
		name  string
		value float64
	}{{"p50", pct.P50}, {"p75", pct.P75}, {"p90", pct.P90}, {"p95", pct.P95}} {
		if c.value < pct.Min || c.value > pct.Max {
			t.Errorf("%s = %.1f falls outside the observed %.1f..%.1f", c.name, c.value, pct.Min, pct.Max)
		}
	}
	// A spike that occupies under 10% of the year should be at p95 and not at
	// p75, or "strong" would be recommending the peak.
	if pct.P75 >= 130 {
		t.Errorf("p75 = %.1f reached the spike price; a quarter-of-the-year target must not be the peak", pct.P75)
	}
}

// Absent evidence is refused rather than guessed, matching the recovery
// outlook's convention. A target built from three weeks of data would look
// exactly as authoritative as one built from a year.
func TestCalcPricePercentilesRefusesThinHistory(t *testing.T) {
	t.Run("too few days", func(t *testing.T) {
		pct := CalcPricePercentiles(percentileHistory(40, func(int) float64 { return 100 }, 10), 0, percentileNow)
		if pct.Basis != PercentileBasisNone {
			t.Fatalf("basis = %q, want none", pct.Basis)
		}
		if pct.Reason == "" {
			t.Error("no reason given, so the UI cannot say why it offered nothing")
		}
		if pct.P75 != 0 {
			t.Errorf("p75 = %.1f, want 0 — a refused verdict must not carry numbers", pct.P75)
		}
	})

	t.Run("quoted but never traded", func(t *testing.T) {
		// 300 days of prices with zero volume: a carried-forward quote on an
		// item nobody buys. Tight percentiles that describe nothing.
		pct := CalcPricePercentiles(percentileHistory(300, func(int) float64 { return 100 }, 0), 0, percentileNow)
		if pct.Basis != PercentileBasisNone {
			t.Fatalf("basis = %q, want none for an untraded series", pct.Basis)
		}
	})

	t.Run("empty", func(t *testing.T) {
		pct := CalcPricePercentiles(nil, 0, percentileNow)
		if pct.Basis != PercentileBasisNone || pct.P50 != 0 {
			t.Fatalf("empty history produced %+v", pct)
		}
	})
}

// The volume figures are the gate on "buy this dip": an annual low on
// something nobody trades is a position you cannot exit.
func TestCalcPricePercentilesReportsVolumeForTheGate(t *testing.T) {
	pct := CalcPricePercentiles(percentileHistory(300, func(int) float64 { return 200 }, 50), 0, percentileNow)

	if pct.Basis != PercentileBasisHistory {
		t.Fatalf("basis = %q (%s)", pct.Basis, pct.Reason)
	}
	if math.Abs(pct.AvgDailyVolume-50) > 0.001 {
		t.Errorf("avg daily volume = %.3f, want 50", pct.AvgDailyVolume)
	}
	if math.Abs(pct.AvgDailyISK-10000) > 0.1 {
		t.Errorf("avg daily ISK = %.1f, want 10000 (50 units at 200)", pct.AvgDailyISK)
	}
}

// A price at the bottom of its year is what the accumulate scan looks for, and
// the ranking has to say so unambiguously.
func TestCalcPricePercentilesPlacesAYearlyLowNearZero(t *testing.T) {
	h := percentileHistory(300, func(i int) float64 {
		if i == 299 { // most recent day
			return 40
		}
		return 100 + float64(i%11)
	}, 30)

	pct := CalcPricePercentiles(h, 0, percentileNow)
	if pct.Basis != PercentileBasisHistory {
		t.Fatalf("basis = %q (%s)", pct.Basis, pct.Reason)
	}
	if pct.Current != 40 {
		t.Fatalf("current = %.1f, want the latest day's 40", pct.Current)
	}
	if pct.CurrentPercentile > 1 {
		t.Errorf("current percentile = %.2f, want ~0 for a fresh yearly low", pct.CurrentPercentile)
	}
}

// PercentileAt backs the editor's choices. It must refuse on a value with no
// basis rather than interpolate an empty distribution into a plausible number.
func TestPercentileAtRefusesWithoutBasis(t *testing.T) {
	none := CalcPricePercentiles(nil, 0, percentileNow)
	if got := none.PercentileAt(75); got != 0 {
		t.Fatalf("PercentileAt on a refused verdict = %.1f, want 0", got)
	}

	good := CalcPricePercentiles(percentileHistory(300, func(i int) float64 { return float64(i + 1) }, 10), 0, percentileNow)
	if got := good.PercentileAt(75); got <= 0 {
		t.Fatalf("PercentileAt(75) = %.1f, want a real price", got)
	}
	// And it agrees with the named field it duplicates.
	if math.Abs(good.PercentileAt(75)-good.P75) > 0.001 {
		t.Errorf("PercentileAt(75) = %.3f but P75 = %.3f", good.PercentileAt(75), good.P75)
	}
}

// Entries out of order, or carrying junk, must not corrupt the distribution.
// ESI is documented as ascending; relying on it is how a sort bug becomes a
// wrong sell price.
func TestCalcPricePercentilesToleratesUnorderedAndJunkEntries(t *testing.T) {
	h := percentileHistory(300, func(i int) float64 { return float64(i + 1) }, 10)
	// Reverse it, and salt in entries that must be ignored rather than fatal.
	for i, j := 0, len(h)-1; i < j; i, j = i+1, j-1 {
		h[i], h[j] = h[j], h[i]
	}
	h = append(h,
		esi.HistoryEntry{Date: "not-a-date", Average: 999999, Volume: 5},
		esi.HistoryEntry{Date: percentileNow.Format("2006-01-02"), Average: math.NaN(), Volume: 5},
		esi.HistoryEntry{Date: percentileNow.Format("2006-01-02"), Average: 0, Volume: 5},
	)

	pct := CalcPricePercentiles(h, 0, percentileNow)
	if pct.Basis != PercentileBasisHistory {
		t.Fatalf("basis = %q (%s)", pct.Basis, pct.Reason)
	}
	if pct.Max > 300 {
		t.Errorf("max = %.1f, so the unparseable 999999 entry leaked in", pct.Max)
	}
	if math.IsNaN(pct.P75) || math.IsNaN(pct.Current) {
		t.Errorf("a NaN entry reached the output: %+v", pct)
	}
	// Latest-by-date, not latest-by-position, so reversing the slice must not
	// change which day counts as today.
	if pct.Current != 300 {
		t.Errorf("current = %.1f, want 300 — the newest day by date", pct.Current)
	}
}

func TestPercentileOfSortedEdges(t *testing.T) {
	s := []float64{10, 20, 30, 40}
	cases := []struct{ pct, want float64 }{
		{0, 10}, {100, 40}, {-5, 10}, {150, 40}, {50, 25},
	}
	for _, c := range cases {
		if got := percentileOfSorted(s, c.pct); math.Abs(got-c.want) > 0.001 {
			t.Errorf("percentileOfSorted(%.0f) = %.3f, want %.3f", c.pct, got, c.want)
		}
	}
	if got := percentileOfSorted(nil, 50); got != 0 {
		t.Errorf("empty distribution = %.1f, want 0", got)
	}
}

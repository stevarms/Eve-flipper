package engine

import (
	"encoding/json"
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

// --- MarketDerived -----------------------------------------------------

// One series, both answers. The point of pairing them is that they need the
// same expensive input, so a caller never has to fetch a year twice.
func TestCalcMarketDerivedAnswersBothFromOneSeries(t *testing.T) {
	h := percentileHistory(400, func(i int) float64 {
		// A gentle wave: enough variation for a trend fit to have residuals,
		// stable enough that it is a dip pattern rather than a decline.
		return 100 + 8*math.Sin(float64(i)/18)
	}, 40)

	d := CalcMarketDerived(h, percentileNow)

	if d.Percentiles.Basis != PercentileBasisHistory {
		t.Fatalf("percentiles basis = %q (%s)", d.Percentiles.Basis, d.Percentiles.Reason)
	}
	if d.Percentiles.P75 <= d.Percentiles.P50 {
		t.Errorf("p75 %.2f is not above p50 %.2f", d.Percentiles.P75, d.Percentiles.P50)
	}
	// Recovery gets its own 180-day window off the same series. It may
	// legitimately refuse on this shape, but it must have *looked* -- a basis
	// of "" would mean it never ran.
	if d.Recovery.Basis != RecoveryBasisHistory && d.Recovery.Basis != RecoveryBasisNone {
		t.Errorf("recovery basis = %q, want history or none", d.Recovery.Basis)
	}
	if d.Recovery.WindowDays <= 0 {
		t.Error("recovery reports no window, so it was not given the series")
	}
}

// The bug this pairing exists to kill: recovery used to read a 90-day cache
// while asking for 180 days, so the verdict depended on cache state. Feeding
// it a full series must produce a materially better-evidenced fit than
// feeding it 90 days of the same data.
func TestCalcMarketDerivedRecoverySeesMoreThanNinetyDays(t *testing.T) {
	full := percentileHistory(400, func(i int) float64 {
		return 100 + 10*math.Sin(float64(i)/25)
	}, 40)
	// What the 90-day cache would have handed it.
	truncated := full[len(full)-90:]

	fromFull := CalcRecoveryOutlook(full, 0)
	fromTruncated := CalcRecoveryOutlook(truncated, 0)

	if fromFull.Samples <= fromTruncated.Samples {
		t.Fatalf("full series gave %d samples, truncated gave %d — the fix changes nothing",
			fromFull.Samples, fromTruncated.Samples)
	}
	// 180 days is the documented window, so a full series should reach it
	// while 90 days of history cannot.
	if fromFull.Samples < 150 {
		t.Errorf("full series only reached %d samples, want most of the 180-day window", fromFull.Samples)
	}
	if fromTruncated.Samples > 95 {
		t.Errorf("truncated series reached %d samples, expected ~90", fromTruncated.Samples)
	}
}

func TestCalcMarketDerivedRefusesBothOnEmptyHistory(t *testing.T) {
	d := CalcMarketDerived(nil, percentileNow)
	if d.Percentiles.Basis != PercentileBasisNone {
		t.Errorf("percentiles basis = %q, want none", d.Percentiles.Basis)
	}
	if d.Recovery.Basis != RecoveryBasisNone {
		t.Errorf("recovery basis = %q, want none", d.Recovery.Basis)
	}
	// Both must say why, or the UI has nothing to show in place of a number.
	if d.Percentiles.Reason == "" || d.Recovery.Reason == "" {
		t.Errorf("a refusal carried no reason: %+v", d)
	}
}

// The whole cache design rests on this: the reduction has to survive JSON,
// because that is how it is stored and read back.
func TestMarketDerivedSurvivesJSONRoundTrip(t *testing.T) {
	h := percentileHistory(400, func(i int) float64 { return 100 + float64(i%23) }, 40)
	before := CalcMarketDerived(h, percentileNow)

	payload, err := json.Marshal(before)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// A few hundred bytes is the premise -- if this ever balloons, the
	// series has leaked into the payload.
	if len(payload) > 2000 {
		t.Errorf("payload is %d bytes; the point is to store a summary, not a series", len(payload))
	}

	var after MarketDerived
	if err := json.Unmarshal(payload, &after); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if after.Percentiles.P75 != before.Percentiles.P75 ||
		after.Percentiles.CurrentPercentile != before.Percentiles.CurrentPercentile ||
		after.Percentiles.AvgDailyVolume != before.Percentiles.AvgDailyVolume {
		t.Errorf("percentiles changed across the round trip")
	}
	if after.Recovery.Basis != before.Recovery.Basis ||
		after.Recovery.ZScore != before.Recovery.ZScore {
		t.Errorf("recovery changed across the round trip")
	}
}

// RankOfPrice is the wire-safe half of the pair: PercentileAt reads the sorted
// series and returns nothing once it is gone, while this one has to keep
// working on a value pulled out of the derived cache -- which is the only form
// the order desk ever sees.
func TestRankOfPriceAgreesWithCurrentPercentile(t *testing.T) {
	h := percentileHistory(400, func(i int) float64 { return float64(i + 1) }, 40)
	pct := CalcPricePercentiles(h, 0, percentileNow)
	if pct.Basis != PercentileBasisHistory {
		t.Fatalf("basis = %q (%s)", pct.Basis, pct.Reason)
	}

	got := pct.RankOfPrice(pct.Current)
	if math.Abs(got-pct.CurrentPercentile) > 1e-9 {
		t.Fatalf("rank of current = %v, current_percentile = %v", got, pct.CurrentPercentile)
	}
}

func TestRankOfPriceSurvivesTheDerivedCache(t *testing.T) {
	// The ladder has eight knots and the body of the distribution is
	// interpolated between them, so agreement is close rather than exact --
	// and the tails, where the knots are furthest apart, are the loosest.
	h := percentileHistory(400, func(i int) float64 { return float64(i + 1) }, 40)
	live := CalcPricePercentiles(h, 0, percentileNow)

	payload, err := json.Marshal(live)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var cached PricePercentiles
	if err := json.Unmarshal(payload, &cached); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, price := range []float64{live.P10, live.P25, live.P50, live.P75, live.P90, live.P95} {
		want := live.RankOfPrice(price)
		got := cached.RankOfPrice(price)
		if math.Abs(got-want) > 1.0 {
			t.Errorf("rank of %v: cached %v, live %v", price, got, want)
		}
	}

	// A price between two knots, where interpolation is doing the work.
	mid := (live.P50 + live.P75) / 2
	if got, want := cached.RankOfPrice(mid), live.RankOfPrice(mid); math.Abs(got-want) > 3.0 {
		t.Errorf("rank of the P50/P75 midpoint: cached %v, live %v", got, want)
	}
}

func TestRankOfPriceClampsOutsideTheObservedRange(t *testing.T) {
	h := percentileHistory(400, func(i int) float64 { return float64(i + 1) }, 40)
	live := CalcPricePercentiles(h, 0, percentileNow)
	payload, _ := json.Marshal(live)
	var cached PricePercentiles
	if err := json.Unmarshal(payload, &cached); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for name, p := range map[string]PricePercentiles{"live": live, "cached": cached} {
		if got := p.RankOfPrice(live.Min / 10); got != 0 {
			t.Errorf("%s: rank below the yearly low = %v, want 0", name, got)
		}
		if got := p.RankOfPrice(live.Max * 10); got != 100 {
			t.Errorf("%s: rank above the yearly high = %v, want 100", name, got)
		}
	}
}

func TestRankOfPriceRefusesWithoutBasis(t *testing.T) {
	// Same contract as PercentileAt: no basis means no answer, not zero
	// dressed up as one. The desk keys its risk verdict on the returned
	// number, so a silent 0 would read as "cheapest all year".
	none := PricePercentiles{Basis: PercentileBasisNone, P50: 100, Min: 1, Max: 200}
	if got := none.RankOfPrice(150); got != 0 {
		t.Fatalf("rank without a basis = %v, want 0", got)
	}
	h := percentileHistory(400, func(i int) float64 { return float64(i + 1) }, 40)
	live := CalcPricePercentiles(h, 0, percentileNow)
	if got := live.RankOfPrice(-5); got != 0 {
		t.Fatalf("rank of a negative price = %v, want 0", got)
	}
}

// --- Scaled window gates (multi-window accumulate) ---

// The absolute gates (120 samples / 90 traded days) made a short window
// impossible to satisfy: a 30-day window has at most 30 of either. Scaling them
// is what lets the sweep ask "is it cheap this month?" at all.
func TestCalcPricePercentilesScalesItsGatesToTheWindow(t *testing.T) {
	price := func(i int) float64 { return float64(i + 1) }

	t.Run("30-day window admits 20 traded days", func(t *testing.T) {
		pct := CalcPricePercentiles(percentileHistory(20, price, 10), 30, percentileNow)
		if pct.Basis != PercentileBasisHistory {
			t.Fatalf("basis = %q (%s), want history at the 20-sample floor", pct.Basis, pct.Reason)
		}
	})

	t.Run("and refuses 19", func(t *testing.T) {
		pct := CalcPricePercentiles(percentileHistory(19, price, 10), 30, percentileNow)
		if pct.Basis != PercentileBasisNone {
			t.Fatalf("basis = %q, want none one sample under the floor", pct.Basis)
		}
	})

	t.Run("the floor is a floor, not a proportion", func(t *testing.T) {
		// 0.55 * 30 rounds to 17, which is below the 20-sample floor. Seventeen
		// bars is not a distribution however short the window is.
		if got, _ := pricePercentileGates(30); got != 20 {
			t.Errorf("minSamples(30) = %d, want the floor 20", got)
		}
	})
}

// The year path must not have moved: every existing consumer reads it, and a
// gate that drifted would silently requalify or disqualify items across the
// whole app.
func TestCalcPricePercentilesYearGatesAreUnchanged(t *testing.T) {
	if s, tr := pricePercentileGates(365); s != pricePercentileMinSamples || tr != pricePercentileMinTradedDays {
		t.Fatalf("gates(365) = %d/%d, want the original %d/%d",
			s, tr, pricePercentileMinSamples, pricePercentileMinTradedDays)
	}
	price := func(i int) float64 { return float64(i + 1) }
	if pct := CalcPricePercentiles(percentileHistory(120, price, 10), 0, percentileNow); pct.Basis != PercentileBasisHistory {
		t.Errorf("basis = %q (%s), want history at exactly 120 samples", pct.Basis, pct.Reason)
	}
	if pct := CalcPricePercentiles(percentileHistory(119, price, 10), 0, percentileNow); pct.Basis != PercentileBasisNone {
		t.Errorf("basis = %q, want none at 119 samples", pct.Basis)
	}
}

// The derived cache is where the accumulate sweep reads its spans from, so all
// of them have to be in it -- and the year has to stay exactly where every
// other caller already looks for it.
func TestCalcMarketDerivedCarriesEveryWindow(t *testing.T) {
	d := CalcMarketDerived(percentileHistory(400, func(i int) float64 { return float64(i + 1) }, 10), percentileNow)

	if len(d.Windows) != len(DerivedWindowDays) {
		t.Fatalf("windows = %d, want %d", len(d.Windows), len(DerivedWindowDays))
	}
	for i, w := range d.Windows {
		if w.WindowDays != DerivedWindowDays[i] {
			t.Errorf("windows[%d] covers %d days, want %d (ascending, and the order is load-bearing for the cache check)",
				i, w.WindowDays, DerivedWindowDays[i])
		}
		if w.Basis != PercentileBasisHistory {
			t.Errorf("windows[%d] basis = %q (%s), want history on a 400-day series", i, w.Basis, w.Reason)
		}
	}
	// On a rising series the shorter the window, the higher its median.
	if !(d.Windows[0].P50 > d.Windows[1].P50 && d.Windows[1].P50 > d.Windows[2].P50 && d.Windows[2].P50 > d.Percentiles.P50) {
		t.Errorf("medians are not ordered by window on a monotonic series: 30=%.0f 90=%.0f 180=%.0f year=%.0f",
			d.Windows[0].P50, d.Windows[1].P50, d.Windows[2].P50, d.Percentiles.P50)
	}
	if d.Percentiles.Samples != 365 {
		t.Errorf("the year member moved: samples = %d, want 365", d.Percentiles.Samples)
	}
}

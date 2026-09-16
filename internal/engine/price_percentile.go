package engine

import (
	"math"
	"sort"
	"time"

	"eve-flipper/internal/esi"
)

// price_percentile.go — where today's price sits in the item's own year.
//
// Two questions share this arithmetic and neither is answered well by an
// average:
//
//   - "Hold this until it is worth what I think it is worth." A target of
//     "the 75th percentile of the last year" is a price the item has
//     genuinely traded at, roughly a quarter of the time. A target derived
//     from mean-plus-something is a price that may never have occurred.
//   - "Is this near a yearly low worth accumulating?" Which is the same
//     ranking read from the other end.
//
// Percentiles are taken over daily *average* price, not the daily low. ESI's
// `lowest` is the cheapest single fill that day, which on a thin item is
// frequently one mispriced order and not a level anyone could have bought
// meaningfully at.

// Bases mirror the recovery outlook's convention: a verdict is evidenced or
// it is absent, never guessed.
const (
	PercentileBasisHistory = "history"
	PercentileBasisNone    = "none"
)

const (
	// A full year, so an annual cycle appears exactly once and a seasonal
	// item is not judged against half its own pattern. ESI serves ~390 days.
	pricePercentileWindowDays = 365

	// Below this there is not enough of a year to have percentiles of. Also
	// the cap on the scaled gate below, so a 365-day window asks for exactly
	// what it always has.
	pricePercentileMinSamples = 120

	// And enough of those days must have actually traded. A series of
	// carried-forward quotes on an untraded item produces beautifully tight
	// percentiles that describe nothing.
	pricePercentileMinTradedDays = 90

	// Floors for a short window. Twenty daily averages is the fewest from
	// which a quartile means anything at all, and a fortnight of real trading
	// is the fewest that says the price was tested rather than quoted.
	pricePercentileFloorSamples    = 20
	pricePercentileFloorTradedDays = 15
)

// pricePercentileGates scales the sample requirements to the window length.
//
// The absolute pair above was written for a year, and it makes every shorter
// window impossible by construction: a 30-day window cannot hold 120 samples,
// so it would refuse every item every time. Scaling keeps the shape of the
// requirement — most of the window present, and most of that actually traded —
// rather than the number, and the caps pin 365 to exactly today's 120/90 so
// nothing that already depends on the yearly figures moves.
//
//	365 -> 120/90   180 -> 99/72   90 -> 50/36   30 -> 20/15
func pricePercentileGates(window int) (minSamples, minTraded int) {
	scale := func(f float64, floor, cap int) int {
		v := int(math.Round(f * float64(window)))
		if v < floor {
			return floor
		}
		if v > cap {
			return cap
		}
		return v
	}
	return scale(0.55, pricePercentileFloorSamples, pricePercentileMinSamples),
		scale(0.40, pricePercentileFloorTradedDays, pricePercentileMinTradedDays)
}

// PricePercentiles describes an item's trailing-year price distribution and
// where it is sitting in it today.
type PricePercentiles struct {
	Basis  string `json:"basis"`
	Reason string `json:"reason,omitempty"`

	WindowDays int `json:"window_days"`
	Samples    int `json:"samples"`
	TradedDays int `json:"traded_days"`

	P10 float64 `json:"p10"`
	P25 float64 `json:"p25"`
	P50 float64 `json:"p50"`
	P75 float64 `json:"p75"`
	P90 float64 `json:"p90"`
	P95 float64 `json:"p95"`
	Min float64 `json:"min"`
	Max float64 `json:"max"`

	// Current is the most recent daily average in the window, and
	// CurrentPercentile is where it falls in the distribution: 0 means the
	// cheapest the item has been all year, 100 the dearest.
	Current           float64 `json:"current"`
	CurrentPercentile float64 `json:"current_percentile"`

	// AvgDailyVolume over the window, and AvgDailyISK the ISK that moves in
	// a day. The volume gate on "buy the dip" reads these: an annual low on
	// something nobody trades is not an opportunity, it is a trap you cannot
	// exit.
	AvgDailyVolume float64 `json:"avg_daily_volume"`
	AvgDailyISK    float64 `json:"avg_daily_isk"`

	// The working distribution. Unexported so it never crosses the wire; see
	// PercentileAt.
	sorted []float64
}

// PercentileAt returns the price at an arbitrary percentile.
//
// Only meaningful on a value straight out of CalcPricePercentiles: the sorted
// distribution it reads is unexported, so it does not survive a JSON round
// trip and this returns 0 afterwards. That is deliberate — a year of daily
// prices per item would dwarf the rest of any payload, and the named
// percentiles below are what a client actually needs. The target editor picks
// from those rather than sliding continuously.
func (p PricePercentiles) PercentileAt(pct float64) float64 {
	if p.Basis != PercentileBasisHistory || len(p.sorted) == 0 {
		return 0
	}
	return percentileOfSorted(p.sorted, pct)
}

// RankOfPrice returns where an arbitrary price falls in the distribution:
// 0 is the cheapest the item has been in the window, 100 the dearest.
//
// Unlike PercentileAt this survives the wire, which is the whole reason it
// exists — the order desk reads percentiles out of the derived cache, where
// `sorted` is long gone. On a value straight from CalcPricePercentiles it
// reads the full series and is exact; on a cached one it interpolates across
// the eight stored quantiles, which is good to a point or two through the
// body of the distribution and coarser in the tails. That is the trade the
// cache exists to make: a year of daily prices per item would dwarf every
// payload it rides on.
func (p PricePercentiles) RankOfPrice(price float64) float64 {
	if p.Basis != PercentileBasisHistory || price <= 0 {
		return 0
	}
	if len(p.sorted) > 0 {
		return rankOfSorted(p.sorted, price)
	}

	// The stored ladder as (price, percentile) knots. Equal prices are
	// folded into one knot spanning both percentiles, so a price sitting on
	// a flat stretch reports the middle of it — the same tie convention
	// rankOfSorted uses.
	type knot struct {
		price        float64
		pctLo, pctHi float64
	}
	raw := []struct{ price, pct float64 }{
		{p.Min, 0}, {p.P10, 10}, {p.P25, 25}, {p.P50, 50},
		{p.P75, 75}, {p.P90, 90}, {p.P95, 95}, {p.Max, 100},
	}
	ladder := make([]knot, 0, len(raw))
	for _, r := range raw {
		if r.price <= 0 || math.IsNaN(r.price) || math.IsInf(r.price, 0) {
			continue
		}
		if n := len(ladder); n > 0 {
			// A ladder that is not ascending is not a distribution; skip
			// the offending knot rather than interpolate backwards.
			if r.price < ladder[n-1].price {
				continue
			}
			if r.price == ladder[n-1].price {
				ladder[n-1].pctHi = r.pct
				continue
			}
		}
		ladder = append(ladder, knot{price: r.price, pctLo: r.pct, pctHi: r.pct})
	}
	if len(ladder) == 0 {
		return 0
	}

	mid := func(k knot) float64 { return (k.pctLo + k.pctHi) / 2 }
	if price <= ladder[0].price {
		return mid(ladder[0])
	}
	last := ladder[len(ladder)-1]
	if price >= last.price {
		return mid(last)
	}
	for i := 1; i < len(ladder); i++ {
		hi := ladder[i]
		if price > hi.price {
			continue
		}
		if price == hi.price {
			return mid(hi)
		}
		lo := ladder[i-1]
		span := hi.price - lo.price
		if span <= 0 {
			return mid(hi)
		}
		return clampRange(lo.pctHi+(hi.pctLo-lo.pctHi)*(price-lo.price)/span, 0, 100)
	}
	return mid(last)
}

// CalcPricePercentiles builds the trailing-year distribution.
//
// `window` defaults to a year. `now` is injected so the traded-day gate can be
// tested without waiting a year for real data.
func CalcPricePercentiles(history []esi.HistoryEntry, window int, now time.Time) PricePercentiles {
	if window <= 0 {
		window = pricePercentileWindowDays
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := PricePercentiles{Basis: PercentileBasisNone, WindowDays: window}

	cutoff := now.AddDate(0, 0, -window)
	prices := make([]float64, 0, len(history))
	var volSum, iskSum float64
	traded := 0
	var latestDate string
	var latest float64

	for _, e := range history {
		d, err := time.Parse("2006-01-02", e.Date)
		if err != nil || d.Before(cutoff) {
			continue
		}
		if e.Average <= 0 || math.IsNaN(e.Average) || math.IsInf(e.Average, 0) {
			continue
		}
		prices = append(prices, e.Average)
		volSum += float64(e.Volume)
		iskSum += float64(e.Volume) * e.Average
		if e.Volume > 0 {
			traded++
		}
		// ESI returns history ascending, but do not rely on it: take the
		// latest by date rather than by position.
		if e.Date > latestDate {
			latestDate = e.Date
			latest = e.Average
		}
	}

	minSamples, minTraded := pricePercentileGates(window)
	out.Samples = len(prices)
	out.TradedDays = traded
	if out.Samples < minSamples {
		out.Reason = "not enough price history"
		return out
	}
	if traded < minTraded {
		out.Reason = "too few days actually traded"
		return out
	}

	sort.Float64s(prices)
	out.sorted = prices
	out.Basis = PercentileBasisHistory
	out.P10 = percentileOfSorted(prices, 10)
	out.P25 = percentileOfSorted(prices, 25)
	out.P50 = percentileOfSorted(prices, 50)
	out.P75 = percentileOfSorted(prices, 75)
	out.P90 = percentileOfSorted(prices, 90)
	out.P95 = percentileOfSorted(prices, 95)
	out.Min = prices[0]
	out.Max = prices[len(prices)-1]

	out.Current = latest
	out.CurrentPercentile = rankOfSorted(prices, latest)
	out.AvgDailyVolume = volSum / float64(out.Samples)
	out.AvgDailyISK = iskSum / float64(out.Samples)
	return out
}

// percentileOfSorted linearly interpolates between the two straddling
// samples, which matters on the short end of the sample-count gate: nearest-
// rank on 120 points moves the answer in visible steps as days roll off.
func percentileOfSorted(sorted []float64, pct float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if pct <= 0 {
		return sorted[0]
	}
	if pct >= 100 {
		return sorted[n-1]
	}
	pos := pct / 100 * float64(n-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo] + (sorted[hi]-sorted[lo])*frac
}

// rankOfSorted is the inverse: what percentile a given price sits at. Ties
// resolve to the midpoint of the tied run, so a price equal to a long flat
// stretch reports the middle of that stretch rather than either edge.
func rankOfSorted(sorted []float64, value float64) float64 {
	n := len(sorted)
	if n == 0 || value <= 0 {
		return 0
	}
	below := sort.SearchFloat64s(sorted, value)
	atOrBelow := sort.SearchFloat64s(sorted, math.Nextafter(value, math.Inf(1)))
	mid := (float64(below) + float64(atOrBelow)) / 2
	return clampRange(mid/float64(n)*100, 0, 100)
}

// MarketDerived is everything worth keeping from one fetch of an item's price
// series.
//
// The two members answer different questions that happen to need the same
// expensive input, so they are computed together and cached together:
// percentiles say where today sits in the item's year, and the recovery
// outlook says whether a low price is a dip that has come back before or a
// decline that has not.
//
// Both carry their own Basis field and refuse rather than guess, so a caller
// checks the member it cares about and never has to interpret a zero.
type MarketDerived struct {
	Percentiles PricePercentiles `json:"percentiles"`
	// Windows is the same distribution over shorter spans, ascending — see
	// DerivedWindowDays. An item that dipped hard last month but has trended
	// up all year is invisible to the yearly figure by construction, and an
	// item grinding down for six months looks the same as a real dip; reading
	// several spans at once is what tells those two apart.
	//
	// Percentiles above stays the 365-day member, so every existing consumer
	// is untouched. A window that cannot clear its own sample gate is still
	// present, carrying Basis "none" — an absent entry and a refused one mean
	// different things to the sweep.
	Windows []PricePercentiles `json:"windows"`
	// Recovery answers "is today a dip worth waiting out", over 180 days. The
	// order desk's hold-or-cut verdict wants exactly that.
	Recovery RecoveryOutlook `json:"recovery"`
	// Reversion answers "does this item come back, in general", over a year.
	// A separate question, and stacking Recovery's own dip test on top of a
	// different cheapness test is what made the accumulate sweep return one
	// row out of fifteen hundred. See mean_reversion.go.
	Reversion MeanReversionProfile `json:"reversion"`
}

// DerivedWindowDays are the shorter spans MarketDerived carries beside the
// year, ascending. Three rather than one because the question "is this cheap"
// has a different answer at each scale and the disagreement between them is
// the signal: cheap on all four is a market that has repriced, cheap only on
// thirty days is a dip that may or may not be a knife.
var DerivedWindowDays = []int{30, 90, 180}

// CalcMarketDerived reduces one price series to both summaries.
//
// The series is read twice and then dropped. That is the whole point: the
// caller can fetch ~390 days, call this, and retain a few hundred bytes
// instead of tens of kilobytes per item — which is what makes examining
// thousands of items affordable.
func CalcMarketDerived(history []esi.HistoryEntry, now time.Time) MarketDerived {
	windows := make([]PricePercentiles, 0, len(DerivedWindowDays))
	for _, w := range DerivedWindowDays {
		windows = append(windows, CalcPricePercentiles(history, w, now))
	}
	return MarketDerived{
		Windows: windows,
		// A year, so an annual cycle appears exactly once.
		Percentiles: CalcPricePercentiles(history, 0, now),
		// 180 days, its own documented window. Passing 0 takes that default;
		// feeding it the full series is the point, since its caller used to
		// hand it whatever the 90-day cache happened to hold.
		Recovery: CalcRecoveryOutlook(history, 0),
		// A year, matching the percentiles, so both halves of an accumulate
		// verdict describe the same span.
		Reversion: CalcMeanReversion(history, 0),
	}
}

package engine

import (
	"sort"

	"eve-flipper/internal/esi"
)

// mean_reversion.go — does this item's price come back, in general?
//
// A deliberately different question from CalcRecoveryOutlook, and the
// distinction cost a scan most of its results before it was drawn.
//
// RecoveryOutlook answers "is *today* a dip worth waiting out". To do that it
// has to decide today is a dip, which it does with a z-score against a 180-day
// trend. That is the right test when you are staring at one underwater order.
//
// It is the wrong test to stack on top of a different cheapness test. The
// accumulate sweep establishes that today is cheap its own way — bottom quarter
// of the item's trailing year — and then only needs to know whether this item is
// the kind of thing that recovers, or the kind of thing that is on its way out.
// Requiring both meant requiring two disagreeing definitions of cheap: "in the
// bottom quarter of its year" and "more than one sigma below a six-month trend"
// are not the same claim, and 427 of 954 items in one real sweep passed the
// first and failed the second.
//
// So this reports the two facts that are independent of today:
//
//   - Is the trend itself falling? Waiting out a decline is not patience.
//   - Have dips of a fixed depth in this item historically returned to trend,
//     and how long did that take?
//
// Fixed depth, not today's depth, is the other half of the separation.
// recoveryEpisodes counts past dips at least as deep as the one you are in,
// which is right for "how long will *this* take" and wrong for "does this item
// revert at all" — the answer would change every day as the price moved.

// MeanReversionProfile is the item's own tendency to come back.
type MeanReversionProfile struct {
	Basis  string `json:"basis"`
	Reason string `json:"reason,omitempty"`

	WindowDays int `json:"window_days"`
	Samples    int `json:"samples"`
	TradedDays int `json:"traded_days"`

	// TrendPctDay is the fitted drift in percent per day, and Declining is
	// whether that drift is statistically significant rather than merely
	// negative. Most series drift down slightly and it means nothing.
	TrendPctDay float64 `json:"trend_pct_day"`
	TrendTStat  float64 `json:"trend_t_stat"`
	Declining   bool    `json:"declining"`

	// Episodes is how many dips of at least recoveryDipZ returned to trend
	// inside the window, and MedianDays how long they took.
	Episodes   int     `json:"episodes"`
	MedianDays float64 `json:"median_days"`
}

// Reverts reports whether the item is worth waiting on at all: measurable, not
// in decline, and with a track record of coming back.
func (p MeanReversionProfile) Reverts() bool {
	return p.Basis == RecoveryBasisHistory && !p.Declining && p.Episodes >= recoveryMinEpisodes
}

// CalcMeanReversion fits the trend and counts recovered dips over `window`,
// defaulting to a year so an annual cycle appears exactly once — the same
// window the percentiles use, so the two halves of an accumulate verdict are
// talking about the same span of time.
func CalcMeanReversion(history []esi.HistoryEntry, window int) MeanReversionProfile {
	if window <= 0 {
		window = pricePercentileWindowDays
	}
	out := MeanReversionProfile{Basis: RecoveryBasisNone, WindowDays: window}

	points := recoveryPointsInWindow(history, window)
	out.Samples = len(points)
	if len(points) < recoveryMinEntries {
		out.Reason = "not enough traded history"
		return out
	}
	for _, p := range points {
		if p.traded {
			out.TradedDays++
		}
	}
	if out.TradedDays < recoveryMinTradedDays {
		out.Reason = "not enough traded history"
		return out
	}

	slope, intercept, sigma, tStat, ok := recoveryFit(points)
	if !ok {
		out.Reason = "price has no variation to measure"
		return out
	}
	out.TrendTStat = tStat
	out.TrendPctDay = trendPercentPerDay(slope)
	out.Declining = tStat < recoveryDeclineT

	// Residuals against the fit, then count dips at a fixed depth rather than
	// at today's. See the note at the top of this file.
	zs := make([]float64, len(points))
	for i, p := range points {
		zs[i] = (p.logAvg - (intercept + slope*p.day)) / sigma
	}
	gaps := recoveryEpisodes(points, zs, recoveryDipZ)
	out.Episodes = len(gaps)
	if len(gaps) > 0 {
		sort.Float64s(gaps)
		out.MedianDays = percentile(gaps, 50)
	}

	// A measurable answer, even when it is "this declines and does not come
	// back". The caller decides what to do with that; Reverts() is the
	// shorthand. Refusing here would throw away the decline finding, which is
	// the single most useful thing this can tell you.
	out.Basis = RecoveryBasisHistory
	if out.Declining {
		out.Reason = "trend is falling, not dipping"
	} else if out.Episodes < recoveryMinEpisodes {
		out.Reason = "no track record of dips recovering"
	}
	return out
}

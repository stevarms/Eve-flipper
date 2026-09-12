package engine

import (
	"math"
	"sort"
	"time"

	"eve-flipper/internal/esi"
)

// Recovery outlook bases, mirroring the order desk's margin bases: a
// verdict is either evidenced or it is absent. There is no middle setting
// where the desk guesses and hedges.
const (
	RecoveryBasisHistory = "history"
	RecoveryBasisNone    = "none"
)

const (
	// The trailing window the fit is taken over. ESI serves roughly 390
	// days; half a year is long enough to contain several dip-and-recover
	// episodes without reaching back to a different balance patch.
	recoveryWindowDays = 180

	// Evidence gate. Below these the outlook is "none" — a thin item's
	// price series is mostly the sampling noise of a handful of trades.
	recoveryMinEntries    = 90
	recoveryMinTradedDays = 60

	// A dip has to have happened before, and recovered, at least this many
	// times before we will tell anyone to wait one out.
	recoveryMinEpisodes = 3

	// How far below trend counts as "unusually low", in residual sigmas.
	recoveryDipZ = -1.0

	// t-statistic on the slope below which the series is a decline rather
	// than a dip. Two standard errors is the conventional bar and it is the
	// difference between "waiting" and "losing more slowly".
	recoveryDeclineT = -2.0
)

// RecoveryOutlook answers one question about a price series: is today's
// price a dip that has historically come back, or is it just where this
// item lives now?
//
// Basis is RecoveryBasisHistory only when every gate below was cleared.
// Otherwise it is RecoveryBasisNone and Reason says which gate failed —
// callers must not treat that as "no recovery", only as "we cannot tell",
// and must drop the hold option rather than substitute a guess.
type RecoveryOutlook struct {
	Basis  string `json:"basis"`
	Reason string `json:"reason,omitempty"`

	// TrendPctDay is the fitted drift in percent per day. Negative but
	// insignificant is normal; significantly negative fails the gate.
	TrendPctDay float64 `json:"trend_pct_day"`

	// ZScore is how far today's price sits below the fitted trend, in
	// standard deviations of the fit's own residuals.
	ZScore float64 `json:"z_score"`

	// TargetPrice is the price a recovery is expected to reach — the trend
	// line at the recovery date, capped so we never promise the peak.
	TargetPrice float64 `json:"target_price"`

	// MedianDays is the empirical median time comparable dips took to
	// return to trend. Not a model output — a median of what happened.
	MedianDays float64 `json:"median_days"`

	// Episodes is how many comparable dips were found and recovered.
	Episodes int `json:"episodes"`

	// WindowDays and Samples describe the evidence the rest is built on,
	// so the UI can show its working rather than assert a number.
	WindowDays int `json:"window_days"`
	Samples    int `json:"samples"`
}

// recoveryPoint is one traded day, positioned by its real calendar offset
// so that gaps in the series (untraded days, ESI holes) do not silently
// compress the time axis.
type recoveryPoint struct {
	day    float64 // days since the oldest entry in the window
	logAvg float64
	avg    float64
	traded bool
}

// CalcRecoveryOutlook fits a log-linear trend to the trailing window of a
// price series and decides whether today is a dip below that trend worth
// waiting out.
//
// The distinction it exists to draw is dip versus decline. A dip is a
// price below a flat trend, and the history says how long those have taken
// to close. A decline is the trend itself falling, where waiting only
// changes how much you lose. The two look identical on a single day's
// quote and are told apart by the slope's significance, not its sign.
//
// The history is regional and daily — it blends every station and both
// sides of the book — which is why the gates are set where they are and
// why the result is refused far more often than it is given.
func CalcRecoveryOutlook(history []esi.HistoryEntry, window int) RecoveryOutlook {
	if window <= 0 {
		window = recoveryWindowDays
	}
	out := RecoveryOutlook{Basis: RecoveryBasisNone, WindowDays: window}

	points := recoveryPointsInWindow(history, window)
	out.Samples = len(points)
	if len(points) < recoveryMinEntries {
		out.Reason = "not enough traded history"
		return out
	}
	traded := 0
	for _, p := range points {
		if p.traded {
			traded++
		}
	}
	if traded < recoveryMinTradedDays {
		out.Reason = "not enough traded history"
		return out
	}

	slope, intercept, sigma, tStat, ok := recoveryFit(points)
	if !ok {
		out.Reason = "price has no variation to measure"
		return out
	}
	out.TrendPctDay = trendPercentPerDay(slope)

	// A significant downward slope means the level itself is moving, so
	// there is no trend to revert to. Checked before the dip test because
	// a falling series is always "below" its own recent history.
	if tStat < recoveryDeclineT {
		out.Reason = "trending down, not dipping"
		return out
	}

	zs := make([]float64, len(points))
	for i, p := range points {
		zs[i] = (p.logAvg - (intercept + slope*p.day)) / sigma
	}
	z := zs[len(zs)-1]
	out.ZScore = z
	if z > recoveryDipZ {
		out.Reason = "price is not unusually low"
		return out
	}

	gaps := recoveryEpisodes(points, zs, z)
	out.Episodes = len(gaps)
	if len(gaps) < recoveryMinEpisodes {
		out.Reason = "no comparable dip in this item's history recovered"
		return out
	}
	sort.Float64s(gaps)
	out.MedianDays = percentile(gaps, 50)

	last := points[len(points)-1]
	target := math.Exp(intercept + slope*(last.day+out.MedianDays))

	// Cap at the window's upper quartile. The fitted line extrapolated
	// forward can wander above anything this item has actually traded at,
	// and a recovery thesis that needs a new high is not a recovery.
	avgs := make([]float64, len(points))
	for i, p := range points {
		avgs[i] = p.avg
	}
	sort.Float64s(avgs)
	if cap75 := percentile(avgs, 75); cap75 > 0 && cap75 < target {
		target = cap75
	}
	if target <= last.avg {
		out.Reason = "recovery target is not above today's price"
		return out
	}

	out.Basis = RecoveryBasisHistory
	out.TargetPrice = target
	return out
}

// recoveryPointsInWindow projects the history onto a day axis anchored at
// the oldest entry kept, dropping anything without a usable price. The
// window is measured back from the newest entry rather than from now, so
// the result does not change with the clock and stale data fails the
// sample gate instead of silently shrinking the window.
func recoveryPointsInWindow(history []esi.HistoryEntry, window int) []recoveryPoint {
	type dated struct {
		t     time.Time
		entry esi.HistoryEntry
	}
	parsed := make([]dated, 0, len(history))
	for _, h := range history {
		if h.Average <= 0 {
			continue
		}
		t, err := time.Parse("2006-01-02", h.Date)
		if err != nil {
			continue
		}
		parsed = append(parsed, dated{t: t, entry: h})
	}
	if len(parsed) == 0 {
		return nil
	}
	sort.Slice(parsed, func(i, j int) bool { return parsed[i].t.Before(parsed[j].t) })

	newest := parsed[len(parsed)-1].t
	cutoff := newest.AddDate(0, 0, -window)
	first := 0
	for first < len(parsed) && parsed[first].t.Before(cutoff) {
		first++
	}
	kept := parsed[first:]
	if len(kept) == 0 {
		return nil
	}

	origin := kept[0].t
	points := make([]recoveryPoint, 0, len(kept))
	for _, d := range kept {
		points = append(points, recoveryPoint{
			day:    d.t.Sub(origin).Hours() / 24,
			logAvg: math.Log(d.entry.Average),
			avg:    d.entry.Average,
			traded: d.entry.Volume > 0,
		})
	}
	return points
}

// recoveryFit is ordinary least squares of log price on day, returning the
// slope, intercept, residual sigma and the slope's t-statistic. Logs rather
// than raw ISK so the slope reads as a compounding rate and a 10 ISK item
// and a 10 billion ISK item are held to the same bar.
func recoveryFit(points []recoveryPoint) (slope, intercept, sigma, tStat float64, ok bool) {
	n := float64(len(points))
	if n < 3 {
		return 0, 0, 0, 0, false
	}
	var sumX, sumY float64
	for _, p := range points {
		sumX += p.day
		sumY += p.logAvg
	}
	meanX, meanY := sumX/n, sumY/n

	var sxx, sxy float64
	for _, p := range points {
		dx := p.day - meanX
		sxx += dx * dx
		sxy += dx * (p.logAvg - meanY)
	}
	if sxx <= 0 {
		return 0, 0, 0, 0, false
	}
	slope = sxy / sxx
	intercept = meanY - slope*meanX

	var sse float64
	for _, p := range points {
		resid := p.logAvg - (intercept + slope*p.day)
		sse += resid * resid
	}
	sigma = math.Sqrt(sse / (n - 2))
	if sigma <= 0 || math.IsNaN(sigma) || math.IsInf(sigma, 0) {
		return 0, 0, 0, 0, false
	}
	tStat = slope / (sigma / math.Sqrt(sxx))
	return slope, intercept, sigma, tStat, true
}

// recoveryEpisodes measures how long past dips at least as deep as the
// current one took to climb back to trend.
//
// Only the first day of each dip counts, or one forty-day slump would be
// reported as forty separate episodes and drown the median. An episode is
// not considered over until the price is back at trend, so a series that
// wobbles either side of the threshold stays one episode rather than
// becoming several short ones. A dip still open at the end of the window
// is not counted: it has not recovered yet, and assuming it will is the
// exact error this whole function exists to avoid.
func recoveryEpisodes(points []recoveryPoint, zs []float64, threshold float64) []float64 {
	var gaps []float64
	inDip := false
	for i, z := range zs {
		if z >= 0 {
			inDip = false
			continue
		}
		if inDip || z > threshold {
			continue
		}
		inDip = true
		for j := i + 1; j < len(zs); j++ {
			if zs[j] >= 0 {
				gaps = append(gaps, points[j].day-points[i].day)
				break
			}
		}
	}
	return gaps
}

// trendPercentPerDay converts a log-linear slope into percent per day. Shared
// with CalcMeanReversion so the two fits cannot report the same drift
// differently.
func trendPercentPerDay(slope float64) float64 {
	return (math.Exp(slope) - 1) * 100
}

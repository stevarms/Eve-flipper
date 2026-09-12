package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// accumulate.go — items trading near the bottom of their own year, that you
// could actually get back out of.
//
// This is the mirror image of every other scan in the app. Station and regional
// trading look at a spread *right now* and ask whether the two sides are far
// enough apart. This looks at one price over a year and asks whether today is
// unusually cheap for this particular item.
//
// The whole thing hangs on refusing far more often than it accepts, because the
// naive version of this idea is actively harmful. "Cheapest it has been all
// year" describes a bargain and a dying item identically, and the difference
// only shows up in two places:
//
//   - Volume. An annual low on something nobody trades is not an entry, it is a
//     position you cannot exit. This is the gate the user asked for first and it
//     is applied before anything else is considered.
//   - Whether dips in *this* item have historically come back. That is what
//     CalcRecoveryOutlook already answers, and it distinguishes a price below a
//     flat trend from a trend that is simply falling. Without it, "buy the dip"
//     recommends every item in a permanent decline, every day, all the way down.

const (
	// How far into the bottom of its own year a price has to be before it is
	// interesting. A quarter is deliberately not aggressive: the point is to
	// find things that are cheap, not to catch exact bottoms, and a tighter
	// threshold mostly finds items whose history is too thin to trust anyway.
	accumulateMaxPercentile = 25.0

	// Liquidity floors. Both must clear, because they fail differently: units
	// catches items that trade in tiny numbers at high value, and ISK catches
	// items that trade in huge numbers for nothing.
	accumulateMinUnitsPerDay = 20.0
	accumulateMinISKPerDay   = 20_000_000.0

	// A recovery has to be worth the wait after fees. Below this the trade is
	// a way of turning ISK into patience.
	accumulateMinUpsidePct = 12.0

	// Sanity bound on the entry price against the item's own history. A
	// current price wildly below the yearly minimum is far more likely to be
	// a data artefact or a market-wide change than an opportunity.
	accumulateMinPriceRatio = 0.35
)

// AccumulateCandidate is one type as the scan sees it before judging: what the
// book says now, and what its year says.
type AccumulateCandidate struct {
	TypeID       int32
	TypeName     string
	CategoryID   int32
	CategoryName string

	// BestSell is what you would pay to buy now; BestBuy what you would get
	// selling instantly. SellDepthISK is how much sell-side inventory sits at
	// the station, used only to rank which types are worth spending an ESI
	// history call on.
	BestSell     float64
	BestBuy      float64
	SellDepthISK float64

	Derived MarketDerived
}

// AccumulateRow is one judged candidate.
type AccumulateRow struct {
	TypeID       int32  `json:"type_id"`
	TypeName     string `json:"type_name"`
	CategoryID   int32  `json:"category_id,omitempty"`
	CategoryName string `json:"category_name,omitempty"`

	// --- where it is now, against its year ---
	BestSell          float64 `json:"best_sell"`
	BestBuy           float64 `json:"best_buy"`
	CurrentPercentile float64 `json:"current_percentile"`
	YearLow           float64 `json:"year_low"`
	YearHigh          float64 `json:"year_high"`
	YearMedian        float64 `json:"year_median"`

	// DiscountPct is how far below the yearly median the entry price sits --
	// the headline "how cheap is this".
	DiscountPct float64 `json:"discount_pct"`

	// --- what recovering is worth ---
	// TargetPrice is the conservative exit: the yearly median, not the peak.
	// Recommending a return to p90 would be quoting the best case as the plan.
	TargetPrice float64 `json:"target_price"`
	// UpsidePct is net of the fees paid on the way out.
	UpsidePct        float64 `json:"upside_pct"`
	UpsideISKPerUnit float64 `json:"upside_isk_per_unit"`

	// --- has this ever come back? ---
	RecoveryBasis   string  `json:"recovery_basis"`
	RecoveryReason  string  `json:"recovery_reason,omitempty"`
	RecoveryDays    float64 `json:"recovery_days"`
	RecoveryEpisode int     `json:"recovery_episodes"`
	TrendPctDay     float64 `json:"trend_pct_day"`

	// --- can you get out ---
	AvgDailyVolume float64 `json:"avg_daily_volume"`
	AvgDailyISK    float64 `json:"avg_daily_isk"`
	// DaysToUnwind is how long the suggested quantity would take to sell at
	// the item's own historic rate, assuming you are a modest share of it.
	DaysToUnwind float64 `json:"days_to_unwind"`

	// --- sizing ---
	SuggestedQty int64   `json:"suggested_qty"`
	CapitalISK   float64 `json:"capital_isk"`
	// ExpectedISK is the conservative gain on the suggested quantity: upside
	// per unit after fees, nothing annualised, nothing compounded.
	ExpectedISK float64 `json:"expected_isk"`

	Grade    TodayGrade `json:"grade"`
	Why      string     `json:"why"`
	Blockers []string   `json:"blockers,omitempty"`

	// Score ranks accepted rows. Not ISK: the whole list is speculative with
	// no fixed horizon, so a bigger number here means better-evidenced and
	// cheaper, not sooner.
	Score float64 `json:"score"`
}

// AccumulateSummary is what the header says about the run.
type AccumulateSummary struct {
	RegionID       int32  `json:"region_id"`
	GeneratedAt    string `json:"generated_at"`
	Examined       int    `json:"examined"`
	Accepted       int    `json:"accepted"`
	RejectedThin   int    `json:"rejected_thin"`
	RejectedPrice  int    `json:"rejected_price"`
	RejectedTrend  int    `json:"rejected_trend"`
	RejectedNoData int    `json:"rejected_no_data"`

	TotalCapitalISK  float64 `json:"total_capital_isk"`
	TotalExpectedISK float64 `json:"total_expected_isk"`
}

// AccumulateResult is the whole sweep.
type AccumulateResult struct {
	Summary AccumulateSummary `json:"summary"`
	Rows    []AccumulateRow   `json:"rows"`
	// Rejected carries a bounded sample of what was turned away and why, so
	// the tab can show that the gates are doing something rather than leaving
	// a short list looking like a broken scan.
	Rejected []AccumulateRow `json:"rejected,omitempty"`
	Warnings []string        `json:"warnings,omitempty"`
}

// AccumulateOpts tunes the sweep. Zero values take the documented defaults.
type AccumulateOpts struct {
	RegionID int32

	MaxPercentile  float64
	MinUnitsPerDay float64
	MinISKPerDay   float64
	MinUpsidePct   float64

	// SalesTaxPercent and BrokerFeePercent are charged against the exit, so
	// upside is quoted as what would actually reach the wallet.
	SalesTaxPercent  float64
	BrokerFeePercent float64

	// MaxCapitalPerItemISK caps a single position. Without it the sizing
	// happily suggests putting everything into one cheap item.
	MaxCapitalPerItemISK float64

	// MaxRows bounds the accepted list, and MaxRejectedSample the explanatory
	// tail.
	MaxRows           int
	MaxRejectedSample int

	Now time.Time
}

func (o AccumulateOpts) normalized() AccumulateOpts {
	if o.MaxPercentile <= 0 {
		o.MaxPercentile = accumulateMaxPercentile
	}
	if o.MinUnitsPerDay <= 0 {
		o.MinUnitsPerDay = accumulateMinUnitsPerDay
	}
	if o.MinISKPerDay <= 0 {
		o.MinISKPerDay = accumulateMinISKPerDay
	}
	if o.MinUpsidePct <= 0 {
		o.MinUpsidePct = accumulateMinUpsidePct
	}
	if o.MaxCapitalPerItemISK <= 0 {
		o.MaxCapitalPerItemISK = 100_000_000
	}
	if o.MaxRows <= 0 {
		o.MaxRows = 100
	}
	if o.MaxRejectedSample <= 0 {
		o.MaxRejectedSample = 40
	}
	if o.Now.IsZero() {
		o.Now = time.Now().UTC()
	}
	return o
}

// BuildAccumulate judges candidates and ranks the survivors.
func BuildAccumulate(candidates []AccumulateCandidate, opts AccumulateOpts) AccumulateResult {
	opts = opts.normalized()

	out := AccumulateResult{
		Rows:     []AccumulateRow{},
		Rejected: []AccumulateRow{},
		Summary: AccumulateSummary{
			RegionID:    opts.RegionID,
			GeneratedAt: opts.Now.Format(time.RFC3339),
			Examined:    len(candidates),
		},
	}

	keepRate := 1 - (opts.SalesTaxPercent+opts.BrokerFeePercent)/100
	if keepRate < 0 {
		keepRate = 0
	}

	for _, c := range candidates {
		row, verdict := judgeAccumulate(c, opts, keepRate)
		switch verdict {
		case accumulateAccept:
			out.Rows = append(out.Rows, row)
		case accumulateThin:
			out.Summary.RejectedThin++
			out.Rejected = append(out.Rejected, row)
		case accumulateNotCheap:
			out.Summary.RejectedPrice++
		case accumulateDeclining:
			out.Summary.RejectedTrend++
			out.Rejected = append(out.Rejected, row)
		case accumulateNoData:
			out.Summary.RejectedNoData++
		}
	}

	sort.SliceStable(out.Rows, func(i, j int) bool {
		if out.Rows[i].Score != out.Rows[j].Score {
			return out.Rows[i].Score > out.Rows[j].Score
		}
		return out.Rows[i].TypeID < out.Rows[j].TypeID
	})
	if len(out.Rows) > opts.MaxRows {
		out.Rows = out.Rows[:opts.MaxRows]
	}

	// Rejections are a sample, and the interesting ones are the near misses:
	// biggest discount first, so what shows is "this looked cheap but".
	sort.SliceStable(out.Rejected, func(i, j int) bool {
		return out.Rejected[i].DiscountPct > out.Rejected[j].DiscountPct
	})
	if len(out.Rejected) > opts.MaxRejectedSample {
		out.Rejected = out.Rejected[:opts.MaxRejectedSample]
	}

	for _, r := range out.Rows {
		out.Summary.TotalCapitalISK += r.CapitalISK
		out.Summary.TotalExpectedISK += r.ExpectedISK
	}
	out.Summary.Accepted = len(out.Rows)
	return out
}

type accumulateVerdict int

const (
	accumulateAccept accumulateVerdict = iota
	accumulateNoData
	accumulateThin
	accumulateNotCheap
	accumulateDeclining
)

// judgeAccumulate applies the gates in the order that costs least to decide
// and matters most to get right.
func judgeAccumulate(c AccumulateCandidate, opts AccumulateOpts, keepRate float64) (AccumulateRow, accumulateVerdict) {
	pct := c.Derived.Percentiles
	row := AccumulateRow{
		TypeID:       c.TypeID,
		TypeName:     c.TypeName,
		CategoryID:   c.CategoryID,
		CategoryName: c.CategoryName,
		BestSell:     c.BestSell,
		BestBuy:      c.BestBuy,
	}

	// --- Is there anything to judge against? ---
	if pct.Basis != PercentileBasisHistory {
		row.Blockers = []string{firstNonEmptyStr(pct.Reason, "no usable price history")}
		row.Grade = TodayGradeUnproven
		return row, accumulateNoData
	}
	if c.BestSell <= 0 {
		row.Blockers = []string{"nothing on sale here to buy"}
		row.Grade = TodayGradeUnproven
		return row, accumulateNoData
	}

	row.CurrentPercentile = pct.CurrentPercentile
	row.YearLow, row.YearHigh, row.YearMedian = pct.Min, pct.Max, pct.P50
	row.AvgDailyVolume, row.AvgDailyISK = pct.AvgDailyVolume, pct.AvgDailyISK
	row.RecoveryBasis = c.Derived.Recovery.Basis
	row.RecoveryReason = c.Derived.Recovery.Reason
	row.RecoveryDays = c.Derived.Recovery.MedianDays
	row.RecoveryEpisode = c.Derived.Recovery.Episodes
	row.TrendPctDay = c.Derived.Recovery.TrendPctDay

	if pct.P50 > 0 {
		row.DiscountPct = (pct.P50 - c.BestSell) / pct.P50 * 100
	}

	// --- Liquidity, first, because it is the gate that makes the rest moot.
	if pct.AvgDailyVolume < opts.MinUnitsPerDay || pct.AvgDailyISK < opts.MinISKPerDay {
		row.Grade = TodayGradeAvoid
		row.Blockers = []string{fmt.Sprintf(
			"too thin to exit: %.0f units and %s a day",
			pct.AvgDailyVolume, todayISK(pct.AvgDailyISK))}
		return row, accumulateThin
	}

	// --- Is it actually cheap? ---
	if pct.CurrentPercentile > opts.MaxPercentile {
		return row, accumulateNotCheap
	}
	// A price far under the yearly floor is more likely bad data or a
	// step-change in the item's worth than a gift.
	if pct.Min > 0 && c.BestSell < pct.Min*accumulateMinPriceRatio {
		row.Grade = TodayGradeUnproven
		row.Blockers = []string{"price is far below anything in its history; treat as suspect"}
		return row, accumulateThin
	}

	// --- Exit target and what it is worth, after fees ---
	row.TargetPrice = pct.P50
	netExit := row.TargetPrice * keepRate
	row.UpsideISKPerUnit = netExit - c.BestSell
	if c.BestSell > 0 {
		row.UpsidePct = row.UpsideISKPerUnit / c.BestSell * 100
	}
	if row.UpsidePct < opts.MinUpsidePct {
		return row, accumulateNotCheap
	}

	// --- Has a dip in this item ever come back? ---
	//
	// The gate that separates a bargain from a decline. CalcRecoveryOutlook
	// refuses unless it found several dips that recovered and the trend is not
	// significantly negative, so "none" here means we cannot tell -- and being
	// unable to tell is a reason not to advise buying, not a reason to guess.
	if c.Derived.Recovery.Basis != RecoveryBasisHistory {
		row.Grade = TodayGradeUnproven
		row.Blockers = []string{firstNonEmptyStr(
			c.Derived.Recovery.Reason, "cannot tell a dip from a decline here")}
		return row, accumulateDeclining
	}

	// --- Sizing ---
	//
	// Bounded by what the item actually turns over, not by what you can
	// afford: a week of its own volume is a position the market can absorb
	// without you being the market.
	byVolume := math.Floor(pct.AvgDailyVolume * 7 * 0.25)
	byCapital := math.Floor(opts.MaxCapitalPerItemISK / c.BestSell)
	qty := math.Min(byVolume, byCapital)
	if qty < 1 {
		row.Grade = TodayGradeAvoid
		row.Blockers = []string{"no position size both the market and the capital cap allow"}
		return row, accumulateThin
	}
	row.SuggestedQty = int64(qty)
	row.CapitalISK = qty * c.BestSell
	row.ExpectedISK = qty * row.UpsideISKPerUnit
	if pct.AvgDailyVolume > 0 {
		row.DaysToUnwind = qty / pct.AvgDailyVolume
	}

	// --- Grade and score ---
	//
	// Reuses Today's vocabulary so "proven" means the same thing on both
	// screens: real evidence behind it, not merely arithmetic that worked out.
	switch {
	case c.Derived.Recovery.Episodes >= 5 && pct.TradedDays >= 300:
		row.Grade = TodayGradeProven
	default:
		row.Grade = TodayGradeLikely
	}

	row.Why = accumulateWhy(row, c.Derived.Recovery)
	// Cheapness, evidence and liquidity, each bounded so no single term can
	// dominate. Deliberately not ISK-denominated -- see the field comment.
	row.Score = clampRange(row.DiscountPct, 0, 60)/60*50 +
		clampRange(float64(c.Derived.Recovery.Episodes), 0, 8)/8*30 +
		clampRange(math.Log10(math.Max(pct.AvgDailyISK, 1))-7, 0, 3)/3*20
	return row, accumulateAccept
}

func accumulateWhy(row AccumulateRow, rec RecoveryOutlook) string {
	parts := make([]string, 0, 4)
	parts = append(parts, fmt.Sprintf("%.0f%% under its yearly median, at the %.0fth percentile",
		row.DiscountPct, row.CurrentPercentile))
	if rec.Episodes > 0 {
		parts = append(parts, fmt.Sprintf("%d past dips recovered, typically in %.0f days",
			rec.Episodes, rec.MedianDays))
	}
	if row.DaysToUnwind > 0 {
		parts = append(parts, fmt.Sprintf("%.1f days of its own volume to unwind", row.DaysToUnwind))
	}
	return strings.Join(parts, " · ")
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

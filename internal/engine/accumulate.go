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
	//
	// The units floor was 20/day, which measured against a real sweep sat
	// exactly on the median of the candidate pool and so discarded half of it
	// on a number chosen by guess. It is now a "somebody trades this at all"
	// bar; the ISK floor guards the other failure mode, and position size is
	// separately capped to a fraction of the item's own turnover, which is the
	// honest defence against not being able to exit.
	accumulateMinUnitsPerDay = 5.0
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
	// selling instantly.
	BestSell float64
	BestBuy  float64

	// SellDepthISK is how much sell-side inventory is listed, and OrderCount
	// how many orders are standing on both sides.
	//
	// OrderCount is what candidates get ranked by, not depth. Depth measures
	// what is *listed*, which is a terrible proxy for what changes hands: one
	// officer module sitting at 20B outranks every mineral in the game on
	// depth while trading twice a month. Ranking a real sweep by depth spent a
	// third of its ESI budget on items with too little history to judge. Order
	// count tracks how contested a market is, which is much closer to turnover.
	SellDepthISK float64
	OrderCount   int

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

	// --- the same round trip, priced consistently (see spread.go) ---
	//
	// UpsidePct above pays the ask to get in and is credited a mid to get out,
	// which is nobody's actual round trip. These two describe what placing a
	// buy order and then a sell order is worth instead, and are filled in only
	// when stored order books can supply a real spread. The gate still tests
	// UpsidePct, so a measured spread can inform a decision without quietly
	// lowering the bar for every candidate.
	Spread SpreadProfile `json:"spread"`
	// UpsideMakerPct is meaningless unless UpsideMakerKnown; a zero here means
	// "not measured", not "no upside".
	UpsideMakerPct   float64 `json:"upside_maker_pct,omitempty"`
	UpsideMakerKnown bool    `json:"upside_maker_known,omitempty"`

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
	RegionID    int32  `json:"region_id"`
	GeneratedAt string `json:"generated_at"`
	Examined    int    `json:"examined"`
	Accepted    int    `json:"accepted"`

	// One counter per gate, because a single "rejected" figure hides which
	// filter is actually binding -- which is exactly how a sweep ends up
	// returning one row and looking broken rather than strict.
	RejectedThin       int `json:"rejected_thin"`
	RejectedNotCheap   int `json:"rejected_not_cheap"`
	RejectedThinUpside int `json:"rejected_thin_upside"`
	RejectedDeclining  int `json:"rejected_declining"`
	RejectedNoRecord   int `json:"rejected_no_record"`
	RejectedNoData     int `json:"rejected_no_data"`
	RejectedSuspect    int `json:"rejected_suspect"`

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
			// Not sampled: "this is not cheap" is the overwhelmingly common
			// outcome and says nothing interesting about the item.
			out.Summary.RejectedNotCheap++
		case accumulateThinUpside:
			out.Summary.RejectedThinUpside++
			out.Rejected = append(out.Rejected, row)
		case accumulateDeclining:
			out.Summary.RejectedDeclining++
			out.Rejected = append(out.Rejected, row)
		case accumulateNoRecord:
			out.Summary.RejectedNoRecord++
			out.Rejected = append(out.Rejected, row)
		case accumulateSuspect:
			out.Summary.RejectedSuspect++
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
	accumulateThinUpside
	accumulateDeclining
	accumulateNoRecord
	accumulateSuspect
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
	// Reversion, not Recovery. Recovery bundles its own "is today a dip"
	// test, which is a second and different definition of cheap from the
	// percentile one applied below; requiring both is what made this sweep
	// reject 427 of 954 items that were genuinely in the bottom quarter of
	// their year. See mean_reversion.go.
	rev := c.Derived.Reversion
	row.RecoveryBasis = rev.Basis
	row.RecoveryReason = rev.Reason
	row.RecoveryDays = rev.MedianDays
	row.RecoveryEpisode = rev.Episodes
	row.TrendPctDay = rev.TrendPctDay

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
		return row, accumulateSuspect
	}

	// --- Exit target and what it is worth, after fees ---
	row.TargetPrice = pct.P50
	netExit := row.TargetPrice * keepRate
	row.UpsideISKPerUnit = netExit - c.BestSell
	if c.BestSell > 0 {
		row.UpsidePct = row.UpsideISKPerUnit / c.BestSell * 100
	}
	if row.UpsidePct < opts.MinUpsidePct {
		row.Blockers = []string{fmt.Sprintf(
			"only %.0f%% upside to its median after fees", row.UpsidePct)}
		return row, accumulateThinUpside
	}

	// --- Is this the kind of item that comes back? ---
	//
	// Two separate failures, reported separately, because they mean opposite
	// things to a buyer: a falling trend says do not buy this at any price,
	// while no track record says we cannot tell and you are on your own.
	if rev.Basis != RecoveryBasisHistory {
		row.Grade = TodayGradeUnproven
		row.Blockers = []string{firstNonEmptyStr(rev.Reason, "no usable price series")}
		return row, accumulateNoData
	}
	if rev.Declining {
		row.Grade = TodayGradeAvoid
		row.Blockers = []string{fmt.Sprintf(
			"the trend itself is falling (%.2f%%/day); this is a decline, not a dip", rev.TrendPctDay)}
		return row, accumulateDeclining
	}
	if rev.Episodes < recoveryMinEpisodes {
		row.Grade = TodayGradeUnproven
		row.Blockers = []string{"no track record of dips in this item recovering"}
		return row, accumulateNoRecord
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
	case rev.Episodes >= 5 && pct.TradedDays >= 300:
		row.Grade = TodayGradeProven
	default:
		row.Grade = TodayGradeLikely
	}

	row.Why = accumulateWhy(row, rev)
	// Cheapness, evidence and liquidity, each bounded so no single term can
	// dominate. Deliberately not ISK-denominated -- see the field comment.
	row.Score = clampRange(row.DiscountPct, 0, 60)/60*50 +
		clampRange(float64(rev.Episodes), 0, 8)/8*30 +
		clampRange(math.Log10(math.Max(pct.AvgDailyISK, 1))-7, 0, 3)/3*20
	return row, accumulateAccept
}

func accumulateWhy(row AccumulateRow, rec MeanReversionProfile) string {
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

package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// today_risk.go — how much an action can be trusted, and how big it is
// allowed to be.
//
// The Today queue ranks on the *downside* of an action, not its headline.
// That alone is not enough: a tight forecast band around a number the
// scanner has been consistently wrong about is still a bad instruction. So
// every action is also graded against evidence, and only `proven` and
// `likely` reach the run queue. The rest are listed separately with their
// blockers named, because a filtered row the user cannot see is
// indistinguishable from a bug.
//
// Evidence is used strongest-first:
//
//  1. Realized P&L for the item from the FIFO trade journal. Real ESI
//     transactions, so it needs nothing from the user and covers the most
//     ground. An item that has lost money over a real sample is a blocker.
//  2. Trading Edge — richer (win rate, reality ratio, size caps) but built
//     from paper trades, so it is present-or-absent and never assumed.
//  3. The forecast spread the station scanner already derives from
//     confidence, CI, SDS, DOS, PVI and history availability.
//  4. Data completeness. Missing inputs make a profit claim *unknown*, and
//     unknown is not zero — those rows are `unproven`, never a number the
//     queue asserts.
//  5. Portfolio risk, so the queue does not advise piling into the item
//     that is already most of the book.

// Minimum real closed sales before the journal is allowed to condemn an
// item outright. Below this a losing streak is noise, and the row is only
// held back to `unproven`.
const todayMinRealSample = 5

// A sample at or above this is treated as settled evidence rather than a
// hint.
const todayStrongSample = 20

// Grade thresholds on the 0-100 reliability score.
const (
	todayScoreProven = 70.0
	todayScoreLikely = 45.0
)

// No single action may try to be more than this share of an item's daily
// volume. Sizing past it moves the price against you, which is the one
// risk a spread calculation never shows.
const todayMaxDailyVolumeShare = 0.25

// realityRatio scales a projection down but never up: the point is to
// discount an optimistic model, and letting it inflate would reintroduce
// exactly the over-promising it exists to catch. Floored so one bad month
// cannot empty the queue.
const (
	todayRealityFloor = 0.50
	todayRealityCeil  = 1.00
)

// TodayGrade is how far an action should be trusted.
type TodayGrade string

const (
	// TodayGradeProven — real history says this works, or the ISK already
	// exists and only needs collecting.
	TodayGradeProven TodayGrade = "proven"
	// TodayGradeLikely — the model is sound and nothing contradicts it,
	// but there is not much personal history behind it.
	TodayGradeLikely TodayGrade = "likely"
	// TodayGradeUnproven — an input is missing, so the profit claim is
	// unknown. Never presented as an instruction.
	TodayGradeUnproven TodayGrade = "unproven"
	// TodayGradeAvoid — evidence actively says no.
	TodayGradeAvoid TodayGrade = "avoid"
)

// Advised reports whether a grade is allowed into the run queue.
func (g TodayGrade) Advised() bool {
	return g == TodayGradeProven || g == TodayGradeLikely
}

// weight discounts a projection by how much the grade can be trusted.
// Applied on top of the already-conservative downside figure, so a
// `likely` row must clear a real margin before it can outrank a `proven`
// one.
func (g TodayGrade) weight() float64 {
	switch g {
	case TodayGradeProven:
		return 1.0
	case TodayGradeLikely:
		return 0.8
	case TodayGradeUnproven:
		return 0.4
	default:
		return 0
	}
}

// TodayReliability is the evidence behind a grade, carried to the UI so a
// chip can be hovered rather than believed.
type TodayReliability struct {
	Grade TodayGrade `json:"grade"`
	Score float64    `json:"score"`

	// Personal history. RealityRatio is realized-over-expected from
	// Trading Edge; 0 means no personal record exists, which is different
	// from a record of 0.
	RealityRatio float64 `json:"reality_ratio,omitempty"`
	SampleTrades int     `json:"sample_trades"`
	RealizedISK  float64 `json:"realized_isk"`
	WinRatePct   float64 `json:"win_rate_pct,omitempty"`

	// Caps names the bound that decided the quantity, so the number is
	// explained rather than asserted.
	Caps []string `json:"caps,omitempty"`
	// Blockers is why this is not advised. Non-empty exactly when the
	// grade is avoid or unproven.
	Blockers []string `json:"blockers,omitempty"`
	// Evidence is the one-line summary the row renders.
	Evidence string `json:"evidence"`
}

// TodayItemHistory is realized P&L for one type out of the FIFO trade
// journal — the `journal/by-type` row, narrowed to what grading needs.
type TodayItemHistory struct {
	TypeID      int32
	SellsQty    int64
	BuysQty     int64
	RealizedISK float64
}

// TodayEdge is the Trading Edge verdict for one type.
type TodayEdge struct {
	LabelCode         string
	RealityRatio      float64
	WinRatePct        float64
	SampleTrades      int
	MaxRecommendedQty int64
	MaxExposureISK    float64
	Advice            string
}

// TodayPositionRisk is the portfolio view of one type — concentration and
// liquidity, which a per-item spread knows nothing about.
type TodayPositionRisk struct {
	RiskLevel       string
	ExposurePct     float64
	DaysToLiquidate float64
	SuggestedBuyISK float64
	MaxCapitalISK   float64
	Action          string
}

// todayEvidence is everything grading looks at for one action. Assembled
// by the caller so the grader itself stays a pure function of its inputs.
type todayEvidence struct {
	kind TodayActionKind

	// certain marks an action whose ISK does not depend on a market
	// prediction at all — collecting finished jobs, cancelling a dead
	// order, restarting a stalled extractor. These are graded on whether
	// their inputs are present, not on how an item has traded.
	certain       bool
	certainReason string

	history *TodayItemHistory
	edge    *TodayEdge
	risk    *TodayPositionRisk

	// expected and downside are the P50 and P80 views of the same figure.
	// Their ratio is how tight the forecast is.
	expected float64
	downside float64

	// Data-completeness gates. Any of these means an input the profit
	// claim depends on was not available.
	unknownMargin   bool // OrderDeskOrder.MarginBasis == "none"
	noBook          bool // the order book could not be read
	noFlow          bool // no volume estimate, so no ETA and no fill
	noHistory       bool // no price history for the type
	pricingFailed   bool // the hub price lookup failed
	thinMargin      bool // positive but under the configured floor
	unprofitableFee bool // the broker fee eats the whole relist gain
}

// gradeTodayAction turns evidence into a grade. Order matters: blockers
// are checked before anything can score its way past them.
func gradeTodayAction(ev todayEvidence) TodayReliability {
	rel := TodayReliability{}
	if ev.history != nil {
		rel.RealizedISK = ev.history.RealizedISK
		rel.SampleTrades = int(ev.history.SellsQty)
	}
	if ev.edge != nil {
		rel.RealityRatio = ev.edge.RealityRatio
		rel.WinRatePct = ev.edge.WinRatePct
		if ev.edge.SampleTrades > rel.SampleTrades {
			rel.SampleTrades = ev.edge.SampleTrades
		}
	}

	// --- Blockers. Evidence that actively says no. ---
	var blockers []string
	if ev.unprofitableFee {
		blockers = append(blockers, "the broker fee is larger than the gain from repricing")
	}
	if ev.edge != nil && ev.edge.LabelCode == "do_not_trade" {
		blockers = append(blockers, "your own record on this item is bad: "+strings.TrimSpace(ev.edge.Advice))
	}
	if ev.history != nil && ev.history.SellsQty >= todayMinRealSample && ev.history.RealizedISK < 0 {
		blockers = append(blockers, fmt.Sprintf(
			"your last %d sales of this lost %s", ev.history.SellsQty, todayISK(-ev.history.RealizedISK)))
	}
	if ev.risk != nil && (ev.risk.Action == "liquidate" || ev.risk.Action == "pause_buy") && ev.kind == TodayActionBuy {
		blockers = append(blockers, "portfolio risk says stop adding to this position")
	}
	// A downside that is not positive is not an opportunity, whatever the
	// median says. Certain actions are exempt: a cancel or a delivery is
	// worth doing without a forecast behind it.
	if !ev.certain && ev.downside <= 0 && ev.expected > 0 {
		blockers = append(blockers, "the realistic case for this is break-even or worse")
	}
	if len(blockers) > 0 {
		rel.Grade = TodayGradeAvoid
		rel.Blockers = blockers
		rel.Score = 0
		rel.Evidence = blockers[0]
		return rel
	}

	// --- Unknowns. Missing inputs mean the claim cannot be checked. ---
	var unknowns []string
	if ev.unknownMargin {
		unknowns = append(unknowns, "no cost basis for this stock, so the profit is unknown")
	}
	if ev.noBook {
		unknowns = append(unknowns, "the order book could not be read")
	}
	if ev.noFlow {
		unknowns = append(unknowns, "no volume estimate, so there is no telling when it fills")
	}
	if ev.noHistory {
		unknowns = append(unknowns, "no price history for this item")
	}
	if ev.pricingFailed {
		unknowns = append(unknowns, "the current price lookup failed")
	}
	if len(unknowns) > 0 {
		rel.Grade = TodayGradeUnproven
		rel.Blockers = unknowns
		rel.Score = 0
		rel.Evidence = unknowns[0]
		return rel
	}

	// --- Certain actions. The ISK exists; only the inputs were at risk. ---
	if ev.certain {
		rel.Grade = TodayGradeProven
		rel.Score = 100
		rel.Evidence = ev.certainReason
		return rel
	}

	// --- Score. ---
	score := 50.0

	if ev.history != nil && ev.history.SellsQty > 0 {
		switch {
		case ev.history.RealizedISK > 0 && ev.history.SellsQty >= todayStrongSample:
			score += 30
		case ev.history.RealizedISK > 0 && ev.history.SellsQty >= todayMinRealSample:
			score += 18
		case ev.history.RealizedISK > 0:
			score += 8
		default:
			// A sample too small to condemn the item outright still is not
			// worth nothing. Reaching here means the losing streak was
			// under todayMinRealSample, so the blocker above did not fire —
			// but treating that the same as no record at all is how a row
			// you have only ever lost on keeps being recommended.
			score -= 25
		}
	}

	if ev.edge != nil {
		switch ev.edge.LabelCode {
		case "good_edge":
			score += 20
		case "watch":
			score += 5
		case "needs_bigger_margin":
			score -= 10
		}
		if ev.edge.RealityRatio > 0 {
			score += clampRange((ev.edge.RealityRatio-1)*20, -15, 15)
		}
	}

	// Forecast tightness: downside/expected near 1 is a narrow band, near
	// 0 is a guess wearing a number.
	if ev.expected > 0 {
		tight := clampRange(ev.downside/ev.expected, 0, 1)
		score += clampRange(tight*30-15, -15, 15)
	}

	if ev.thinMargin {
		score -= 20
	}

	if ev.risk != nil {
		switch strings.ToLower(ev.risk.RiskLevel) {
		case "high":
			score -= 15
		case "critical", "extreme", "severe":
			score -= 25
		}
	}

	rel.Score = clampRange(score, 0, 100)
	switch {
	case rel.Score >= todayScoreProven:
		rel.Grade = TodayGradeProven
	case rel.Score >= todayScoreLikely:
		rel.Grade = TodayGradeLikely
	default:
		rel.Grade = TodayGradeUnproven
		rel.Blockers = []string{"not enough evidence that this one works"}
	}
	rel.Evidence = todayEvidenceLine(ev, rel)
	return rel
}

// todayEvidenceLine renders the one sentence the row shows under the
// numbers. It names what the grade rests on rather than restating it.
func todayEvidenceLine(ev todayEvidence, rel TodayReliability) string {
	parts := make([]string, 0, 3)
	if ev.history != nil && ev.history.SellsQty > 0 {
		verb := "made"
		amount := ev.history.RealizedISK
		if amount < 0 {
			verb = "lost"
			amount = -amount
		}
		parts = append(parts, fmt.Sprintf("%d real sales of this have %s you %s",
			ev.history.SellsQty, verb, todayISK(amount)))
	}
	if ev.edge != nil && ev.edge.SampleTrades > 0 {
		if ev.edge.WinRatePct > 0 {
			parts = append(parts, fmt.Sprintf("%.0f%% of your logged trades profitable", ev.edge.WinRatePct))
		}
		if ev.edge.RealityRatio > 0 {
			parts = append(parts, fmt.Sprintf("realized %.2fx of plan", ev.edge.RealityRatio))
		}
	}
	if len(parts) == 0 {
		if ev.expected > 0 && ev.downside > 0 {
			return fmt.Sprintf("no personal record yet; forecast holds %s of %s in the realistic case",
				todayISK(ev.downside), todayISK(ev.expected))
		}
		return "no personal record yet; graded on the forecast alone"
	}
	return strings.Join(parts, ", ")
}

// realityDiscount is the extra haircut applied to a projection from the
// user's own realized-over-expected ratio. Only ever <= 1.
func realityDiscount(edge *TodayEdge) float64 {
	if edge == nil || edge.RealityRatio <= 0 {
		return 1
	}
	return clampRange(edge.RealityRatio, todayRealityFloor, todayRealityCeil)
}

// todayQuantityCap is the outcome of sizing an action: how many units, at
// what capital, and which bound decided it.
type todayQuantityCap struct {
	Quantity int64
	Capital  float64
	Caps     []string
}

// todayQuantityLimits are the ceilings a buy is sized against. Zero means
// "no limit from this source", which is why each is checked before use —
// a missing cap must not silently become a cap of zero.
type todayQuantityLimits struct {
	FlowPerDay       float64 // one day of flippable flow — the starting point
	AvgDailyVolume   float64 // for the market-depth share cap
	UnitPrice        float64
	FreeWalletISK    float64
	MaxInvestmentISK float64
	Edge             *TodayEdge
	Risk             *TodayPositionRisk
}

// capTodayQuantity sizes a buy against every ceiling that applies and
// reports which one bound it. Starting from a day of flow rather than from
// available capital is deliberate: the question is how much the market can
// absorb, and only then whether the ISK is there.
func capTodayQuantity(lim todayQuantityLimits) todayQuantityCap {
	out := todayQuantityCap{}
	if lim.UnitPrice <= 0 {
		return out
	}

	type bound struct {
		qty   float64
		label string
	}
	bounds := make([]bound, 0, 6)

	base := math.Floor(lim.FlowPerDay)
	if base < 1 {
		base = 1
	}
	bounds = append(bounds, bound{base, "one day of flow"})

	if lim.AvgDailyVolume > 0 {
		bounds = append(bounds, bound{
			math.Floor(lim.AvgDailyVolume * todayMaxDailyVolumeShare), "market depth"})
	}
	if lim.MaxInvestmentISK > 0 {
		bounds = append(bounds, bound{
			math.Floor(lim.MaxInvestmentISK / lim.UnitPrice), "your max investment setting"})
	}
	if lim.FreeWalletISK > 0 {
		bounds = append(bounds, bound{
			math.Floor(lim.FreeWalletISK / lim.UnitPrice), "free ISK in your wallet"})
	}
	if lim.Edge != nil {
		if lim.Edge.MaxRecommendedQty > 0 {
			bounds = append(bounds, bound{
				float64(lim.Edge.MaxRecommendedQty), "the size your own trades have worked at"})
		}
		if lim.Edge.MaxExposureISK > 0 {
			bounds = append(bounds, bound{
				math.Floor(lim.Edge.MaxExposureISK / lim.UnitPrice), "your usual exposure on this item"})
		}
	}
	if lim.Risk != nil && lim.Risk.SuggestedBuyISK > 0 {
		bounds = append(bounds, bound{
			math.Floor(lim.Risk.SuggestedBuyISK / lim.UnitPrice), "portfolio concentration"})
	}

	best := math.Inf(1)
	for _, b := range bounds {
		if b.qty < best {
			best = b.qty
		}
	}
	if math.IsInf(best, 1) || best < 1 {
		// Every bound came back below one unit. That is a real answer —
		// there is no size at which this is worth doing — not a rounding
		// artefact to be clamped up to 1.
		if best < 1 && !math.IsInf(best, 1) {
			for _, b := range bounds {
				if b.qty == best {
					out.Caps = append(out.Caps, b.label)
				}
			}
			sort.Strings(out.Caps)
		}
		return out
	}

	// Name every bound sitting at the minimum, not just the first found,
	// so a tie reads honestly.
	for _, b := range bounds {
		if b.qty == best {
			out.Caps = append(out.Caps, b.label)
		}
	}
	sort.Strings(out.Caps)

	out.Quantity = int64(best)
	out.Capital = float64(out.Quantity) * lim.UnitPrice
	return out
}

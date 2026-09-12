package engine

import (
	"fmt"
	"sort"
	"time"
)

// spread.go — what the bid-ask gap costs an accumulate round trip.
//
// Accumulate's arithmetic mixes two price conventions, and that mixing is the
// defect this file exists to measure.
//
// Entry is `BestSell`, today's lowest ask: the price of buying by taking
// somebody's sell order. Exit is the yearly median of ESI's daily *average*
// traded price, which sits near mid-market because executed trades land on both
// sides of the book. So the model charges an ask on the way in and credits a
// mid on the way out, and the difference between those two conventions is one
// half-spread that appears nowhere in the upside figure.
//
// It is not a rounding error. On a wide book the spread is the trade, and it
// runs the wrong way for the strategy: what the scan calls a 12% recovery can be
// several points thinner once both legs are priced the same way. Worse, nobody
// accumulating actually enters by taking an ask — you place a buy order and fill
// near the bid, which is *cheaper* than the model assumes. Both legs are
// mis-stated, in opposite directions, and neither error is visible.
//
// ESI cannot fix this. Its history endpoint reports what executed, never what
// was quoted, so a bid and an ask simply do not exist in it. The Fuzzwork
// order-book archive is the one source that carries both sides at the same
// instant, going back years. This is the question that data answers well, and
// close to the only one Accumulate needs it for.
//
// What this does NOT do is move the gate. A spread measurement makes the maker
// round trip look better than the current figure, and quietly raising every
// upside number on the strength of a new input is how a scan starts
// recommending things it should not. The measured spread is reported, a
// same-convention upside is reported beside the existing one, and the floor
// keeps testing the conservative number.

const (
	// A relative spread beyond this is treated as unusable rather than real.
	// Genuine books get wide, but 100%+ almost always means one side of the
	// snapshot was nearly empty -- a single stale order facing nothing -- and
	// a median taken over those describes the archive's gaps, not the market.
	spreadMaxRelPct = 100.0

	// Below this many paired snapshots the median is noise. Roughly two weeks
	// of daily captures, or a couple of months of the weekly tail.
	spreadMinSamples = 10
)

// SpreadProfile is an item's typical bid-ask gap, measured from stored books.
//
// Percentages are relative to mid-market, so they compare across items of any
// price: a 2% spread means the same thing on a 3 ISK mineral and a 3B ship.
type SpreadProfile struct {
	// Basis is "orderbook" when the numbers mean something, "none" otherwise.
	// A caller must branch on this rather than treating a zero spread as
	// "tight" -- absent evidence and a tight book are opposite situations.
	Basis  string `json:"basis"`
	Reason string `json:"reason,omitempty"`

	// Samples is how many instants had both a bid and an ask to compare.
	Samples int `json:"samples"`

	// MedianPct is the typical relative spread; P75Pct the wide end, for the
	// conservative read.
	MedianPct float64 `json:"median_pct"`
	P75Pct    float64 `json:"p75_pct"`

	// OldestAt / NewestAt bound what was measured, so a profile built from a
	// three-day window cannot be mistaken for a year of evidence.
	OldestAt string `json:"oldest_at,omitempty"`
	NewestAt string `json:"newest_at,omitempty"`
}

// Measured reports whether the profile carries usable evidence.
func (s SpreadProfile) Measured() bool { return s.Basis == "orderbook" && s.Samples > 0 }

// HalfPct is what one side of the book costs, which is the figure that applies
// when only one leg of a round trip crosses.
func (s SpreadProfile) HalfPct() float64 {
	if !s.Measured() {
		return 0
	}
	return s.MedianPct / 2
}

// CalcSpreadProfile pairs each bid book with the ask book captured nearest to
// it and summarises the gap.
//
// Pairing rather than averaging the two sides independently: a spread is only
// meaningful between quotes that existed at the same moment. An archived
// snapshot carries both sides at one instant, so those pair at zero age;
// separately recorded books need the tolerance.
func CalcSpreadProfile(bids, asks []OrderBookReplayBook, maxAge time.Duration) SpreadProfile {
	out := SpreadProfile{Basis: "none"}

	bids = normalizeReplayBooks(bids, "buy")
	asks = normalizeReplayBooks(asks, "sell")
	if len(bids) == 0 || len(asks) == 0 {
		out.Reason = "no stored order books for this item"
		return out
	}

	rels := make([]float64, 0, len(bids))
	var oldest, newest time.Time
	discarded := 0

	for _, bidBook := range bids {
		askBook, ok := nearestReplayBook(bidBook.CapturedAt, asks, maxAge)
		if !ok {
			continue
		}
		bestBid := bidBook.Levels[0].Price
		bestAsk := askBook.Levels[0].Price
		if bestBid <= 0 || bestAsk <= 0 {
			continue
		}
		// A crossed book is not a negative spread, it is a bad snapshot: the
		// two sides were captured far enough apart that the market moved
		// through them, or one side is stale. Either way it cannot be averaged
		// in.
		if bestAsk <= bestBid {
			discarded++
			continue
		}
		mid := (bestAsk + bestBid) / 2
		if mid <= 0 {
			continue
		}
		rel := (bestAsk - bestBid) / mid * 100
		if rel > spreadMaxRelPct {
			discarded++
			continue
		}
		rels = append(rels, rel)
		if oldest.IsZero() || bidBook.CapturedAt.Before(oldest) {
			oldest = bidBook.CapturedAt
		}
		if bidBook.CapturedAt.After(newest) {
			newest = bidBook.CapturedAt
		}
	}

	out.Samples = len(rels)
	if len(rels) < spreadMinSamples {
		out.Reason = fmt.Sprintf(
			"only %d paired snapshots (need %d); %d discarded as crossed or absurd",
			len(rels), spreadMinSamples, discarded)
		return out
	}

	sort.Float64s(rels)
	out.Basis = "orderbook"
	out.MedianPct = sanitizeFloat(percentileOfSorted(rels, 50))
	out.P75Pct = sanitizeFloat(percentileOfSorted(rels, 75))
	out.OldestAt = oldest.UTC().Format(time.RFC3339)
	out.NewestAt = newest.UTC().Format(time.RFC3339)
	return out
}

// AccumulateMakerUpside re-prices an accumulate round trip so both legs use the
// same convention, which the headline figure does not.
//
// The headline pays the ask to get in and is credited a mid to get out. Anyone
// actually accumulating places a buy order and fills near the bid, then posts a
// sell order and is paid near the ask — both legs on the maker side, which is
// the same round trip the station backtest models.
//
// Returns the upside percent and whether it could be computed at all. Falls
// back to nothing rather than to the headline: a caller showing "the same
// number twice" has been told something, where a silent fallback would look
// like agreement between two independent estimates.
func AccumulateMakerUpside(
	bestBuy, target, keepRate float64,
	spread SpreadProfile,
) (float64, bool) {
	if !spread.Measured() || bestBuy <= 0 || target <= 0 || keepRate <= 0 {
		return 0, false
	}
	// Entry: a buy order fills at the bid, which is what BestBuy already is.
	entry := bestBuy
	// Exit: the target is a mid-level statistic (a median of daily average
	// traded prices), and a resting sell order is paid the ask, one half-spread
	// above mid.
	exit := target * (1 + spread.HalfPct()/100) * keepRate
	if entry <= 0 {
		return 0, false
	}
	return sanitizeFloat((exit - entry) / entry * 100), true
}

// AccumulateKeepRate is the fraction of an exit that survives fees.
//
// Exported so the spread enrichment below and BuildAccumulate cannot drift to
// two different rates for the same trade.
func AccumulateKeepRate(opts AccumulateOpts) float64 {
	keep := 1 - (opts.SalesTaxPercent+opts.BrokerFeePercent)/100
	if keep < 0 {
		return 0
	}
	return keep
}

// EnrichAccumulateSpreads prices each surviving row's round trip consistently,
// using whatever stored order books the lookup can supply.
//
// Deliberately a second pass over the *result* rather than an input to the
// gates. Two reasons. It keeps the floor testing the conservative,
// history-only figure, so adding this data source cannot by itself make the
// scan recommend more things. And it bounds the cost: a sweep considers
// thousands of candidates but returns tens, and a book lookup per candidate
// would be thousands of queries against a single-connection database.
func EnrichAccumulateSpreads(
	res *AccumulateResult,
	opts AccumulateOpts,
	lookup func(typeID int32) SpreadProfile,
) {
	if res == nil || lookup == nil {
		return
	}
	keep := AccumulateKeepRate(opts)
	for i := range res.Rows {
		row := &res.Rows[i]
		row.Spread = lookup(row.TypeID)
		if !row.Spread.Measured() {
			continue
		}
		pct, ok := AccumulateMakerUpside(row.BestBuy, row.TargetPrice, keep, row.Spread)
		if !ok {
			continue
		}
		row.UpsideMakerPct = pct
		row.UpsideMakerKnown = true
	}
}
